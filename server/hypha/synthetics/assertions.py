"""Assertions et extractions des tests synthétiques (contrat § 29), sans réseau.

Assertion : `{"type":"status|header|body_contains|json_path|latency_ms","path?","op":"eq|ne|lt|gt|contains|matches","value"}`.
Extraction : `{"name","from":"json_path|header|regex","path"}` ; la variable sert ensuite en
`{{nom}}` dans l'URL, les en-têtes et le corps des étapes suivantes.

Chemin JSON : sous-ensemble de JSONPath, `$.a.b[0]['c d']` (le `$` est facultatif).
"""
import json
import re
from dataclasses import dataclass, field

from ..ingest.sensitive import compile_pattern
from ..errors import QueryError

TYPES = ("status", "header", "body_contains", "json_path", "latency_ms")
OPS = ("eq", "ne", "lt", "gt", "contains", "matches")
EXTRACT_FROM = ("json_path", "header", "regex")
MAX_ASSERTIONS = 30
MAX_EXTRACTS = 20
_VAR_RE = re.compile(r"\{\{\s*([A-Za-z_][\w.\-]{0,63})\s*\}\}")
_NAME_RE = re.compile(r"^[A-Za-z_][\w.\-]{0,63}$")
_PATH_TOKEN = re.compile(r"\.([A-Za-z_$][\w$\-]*)|\[(\d+)\]|\[\s*'((?:[^'\\]|\\.)*)'\s*\]|\[\s*\"((?:[^\"\\]|\\.)*)\"\s*\]")
_MISSING = object()


def parse_path(path: str) -> list:
    """`$.a.b[0]` → ["a", "b", 0] ; lève ValueError si le chemin est mal formé."""
    if not isinstance(path, str) or not path.strip() or len(path) > 500:
        raise ValueError("chemin JSON vide ou trop long")
    text = path.strip()
    if text.startswith("$"):
        text = text[1:]
    if text and not text.startswith((".", "[")):
        text = "." + text
    out, pos = [], 0
    while pos < len(text):
        m = _PATH_TOKEN.match(text, pos)
        if not m:
            raise ValueError(f"chemin JSON invalide : « {path} »")
        if m.group(1) is not None:
            out.append(m.group(1))
        elif m.group(2) is not None:
            out.append(int(m.group(2)))
        else:
            out.append(m.group(3) if m.group(3) is not None else m.group(4))
        pos = m.end()
    return out


def json_get(doc, path: list):
    cur = doc
    for seg in path:
        if isinstance(seg, int):
            if not isinstance(cur, list) or not -len(cur) <= seg < len(cur):
                return _MISSING
            cur = cur[seg]
        else:
            if not isinstance(cur, dict) or seg not in cur:
                return _MISSING
            cur = cur[seg]
    return cur


def render(text: str, variables: dict, missing: list | None = None) -> str:
    """Remplace `{{nom}}` ; un nom inconnu reste tel quel et s'ajoute à `missing`."""
    if not isinstance(text, str) or "{{" not in text:
        return text

    def repl(m):
        name = m.group(1)
        if name in variables:
            return str(variables[name])
        if missing is not None:
            missing.append(name)
        return m.group(0)
    return _VAR_RE.sub(repl, text)


# ── validation ─────────────────────────────────────────────────────────────────

def validate_assertions(raw, where: str = "assertions") -> list[dict]:
    if raw is None:
        return []
    if not isinstance(raw, list) or len(raw) > MAX_ASSERTIONS:
        raise ValueError(f"{where} doit être une liste ({MAX_ASSERTIONS} au plus)")
    out = []
    for i, a in enumerate(raw):
        label = f"{where}[{i}]"
        if not isinstance(a, dict) or a.get("type") not in TYPES:
            raise ValueError(f"{label}.type attendu : {'|'.join(TYPES)}")
        op = a.get("op", "contains" if a["type"] == "body_contains" else "eq")
        if op not in OPS:
            raise ValueError(f"{label}.op attendu : {'|'.join(OPS)}")
        value = a.get("value")
        if a["type"] != "header" and value is None:
            raise ValueError(f"{label}.value est obligatoire")
        if isinstance(value, (dict, list)) or (isinstance(value, str) and len(value) > 2000):
            raise ValueError(f"{label}.value : texte (2000 caractères au plus), nombre ou booléen")
        item = {"type": a["type"], "op": op, "value": value}
        if a["type"] in ("header", "json_path"):
            path = a.get("path")
            if not isinstance(path, str) or not path.strip():
                raise ValueError(f"{label}.path est obligatoire")
            if a["type"] == "json_path":
                parse_path(path)
            item["path"] = path.strip()
        if op in ("lt", "gt") and not _number(value):
            raise ValueError(f"{label} : lt et gt comparent des nombres")
        if op == "matches":
            try:
                compile_pattern(str(value), allow_empty=True)
            except QueryError as e:
                raise ValueError(f"{label}.value : {e}")
        out.append(item)
    return out


def validate_extracts(raw, where: str = "extract") -> list[dict]:
    if raw is None:
        return []
    if not isinstance(raw, list) or len(raw) > MAX_EXTRACTS:
        raise ValueError(f"{where} doit être une liste ({MAX_EXTRACTS} au plus)")
    out = []
    for i, e in enumerate(raw):
        label = f"{where}[{i}]"
        if not isinstance(e, dict) or not isinstance(e.get("name"), str) or not _NAME_RE.match(e["name"]):
            raise ValueError(f"{label}.name : nom de variable [A-Za-z_][A-Za-z0-9_.-]*")
        if e.get("from") not in EXTRACT_FROM:
            raise ValueError(f"{label}.from attendu : {'|'.join(EXTRACT_FROM)}")
        path = e.get("path")
        if not isinstance(path, str) or not path.strip() or len(path) > 500:
            raise ValueError(f"{label}.path est obligatoire")
        if e["from"] == "json_path":
            parse_path(path)
        elif e["from"] == "regex":
            try:
                compile_pattern(path, allow_empty=True)
            except QueryError as err:
                raise ValueError(f"{label}.path : {err}")
        out.append({"name": e["name"], "from": e["from"], "path": path.strip() if e["from"] != "regex" else path})
    return out


# ── évaluation ─────────────────────────────────────────────────────────────────

@dataclass
class Response:
    status_code: int | None
    headers: dict = field(default_factory=dict)       # noms en minuscules
    body: str = ""
    latency_ms: float | None = None
    doc: object = _MISSING                            # corps JSON (décodé à la demande)

    def json(self):
        if self.doc is _MISSING:
            try:
                self.doc = json.loads(self.body) if self.body else None
            except ValueError:
                self.doc = None
        return self.doc


def _number(v) -> bool:
    if isinstance(v, bool):
        return False
    if isinstance(v, (int, float)):
        return True
    try:
        float(str(v))
        return True
    except ValueError:
        return False


def _equal(actual, expected) -> bool:
    if isinstance(actual, bool) or isinstance(expected, bool):
        if isinstance(expected, str):
            return str(actual).lower() == expected.strip().lower()
        return actual == expected
    if _number(actual) and _number(expected) and not (isinstance(actual, str) and isinstance(expected, str)):
        return float(actual) == float(expected)
    if actual is None:
        return expected in (None, "null")
    return str(actual) == str(expected)


def compare(actual, op: str, expected) -> bool:
    if op == "eq":
        return actual is not _MISSING and _equal(actual, expected)
    if op == "ne":
        return actual is _MISSING or not _equal(actual, expected)
    if actual is _MISSING or actual is None:
        return False
    if op in ("lt", "gt"):
        if not _number(actual):
            return False
        a, b = float(actual), float(expected)
        return a < b if op == "lt" else a > b
    if op == "contains":
        if isinstance(actual, list):
            return any(_equal(x, expected) for x in actual)
        if isinstance(actual, dict):
            return str(expected) in actual
        return str(expected) in str(actual)
    if op == "matches":
        text = actual if isinstance(actual, str) else json.dumps(actual, ensure_ascii=False)
        return compile_pattern(str(expected), allow_empty=True).search(text) is not None
    return False


def actual_value(a: dict, resp: Response):
    t = a["type"]
    if t == "status":
        return resp.status_code if resp.status_code is not None else _MISSING
    if t == "latency_ms":
        return resp.latency_ms if resp.latency_ms is not None else _MISSING
    if t == "header":
        return resp.headers.get(a["path"].lower(), _MISSING)
    if t == "body_contains":
        return resp.body
    return json_get(resp.json(), parse_path(a["path"]))


def evaluate(assertions: list[dict], resp: Response) -> list[dict]:
    """Résultat de chaque assertion : {type, path?, op, value, actual, ok}."""
    out = []
    for a in assertions:
        actual = actual_value(a, resp)
        if a["type"] == "body_contains":
            needle = str(a["value"])
            if a["op"] == "ne":
                good = needle not in resp.body
            elif a["op"] == "matches":
                good = compare(resp.body, "matches", needle)
            else:
                good = needle in resp.body
            shown = None
        else:
            good = compare(actual, a["op"], a["value"])
            shown = None if actual is _MISSING else actual
            if isinstance(shown, str) and len(shown) > 200:
                shown = shown[:200] + "…"
            elif isinstance(shown, (dict, list)):
                shown = json.dumps(shown, ensure_ascii=False)[:200]
        item = {"type": a["type"], "op": a["op"], "value": a["value"], "actual": shown, "ok": good}
        if "path" in a:
            item["path"] = a["path"]
        out.append(item)
    return out


def extract(extracts: list[dict], resp: Response) -> tuple[dict, list[str]]:
    """Variables tirées de la réponse, et noms de celles qui n'ont rien trouvé."""
    found, missing = {}, []
    for e in extracts:
        value = None
        if e["from"] == "header":
            value = resp.headers.get(e["path"].lower())
        elif e["from"] == "json_path":
            v = json_get(resp.json(), parse_path(e["path"]))
            value = None if v is _MISSING else (v if isinstance(v, str) else json.dumps(v, ensure_ascii=False))
        else:
            m = compile_pattern(e["path"], allow_empty=True).search(resp.body)
            if m:
                value = m.group(1) if m.groups() else m.group(0)
        if value is None:
            missing.append(e["name"])
        else:
            found[e["name"]] = value
    return found, missing


def describe_failure(r: dict) -> str:
    where = f" {r['path']}" if r.get("path") else ""
    return f"assertion {r['type']}{where} {r['op']} {r['value']!r} en échec (obtenu {r['actual']!r})"
