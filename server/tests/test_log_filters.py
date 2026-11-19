"""Filtres d'exclusion (contrat § 11) : parité entre le prédicat Python et la traduction SQL.

La comparaison se fait sur l'arbre analysé : pour chaque feuille, le SQL réellement émis par
logs_ql.LogsResolver est évalué par un interpréteur de référence écrit ici, indépendant du code
du serveur (LIKE réécrit à la main, inclusion jsonb, logique à trois valeurs et COALESCE) ; les
nœuds ET / OU / NON se combinent comme en SQL. Le prédicat de logs_match doit donner la même
réponse sur chaque ligne.
"""
import json
from datetime import datetime, timezone

import pytest

from hypha.errors import QueryError
from hypha.ingest import body as B
from hypha.ingest import log_filters as F
from hypha.query import logs_match as M
from hypha.query import logs_ql as L

# ── interpréteur de référence du SQL émis ──────────────────────────────────────

_ABSENT = object()


def sql_like(s, pattern):
    """LIKE de PostgreSQL, échappement par barre oblique inverse ; NULL si s est NULL."""
    if s is None:
        return None
    tokens, i = [], 0
    while i < len(pattern):
        c = pattern[i]
        if c == "\\" and i + 1 < len(pattern):
            tokens.append(("lit", pattern[i + 1]))
            i += 2
            continue
        tokens.append(("any",) if c == "%" else ("one",) if c == "_" else ("lit", c))
        i += 1
    memo = {}

    def match(ti, si):
        key = (ti, si)
        if key in memo:
            return memo[key]
        if ti == len(tokens):
            res = si == len(s)
        elif tokens[ti][0] == "any":
            res = any(match(ti + 1, k) for k in range(si, len(s) + 1))
        elif si < len(s) and (tokens[ti][0] == "one" or tokens[ti][1] == s[si]):
            res = match(ti + 1, si + 1)
        else:
            res = False
        memo[key] = res
        return res

    return match(0, 0)


def ref_eq(a, b):
    if isinstance(a, bool) or isinstance(b, bool):
        return type(a) is bool and type(b) is bool and a == b
    if isinstance(a, (int, float)) and isinstance(b, (int, float)):
        return a == b
    return type(a) is type(b) and a == b


def ref_contains(a, b):
    if isinstance(b, dict):
        return isinstance(a, dict) and all(k in a and ref_contains(a[k], v) for k, v in b.items())
    if isinstance(a, (dict, list)):
        return False
    return ref_eq(a, b)


def ref_path(obj, path):
    for seg in path:
        if isinstance(obj, dict) and seg in obj:
            obj = obj[seg]
        elif isinstance(obj, list) and seg.lstrip("-").isdigit() and -len(obj) <= int(seg) < len(obj):
            obj = obj[int(seg)]
        else:
            return _ABSENT
    return obj


def ref_text(v):
    if v is _ABSENT or v is None:
        return None
    if isinstance(v, str):
        return v
    if isinstance(v, bool):
        return "true" if v else "false"
    if isinstance(v, dict):
        return "{" + ", ".join(f"{json.dumps(k)}: {ref_text(v[k]) if not isinstance(v[k], str) else json.dumps(v[k])}"
                               for k in sorted(v, key=lambda k: (len(k.encode()), k.encode()))) + "}"
    if isinstance(v, list):
        return "[" + ", ".join(json.dumps(x) for x in v) + "]"
    return str(v)


def sql_or(*vals):
    if any(v is True for v in vals):
        return True
    return None if any(v is None for v in vals) else False


COLUMNS = ("service", "status", "hostname", "source", "trace_id", "span_id")


def ref_leaf(cond: str, p: list, row: dict):
    msg, attrs, tags = row.get("message"), row.get("attributes") or {}, row.get("tags") or {}
    if cond == "lower(message) LIKE %s ESCAPE '\\'":
        # Le motif arrive déjà en minuscules et échappé : seul le message passe par lower().
        return None if msg is None else sql_like(msg.lower(), p[0])
    for col in COLUMNS:
        if cond == f"{col} = %s":
            return None if row.get(col) is None else row[col] == p[0]
        if cond == f"{col} LIKE %s":
            return sql_like(row.get(col), p[0])
    if cond == "tags @> %s::jsonb":
        return ref_contains(tags, json.loads(p[0]))
    if cond == "(tags ->> %s) LIKE %s":
        return sql_like(ref_text(tags.get(p[0], _ABSENT)), p[1])
    if cond == "(attributes #> %s::text[] IS NOT NULL OR attributes -> %s IS NOT NULL)":
        return ref_path(attrs, p[0]) is not _ABSENT or p[1] in attrs
    if cond == "((attributes #>> %s::text[]) LIKE %s OR (attributes ->> %s) LIKE %s)":
        return sql_or(sql_like(ref_text(ref_path(attrs, p[0])), p[1]),
                      sql_like(ref_text(attrs.get(p[2], _ABSENT)), p[3]))
    if cond.startswith("(attributes @> %s::jsonb"):
        assert cond == "(" + " OR ".join(["attributes @> %s::jsonb"] * len(p)) + ")"
        return any(ref_contains(attrs, json.loads(x)) for x in p)
    raise AssertionError(f"gabarit SQL inconnu de l'interpréteur de référence : {cond}")


def ref_eval(node, row):
    """Évalue l'arbre comme le ferait PostgreSQL sur le fragment émis par to_sql (NULL → faux par COALESCE)."""
    if isinstance(node, L.And):
        return all(ref_eval(n, row) for n in node.nodes)
    if isinstance(node, L.Or):
        return any(ref_eval(n, row) for n in node.nodes)
    if isinstance(node, L.Not):
        return not ref_eval(node.node, row)
    params: list = []
    resolver = L.LogsResolver()
    cond = resolver.text(node, params) if node.field is None else resolver.field(node, params)
    return bool(ref_leaf(cond, params, row))


# ── jeu de lignes ──────────────────────────────────────────────────────────────

def line(message, status, service, hostname, source, attributes=None, tags=None):
    return {"message": message, "status": status, "service": service, "hostname": hostname, "source": source,
            "trace_id": None, "span_id": None, "attributes": attributes or {}, "tags": tags or {}}


ROWS = [
    line("GET /api/sante HTTP/1.1 200", "info", "uvicorn", "web-01", "journald",
         {"http": {"status_code": 200, "method": "GET"}}, {"env": "prod", "host": "web-01"}),
    line("upstream timed out (110: Connection timed out)", "error", "nginx", "web-02", "file",
         {"http.status_code": 502}, {"env": "prod"}),
    line("Connection refused by peer", "warning", "nginx", "web-01", "file",
         {"http": {"status_code": "502"}}, {"env": "staging", "team": "backend"}),
    line("health check ok", "info", None, "web-03", "haproxy"),
    line("Tâche CRON terminée", "notice", "cron", "web-01", "journald",
         {"user": {"name": "jean.dupont"}}, {"team": "back-office"}),
    line("multi\nligne erreur fatale", "critical", "api", "app-01", "otlp",
         {"trace": None, "list": [{"a": 1}], "ok": True}, {"env": "prod"}),
    line("ERROR: disk full on /var", "error", "kernel", "web-02", "journald", {"http": {"status_code": 502.0}}),
    line("nginx: GET /health 200 0.001", "info", "nginx", "web-01", "nginx",
         {"http": {"method": "GET", "status_code": 200}, "duration": 1.5}, {"env": "prod"}),
    line("rror partiel", "debug", "nginx", None, "file", {"flag": "true"}),
    line("50%_done", "info", "batch", "web-03", "file", {}, {"env": "prod\n"}),
    line("Connexion refusée par le serveur", "error", "nginx", "web-01", "file",
         {"http": {"status_code": 503}}, {"env": "prod"}),
    line("user jean logged in", "info", "auth", "web-02", "journald", {"user.name": "jean"}, {"team": "backend"}),
    line("fichier C:\\Temp\\log introuvable", "error", "batch", "web-03", "file"),
]

# Requête → indices des lignes attendus (relevés à la main, pour que les deux côtés ne puissent pas
# se tromper de la même façon sans que le test le voie).
EXPECTED = {
    "error": [6],
    "rror": [6, 8],
    '"connection refused"': [2],
    "conn*refus*": [2, 10],
    "service:nginx status:error": [1, 10],
    "service:ngi* -status:info": [1, 2, 8, 10],
    "status:warn OR status:critical": [2, 5],
    "-service:cron": [0, 1, 2, 3, 5, 6, 7, 8, 9, 10, 11, 12],
    "host:web-01 (status:error OR status:warning)": [2, 10],
    "@http.status_code:502": [1, 2, 6],
    "@http.status_code:50*": [1, 2, 6, 10],
    "@user.name:jean*": [4, 11],
    "@trace:*": [5],
    "@list.0.a:1": [],
    "@list.0.a:*": [5],
    "@ok:true": [5],
    "@flag:true": [8],
    "env:prod": [0, 1, 5, 7, 10],
    "env:pro*": [0, 1, 5, 7, 9, 10],
    "team:back*": [2, 4, 11],
    "@http:{*": [0, 2, 6, 7, 10],
    '"50%_"': [9],
    # Échappement LIKE : % et _ saisis restent littéraux (sinon « %d » prendrait toute ligne avec un d).
    '"%d"': [],
    '"_done"': [9],
    '"c:\\\\temp"': [12],
    "Temp*Introuv": [12],
    "*": list(range(len(ROWS))),
    "@duration:1.5": [7],
    "health -check": [7],
    "-(service:nginx OR service:cron) status:info": [0, 3, 9, 11],
    "source:journald host:web-0*": [0, 4, 6, 11],
}


@pytest.mark.parametrize("query", sorted(EXPECTED))
def test_python_predicate_matches_sql(query):
    node = L.parse(query)
    predicate = M.compile_predicate(query)
    python_rows = [i for i, r in enumerate(ROWS) if predicate(r)]
    sql_rows = [i for i, r in enumerate(ROWS) if ref_eval(node, r)]
    assert python_rows == sql_rows, (query, python_rows, sql_rows)
    assert python_rows == EXPECTED[query], (query, python_rows)


def test_same_decision_on_rows_ready_for_copy():
    """Le prédicat lit la ligne telle qu'elle sera écrite (tuple de log_row, colonnes jsonb en texte)."""
    ts = int(datetime(2026, 11, 18, 12, tzinfo=timezone.utc).timestamp() * 1000)
    for query in EXPECTED:
        predicate = M.compile_predicate(query)
        for r in ROWS:
            row = B.log_row(1, 10, r["hostname"], ts, r["message"], r["status"], r["service"], r["source"],
                            r["attributes"], r["tags"], None, None)
            assert predicate(F.LogView(row)) == predicate(r), query


def test_compile_errors_match_search():
    for bad in ('"ouvert', "@bad!:x", "a OR"):
        with pytest.raises(QueryError):
            M.compile_predicate(bad)
    with pytest.raises(QueryError):
        F.compile_filter({"id": 1, "name": "vide", "query": "   ", "action": "exclude", "sample_rate": 0})


def test_jsonb_text_like_postgresql():
    assert M.jsonb_text({"bb": 1, "a": [1, "x"], "ccc": None}) == '{"a": [1, "x"], "bb": 1, "ccc": null}'
    assert M.jsonb_text(1e-07) == "0.0000001" and M.jsonb_text(502.0) == "502.0" and M.jsonb_text(True) == "true"
    assert M.jsonb_text(None) is None and M.jsonb_text("brut") == "brut"


# ── tirage déterministe ────────────────────────────────────────────────────────

def test_sampling_is_deterministic_and_proportional():
    assert F.keep_sampled("x", 1, 0.0) is False and F.keep_sampled("x", 1, 1.0) is True
    decisions = [F.keep_sampled(f"GET /api/sante {i}", 1_800_000_000_000 + i, 0.25) for i in range(20_000)]
    again = [F.keep_sampled(f"GET /api/sante {i}", 1_800_000_000_000 + i, 0.25) for i in range(20_000)]
    assert decisions == again                           # un renvoi de l'agent donne la même décision
    assert 0.23 < sum(decisions) / len(decisions) < 0.27
    same_msg = [F.keep_sampled("même message", 1_800_000_000_000 + i, 0.5) for i in range(2000)]
    assert 0.45 < sum(same_msg) / len(same_msg) < 0.55  # l'horodatage entre dans le tirage


# ── règles de décision ─────────────────────────────────────────────────────────

def view(message, status, service="nginx", ts=1_800_000_000_000):
    return F.LogView(B.log_row(1, 10, "web-01", ts, message, status, service, "file", {}, {}, None, None))


def flt(fid, query, action="exclude", rate=0.0):
    return F.compile_filter({"id": fid, "name": f"f{fid}", "query": query, "action": action, "sample_rate": rate})


def test_error_and_worse_never_sampled_out():
    sample_all_out = [flt(1, "health", "sample", 0.0)]
    assert F.decide(sample_all_out, view("health check", "info")) == 1
    assert F.decide(sample_all_out, view("health check", "warning")) == 1
    for status in ("error", "critical", "alert", "emergency"):
        assert F.decide(sample_all_out, view("health check", status)) is None


def test_exclude_drops_errors_only_when_it_targets_status():
    by_service = [flt(1, "service:nginx")]
    assert F.decide(by_service, view("x", "info")) == 1
    assert F.decide(by_service, view("x", "error")) is None
    by_status = [flt(2, "service:nginx status:error")]
    assert F.decide(by_status, view("x", "error")) == 2
    negated = [flt(3, "service:nginx -status:info")]
    assert F.decide(negated, view("x", "error")) is None       # -status: ne « vise » pas status
    assert M.targets_status(L.parse("status:error OR service:x")) is True
    assert M.targets_status(L.parse("-status:error")) is False


def test_first_matching_filter_decides():
    keep_all_first = [flt(1, "health", "sample", 1.0), flt(2, "health")]
    assert F.decide(keep_all_first, view("health", "info")) is None
    # Pour une erreur, les filtres qui ne peuvent pas la jeter sont sautés : le suivant décide.
    error_chain = [flt(1, "service:nginx"), flt(2, "status:error")]
    assert F.decide(error_chain, view("boom", "error")) == 2
    assert F.decide([flt(1, "service:cron")], view("health", "info")) is None


def test_apply_counts_per_filter():
    rows = [B.log_row(1, 10, "web-01", 1_800_000_000_000 + i, msg, st, "nginx", "file", {}, {}, None, None)
            for i, (msg, st) in enumerate([("GET /health 200", "info"), ("GET /health 200", "info"),
                                           ("upstream timed out", "error"), ("GET /api 200", "info")])]
    kept, counts = F.apply_filters([flt(7, '"/health"')], rows)
    assert [r[7] for r in kept] == ["upstream timed out", "GET /api 200"] and counts == {7: 2}
    assert F.apply_filters([], rows) == (rows, {})
