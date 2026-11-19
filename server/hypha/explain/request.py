"""Corps de `POST /api/v1/explain` (contrat § 10), validé sans accès à la base."""
import re
from dataclasses import dataclass

from .prompt import normalize_lang

KINDS = ("alert", "host", "log", "trace", "error")
_TRACE_RE = re.compile(r"^[0-9a-f]{16,32}$")
_GROUP_RE = re.compile(r"^(\*|[a-z0-9_.\-/]+:[^,]*(,[a-z0-9_.\-/]+:[^,]*)*)$")


@dataclass
class Request:
    kind: str
    lang: str
    monitor_id: int | None = None
    group: str = "*"
    host_id: int | None = None
    log_id: int | None = None
    ts: int | None = None
    trace_id: str | None = None
    issue_id: int | None = None       # § 25 : problème du suivi des erreurs


def _int(data: dict, key: str) -> int:
    v = data.get(key)
    if isinstance(v, bool) or not isinstance(v, int) or v < 0:
        raise ValueError(f"{key} (entier) est obligatoire")
    return v


def parse_request(data) -> Request:
    if not isinstance(data, dict):
        raise ValueError("le corps doit être un objet JSON")
    kind = data.get("kind")
    if kind not in KINDS:
        raise ValueError(f"kind attendu : {'|'.join(KINDS)}")
    req = Request(kind=kind, lang=normalize_lang(data.get("lang")))
    if kind == "alert":
        req.monitor_id = _int(data, "monitor_id")
        group = data.get("group") or "*"
        if not isinstance(group, str) or len(group) > 1000 or not _GROUP_RE.match(group):
            raise ValueError("group doit être '*' ou de la forme clé:valeur,clé:valeur")
        req.group = group
    elif kind == "host":
        req.host_id = _int(data, "host_id")
    elif kind == "log":
        req.log_id = _int(data, "log_id")
        req.ts = _int(data, "ts")         # l'horodatage borne la recherche à une partition
    elif kind == "error":
        req.issue_id = _int(data, "issue_id")
    else:
        trace_id = str(data.get("trace_id") or "").lower()
        if not _TRACE_RE.match(trace_id):
            raise ValueError("trace_id (16 à 32 caractères hexadécimaux) est obligatoire")
        req.trace_id = trace_id
    return req
