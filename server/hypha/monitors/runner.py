"""Évaluation des moniteurs par le worker : mesure par type, puis application du plan d'états.

Ordre d'un passage : maintenances (événements de début et de fin), moniteurs `host` (leur ALERT
supprime les autres groupes de l'hôte, § 17), moniteurs ordinaires, puis composites (qui lisent
l'état des autres). Les compteurs des tolérances (`require_consecutive`, `recovery_consecutive`)
sont gardés dans `monitor_states` (`pending_state`, `pending_count`) : un changement de worker
ne perd pas une confirmation en cours.
"""
import asyncio
import logging
import re
from datetime import datetime, timedelta, timezone
from urllib.parse import quote

import psycopg
from psycopg_pool import PoolTimeout

from ..config import Settings
from ..db import Database, fetch_all, fetch_one
from ..errors import QueryError
from ..query import logs_ql, metrics_exec, metrics_ql as mql
from ..util import TTLCache, bucket_floor, group_key, jdump, like_escape, ms_to_dt, nice_step, now_ms, parse_group_key
from . import algorithms as A
from . import composite
from . import downtime as D
from . import evaluate as ev
from . import maintenance, service_link, template
from .notify import Notice, Notifier

log = logging.getLogger("hypha.monitors")

ENTRY = "(parent_span_id IS NULL OR kind IN ('server', 'consumer'))"
LOG_GROUP_COLUMNS = {"service": "service", "host": "hostname", "status": "status", "source": "source"}
EVENT_GROUP_COLUMNS = {"host": "h.hostname", "source": "e.source", "type": "e.event_type",
                       "alert_type": "e.alert_type", "priority": "e.priority"}
HISTORY = TTLCache(900, max_items=5000)       # historiques des anomalies (15 min)
FORECASTS = TTLCache(300, max_items=5000)     # séries des prévisions (5 min)
STREAKS: dict[int, dict] = {}                 # moniteur → groupe → (état visé, évaluations)
OUT_SINCE_LOOKBACK = timedelta(days=30)
ERROR_STATUSES = ["emergency", "alert", "critical", "error"]


def _prev_states(rows: list[dict]) -> dict[str, ev.PrevState]:
    return {r["group_key"]: ev.PrevState(r["state"], r["value"], r["since"], r["last_eval"], r["last_notified"],
                                         acked=r.get("acked_at") is not None, flapping=bool(r.get("flapping")),
                                         escalation_step=r.get("escalation_step") or 0,
                                         suppressed=r.get("suppressed"))
            for r in rows}


def _numeric(prev: dict, thresholds: dict, comparator: str):
    def state(group: str, value: float):
        old = prev.get(group)
        return ev.threshold_state(old.state if old else None, value, thresholds, comparator)
    return state


def _clean(points: list) -> list:
    return [(ts, v) for ts, v in points if v is not None]


def _span_detail(detail: dict | None, group: dict, points: list) -> None:
    if detail is not None and len(detail.setdefault("series", [])) < 50:
        detail["series"].append({"group": group, "points": points})


async def _anomaly(conn, m: dict, q: dict, prev: dict, end: int, detail: dict | None) -> dict:
    mq = mql.parse(q["q"])
    alg, season = q["algorithm"], q["seasonality"]
    if alg == "basic":
        step, source, span_s = 60, mql.RAW, A.BASIC_LOOKBACK_S
    elif season == "hourly":
        step, source, span_s = 60, mql.RAW, 86400
    else:
        step, source = 300, mql.R5M
        span_s = 7 * 86400 if season == "daily" else 21 * 86400
    w_start = bucket_floor(end - q["window_s"] * 1000, step)
    key = ("anomaly", m.get("id"), q["q"], alg, season)
    cached = None if detail is not None else HISTORY.get(key)
    if cached is None:
        rows = await metrics_exec.execute(conn, m["org_id"], mq, w_start - (span_s + step) * 1000, w_start, source, step)
        cached = {"until": w_start, "groups": {group_key(mq.group_by, r["group"]): (r["group"], _clean(r["points"]))
                                               for r in rows}, "models": {}}
        if detail is None:
            HISTORY.set(key, cached)
    recent = await metrics_exec.execute(conn, m["org_id"], mq, cached["until"], end, mql.RAW, step)
    recent_by = {group_key(mq.group_by, r["group"]): (r["group"], _clean(r["points"])) for r in recent}
    state = _numeric(prev, m["thresholds"] or {}, ">=")
    measured = {}
    for g in set(cached["groups"]) | set(recent_by):
        gdict, old_pts = cached["groups"].get(g) or recent_by[g]
        pts = old_pts + (recent_by.get(g, (gdict, []))[1])
        history = [p for p in pts if p[0] < w_start]
        window = [p for p in pts if p[0] >= w_start]
        if not window or not A.enough_history(alg, history, w_start, A.SEASONS[season]):
            measured[g] = None
            continue
        model = None
        if alg == "robust":
            model = cached["models"].get(g)
            if model is None:
                model = cached["models"][g] = A.decompose(history, A.SEASONS[season], step)
        bounds = A.anomaly_bounds(alg, history, window, q["bounds"], season, step, model)
        share = A.anomaly_share(bounds, q["direction"])
        measured[g] = None if share is None else (state(g, share), share)
        _span_detail(detail, gdict, [[ts, v] for ts, v in pts if ts >= w_start - 6 * 3600 * 1000])
        if detail is not None and "bounds" not in detail:
            detail["bounds"] = [[ts, lo, hi] for ts, _, lo, hi in bounds]
    return measured


async def _forecast(conn, m: dict, q: dict, end: int, detail: dict | None) -> dict:
    mq = mql.parse(q["q"])
    start = end - q["history_s"] * 1000
    source = mql.choose_source(start, end, now_ms())
    step = max(source.min_step, nice_step(q["history_s"] / 120))
    if q["algorithm"] == "seasonal":
        step = max(source.min_step, min(step, 3600))
    key = ("forecast", m.get("id"), q["q"], q["history_s"], step)
    rows = None if detail is not None else FORECASTS.get(key)
    if rows is None:
        rows = await metrics_exec.execute(conn, m["org_id"], mq, start, end, source, step)
        if detail is None:
            FORECASTS.set(key, rows)
    th = m["thresholds"] or {}
    now_s = end / 1000
    measured = {}
    for r in rows:
        g = group_key(mq.group_by, r["group"])
        pts = _clean(r["points"])
        model = A.fit_forecast(pts, q["algorithm"])
        if model is None:
            measured[g] = None
            continue
        eta_c = A.time_to_breach(model, th["critical"], q["comparator"], q["horizon_s"], now_s)
        eta_w = (A.time_to_breach(model, th["warning"], q["comparator"], q["horizon_s"], now_s)
                 if th.get("warning") is not None else None)
        measured[g] = (A.forecast_state(eta_c, eta_w, q["horizon_s"]), eta_c)
        _span_detail(detail, r["group"], [[ts, v] for ts, v in pts])
        if detail is not None and "forecast" not in detail:
            detail["forecast"] = A.forecast_points(model, now_s, q["horizon_s"])
    return measured


async def _outlier(conn, m: dict, q: dict, end: int, detail: dict | None) -> dict:
    mq = mql.parse(q["q"])
    step = nice_step(max(15, q["window_s"] / 30))
    rows = await metrics_exec.execute(conn, m["org_id"], mq, end - q["window_s"] * 1000, end, mql.RAW, step)
    series = {group_key(mq.group_by, r["group"]): dict(_clean(r["points"])) for r in rows}
    for r in rows:
        _span_detail(detail, r["group"], r["points"])
    shares = (A.outliers_dbscan if q["algorithm"] == "dbscan" else A.outliers_mad)(series, q["tolerance"])
    measured = {}
    for g in series:
        v = shares.get(g)
        measured[g] = None if v is None else (ev.ALERT if v > q["pct"] else ev.OK, v)
    return measured


async def _change(conn, m: dict, q: dict, prev: dict, end: int, detail: dict | None) -> dict:
    mq = mql.parse(q["q"])
    w = q["window_s"] * 1000
    step = nice_step(max(15, q["window_s"] / 30))
    now = now_ms()
    cur = await metrics_exec.execute(conn, m["org_id"], mq, end - w, end, mql.choose_source(end - w, end, now), step)
    then_end = end - q["shift_s"] * 1000
    old = await metrics_exec.execute(conn, m["org_id"], mq, then_end - w, then_end,
                                     mql.choose_source(then_end - w, then_end, now), step)
    before = {group_key(mq.group_by, r["group"]): metrics_exec.window_aggregate(r["points"], q["aggregation"])
              for r in old}
    state = _numeric(prev, m["thresholds"] or {}, q["comparator"])
    measured = {}
    for r in cur:
        g = group_key(mq.group_by, r["group"])
        v = A.change_value(metrics_exec.window_aggregate(r["points"], q["aggregation"]), before.get(g), q["mode"])
        measured[g] = None if v is None else (state(g, v), v)
        _span_detail(detail, r["group"], r["points"])
    return measured


async def _event_count(conn, m: dict, q: dict, prev: dict, end: int) -> dict:
    frag, qparams = logs_ql.compile_query(q.get("q", ""), logs_ql.EventsResolver())
    by = q.get("by", [])
    exprs, eparams = [], []
    for i, k in enumerate(by):
        if k in EVENT_GROUP_COLUMNS:
            exprs.append(f"{EVENT_GROUP_COLUMNS[k]} AS g{i}")
        else:
            exprs.append(f"e.tags ->> %s AS g{i}")
            eparams.append(k)
    group = f"GROUP BY {', '.join(str(i + 1) for i in range(len(by)))}" if by else ""
    rows = await fetch_all(conn, f"""
        SELECT {', '.join(exprs + ['count(*) AS n'])}
          FROM events e LEFT JOIN hosts h ON h.id = e.host_id AND h.org_id = e.org_id
         WHERE e.org_id = %s AND e.ts > %s AND e.ts <= %s AND {frag} {group}
    """, eparams + [m["org_id"], ms_to_dt(end - q["window_s"] * 1000), ms_to_dt(end)] + qparams)
    state = _numeric(prev, m["thresholds"] or {}, q.get("comparator", ">"))
    return _counts(rows, by, prev, state)


def _counts(rows: list[dict], by: list[str], prev: dict, state) -> dict:
    measured = {}
    if not by:
        n = float(rows[0]["n"]) if rows else 0.0
        measured["*"] = (state("*", n), n)
        return measured
    for r in rows:
        g = group_key(by, {k: r[f"g{i}"] for i, k in enumerate(by)})
        measured[g] = (state(g, float(r["n"])), float(r["n"]))
    for g in prev:                     # groupe connu sans ligne sur la fenêtre : zéro, pas « sans données »
        if g not in measured:
            measured[g] = (state(g, 0.0), 0.0)
    return measured


async def _process(conn, m: dict, q: dict) -> dict:
    name, match = q["name"], q["match"]
    if match == "exact":
        cond, mparams = "p.name = %s", [name]
    elif match == "contains":
        pattern = "%" + like_escape(name) + "%"
        cond, mparams = "(p.name ILIKE %s OR coalesce(p.cmdline, '') ILIKE %s)", [pattern, pattern]
    else:
        cond, mparams = "(p.name ~ %s OR coalesce(p.cmdline, '') ~ %s)", [name, name]
    where, wparams = [], []
    tags = dict(q.get("tags") or {})
    if "host" in tags:
        where.append("h.hostname = %s")
        wparams.append(tags.pop("host"))
    if tags:
        where.append("h.tags @> %s::jsonb")
        wparams.append(jdump(tags))
    org = m["org_id"]
    rows = await fetch_all(conn, f"""
        SELECT h.hostname, count(*) FILTER (WHERE {cond}) AS n
          FROM (SELECT host_id, max(ts) AS ts FROM process_snapshots
                 WHERE org_id = %s AND ts > now() - interval '10 minutes' GROUP BY host_id) last
          JOIN hosts h ON h.id = last.host_id AND h.org_id = %s
          JOIN process_snapshots p ON p.org_id = %s AND p.host_id = last.host_id AND p.ts = last.ts
                                  AND p.ts > now() - interval '10 minutes'
         WHERE {' AND '.join(where) or 'TRUE'}
         GROUP BY h.hostname
    """, mparams + [org, org, org] + wparams)
    measured = {}
    for r in rows:
        n = float(r["n"])
        bad = n < q["min"] or (q.get("max") is not None and n > q["max"])
        measured[f"host:{r['hostname']}"] = (ev.ALERT if bad else ev.OK, n)
    return measured


async def _error_tracking(conn, m: dict, q: dict, prev: dict, end: int) -> dict:
    start = ms_to_dt(end - q["window_s"] * 1000)
    where, params = ["i.org_id = %s", "i.status = 'open'", "i.last_seen > %s", "h.hour >= date_trunc('hour', %s::timestamptz)"], \
        [m["org_id"], start, start]
    if q.get("service"):
        where.append("i.service = %s")
        params.append(q["service"])
    if q.get("new_only", True):
        where.append("(i.first_seen > %s OR i.regressed_at > %s)")
        params += [start, start]
    # Occurrences au pas des compteurs horaires (`error_issue_hits`) : exact pour un problème
    # nouveau ou régressé dans la fenêtre, arrondi à l'heure pour un problème plus ancien.
    row = await fetch_one(conn, f"""
        SELECT coalesce(sum(h.count), 0)::float8 AS n
          FROM error_issues i JOIN error_issue_hits h ON h.issue_id = i.id
         WHERE {' AND '.join(where)}
    """, params)
    n = float(row["n"]) if row else 0.0
    state = _numeric(prev, m["thresholds"] or {}, q.get("comparator", ">"))
    return {"*": (state("*", n), n)}


async def _composite(conn, m: dict, q: dict) -> dict:
    ids = q.get("ids") or sorted(composite.ids(composite.parse(q["expression"])))
    rows = await fetch_all(conn, """
        SELECT m.id, ms.state FROM monitors m LEFT JOIN monitor_states ms ON ms.monitor_id = m.id
         WHERE m.org_id = %s AND m.id = ANY(%s::bigint[]) AND m.enabled
    """, (m["org_id"], ids))
    if not rows:
        return {"*": None}
    states: dict[int, list[str]] = {}
    for r in rows:
        states.setdefault(r["id"], [])
        if r["state"]:
            states[r["id"]].append(r["state"])
    truth = {}
    for mid, st in states.items():
        w = ev.worst(st) if st else ev.OK
        truth[mid] = w == ev.ALERT or (q.get("warn_as_true") and w == ev.WARN)
    hit = composite.evaluate(composite.parse(q["expression"]), truth)
    return {"*": (ev.ALERT if hit else ev.OK, 1.0 if hit else 0.0)}


def _log_select(by: list[str]) -> tuple[list[str], list]:
    exprs, params = [], []
    for i, k in enumerate(by):
        if k.startswith("@"):
            exprs.append(f"coalesce(attributes #>> %s::text[], attributes ->> %s) AS g{i}")
            params += [logs_ql.attribute_path(k[1:]), k[1:]]
        else:
            exprs.append(f"{LOG_GROUP_COLUMNS[k]} AS g{i}")
    return exprs, params


async def measure(conn, m: dict, prev: dict[str, ev.PrevState], detail: dict | None = None) -> dict:
    """Valeur et état de chaque groupe : {groupe: (état, valeur)} ou {groupe: None} sans données.

    `detail` (aperçu) reçoit les séries, bornes et prévisions utiles au graphique.
    """
    q, org, mtype = m["query"], m["org_id"], m["type"]
    thresholds = m["thresholds"] or {}
    delay_s = ev.Options.of(m.get("options")).evaluation_delay_s
    end = now_ms() - delay_s * 1000
    measured: dict = {}

    if mtype == "metric":
        mq = mql.parse(q["q"])
        state = _numeric(prev, thresholds, q.get("comparator", ">"))
        window = q.get("window_s", 300)
        step = nice_step(max(15, window / 30))
        for r in await metrics_exec.execute(conn, org, mq, end - window * 1000, end, mql.RAW, step):
            g = group_key(mq.group_by, r["group"])
            v = metrics_exec.window_aggregate(r["points"], q.get("aggregation", "avg"))
            measured[g] = None if v is None else (state(g, v), v)
            _span_detail(detail, r["group"], r["points"])

    elif mtype == "host":
        # « Hôte sans nouvelles » = agent muet. Équipements SNMP et ressources de connecteurs ont leurs
        # propres contrôles (snmp.can_poll, ovhcloud/vsphere.can_connect) : une VM supprimée n'est pas
        # un agent tombé. `kinds` élargit au besoin.
        where, params = ["org_id = %s", "kind = ANY(%s)"], [org, list(q.get("kinds") or ["agent"])]
        tags = dict(q.get("tags") or {})
        if "host" in tags:
            where.append("hostname = %s")
            params.append(tags.pop("host"))
        if tags:
            where.append("tags @> %s::jsonb")
            params.append(jdump(tags))
        rows = await fetch_all(conn, f"SELECT hostname, extract(epoch FROM now() - last_seen)::float8 AS age "
                                     f"FROM hosts WHERE {' AND '.join(where)}", params)
        ages: dict[str, float] = {}
        for r in rows:
            # Agent réinstallé : deux lignes pour un même nom ; la plus récente fait foi.
            ages[r["hostname"]] = min(r["age"], ages.get(r["hostname"], r["age"]))
        for hostname, age in ages.items():
            measured[f"host:{hostname}"] = (ev.ALERT if age > m["no_data_s"] else ev.OK, age)

    elif mtype == "service_check":
        by = q.get("by", ["host"])
        rows = await fetch_all(conn, """
            SELECT DISTINCT ON (c.host_id, c.tags) h.hostname, c.tags, c.status
              FROM check_runs c LEFT JOIN hosts h ON h.id = c.host_id AND h.org_id = c.org_id
             WHERE c.org_id = %s AND c.check_name = %s AND c.ts > now() - make_interval(secs => %s)
               AND c.tags @> %s::jsonb
             ORDER BY c.host_id, c.tags, c.ts DESC
        """, (org, q["check"], q.get("window_s", 300), jdump(q.get("tags") or {})))
        statuses: dict[str, list[int]] = {}
        for r in rows:
            tags = r["tags"] or {}
            values = {k: tags.get(k) or (r["hostname"] if k == "host" else None) for k in by}
            statuses.setdefault(group_key(by, values), []).append(r["status"])
        for g, st in statuses.items():
            worst = ev.worst_check_status(st)
            measured[g] = (ev.check_status_state(worst), float(worst))
        if "host" in by and any(old.state != ev.OK for old in prev.values()):
            alive = {r["hostname"] for r in await fetch_all(conn, """
                SELECT hostname FROM hosts
                 WHERE org_id = %s AND last_seen > now() - make_interval(secs => %s)
            """, (org, q.get("window_s", 300)))}
            for g in ev.recovered_groups(prev, measured, alive):
                measured[g] = (ev.OK, 0.0)
        if q["check"] in ev.PROBLEM_ONLY_CHECKS:
            # L'agent n'émet ce contrôle qu'en cas de panne : des hôtes vivants qui n'en signalent
            # aucune, c'est l'état normal, affiché OK (groupe « * ») plutôt que « sans données ».
            any_alive = await fetch_one(conn, """
                SELECT 1 AS up FROM hosts WHERE org_id = %s AND last_seen > now() - make_interval(secs => %s) LIMIT 1
            """, (org, q.get("window_s", 300)))
            if any_alive:
                measured.setdefault("*", (ev.OK, 0.0))

    elif mtype == "log":
        frag, params = logs_ql.compile_query(q.get("q", ""))
        by = q.get("by", [])
        exprs, eparams = _log_select(by)
        group = f"GROUP BY {', '.join(str(i + 1) for i in range(len(by)))}" if by else ""
        rows = await fetch_all(conn, f"""
            SELECT {', '.join(exprs + ['count(*) AS n'])} FROM logs
             WHERE org_id = %s AND ts > %s AND ts <= %s AND {frag} {group}
        """, eparams + [org, ms_to_dt(end - q.get("window_s", 300) * 1000), ms_to_dt(end)] + params)
        measured = _counts(rows, by, prev, _numeric(prev, thresholds, q.get("comparator", ">")))

    elif mtype == "synthetic":
        failures = q.get("failures", 1)
        test = await fetch_one(conn, "SELECT interval_s FROM synthetic_tests WHERE org_id = %s AND id = %s",
                               (org, q["test_id"]))
        window = ev.synthetic_window(m["no_data_s"], test["interval_s"] if test else None)
        if q.get("locations_min_failing"):
            rows = await fetch_all(conn, """
                SELECT location, ok, ts FROM synthetic_results
                 WHERE org_id = %s AND test_id = %s AND ts > now() - make_interval(secs => %s)
                 ORDER BY ts DESC LIMIT 500
            """, (org, q["test_id"], window))
            by_loc: dict[str, list[bool]] = {}
            for r in rows:
                by_loc.setdefault(r["location"], []).append(r["ok"])
            failing = sum(1 for oks in by_loc.values() if ev.consecutive_failures(oks) >= failures)
            measured["*"] = ((ev.ALERT if failing >= q["locations_min_failing"] else ev.OK, float(failing))
                             if by_loc else None)
        else:
            rows = await fetch_all(conn, """
                SELECT ok, ts FROM synthetic_results
                 WHERE org_id = %s AND test_id = %s AND ts > now() - make_interval(secs => %s)
                 ORDER BY ts DESC LIMIT 50
            """, (org, q["test_id"], window))
            if rows:
                n = ev.consecutive_failures([r["ok"] for r in rows])
                measured["*"] = (ev.ALERT if n >= failures else ev.OK, float(n))
            else:
                measured["*"] = None

    elif mtype == "apm":
        row = await fetch_one(conn, f"""
            SELECT count(*) AS hits, count(*) FILTER (WHERE status = 'error') AS errors,
                   percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ns) / 1e6 AS p95
              FROM spans WHERE org_id = %s AND service = %s AND ts > %s AND ts <= %s AND {ENTRY}
        """, (org, q["service"], ms_to_dt(end - q.get("window_s", 300) * 1000), ms_to_dt(end)))
        metric = q["metric"]
        if metric == "hits":
            v = float(row["hits"])
        elif metric == "error_rate":
            v = row["errors"] / row["hits"] if row["hits"] else None
        else:
            v = row["p95"]
        state = _numeric(prev, thresholds, q.get("comparator", ">"))
        measured["*"] = None if v is None else (state("*", v), v)

    elif mtype == "slo":
        from ..slo.service import load_slo, slo_status
        slo = await load_slo(conn, org, q["slo_id"])
        if slo is None:
            measured["*"] = None                     # SLO supprimé : sans données
        else:
            status = await slo_status(conn, slo, with_history=False)
            v = status["error_budget_remaining"] if q["on"] == "budget" else status[f"burn_rate_{q['window']}"]
            state = _numeric(prev, thresholds, q["comparator"])
            measured["*"] = (state("*", v), v)

    elif mtype == "anomaly":
        measured = await _anomaly(conn, m, q, prev, end, detail)
    elif mtype == "forecast":
        measured = await _forecast(conn, m, q, end, detail)
    elif mtype == "outlier":
        measured = await _outlier(conn, m, q, end, detail)
    elif mtype == "change":
        measured = await _change(conn, m, q, prev, end, detail)
    elif mtype == "event":
        measured = await _event_count(conn, m, q, prev, end)
    elif mtype == "process":
        measured = await _process(conn, m, q)
    elif mtype == "error_tracking":
        measured = await _error_tracking(conn, m, q, prev, end)
    elif mtype == "composite":
        measured = await _composite(conn, m, q)
    return measured


def _event_tags(m: dict, group: str) -> dict:
    tags = dict(m.get("tags") or {})
    tags.update({k: v for k, v in parse_group_key(group).items() if re.match(r"^[a-z0-9_.\-/]{1,100}$", k)})
    tags["monitor_id"] = str(m["id"])
    return tags


async def _suppressions(conn, m: dict, groups: set[str], opts: ev.Options,
                        org: maintenance.OrgContext) -> dict[str, str]:
    """Motif de suppression de chaque groupe : maintenance, puis dépendance (hôte en panne, moniteurs cités)."""
    out: dict[str, str] = {}
    dep_all, dep_hosts = False, set()
    if opts.depends_on:
        for r in await fetch_all(conn, """
            SELECT ms.group_key FROM monitor_states ms JOIN monitors m ON m.id = ms.monitor_id
             WHERE m.org_id = %s AND ms.monitor_id = ANY(%s::bigint[]) AND m.enabled AND ms.state = 'ALERT'
        """, (m["org_id"], opts.depends_on)):
            host = parse_group_key(r["group_key"]).get("host")
            if r["group_key"] == "*":
                dep_all = True
            elif host:
                dep_hosts.add(host)
    for g in groups:
        if org.active and D.suppressed_by(org.active, m["id"], g, org.host_tags):
            out[g] = ev.DOWNTIME
            continue
        host = parse_group_key(g).get("host")
        if dep_all or (host and host in dep_hosts):
            out[g] = ev.DEPENDENCY
        elif host and m["type"] != "host" and opts.depends_on_host and host in org.down_hosts:
            out[g] = ev.DEPENDENCY
    return out


def _may_change(prev: dict, measured: dict) -> bool:
    if any(old.flapping for old in prev.values()):
        return True
    for g, mv in measured.items():
        old = prev.get(g)
        if mv is None:
            if old is not None and old.state != ev.NODATA:
                return True
        elif old is None or mv[0] != old.state:
            return True
    return False


async def _recent_changes(conn, m: dict, opts: ev.Options, now: datetime) -> dict[str, list[datetime]]:
    out: dict[str, list[datetime]] = {}
    for r in await fetch_all(conn, "SELECT group_key, ts FROM alert_events WHERE monitor_id = %s AND ts > %s ORDER BY ts",
                             (m["id"], now - timedelta(seconds=opts.flap_window_s))):
        out.setdefault(r["group_key"], []).append(r["ts"])
    return out


async def _out_since(conn, m: dict, now: datetime) -> dict[str, datetime]:
    rows = await fetch_all(conn, """
        SELECT DISTINCT ON (group_key) group_key, ts FROM alert_events
         WHERE monitor_id = %s AND ts > %s AND (from_state IS NULL OR from_state = 'OK') AND to_state <> 'OK'
         ORDER BY group_key, ts DESC
    """, (m["id"], now - OUT_SINCE_LOOKBACK))
    return {r["group_key"]: r["ts"] for r in rows}


async def _host_errors(conn, org_id: int, hostname: str, now: datetime) -> list[str]:
    rows = await fetch_all(conn, """
        SELECT ts, status, service, left(message, 300) AS message FROM logs
         WHERE org_id = %s AND hostname = %s AND ts > %s AND status = ANY(%s::text[])
         ORDER BY ts DESC LIMIT 5
    """, (org_id, hostname, now - timedelta(minutes=30), ERROR_STATUSES))
    return [f"{r['ts'].strftime('%H:%M:%S')} [{r['status']}] {r['service'] or ''} {r['message']}".strip() for r in rows]


def message_context(m: dict, group: str, state: str, value, previous: str | None, old: ev.PrevState | None,
                    now: datetime, url: str, renotify: bool = False, escalation: bool = False) -> dict:
    """Variables et drapeaux d'un gabarit de message (contrat § 18)."""
    tags = {**(m.get("tags") or {}), **parse_group_key(group)}
    th = m.get("thresholds") or {}
    thr = th.get("warning") if state == ev.WARN else th.get("critical")
    prio = m.get("priority")
    ctx = {"name": m["name"], "group": group, "host": tags.get("host", ""), "value": ev.format_value(value),
           "state": state, "priority": f"P{prio}" if prio else "", "threshold": "" if thr is None else f"{thr:g}",
           "url": url, "last_value": ev.format_value(old.value) if old else "",
           "duration": template.human_duration((now - old.since).total_seconds()) if old else "",
           "eta": template.eta_text(value) if m["type"] == "forecast" else "", "tags": tags}
    ctx.update(template.flags_for(state, previous, renotify, escalation))
    return ctx


async def _load_channels(conn, org_id: int, ids: set[int]) -> dict[int, dict]:
    if not ids:
        return {}
    rows = await fetch_all(conn, """
        SELECT id, kind, name, config FROM notification_channels
         WHERE org_id = %s AND id = ANY(%s::bigint[]) AND enabled
    """, (org_id, sorted(ids)))
    return {r["id"]: r for r in rows}


async def _incident_timeline(conn, m: dict, transitions: list, now: datetime) -> None:
    """Chronologie automatique (§ 20) : chaque changement d'état d'un moniteur ou d'un groupe lié
    à un incident non résolu y ajoute une entrée `alert`."""
    refs = [f"{m['id']}|{g}" for g, *_ in transitions]
    links = await fetch_all(conn, """
        SELECT l.incident_id, l.ref FROM incident_links l JOIN incidents i ON i.id = l.incident_id
         WHERE i.org_id = %s AND i.status <> 'resolved' AND l.kind = 'monitor' AND (l.ref = %s OR l.ref = ANY(%s::text[]))
    """, (m["org_id"], str(m["id"]), refs))
    done = set()
    for link in links:
        for group, from_state, to_state, value, _ in transitions:
            if link["ref"] not in (str(m["id"]), f"{m['id']}|{group}") or (link["incident_id"], group) in done:
                continue
            done.add((link["incident_id"], group))
            label = m["name"] + ("" if group == "*" else f" ({group})")
            await conn.execute("""
                INSERT INTO incident_timeline (incident_id, ts, kind, body, data)
                VALUES (%s, %s, 'alert', %s, %s::jsonb)
            """, (link["incident_id"], now, f"{label} : {from_state} → {to_state}, valeur {ev.format_value(value)}",
                  jdump({"monitor_id": m["id"], "group": group, "from": from_state, "to": to_state, "value": value})))


async def apply(conn, m: dict, prev: dict[str, ev.PrevState], measured: dict, settings: Settings,
                channels: list[dict], allow_private: bool, org: maintenance.OrgContext | None = None,
                db_streaks: dict | None = None) -> list[Notice]:
    """Écrit les états, l'historique et les événements ; rend les notifications à envoyer après COMMIT."""
    now = datetime.now(timezone.utc)
    org = org or maintenance.OrgContext()
    opts = ev.Options.of(m.get("options"))
    muted = bool(m["muted_until"] and m["muted_until"] > now)
    groups = set(prev) | set(measured)
    suppress = await _suppressions(conn, m, groups, opts, org)
    recent = await _recent_changes(conn, m, opts, now) if opts.flap_changes > 0 and _may_change(prev, measured) else {}
    out_since = (await _out_since(conn, m, now)
                 if opts.escalations and any(o.state != ev.OK for o in prev.values()) else {})
    p = ev.plan(prev, measured, now, m["no_data_s"], m["notify_no_data"], m["renotify_s"], muted,
                opts=opts, suppress=suppress, recent_changes=recent,
                streaks=db_streaks if db_streaks is not None else STREAKS.get(m["id"]),
                out_since=out_since)
    STREAKS[m["id"]] = p.streaks
    for group, state, value, since, last_eval, last_notified in p.upserts:
        flapping, step, reason, _ = p.flags[group]
        await conn.execute("""
            INSERT INTO monitor_states (monitor_id, group_key, state, value, since, last_eval, last_notified,
                                        flapping, escalation_step, suppressed)
            VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s)
            ON CONFLICT (monitor_id, group_key) DO UPDATE
               SET state = EXCLUDED.state, value = EXCLUDED.value, since = EXCLUDED.since,
                   last_eval = EXCLUDED.last_eval, last_notified = EXCLUDED.last_notified,
                   flapping = EXCLUDED.flapping, escalation_step = EXCLUDED.escalation_step,
                   suppressed = EXCLUDED.suppressed
        """, (m["id"], group, state, value, since, last_eval, last_notified, flapping, step, reason))
    for group in p.deletes:
        await conn.execute("DELETE FROM monitor_states WHERE monitor_id = %s AND group_key = %s", (m["id"], group))
    if db_streaks is not None and (db_streaks or p.streaks):
        # Tolérances gardées en base : un changement de worker ne perd pas une confirmation en cours.
        await conn.execute("""
            UPDATE monitor_states SET pending_state = NULL, pending_count = 0
             WHERE monitor_id = %s AND pending_state IS NOT NULL AND NOT (group_key = ANY(%s::text[]))
        """, (m["id"], list(p.streaks)))
        for group, (target, n) in p.streaks.items():
            await conn.execute("UPDATE monitor_states SET pending_state = %s, pending_count = %s "
                               "WHERE monitor_id = %s AND group_key = %s", (target, n, m["id"], group))
    cleared = [g for g, f in p.flags.items() if f[3]]
    if cleared:
        # Un retour à OK efface l'acquittement (§ 17).
        await conn.execute("""
            UPDATE monitor_states SET acked_by = NULL, acked_at = NULL, ack_comment = NULL
             WHERE monitor_id = %s AND group_key = ANY(%s::text[]) AND state = 'OK'
        """, (m["id"], cleared))

    url = f"{settings.public_url}/#/monitors/{m['id']}"
    mon = {"id": m["id"], "name": m["name"], "type": m["type"], "priority": m.get("priority")}
    th = m.get("thresholds") or {}
    base_ids = {c["id"] for c in channels}
    extra_ids = set(template.mentions(m.get("message") or ""))
    for esc in opts.escalations:
        extra_ids |= set(esc.get("channel_ids") or []) | set(template.mentions(esc.get("message") or ""))
    by_id = {c["id"]: c for c in channels}
    by_id.update(await _load_channels(conn, m["org_id"], extra_ids - base_ids))
    logs_cache: dict[str, list[str]] = {}
    books = (await service_link.runbooks(conn, m["org_id"])
             if p.transitions or p.renotify or p.notices or p.escalations else {})

    def text_for(group, state, value, previous, renotify=False, escalation=False, tmpl=None):
        ctx = message_context(m, group, state, value, previous, prev.get(group), now, url, renotify, escalation)
        return template.render(tmpl or m.get("message") or ev.DEFAULT_MESSAGE, ctx)

    async def notices_for(group, state, value, previous, text, kind, target_ids, renotify=False):
        targets = [by_id[i] for i in target_ids if i in by_id]
        targets += [by_id[i] for i in template.mentions(text) if i in by_id and i not in target_ids]
        host = parse_group_key(group).get("host")
        logs: list[str] = []
        if host and state in (ev.ALERT, ev.WARN) and any(c["kind"] == "email" for c in targets):
            if host not in logs_cache:
                logs_cache[host] = await _host_errors(conn, m["org_id"], host, now)
            logs = logs_cache[host]
        thr = th.get("warning") if state == ev.WARN else th.get("critical")
        body = template.strip_mentions(text)
        if state != ev.OK:
            body = service_link.with_runbook(body, books.get(service_link.monitor_service(m, group) or ""))
        return [Notice(c, mon, group, state, previous, value, body, url, renotify,
                       allow_private, kind=kind, threshold=thr,
                       explain_url=f"{settings.public_url}/#/problems?explain={m['id']}&group={quote(group)}",
                       logs=logs) for c in targets]

    notices: list[Notice] = []
    base = [c["id"] for c in channels]
    for group, from_state, to_state, value, notify in p.transitions:
        message = text_for(group, to_state, value, from_state)
        await conn.execute("""
            INSERT INTO alert_events (org_id, monitor_id, group_key, ts, from_state, to_state, value, message, suppressed)
            VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s)
        """, (m["org_id"], m["id"], group, now, from_state, to_state, value, template.strip_mentions(message)[:10000],
              p.suppressed.get(group)))
        title = f"[{to_state}] {m['name']}" + ("" if group == "*" else f" ({group})")
        await conn.execute("""
            INSERT INTO events (org_id, ts, source, event_type, title, text, alert_type, priority, tags, aggregation_key)
            VALUES (%s, %s, 'monitor', 'monitor_state', %s, %s, %s, 'normal', %s::jsonb, %s)
        """, (m["org_id"], now, title[:500], template.strip_mentions(message)[:8000], ev.EVENT_ALERT_TYPE[to_state],
              jdump(_event_tags(m, group)), f"monitor:{m['id']}:{group}"[:200]))
        if notify:
            notices += await notices_for(group, to_state, value, from_state, message, "alert", base)
    if p.transitions:
        await _incident_timeline(conn, m, p.transitions, now)
    for group, state, value in p.renotify:
        message = text_for(group, state, value, state, renotify=True)
        notices += await notices_for(group, state, value, state, message, "renotify", base, renotify=True)
    for group, state, value, motive in p.notices:
        if motive == "flapping":
            minutes = opts.flap_window_s // 60
            message = (f"{m['name']}" + ("" if group == "*" else f" ({group})") +
                       f" est instable : au moins {opts.flap_changes} changements d'état en {minutes} min. "
                       "Notifications suspendues jusqu'à stabilité.")
            notices += await notices_for(group, state, value, None, message, "flapping", base)
        else:
            message = text_for(group, state, value, None)
            notices += await notices_for(group, state, value, None, message, "reminder", base)
    for group, state, value, step, esc in p.escalations:
        message = text_for(group, state, value, None, escalation=True, tmpl=esc.get("message") or None)
        notices += await notices_for(group, state, value, None, message, "escalation", esc.get("channel_ids") or [])
    return notices


async def _down_hosts(db: Database, orgs: list[int]) -> dict[int, set[str]]:
    out: dict[int, set[str]] = {}
    if not orgs:
        return out
    async with db.writer() as conn:
        for r in await fetch_all(conn, """
            SELECT m.org_id, ms.group_key FROM monitor_states ms JOIN monitors m ON m.id = ms.monitor_id
             WHERE m.type = 'host' AND m.enabled AND ms.state = 'ALERT' AND m.org_id = ANY(%s::bigint[])
        """, (orgs,)):
            host = parse_group_key(r["group_key"]).get("host")
            if host:
                out.setdefault(r["org_id"], set()).add(host)
    return out


async def evaluate_all(db: Database, notifier: Notifier, settings: Settings, concurrency: int = 4) -> int:
    now = datetime.now(timezone.utc)
    async with db.writer() as conn:
        monitors = await fetch_all(conn, """
            SELECT m.*, o.settings AS org_settings FROM monitors m JOIN orgs o ON o.id = m.org_id
             WHERE m.enabled ORDER BY m.id
        """)
        downtimes = await maintenance.load(conn, now)
        await maintenance.emit_events(conn, downtimes, now)
        contexts = await maintenance.contexts(conn, downtimes, now)
    sem = asyncio.Semaphore(concurrency)
    alive = {m["id"] for m in monitors}
    for mid in [k for k in STREAKS if k not in alive]:
        STREAKS.pop(mid, None)

    async def one(m: dict) -> None:
        async with sem:
            try:
                async with db.writer() as conn:
                    rows = await fetch_all(conn, "SELECT * FROM monitor_states WHERE monitor_id = %s", (m["id"],))
                    prev = _prev_states(rows)
                    db_streaks = {r["group_key"]: (r["pending_state"], r["pending_count"])
                                  for r in rows if r.get("pending_state")}
                    measured = await measure(conn, m, prev)
                    channels = await fetch_all(conn, """
                        SELECT id, kind, name, config FROM notification_channels
                         WHERE org_id = %s AND id = ANY(%s::bigint[]) AND enabled
                    """, (m["org_id"], m["channel_ids"] or []))
                    allow = settings.allow_private_targets or bool((m["org_settings"] or {}).get("allow_private_targets"))
                    org = contexts.setdefault(m["org_id"], maintenance.OrgContext())
                    notices = await apply(conn, m, prev, measured, settings, channels, allow, org, db_streaks)
                for n in notices:
                    notifier.submit(n)
            except (QueryError, ValueError, KeyError) as e:
                log.warning("moniteur %s (%s) non évaluable : %s", m["id"], m["name"], e)
            except (psycopg.OperationalError, PoolTimeout):
                raise
            except psycopg.Error as e:
                log.warning("moniteur %s (%s) : requête en échec : %s", m["id"], m["name"], str(e).splitlines()[0])
            except Exception:
                log.exception("moniteur %s (%s) : erreur inattendue", m["id"], m["name"])

    hosts = [m for m in monitors if m["type"] == "host"]
    composites = [m for m in monitors if m["type"] == "composite"]
    others = [m for m in monitors if m["type"] not in ("host", "composite")]
    await asyncio.gather(*(one(m) for m in hosts))
    for org_id, down in (await _down_hosts(db, sorted({m["org_id"] for m in others}))).items():
        contexts.setdefault(org_id, maintenance.OrgContext()).down_hosts = down
    await asyncio.gather(*(one(m) for m in others))
    await asyncio.gather(*(one(m) for m in composites))
    return len(monitors)
