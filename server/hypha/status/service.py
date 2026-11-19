"""Page de statut publique (contrat § 21) : assemblée depuis la base, sans donnée interne.

Le résultat ne porte que les noms et groupes saisis dans la page, des statuts, des disponibilités,
les incidents qui citent la page (titre, mises à jour publiques) et les maintenances : ni nom
d'hôte, ni valeur, ni requête. Chaque requête filtre l'organisation de la page.
"""
from datetime import date, datetime, timedelta, timezone

from ..db import fetch_all
from ..monitors import downtime as D
from ..monitors.evaluate import worst
from ..query import metrics_ql as mql
from ..slo import compute as SC
from ..slo.service import _bucket_sums, _series_ids, slo_status
from ..util import dt_to_ms, ms_to_dt
from . import compute as C

HOST_DOWN_S = 120
MAINTENANCE_AHEAD = timedelta(days=7)


def _changes(rows) -> list[SC.Change]:
    return [SC.Change(r["ts"].timestamp(), r["group_key"], r["from_state"], r["to_state"]) for r in rows]


async def _monitor_history(conn, org_id: int, ids: list[int], start: datetime, end: datetime) -> dict:
    """Par moniteur : changements avant la fenêtre (dernier par groupe), dans la fenêtre, états courants."""
    out = {i: {"before": [], "inside": [], "current": {}} for i in ids}
    if not ids:
        return out
    for r in await fetch_all(conn, """
        SELECT DISTINCT ON (monitor_id, group_key) monitor_id, ts, group_key, from_state, to_state FROM alert_events
         WHERE org_id = %s AND monitor_id = ANY(%s::bigint[]) AND ts < %s
         ORDER BY monitor_id, group_key, ts DESC, id DESC
    """, (org_id, ids, start)):
        out[r["monitor_id"]]["before"].append(r)
    for r in await fetch_all(conn, """
        SELECT monitor_id, ts, group_key, from_state, to_state FROM alert_events
         WHERE org_id = %s AND monitor_id = ANY(%s::bigint[]) AND ts >= %s AND ts <= %s
         ORDER BY ts, id LIMIT 200000
    """, (org_id, ids, start, end)):
        out[r["monitor_id"]]["inside"].append(r)
    for r in await fetch_all(conn, """
        SELECT ms.monitor_id, ms.group_key, ms.state FROM monitor_states ms JOIN monitors m ON m.id = ms.monitor_id
         WHERE m.org_id = %s AND ms.monitor_id = ANY(%s::bigint[])
    """, (org_id, ids)):
        out[r["monitor_id"]]["current"][r["group_key"]] = r["state"]
    return out


def _intervals(hist: dict, start_s: float, end_s: float, no_data_s: float, group: str | None = None):
    def keep(rows):
        return [r for r in rows if group is None or r["group_key"] == group]
    before, inside = _changes(keep(hist["before"])), _changes(keep(hist["inside"]))
    current = {g: s for g, s in hist["current"].items() if group is None or g == group}
    bad = SC.merge(SC.alert_intervals(before, inside, current, start_s, end_s, no_data_s))
    warn = SC.merge(SC.alert_intervals(before, inside, current, start_s, end_s, no_data_s, frozenset({"WARN"})))
    return bad, warn


def _component_days(hist, start_s, now_s, days, no_data_s, priority_one, group=None):
    bad, warn = _intervals(hist, start_s, now_s, no_data_s, group)
    return C.daily_from_intervals(bad, warn, start_s, now_s, days, priority_one), \
        C.uptime_from_intervals(bad, start_s, now_s)


async def build(conn, page: dict, now: datetime | None = None) -> dict:
    now = now or datetime.now(timezone.utc)
    org = page["org_id"]
    now_s = now.timestamp()
    start = now - timedelta(days=C.DAYS)
    days = C.utc_days(now)
    comps = [c for c in (page["components"] or []) if isinstance(c, dict)]
    ids = {k: sorted({int(c["ref_id"]) for c in comps if c.get("kind") == k and isinstance(c.get("ref_id"), int)})
           for k in ("monitor", "synthetic", "slo", "host")}

    slos = {r["id"]: r for r in await fetch_all(conn, """
        SELECT org_id, id, name, description, type, monitor_id, good_query, total_query, target, warning, timeframe,
               tags, created_by, created_at, updated_at
          FROM slos WHERE org_id = %s AND id = ANY(%s::bigint[])
    """, (org, ids["slo"]))} if ids["slo"] else {}
    hosts = {r["id"]: r for r in await fetch_all(conn, """
        SELECT id, hostname, last_seen, first_seen FROM hosts WHERE org_id = %s AND id = ANY(%s::bigint[])
    """, (org, ids["host"]))} if ids["host"] else {}
    host_monitors = await fetch_all(conn, """
        SELECT id, priority, created_at, no_data_s FROM monitors WHERE org_id = %s AND type = 'host' AND enabled
    """, (org,)) if hosts else []
    monitor_ids = set(ids["monitor"]) | {s["monitor_id"] for s in slos.values() if s["monitor_id"]} | \
        {m["id"] for m in host_monitors}
    monitors = {r["id"]: r for r in await fetch_all(conn, """
        SELECT id, priority, created_at, no_data_s FROM monitors WHERE org_id = %s AND id = ANY(%s::bigint[])
    """, (org, sorted(monitor_ids)))} if monitor_ids else {}
    history = await _monitor_history(conn, org, sorted(monitors), start, now)

    downtimes = await fetch_all(conn, """
        SELECT id, name, scope, start_ts, end_ts, recurrence, enabled FROM downtimes
         WHERE org_id = %s AND enabled AND (end_ts IS NULL OR end_ts > now())
    """, (org,))
    active = [(d, D.active_window(d, now)) for d in downtimes]
    active = [d for d, w in active if w]

    synth_last, synth_days = {}, {}
    if ids["synthetic"]:
        for r in await fetch_all(conn, """
            SELECT DISTINCT ON (test_id) test_id, ok FROM synthetic_results
             WHERE org_id = %s AND test_id = ANY(%s::bigint[]) AND ts > now() - interval '1 day'
             ORDER BY test_id, ts DESC
        """, (org, ids["synthetic"])):
            synth_last[r["test_id"]] = r["ok"]
        for r in await fetch_all(conn, """
            SELECT test_id, date_trunc('day', ts) AS d, count(*) AS n, count(*) FILTER (WHERE ok) AS ok
              FROM synthetic_results WHERE org_id = %s AND test_id = ANY(%s::bigint[]) AND ts >= %s
             GROUP BY 1, 2
        """, (org, ids["synthetic"], start)):
            synth_days.setdefault(r["test_id"], {})[r["d"].date()] = (float(r["ok"]), float(r["n"]))

    def monitor_in_maintenance(mid: int) -> bool:
        groups = list((history.get(mid) or {}).get("current", {})) or ["*"]
        return any(D.covers(d["scope"] or {}, mid, g) for d in active for g in groups)

    def global_maintenance() -> bool:
        return any(not any((d["scope"] or {}).get(k) for k in ("monitor_ids", "tags", "groups")) for d in active)

    out_components = []
    for c in comps:
        kind, ref = c.get("kind"), c.get("ref_id")
        item = {"name": str(c.get("name") or ""), "group": str(c.get("group") or ""), "status": "operational",
                "uptime_90d": None, "days": [{"date": d.isoformat(), "uptime": None, "worst": None} for d in days]}
        if kind == "monitor" and ref in monitors:
            m = monitors[ref]
            states = list(history[ref]["current"].values())
            item["status"] = C.monitor_status(worst(states) if states else None, m["priority"],
                                              monitor_in_maintenance(ref))
            item["days"], item["uptime_90d"] = _component_days(
                history[ref], max(start.timestamp(), m["created_at"].timestamp()), now_s, days, m["no_data_s"],
                m["priority"] == 1)
        elif kind == "synthetic" and ref in ids["synthetic"]:
            counts = synth_days.get(ref, {})
            item["status"] = C.synthetic_status(synth_last.get(ref), global_maintenance())
            item["days"] = C.daily_from_counts(counts, days)
            item["uptime_90d"] = C.uptime_from_counts(counts)
        elif kind == "slo" and ref in slos:
            s = slos[ref]
            try:
                state = (await slo_status(conn, s, with_history=False))["state"]
            except Exception:
                state = None
            item["status"] = C.slo_status(state, global_maintenance())
            if s["type"] == "monitor" and s["monitor_id"] in monitors:
                m = monitors[s["monitor_id"]]
                item["days"], item["uptime_90d"] = _component_days(
                    history[m["id"]], max(start.timestamp(), m["created_at"].timestamp()), now_s, days,
                    m["no_data_s"], False)
            elif s["type"] == "metric":
                counts = await _slo_metric_days(conn, s, start, now)
                item["days"] = C.daily_from_counts(counts, days, "slo", float(f"{float(s['target']):.7g}"))
                item["uptime_90d"] = C.uptime_from_counts(counts)
        elif kind == "host" and ref in hosts:
            h = hosts[ref]
            up = (now - h["last_seen"]).total_seconds() <= HOST_DOWN_S
            maint = any(D.covers_host(d["scope"] or {}, h["hostname"]) for d in active)
            item["status"] = C.host_status(up, maint)
            m = _host_monitor(host_monitors, history, f"host:{h['hostname']}")
            if m:
                item["days"], item["uptime_90d"] = _component_days(
                    history[m["id"]],
                    max(start.timestamp(), h["first_seen"].timestamp(), m["created_at"].timestamp()), now_s, days,
                    m["no_data_s"], True, f"host:{h['hostname']}")
        out_components.append(item)

    incidents = await fetch_all(conn, """
        SELECT id, number, title, status, severity, detected_at, resolved_at FROM incidents
         WHERE org_id = %s AND %s = ANY(status_page_ids)
           AND (resolved_at IS NULL OR resolved_at > now() - interval '90 days')
         ORDER BY (status = 'resolved'), detected_at DESC LIMIT 50
    """, (org, page["id"]))
    updates: dict[int, list] = {}
    if incidents:
        for r in await fetch_all(conn, """
            SELECT incident_id, ts, body FROM incident_timeline
             WHERE incident_id = ANY(%s::bigint[]) AND kind = 'public_update' ORDER BY ts DESC
        """, ([i["id"] for i in incidents],)):
            updates.setdefault(r["incident_id"], []).append({"ts": dt_to_ms(r["ts"]), "body": r["body"]})

    maintenances = _maintenances(downtimes, now, comps, history, hosts)
    statuses = [c["status"] for c in out_components]
    return {
        "title": page["title"], "description": page["description"], "overall": C.overall(statuses),
        "components": out_components,
        "incidents": [{"number": i["number"], "title": i["title"], "status": i["status"], "severity": i["severity"],
                       "started": dt_to_ms(i["detected_at"]), "resolved": dt_to_ms(i["resolved_at"]),
                       "updates": updates.get(i["id"], [])} for i in incidents],
        "maintenances": maintenances, "generated_at": dt_to_ms(now),
    }


def _host_monitor(host_monitors: list[dict], history: dict, group: str) -> dict | None:
    """Le moniteur `host` qui suit cet hôte (état courant ou changements), à défaut le premier."""
    for m in host_monitors:
        h = history.get(m["id"]) or {}
        if group in h.get("current", {}) or any(r["group_key"] == group for r in h.get("inside", []) + h.get("before", [])):
            return m
    return host_monitors[0] if host_monitors else None


def _maintenances(downtimes: list[dict], now: datetime, comps: list[dict], history: dict, hosts: dict) -> list[dict]:
    """Maintenances actives et à venir (7 jours) qui touchent un composant de la page."""
    mon_groups = {c["ref_id"]: list((history.get(c["ref_id"]) or {}).get("current", {})) or ["*"]
                  for c in comps if c.get("kind") == "monitor" and isinstance(c.get("ref_id"), int)}
    hostnames = [hosts[c["ref_id"]]["hostname"] for c in comps if c.get("kind") == "host" and c.get("ref_id") in hosts]
    out = []
    for d in downtimes:
        scope = d["scope"] or {}
        is_global = not any(scope.get(k) for k in ("monitor_ids", "tags", "groups"))
        touches = is_global or any(D.covers(scope, mid, g) for mid, gs in mon_groups.items() for g in gs) or \
            any(D.covers_host(scope, h) for h in hostnames)
        if not touches:
            continue
        for w in D.occurrences(d, now, now + MAINTENANCE_AHEAD)[:5]:
            out.append({"name": d["name"], "start": dt_to_ms(w.start), "end": dt_to_ms(w.end) if w.end else None})
    out.sort(key=lambda x: x["start"])
    return out[:20]


async def _slo_metric_days(conn, slo: dict, start: datetime, now: datetime) -> dict[date, tuple[float, float]]:
    good_ids = await _series_ids(conn, slo["org_id"], slo["good_query"])
    total_ids = await _series_ids(conn, slo["org_id"], slo["total_query"])
    a, b = dt_to_ms(start), dt_to_ms(now)
    good = await _bucket_sums(conn, good_ids, mql.R5M, 86400, a, b)
    total = await _bucket_sums(conn, total_ids, mql.R5M, 86400, a, b)
    return {ms_to_dt(k).date(): (good.get(k, 0.0), total.get(k, 0.0)) for k in set(good) | set(total)}
