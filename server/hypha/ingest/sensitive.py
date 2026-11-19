"""Masquage des données sensibles des journaux (contrat § 23), à l'ingestion, avant l'écriture.

Appliqué après les filtres d'exclusion et le découpage des formats (§§ 11, 12), sur `message` et
les valeurs texte de `attributes`. Une partie reconnue devient `[masqué:<règle>]` ; pour les
formes « clé=valeur » (password=…, Authorization: Bearer …), seule la valeur est remplacée.

Règles livrées (`builtin`) : `secrets`, `iban`, `fr_nir`, `credit_card`, `phone_fr`, `email`,
`ipv4_public`, essayées dans cet ordre (un IBAN ou un NIR ne doit pas être pris pour une carte).
Les cartes passent la clé de Luhn, les IBAN le modulo 97, les NIR leur clé de contrôle, les IPv4
le caractère public de l'adresse. Une organisation créée après la migration 006 reçoit `secrets`
active ; les autres règles, et toutes celles d'une organisation plus ancienne, sont posées inactives.

Coût : chaque règle a un test préalable bon marché (une sous-chaîne ou une expression courte) ;
les attributs ne sont décodés que si une règle reconnaît quelque chose dans leur JSON brut.

Expression propre d'une organisation : refusée si elle porte des répétitions imbriquées
(`(a+)+`) ou des renvois (`\\1`), qui peuvent bloquer l'ingestion de tout le processus. Risque
restant : une alternance ambiguë sous répétition (`(a|a)*`) n'est pas détectée.
"""
import ipaddress
import json
import logging
import re
from dataclasses import dataclass
from typing import Callable

from ..errors import QueryError
from ..query import logs_match
from .body import LOG_COLUMNS
from .log_filters import LogView

log = logging.getLogger("hypha.ingest")

_I_MESSAGE = LOG_COLUMNS.index("message")
_I_ATTRS = LOG_COLUMNS.index("attributes")

BUILTIN_ORDER = ("secrets", "iban", "fr_nir", "credit_card", "phone_fr", "email", "ipv4_public")
BUILTIN_LABELS = {
    "secrets": "Secrets (jetons, mots de passe, clés)",
    "iban": "IBAN",
    "fr_nir": "Numéro de sécurité sociale (NIR)",
    "credit_card": "Cartes bancaires",
    "phone_fr": "Téléphones (France)",
    "email": "Adresses électroniques",
    "ipv4_public": "Adresses IPv4 publiques",
}
TARGETS = ("message", "attributes")
MAX_PATTERN = 500


def replacement_for(key: str) -> str:
    return f"[masqué:{key}]"


# ── contrôles ──────────────────────────────────────────────────────────────────

def luhn_ok(digits: str) -> bool:
    total, double = 0, False
    for ch in reversed(digits):
        d = ord(ch) - 48
        if double:
            d *= 2
            if d > 9:
                d -= 9
        total += d
        double = not double
    return total % 10 == 0


IBAN_LENGTHS = {
    "AD": 24, "AT": 20, "BE": 16, "BG": 22, "CH": 21, "CY": 28, "CZ": 24, "DE": 22, "DK": 18, "EE": 20,
    "ES": 24, "FI": 18, "FR": 27, "GB": 22, "GI": 23, "GR": 27, "HR": 21, "HU": 28, "IE": 22, "IS": 26,
    "IT": 27, "LI": 21, "LT": 20, "LU": 20, "LV": 21, "MC": 27, "MT": 31, "NL": 18, "NO": 15, "PL": 28,
    "PT": 25, "RO": 24, "SE": 24, "SI": 19, "SK": 24, "SM": 27, "VA": 22, "MA": 28, "TN": 24, "DZ": 26,
}


def iban_ok(compact: str) -> bool:
    if not 15 <= len(compact) <= 34 or not compact[:2].isalpha() or not compact[2:4].isdigit():
        return False
    expected = IBAN_LENGTHS.get(compact[:2])
    if expected is not None and len(compact) != expected:
        return False
    rearranged = compact[4:] + compact[:4]
    try:
        number = "".join(str(int(c, 36)) for c in rearranged)
    except ValueError:
        return False
    return int(number) % 97 == 1


def nir_ok(compact: str) -> bool:
    """NIR de 15 caractères (13 + clé) ; Corse : 2A vaut 19, 2B vaut 18 pour le calcul."""
    if len(compact) != 15:
        return False
    body, key = compact[:13], compact[13:]
    body = body[:5] + {"2A": "19", "2B": "18"}.get(body[5:7], body[5:7]) + body[7:]
    if not body.isdigit() or not key.isdigit():
        return False
    return 97 - int(body) % 97 == int(key)


_PUBLIC_CACHE: dict[str, bool] = {}


def public_ipv4(text: str) -> bool:
    """Adresse IPv4 publique (au sens IANA) ; les adresses déjà jugées sont gardées en mémoire."""
    v = _PUBLIC_CACHE.get(text)
    if v is None:
        try:
            v = ipaddress.IPv4Address(text).is_global
        except ValueError:
            v = False
        if len(_PUBLIC_CACHE) >= 50_000:
            _PUBLIC_CACHE.clear()
        _PUBLIC_CACHE[text] = v
    return v


def card_prefix_ok(d: str) -> bool:
    """Préfixe émetteur et longueur d'un numéro de carte (Visa, Mastercard, Amex, Discover, Diners,
    JCB, Maestro) : écarte les horodatages en ms et autres longs nombres qui passeraient Luhn."""
    n = len(d)
    if d[0] == "4":
        return n in (13, 16, 19)
    two, four = d[:2], d[:4]
    if "51" <= two <= "55" or "2221" <= four <= "2720":
        return n == 16
    if two in ("34", "37"):
        return n == 15
    if d[0] == "6":
        return 16 <= n <= 19
    if two in ("30", "36", "38", "39"):
        return 14 <= n <= 16
    if two == "35":
        return 16 <= n <= 19
    if two in ("50", "56", "57", "58"):
        return 12 <= n <= 19
    return False


# ── règles livrées ─────────────────────────────────────────────────────────────
# Chaque expression commence par une classe de caractères (le moteur saute alors vite les positions
# impossibles) ; les bornes à gauche se vérifient à la main sur le caractère précédent.

_AUTH_RE = re.compile(r"(?i)\bauthorization\b[\"']?\s*[:=]\s*[\"']?(?:basic|bearer|token|digest)?\s*"
                      r"(?P<v>[\w\-.~+/]{6,}=*)")
_BEARER_RE = re.compile(r"(?i)\bbearer\s+(?P<v>[\w\-.~+/]{8,}=*)")
# password=…, db_password: …, "api_key":"…", X-Api-Key: …, csrf_token=… ; « pass » et « pwd » seuls.
_KV_RE = re.compile(r"(?i)(?:(?<![\w])|(?<=[_\-]))(?:password|passwd|passphrase|secret|api[_-]?key|apikey"
                    r"|access[_-]?key|token|private[_-]?key|pass|pwd)\b[\"']?\s*[:=]\s*[\"']?"
                    r"(?P<v>[^\s\"'&,;<>]{1,300})")
_AWS_RE = re.compile(r"\b(?:AKIA|ASIA)[0-9A-Z]{16}\b")
_GH_RE = re.compile(r"\bgh[pousr]_[A-Za-z0-9]{36,255}\b|\bgithub_pat_[A-Za-z0-9_]{22,255}\b")
_SLACK_RE = re.compile(r"\bxox[abposr]-[A-Za-z0-9-]{10,250}")
_HPH_RE = re.compile(r"\bhph_[0-9A-Za-z]{40}\b")
_JWT_RE = re.compile(r"\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]*")
_PEM_RE = re.compile(r"-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY-----[\s\S]{0,10000}?"
                     r"(?:-----END (?:[A-Z0-9]+ )*PRIVATE KEY-----|\Z)")
# (mots qui doivent figurer, cherchés en minuscules ?, expression)
_SECRET_PARTS = [
    (("authoriz",), True, _AUTH_RE),
    (("bearer",), True, _BEARER_RE),
    (("pass", "pwd", "secret", "token", "key"), True, _KV_RE),
    (("AKIA", "ASIA"), False, _AWS_RE),
    (("ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_"), False, _GH_RE),
    (("xox",), False, _SLACK_RE),
    (("hph_",), False, _HPH_RE),
    (("eyJ",), False, _JWT_RE),
    (("PRIVATE KEY",), False, _PEM_RE),
]
_SECRET_WORDS = ("pass", "pwd", "secret", "token", "key", "authoriz", "bearer", "cookie", "akia", "asia", "ghp_",
                 "gho_", "ghu_", "ghs_", "ghr_", "github_pat_", "xox", "hph_", "eyj")
SECRET_KEY_RE = re.compile(r"(?i)^(?:password|passwd|passphrase|pwd|secret|client_secret|api[_-]?key|apikey"
                           r"|access[_-]?key|secret[_-]?key|access[_-]?token|refresh[_-]?token|auth[_-]?token|token"
                           r"|private[_-]?key|authorization|proxy-authorization|cookie|set-cookie|x-hypha-api-key"
                           r"|x-hypha-app-key|x-api-key)$")

_IBAN_RE = re.compile(r"[A-Z]{2}\d{2}(?: ?[A-Z0-9]){11,30}\b")
_NIR_RE = re.compile(r"[12][ .]?\d{2}[ .]?(?:0[1-9]|1[0-2]|[2-9]\d)[ .]?(?:\d{2}|2[ABab])[ .]?\d{3}[ .]?\d{3}"
                     r"[ .]?\d{2}(?!\d)")
_CARD_RE = re.compile(r"\d(?:[ -]?\d){12,18}(?!\d)")
_PHONE_RE = re.compile(r"(?:\+33[ .-]?(?:\(0\)[ .-]?)?|0033[ .-]?|0)[1-9](?:[ .-]?\d{2}){4}(?!\d)")
_EMAIL_RE = re.compile(r"[A-Za-z0-9._%+\-]{1,64}@[A-Za-z0-9\-]{1,63}(?:\.[A-Za-z0-9\-]{1,63})*\.[A-Za-z]{2,24}\b")
_IPV4_RE = re.compile(r"\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}(?!\d|\.\d)")


def _prev(text: str, start: int) -> str:
    return text[start - 1] if start > 0 else " "


def secrets_hint(text: str) -> bool:
    low = text.lower()
    return any(w in low for w in _SECRET_WORDS)


def _secrets_spans(text: str):
    low = None
    spans = []
    for words, lower, rx in _SECRET_PARTS:
        if lower:
            if low is None:
                low = text.lower()
            hay = low
        else:
            hay = text
        if not any(w in hay for w in words):
            continue
        has_v = "v" in rx.groupindex
        for m in rx.finditer(text):
            spans.append(m.span("v") if has_v else m.span())
    spans.sort()
    return spans


def _iban_spans(text: str):
    for m in _IBAN_RE.finditer(text):
        if _prev(text, m.start()).isalnum():
            continue
        raw = m.group(0)
        positions = [i for i, c in enumerate(raw) if c != " "]
        compact = raw.replace(" ", "")
        lengths = [IBAN_LENGTHS[compact[:2]]] if compact[:2] in IBAN_LENGTHS else range(len(compact), 14, -1)
        for n in lengths:
            if n <= len(compact) and iban_ok(compact[:n]):
                yield m.start(), m.start() + positions[n - 1] + 1
                break


def _nir_spans(text: str):
    for m in _NIR_RE.finditer(text):
        if not _prev(text, m.start()).isdigit() and nir_ok(m.group(0).replace(" ", "").replace(".", "").upper()):
            yield m.span()


def _card_spans(text: str):
    for m in _CARD_RE.finditer(text):
        if _prev(text, m.start()) in "0123456789-":
            continue
        digits = m.group(0).replace(" ", "").replace("-", "")
        if 13 <= len(digits) <= 19 and card_prefix_ok(digits) and luhn_ok(digits):
            yield m.span()


def _phone_spans(text: str):
    for m in _PHONE_RE.finditer(text):
        if _prev(text, m.start()) not in "0123456789+":
            yield m.span()


def _email_spans(text: str):
    for m in _EMAIL_RE.finditer(text):
        yield m.span()


def _ipv4_spans(text: str):
    for m in _IPV4_RE.finditer(text):
        if _prev(text, m.start()) in "0123456789.":
            continue
        ip = m.group(0)
        if all(int(o) <= 255 for o in ip.split(".")) and public_ipv4(ip):
            yield m.span()


def _plain(regex: re.Pattern):
    def spans(text: str):
        for m in regex.finditer(text):
            yield m.span()
    return spans


@dataclass(frozen=True)
class Builtin:
    spans: Callable
    hint: Callable[[str], bool] | None = None        # test préalable du texte (None : l'expression suffit)
    raw_hint: Callable[[str], bool] | None = None    # test préalable du JSON brut des attributs
    secret_keys: bool = False
    detector: re.Pattern | None = None               # recherche en C, sans contrôle : candidat ou rien


BUILTINS = {
    "secrets": Builtin(_secrets_spans, None, secrets_hint, secret_keys=True),
    "iban": Builtin(_iban_spans, detector=_IBAN_RE),
    "fr_nir": Builtin(_nir_spans, detector=_NIR_RE),
    "credit_card": Builtin(_card_spans, detector=_CARD_RE),
    "phone_fr": Builtin(_phone_spans, detector=_PHONE_RE),
    "email": Builtin(_email_spans, detector=re.compile("@")),
    "ipv4_public": Builtin(_ipv4_spans, detector=_IPV4_RE),
}


# ── expression propre : contrôle de sûreté ─────────────────────────────────────

try:
    from re import _constants as _C, _parser as _P      # Python ≥ 3.11
except ImportError:                                      # pragma: no cover
    import sre_constants as _C
    import sre_parse as _P

_REPEATS = {_C.MAX_REPEAT, _C.MIN_REPEAT} | ({_C.POSSESSIVE_REPEAT} if hasattr(_C, "POSSESSIVE_REPEAT") else set())


def _walk(items, inside_repeat: bool) -> None:
    for op, av in items:
        if op in _REPEATS:
            lo, hi, sub = av
            unbounded = hi == _C.MAXREPEAT or hi > 100
            if inside_repeat and unbounded:
                raise QueryError("expression refusée : répétitions imbriquées (risque de blocage)")
            _walk(sub, inside_repeat or unbounded)
        elif op in (_C.GROUPREF, getattr(_C, "GROUPREF_EXISTS", None)):
            raise QueryError("expression refusée : les renvois (\\1) ne sont pas acceptés")
        elif op == _C.SUBPATTERN:
            _walk(av[-1], inside_repeat)
        elif op == _C.BRANCH:
            for alt in av[1]:
                _walk(alt, inside_repeat)
        elif op in (_C.ASSERT, _C.ASSERT_NOT):
            _walk(av[1], inside_repeat)
        elif op == getattr(_C, "ATOMIC_GROUP", None):
            _walk(av, inside_repeat)


def compile_pattern(pattern, allow_empty: bool = False) -> re.Pattern:
    """Expression d'une règle propre, compilée après contrôle ; lève QueryError (message montré)."""
    if not isinstance(pattern, str) or not pattern.strip():
        raise QueryError("pattern est obligatoire pour une règle sans builtin")
    if len(pattern) > MAX_PATTERN:
        raise QueryError(f"expression de plus de {MAX_PATTERN} caractères")
    try:
        parsed = _P.parse(pattern)
        compiled = re.compile(pattern)
    except re.error as e:
        raise QueryError(f"expression invalide : {e}")
    _walk(list(parsed), False)
    if not allow_empty and compiled.search("") is not None:
        raise QueryError("expression refusée : elle reconnaît la chaîne vide")
    return compiled


# ── règle compilée ─────────────────────────────────────────────────────────────

@dataclass
class Rule:
    id: int
    key: str
    name: str
    replacement: str
    spans: Callable
    hint: Callable[[str], bool] | None = None
    secret_keys: bool = False
    predicate: Callable | None = None
    on_message: bool = True
    on_attributes: bool = True
    raw_hint: Callable[[str], bool] | None = None
    detector: re.Pattern | None = None

    def apply(self, text: str) -> tuple[str, int]:
        if not text or (self.hint is not None and not self.hint(text)):
            return text, 0
        if self.detector is not None and self.detector.search(text) is None:
            return text, 0
        out, last, n = [], 0, 0
        for s, e in self.spans(text):
            if s < last or e <= s:
                continue
            out.append(text[last:s])
            out.append(self.replacement)
            last = e
            n += 1
        if not n:
            return text, 0
        out.append(text[last:])
        return "".join(out), n

    def triggers(self, raw_json: str) -> bool:
        """Test sur le JSON brut des attributs : faut-il les décoder pour cette règle ?"""
        hint = self.raw_hint or self.hint
        if hint is not None and not hint(raw_json):
            return False
        if self.detector is not None and self.detector.search(raw_json) is None:
            return False
        if self.secret_keys:
            return True
        return next(iter(self.spans(raw_json)), None) is not None


def compile_rule(row: dict) -> Rule:
    """Règle d'après sa ligne `sensitive_rules` ; lève QueryError si elle est inutilisable."""
    builtin = row.get("builtin")
    targets = set(row.get("targets") or TARGETS)
    scope = (row.get("scope_query") or "").strip()
    predicate = logs_match.compile_predicate(scope) if scope else None
    key = builtin or "propre"
    replacement = row.get("replacement") or replacement_for(key)
    if builtin:
        spec = BUILTINS.get(builtin)
        if spec is None:
            raise QueryError(f"règle livrée inconnue : {builtin}")
        return Rule(row["id"], builtin, row.get("name") or builtin, replacement, spec.spans, spec.hint,
                    spec.secret_keys, predicate, "message" in targets, "attributes" in targets, spec.raw_hint,
                    spec.detector)
    regex = compile_pattern(row.get("pattern"))
    return Rule(row["id"], key, row.get("name") or "règle", replacement, _plain(regex), None, False, predicate,
                "message" in targets, "attributes" in targets, None, regex)


class RuleSet(list):
    """Règles compilées d'une organisation, dans l'ordre d'application."""


def _mask_tree(obj, rule: Rule) -> int:
    n = 0
    if isinstance(obj, dict):
        for k, v in obj.items():
            if isinstance(v, str):
                if rule.secret_keys and v and SECRET_KEY_RE.match(str(k).rsplit(".", 1)[-1]):
                    obj[k] = rule.replacement
                    n += 1
                    continue
                new, m = rule.apply(v)
                if m:
                    obj[k] = new
                    n += m
            elif isinstance(v, (int, float)) and not isinstance(v, bool) and rule.secret_keys \
                    and SECRET_KEY_RE.match(str(k).rsplit(".", 1)[-1]):
                obj[k] = rule.replacement
                n += 1
            elif isinstance(v, (dict, list)):
                n += _mask_tree(v, rule)
    elif isinstance(obj, list):
        for i, v in enumerate(obj):
            if isinstance(v, str):
                new, m = rule.apply(v)
                if m:
                    obj[i] = new
                    n += m
            elif isinstance(v, (dict, list)):
                n += _mask_tree(v, rule)
    return n


def mask_text(text: str, rules: list[Rule], counts: dict | None = None) -> tuple[str, dict[int, int]]:
    """Texte masqué par les règles (dans l'ordre) et nombre de remplacements par règle."""
    hits: dict[int, int] = {}
    for rule in rules:
        text, n = rule.apply(text)
        if n:
            hits[rule.id] = hits.get(rule.id, 0) + n
    if counts is not None:
        for rid, n in hits.items():
            counts[rid] = counts.get(rid, 0) + n
    return text, hits


def mask_row(row: tuple, rules: list[Rule], counts: dict) -> tuple:
    """Ligne prête pour le COPY, masquée ; la même ligne si rien n'est reconnu."""
    message = row[_I_MESSAGE]
    raw = row[_I_ATTRS]
    has_attrs = bool(raw) and raw != "{}"
    attrs = None
    view = None
    new_message = message
    attrs_changed = False
    for rule in rules:
        if rule.predicate is not None:
            if view is None:
                view = LogView(row)
            if not rule.predicate(view):
                continue
        if rule.on_message and new_message:
            new_message, n = rule.apply(new_message)
            if n:
                counts[rule.id] = counts.get(rule.id, 0) + n
        if rule.on_attributes and has_attrs and rule.triggers(raw):
            if attrs is None:
                attrs = json.loads(raw)
            n = _mask_tree(attrs, rule)
            if n:
                attrs_changed = True
                counts[rule.id] = counts.get(rule.id, 0) + n
    if new_message is message and not attrs_changed:
        return row
    out = list(row)
    out[_I_MESSAGE] = new_message
    if attrs_changed:
        out[_I_ATTRS] = json.dumps(attrs, ensure_ascii=False, separators=(",", ":"))
    return tuple(out)


def mask_rows(rows: list[tuple], rules: list[Rule], counts: dict) -> list[tuple]:
    if not rules:
        return rows
    return [mask_row(r, rules, counts) for r in rows]


# ── base : règles d'une organisation ───────────────────────────────────────────

RULES_SQL = """
    SELECT id, name, builtin, pattern, replacement, scope_query, targets FROM sensitive_rules
     WHERE org_id = %s AND enabled ORDER BY position, id
"""
SEED_FLAG = "sensitive_rules_seeded"


def builtin_rows(new_org: bool) -> list[tuple]:
    """(nom, builtin, remplacement, actif, position) des règles livrées."""
    return [(BUILTIN_LABELS[k], k, replacement_for(k), new_org and k == "secrets", i)
            for i, k in enumerate(BUILTIN_ORDER)]


async def seed_builtin(conn, org_id: int) -> bool:
    """Pose les règles livrées une seule fois par organisation ; vrai si elles viennent d'être posées."""
    from ..db import fetch_one
    row = await fetch_one(conn, f"SELECT settings ? '{SEED_FLAG}' AS done FROM orgs WHERE id = %s", (org_id,))
    if row is None or row["done"]:
        return False
    row = await fetch_one(conn, f"SELECT settings ? '{SEED_FLAG}' AS done FROM orgs WHERE id = %s FOR UPDATE",
                          (org_id,))
    if row is None or row["done"]:
        return False
    # Organisation « nouvelle » : créée après la pose du schéma 006 (version 0.4.0).
    new = await fetch_one(conn, """
        SELECT o.created_at >= v.applied_at AS new FROM orgs o JOIN schema_version v ON v.version = 6
         WHERE o.id = %s
    """, (org_id,))
    rows = builtin_rows(bool(new and new["new"]))
    await conn.execute("""
        INSERT INTO sensitive_rules (org_id, name, builtin, replacement, enabled, position)
        SELECT %s, x.name, x.builtin, x.repl, x.enabled, x.pos
          FROM unnest(%s::text[], %s::text[], %s::text[], %s::bool[], %s::int[]) AS x(name, builtin, repl, enabled, pos)
    """, (org_id, [r[0] for r in rows], [r[1] for r in rows], [r[2] for r in rows], [r[3] for r in rows],
          [r[4] for r in rows]))
    await conn.execute(f"UPDATE orgs SET settings = settings || '{{\"{SEED_FLAG}\": true}}'::jsonb WHERE id = %s",
                       (org_id,))
    return True


async def load_rules(conn, org_id: int, bad: set) -> RuleSet:
    from ..db import fetch_all
    rules = []
    for r in await fetch_all(conn, RULES_SQL, (org_id,)):
        try:
            rules.append(compile_rule(r))
        except QueryError as e:
            if r["id"] not in bad:
                bad.add(r["id"])
                log.warning("règle de masquage %s (« %s ») ignorée : %s", r["id"], r["name"], e)
    return RuleSet(rules)


async def write_counts(conn, pending: dict) -> None:
    items = sorted(pending.items())
    await conn.execute("""
        UPDATE sensitive_rules r SET matched_total = r.matched_total + x.n
          FROM unnest(%s::bigint[], %s::bigint[], %s::bigint[]) AS x(org_id, id, n)
         WHERE r.id = x.id AND r.org_id = x.org_id
    """, ([o for (o, _), _ in items], [i for (_, i), _ in items], [n for _, n in items]))
