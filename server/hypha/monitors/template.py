"""Gabarits des messages de moniteur (contrat § 18), sans accès à la base.

Variables : {{name}} {{group}} {{host}} {{value}} {{state}} {{priority}} {{threshold}} {{url}}
{{last_value}} {{duration}} {{eta}} {{tags.<clé>}} (espaces tolérés : {{ name }}).
Blocs : {{#is_alert}}…{{/is_alert}}, {{#is_warning}}, {{#is_recovery}}, {{#is_nodata}},
{{#is_renotify}}, {{#is_escalation}} ; {{^is_alert}}…{{/is_alert}} rend le bloc quand la condition
est fausse. Mentions : `@canal-<id>` ajoute le canal <id> pour ce message (retirée du texte envoyé).

Une variable inconnue reste telle quelle (la faute de frappe se voit dans le message reçu) ; une
variable connue mais sans valeur (étiquette absente) devient vide.
"""
import re

FLAGS = ("is_alert", "is_warning", "is_recovery", "is_nodata", "is_renotify", "is_escalation")
VARIABLES = ("name", "group", "host", "value", "state", "priority", "threshold", "url", "last_value",
             "duration", "eta")

_BLOCK_RE = re.compile(r"\{\{\s*([#^])\s*(" + "|".join(FLAGS) + r")\s*\}\}(.*?)\{\{\s*/\s*\2\s*\}\}", re.S)
_VAR_RE = re.compile(r"\{\{\s*([a-z_]+)(?:\.([a-z0-9_.\-/]{1,100}))?\s*\}\}")
_MENTION_RE = re.compile(r"(?<![\w@])@canal-(\d{1,18})\b")
_SPACES_RE = re.compile(r"[ \t]+\n")


def flags_for(state: str, previous: str | None = None, renotify: bool = False, escalation: bool = False) -> dict:
    return {"is_alert": state == "ALERT", "is_warning": state == "WARN",
            "is_recovery": state == "OK" and previous not in (None, "OK"),
            "is_nodata": state == "NODATA", "is_renotify": renotify, "is_escalation": escalation}


def _blocks(text: str, flags: dict) -> str:
    # Blocs imbriqués de conditions différentes : on répète jusqu'à stabilité (borné).
    for _ in range(10):
        new = _BLOCK_RE.sub(lambda m: m.group(3) if bool(flags.get(m.group(2))) == (m.group(1) == "#") else "", text)
        if new == text:
            break
        text = new
    return text


def render(template: str, ctx: dict) -> str:
    """Rend le gabarit. `ctx` porte les variables, `tags` (dict) et les drapeaux is_* (facultatifs)."""
    text = _blocks(template or "", ctx)
    tags = ctx.get("tags") or {}

    def var(m):
        name, sub = m.group(1), m.group(2)
        if name == "tags" and sub:
            return str(tags.get(sub, ""))
        if sub is None and name in VARIABLES:
            value = ctx.get(name)
            return "" if value is None else str(value)
        return m.group(0)

    return _VAR_RE.sub(var, text)


def mentions(text: str) -> list[int]:
    """Canaux cités par `@canal-<id>`, dans l'ordre, sans doublon."""
    out: list[int] = []
    for m in _MENTION_RE.finditer(text or ""):
        cid = int(m.group(1))
        if cid not in out:
            out.append(cid)
    return out


def strip_mentions(text: str) -> str:
    return _SPACES_RE.sub("\n", _MENTION_RE.sub("", text or "")).strip()


def human_duration(seconds: float | None) -> str:
    """« 2 j 4 h », « 3 h 5 min », « 42 s » : deux unités au plus."""
    if seconds is None:
        return ""
    s = int(max(0, round(seconds)))
    if s < 60:
        return f"{s} s"
    units = (("j", 86400), ("h", 3600), ("min", 60))
    parts = []
    for label, size in units:
        if s >= size:
            parts.append(f"{s // size} {label}")
            s %= size
        if len(parts) == 2:
            break
    if len(parts) == 1 and parts[0].endswith("min") and s:
        parts.append(f"{s} s")
    return " ".join(parts)


def eta_text(seconds: float | None) -> str:
    """Délai d'une prévision : « dans 2 j 4 h », « déjà franchi », « jamais »."""
    if seconds is None or seconds < 0:
        return "jamais"
    if seconds == 0:
        return "déjà franchi"
    return "dans " + human_duration(seconds)
