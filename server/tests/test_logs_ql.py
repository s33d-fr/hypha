import json

import pytest

from hypha.errors import QueryError
from hypha.query import logs_ql as L

from conftest import placeholders


def sql(q, resolver=None):
    frag, params = L.compile_query(q, resolver)
    assert placeholders(frag) == len(params), (frag, params)
    return frag, params


def test_empty_query():
    assert L.compile_query("") == ("TRUE", [])
    assert L.compile_query(None) == ("TRUE", [])


def test_free_words_and_fields():
    frag, params = sql('connexion "refusée par" service:nginx status:ERR host:web-01 source:file')
    assert frag.count(" AND ") >= 4
    # Mots et phrases : la ligne contient le texte, sans casse (contrat § 11), forme servie par
    # l'index trigramme : valeur en minuscules, entre %, échappée.
    assert frag.count("lower(message) LIKE %s ESCAPE '\\'") == 2 and "tsquery" not in frag
    assert params == ["%connexion%", "%refusée par%", "nginx", "error", "web-01", "file"]
    assert "service = %s" in frag and "status = %s" in frag and "hostname = %s" in frag


def test_or_binds_tighter_than_implicit_and():
    tree = L.parse("status:error OR status:critical service:nginx")
    assert isinstance(tree, L.And)
    assert isinstance(tree.nodes[0], L.Or) and isinstance(tree.nodes[1], L.Term)


def test_parentheses_and_negation():
    tree = L.parse('-(service:cron OR service:backup) -"bruit connu" timeout')
    assert isinstance(tree, L.And) and len(tree.nodes) == 3
    assert isinstance(tree.nodes[0], L.Not) and isinstance(tree.nodes[0].node, L.Or)
    assert isinstance(tree.nodes[1], L.Not) and tree.nodes[1].node.phrase
    frag, _ = sql('-(service:cron OR service:backup) -"bruit connu" timeout')
    assert frag.count("NOT COALESCE(") == 2


def test_attributes_nested_flat_and_typed():
    frag, params = sql("@http.status_code:502")
    docs = [json.loads(p) for p in params]
    assert {"http": {"status_code": "502"}} in docs and {"http": {"status_code": 502}} in docs
    assert {"http.status_code": 502} in docs and {"http.status_code": "502"} in docs
    assert frag.count("attributes @> %s::jsonb") == 4
    frag, params = sql("@user.name:jean*")
    assert params == [["user", "name"], "jean%", "user.name", "jean%"]
    frag, params = sql("@trace:*")
    assert "IS NOT NULL" in frag


def test_wildcards_and_tags():
    frag, params = sql("service:ngi* env:prod team:back*")
    assert "service LIKE %s" in frag and params[0] == "ngi%"
    assert json.loads(params[1]) == {"env": "prod"}
    assert params[2:] == ["team", "back%"]
    frag, params = sql("Conn*Refused")
    assert "lower(message) LIKE %s ESCAPE '\\'" in frag and params == ["%conn%refused%"]


def test_values_never_in_sql():
    nasty = "x'); DROP TABLE logs; --"
    frag, params = sql(f'"{nasty}" service:"{nasty}" @a.b:"{nasty}"')
    assert "DROP" not in frag
    assert nasty in params


def test_symbols_only_word_uses_substring():
    frag, params = sql("=>")
    assert frag == "lower(message) LIKE %s ESCAPE '\\'" and params == ["%=>%"]
    # « - » collé à un terme l'exclut, même devant un symbole.
    frag, _ = sql("->")
    assert frag.startswith("NOT COALESCE(")


def test_like_escaping_of_free_text():
    # %, _ et \ saisis gardent leur sens littéral ; la valeur part en minuscules.
    assert sql('"50%_OK"')[1] == ["%50\\%\\_ok%"]
    assert sql('"C:\\\\Temp"')[1] == ["%c:\\\\temp%"]
    assert sql("a_b*c%")[1] == ["%a\\_b%c\\%%"]


def test_terms_stay_indexable():
    # Hors négation, un terme reste nu : `lower(message) LIKE …` et `service = …` sont des
    # conditions que le planificateur sert par l'index de chaque partition.
    frag, _ = sql("service:nginx timeout")
    assert frag == "(service = %s AND lower(message) LIKE %s ESCAPE '\\')"
    frag, _ = sql("timeout -service:cron")
    assert frag == "(lower(message) LIKE %s ESCAPE '\\' AND NOT COALESCE(service = %s, false))"


@pytest.mark.parametrize("bad", ['"ouvert', "(a b", "a b)", "OR a", "a OR", "service:", "@bad!:x",
                                 "()", "a OR OR b"])
def test_errors(bad):
    with pytest.raises(QueryError):
        L.compile_query(bad)


def test_unknown_status_kept_as_typed():
    _, params = sql("status:bizarre")
    assert params == ["bizarre"]


def test_events_resolver():
    frag, params = sql("déploiement source:deploy alert_type:error host:web-01 env:prod", L.EventsResolver())
    assert "e.title ILIKE %s" in frag and "e.source = %s" in frag and "h.hostname = %s" in frag
    assert "e.tags @> %s::jsonb" in frag
    assert params[:2] == ["%déploiement%", "%déploiement%"]
    with pytest.raises(QueryError):
        L.compile_query("@a:b", L.EventsResolver())
