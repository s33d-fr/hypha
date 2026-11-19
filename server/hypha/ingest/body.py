"""Décodage et validation des corps envoyés par l'agent (contrat § 3). Aucun accès à la base.

Règle : un corps mal formé dans sa structure (pas d'objet `agent`, liste attendue absente) vaut
400 ; un élément isolé invalide ou hors fenêtre de temps est écarté et compté dans `dropped`,
le reste du lot est écrit.
"""
import json
import math
import uuid
import zlib
from dataclasses import dataclass, field
from datetime import datetime

from ..util import METRIC_NAME_RE, clean_tags, clean_text, jdump, ms_to_dt, now_ms

MAX_BODY = 10 * 1024 * 1024
WINDOW_PAST_MS = 14 * 24 * 3600 * 1000
WINDOW_FUTURE_MS = 10 * 60 * 1000

LOG_STATUSES = ("emergency", "alert", "critical", "error", "warning", "notice", "info", "debug")
STATUS_ALIASES = {
    "emerg": "emergency", "panic": "emergency", "crit": "critical", "fatal": "critical",
    "err": "error", "eror": "error", "warn": "warning", "information": "info",
    "informational": "info", "trace": "debug", "verbose": "debug", "dbg": "debug",
    "ok": "info", "success": "info", "severe": "error",
}
SYSLOG_NUMBERS = {0: "emergency", 1: "alert", 2: "critical", 3: "error", 4: "warning",
                  5: "notice", 6: "info", 7: "debug"}
METRIC_TYPES = ("gauge", "count", "rate", "distribution")
EVENT_ALERT_TYPES = ("info", "success", "warning", "error")
RUM_TYPES = ("view", "action", "error", "resource", "long_task", "vital")


class BodyError(Exception):
    def __init__(self, status: int, code: str, detail: str):
        super().__init__(detail)
        self.status = status
        self.code = code
        self.detail = detail


# ── décompression et JSON ──────────────────────────────────────────────────────

def decompress(raw: bytes, encoding: str | None, limit: int = MAX_BODY) -> bytes:
    """Décompresse en s'arrêtant à `limit` octets : une bombe gzip ne remplit pas la mémoire."""
    enc = (encoding or "").strip().lower()
    if enc in ("", "identity"):
        if len(raw) > limit:
            raise BodyError(413, "payload_too_large", f"corps de plus de {limit} octets")
        return raw
    if enc in ("gzip", "x-gzip"):
        d = zlib.decompressobj(16 + zlib.MAX_WBITS)
    elif enc == "deflate":
        d = zlib.decompressobj(32 + zlib.MAX_WBITS)   # zlib ou gzip, détection automatique
    else:
        raise BodyError(400, "unsupported_encoding", f"Content-Encoding non pris en charge : {enc}")
    try:
        out = d.decompress(raw, limit + 1)
    except zlib.error as e:
        raise BodyError(400, "invalid_gzip", f"corps compressé illisible : {e}")
    if len(out) > limit or d.unconsumed_tail:
        raise BodyError(413, "payload_too_large", f"corps décompressé de plus de {limit} octets")
    if not d.eof:
        raise BodyError(400, "invalid_gzip", "corps compressé tronqué")
    return out


def parse_json_object(data: bytes) -> dict:
    try:
        obj = json.loads(data)
    except (ValueError, UnicodeDecodeError) as e:
        raise BodyError(400, "invalid_json", f"JSON invalide : {e}")
    if not isinstance(obj, dict):
        raise BodyError(400, "invalid_body", "le corps doit être un objet JSON")
    return obj


# ── fenêtre de temps et valeurs ────────────────────────────────────────────────

@dataclass
class Window:
    lo: int
    hi: int

    @classmethod
    def around(cls, now: int | None = None) -> "Window":
        now = now_ms() if now is None else now
        return cls(now - WINDOW_PAST_MS, now + WINDOW_FUTURE_MS)

    def ok(self, ts_ms) -> bool:
        return self.lo <= ts_ms <= self.hi


def as_ts(value) -> int | None:
    """Horodatage en ms (entier ou flottant fini), sinon None."""
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        return None
    if isinstance(value, float) and not math.isfinite(value):
        return None
    return int(value)


def as_float(value) -> float | None:
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        return None
    f = float(value)
    return f if math.isfinite(f) else None


def as_int(value, lo: int | None = None, hi: int | None = None) -> int | None:
    if isinstance(value, bool):
        return None
    if isinstance(value, float):
        if not math.isfinite(value):
            return None
        value = int(value)
    if not isinstance(value, int):
        return None
    if lo is not None and value < lo:
        return None
    if hi is not None and value > hi:
        return None
    return value


def opt_text(value, max_len: int) -> str | None:
    if value is None:
        return None
    if not isinstance(value, str):
        value = str(value)
    value = clean_text(value, max_len)
    return value or None


def normalize_log_status(value) -> str:
    if isinstance(value, int) and not isinstance(value, bool):
        return SYSLOG_NUMBERS.get(value, "info")
    if not isinstance(value, str):
        return "info"
    s = value.strip().lower()
    if s in LOG_STATUSES:
        return s
    return STATUS_ALIASES.get(s, "info")


# ── agent ──────────────────────────────────────────────────────────────────────

@dataclass
class AgentInfo:
    id: str
    hostname: str
    version: str | None = None
    os: str | None = None
    arch: str | None = None
    via: str | None = None      # équipement réseau (§ 14) : id de l'agent qui l'interroge


def parse_agent(body: dict) -> AgentInfo:
    a = body.get("agent")
    if not isinstance(a, dict):
        raise BodyError(400, "invalid_body", "objet `agent` absent")
    try:
        agent_id = str(uuid.UUID(str(a.get("id", ""))))
    except ValueError:
        raise BodyError(400, "invalid_body", "`agent.id` doit être un UUID")
    hostname = a.get("hostname")
    if not isinstance(hostname, str) or not hostname.strip():
        raise BodyError(400, "invalid_body", "`agent.hostname` absent")
    via = None
    if a.get("via") not in (None, ""):
        try:
            via = str(uuid.UUID(str(a["via"])))
        except ValueError:
            raise BodyError(400, "invalid_body", "`agent.via` doit être un UUID")
        if via == agent_id:
            raise BodyError(400, "invalid_body", "`agent.via` désigne l'agent lui-même")
    return AgentInfo(
        id=agent_id,
        hostname=clean_text(hostname.strip(), 255),
        version=opt_text(a.get("version"), 50),
        os=opt_text(a.get("os"), 50),
        arch=opt_text(a.get("arch"), 50),
        via=via,
    )


def require_list(body: dict, key: str) -> list:
    value = body.get(key)
    if not isinstance(value, list):
        raise BodyError(400, "invalid_body", f"liste `{key}` absente")
    return value


# ── § 3.1 host ─────────────────────────────────────────────────────────────────

# Hôtes sans agent installé, rattachés à l'agent qui les interroge (`agent.via`).
DEVICE_KINDS = ("network_device", "cloud_resource")


def parse_host(body: dict, agent: AgentInfo) -> dict:
    h = body.get("host")
    if not isinstance(h, dict):
        raise BodyError(400, "invalid_body", "objet `host` absent")
    ips = h.get("ip_addresses")
    ips = [clean_text(str(ip), 64) for ip in ips[:64]] if isinstance(ips, list) else []
    boot = as_ts(h.get("boot_time"))
    meta = h.get("meta") if isinstance(h.get("meta"), dict) else {}
    status = h.get("status") if isinstance(h.get("status"), dict) else {}
    # § 14 : un équipement réseau (pas d'agent installé dessus) ; § 27 : capacités et mise à jour.
    # § 34-35 : ressource lue par un connecteur (serveur OVH, hôte ESXi, VM), même rattachement.
    device_kind = h.get("kind") in DEVICE_KINDS or agent.via is not None
    device = h.get("device") if isinstance(h.get("device"), dict) else {}
    if device_kind and not ips and isinstance(device.get("address"), str) and device["address"].strip():
        ips = [clean_text(device["address"], 64)]
    capabilities = h.get("capabilities") if isinstance(h.get("capabilities"), dict) else None
    update = status.get("update") if isinstance(status.get("update"), dict) else None
    return {
        "hostname": opt_text(h.get("hostname"), 255) or agent.hostname,
        "os": opt_text(h.get("os"), 50) or agent.os,
        "platform": opt_text(h.get("platform"), 100),
        "platform_version": opt_text(h.get("platform_version"), 100),
        "kernel": opt_text(h.get("kernel"), 200),
        "arch": opt_text(h.get("arch"), 50) or agent.arch,
        "cpu_cores": as_int(h.get("cpu_cores"), 0, 100_000),
        "cpu_model": opt_text(h.get("cpu_model"), 300),
        "mem_total": as_int(h.get("mem_total"), 0),
        "ip_addresses": jdump(ips),
        "tags": jdump(clean_tags(h.get("tags"))),
        "agent_version": opt_text(h.get("agent_version"), 50) or agent.version,
        "boot_time": ms_to_dt(boot) if boot and boot > 0 else None,
        "meta": jdump(meta),
        "status": jdump(status),
        "kind": (h.get("kind") if h.get("kind") in DEVICE_KINDS else "network_device") if device_kind else "agent",
        "device": jdump(device) if device_kind else None,
        "capabilities": jdump(capabilities) if capabilities is not None and not device_kind else None,
        "update_status": jdump(update) if update is not None and not device_kind else None,
        "config_hash": opt_text(h.get("config_hash"), 128) if not device_kind else None,
    }


# ── § 3.2 series ───────────────────────────────────────────────────────────────

@dataclass
class SeriesBatch:
    name: str
    type: str
    unit: str | None
    tags: dict
    points: list[tuple[datetime, float]] = field(default_factory=list)


def parse_series(items: list, window: Window, default_host: str | None) -> tuple[list[SeriesBatch], int]:
    out: list[SeriesBatch] = []
    dropped = 0
    for item in items:
        if not isinstance(item, dict):
            dropped += 1
            continue
        points = item.get("points")
        npoints = len(points) if isinstance(points, list) else 1
        name = item.get("metric")
        if not isinstance(name, str) or not METRIC_NAME_RE.match(name) or not isinstance(points, list):
            dropped += npoints
            continue
        mtype = item.get("type")
        mtype = mtype if mtype in METRIC_TYPES else "gauge"
        tags = clean_tags(item.get("tags"))
        if default_host and "host" not in tags:
            # « L'hôte est toujours l'étiquette host » : on la pose si l'agent l'a omise.
            tags["host"] = default_host
        batch = SeriesBatch(name=name, type=mtype, unit=opt_text(item.get("unit"), 50), tags=tags)
        for p in points:
            if not isinstance(p, (list, tuple)) or len(p) != 2:
                dropped += 1
                continue
            ts, value = as_ts(p[0]), as_float(p[1])
            if ts is None or value is None or not window.ok(ts):
                dropped += 1
                continue
            batch.points.append((ms_to_dt(ts), value))
        if batch.points:
            out.append(batch)
    return out, dropped


# ── § 3.3 logs ─────────────────────────────────────────────────────────────────

LOG_COLUMNS = ("org_id", "ts", "host_id", "hostname", "service", "source", "status", "message",
               "attributes", "tags", "trace_id", "span_id")
MAX_MESSAGE = 32 * 1024


def log_row(org_id, host_id, hostname, ts_ms, message, status, service, source, attributes, tags,
            trace_id, span_id) -> tuple:
    return (org_id, ms_to_dt(ts_ms), host_id, hostname, service, source, status,
            clean_text(message, MAX_MESSAGE), jdump(attributes or {}), jdump(tags or {}),
            trace_id, span_id)


def parse_logs(items: list, window: Window, org_id: int, host_id: int, hostname: str,
               now: int | None = None) -> tuple[list[tuple], int]:
    now = now_ms() if now is None else now
    rows, dropped = [], 0
    for item in items:
        if not isinstance(item, dict):
            dropped += 1
            continue
        message = item.get("message")
        if message is None:
            dropped += 1
            continue
        if not isinstance(message, str):
            message = json.dumps(message, ensure_ascii=False)
        ts = as_ts(item.get("ts"))
        if ts is None:
            ts = now                      # une ligne sans date reste plus utile que perdue
        if not window.ok(ts):
            dropped += 1
            continue
        attributes = item.get("attributes")
        rows.append(log_row(
            org_id, host_id, hostname, ts, message,
            normalize_log_status(item.get("status")),
            opt_text(item.get("service"), 100),
            opt_text(item.get("source"), 100),
            attributes if isinstance(attributes, dict) else {},
            clean_tags(item.get("tags")),
            opt_text(item.get("trace_id"), 64),
            opt_text(item.get("span_id"), 32),
        ))
    return rows, dropped


# ── § 3.4 checks ───────────────────────────────────────────────────────────────

CHECK_COLUMNS = ("org_id", "host_id", "ts", "check_name", "status", "message", "tags")


def parse_checks(items: list, window: Window, org_id: int, host_id: int,
                 now: int | None = None) -> tuple[list[tuple], int]:
    now = now_ms() if now is None else now
    rows, dropped = [], 0
    for item in items:
        if not isinstance(item, dict):
            dropped += 1
            continue
        name = opt_text(item.get("name"), 200)
        status = as_int(item.get("status"), 0, 3)
        ts = as_ts(item.get("ts"))
        ts = now if ts is None else ts
        if not name or status is None or not window.ok(ts):
            dropped += 1
            continue
        rows.append((org_id, host_id, ms_to_dt(ts), name, status,
                     opt_text(item.get("message"), 4000), jdump(clean_tags(item.get("tags")))))
    return rows, dropped


# ── § 3.5 events ───────────────────────────────────────────────────────────────

EVENT_COLUMNS = ("org_id", "ts", "host_id", "source", "event_type", "title", "text", "alert_type",
                 "priority", "tags", "aggregation_key")


def normalize_alert_type(value) -> str:
    s = str(value or "info").strip().lower()
    if s in EVENT_ALERT_TYPES:
        return s
    return {"warn": "warning", "err": "error", "critical": "error", "ok": "success"}.get(s, "info")


def event_row(org_id, host_id, item: dict, ts_ms: int, default_source: str) -> tuple | None:
    title = opt_text(item.get("title"), 500)
    if not title:
        return None
    priority = item.get("priority")
    priority = priority if priority in ("normal", "low") else "normal"
    return (org_id, ms_to_dt(ts_ms), host_id, opt_text(item.get("source"), 50) or default_source,
            opt_text(item.get("type"), 100), title, opt_text(item.get("text"), 8000),
            normalize_alert_type(item.get("alert_type")), priority,
            jdump(clean_tags(item.get("tags"))), opt_text(item.get("aggregation_key"), 200))


def parse_events(items: list, window: Window, org_id: int, host_id: int,
                 now: int | None = None) -> tuple[list[tuple], int]:
    now = now_ms() if now is None else now
    rows, dropped = [], 0
    for item in items:
        if not isinstance(item, dict):
            dropped += 1
            continue
        ts = as_ts(item.get("ts"))
        ts = now if ts is None else ts
        row = event_row(org_id, host_id, item, ts, "agent") if window.ok(ts) else None
        if row is None:
            dropped += 1
            continue
        rows.append(row)
    return rows, dropped


# ── § 3.6 / 3.7 / 3.8 instantanés ──────────────────────────────────────────────

PROCESS_COLUMNS = ("org_id", "host_id", "ts", "pid", "ppid", "name", "username", "cmdline", "state",
                   "cpu_pct", "mem_rss", "mem_pct", "threads", "started_at", "io_read_bps",
                   "io_write_bps")
CONNECTION_COLUMNS = ("org_id", "host_id", "ts", "proto", "direction", "laddr", "lport", "raddr",
                      "rport", "state", "pid", "process", "count")
CONTAINER_COLUMNS = ("org_id", "host_id", "ts", "container_id", "name", "image", "state", "cpu_pct",
                     "mem_usage", "mem_limit", "net_rx_bps", "net_tx_bps", "labels")
INT32 = 2 ** 31 - 1


def snapshot_ts(body: dict, window: Window) -> datetime | None:
    """`ts` de tête d'un instantané ; None s'il manque ou sort de la fenêtre."""
    ts = as_ts(body.get("ts"))
    if ts is None or not window.ok(ts):
        return None
    return ms_to_dt(ts)


def parse_processes(items: list, ts: datetime, org_id: int, host_id: int) -> tuple[list[tuple], int]:
    rows, dropped = [], 0
    for p in items[:5000]:
        if not isinstance(p, dict):
            dropped += 1
            continue
        pid = as_int(p.get("pid"), 0, INT32)
        name = opt_text(p.get("name"), 300)
        if pid is None or not name:
            dropped += 1
            continue
        started = as_ts(p.get("started"))
        rows.append((org_id, host_id, ts, pid, as_int(p.get("ppid"), 0, INT32), name,
                     opt_text(p.get("user"), 100), opt_text(p.get("cmdline"), 4000),
                     opt_text(p.get("state"), 20), as_float(p.get("cpu_pct")),
                     as_int(p.get("mem_rss"), 0), as_float(p.get("mem_pct")),
                     as_int(p.get("threads"), 0, INT32),
                     ms_to_dt(started) if started and started > 0 else None,
                     as_int(p.get("io_read_bps"), 0), as_int(p.get("io_write_bps"), 0)))
    dropped += max(0, len(items) - 5000)
    return rows, dropped


def parse_connections(items: list, ts: datetime, org_id: int, host_id: int) -> tuple[list[tuple], int]:
    rows, dropped = [], 0
    for c in items[:2000]:
        if not isinstance(c, dict):
            dropped += 1
            continue
        proto = opt_text(c.get("proto"), 10)
        if not proto:
            dropped += 1
            continue
        direction = c.get("direction")
        direction = direction if direction in ("incoming", "outgoing", "listening") else None
        rows.append((org_id, host_id, ts, proto.lower(), direction, opt_text(c.get("laddr"), 64),
                     as_int(c.get("lport"), 0, 65535), opt_text(c.get("raddr"), 64),
                     as_int(c.get("rport"), 0, 65535), opt_text(c.get("state"), 30),
                     as_int(c.get("pid"), 0, INT32), opt_text(c.get("process"), 300),
                     as_int(c.get("count"), 1, INT32) or 1))
    dropped += max(0, len(items) - 2000)
    return rows, dropped


def parse_containers(items: list, ts: datetime, org_id: int, host_id: int) -> tuple[list[tuple], int]:
    rows, dropped = [], 0
    for c in items[:2000]:
        if not isinstance(c, dict):
            dropped += 1
            continue
        cid = opt_text(c.get("id"), 128)
        if not cid:
            dropped += 1
            continue
        labels = c.get("labels") if isinstance(c.get("labels"), dict) else {}
        rows.append((org_id, host_id, ts, cid, opt_text(c.get("name"), 300),
                     opt_text(c.get("image"), 500), opt_text(c.get("state"), 30),
                     as_float(c.get("cpu_pct")), as_int(c.get("mem_usage"), 0),
                     as_int(c.get("mem_limit"), 0), as_int(c.get("net_rx_bps"), 0),
                     as_int(c.get("net_tx_bps"), 0), jdump(labels)))
    dropped += max(0, len(items) - 2000)
    return rows, dropped


# ── § 3.10 RUM ─────────────────────────────────────────────────────────────────

RUM_COLUMNS = ("org_id", "app_id", "ts", "session_id", "view_url", "event_type", "name",
               "duration_ms", "attributes", "user_agent", "country")


def parse_rum(body: dict, window: Window, org_id: int, app_id: int, user_agent: str | None,
              country: str | None, now: int | None = None) -> tuple[list[tuple], int]:
    now = now_ms() if now is None else now
    session_id = opt_text(body.get("session_id"), 100)
    events = body.get("events")
    if not session_id or not isinstance(events, list):
        raise BodyError(400, "invalid_body", "`session_id` et `events` sont obligatoires")
    rows, dropped = [], 0
    for e in events[:1000]:
        if not isinstance(e, dict) or e.get("type") not in RUM_TYPES:
            dropped += 1
            continue
        ts = as_ts(e.get("ts"))
        ts = now if ts is None else ts
        if not window.ok(ts):
            dropped += 1
            continue
        attributes = e.get("attributes") if isinstance(e.get("attributes"), dict) else {}
        rows.append((org_id, app_id, ms_to_dt(ts), session_id, opt_text(e.get("url"), 2000),
                     e["type"], opt_text(e.get("name"), 500), as_float(e.get("duration_ms")),
                     jdump(attributes), opt_text(user_agent, 500), opt_text(country, 2)))
    dropped += max(0, len(events) - 1000)
    return rows, dropped
