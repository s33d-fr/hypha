"""Suivi des erreurs (contrat § 25) : extraction à l'ingestion, empreinte, regroupement par lot.

Sources : span `status=error` portant un événement `exception` ; ligne de journal `error` ou plus
grave dont le message ou les attributs portent une pile (Python, Java, Go, Node) ou
`error.kind` / `error.stack` ; événement RUM `error`.

Empreinte = sha1(origine, service, type, 5 trames normalisées) ; à défaut de pile, le message
réduit en gabarit (§ 23). Trames retenues : les 5 plus proches de l'erreur (les dernières d'une
trace Python, les premières ailleurs), sans numéro de ligne, adresse ni valeur.

L'empreinte est aussi posée sur l'occurrence (`error.fingerprint` dans ses attributs) : les
dernières occurrences d'un problème se retrouvent par l'index des attributs, sans table de plus.
"""
import hashlib
import json
import re
from dataclasses import dataclass, field
from datetime import datetime, timezone

from ..query.patterns import template
from .body import LOG_COLUMNS

SEVERE = frozenset(("emergency", "alert", "critical", "error"))
MAX_MESSAGE = 1000
MAX_STACK = 16 * 1024
FRAMES = 5
_COL = {n: i for i, n in enumerate(LOG_COLUMNS)}

_PY_FRAME = re.compile(r'File "(?P<file>[^"]+)", line \d+, in (?P<fn>\S+)')
_JAVA_FRAME = re.compile(r"^\s*at (?P<fn>[\w$.<>/\-]+)\((?P<file>[^:)]*)(?::\d+)?\)", re.M)
_NODE_FRAME = re.compile(r"^\s*at (?:(?P<fn>[^\s(]+(?: \[as [^\]]+\])?) )?\(?(?P<file>[^\s()]+?):\d+:\d+\)?\s*$", re.M)
_GO_FRAME = re.compile(r"^(?P<fn>[\w./*()\-\[\]{}]+)\([^\n]*\)\n\s+(?P<file>[^\s:]+):\d+", re.M)
_NORM = re.compile(r"0x[0-9a-fA-F]+|(?<![A-Za-z_\d])\d+")    # adresses, numéros (Foo$1), pas s33d

_PY_LAST = re.compile(r"^(?P<type>[A-Za-z_][\w.]*)(?::\s?(?P<msg>.*))?$")
_JAVA_HEAD = re.compile(r"(?:Exception in thread \"[^\"]*\" )?(?P<type>(?:[\w$]+\.)+[\w$]*(?:Exception|Error|Throwable)"
                        r"|[A-Z][\w$]*(?:Exception|Error))(?::\s*(?P<msg>[^\n]*))?")
_GO_PANIC = re.compile(r"^panic: (?P<msg>[^\n]*)", re.M)
_NODE_HEAD = re.compile(r"^(?P<type>[A-Z]\w*(?:Error|Exception))(?::\s*(?P<msg>[^\n]*))?", re.M)


def _norm(frame: str) -> str:
    return _NORM.sub("", frame).strip()


def stack_kind(text: str | None) -> str | None:
    """Langage d'une pile reconnue dans le texte, ou None (test de sous-chaîne d'abord)."""
    if not text:
        return None
    if "Traceback (most recent call last)" in text:
        return "python"
    if "goroutine " in text and "[running]" in text:
        return "go"
    if "\tat " in text or "\n    at " in text or "\n  at " in text:
        if _JAVA_FRAME.search(text):
            return "java"
        if _NODE_FRAME.search(text):
            return "node"
    return None


def frames(stack: str, kind: str | None = None) -> list[str]:
    """Les 5 trames normalisées les plus proches de l'erreur."""
    kind = kind or stack_kind(stack)
    if kind == "python":
        found = [f"{m['file']}:{m['fn']}" for m in _PY_FRAME.finditer(stack)]
        return [_norm(f) for f in found[-FRAMES:][::-1]]
    if kind == "go":
        found = [f"{m['fn']} {m['file']}" for m in _GO_FRAME.finditer(stack)]
    elif kind == "java":
        found = [f"{m['fn']}({m['file']})" for m in _JAVA_FRAME.finditer(stack)]
    elif kind == "node":
        found = [f"{m['fn'] or '<anonyme>'} {m['file']}" for m in _NODE_FRAME.finditer(stack)]
    else:
        found = []
    return [_norm(f) for f in found[:FRAMES]]


def error_type_message(text: str, kind: str | None) -> tuple[str | None, str]:
    """(type, message) d'une pile ; le message par défaut est la première ligne du texte."""
    first = (text or "").strip().split("\n", 1)[0].strip()
    if kind == "python":
        for line in reversed(text.rstrip().split("\n")):
            line = line.strip()
            if not line or line.startswith(("File ", "Traceback", "^", "~")):
                continue
            m = _PY_LAST.match(line)
            if m and not line.startswith(" "):
                return m["type"], (m["msg"] or line)
            break
    elif kind == "java":
        m = _JAVA_HEAD.search(text)
        if m:
            return m["type"], m["msg"] or first
    elif kind == "go":
        m = _GO_PANIC.search(text)
        if m:
            return "panic", m["msg"]
        return "goroutine", first
    elif kind == "node":
        m = _NODE_HEAD.search(text)
        if m:
            return m["type"], m["msg"] or first
    return None, first


def fingerprint(origin: str, service: str | None, error_type: str | None, stack_frames: list[str],
                message: str | None) -> str:
    parts = [origin, service or "", error_type or ""] + (stack_frames or [template(message)])
    return hashlib.sha1("\x1f".join(parts).encode("utf-8")).hexdigest()


@dataclass
class Occurrence:
    origin: str
    service: str | None
    error_type: str | None
    message: str
    stack: str | None
    ts: datetime
    fingerprint: str
    trace_id: str | None = None
    host_id: int | None = None
    index: int | None = None          # rang de la ligne dans le lot (journaux)


def _make(origin, service, error_type, message, stack, ts, trace_id=None, host_id=None, index=None) -> Occurrence:
    kind = stack_kind(stack) if stack else None
    fp = fingerprint(origin, service, error_type, frames(stack, kind) if stack else [], message)
    return Occurrence(origin, service, (error_type or None) and error_type[:200], (message or "")[:MAX_MESSAGE],
                      stack[:MAX_STACK] if stack else None, ts, fp, trace_id, host_id, index)


def _attr(attrs: dict, dotted: str):
    if dotted in attrs:
        return attrs[dotted]
    cur = attrs
    for seg in dotted.split("."):
        if not isinstance(cur, dict) or seg not in cur:
            return None
        cur = cur[seg]
    return cur


# ── journaux ───────────────────────────────────────────────────────────────────

def from_log_row(row: tuple, index: int) -> Occurrence | None:
    if row[_COL["status"]] not in SEVERE:
        return None
    message = row[_COL["message"]] or ""
    raw = row[_COL["attributes"]] or ""
    stack = message if stack_kind(message) else None
    etype = msg = None
    if '"error' in raw or '"exception' in raw:
        attrs = json.loads(raw)
        a_stack = _attr(attrs, "error.stack") or _attr(attrs, "exception.stacktrace")
        a_kind = _attr(attrs, "error.kind") or _attr(attrs, "error.type") or _attr(attrs, "exception.type")
        a_msg = _attr(attrs, "error.message") or _attr(attrs, "exception.message")
        if isinstance(a_stack, str) and a_stack.strip():
            stack = a_stack
        if isinstance(a_kind, str) and a_kind.strip():
            etype = a_kind.strip()
        if isinstance(a_msg, str) and a_msg.strip():
            msg = a_msg.strip()
    if stack is None and etype is None:
        return None
    if stack:
        t, m = error_type_message(stack, stack_kind(stack))
        etype, msg = etype or t, msg or m
    msg = msg or message.strip().split("\n", 1)[0]
    return _make("log", row[_COL["service"]], etype, msg, stack, row[_COL["ts"]],
                 row[_COL["trace_id"]], row[_COL["host_id"]], index)


def tag_log_row(row: tuple, fp: str, log_id: int | None = None) -> tuple:
    """Pose `error.fingerprint` dans les attributs (imbriqué, ou à plat si `error` n'est pas un objet)."""
    attrs = json.loads(row[_COL["attributes"]] or "{}")
    err = attrs.get("error")
    if err is None:
        attrs["error"] = {"fingerprint": fp}
    elif isinstance(err, dict):
        err.setdefault("fingerprint", fp)
    else:
        attrs.setdefault("error.fingerprint", fp)
    out = list(row)
    out[_COL["attributes"]] = json.dumps(attrs, ensure_ascii=False, separators=(",", ":"))
    return tuple(out) + ((log_id,) if log_id is not None else ())


# ── traces ─────────────────────────────────────────────────────────────────────

def from_span(span: dict, ts: datetime, host_id: int | None) -> Occurrence | None:
    """Span OTLP converti (ingest/otlp.convert_traces) ; seulement s'il porte un événement exception."""
    if span.get("status") != "error":
        return None
    for e in span.get("events") or []:
        if e.get("name") != "exception":
            continue
        a = e.get("attributes") or {}
        stack = a.get("exception.stacktrace")
        stack = stack if isinstance(stack, str) and stack.strip() else None
        etype = a.get("exception.type")
        message = a.get("exception.message") or span.get("status_message") or span.get("name") or ""
        return _make("trace", span.get("service"), str(etype) if etype else None, str(message), stack, ts,
                     span.get("trace_id"), host_id)
    return None


# ── RUM ────────────────────────────────────────────────────────────────────────

def from_rum(name: str | None, attributes: dict, ts: datetime, service: str) -> Occurrence:
    a = attributes or {}
    stack = a.get("stack") or _attr(a, "error.stack")
    stack = stack if isinstance(stack, str) and stack.strip() else None
    etype = a.get("type") or _attr(a, "error.type") or _attr(a, "error.kind")
    message = name or a.get("message") or ""
    if stack and not etype:
        etype, _ = error_type_message(stack, "node")
    return _make("rum", service, str(etype) if etype else None, str(message), stack, ts)


# ── regroupement et écriture ───────────────────────────────────────────────────

@dataclass
class IssueDelta:
    fingerprint: str
    origin: str
    service: str | None
    error_type: str | None
    message: str
    stack: str | None
    first_seen: datetime
    last_seen: datetime
    count: int = 0
    trace_id: str | None = None
    log_id: int | None = None
    host_id: int | None = None
    hours: dict = field(default_factory=dict)


def _hour(ts: datetime) -> datetime:
    return ts.astimezone(timezone.utc).replace(minute=0, second=0, microsecond=0)


def aggregate(occurrences: list[Occurrence], log_ids: dict[int, int] | None = None) -> list[IssueDelta]:
    """Un delta par empreinte : nombre, bornes, dernière occurrence, comptes par heure."""
    out: dict[str, IssueDelta] = {}
    for o in occurrences:
        d = out.get(o.fingerprint)
        log_id = (log_ids or {}).get(o.index) if o.index is not None else None
        if d is None:
            d = out[o.fingerprint] = IssueDelta(o.fingerprint, o.origin, o.service, o.error_type, o.message,
                                                o.stack, o.ts, o.ts)
        d.count += 1
        if o.ts < d.first_seen:
            d.first_seen = o.ts
        if o.ts >= d.last_seen:
            d.last_seen = o.ts
            d.message, d.stack = o.message, o.stack or d.stack
            d.trace_id = o.trace_id or d.trace_id
            d.host_id = o.host_id or d.host_id
            d.log_id = log_id or d.log_id
        h = _hour(o.ts)
        d.hours[h] = d.hours.get(h, 0) + 1
    return sorted(out.values(), key=lambda d: d.fingerprint)


UPSERT_ISSUES = """
    INSERT INTO error_issues AS i (org_id, fingerprint, origin, service, error_type, message, stack, first_seen,
                                   last_seen, count, last_trace_id, last_log_id, last_host_id)
    SELECT %s, x.fp, x.origin, x.service, x.etype, x.message, x.stack, x.first_seen, x.last_seen, x.n,
           x.trace_id, x.log_id, x.host_id
      FROM unnest(%s::text[], %s::text[], %s::text[], %s::text[], %s::text[], %s::text[], %s::timestamptz[],
                  %s::timestamptz[], %s::bigint[], %s::text[], %s::bigint[], %s::bigint[])
           AS x(fp, origin, service, etype, message, stack, first_seen, last_seen, n, trace_id, log_id, host_id)
     ORDER BY x.fp
    ON CONFLICT (org_id, fingerprint) DO UPDATE SET
       first_seen = least(i.first_seen, EXCLUDED.first_seen),
       last_seen = greatest(i.last_seen, EXCLUDED.last_seen),
       count = i.count + EXCLUDED.count,
       message = CASE WHEN EXCLUDED.last_seen >= i.last_seen THEN EXCLUDED.message ELSE i.message END,
       stack = coalesce(CASE WHEN EXCLUDED.last_seen >= i.last_seen THEN EXCLUDED.stack END, i.stack),
       last_trace_id = CASE WHEN EXCLUDED.last_seen >= i.last_seen
                            THEN coalesce(EXCLUDED.last_trace_id, i.last_trace_id) ELSE i.last_trace_id END,
       last_log_id = CASE WHEN EXCLUDED.last_seen >= i.last_seen
                          THEN coalesce(EXCLUDED.last_log_id, i.last_log_id) ELSE i.last_log_id END,
       last_host_id = CASE WHEN EXCLUDED.last_seen >= i.last_seen
                           THEN coalesce(EXCLUDED.last_host_id, i.last_host_id) ELSE i.last_host_id END,
       status = CASE WHEN i.status = 'resolved' AND EXCLUDED.last_seen > coalesce(i.resolved_at, '-infinity')
                     THEN 'open' ELSE i.status END,
       regressed_at = CASE WHEN i.status = 'resolved' AND EXCLUDED.last_seen > coalesce(i.resolved_at, '-infinity')
                           THEN EXCLUDED.last_seen ELSE i.regressed_at END
    RETURNING id, fingerprint
"""

UPSERT_HITS = """
    INSERT INTO error_issue_hits (issue_id, hour, count)
    SELECT x.id, x.hour, x.n FROM unnest(%s::bigint[], %s::timestamptz[], %s::bigint[]) AS x(id, hour, n)
     ORDER BY 1, 2
    ON CONFLICT (issue_id, hour) DO UPDATE SET count = error_issue_hits.count + EXCLUDED.count
"""


HITS_RETENTION_DAYS = 92


async def purge_hits(db) -> int:
    """Worker : comptes horaires au-delà de 92 jours (les problèmes eux-mêmes restent)."""
    async with db.writer() as conn:
        cur = await conn.execute("DELETE FROM error_issue_hits WHERE hour < now() - make_interval(days => %s)",
                                 (HITS_RETENTION_DAYS,))
        return cur.rowcount


async def upsert(conn, org_id: int, deltas: list[IssueDelta]) -> int:
    """Écrit les problèmes et leurs comptes horaires ; rend le nombre de problèmes touchés."""
    from ..db import fetch_all
    if not deltas:
        return 0
    rows = await fetch_all(conn, UPSERT_ISSUES, (
        org_id, [d.fingerprint for d in deltas], [d.origin for d in deltas], [d.service for d in deltas],
        [d.error_type for d in deltas], [d.message for d in deltas], [d.stack for d in deltas],
        [d.first_seen for d in deltas], [d.last_seen for d in deltas], [d.count for d in deltas],
        [d.trace_id for d in deltas], [d.log_id for d in deltas], [d.host_id for d in deltas]))
    ids = {r["fingerprint"]: r["id"] for r in rows}
    hit_ids, hours, counts = [], [], []
    for d in deltas:
        issue_id = ids.get(d.fingerprint)
        if issue_id is None:
            continue
        for h, n in sorted(d.hours.items()):
            hit_ids.append(issue_id)
            hours.append(h)
            counts.append(n)
    if hit_ids:
        await conn.execute(UPSERT_HITS, (hit_ids, hours, counts))
    return len(ids)
