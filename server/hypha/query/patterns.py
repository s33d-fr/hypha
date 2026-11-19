"""Gabarit d'un message de journal (contrat § 23) : motifs, et empreinte d'une erreur sans pile (§ 25).

Les parties variables sont remplacées en une seule passe ; à une même position, la première
alternative qui correspond gagne, dans cet ordre : horodatage `<ts>`, UUID `<uuid>`, adresse IP
`<ip>`, chaîne entre guillemets `<str>`, URL ou chemin `<path>`, hexadécimal `<hex>`, nombre
`<num>`. Un nombre collé derrière une lettre (`sda1`, `v2`) reste tel quel ; derrière un
séparateur (`worker-3`, `1234ms`) il est remplacé.
"""
import re

_TS = (r"\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}(?::\d{2}(?:[.,]\d+)?)?(?:Z|[+-]\d{2}:?\d{2})?"
       r"|\d{1,2}/[A-Z][a-z]{2}/\d{4}:\d{2}:\d{2}:\d{2}(?:\.\d+)?(?: [+-]\d{4})?"
       r"|(?:Mon|Tue|Wed|Thu|Fri|Sat|Sun),? \d{1,2} [A-Z][a-z]{2} \d{4} \d{2}:\d{2}:\d{2}(?: [A-Z]{3}| [+-]\d{4})?"
       r"|(?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec) [ \d]\d \d{2}:\d{2}:\d{2}"
       r"|\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}"
       r"|(?<![\d:])\d{2}:\d{2}:\d{2}(?:[.,]\d+)?(?![\d:])")
_UUID = r"\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b"
_IPV4 = r"(?<![\w.])(?:\d{1,3}\.){3}\d{1,3}(?::\d{1,5})?(?![\w]|\.\d)"
_H = r"[0-9a-fA-F]{1,4}"
_IPV6 = (rf"(?<![\w:])(?:(?:{_H}:){{7}}{_H}|(?:{_H}:){{1,7}}:(?:{_H}(?::{_H}){{0,6}})?|::{_H}(?::{_H}){{0,6}})"
         r"(?![\w:])")
_STR = r'"(?:[^"\\\n]|\\.){0,500}"|(?<!\w)\'(?:[^\'\\\n]|\\.){0,200}\'(?!\w)'
_PATH = r"\b[a-z][a-z0-9+.\-]{1,15}://[^\s\"'<>]+|(?<![\w.])(?:/[\w.\-@~%+=:]+){2,}/?|\b[A-Za-z]:\\[^\s\"'<>]+"
_HEX = r"\b0x[0-9a-fA-F]+\b|\b(?=[0-9a-fA-F]*\d)(?=[0-9a-fA-F]*[a-fA-F])[0-9a-fA-F]{8,}\b"
_NUM = r"(?<![A-Za-z_\d])[-+]?\d+(?:[.,]\d+)*"

_TEMPLATE_RE = re.compile(
    f"(?P<ts>{_TS})|(?P<uuid>{_UUID})|(?P<ip>{_IPV4}|{_IPV6})|(?P<str>{_STR})|(?P<path>{_PATH})"
    f"|(?P<hex>{_HEX})|(?P<num>{_NUM})")
_SPACES_RE = re.compile(r"\s+")
_PLACEHOLDER_RE = re.compile(r"<(?:ts|uuid|ip|str|path|hex|num)>")
MAX_TEMPLATE = 500
PLACEHOLDERS = ("<ts>", "<uuid>", "<ip>", "<str>", "<path>", "<hex>", "<num>")


def _placeholder(m: re.Match) -> str:
    return f"<{m.lastgroup}>"


def template(message: str | None) -> str:
    """Gabarit du message : parties variables remplacées, espaces réduits, 500 caractères au plus."""
    if not message:
        return ""
    text = message if len(message) <= 4096 else message[:4096]
    out = _TEMPLATE_RE.sub(_placeholder, text)
    return _SPACES_RE.sub(" ", out).strip()[:MAX_TEMPLATE]


def _quote(fragment: str) -> str:
    return '"' + fragment.replace("\\", "\\\\").replace('"', '\\"') + '"'


def pattern_query(tpl: str, service: str | None = None, max_fragments: int = 4) -> str:
    """Recherche (langage du § 7) qui retrouve les lignes d'un motif : ses parties fixes les plus
    longues, chacune entre guillemets (la ligne les contient toutes), et le service s'il est unique."""
    fragments = [f.strip() for f in _PLACEHOLDER_RE.split(tpl)]
    fragments = [f for f in fragments if sum(c.isalnum() for c in f) >= 3]
    chosen = sorted(fragments, key=len, reverse=True)[:max_fragments]
    ordered = [f for f in fragments if f in chosen][:max_fragments]
    parts = []
    if service and re.fullmatch(r"[A-Za-z0-9_.\-/]+", service):
        parts.append(f"service:{service}")
    parts += [_quote(f[:200]) for f in ordered]
    return " ".join(parts)


def group_patterns(rows: list[dict], limit: int = 50) -> list[dict]:
    """Regroupe des lignes (message, status, service, ts) par gabarit, du plus fréquent au moins fréquent.

    Rend [{pattern, count, share, status:{…}, services:[…], sample, first_ts, last_ts, query}] ;
    `share` est la part de l'échantillon, `sample` le message le plus récent du motif.
    """
    groups: dict[str, dict] = {}
    total = 0
    for r in rows:
        total += 1
        tpl = template(r.get("message"))
        g = groups.get(tpl)
        ts = r.get("ts")
        if g is None:
            g = groups[tpl] = {"pattern": tpl, "count": 0, "status": {}, "services": {}, "sample": None,
                               "first_ts": ts, "last_ts": ts}
        g["count"] += 1
        status = r.get("status") or "info"
        g["status"][status] = g["status"].get(status, 0) + 1
        service = r.get("service")
        if service:
            g["services"][service] = g["services"].get(service, 0) + 1
        if ts is not None:
            if g["last_ts"] is None or ts >= g["last_ts"]:
                g["last_ts"] = ts
                g["sample"] = r.get("message")
            if g["first_ts"] is None or ts < g["first_ts"]:
                g["first_ts"] = ts
        if g["sample"] is None:
            g["sample"] = r.get("message")
    out = []
    for g in sorted(groups.values(), key=lambda x: -x["count"])[:limit]:
        services = sorted(g["services"], key=lambda s: -g["services"][s])
        out.append({
            "pattern": g["pattern"], "count": g["count"], "share": g["count"] / total if total else 0.0,
            "status": g["status"], "services": services[:10],
            "sample": (g["sample"] or "")[:2000], "first_ts": g["first_ts"], "last_ts": g["last_ts"],
            "query": pattern_query(g["pattern"], services[0] if len(services) == 1 else None),
        })
    return out
