"""Langage de recherche des journaux (contrat § 7) : analyse et traduction en SQL paramétré.

    erreur "connexion refusée" service:nginx status:error host:web-01 source:file
    @http.status_code:502 -service:cron status:error OR status:critical (a OR b) -"bruit connu"

Priorités : `OR` lie deux termes voisins plus fort que le ET implicite, si bien que
`status:error OR status:critical service:nginx` = (error OU critical) ET nginx. Les parenthèses
regroupent, `-` exclut. Mots libres et "phrases" : la ligne CONTIENT le texte, sans casse (contrat
§ 11) ; `*` dans un mot non cité vaut n'importe quelle suite de caractères.

Cette traduction SQL est la référence : query/logs_match.py en tire des prédicats Python qui
sélectionnent exactement les mêmes lignes (filtres d'exclusion appliqués à l'ingestion). Toute
évolution d'un côté se reporte de l'autre, et le test test_log_filters.py les compare.

Aucune valeur saisie n'entre dans le texte SQL : tout passe en paramètre ; les chemins
d'attributs et noms d'étiquettes sont validés avant usage, et passés eux aussi en paramètre.
"""
import json
import re
from dataclasses import dataclass

from ..errors import QueryError
from ..ingest.body import LOG_STATUSES, STATUS_ALIASES
from ..util import TAG_KEY_RE, like_escape

MAX_TERMS = 50
MAX_DEPTH = 10
_ATTR_SEG_RE = re.compile(r"^[A-Za-z0-9_\-]{1,100}$")
_ATTR_FLAT_RE = re.compile(r"^[A-Za-z0-9_.\-]{1,200}$")


# ── lexique ────────────────────────────────────────────────────────────────────

@dataclass
class Tok:
    kind: str            # term | or | and | lpar | rpar
    text: str = ""
    quoted: bool = False  # valeur (ou mot) entre guillemets
    negate: bool = False


def tokenize(q: str) -> list[Tok]:
    toks: list[Tok] = []
    i, n = 0, len(q)
    while i < n:
        ch = q[i]
        if ch.isspace():
            i += 1
            continue
        if ch == "(":
            toks.append(Tok("lpar"))
            i += 1
            continue
        if ch == ")":
            toks.append(Tok("rpar"))
            i += 1
            continue
        negate = False
        if ch == "-" and i + 1 < n and not q[i + 1].isspace():
            negate = True
            i += 1
            if q[i] == "(":
                toks.append(Tok("not"))
                continue
        buf, quoted = [], False
        while i < n:
            ch = q[i]
            if ch == '"':
                j = i + 1
                part = []
                while j < n and q[j] != '"':
                    if q[j] == "\\" and j + 1 < n:
                        j += 1
                    part.append(q[j])
                    j += 1
                if j >= n:
                    raise QueryError("guillemet non fermé")
                buf.append("\x00" + "".join(part) + "\x00")   # repère interne de valeur citée
                quoted = True
                i = j + 1
                continue
            if ch.isspace() or ch in "()":
                break
            buf.append(ch)
            i += 1
        text = "".join(buf)
        if not quoted and not negate and text in ("OR", "AND", "NOT"):
            toks.append(Tok(text.lower()))
            continue
        if not quoted and not negate and text == "||":
            toks.append(Tok("or"))
            continue
        toks.append(Tok("term", text, quoted, negate))
    return toks


# ── arbre ──────────────────────────────────────────────────────────────────────

@dataclass
class Term:
    field: str | None     # None = texte libre ; "@a.b" = attribut ; sinon nom de champ/étiquette
    value: str
    phrase: bool = False  # valeur citée : recherche exacte
    wildcard: bool = False


@dataclass
class Not:
    node: object


@dataclass
class And:
    nodes: list


@dataclass
class Or:
    nodes: list


def _term_from(tok: Tok) -> Term:
    text = tok.text
    if text.startswith("\x00") and text.endswith("\x00") and text.count("\x00") == 2:
        return Term(None, text[1:-1], phrase=True)
    key, sep, value = text.partition(":")
    if sep and key and "\x00" not in key and (key.startswith("@") or TAG_KEY_RE.match(key)):
        if value.startswith("\x00") and value.endswith("\x00") and value.count("\x00") == 2:
            return Term(key, value[1:-1], phrase=True)
        value = value.replace("\x00", "")
        if value == "":
            raise QueryError(f"valeur vide après « {key}: »")
        return Term(key, value, wildcard="*" in value)
    word = text.replace("\x00", "")
    if not word:
        raise QueryError("terme vide")
    return Term(None, word, phrase=tok.quoted, wildcard="*" in word and not tok.quoted)


class _Parser:
    def __init__(self, toks: list[Tok]):
        self.toks = toks
        self.i = 0
        self.terms = 0

    def peek(self) -> Tok | None:
        return self.toks[self.i] if self.i < len(self.toks) else None

    def take(self) -> Tok:
        tok = self.toks[self.i]
        self.i += 1
        return tok

    def parse_and(self, depth: int):
        if depth > MAX_DEPTH:
            raise QueryError("parenthèses trop imbriquées")
        nodes = []
        while True:
            tok = self.peek()
            if tok is None or tok.kind == "rpar":
                break
            if tok.kind == "and":
                self.take()
                continue
            if tok.kind == "or":
                raise QueryError("OR doit se placer entre deux termes")
            nodes.append(self.parse_or(depth))
        if not nodes:
            return None
        return nodes[0] if len(nodes) == 1 else And(nodes)

    def parse_or(self, depth: int):
        nodes = [self.parse_unary(depth)]
        while self.peek() is not None and self.peek().kind == "or":
            self.take()
            if self.peek() is None or self.peek().kind in ("rpar", "or", "and"):
                raise QueryError("OR doit se placer entre deux termes")
            nodes.append(self.parse_unary(depth))
        return nodes[0] if len(nodes) == 1 else Or(nodes)

    def parse_unary(self, depth: int):
        tok = self.take()
        if tok.kind == "not":
            if self.peek() is None:
                raise QueryError("exclusion sans terme")
            return Not(self.parse_unary(depth))
        if tok.kind == "lpar":
            node = self.parse_and(depth + 1)
            if self.peek() is None or self.peek().kind != "rpar":
                raise QueryError("parenthèse non fermée")
            self.take()
            if node is None:
                raise QueryError("parenthèses vides")
            return node
        if tok.kind == "term":
            self.terms += 1
            if self.terms > MAX_TERMS:
                raise QueryError(f"{MAX_TERMS} termes au plus")
            term = _term_from(tok)
            return Not(term) if tok.negate else term
        raise QueryError("parenthèse fermante en trop")


def parse(q: str | None):
    """Arbre de la requête, ou None si elle est vide."""
    if q is None or not q.strip():
        return None
    if len(q) > 4000:
        raise QueryError("requête trop longue")
    parser = _Parser(tokenize(q.replace("\x00", "")))
    node = parser.parse_and(0)
    if parser.peek() is not None:
        raise QueryError("parenthèse fermante en trop")
    return node


# ── traduction SQL ────────────────────────────────────────────────────────────

def _pattern(value: str) -> str:
    """Motif LIKE : `*` devient `%`, le reste est littéral."""
    return "%".join(like_escape(part) for part in value.split("*"))


def _typed_values(value: str) -> list:
    """Une valeur d'attribut saisie peut viser une chaîne, un nombre ou un booléen JSON."""
    out: list = [value]
    if re.fullmatch(r"-?\d+", value):
        out.append(int(value))
    elif re.fullmatch(r"-?\d+\.\d+", value):
        out.append(float(value))
    elif value in ("true", "false"):
        out.append(value == "true")
    return out


def _nest(path: list[str], value) -> dict:
    obj = value
    for seg in reversed(path):
        obj = {seg: obj}
    return obj


def field_value(key: str, term: Term) -> str:
    """Valeur comparée à une colonne : `status:` suit les alias d'ingestion (warn → warning…)."""
    value = term.value
    if key == "status" and not term.wildcard:
        value = value.lower()
        value = value if value in LOG_STATUSES else STATUS_ALIASES.get(value, value)
    return value


def attribute_path(path_text: str) -> list[str]:
    """Chemin `@a.b.c` validé (segments et forme à plat), ou QueryError."""
    if not _ATTR_FLAT_RE.match(path_text):
        raise QueryError(f"chemin d'attribut invalide : « @{path_text} »")
    path = path_text.split(".")
    if not all(_ATTR_SEG_RE.match(seg) for seg in path):
        raise QueryError(f"chemin d'attribut invalide : « @{path_text} »")
    return path


def attribute_docs(path: list[str], path_text: str, value: str) -> list:
    """Documents d'inclusion d'une égalité d'attribut : imbriqué et à plat, en texte et typé."""
    docs = []
    for v in _typed_values(value):
        docs.append(_nest(path, v))
        if len(path) > 1:
            docs.append({path_text: v})
    return docs


class LogsResolver:
    """Correspondance champ → colonne pour la table `logs`."""
    columns = {"service": "service", "status": "status", "host": "hostname", "source": "source",
               "trace_id": "trace_id", "span_id": "span_id"}
    message = "message"
    attributes = "attributes"
    tags = "tags"

    def text(self, term: Term, params: list) -> str:
        # La ligne contient le texte, sans casse : `lower(message) LIKE '%v%'`, valeur en minuscules
        # et %, _, \ échappés. C'est la forme que sert l'index trigramme gin (lower(message)) posé
        # sur les partitions de `logs` (schema/004) ; `*` dans un mot non cité devient `%`.
        value = term.value.lower()
        pattern = _pattern(value) if term.wildcard and not term.phrase else like_escape(value)
        params.append("%" + pattern + "%")
        return f"lower({self.message}) LIKE %s ESCAPE '\\'"

    def field(self, term: Term, params: list) -> str:
        key = term.field
        if key.startswith("@"):
            return self.attribute(key[1:], term, params)
        column = self.columns.get(key)
        if column:
            value = field_value(key, term)
            if term.wildcard:
                params.append(_pattern(value))
                return f"{column} LIKE %s"
            params.append(value)
            return f"{column} = %s"
        if not TAG_KEY_RE.match(key):
            raise QueryError(f"champ inconnu : « {key} »")
        if term.wildcard:
            params += [key, _pattern(term.value)]
            return f"({self.tags} ->> %s) LIKE %s"
        params.append(json.dumps({key: term.value}, ensure_ascii=False))
        return f"{self.tags} @> %s::jsonb"

    def attribute(self, path_text: str, term: Term, params: list) -> str:
        path = attribute_path(path_text)
        col = self.attributes
        if term.value == "*" and not term.phrase:
            params += [path, path_text]
            return f"({col} #> %s::text[] IS NOT NULL OR {col} -> %s IS NOT NULL)"
        if term.wildcard and not term.phrase:
            pattern = _pattern(term.value)
            params += [path, pattern, path_text, pattern]
            return f"(({col} #>> %s::text[]) LIKE %s OR ({col} ->> %s) LIKE %s)"
        # Égalités par inclusion (index GIN jsonb_path_ops) : forme imbriquée {"http":{"status_code":502}}
        # comme forme à plat {"http.status_code":502} (attributs OTLP), en texte et en nombre.
        conds = []
        for doc in attribute_docs(path, path_text, term.value):
            params.append(json.dumps(doc, ensure_ascii=False))
            conds.append(f"{col} @> %s::jsonb")
        return "(" + " OR ".join(conds) + ")"


class EventsResolver(LogsResolver):
    """Variante pour `events` (alias `e`, hôtes joints en `h`) : texte libre sur titre et texte."""
    columns = {"source": "e.source", "type": "e.event_type", "alert_type": "e.alert_type",
               "priority": "e.priority", "host": "h.hostname", "aggregation_key": "e.aggregation_key"}
    tags = "e.tags"

    def text(self, term: Term, params: list) -> str:
        pattern = _pattern(term.value) if term.wildcard else "%" + like_escape(term.value) + "%"
        params += [pattern, pattern]
        return "(e.title ILIKE %s OR coalesce(e.text, '') ILIKE %s)"

    def attribute(self, path_text: str, term: Term, params: list) -> str:
        raise QueryError("les événements n'ont pas d'attributs @ ; filtrez par étiquette clé:valeur")


def to_sql(node, resolver=None) -> tuple[str, list]:
    """Fragment WHERE (sans le filtre d'organisation ni de temps, posés par l'appelant)."""
    resolver = resolver or LogsResolver()
    params: list = []
    if node is None:
        return "TRUE", params

    def walk(n) -> str:
        if isinstance(n, And):
            return "(" + " AND ".join(walk(x) for x in n.nodes) + ")"
        if isinstance(n, Or):
            return "(" + " OR ".join(walk(x) for x in n.nodes) + ")"
        if isinstance(n, Not):
            # COALESCE : « -service:x » garde les lignes sans service (NULL n'est pas « x »).
            return f"NOT COALESCE({walk(n.node)}, false)"
        if isinstance(n, Term):
            # Pas de COALESCE sur un terme : hors d'un NOT, un NULL vaut déjà faux (ET et OU sont
            # monotones, le WHERE écarte NULL), et un terme nu reste une condition que le
            # planificateur sait servir par un index (trigramme, (org_id, service, ts)…).
            return resolver.text(n, params) if n.field is None else resolver.field(n, params)
        raise QueryError("requête invalide")

    return walk(node), params


def compile_query(q: str | None, resolver=None) -> tuple[str, list]:
    return to_sql(parse(q), resolver)
