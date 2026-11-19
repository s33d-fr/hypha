"""Collecte du contexte d'une explication (contrat § 10) : bornée, et filtrée sur l'organisation.

Chaque requête porte `org_id`, y compris celles qui suivent un identifiant d'hôte ou de trace
déjà vérifié : une explication ne lit jamais les données d'une autre organisation.
"""
import json
from datetime import datetime, timedelta, timezone

from ..db import fetch_all, fetch_one
from ..errors import not_found
from ..util import ms_to_dt
from . import context as C
from .request import Request

WINDOW = timedelta(minutes=30)
CHECK_WINDOW = timedelta(minutes=15)
EVENT_WINDOW = timedelta(hours=24)
PROCESS_LOOKBACK = timedelta(days=3)
MAX_LOG_ROWS = 1000

HOST_METRICS = ["system.cpu.usage", "system.mem.pct_used", "system.load.norm.1", "system.disk.in_use",
                "system.swap.pct_used", "system.net.bytes_rcvd", "system.net.bytes_sent",
                "system.io.read_bytes", "system.io.write_bytes"]
SEVERITY_ORDER = ["emergency", "alert", "critical", "error", "warning", "notice", "info", "debug"]
HOST_FIELDS = """id, hostname, os, platform, platform_version, kernel, arch, cpu_cores, mem_total, tags, meta,
                 last_seen, boot_time"""


async def _host(conn, org_id: int, host_id: int | None = None, hostname: str | None = None) -> dict | None:
    if host_id is not None:
        return await fetch_one(conn, f"SELECT {HOST_FIELDS} FROM hosts WHERE org_id = %s AND id = %s",
                               (org_id, host_id))
    if hostname:
        return await fetch_one(conn, f"SELECT {HOST_FIELDS} FROM hosts WHERE org_id = %s AND hostname = %s "
                                     "ORDER BY last_seen DESC LIMIT 1", (org_id, hostname))
    return None


async def host_sections(conn, ctx: C.Context, org_id: int, host: dict, start: datetime, end: datetime) -> None:
    """Hôte, contrôles non OK, journaux, métriques, processus, événements, dans cet ordre."""
    hid = host["id"]
    ctx.summary["host"] = host["hostname"]
    ctx.add("host", C.format_host(host))

    checks = await fetch_all(conn, """
        SELECT DISTINCT ON (check_name, tags) check_name AS name, status, message, tags, ts
          FROM check_runs
         WHERE org_id = %s AND host_id = %s AND ts > %s AND ts <= %s
         ORDER BY check_name, tags, ts DESC
    """, (org_id, hid, end - CHECK_WINDOW, end))
    bad = [c for c in checks if c["status"] != 0]
    ctx.summary["checks"] = len(bad)
    ctx.add("checks", C.format_checks(bad))

    logs = await fetch_all(conn, """
        SELECT ts, status, service, left(message, 600) AS message
          FROM logs
         WHERE org_id = %s AND host_id = %s AND ts >= %s AND ts < %s
         ORDER BY array_position(%s::text[], status), ts DESC
         LIMIT %s
    """, (org_id, hid, start, end, SEVERITY_ORDER, MAX_LOG_ROWS))
    groups = C.group_logs(logs)
    ctx.summary["logs"] = sum(g.count for g in groups)
    ctx.add("logs", C.format_logs(groups))

    metrics = await fetch_all(conn, """
        SELECT s.name, s.tags, s.unit, avg(p.value) AS avg, max(p.value) AS max
          FROM metric_series s JOIN metric_points p ON p.series_id = s.id
         WHERE s.org_id = %s AND s.host_id = %s AND s.name = ANY(%s::text[]) AND p.ts >= %s AND p.ts < %s
         GROUP BY s.id, s.name, s.tags, s.unit
    """, (org_id, hid, HOST_METRICS, end - WINDOW, end))
    ctx.summary["metrics"] = sorted({m["name"] for m in metrics})
    ctx.add("metrics", C.format_metrics(metrics))

    snap = await fetch_one(conn, """
        SELECT max(ts) AS ts FROM process_snapshots
         WHERE org_id = %s AND host_id = %s AND ts <= %s AND ts > %s
    """, (org_id, hid, end, end - PROCESS_LOOKBACK))
    procs: list[dict] = []
    if snap and snap["ts"]:
        top = """
            SELECT pid, name, username AS "user", cmdline, cpu_pct, mem_rss
              FROM process_snapshots WHERE org_id = %s AND host_id = %s AND ts = %s
             ORDER BY {col} DESC NULLS LAST LIMIT 8
        """
        by_cpu = await fetch_all(conn, top.format(col="cpu_pct"), (org_id, hid, snap["ts"]))
        by_mem = await fetch_all(conn, top.format(col="mem_rss"), (org_id, hid, snap["ts"]))
        procs = C.pick_processes(by_cpu, by_mem)
    ctx.summary["processes"] = len(procs)
    ctx.add("processes", C.format_processes(procs))

    events = await fetch_all(conn, """
        SELECT ts, source, event_type, title, alert_type FROM events
         WHERE org_id = %s AND host_id = %s AND ts > %s AND ts <= %s
         ORDER BY ts DESC LIMIT 10
    """, (org_id, hid, end - EVENT_WINDOW, end))
    ctx.summary["events"] = len(events)
    ctx.add("events", C.format_events(events))


async def collect(conn, org_id: int, req: Request) -> C.Context:
    """Rend le contexte ; lève 404 si l'objet visé n'existe pas dans l'organisation."""
    ctx = C.Context(req.lang, summary={"host": None, "logs": 0, "checks": 0, "events": 0, "processes": 0,
                                       "metrics": []})
    now = datetime.now(timezone.utc)
    host = None
    start, end = now - WINDOW, now

    if req.kind == "alert":
        monitor = await fetch_one(conn, "SELECT id, name, type, query, thresholds FROM monitors "
                                        "WHERE org_id = %s AND id = %s", (org_id, req.monitor_id))
        if monitor is None:
            raise not_found("moniteur introuvable")
        state = await fetch_one(conn, """
            SELECT ms.state, ms.value, ms.since, ms.last_eval
              FROM monitor_states ms JOIN monitors m ON m.id = ms.monitor_id AND m.org_id = %s
             WHERE ms.monitor_id = %s AND ms.group_key = %s
        """, (org_id, req.monitor_id, req.group))
        history = await fetch_all(conn, """
            SELECT ts, from_state, to_state, value FROM alert_events
             WHERE org_id = %s AND monitor_id = %s AND group_key = %s ORDER BY ts DESC LIMIT 10
        """, (org_id, req.monitor_id, req.group))
        ctx.summary["monitor"] = monitor["name"]
        ctx.add("monitor", C.format_monitor(monitor, req.group, state, history))
        host = await _host(conn, org_id, hostname=C.host_from_group(req.group))

    elif req.kind == "host":
        host = await _host(conn, org_id, host_id=req.host_id)
        if host is None:
            raise not_found("hôte introuvable")

    elif req.kind == "log":
        at = ms_to_dt(req.ts)
        line = await fetch_one(conn, """
            SELECT id, ts, host_id, hostname, service, source, status, message, attributes, trace_id
              FROM logs WHERE org_id = %s AND id = %s AND ts >= %s AND ts <= %s
        """, (org_id, req.log_id, at - timedelta(seconds=1), at + timedelta(seconds=1)))
        if line is None:
            raise not_found("ligne de journal introuvable")
        subject = [f"- ts: {C.fmt_ts(line['ts'])}", f"- status: {line['status']}",
                   f"- service: {line['service'] or '?'}", f"- source: {line['source'] or '?'}",
                   f"- message: {C.clip(line['message'], 2000)}"]
        if line["attributes"]:
            subject.append("- attributes: " + C.clip(json.dumps(line["attributes"], ensure_ascii=False, default=str), 1500))
        ctx.add("subject", "\n".join(subject))
        host = await _host(conn, org_id, host_id=line["host_id"]) if line["host_id"] else \
            await _host(conn, org_id, hostname=line["hostname"])
        start, end = line["ts"] - WINDOW / 2, min(line["ts"] + WINDOW / 2, now)

    elif req.kind == "trace":
        spans = await fetch_all(conn, """
            SELECT span_id, parent_span_id, service, name, coalesce(resource, name) AS resource, kind, ts,
                   duration_ns::float8 / 1e6 AS duration_ms, status, status_message, host_id, hostname
              FROM spans WHERE org_id = %s AND trace_id = %s ORDER BY ts LIMIT 5000
        """, (org_id, req.trace_id))
        if not spans:
            raise not_found("trace introuvable")
        chosen = C.trace_spans(spans)
        root = C.critical_path(spans)[0]
        errors = sum(1 for s in spans if s["status"] == "error")
        ctx.summary["spans"] = len(spans)
        ctx.add("subject", f"- trace_id: {req.trace_id}\n- spans: {len(spans)}, errors: {errors}\n"
                           f"- root: service={root['service']} name={C.clip(root['name'], 120)} "
                           f"duration={C.fmt_num(root['duration_ms'])}ms")
        ctx.add("trace", C.format_spans(chosen))
        linked = await fetch_all(conn, """
            SELECT ts, status, service, left(message, 600) AS message FROM logs
             WHERE org_id = %s AND trace_id = %s ORDER BY ts LIMIT 500
        """, (org_id, req.trace_id))
        ctx.add("trace_logs", C.format_logs(C.group_logs(linked)))
        anchor = next((s for s in spans if s["host_id"] or s["hostname"]), None)
        if anchor:
            host = await _host(conn, org_id, host_id=anchor["host_id"]) if anchor["host_id"] else \
                await _host(conn, org_id, hostname=anchor["hostname"])
        start, end = root["ts"] - WINDOW / 2, min(root["ts"] + WINDOW / 2, now)

    elif req.kind == "error":
        # § 25 : pile et dernières occurrences du problème, puis l'hôte de la dernière occurrence.
        from ..api.error_tracking import load_issue, occurrences
        issue = await load_issue(conn, org_id, req.issue_id)
        last = await occurrences(conn, org_id, issue)
        ctx.summary["issue"] = issue["error_type"] or C.clip(issue["message"], 80)
        ctx.summary["occurrences"] = len(last)
        ctx.add("subject", C.format_issue(issue))
        ctx.add("stack", C.clip_lines(issue.get("stack"), 4000))
        ctx.add("occurrences", C.format_occurrences(last))
        host = await _host(conn, org_id, host_id=issue["last_host_id"]) if issue["last_host_id"] else None
        anchor = issue["last_seen"]
        start, end = anchor - WINDOW / 2, min(anchor + WINDOW / 2, now)

    if host is not None:
        await host_sections(conn, ctx, org_id, host, start, end)
    return ctx
