"""Routes et tâches du worker de la 0.4.0 (alertes, incidents, exploitation) exercées sans base : une
fausse connexion répond et vérifie que chaque requête SQL reçoit autant de paramètres qu'elle a de `%s`.
Aucun réseau (les envois sont interceptés), aucune base (règle du parc)."""
import asyncio
import json
import re
from contextlib import asynccontextmanager
from datetime import datetime, timedelta, timezone

import pytest

from conftest import placeholders
from test_routes_nodb import APP_KEY, FakeCursor  # pose aussi l'environnement et les modules de remplacement

from fastapi.testclient import TestClient  # noqa: E402

from hypha.app import create_app  # noqa: E402
from hypha.config import get_settings  # noqa: E402
from hypha.runtime import Runtime, set_runtime  # noqa: E402
from hypha.util import dt_to_ms, generate_api_key, key_hash  # noqa: E402

NOW = datetime.now(timezone.utc)
MEMBER_KEY = generate_api_key()
VIEWER_KEY = generate_api_key()
OWNER = {"X-Hypha-App-Key": APP_KEY}
MEMBER = {"X-Hypha-App-Key": MEMBER_KEY}
VIEWER = {"X-Hypha-App-Key": VIEWER_KEY}
STATE: dict = {}

MON = {"id": 3, "name": "CPU", "type": "metric", "query": {"q": "avg:system.cpu.usage{*} by {host}", "window_s": 300,
       "aggregation": "avg", "comparator": ">"}, "thresholds": {"critical": 90.0}, "message": "", "channel_ids": [],
       "notify_no_data": False, "no_data_s": 600, "renotify_s": 0, "tags": {}, "enabled": True, "muted_until": None,
       "priority": 2, "options": {}, "created_by": 1, "created_at": NOW - timedelta(days=200), "updated_at": NOW}
DT_ROW = {"id": 8, "org_id": 1, "name": "Patch", "message": "", "scope": {"monitor_ids": [3]},
          "start_ts": NOW - timedelta(minutes=10), "end_ts": NOW + timedelta(minutes=50), "recurrence": None,
          "enabled": True, "created_by": 1, "created_at": NOW, "updated_at": NOW}
PAGE_COMPONENTS = [{"name": "API", "group": "Services", "kind": "monitor", "ref_id": 3},
                   {"name": "Site", "group": "", "kind": "synthetic", "ref_id": 12},
                   {"name": "Disponibilité", "group": "", "kind": "slo", "ref_id": 4},
                   {"name": "Serveur web", "group": "Hôtes", "kind": "host", "ref_id": 10}]
PAGE = {"id": 2, "slug": "parc", "title": "Parc S33D", "description": "", "components": PAGE_COMPONENTS,
        "public": True, "created_at": NOW, "updated_at": NOW}
SLO = {"org_id": 1, "id": 4, "name": "API", "description": "", "type": "monitor", "monitor_id": 3, "good_query": None,
       "total_query": None, "target": 99.9000015, "warning": None, "timeframe": "30d", "tags": {}, "created_by": 1,
       "created_at": NOW - timedelta(days=100), "updated_at": NOW}
REPORT = {"org_id": 1, "id": 6, "name": "Hebdo", "kind": "summary", "dashboard_id": None, "channel_ids": [1],
          "schedule": {"every": "week", "weekday": 1, "hour": 8, "minute": 0, "tz": "Europe/Paris"},
          "period_s": 604800, "enabled": True, "last_sent_at": None, "created_by": 1,
          "created_at": NOW - timedelta(days=30)}
HOST_AGENT = {"host_id": 10, "hostname": "web-01", "os": "linux", "kind": "agent",
              "agent_id": "6f1c2b8e-0000-4000-8000-000000000001",
              "capabilities": {"actions": {"enabled": True, "kinds": ["restart_service", "start_service"],
                                           "scripts": []}}}
ALERT_HISTORY = [
    {"monitor_id": 3, "ts": NOW - timedelta(hours=2), "group_key": "*", "from_state": "OK", "to_state": "ALERT"},
    {"monitor_id": 3, "ts": NOW - timedelta(hours=1), "group_key": "*", "from_state": "ALERT", "to_state": "OK"},
    {"monitor_id": 20, "ts": NOW - timedelta(hours=3), "group_key": "host:web-01", "from_state": "OK", "to_state": "ALERT"},
    {"monitor_id": 20, "ts": NOW - timedelta(minutes=150), "group_key": "host:web-01", "from_state": "ALERT",
     "to_state": "OK"},
]
GENERIC_ID = re.compile(r"^SELECT id FROM (monitors|hosts|slos|synthetic_tests|error_issues|dashboards|status_pages) "
                        r"WHERE org_id = %s AND id = (%s|ANY\(%s::bigint\[\]\))$")


def _points(params) -> list[dict]:
    """Seaux réguliers de `points_sql` : params = [ids, idx, pas, origine, ids, début, fin]."""
    step, start, end = params[2], params[5], params[6]
    out, t = [], start
    mode = STATE.get("series_mode", "flat")
    while t < end:
        for gi in sorted(set(params[1])):
            if mode == "trend":
                v = 50 + ((t - NOW).total_seconds() / 3600 + 168) * 0.1     # 66,8 maintenant, +0,1 par heure
            elif mode == "outlier":
                v = 9.0 if gi == 3 else 1.0 + 0.01 * gi
            else:
                v = 10.0
            out.append({"gi": gi, "bucket": t, "v": v})
        t += step
    return out


def respond(sql: str, params) -> list[dict]:
    s = " ".join(sql.split())
    if "FROM api_keys k" in s:
        role = "viewer" if params[0] == key_hash(VIEWER_KEY) else "member" if params[0] == key_hash(MEMBER_KEY) else "owner"
        return [{"id": 2, "org_id": 1, "kind": "app", "revoked": False, "created_by": 1, "role": role,
                 "email": "alice@example.org", "name": "Alice"}]
    m = GENERIC_ID.match(s)
    if m:
        ids = params[1] if isinstance(params[1], list) else [params[1]]
        return [{"id": i} for i in ids if i != 999]
    # ── métriques (aperçus, moniteurs avancés, rapports) ──
    if "SELECT id, tags, unit FROM metric_series" in s:
        return STATE.get("series_rows", [{"id": 1, "tags": {"host": "web-01"}, "unit": "percent"},
                                         {"id": 2, "tags": {"host": "web-02"}, "unit": "percent"}])
    if s.startswith("WITH g AS"):
        return _points(params)
    # ── moniteurs ──
    if s.startswith("SELECT id, name, priority FROM monitors WHERE org_id = %s AND id = %s"):
        return [{"id": params[1], "name": "CPU", "priority": 2}]
    if "FROM monitors WHERE org_id = %s AND id = %s" in s and "priority, options" in s:
        return [dict(MON)]
    if s.startswith("INSERT INTO monitors") or s.startswith("UPDATE monitors SET name") or             s.startswith("UPDATE monitors SET muted_until"):
        return [dict(MON)]
    if s.startswith("DELETE FROM monitors"):
        return [{"id": params[1], "name": "CPU", "type": "metric", "query": {}}]
    if s.startswith("SELECT type FROM monitors WHERE org_id = %s AND id = %s"):
        return [{"type": "metric"}]
    if s.startswith("INSERT INTO slos"):
        return [dict(SLO)]
    if "INSERT INTO api_keys" in s:
        return [{"id": 4, "name": params[1], "kind": params[2], "prefix": params[3], "created_at": NOW,
                 "last_used_at": None, "revoked_at": None, "created_by": 1}]
    if s.startswith("INSERT INTO users"):
        return [{"id": 77}]
    if s.startswith("INSERT INTO events") and "RETURNING id, ts" in s:
        return [{"id": 9, "ts": NOW}]
    if s.startswith("SELECT id, query FROM monitors WHERE org_id = %s AND type = 'composite'"):
        return STATE.get("composites", [])
    if s.startswith("UPDATE monitor_states SET acked_by = %s"):
        return [{"group_key": g, "state": "ALERT", "acked_at": NOW} for g in params[3] if g != "host:ok"]
    if s.startswith("SELECT count(*) AS n FROM monitor_states WHERE monitor_id = %s"):
        return [{"n": 1}]
    if s.startswith("UPDATE monitor_states SET acked_by = NULL") and "RETURNING group_key" in s:
        return [] if params[1] == "none" else [{"group_key": params[1]}]
    if "LEFT JOIN users u ON u.id = ms.acked_by WHERE" in s and "LIMIT 5000" in s:
        base = {"type": "metric", "value": 1.0, "last_notified": NOW, "acked_by": None, "acked_at": None,
                "ack_comment": None, "acked_by_email": None, "suppressed": None, "flapping": False,
                "escalation_step": 0, "muted": False}
        return [{**base, "monitor_id": 3, "monitor_name": "CPU", "priority": 2, "group": "host:web-01", "state": "ALERT",
                 "since": NOW - timedelta(hours=1)},
                {**base, "monitor_id": 4, "monitor_name": "Disque", "priority": None, "group": "*", "state": "WARN",
                 "since": NOW - timedelta(hours=5), "acked_at": NOW, "acked_by": 1, "acked_by_email": "alice@example.org",
                 "ack_comment": "vu"},
                {**base, "monitor_id": 5, "monitor_name": "Hôte", "priority": 1, "group": "host:web-02", "state": "ALERT",
                 "since": NOW - timedelta(minutes=10), "suppressed": "downtime"}]
    if s.startswith("SELECT l.ref, max(i.id) AS incident_id"):
        return [{"ref": "3|host:web-01", "incident_id": 7}]
    if "count(*) FILTER (WHERE ms.acked_at IS NULL" in s:
        return [{"unacked": 2, "total": 3}]
    # ── maintenances ──
    if "FROM downtimes WHERE org_id = %s AND id = %s" in s:
        return [dict(DT_ROW)]
    if s.startswith("INSERT INTO downtimes"):
        return [dict(DT_ROW, name=params[1], scope=json.loads(params[3]), start_ts=params[4], end_ts=params[5],
                     recurrence=json.loads(params[6]) if params[6] else None)]
    if s.startswith("UPDATE downtimes"):
        return [dict(DT_ROW, name=params[0], scope=json.loads(params[2]), start_ts=params[3], end_ts=params[4],
                     recurrence=json.loads(params[5]) if params[5] else None)]
    if s.startswith("DELETE FROM downtimes"):
        return [dict(DT_ROW)]
    if "FROM downtimes WHERE org_id = %s ORDER BY start_ts DESC" in s or \
            "FROM downtimes WHERE org_id = %s AND enabled AND start_ts <= now()" in s:
        later = f"{(NOW + timedelta(hours=6)).hour:02d}:00"           # jamais active pendant le test
        rec = dict(DT_ROW, id=9, name="Nuit", start_ts=NOW - timedelta(days=30), end_ts=None,
                   recurrence={"type": "daily", "start": later, "duration_s": 600, "tz": "UTC"})
        return [dict(DT_ROW), rec]
    if "FROM downtimes WHERE org_id = %s AND enabled AND (end_ts IS NULL" in s:
        return [dict(DT_ROW, id=11, name="Bascule", start_ts=NOW + timedelta(days=2),
                     end_ts=NOW + timedelta(days=2, hours=1))]
    if "FROM downtimes WHERE enabled AND start_ts <= %s" in s:
        return STATE.get("worker_downtimes", [])
    if s.startswith("SELECT aggregation_key, event_type FROM events"):
        return STATE.get("seen_events", [])
    # ── incidents ──
    if "FROM incidents i LEFT JOIN users cu ON cu.id = i.commander_id" in s:
        if "WHERE i.org_id = %s AND i.id = %s" in s and params[1] == 404:
            return []
        return [{"id": 7, "number": 12, "title": "API en panne", "severity": 2, "status": STATE.get("inc_status", "active"),
                 "commander_id": None, "commander_email": None, "customer_impact": "", "summary": "", "postmortem": "",
                 "fields": {}, "status_page_ids": [], "public_message": "", "detected_at": NOW - timedelta(hours=1),
                 "resolved_at": None, "created_by": 1, "created_at": NOW, "updated_at": NOW, "links_count": 2}]
    if s.startswith("SELECT coalesce(max(number), 0) + 1 AS n FROM incidents"):
        return [{"n": 12}]
    if s.startswith("INSERT INTO incidents"):
        return [{"id": 7}]
    if s.startswith("UPDATE incidents SET title"):
        STATE["inc_status"] = params[2]
        return []
    if s.startswith("SELECT l.incident_id, l.ref FROM incident_links l"):
        return [{"incident_id": 7, "ref": str(params[1])}]
    if s.startswith("INSERT INTO incident_timeline"):
        return [{"id": 1, "ts": NOW, "kind": "note", "author_id": 1, "body": "x", "data": {}}]
    if s.startswith("INSERT INTO incident_links"):
        return [{"incident_id": 7}]
    if s.startswith("SELECT kind, ref FROM incident_links"):
        return [{"kind": "monitor", "ref": "3|host:web-01"}, {"kind": "host", "ref": "10"}]
    if "FROM incident_timeline t LEFT JOIN users u" in s:
        return [{"id": 1, "ts": NOW, "kind": "note", "author_id": 1, "author": "alice@example.org", "body": "x", "data": {}}]
    if s.startswith("DELETE FROM incident_links"):
        return [] if params[2] == "absent" else [{"kind": params[1]}]
    if s.startswith("SELECT user_id FROM memberships"):
        return [] if params[1] == 999 else [{"user_id": params[1]}]
    if s.startswith("SELECT state, value, since FROM monitor_states"):
        return [{"state": "ALERT", "value": 95.0, "since": NOW}]
    if s.startswith("SELECT ts, from_state, to_state, value FROM alert_events WHERE org_id = %s AND monitor_id = %s AND group_key"):
        return [{"ts": NOW - timedelta(minutes=5), "from_state": "OK", "to_state": "ALERT", "value": 95.0}]
    if s.startswith("SELECT id FROM hosts WHERE org_id = %s AND hostname = %s"):
        return [{"id": 10}]
    if s.startswith("SELECT settings FROM orgs WHERE id = %s"):
        return [{"settings": {"incident_channel_ids": [1]}}]
    if "FROM alert_events a JOIN monitors m ON m.id = a.monitor_id WHERE a.org_id = %s AND a.monitor_id = ANY" in s:
        return [{"ts": NOW - timedelta(minutes=50), "monitor": "CPU", "monitor_id": 3, "group_key": "host:web-01",
                 "from_state": "OK", "to_state": "ALERT", "value": 95.0}]
    if s.startswith("SELECT hostname FROM hosts WHERE org_id = %s AND id = ANY"):
        return [{"hostname": "web-01"}]
    if "FROM logs WHERE org_id = %s AND hostname = ANY" in s:
        return [{"ts": NOW, "status": "error", "service": "api", "message": "connexion refusée"}]
    # ── canaux ──
    if "FROM notification_channels WHERE org_id = %s AND id = ANY(%s::bigint[]) AND enabled AND kind = 'email'" in s:
        return [{"id": 1, "kind": "email", "name": "Boîte", "config": {"to": ["ops@example.org"]}}]
    if s.startswith("SELECT id, kind FROM notification_channels"):
        return [{"id": i, "kind": "email" if i == 1 else "slack"} for i in params[1]]
    if "FROM notification_channels WHERE org_id = %s AND id = ANY(%s::bigint[]) AND enabled" in s:
        kinds = STATE.get("channel_kinds", {})
        return [{"id": i, "kind": kinds.get(i, "webhook"), "name": f"c{i}",
                 "config": {"to": ["ops@example.org"]} if kinds.get(i) == "email" else {"url": "https://example.org/h"}}
                for i in params[1]]
    if s.startswith("SELECT id, kind, name, config FROM notification_channels WHERE org_id = %s AND id = %s"):
        return [] if params[1] == 404 else [{"id": params[1], "kind": "pagerduty", "name": "PD",
                                             "config": {"routing_key": "a" * 32}}]
    if s.startswith("INSERT INTO notification_channels"):
        return [{"id": 1, "kind": params[1], "name": params[2], "config": json.loads(params[3]), "enabled": params[4],
                 "created_at": NOW}]
    if s.startswith("SELECT id FROM notification_channels WHERE org_id = %s AND id = ANY"):
        return [{"id": i} for i in params[1] if i != 999]
    # ── pages de statut ──
    if "FROM status_pages WHERE org_id = %s AND id = %s" in s or s.startswith("INSERT INTO status_pages") or \
            s.startswith("UPDATE status_pages") or "FROM status_pages WHERE org_id = %s ORDER BY" in s:
        return [dict(PAGE)]
    if s.startswith("SELECT id FROM status_pages WHERE slug = %s"):
        return [{"id": 99}] if params[0] == "pris" else []
    if s.startswith("DELETE FROM status_pages"):
        return [{"id": 2, "slug": "parc", "title": "Parc S33D"}]
    if "FROM status_pages WHERE slug = %s AND public" in s:
        return [{"id": 2, "org_id": 1, "title": "Parc S33D", "description": "", "components": PAGE_COMPONENTS}] \
            if params[0] == "parc" else []
    if "FROM slos WHERE org_id = %s AND id = ANY" in s or "FROM slos WHERE org_id = %s ORDER BY name, id LIMIT 50" in s:
        return [dict(SLO)]
    if s.startswith("SELECT id, hostname, last_seen, first_seen FROM hosts"):
        return [{"id": 10, "hostname": "web-01", "last_seen": NOW, "first_seen": NOW - timedelta(days=200)}]
    if s.startswith("SELECT id, priority, created_at, no_data_s FROM monitors WHERE org_id = %s AND type = 'host'"):
        return [{"id": 20, "priority": 1, "created_at": NOW - timedelta(days=200), "no_data_s": 300}]
    if s.startswith("SELECT id, priority, created_at, no_data_s FROM monitors WHERE org_id = %s AND id = ANY"):
        return [{"id": i, "priority": 2 if i == 3 else 1, "created_at": NOW - timedelta(days=200), "no_data_s": 300}
                for i in params[1]]
    if s.startswith("SELECT DISTINCT ON (monitor_id, group_key) monitor_id, ts"):
        return []
    if s.startswith("SELECT monitor_id, ts, group_key, from_state, to_state FROM alert_events"):
        return [r for r in ALERT_HISTORY if r["monitor_id"] in params[1]]
    if s.startswith("SELECT ms.monitor_id, ms.group_key, ms.state FROM monitor_states ms"):
        cur = {3: [("*", "OK")], 20: [("host:web-01", "OK")]}
        return [{"monitor_id": i, "group_key": g, "state": st} for i in params[1] for g, st in cur.get(i, [])]
    if s.startswith("SELECT id, created_at, no_data_s FROM monitors"):
        return [{"id": params[1], "created_at": NOW - timedelta(days=200), "no_data_s": 600}]
    if s.startswith("SELECT DISTINCT ON (test_id) test_id, ok FROM synthetic_results"):
        return [{"test_id": 12, "ok": False}]
    if "date_trunc('day', ts) AS d" in s:
        return [{"test_id": 12, "d": NOW.replace(hour=0, minute=0, second=0, microsecond=0), "n": 100, "ok": 99}]
    if "AND %s = ANY(status_page_ids)" in s:
        return [{"id": 7, "number": 12, "title": "API en panne", "status": "resolved", "severity": 2,
                 "detected_at": NOW - timedelta(hours=2), "resolved_at": NOW - timedelta(hours=1)}]
    if "kind = 'public_update'" in s:
        return [{"incident_id": 7, "ts": NOW - timedelta(minutes=90), "body": "Correctif déployé"}]
    # ── rapports ──
    if "FROM report_schedules WHERE org_id = %s AND id = %s" in s or s.startswith("INSERT INTO report_schedules") or \
            s.startswith("UPDATE report_schedules SET name") or "FROM report_schedules WHERE org_id = %s ORDER BY" in s:
        return [dict(REPORT)]
    if "FROM report_schedules WHERE enabled" in s:
        return [dict(REPORT)]
    if s.startswith("DELETE FROM report_schedules"):
        return [{"id": 6, "name": "Hebdo", "kind": "summary"}]
    if s.startswith("SELECT name FROM orgs WHERE id = %s"):
        return [{"name": "S33D"}]
    if s.startswith("SELECT id, no_data_s FROM monitors WHERE org_id = %s AND type = 'host'"):
        return [{"id": 20, "no_data_s": 300}]
    if s.startswith("SELECT id, no_data_s FROM monitors WHERE org_id = %s AND id = ANY"):
        return [{"id": i, "no_data_s": 600} for i in params[1]]
    if s.startswith("SELECT m.priority, count(*) AS n FROM alert_events a"):
        return [{"priority": 2, "n": 3}, {"priority": None, "n": 1}]
    if s.startswith("SELECT m.id, m.name, m.priority, count(*) AS n FROM alert_events a"):
        return [{"id": 3, "name": "CPU", "priority": 2, "n": 4}]
    if s.startswith("SELECT DISTINCT a.monitor_id FROM alert_events a"):
        return [{"monitor_id": 3}]
    if "FROM incidents WHERE org_id = %s AND (detected_at >= %s" in s:
        return [{"number": 12, "title": "API en panne", "severity": 2, "status": "resolved",
                 "detected_at": NOW - timedelta(hours=2), "resolved_at": NOW - timedelta(hours=1)}]
    if s.startswith("SELECT hostname, first_seen, last_seen FROM hosts"):
        return [{"hostname": "neuf", "first_seen": NOW - timedelta(days=1), "last_seen": NOW}]
    if "FROM host_inventory i JOIN hosts h" in s:
        return [{"hostname": "web-01", "security": 3, "pending": 12}]
    if "r.details ->> 'ssl_days_left' AS days" in s:
        return [{"name": "site public", "days": "12.5"}]
    if "s.name = 'tls.cert.days_left'" in s:
        return [{"tags": {"host": "web-01", "cert_path": "/etc/x.pem"}, "value": 45.0}]
    if s.startswith("SELECT title, widgets, template_vars FROM dashboards"):
        return [{"title": "Vue", "widgets": [{"id": "w1", "type": "timeseries", "title": "CPU",
                                               "queries": [{"q": "avg:system.cpu.usage{$host} by {host}"}]},
                                              {"id": "w2", "type": "note"}],
                 "template_vars": [{"name": "host", "tag": "host", "default": "*"}]}]
    # ── journal d'audit ──
    if "FROM audit_log WHERE org_id = %s" in s:
        rows = [{"id": i, "ts": NOW - timedelta(seconds=10 - i), "actor": "alice@example.org", "user_id": 1,
                 "action": "create", "object_type": "monitor", "object_id": str(i), "summary": "x", "diff": {}, "ip": None}
                for i in (9, 8, 7)]
        if "(ts, id) < (%s, %s)" in s:
            rows = [r for r in rows if r["id"] < params[-2]]
        return rows[:params[-1]]
    # ── actions à distance ──
    if "FROM hosts h LEFT JOIN agents a ON a.id = h.agent_id" in s:
        if params[1] in (404, "inconnu"):
            return []
        if params[1] == 11:
            return [{**HOST_AGENT, "host_id": 11, "kind": "network_device", "agent_id": None}]
        if params[1] == 12:
            return [{**HOST_AGENT, "host_id": 12, "capabilities": {}}]
        return [dict(HOST_AGENT)]
    if s.startswith("INSERT INTO agent_actions"):
        return [{"id": 5}]
    if "FROM agent_actions a LEFT JOIN hosts h" in s:
        return [{"id": 5, "host_id": 10, "hostname": "web-01", "agent_id": HOST_AGENT["agent_id"], "kind": "restart_service",
                 "params": {"unit": "nginx.service"}, "reason": "", "origin": {}, "status": STATE.get("action_status", "pending_approval"),
                 "proposed_by": STATE.get("proposer", 1), "proposed_by_email": "alice@example.org", "proposed_by_ai": False,
                 "approved_by": None, "approved_by_email": None, "approved_at": None, "sent_at": None, "finished_at": None,
                 "exit_code": None, "output": "", "expires_at": NOW + timedelta(hours=1), "created_at": NOW}]
    if s.startswith("UPDATE agent_actions SET status = 'approved'") or s.startswith("UPDATE agent_actions SET status = 'rejected'"):
        return [{"id": 5}] if STATE.get("action_status", "pending_approval") == "pending_approval" else []
    if s.startswith("UPDATE agent_actions SET status = 'expired'"):
        return [{"id": 5, "org_id": 1, "kind": "restart_service", "params": {"unit": "nginx.service"}, "host_id": 10,
                 "was_approved": True}]
    if s.startswith("SELECT DISTINCT check_name, tags ->> 'unit' AS unit"):
        return [{"check_name": "systemd.unit", "unit": "nginx.service", "service": None}]
    if s.startswith("SELECT DISTINCT name FROM process_snapshots"):
        return [{"name": "php-fpm8.3"}]
    # ── explication (contexte minimal) ──
    if "FROM hosts WHERE org_id = %s AND id = %s" in s or "FROM hosts WHERE org_id = %s AND hostname = %s" in s:
        return [{"id": 10, "hostname": "web-01", "os": "linux", "platform": "ubuntu", "platform_version": "26.04",
                 "kernel": "7.0.0", "arch": "amd64", "cpu_cores": 8, "mem_total": 1 << 34, "tags": {}, "meta": {},
                 "last_seen": NOW, "boot_time": None}]
    # ── mesures des nouveaux types (worker) ──
    if "FROM events e LEFT JOIN hosts h" in s and "count(*) AS n" in s:
        return [{"g0": "web-01", "n": 4}]
    if "count(*) FILTER (WHERE" in s and "process_snapshots" in s:
        return [{"hostname": "web-01", "n": 0}, {"hostname": "web-02", "n": 2}]
    if "FROM error_issues i JOIN error_issue_hits h" in s:
        return [{"n": 3.0}]
    if s.startswith("SELECT m.id, ms.state FROM monitors m LEFT JOIN monitor_states ms"):
        return [{"id": 3, "state": "ALERT"}, {"id": 3, "state": "OK"}, {"id": 4, "state": "WARN"}]
    if "AS g0, count(*) AS n FROM logs" in s:
        return [{"g0": "203.0.113.9", "n": 25}]
    if "FROM logs WHERE org_id = %s AND hostname = %s AND ts > %s AND status = ANY" in s:
        return [{"ts": NOW, "status": "error", "service": "nginx", "message": "upstream timed out"}]
    if s.startswith("SELECT m.*, o.settings AS org_settings FROM monitors m"):
        return STATE.get("worker_monitors", [])
    if "m.type = 'host' AND m.enabled AND ms.state = 'ALERT'" in s:
        STATE.setdefault("down_queries", 0)
        STATE["down_queries"] += 1
        return [{"org_id": 1, "group_key": "host:web-02"}]
    return []


class FakeConn:
    def __init__(self, log):
        self.log = log

    async def execute(self, sql, params=None):
        n = placeholders(sql)
        assert n == len(params or ()), f"{n} %s pour {len(params or ())} paramètres :\n{sql}\n{params}"
        self.log.append(("SQL", sql, params))
        return FakeCursor(respond(sql, params), self.log)

    def cursor(self):
        return FakeCursor([], self.log)


class FakeDatabase:
    def __init__(self):
        self.log = []
        self.state = "ok"

    def mark(self, ok):
        self.state = "ok" if ok else "erreur"

    @asynccontextmanager
    async def writer(self, timeout=5.0):
        yield FakeConn(self.log)

    reader = writer


@pytest.fixture()
def client(monkeypatch):
    STATE.clear()
    get_settings.cache_clear()
    db = FakeDatabase()
    set_runtime(Runtime(settings=get_settings(), db=db))
    sent = []

    async def fake_deliver_all(settings, notices):
        sent.extend(notices)
        return [(True, "envoyé") for _ in notices]

    from hypha.api import incidents as inc_route
    from hypha.api import reports as rep_route
    monkeypatch.setattr(inc_route, "deliver_all", fake_deliver_all)
    monkeypatch.setattr(rep_route, "deliver_all", fake_deliver_all)
    from hypha.api import status_pages
    status_pages._cache.clear()
    c = TestClient(create_app(), raise_server_exceptions=True)
    c.db, c.sent = db, sent
    yield c
    set_runtime(None)


def sqls(client, needle: str) -> list:
    return [e for e in client.db.log if e[0] == "SQL" and needle in " ".join(e[1].split())]


def audited(client, action: str, object_type: str) -> bool:
    # Paramètres d'audit.record : org, user, actor, action, object_type, object_id, summary, diff, ip, ua.
    return any(e[2][3] == action and e[2][4] == object_type for e in sqls(client, "INSERT INTO hypha.audit_log"))


# ── § 17 : problèmes, acquittement ─────────────────────────────────────────────

def test_problems_view_and_count(client):
    r = client.get("/api/v1/problems", headers=VIEWER)
    assert r.status_code == 200, r.text
    rows = r.json()
    assert [x["monitor_id"] for x in rows] == [5, 3, 4]               # P1, P2, puis non classée
    assert rows[1]["incident_id"] == 7 and rows[1]["host"] == "web-01" and rows[1]["duration_s"] >= 3599
    assert rows[2]["acked"]["by"] == "alice@example.org" and rows[2]["acked"]["comment"] == "vu"
    assert rows[0]["suppressed"] == "downtime" and rows[0]["acked"] is None
    assert set(rows[0]) >= {"monitor_id", "monitor_name", "type", "priority", "group", "host", "state", "value", "since",
                            "duration_s", "acked", "suppressed", "flapping", "escalation_step", "muted", "incident_id",
                            "last_notified"}
    r = client.get("/api/v1/problems?priority=1,2&acked=false&suppressed=none&q=web", headers=VIEWER)
    assert r.status_code == 200
    sql, params = sqls(client, "LIMIT 5000")[-1][1:]
    assert "m.priority = ANY(%s::smallint[])" in sql and params[1] == [1, 2] and params[-1] == "%web%"
    assert client.get("/api/v1/problems?priority=9", headers=VIEWER).status_code == 400
    assert client.get("/api/v1/problems?suppressed=autre", headers=VIEWER).status_code == 400
    assert client.get("/api/v1/problems/count", headers=VIEWER).json() == {"unacked": 2, "total": 3}


def test_ack_and_unack(client):
    r = client.post("/api/v1/monitors/3/groups/ack", headers=MEMBER, json={"group": "host:web-01", "comment": "je regarde"})
    assert r.status_code == 200, r.text
    assert r.json()["acked"][0]["group"] == "host:web-01"
    ev = sqls(client, "'monitor', 'ack'")
    assert ev and ev[0][2][1] == "Acquitté : CPU (host:web-01)" and "je regarde" in ev[0][2][2]
    assert audited(client, "ack", "monitor")
    r = client.post("/api/v1/monitors/3/groups/ack", headers=MEMBER, json={"groups": ["host:a", "host:b"]})
    assert len(r.json()["acked"]) == 2
    r = client.post("/api/v1/monitors/3/groups/ack", headers=MEMBER, json={"group": "host:ok"})
    assert r.status_code == 409 and r.json()["error"] == "not_in_alert"
    assert client.post("/api/v1/monitors/3/groups/ack", headers=VIEWER, json={"group": "x:y"}).status_code == 403
    assert client.post("/api/v1/monitors/3/groups/ack", headers=MEMBER, json={}).status_code == 400
    assert client.delete("/api/v1/monitors/3/groups/ack?group=host:web-01", headers=MEMBER).status_code == 200
    assert audited(client, "unack", "monitor")
    assert client.delete("/api/v1/monitors/3/groups/ack?group=none", headers=MEMBER).status_code == 404


# ── §§ 17, 19 : définition, modèles, aperçu, composites ───────────────────────

def test_monitor_priority_options_and_audit(client):
    body = {"name": "CPU", "type": "metric", "query": {"q": "avg:system.cpu.usage{*} by {host}"},
            "thresholds": {"critical": 90}, "priority": 2, "message": "{{#is_alert}}@canal-1{{/is_alert}}",
            "options": {"require_consecutive": 2, "escalations": [{"after_s": 900, "channel_ids": [1]}]}}
    r = client.post("/api/v1/monitors", headers=MEMBER, json=body)
    assert r.status_code == 201, r.text
    ins = sqls(client, "INSERT INTO monitors")[0][2]
    assert ins[12] == 2 and json.loads(ins[13])["require_consecutive"] == 2
    assert audited(client, "create", "monitor")
    r = client.post("/api/v1/monitors", headers=MEMBER, json={**body, "message": "@canal-999"})
    assert r.status_code == 400 and "999" in r.json()["detail"]
    r = client.post("/api/v1/monitors", headers=MEMBER, json={**body, "options": {"depends_on": [999]}})
    assert r.status_code == 400
    assert client.put("/api/v1/monitors/3", headers=MEMBER, json={"priority": 1}).status_code == 200
    assert audited(client, "update", "monitor")
    assert client.post("/api/v1/monitors/3/mute", headers=MEMBER, json={"until": 0}).status_code == 200
    assert audited(client, "mute", "monitor")
    assert client.delete("/api/v1/monitors/3", headers=MEMBER).status_code == 200
    assert audited(client, "delete", "monitor")


def test_composite_cycles_refused(client):
    STATE["composites"] = [{"id": 30, "query": {"expression": "31", "ids": [31]}},
                           {"id": 31, "query": {"expression": "3", "ids": [3]}}]
    comp = {"name": "Composite", "type": "composite", "query": {"expression": "30 && 3"}}
    assert client.post("/api/v1/monitors", headers=MEMBER, json=comp).status_code == 201
    r = client.post("/api/v1/monitors", headers=MEMBER, json={**comp, "query": {"expression": "30 && 999"}})
    assert r.status_code == 400 and "999" in r.json()["detail"]
    r = client.put("/api/v1/monitors/31", headers=MEMBER, json={"type": "composite", "query": {"expression": "30"}})
    assert r.status_code == 400 and r.json()["error"] == "composite_cycle"
    r = client.put("/api/v1/monitors/30", headers=MEMBER, json={"type": "composite", "query": {"expression": "30"}})
    assert r.status_code == 400 and "lui-même" in r.json()["detail"]


def test_templates_and_from_template(client):
    r = client.get("/api/v1/monitors/templates", headers=VIEWER)
    assert r.status_code == 200
    cat = {t["id"]: t for t in r.json()}
    assert "disk_forecast" in cat and cat["disk_forecast"]["definition"]["type"] == "forecast"
    assert "definition" not in cat["synthetic_failed"]
    r = client.post("/api/v1/monitors/from-template", headers=MEMBER,
                    json={"template": "disk_forecast", "vars": {"scope": "env:prod"}, "channel_ids": [1]})
    assert r.status_code == 201, r.text
    ins = sqls(client, "INSERT INTO monitors")[-1][2]
    assert ins[2] == "forecast" and json.loads(ins[3])["q"] == "max:system.disk.in_use{env:prod} by {host,mount}"
    assert ins[6] == [1]
    r = client.post("/api/v1/monitors/from-template", headers=MEMBER, json={"template": "nope"})
    assert r.status_code == 400 and r.json()["error"] == "invalid_template"


def test_monitor_preview(client):
    r = client.post("/api/v1/monitors/preview", headers=MEMBER, json={
        "type": "metric", "query": {"q": "avg:system.cpu.usage{*} by {host}"}, "thresholds": {"critical": 5}})
    assert r.status_code == 200, r.text
    out = r.json()
    assert out["groups"] == [{"group": "host:web-01", "state": "ALERT", "value": 10.0},
                             {"group": "host:web-02", "state": "ALERT", "value": 10.0}]
    assert len(out["series"]) == 2 and out["bounds"] == [] and out["forecast"] == []
    r = client.post("/api/v1/monitors/preview", headers=MEMBER, json={
        "type": "anomaly", "query": {"q": "avg:system.load.norm.1{*} by {host}", "algorithm": "basic"}})
    assert r.status_code == 200, r.text
    assert r.json()["groups"][0]["state"] == "OK" and r.json()["groups"][0]["value"] == 0.0
    assert r.json()["bounds"] and len(r.json()["bounds"][0]) == 3
    STATE["series_mode"] = "trend"
    r = client.post("/api/v1/monitors/preview", headers=MEMBER, json={
        "type": "forecast", "query": {"q": "max:system.disk.in_use{*} by {host}", "horizon_s": 172800},
        "thresholds": {"critical": 95}})
    assert r.status_code == 200, r.text
    g = r.json()["groups"][0]
    assert g["value"] > 0 and r.json()["forecast"] and len(r.json()["forecast"][0]) == 4
    assert not any(e for e in sqls(client, "INSERT INTO monitor_states"))      # rien d'enregistré
    r = client.post("/api/v1/monitors/preview", headers=MEMBER, json={"type": "metric", "query": {"q": "median:x"}})
    assert r.status_code == 400
    assert client.post("/api/v1/monitors/preview", headers=VIEWER, json={"type": "metric"}).status_code == 403


# ── § 16 : maintenances ────────────────────────────────────────────────────────

def test_downtimes_routes(client):
    r = client.get("/api/v1/downtimes", headers=VIEWER)
    assert r.status_code == 200, r.text
    rows = r.json()
    assert rows[0]["id"] == 8 and rows[0]["active"] is True and rows[0]["next_end"] == dt_to_ms(DT_ROW["end_ts"])
    assert rows[1]["active"] is False and rows[1]["next_start"] > dt_to_ms(NOW)
    r = client.get("/api/v1/downtimes/active", headers=VIEWER)
    assert [d["id"] for d in r.json()] == [8]
    r = client.post("/api/v1/downtimes", headers=MEMBER, json={"name": "Patch web-01", "duration_s": 3600,
                                                                "scope": {"tags": {"host": "web-01"}}})
    assert r.status_code == 201, r.text
    assert r.json()["active"] is True and r.json()["scope"]["tags"] == {"host": "web-01"}
    assert audited(client, "create", "downtime")
    r = client.post("/api/v1/downtimes", headers=MEMBER, json={
        "name": "Nuit", "recurrence": {"type": "weekly", "days": [1, 3], "start": "02:00", "duration_s": 1800,
                                       "tz": "Europe/Paris"}})
    assert r.status_code == 201 and r.json()["recurrence"]["days"] == [1, 3]
    assert client.post("/api/v1/downtimes", headers=MEMBER, json={"name": "x", "scope": {"monitor_ids": [999]}}).status_code == 400
    assert client.post("/api/v1/downtimes", headers=MEMBER, json={"name": "x", "recurrence": {"type": "yearly"}}).status_code == 400
    assert client.post("/api/v1/downtimes", headers=VIEWER, json={"name": "x"}).status_code == 403
    r = client.put("/api/v1/downtimes/8", headers=MEMBER, json={"name": "Patch prolongé", "duration_s": 7200})
    assert r.status_code == 200 and r.json()["name"] == "Patch prolongé"
    assert audited(client, "update", "downtime")
    assert client.delete("/api/v1/downtimes/8", headers=MEMBER).status_code == 200
    assert audited(client, "delete", "downtime")
    end = sqls(client, "'downtime', %s")
    assert end and end[-1][2][2] == "downtime_end"                    # supprimée en cours : fin marquée


# ── § 18 : canaux ──────────────────────────────────────────────────────────────

def test_new_channels_validation_redaction_and_test(client, monkeypatch):
    good = [("telegram", {"bot_token": "123456:ABCdefGhIJKlmnoPQRstuVWxyz_0123", "chat_id": -100123}),
            ("discord", {"url": "https://discord.com/api/webhooks/1/abc"}),
            ("googlechat", {"url": "https://chat.googleapis.com/v1/spaces/x/messages?key=k"}),
            ("mattermost", {"url": "https://mm.example.org/hooks/abc"}),
            ("ntfy", {"url": "https://ntfy.sh", "topic": "hypha-ops", "token": "tk_x"}),
            ("pagerduty", {"routing_key": "R" * 32}), ("opsgenie", {"api_key": "0f0f0f0f-1111-2222-3333-444444444444"})]
    for kind, cfg in good:
        r = client.post("/api/v1/channels", headers=OWNER, json={"kind": kind, "name": kind, "config": cfg})
        assert r.status_code == 201, (kind, r.text)
    bad = [("telegram", {"bot_token": "x", "chat_id": 1}), ("discord", {"url": "http://discord.com/x"}),
           ("ntfy", {"topic": "a b"}), ("pagerduty", {"routing_key": "court"}),
           ("opsgenie", {"api_key": "0f0f0f0f-1111-2222-3333-444444444444", "region": "asia"}), ("sms", {})]
    for kind, cfg in bad:
        r = client.post("/api/v1/channels", headers=OWNER, json={"kind": kind, "name": kind, "config": cfg})
        assert r.status_code == 400, (kind, r.text)
    diffs = [json.loads(e[2][7]) for e in sqls(client, "INSERT INTO hypha.audit_log") if e[2][4] == "channel"]
    assert diffs and all("123456:ABC" not in json.dumps(d) and "R" * 32 not in json.dumps(d) for d in diffs)
    from hypha.api.settings_routes import _redact
    from hypha.auth.principal import Principal
    row = {"id": 1, "kind": "telegram", "config": {"bot_token": "123:abc", "chat_id": "-1"}}
    assert _redact(row, Principal(1, None, None, 1, "member"))["config"] == {"chat_id": "-1"}
    sent = []

    async def fake_send(settings, http, notice):
        sent.append(notice)
        if notice.channel["id"] == 2:
            raise RuntimeError("réponse 403")

    from hypha.monitors import notify
    monkeypatch.setattr(notify, "send", fake_send)
    r = client.post("/api/v1/channels/1/test", headers=OWNER)
    assert r.status_code == 200 and r.json() == {"ok": True, "detail": "message d'essai envoyé"}
    assert sent[0].kind == "test" and sent[0].channel["kind"] == "pagerduty"
    assert client.post("/api/v1/channels/2/test", headers=OWNER).json() == {"ok": False, "detail": "réponse 403"}
    assert audited(client, "test", "channel")
    assert client.post("/api/v1/channels/404/test", headers=OWNER).status_code == 404
    assert client.post("/api/v1/channels/1/test", headers=MEMBER).status_code == 403


def test_settings_writes_are_audited(client):
    r = client.post("/api/v1/keys", headers=OWNER, json={"name": "parc"})
    assert r.status_code == 201 and audited(client, "create", "key")
    # La clé entière n'apparaît jamais dans l'audit, seulement son préfixe d'affichage.
    assert r.json()["key"] not in json.dumps([e[2] for e in sqls(client, "INSERT INTO hypha.audit_log")], default=str)
    assert client.post("/api/v1/org/members", headers=OWNER, json={"email": "a@example.org", "role": "viewer"}).status_code == 201
    assert audited(client, "create", "member")
    assert client.post("/api/v1/events", headers=MEMBER, json={"title": "Déploiement"}).status_code == 201
    assert audited(client, "create", "event")
    assert client.post("/api/v1/slos", headers=MEMBER, json={"name": "m", "type": "monitor", "monitor_id": 3,
                                                             "target": 99.5}).status_code == 201
    assert audited(client, "create", "slo")


# ── § 20 : incidents ───────────────────────────────────────────────────────────

def test_incidents_lifecycle(client):
    r = client.post("/api/v1/incidents", headers=MEMBER, json={
        "title": "API en panne", "severity": 2, "links": [{"kind": "monitor", "ref": "3|host:web-01"},
                                                          {"kind": "service", "ref": "api"}]})
    assert r.status_code == 201, r.text
    assert r.json()["number"] == 12 and r.json()["links"] and r.json()["timeline"]
    assert sqls(client, "pg_advisory_xact_lock(%s, (%s)::int)")[0][2][0] == 7268005
    assert len(sqls(client, "INSERT INTO incident_links")) == 2
    assert audited(client, "create", "incident")
    assert client.sent and client.sent[0].kind == "incident" and client.sent[0].dedup_key == "incident:7"
    for bad in ({"title": ""}, {"title": "x", "severity": 9}, {"title": "x", "links": [{"kind": "monitor", "ref": "999"}]},
                {"title": "x", "commander_id": 999}, {"title": "x", "status": "fermé"},
                {"title": "x", "links": [{"kind": "pizza", "ref": "1"}]}):
        assert client.post("/api/v1/incidents", headers=MEMBER, json=bad).status_code == 400, bad
    r = client.put("/api/v1/incidents/7", headers=MEMBER, json={"status": "resolved", "severity": 1})
    assert r.status_code == 200, r.text
    kinds = [e[2][2] for e in sqls(client, "INSERT INTO incident_timeline")]
    assert "status" in kinds and "severity" in kinds
    upd = sqls(client, "UPDATE incidents SET title")[0][2]
    assert upd[2] == "resolved" and upd[10] == "resolved"
    assert client.sent[-1].state == "OK"                               # résolu : résolution PagerDuty/Opsgenie
    r = client.post("/api/v1/incidents/7/timeline", headers=MEMBER, json={"kind": "public_update", "body": "Corrigé"})
    assert r.status_code == 201 and sqls(client, "UPDATE incidents SET public_message")
    assert client.post("/api/v1/incidents/7/timeline", headers=MEMBER, json={"kind": "ai", "body": "x"}).status_code == 400
    assert client.post("/api/v1/incidents/7/links", headers=MEMBER, json={"kind": "host", "ref": 10}).status_code == 201
    assert client.post("/api/v1/incidents/7/links", headers=MEMBER, json={"kind": "host", "ref": "abc"}).status_code == 400
    assert client.delete("/api/v1/incidents/7/links?kind=host&ref=10", headers=MEMBER).status_code == 200
    assert client.delete("/api/v1/incidents/7/links?kind=host&ref=absent", headers=MEMBER).status_code == 404
    assert client.get("/api/v1/incidents/404", headers=VIEWER).status_code == 404
    r = client.get("/api/v1/incidents?status=active,stable&severity=1,2", headers=VIEWER)
    assert r.status_code == 200 and r.json()[0]["duration_s"] >= 3599
    assert client.get("/api/v1/incidents?status=fermé", headers=VIEWER).status_code == 400


def test_incident_channel_settings(client):
    r = client.get("/api/v1/incidents/settings", headers=VIEWER)
    assert r.status_code == 200 and r.json() == {"incident_channel_ids": [1]}
    r = client.put("/api/v1/incidents/settings", headers=OWNER, json={"incident_channel_ids": [2, 1, 2]})
    assert r.status_code == 200 and r.json() == {"incident_channel_ids": [1, 2]}
    upd = sqls(client, "UPDATE orgs SET settings = settings || %s::jsonb")[0][2]
    assert json.loads(upd[0]) == {"incident_channel_ids": [1, 2]} and audited(client, "update", "org_settings")
    assert client.put("/api/v1/incidents/settings", headers=OWNER, json={"incident_channel_ids": [999]}).status_code == 400
    assert client.put("/api/v1/incidents/settings", headers=MEMBER, json={"incident_channel_ids": []}).status_code == 403


def test_incident_from_alert(client):
    r = client.post("/api/v1/incidents/from-alert", headers=MEMBER, json={"monitor_id": 3, "group": "host:web-01"})
    assert r.status_code == 201, r.text
    ins = sqls(client, "INSERT INTO incidents")[0][2]
    assert ins[2] == "CPU (host:web-01)" and ins[3] == 2               # sévérité tirée de la priorité
    links = [e[2][1:] for e in sqls(client, "INSERT INTO incident_links")]
    assert ("monitor", "3|host:web-01") in links and ("host", "10") in links
    alerts = [e for e in sqls(client, "INSERT INTO incident_timeline") if e[2][2] == "alert"]
    assert alerts and "OK → ALERT" in alerts[0][2][4]


def test_postmortem(client, monkeypatch):
    assert client.post("/api/v1/incidents/7/postmortem", headers=MEMBER).status_code == 503
    monkeypatch.setenv("HYPHA_LLM_URL", "http://llm.invalid:11434")
    get_settings.cache_clear()
    set_runtime(Runtime(settings=get_settings(), db=client.db))
    calls = []

    async def fake_chat(url, model, messages, timeout_s):
        calls.append(messages)
        return "## Résumé\nL'API est tombée.", 4321

    from hypha.api import incidents as route
    monkeypatch.setattr(route, "chat", fake_chat)
    r = client.post("/api/v1/incidents/7/postmortem", headers=MEMBER, json={"lang": "fr"})
    assert r.status_code == 200, r.text
    assert r.json()["postmortem"].startswith("## Résumé") and r.json()["context"]["logs"] == 1
    system, user = calls[0][0]["content"], calls[0][1]["content"]
    assert "## Cause racine" in system and "| Action | Responsable | Échéance |" in system
    assert "INC-12" in user and "connexion refusée" in user and "CPU (host:web-01)" in user
    assert sqls(client, "UPDATE incidents SET postmortem")[0][2][0].startswith("## Résumé")
    assert [e for e in sqls(client, "INSERT INTO incident_timeline") if e[2][2] == "ai"]
    get_settings.cache_clear()


# ── § 21 : pages de statut ─────────────────────────────────────────────────────

def test_status_pages_admin(client):
    body = {"slug": "parc", "title": "Parc S33D", "public": True, "components": PAGE_COMPONENTS}
    r = client.post("/api/v1/status-pages", headers=OWNER, json=body)
    assert r.status_code == 201, r.text
    assert r.json()["public_url"].endswith("/status/?p=parc") and audited(client, "create", "status_page")
    assert client.post("/api/v1/status-pages", headers=OWNER, json={**body, "slug": "pris"}).status_code == 409
    assert client.post("/api/v1/status-pages", headers=OWNER, json={**body, "slug": "-x"}).status_code == 400
    bad = {**body, "components": [{"name": "x", "kind": "monitor", "ref_id": 999}]}
    assert client.post("/api/v1/status-pages", headers=OWNER, json=bad).status_code == 400
    assert client.post("/api/v1/status-pages", headers=MEMBER, json=body).status_code == 403
    assert client.put("/api/v1/status-pages/2", headers=OWNER, json={"title": "Parc"}).status_code == 200
    assert client.delete("/api/v1/status-pages/2", headers=OWNER).status_code == 200
    assert client.get("/api/v1/status-pages", headers=OWNER).status_code == 200


def test_public_status_page(client):
    r = client.get("/api/public/status/parc")
    assert r.status_code == 200, r.text
    assert r.headers["cache-control"] == "public, max-age=30"
    out = r.json()
    assert set(out) == {"title", "description", "overall", "components", "incidents", "maintenances", "generated_at"}
    comps = {c["name"]: c for c in out["components"]}
    assert comps["API"]["status"] == "operational" and comps["API"]["group"] == "Services"
    alert_day = (NOW - timedelta(minutes=90)).date().isoformat()        # jour de l'alerte de 1 h du moniteur 3
    host_day = (NOW - timedelta(minutes=165)).date().isoformat()        # jour de la panne de 30 min de l'hôte
    api_day = {d["date"]: d for d in comps["API"]["days"]}[alert_day]
    assert len(comps["API"]["days"]) == 90 and api_day["uptime"] < 100 and api_day["worst"] == "partial_outage"
    assert comps["API"]["uptime_90d"] < 100
    assert comps["Site"]["status"] == "partial_outage" and comps["Site"]["days"][-1]["uptime"] == 99.0
    assert comps["Disponibilité"]["status"] == "operational"
    host = {d["date"]: d for d in comps["Serveur web"]["days"]}[host_day]
    assert comps["Serveur web"]["status"] == "operational" and host["worst"] == "major_outage"
    assert out["overall"] == "partial_outage"
    assert out["incidents"][0]["number"] == 12 and out["incidents"][0]["updates"][0]["body"] == "Correctif déployé"
    assert out["maintenances"][0]["name"] == "Bascule"
    text = r.text
    assert "web-01" not in text and "system.cpu" not in text and "ref_id" not in text and "monitor_id" not in text
    before = len(client.db.log)
    assert client.get("/api/public/status/parc").status_code == 200
    assert len(client.db.log) == before                                  # cache de 30 s
    assert client.get("/api/public/status/inconnu").status_code == 404
    assert client.get("/api/public/status/A_B").status_code == 404


# ── § 22 : rapports ────────────────────────────────────────────────────────────

def test_reports_routes(client):
    body = {"name": "Hebdo", "kind": "summary", "channel_ids": [1], "schedule": {"every": "week", "weekday": 1, "hour": 8}}
    r = client.post("/api/v1/reports", headers=MEMBER, json=body)
    assert r.status_code == 201, r.text
    assert r.json()["next_send"] > dt_to_ms(NOW) and audited(client, "create", "report")
    for bad in ({**body, "kind": "pdf"}, {**body, "channel_ids": [2]}, {**body, "schedule": {"every": "year"}},
                {**body, "kind": "dashboard"}, {**body, "period_s": 10}):
        assert client.post("/api/v1/reports", headers=MEMBER, json=bad).status_code == 400, bad
    r = client.get("/api/v1/reports/6/preview", headers=MEMBER)
    assert r.status_code == 200 and r.headers["content-type"].startswith("text/html")
    html = r.text
    for part in ("Disponibilité des hôtes", "web-01", "Alertes par priorité", "CPU", "INC-12", "neuf", "site public",
                 "Mises à jour de sécurité"):
        assert part in html, part
    assert "<script" not in html and "default-src 'none'" in r.headers["content-security-policy"]
    r = client.post("/api/v1/reports/6/send", headers=MEMBER)
    assert r.status_code == 200 and r.json()["ok"] is True and r.json()["sent_to"] == [1]
    assert client.sent[-1].html_body and client.sent[-1].kind == "report" and audited(client, "send", "report")
    assert client.put("/api/v1/reports/6", headers=MEMBER, json={"kind": "dashboard", "dashboard_id": 5}).status_code == 200
    assert client.delete("/api/v1/reports/6", headers=MEMBER).status_code == 200


def test_report_dashboard_build():
    from hypha.reports import service as S
    STATE.clear()
    report = dict(REPORT, kind="dashboard", dashboard_id=5)
    conn = FakeConn([])
    subject, html, text = asyncio.run(S.build(conn, get_settings(), report, 1))
    assert subject.startswith("[HYPHA] Hebdo") and "<polyline" in html and "host:web-01" in html
    assert "Tableau « Vue »" in html


# ── § 24 : audit ───────────────────────────────────────────────────────────────

def test_audit_route(client):
    r = client.get("/api/v1/audit?limit=2&object_type=monitor&action=create&actor=alice&q=x", headers=OWNER)
    assert r.status_code == 200, r.text
    out = r.json()
    assert [e["id"] for e in out["entries"]] == [9, 8] and out["next_cursor"]
    r = client.get(f"/api/v1/audit?limit=2&cursor={out['next_cursor']}", headers=OWNER)
    assert [e["id"] for e in r.json()["entries"]] == [7] and r.json()["next_cursor"] is None
    assert client.get("/api/v1/audit?cursor=!!!", headers=OWNER).status_code == 400
    assert client.get("/api/v1/audit", headers=MEMBER).status_code == 403


def test_logout_is_audited(client):
    client.cookies.set("hypha_session", "s" * 43)
    r = client.post("/api/auth/deconnexion")
    assert r.status_code == 200
    assert sqls(client, "DELETE FROM sessions s USING users u")


# ── § 28 : actions à distance ──────────────────────────────────────────────────

def test_actions_flow(client):
    body = {"host_id": 10, "kind": "restart_service", "params": {"unit": "nginx"}, "reason": "nginx en échec",
            "origin": {"monitor_id": 3, "group": "host:web-01"}}
    r = client.post("/api/v1/actions", headers=MEMBER, json=body)
    assert r.status_code == 201, r.text
    ins = sqls(client, "INSERT INTO agent_actions")[0][2]
    assert json.loads(ins[4]) == {"unit": "nginx.service"} and ins[2] == HOST_AGENT["agent_id"]
    assert audited(client, "propose", "action")
    assert client.post("/api/v1/actions", headers=MEMBER, json={**body, "kind": "stop_service"}).status_code == 409
    assert client.post("/api/v1/actions", headers=MEMBER, json={**body, "host_id": 11}).status_code == 409
    assert client.post("/api/v1/actions", headers=MEMBER, json={**body, "host_id": 12}).status_code == 409
    assert client.post("/api/v1/actions", headers=MEMBER, json={**body, "host_id": 404}).status_code == 404
    assert client.post("/api/v1/actions", headers=MEMBER, json={**body, "params": {"unit": "a;b"}}).status_code == 400
    assert client.post("/api/v1/actions", headers=MEMBER, json={**body, "origin": {"cmd": "rm"}}).status_code == 400
    assert client.post("/api/v1/actions", headers=VIEWER, json=body).status_code == 403
    assert client.post("/api/v1/actions/5/approve", headers=MEMBER).status_code == 403
    r = client.post("/api/v1/actions/5/approve", headers=OWNER)
    assert r.status_code == 200 and audited(client, "approve", "action")
    assert "interval '10 minutes'" in sqls(client, "UPDATE agent_actions SET status = 'approved'")[0][1]
    STATE["action_status"] = "sent"
    assert client.post("/api/v1/actions/5/approve", headers=OWNER).status_code == 409
    assert client.post("/api/v1/actions/5/reject", headers=OWNER).status_code == 409
    STATE["action_status"] = "pending_approval"
    assert client.post("/api/v1/actions/5/reject", headers=MEMBER, json={"comment": "pas maintenant"}).status_code == 200
    STATE["proposer"] = 42
    assert client.post("/api/v1/actions/5/reject", headers=MEMBER).status_code == 403
    assert audited(client, "reject", "action")
    r = client.get("/api/v1/actions?status=pending_approval,sent&host_id=10", headers=VIEWER)
    assert r.status_code == 200 and r.json()[0]["id"] == 5
    assert client.get("/api/v1/actions?status=fini", headers=VIEWER).status_code == 400
    assert client.get("/api/v1/actions/5", headers=VIEWER).status_code == 200


def test_explain_proposes_actions(monkeypatch):
    STATE.clear()
    monkeypatch.setenv("HYPHA_LLM_URL", "http://llm.invalid:11434")
    get_settings.cache_clear()
    db = FakeDatabase()
    set_runtime(Runtime(settings=get_settings(), db=db))
    from hypha.api import explain as route
    from hypha.explain.llm import HourlyQuota

    async def fake_chat(url, model, messages, timeout_s):
        return ("## Ce qui se passe\nnginx est arrêté.\n## Cause probable\nx\n## Correctif\n"
                "`sudo systemctl restart nginx` puis `systemctl restart php-fpm8.3` et `systemctl stop redis`\n"
                "## Éviter la récidive\ny"), 1000

    monkeypatch.setattr(route, "chat", fake_chat)
    monkeypatch.setattr(route, "_quota", HourlyQuota(60))
    c = TestClient(create_app())
    r = c.post("/api/v1/explain", headers=OWNER, json={"kind": "host", "host_id": 10, "lang": "fr"})
    assert r.status_code == 200, r.text
    proposed = r.json()["proposed_actions"]
    # nginx : vu par un contrôle ; php-fpm8.3 : processus connu ; redis : geste stop non autorisé par l'agent.
    assert [(a["kind"], a["params"]) for a in proposed] == [("restart_service", {"unit": "nginx.service"}),
                                                           ("restart_service", {"unit": "php-fpm8.3.service"})]
    assert proposed[0]["label"] == "Redémarrer nginx.service sur web-01" and proposed[0]["host_id"] == 10
    assert proposed[0]["reason"].startswith("Correctif proposé par l'IA")
    set_runtime(None)
    get_settings.cache_clear()


# ── worker ─────────────────────────────────────────────────────────────────────

def _monitor(**kw):
    base = {"id": 1, "org_id": 1, "name": "cpu", "type": "metric", "thresholds": {"critical": 0.5}, "message": "",
            "no_data_s": 600, "notify_no_data": False, "renotify_s": 0, "tags": {}, "muted_until": None,
            "channel_ids": [1], "priority": 2, "options": {},
            "query": {"q": "avg:system.cpu.usage{*} by {host}", "window_s": 300, "aggregation": "max", "comparator": ">"}}
    return {**base, **kw}


CHANNEL = {"id": 1, "kind": "webhook", "name": "x", "config": {"url": "https://example.org/hook"}}


def test_apply_downtime_suppresses_and_records():
    from hypha.monitors import downtime as D
    from hypha.monitors import maintenance, runner
    STATE.clear()
    log = []
    conn = FakeConn(log)
    org = maintenance.OrgContext(active=[D.Active(8, "Patch", {"monitor_ids": [1]}, None)])
    notices = asyncio.run(runner.apply(conn, _monitor(), {}, {"host:web-01": ("ALERT", 0.9)}, get_settings(),
                                       [CHANNEL], False, org))
    assert notices == []
    ev = [e for e in log if "INSERT INTO alert_events" in e[1]]
    assert ev and ev[0][2][-1] == "downtime"
    up = [e for e in log if "INSERT INTO monitor_states" in e[1]]
    assert up[0][2][-1] == "downtime"
    # Hôte en panne (moniteur host en ALERT) : les autres groupes de l'hôte passent en dépendance.
    org = maintenance.OrgContext(down_hosts={"web-01"})
    log.clear()
    notices = asyncio.run(runner.apply(conn, _monitor(id=2), {}, {"host:web-01": ("ALERT", 0.9),
                                                                  "host:web-02": ("ALERT", 0.9)},
                                       get_settings(), [CHANNEL], False, org))
    assert [n.group for n in notices] == ["host:web-02"]
    reasons = {e[2][1]: e[2][-1] for e in log if "INSERT INTO monitor_states" in e[1]}
    assert reasons == {"host:web-01": "dependency", "host:web-02": None}


def test_apply_mentions_logs_and_escalations():
    from hypha.monitors import evaluate as ev
    from hypha.monitors import runner
    STATE.clear()
    STATE["channel_kinds"] = {9: "email"}
    log = []
    conn = FakeConn(log)
    m = _monitor(id=40, message="{{#is_alert}}{{name}} sur {{host}} @canal-9{{/is_alert}}",
                 options={"escalations": [{"after_s": 60, "channel_ids": [8], "message": "Escalade {{name}}"}]})
    notices = asyncio.run(runner.apply(conn, m, {}, {"host:web-01": ("ALERT", 0.9)}, get_settings(), [CHANNEL], False))
    assert sorted(n.channel["id"] for n in notices) == [1, 9]
    mail = [n for n in notices if n.channel["id"] == 9][0]
    assert mail.message == "cpu sur web-01" and mail.logs and "upstream timed out" in mail.logs[0]
    assert mail.monitor["priority"] == 2 and mail.threshold == 0.5 and "explain=40" in mail.explain_url
    since = datetime.now(timezone.utc) - timedelta(minutes=5)
    prev = {"host:web-01": ev.PrevState("ALERT", 0.9, since, since, since)}
    notices = asyncio.run(runner.apply(conn, m, prev, {"host:web-01": ("ALERT", 0.9)}, get_settings(), [CHANNEL], False))
    assert [(n.kind, n.channel["id"], n.message) for n in notices] == [("escalation", 8, "Escalade cpu")]
    up = [e for e in log if "INSERT INTO monitor_states" in e[1]][-1][2]
    assert up[8] == 1                                                   # escalation_step


def test_incident_timeline_from_transitions():
    from hypha.monitors import runner
    STATE.clear()
    log = []
    conn = FakeConn(log)
    asyncio.run(runner._incident_timeline(conn, _monitor(), [("host:web-01", "OK", "ALERT", 0.9, True),
                                                             ("host:web-02", "ALERT", "OK", 0.1, True)],
                                          datetime.now(timezone.utc)))
    entries = [e[2] for e in log if "INSERT INTO incident_timeline" in e[1]]
    # Lien au moniteur entier (ref « 1 ») : une entrée par groupe qui change.
    assert [e[0] for e in entries] == [7, 7] and "cpu (host:web-01) : OK → ALERT" in entries[0][2]


def test_measure_new_types():
    from hypha.monitors import runner
    STATE.clear()
    conn = FakeConn([])

    def run(**kw):
        return asyncio.run(runner.measure(conn, _monitor(**kw), {}))

    assert run(type="event", thresholds={"critical": 3},
               query={"q": "source:snmp type:snmp_trap", "window_s": 300, "by": ["host"], "comparator": ">"}) == \
        {"host:web-01": ("ALERT", 4.0)}
    assert run(type="process", query={"name": "nginx", "match": "contains", "tags": {"role": "web", "host": "web-01"},
                                      "by": ["host"], "min": 1, "max": None}) == \
        {"host:web-01": ("ALERT", 0.0), "host:web-02": ("OK", 2.0)}
    assert run(type="error_tracking", thresholds={"critical": 0},
               query={"service": "api", "new_only": True, "window_s": 300, "comparator": ">"}) == {"*": ("ALERT", 3.0)}
    assert run(type="composite", query={"expression": "3 && !4", "warn_as_true": False, "ids": [3, 4]}) == \
        {"*": ("ALERT", 1.0)}
    assert run(type="composite", query={"expression": "3 && !4", "warn_as_true": True, "ids": [3, 4]}) == \
        {"*": ("OK", 0.0)}
    assert run(type="log", thresholds={"critical": 20},
               query={"q": "@evt.outcome:failure", "window_s": 300, "comparator": ">", "by": ["@network.client.ip"]}) == \
        {"@network.client.ip:203.0.113.9": ("ALERT", 25.0)}
    change = run(type="change", thresholds={"critical": 50},
                 query={"q": "sum:trace.hits{service:api}", "window_s": 300, "shift_s": 3600, "mode": "pct",
                        "aggregation": "avg", "comparator": ">"})
    assert change == {"*": ("OK", 0.0)}
    STATE["series_rows"] = [{"id": i, "tags": {"host": f"w{i}"}, "unit": None} for i in range(5)]
    STATE["series_mode"] = "outlier"
    out = run(type="outlier", query={"q": "avg:system.load.norm.1{*} by {host}", "window_s": 900, "algorithm": "mad",
                                     "tolerance": 3.0, "pct": 20.0})
    assert out["host:w3"][0] == "ALERT" and all(out[f"host:w{i}"][0] == "OK" for i in (0, 1, 2, 4))
    sync = run(type="synthetic", no_data_s=600, query={"test_id": 12, "failures": 2, "locations_min_failing": 1})
    assert sync == {"*": None}


def test_downtime_events():
    from hypha.monitors import maintenance
    STATE.clear()
    now = datetime.now(timezone.utc)
    row = dict(DT_ROW, scope={"tags": {"host": "web-01"}})
    log = []
    assert asyncio.run(maintenance.emit_events(FakeConn(log), [row], now)) == 1
    ins = [e for e in log if "INSERT INTO events" in e[1]][0][2]
    assert ins[2] == "downtime_start" and json.loads(ins[5]) == {"host": "web-01", "downtime_id": "8"}
    key = ins[6]
    STATE["seen_events"] = [{"aggregation_key": key, "event_type": "downtime_start"}]
    assert asyncio.run(maintenance.emit_events(FakeConn([]), [row], now)) == 0
    assert asyncio.run(maintenance.emit_events(FakeConn([]), [row], row["end_ts"] + timedelta(minutes=1))) == 1


def test_worker_tasks_expire_reports_and_evaluate(monkeypatch):
    from hypha.actions import service as actions_service
    from hypha.monitors import runner
    from hypha.reports import service as reports_service
    STATE.clear()
    db = FakeDatabase()
    assert asyncio.run(actions_service.expire(db)) == 1
    aud = [e for e in db.log if "INSERT INTO hypha.audit_log" in e[1]][0][2]
    assert aud[2] == "système" and aud[3] == "expire" and "jamais prise" in aud[6]
    sent = []

    async def fake_deliver_all(settings, notices):
        sent.extend(notices)
        return [(True, "envoyé") for _ in notices]

    monkeypatch.setattr(reports_service, "deliver_all", fake_deliver_all)
    assert asyncio.run(reports_service.tick(db, get_settings())) == 1
    assert sent and sent[0].html_body and sent[0].subject_override.startswith("[HYPHA] Hebdo")
    assert [e for e in db.log if e[1].strip().startswith("UPDATE report_schedules SET last_sent_at")]

    class Notifier:
        def __init__(self):
            self.notices = []

        def submit(self, n):
            self.notices.append(n)

    host = _monitor(id=20, type="host", query={"tags": {}}, thresholds={}, org_settings={}, enabled=True)
    comp = _monitor(id=30, type="composite", query={"expression": "3", "warn_as_true": False, "ids": [3]},
                    thresholds={}, org_settings={}, enabled=True)
    cpu = _monitor(id=31, org_settings={}, enabled=True)
    STATE["worker_monitors"] = [host, cpu, comp]
    notifier = Notifier()
    assert asyncio.run(runner.evaluate_all(db, notifier, get_settings())) == 3
    assert STATE["down_queries"] == 1
    groups = {(n.monitor["id"], n.group) for n in notifier.notices}
    assert (30, "*") in groups and (31, "host:web-01") in groups and (31, "host:web-02") not in groups
