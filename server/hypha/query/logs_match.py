r"""Prédicats Python tirés du langage des journaux, pour juger une ligne AVANT son écriture.

Même analyseur que la recherche (logs_ql.parse), et chaque feuille reproduit l'opérateur SQL
qu'émet logs_ql.LogsResolver pour elle :

    mot libre, "phrase"   lower(message) LIKE '%v%' ESCAPE '\'     → v.lower() in message.lower()
    mot avec *            lower(message) LIKE '%a%b%' ESCAPE '\'   → a…b dans message, sans casse
                          (v en minuscules côté Python, %, _ et \ échappés : sens littéral)
    champ:v               colonne = v (status: suit les alias)     → égalité exacte, NULL = faux
    champ:v*              colonne LIKE 'v%'  (sensible à la casse) → motif ancré
    clé:v (étiquette)     tags @> {"clé":"v"}                      → inclusion jsonb
    clé:v*                (tags ->> 'clé') LIKE 'v%'               → texte jsonb, motif ancré
    @a.b:v                attributes @> {a:{b:v}} OU {"a.b":v}, en texte et typé (nombre, booléen)
    @a.b:v*               (attributes #>> '{a,b}') LIKE … OU (attributes ->> 'a.b') LIKE …
    @a.b:*                attributes #> '{a,b}' IS NOT NULL OU attributes -> 'a.b' IS NOT NULL
    -x, ET, OR            NOT COALESCE(x, false), AND, OR

Écart connu, sans portée pratique : lower() de PostgreSQL et str.lower() de Python peuvent
différer sur de rares caractères (ß, İ) selon la locale de la base.
"""
import json
import re
from decimal import Decimal
from typing import Callable

from ..errors import QueryError
from . import logs_ql as L

Predicate = Callable[[object], bool]


# ── sémantique jsonb ───────────────────────────────────────────────────────────

def _is_number(v) -> bool:
    return isinstance(v, (int, float)) and not isinstance(v, bool)


def _scalar_eq(a, b) -> bool:
    """Égalité de deux scalaires jsonb : les types doivent concorder (true n'est pas 1)."""
    if isinstance(b, bool) or isinstance(a, bool):
        return isinstance(a, bool) and isinstance(b, bool) and a == b
    if _is_number(b):
        return _is_number(a) and a == b
    if b is None:
        return a is None
    return isinstance(a, str) and isinstance(b, str) and a == b


def jsonb_contains(container, contained) -> bool:
    """Opérateur `@>` entre valeurs imbriquées. L'exception « un tableau contient un scalaire » de
    PostgreSQL ne vaut qu'au premier niveau ; nos documents commencent toujours par un objet."""
    if isinstance(contained, dict):
        return isinstance(container, dict) and all(
            k in container and jsonb_contains(container[k], v) for k, v in contained.items())
    if isinstance(contained, list):
        return isinstance(container, list) and all(
            any(jsonb_contains(x, y) for x in container) for y in contained)
    if isinstance(container, (dict, list)):
        return False
    return _scalar_eq(container, contained)


def _numeric_text(v) -> str:
    if isinstance(v, int):
        return str(v)
    # Le flottant écrit par json.dumps est relu par PostgreSQL en numeric, qui l'affiche sans exposant.
    return format(Decimal(repr(v)), "f")


def _jsonb_key(k: str):
    # jsonb range les clés d'un objet par longueur (en octets) puis par octets.
    b = k.encode("utf-8")
    return (len(b), b)


def jsonb_out(v) -> str:
    """Texte d'une valeur jsonb tel que l'écrit PostgreSQL (`, ` et `: `, clés rangées)."""
    if v is None:
        return "null"
    if isinstance(v, bool):
        return "true" if v else "false"
    if _is_number(v):
        return _numeric_text(v)
    if isinstance(v, str):
        return json.dumps(v, ensure_ascii=False)
    if isinstance(v, list):
        return "[" + ", ".join(jsonb_out(x) for x in v) + "]"
    if isinstance(v, dict):
        return "{" + ", ".join(f"{json.dumps(k, ensure_ascii=False)}: {jsonb_out(v[k])}"
                               for k in sorted(v, key=_jsonb_key)) + "}"
    return json.dumps(v, ensure_ascii=False, default=str)


def jsonb_text(v) -> str | None:
    """Résultat de `->>` / `#>>` : la chaîne nue, le texte jsonb sinon, NULL pour un null JSON."""
    if v is None:
        return None
    if isinstance(v, str):
        return v
    return jsonb_out(v)


_MISSING = object()


def jsonb_path(obj, path: list[str]):
    """Opérateur `#>` : clés d'objet, ou indices (négatifs depuis la fin) dans un tableau."""
    cur = obj
    for seg in path:
        if isinstance(cur, dict):
            if seg not in cur:
                return _MISSING
            cur = cur[seg]
        elif isinstance(cur, list):
            if not re.fullmatch(r"-?\d+", seg):
                return _MISSING
            i = int(seg)
            if i < 0:
                i += len(cur)
            if not 0 <= i < len(cur):
                return _MISSING
            cur = cur[i]
        else:
            return _MISSING
    return cur


def like_matcher(pattern_parts: list[str], anchored: bool) -> Callable[[str], bool]:
    """Motif LIKE issu d'une valeur à `*` : les parties sont littérales, `*` vaut toute suite (sauts
    de ligne compris). Ancré : la chaîne entière (fullmatch, car `$` tolère un saut de ligne final,
    LIKE non) ; non ancré : les parties dans l'ordre, n'importe où (LIKE '%…%')."""
    rx = re.compile(".*".join(re.escape(p) for p in pattern_parts), re.S)
    if anchored:
        return lambda s: rx.fullmatch(s) is not None
    return lambda s: rx.search(s) is not None


# ── compilation ────────────────────────────────────────────────────────────────

def _text_leaf(term: L.Term) -> Predicate:
    if term.wildcard and not term.phrase:
        match = like_matcher([p.lower() for p in term.value.split("*")], anchored=False)
        return lambda r: (m := r.get("message")) is not None and match(m.lower())
    needle = term.value.lower()
    return lambda r: (m := r.get("message")) is not None and needle in m.lower()


def _field_leaf(term: L.Term, resolver: L.LogsResolver) -> Predicate:
    key = term.field
    if key.startswith("@"):
        return _attribute_leaf(key[1:], term)
    column = resolver.columns.get(key)
    if column:
        value = L.field_value(key, term)
        if term.wildcard:
            match = like_matcher(value.split("*"), anchored=True)
            return lambda r: isinstance(v := r.get(column), str) and match(v)
        return lambda r: r.get(column) == value
    if term.wildcard:
        match = like_matcher(term.value.split("*"), anchored=True)

        def tag_like(r) -> bool:
            tags = r.get("tags") or {}
            text = jsonb_text(tags.get(key)) if isinstance(tags, dict) else None
            return text is not None and match(text)
        return tag_like
    doc = {key: term.value}
    return lambda r: jsonb_contains(r.get("tags") or {}, doc)


def _attribute_leaf(path_text: str, term: L.Term) -> Predicate:
    path = L.attribute_path(path_text)

    def attrs(r):
        a = r.get("attributes")
        return a if isinstance(a, dict) else {}

    if term.value == "*" and not term.phrase:
        return lambda r: jsonb_path(attrs(r), path) is not _MISSING or path_text in attrs(r)
    if term.wildcard and not term.phrase:
        match = like_matcher(term.value.split("*"), anchored=True)

        def attr_like(r) -> bool:
            a = attrs(r)
            nested = jsonb_path(a, path)
            for v in ((None if nested is _MISSING else nested), a.get(path_text)):
                text = jsonb_text(v)
                if text is not None and match(text):
                    return True
            return False
        return attr_like
    docs = L.attribute_docs(path, path_text, term.value)
    return lambda r: any(jsonb_contains(attrs(r), d) for d in docs)


def compile_node(node, resolver: L.LogsResolver | None = None) -> Predicate:
    resolver = resolver or L.LogsResolver()
    if node is None:
        return lambda r: True
    if isinstance(node, L.And):
        parts = [compile_node(n, resolver) for n in node.nodes]
        return lambda r: all(p(r) for p in parts)
    if isinstance(node, L.Or):
        parts = [compile_node(n, resolver) for n in node.nodes]
        return lambda r: any(p(r) for p in parts)
    if isinstance(node, L.Not):
        inner = compile_node(node.node, resolver)
        return lambda r: not inner(r)
    if isinstance(node, L.Term):
        return _text_leaf(node) if node.field is None else _field_leaf(node, resolver)
    raise QueryError("requête invalide")


def compile_predicate(q: str | None) -> Predicate:
    """Prédicat d'une requête ; lève QueryError exactement comme la traduction SQL."""
    node = L.parse(q)
    L.to_sql(node)              # mêmes refus que la recherche (champ inconnu, chemin invalide)
    return compile_node(node)


def targets_status(node) -> bool:
    """Vrai si la requête vise explicitement `status:` (terme non nié) : seul un tel filtre
    d'exclusion peut jeter une ligne de niveau error ou plus grave (contrat § 11)."""
    if node is None:
        return False
    if isinstance(node, L.Term):
        return node.field == "status"
    if isinstance(node, L.Not):
        return False
    if isinstance(node, (L.And, L.Or)):
        return any(targets_status(n) for n in node.nodes)
    return False
