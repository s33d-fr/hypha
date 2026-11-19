"""Routes de l'API exercées sans base : une fausse connexion répond, et vérifie que chaque requête SQL
reçoit autant de paramètres qu'elle a de `%s`. Aucun réseau, aucune base (règle du parc).

Sur un poste sans psycopg, des modules minimaux le remplacent le temps de l'import.
"""
import gzip
import json
import os
import re
import sys
import types
from contextlib import asynccontextmanager
from datetime import datetime, timezone

import pytest

from conftest import placeholders


def _stub_psycopg():
    try:
        import psycopg  # noqa: F401
        import psycopg_pool  # noqa: F401
        return
    except ImportError:
        pass

    def mod(name, **attrs):
        m = types.ModuleType(name)
        m.__dict__.update(attrs)
        sys.modules[name] = m
        return m

    class Error(Exception):
        pass

    class OperationalError(Error):
        pass

    errors = mod("psycopg.errors", DeadlockDetected=type("DeadlockDetected", (Error,), {}),
                 QueryCanceled=type("QueryCanceled", (OperationalError,), {}))
    pg = mod("psycopg", Error=Error, OperationalError=OperationalError, DataError=type("DataError", (Error,), {}),
             IntegrityError=type("IntegrityError", (Error,), {}), errors=errors, AsyncConnection=object,
             Connection=object)
    pg.rows = mod("psycopg.rows", dict_row=object())
    pg.sql = mod("psycopg.sql", SQL=str, Identifier=str)

    class AsyncConnectionPool:
        check_connection = staticmethod(lambda c: None)

        def __init__(self, *a, **k):
            pass

    mod("psycopg_pool", AsyncConnectionPool=AsyncConnectionPool, PoolTimeout=type("PoolTimeout", (Error,), {}))


_stub_psycopg()
os.environ.setdefault("HYPHA_DB_WRITE", "postgresql://essai@localhost/inexistant")
os.environ.setdefault("HYPHA_MODE", "saas")

from fastapi.testclient import TestClient  # noqa: E402

from hypha.app import create_app  # noqa: E402
from hypha.config import get_settings  # noqa: E402
from hypha.runtime import Runtime, set_runtime  # noqa: E402
from hypha.util import generate_api_key, key_hash  # noqa: E402

NOW = datetime.now(timezone.utc)
INGEST_KEY = generate_api_key()
APP_KEY = generate_api_key()


class FakeCursor:
    def __init__(self, rows, log):
        self.rows = rows
        self.rowcount = len(rows)
        self.log = log

    async def fetchall(self):
        return self.rows

    async def fetchone(self):
        return self.rows[0] if self.rows else None

    async def execute(self, sql, params=None):
        return self

    async def __aenter__(self):
        return self

    async def __aexit__(self, *exc):
        return False

    @asynccontextmanager
    async def copy(self, sql):
        rows = []

        class Copy:
            async def write_row(self, row):
                rows.append(row)
        yield Copy()
        cols = re.search(r"\((.*)\) FROM STDIN", sql).group(1).split(", ")
        assert all(len(r) == len(cols) for r in rows), sql
        self.log.append(("COPY", sql, rows))


def respond(sql: str, params) -> list[dict]:
    s = " ".join(sql.split())
    if "FROM api_keys WHERE key_hash" in s:
        return [{"id": 1, "org_id": 1, "kind": "ingest", "revoked": False}]
    if "FROM api_keys k" in s:
        kind = "app" if params[0] == key_hash(APP_KEY) else "ingest"
        return [{"id": 2, "org_id": 1, "kind": kind, "revoked": False, "created_by": 1, "role": "owner",
                 "email": "alice@example.org", "name": "Alice"}]
    if s.startswith("INSERT INTO hosts"):
        return [{"id": 10}]
    if s.startswith("INSERT INTO metric_series"):
        return [{"id": 100 + i, "name": n, "tags_hash": h} for i, (n, h) in enumerate(zip(params[2], params[4]))]
    if "SELECT id, tags, unit FROM metric_series" in s:
        return [{"id": 1, "tags": {"host": "web-01"}, "unit": "percent"},
                {"id": 2, "tags": {"host": "web-02"}, "unit": "percent"}]
    if s.startswith("WITH g AS"):
        # params[5] : début aligné de la plage, donc le premier seau attendu.
        return [{"gi": 0, "bucket": params[5], "v": 1.0}, {"gi": 1, "bucket": params[5], "v": 1.0}]
    if "FROM rum_apps WHERE client_token" in s:
        return [{"id": 5, "org_id": 1}]
    if "FROM orgs WHERE id" in s and "SELECT id, slug, name" in s:
        return [{"id": 1, "slug": "s33d", "name": "S33D"}]
    if "settings ? 'default_dashboards'" in s:
        return [{"done": True}]
    if s.startswith("INSERT INTO monitors") or s.startswith("UPDATE monitors") or "FROM monitors WHERE org_id = %s AND id" in s:
        return [{"id": 3, "name": "CPU", "type": "metric", "query": {"q": "avg:system.cpu.usage{*} by {host}",
                 "window_s": 300, "aggregation": "avg", "comparator": ">"}, "thresholds": {"critical": 90.0},
                 "message": "", "channel_ids": [], "notify_no_data": False, "no_data_s": 600, "renotify_s": 0,
                 "tags": {}, "enabled": True, "muted_until": None, "created_by": 1, "created_at": NOW,
                 "updated_at": NOW}]
    if "INSERT INTO events" in s:
        return [{"id": 9, "ts": NOW}]
    if "INSERT INTO api_keys" in s:
        return [{"id": 4, "name": "x", "kind": "ingest", "prefix": "hph_", "created_at": NOW, "last_used_at": None,
                 "revoked_at": None, "created_by": 1}]
    if s.startswith("INSERT INTO dashboards"):
        return [{"id": 6, "title": "T", "description": "", "widgets": [], "template_vars": [], "created_by": 1,
                 "created_at": NOW, "updated_at": NOW}]
    if "count(*) AS n" in s and "FROM dashboards" in s:
        return [{"n": 1}]
    if "FROM hosts h WHERE h.org_id = %s AND h.id = %s" in s:
        return [{"id": 10, "hostname": "web-01", "status": "up", "last_seen": NOW, "meta": {}}]
    if "FROM spans WHERE org_id = %s AND trace_id = %s" in s:
        if params[1] == "f" * 32:
            return []
        return [{"span_id": "eee19b7ec3c1b174", "parent_span_id": None, "service": "api", "name": "GET /",
                 "resource": "/", "kind": "server", "ts": NOW, "duration_ms": 1.5, "status": "error",
                 "status_message": "boom", "host_id": 10, "hostname": "web-01"}]
    # Explication des pannes (contrat § 10).
    if ("FROM hosts WHERE org_id = %s AND hostname = %s" in s or "FROM hosts WHERE org_id = %s AND id = %s" in s) \
            and "extract(epoch" not in s:
        return [{"id": 10, "hostname": "web-01", "os": "linux", "platform": "ubuntu", "platform_version": "26.04",
                 "kernel": "7.0.0", "arch": "amd64", "cpu_cores": 8, "mem_total": 17179869184, "tags": {"env": "prod"},
                 "meta": {}, "last_seen": NOW, "boot_time": None}]
    if "FROM check_runs WHERE org_id = %s AND host_id = %s AND ts > %s AND ts <= %s" in s:
        return [{"name": "systemd.unit", "status": 2, "message": "failed", "tags": {"unit": "nginx.service"}, "ts": NOW},
                {"name": "hypha.agent.up", "status": 0, "message": None, "tags": {}, "ts": NOW}]
    if "ORDER BY array_position(%s::text[], status)" in s or "trace_id = %s ORDER BY ts LIMIT 500" in s:
        return [{"ts": NOW, "status": "error", "service": "nginx", "message": f"upstream timed out pid {i}"}
                for i in range(3)]
    if "avg(p.value) AS avg" in s:
        return [{"name": "system.cpu.usage", "tags": {"host": "web-01"}, "unit": "percent", "avg": 40.0, "max": 99.0}]
    if s.startswith("SELECT max(ts) AS ts FROM process_snapshots"):
        return [{"ts": NOW}]
    if 'username AS "user", cmdline, cpu_pct, mem_rss' in s:
        return [{"pid": 1, "name": "nginx", "user": "www-data", "cmdline": "nginx: worker", "cpu_pct": 50.0,
                 "mem_rss": 1048576}]
    if s.startswith("SELECT ts, source, event_type, title, alert_type FROM events"):
        return [{"ts": NOW, "source": "agent", "event_type": "service_state", "title": "nginx : active -> failed",
                 "alert_type": "error"}]
    if "FROM logs WHERE org_id = %s AND id = %s AND ts >= %s AND ts <= %s" in s:
        return [{"id": 5, "ts": NOW, "host_id": 10, "hostname": "web-01", "service": "nginx", "source": "file",
                 "status": "error", "message": "connect() failed (111: Connection refused)",
                 "attributes": {"http": {"status_code": 502}}, "trace_id": None}]
    # Moniteurs (worker) : une mesure par type.
    if "SELECT hostname, extract(epoch FROM now() - last_seen)" in s:
        return [{"hostname": "web-01", "age": 900.0}, {"hostname": "web-01", "age": 5.0}, {"hostname": "web-02", "age": 700.0}]
    if "FROM check_runs c LEFT JOIN hosts h" in s and "c.check_name = %s" in s:
        return [{"hostname": "web-01", "tags": {"unit": "nginx.service"}, "status": 2},
                {"hostname": "web-01", "tags": {"unit": "cron.service"}, "status": 0}]
    if s.startswith("SELECT count(*) AS n FROM logs"):
        return [{"n": 12}]
    if "SELECT ok, ts FROM synthetic_results" in s:
        return [{"ok": False, "ts": NOW}, {"ok": False, "ts": NOW}, {"ok": True, "ts": NOW}]
    if "/ 1e6 AS p95 FROM spans" in s:
        return [{"hits": 10, "errors": 2, "p95": 120.0}]
    # Filtres d'exclusion des journaux (contrat § 11).
    if "FROM log_filters\n" in sql and "WHERE org_id = %s AND enabled" in s:
        return [{"id": 1, "name": "santé", "query": '"GET /api/sante"', "action": "exclude", "sample_rate": 0.0}]
    if s.startswith("SELECT id FROM log_filters WHERE org_id = %s ORDER BY position, id"):
        return [{"id": 1}, {"id": 2}]
    if s.startswith("INSERT INTO log_filters") or s.startswith("UPDATE log_filters SET name") or \
            ("FROM log_filters WHERE org_id = %s" in s and "excluded_total" in s and not s.startswith("UPDATE")):
        return [{"id": 1, "name": "santé", "query": '"GET /api/sante"', "action": "sample", "sample_rate": 0.2000000029,
                 "enabled": True, "position": 0, "excluded_total": 42, "created_at": NOW, "updated_at": NOW}]
    if s.startswith("SELECT count(*) AS n, count(*) FILTER (WHERE status = ANY"):
        return [{"n": 10, "severe": 2}]
    # SLO (contrat § 13).
    if "FROM slos WHERE org_id = %s" in s or s.startswith("INSERT INTO slos") or s.startswith("UPDATE slos"):
        return [dict(SLO_ROW)]
    if s.startswith("SELECT type FROM monitors WHERE org_id = %s AND id = %s"):
        return [{"type": "metric"}]
    if s.startswith("SELECT id, created_at, no_data_s FROM monitors"):
        return [{"id": 3, "created_at": NOW, "no_data_s": 600}]
    if s.startswith("SELECT coalesce(sum(value), 0)::float8 AS v FROM metric_points"):
        return [{"v": 5.0}]
    return []


SLO_ROW = {"org_id": 1, "id": 4, "name": "API", "description": "", "type": "metric", "monitor_id": None,
           "good_query": "sum:trace.hits{service:api,!status:error}", "total_query": "sum:trace.hits{service:api}",
           "target": 99.9000015, "warning": None, "timeframe": "30d", "tags": {}, "created_by": 1,
           "created_at": NOW, "updated_at": NOW}


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

    @asynccontextmanager
    async def transaction(self):
        yield self


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
def client():
    get_settings.cache_clear()
    db = FakeDatabase()
    set_runtime(Runtime(settings=get_settings(), db=db))
    c = TestClient(create_app(), raise_server_exceptions=True)
    c.db = db
    yield c
    set_runtime(None)


AGENT = {"id": "6f1c2b8e-0000-4000-8000-000000000001", "version": "0.1.0", "hostname": "web-01", "os": "linux",
         "arch": "amd64"}
UI = {"X-Hypha-App-Key": APP_KEY}


def post_gz(client, path, body, key=INGEST_KEY):
    return client.post(path, content=gzip.compress(json.dumps(body).encode()),
                       headers={"Content-Encoding": "gzip", "X-Hypha-Api-Key": key, "Content-Type": "application/json"})


def test_health(client):
    r = client.get("/api/sante")
    assert r.status_code == 200 and r.json()["ok"] is True and r.json()["db"] == "ok"


def test_ingest_auth_errors(client):
    assert client.post("/api/v1/intake/metrics", json={}).status_code == 401
    r = client.post("/api/v1/intake/metrics", json={}, headers={"X-Hypha-Api-Key": "hph_court"})
    assert r.status_code == 401 and r.json()["error"] == "invalid_api_key"


def test_ingest_metrics(client):
    ts = int(NOW.timestamp() * 1000)
    r = post_gz(client, "/api/v1/intake/metrics", {"agent": AGENT, "series": [
        {"metric": "system.cpu.user", "type": "gauge", "unit": "percent", "tags": {"host": "web-01"},
         "points": [[ts, 12.5], [ts - 30 * 86400_000, 1]]}]})
    assert r.status_code == 202, r.text
    assert r.json() == {"status": "ok", "accepted": 1, "dropped": 1}
    copies = [e for e in client.db.log if e[0] == "COPY"]
    assert copies and copies[0][2][0][0] == 100


def test_ingest_all_agent_routes(client):
    ts = int(NOW.timestamp() * 1000)
    cases = {
        "/api/v1/intake/host": {"host": {"hostname": "web-01", "cpu_cores": 8, "status": {"errors": []}}},
        "/api/v1/intake/logs": {"logs": [{"ts": ts, "message": "x", "status": "error"}]},
        "/api/v1/intake/checks": {"checks": [{"ts": ts, "name": "hypha.agent.up", "status": 0}]},
        "/api/v1/intake/events": {"events": [{"ts": ts, "title": "démarrage"}]},
        "/api/v1/intake/processes": {"ts": ts, "processes": [{"pid": 1, "name": "systemd"}]},
        "/api/v1/intake/connections": {"ts": ts, "connections": [{"proto": "tcp", "count": 2}]},
        "/api/v1/intake/containers": {"ts": ts, "containers": [{"id": "abc"}]},
    }
    for path, extra in cases.items():
        r = post_gz(client, path, {"agent": AGENT, **extra})
        assert r.status_code == 202, (path, r.text)
        assert r.json()["accepted"] == 1, path
    r = post_gz(client, "/api/v1/intake/logs", {"agent": AGENT})
    assert r.status_code == 400


def test_ingest_too_big(client):
    r = client.post("/api/v1/intake/logs", content=gzip.compress(b" " * (11 * 1024 * 1024)),
                    headers={"Content-Encoding": "gzip", "X-Hypha-Api-Key": INGEST_KEY})
    assert r.status_code == 413


def test_otlp_json_and_rum(client):
    ts_ns = int(NOW.timestamp() * 1e9)
    body = {"resourceSpans": [{"resource": {"attributes": [{"key": "service.name", "value": {"stringValue": "api"}}]},
            "scopeSpans": [{"spans": [{"traceId": "5b8efff798038103d269b633813fc60c", "spanId": "eee19b7ec3c1b174",
                                       "name": "GET /", "kind": 2, "startTimeUnixNano": str(ts_ns),
                                       "endTimeUnixNano": str(ts_ns + 1000)}]}]}]}
    r = client.post("/v1/traces", json=body, headers={"Authorization": f"Bearer {INGEST_KEY}"})
    assert r.status_code == 200 and r.json() == {}
    r = client.post("/v1/traces", content=b"x", headers={"Authorization": f"Bearer {INGEST_KEY}",
                                                         "Content-Type": "text/plain"})
    assert r.status_code == 415
    r = client.options("/api/v1/intake/rum")
    assert r.status_code == 204 and r.headers["access-control-allow-origin"] == "*"
    r = client.post("/api/v1/intake/rum?token=pubabc", content=json.dumps(
        {"session_id": "s", "events": [{"type": "view", "url": "/", "duration_ms": 800}]}),
        headers={"Content-Type": "text/plain"})
    assert r.status_code == 202 and r.headers["access-control-allow-origin"] == "*"
    r = client.post("/api/v1/intake/rum", content=b"{}")
    assert r.status_code == 401 and r.headers["access-control-allow-origin"] == "*"


def test_ui_requires_auth(client):
    r = client.get("/api/v1/hosts")
    assert r.status_code == 401 and r.json()["error"] == "unauthenticated" and r.json()["mode"] == "saas"
    r = client.get("/api/v1/hosts", headers={"X-Hypha-App-Key": INGEST_KEY})
    assert r.status_code == 403


def test_ui_get_routes(client):
    paths = ["/api/v1/me", "/api/v1/hosts?q=web&tag=env:prod&tag=host:web-01", "/api/v1/hosts/10",
             "/api/v1/processes", "/api/v1/processes?host_id=10&q=nginx", "/api/v1/connections?host_id=10",
             "/api/v1/containers", "/api/v1/checks?host_id=10&name=systemd.unit", "/api/v1/agents",
             "/api/v1/metrics/names?q=cpu", "/api/v1/metrics/tags?metric=system.cpu.user",
             "/api/v1/logs/search?q=status:error%20OR%20status:critical%20-service:cron&limit=10",
             "/api/v1/logs/facets?q=timeout", "/api/v1/logs/histogram?q=@http.status_code:502&buckets=30",
             "/api/v1/logs/tail?q=nginx", "/api/v1/logs/tail?after_id=5",
             "/api/v1/apm/services", "/api/v1/apm/services/api", "/api/v1/apm/traces?service=api&status=error&min_ms=5",
             "/api/v1/apm/traces/5b8efff798038103d269b633813fc60c", "/api/v1/events?q=source:deploy",
             "/api/v1/monitors", "/api/v1/monitors/3", "/api/v1/monitors/3/history", "/api/v1/alerts",
             "/api/v1/dashboards", "/api/v1/synthetics", "/api/v1/rum/apps", "/api/v1/keys", "/api/v1/channels",
             "/api/v1/org/members", "/api/v1/install"]
    for path in paths:
        r = client.get(path, headers=UI)
        assert r.status_code in (200, 404), (path, r.status_code, r.text)
    r = client.get("/api/v1/logs/search?q=%22ouvert", headers=UI)
    assert r.status_code == 400 and r.json()["error"] == "invalid_query"
    inst = client.get("/api/v1/install", headers=UI).json()
    assert inst["linux"].startswith("curl -fsSL https://hypha.s33d.fr/install/install.sh | sudo HYPHA_API_KEY=")


def test_install_follows_published_agent(client, monkeypatch):
    from hypha.api import fleet
    monkeypatch.setattr(fleet, "published_version", lambda install_dir: "9.8.7")
    inst = client.get("/api/v1/install", headers=UI).json()
    inst = inst.get("data", inst)
    assert inst["version"] == "9.8.7" and inst["docker"].endswith(":9.8.7")


def test_metrics_query(client):
    r = client.post("/api/v1/metrics/query", headers=UI, json={
        "from": int(NOW.timestamp() * 1000) - 3600_000, "to": int(NOW.timestamp() * 1000),
        "queries": [{"id": "a", "q": "avg:system.cpu.user{host:web-01,env:prod} by {host}"}], "max_points": 300})
    assert r.status_code == 200, r.text
    body = r.json()
    assert body["interval_s"] == 15 and isinstance(body["series"], list)
    r = client.post("/api/v1/metrics/query", headers=UI, json={"queries": [{"id": "a", "q": "median:x"}]})
    assert r.status_code == 400 and "« a »" in r.json()["detail"]


def test_monitor_crud(client):
    data = {"name": "CPU", "type": "metric", "query": {"q": "avg:system.cpu.usage{*} by {host}"},
            "thresholds": {"critical": 90}}
    r = client.post("/api/v1/monitors", headers=UI, json=data)
    assert r.status_code == 201, r.text
    assert r.json()["state"] == "NODATA"
    r = client.put("/api/v1/monitors/3", headers=UI, json={"thresholds": {"critical": 95, "warning": 80}})
    assert r.status_code == 200, r.text
    r = client.post("/api/v1/monitors/3/mute", headers=UI, json={"until": None})
    assert r.status_code == 200
    r = client.post("/api/v1/monitors", headers=UI, json={**data, "thresholds": {}})
    assert r.status_code == 400 and r.json()["error"] == "invalid_monitor"


def test_other_writes(client):
    assert client.post("/api/v1/events", headers=UI, json={"title": "Déploiement", "tags": {"env": "prod"}}).status_code == 201
    r = client.post("/api/v1/keys", headers=UI, json={"name": "parc"})
    assert r.status_code == 201 and r.json()["key"].startswith("hph_")
    r = client.post("/api/v1/dashboards", headers=UI, json={"title": "T", "widgets": [
        {"id": "w1", "type": "timeseries", "x": 0, "y": 0, "w": 12, "h": 4, "queries": [{"q": "avg:x{*}"}]}]})
    assert r.status_code == 201, r.text
    r = client.post("/api/v1/dashboards", headers=UI, json={"title": "T", "widgets": [
        {"id": "w1", "type": "timeseries", "x": 6, "w": 12}]})
    assert r.status_code == 400
    r = client.post("/api/v1/synthetics", headers=UI, json={"name": "s", "kind": "http", "target": "http://172.16.1.1/"})
    assert r.status_code == 400 and r.json()["error"] == "target_not_allowed"
    r = client.post("/api/v1/channels", headers=UI, json={"kind": "slack", "name": "ops", "config": {"url": "http://x"}})
    assert r.status_code == 400
    r = client.post("/api/v1/auth/local", json={"login": "a", "password": "b"})
    assert r.status_code == 404
    assert client.post("/api/auth/local", json={"login": "a", "password": "b"}).status_code == 403


def test_monitor_runner_all_types():
    import asyncio

    from hypha.monitors import runner

    settings = get_settings()
    db = FakeDatabase()
    conn = FakeConn(db.log)
    base = {"org_id": 1, "message": "", "no_data_s": 600, "notify_no_data": False, "renotify_s": 0, "tags": {},
            "muted_until": None, "channel_ids": [1]}
    monitors = [
        {"id": 1, "name": "cpu", "type": "metric", "thresholds": {"critical": 0.5},
         "query": {"q": "avg:system.cpu.usage{*} by {host}", "window_s": 300, "aggregation": "max", "comparator": ">"}},
        {"id": 2, "name": "hôtes", "type": "host", "thresholds": {}, "query": {"tags": {"env": "prod", "host": "web-01"}}},
        {"id": 3, "name": "units", "type": "service_check", "thresholds": {},
         "query": {"check": "systemd.unit", "tags": {}, "by": ["host", "unit"], "window_s": 300}},
        {"id": 4, "name": "erreurs", "type": "log", "thresholds": {"critical": 10},
         "query": {"q": "status:error service:nginx", "window_s": 300, "comparator": ">", "by": []}},
        {"id": 5, "name": "site", "type": "synthetic", "thresholds": {}, "query": {"test_id": 12, "failures": 2}},
        {"id": 6, "name": "api", "type": "apm", "thresholds": {"critical": 0.1},
         "query": {"service": "api", "metric": "error_rate", "window_s": 300, "comparator": ">"}},
    ]
    channel = {"id": 1, "kind": "webhook", "name": "x", "config": {"url": "https://example.org/hook"}}
    results = {}

    async def go():
        for m in monitors:
            m = {**base, **m}
            measured = await runner.measure(conn, m, {})
            notices = await runner.apply(conn, m, {}, measured, settings, [channel], False)
            results[m["type"]] = (measured, notices)

    asyncio.run(go())
    assert results["metric"][0] == {"host:web-01": ("ALERT", 1.0), "host:web-02": ("ALERT", 1.0)}
    assert results["host"][0] == {"host:web-01": ("OK", 5.0), "host:web-02": ("ALERT", 700.0)}
    assert results["service_check"][0]["host:web-01,unit:nginx.service"] == ("ALERT", 2.0)
    assert results["log"][0] == {"*": ("ALERT", 12.0)}
    assert results["synthetic"][0] == {"*": ("ALERT", 2.0)}
    assert results["apm"][0] == {"*": ("ALERT", 0.2)}
    assert len(results["apm"][1]) == 1 and results["apm"][1][0].webhook_payload()["state"] == "ALERT"
    events = [e for e in db.log if e[0] == "SQL" and e[1].strip().startswith("INSERT INTO events")]
    assert events, "un changement d'état crée un événement"


@pytest.fixture()
def explain_client(monkeypatch):
    from hypha.api import explain as route
    from hypha.explain.llm import HourlyQuota
    monkeypatch.setenv("HYPHA_LLM_URL", "http://llm.invalid:11434")
    monkeypatch.setenv("HYPHA_LLM_MODEL", "qwen3.5:35b")
    calls = []

    async def fake_chat(url, model, messages, timeout_s):
        calls.append({"url": url, "model": model, "messages": messages, "timeout_s": timeout_s})
        return "## Ce qui se passe\nnginx est tombé.", 1234

    monkeypatch.setattr(route, "chat", fake_chat)
    monkeypatch.setattr(route, "_quota", HourlyQuota(60))
    get_settings.cache_clear()
    set_runtime(Runtime(settings=get_settings(), db=FakeDatabase()))
    c = TestClient(create_app())
    c.calls = calls
    yield c
    set_runtime(None)
    get_settings.cache_clear()


def test_explain_disabled_without_llm_url(client):
    r = client.get("/api/v1/me", headers=UI)
    assert r.json()["features"]["explain"] is False
    r = client.post("/api/v1/explain", headers=UI, json={"kind": "host", "host_id": 10})
    assert r.status_code == 503 and r.json()["error"] == "llm_unavailable"


def test_explain_four_kinds(explain_client):
    c = explain_client
    assert c.get("/api/v1/me", headers=UI).json()["features"]["explain"] is True
    bodies = [
        {"kind": "alert", "monitor_id": 3, "group": "host:web-01,unit:nginx.service", "lang": "fr"},
        {"kind": "host", "host_id": 10, "lang": "en"},
        {"kind": "log", "log_id": 5, "ts": int(NOW.timestamp() * 1000), "lang": "fr"},
        {"kind": "trace", "trace_id": "5b8efff798038103d269b633813fc60c"},
    ]
    for body in bodies:
        r = c.post("/api/v1/explain", headers=UI, json=body)
        assert r.status_code == 200, (body, r.text)
        out = r.json()
        assert out["explanation"].startswith("## Ce qui se passe") and out["model"] == "qwen3.5:35b"
        assert out["duration_ms"] == 1234
        ctx = out["context"]
        assert ctx["host"] == "web-01" and ctx["checks"] == 1 and ctx["events"] == 1 and ctx["processes"] == 1
        assert ctx["logs"] == 3 and ctx["metrics"] == ["system.cpu.usage"]
    first = c.calls[0]["messages"]
    assert "## Cause probable" in first[0]["content"]
    user = first[1]["content"]
    assert user.startswith("Explique cette alerte.") and "### Moniteur" in user and "### Hôte" in user
    assert "3x" in user and "systemd.unit CRIT (unit=nginx.service)" in user and "hypha.agent.up" not in user
    assert len(user) < 13000
    assert c.calls[1]["messages"][1]["content"].startswith("Explain this host")
    assert "### Spans de la trace" in c.calls[3]["messages"][1]["content"]


def test_explain_errors(explain_client):
    from hypha.api import explain as route
    from hypha.explain.llm import HourlyQuota
    c = explain_client
    r = c.post("/api/v1/explain", headers=UI, json={"kind": "trace", "trace_id": "f" * 32})
    assert r.status_code == 404
    r = c.post("/api/v1/explain", headers=UI, json={"kind": "log", "log_id": 5})
    assert r.status_code == 400
    assert c.post("/api/v1/explain", json={"kind": "host", "host_id": 10}).status_code == 401
    route._quota = HourlyQuota(1)
    assert c.post("/api/v1/explain", headers=UI, json={"kind": "host", "host_id": 10}).status_code == 200
    r = c.post("/api/v1/explain", headers=UI, json={"kind": "host", "host_id": 10})
    assert r.status_code == 429 and r.json()["error"] == "rate_limited"


def test_log_filters_at_ingestion(client):
    ts = int(NOW.timestamp() * 1000)
    r = post_gz(client, "/api/v1/intake/logs", {"agent": AGENT, "logs": [
        {"ts": ts, "message": "GET /api/sante HTTP/1.1 200", "status": "info", "service": "uvicorn"},
        {"ts": ts, "message": "GET /api/sante HTTP/1.1 500", "status": "error", "service": "uvicorn"},
        {"ts": ts, "message": "GET /api/v1/hosts 200", "status": "info", "service": "uvicorn"},
    ]})
    assert r.status_code == 202, r.text
    # La ligne d'erreur n'est pas jetée : le filtre ne vise pas status:.
    assert r.json() == {"status": "ok", "accepted": 2, "dropped": 1}
    copies = [e for e in client.db.log if e[0] == "COPY" and "COPY logs" in e[1]]
    assert [row[7] for row in copies[0][2]] == ["GET /api/sante HTTP/1.1 500", "GET /api/v1/hosts 200"]
    sqls = [e[1] for e in client.db.log if e[0] == "SQL"]
    assert any("UPDATE log_filters f SET excluded_total" in q for q in sqls)
    assert any(e[0] == "COPY" and "metric_points" in e[1] for e in client.db.log)


def test_log_filters_routes(client):
    r = client.get("/api/v1/logs/filters", headers=UI)
    assert r.status_code == 200 and r.json()[0]["sample_rate"] == 0.2 and r.json()[0]["excluded_total"] == 42
    body = {"name": "sondes", "query": '"GET /api/sante"', "action": "sample", "sample_rate": 0.2}
    assert client.post("/api/v1/logs/filters", headers=UI, json=body).status_code == 201
    for bad in ({**body, "query": "  "}, {**body, "query": '"ouvert'}, {**body, "action": "drop"},
                {**body, "sample_rate": 2}):
        assert client.post("/api/v1/logs/filters", headers=UI, json=bad).status_code == 400, bad
    assert client.put("/api/v1/logs/filters/1", headers=UI, json={"enabled": False}).status_code == 200
    assert client.post("/api/v1/logs/filters/reorder", headers=UI, json={"ids": [2]}).status_code == 200
    assert client.post("/api/v1/logs/filters/reorder", headers=UI, json={"ids": [9]}).status_code == 400
    assert client.post("/api/v1/logs/filters/reorder", headers=UI, json={"ids": [1, 1]}).status_code == 400
    r = client.get("/api/v1/logs/filters/preview", headers=UI, params={"q": "service:uvicorn sante"})
    assert r.status_code == 200, r.text
    assert r.json()["per_hour"] == 10 and r.json()["share"] == 10 / 12 and r.json()["errors"] == 2
    assert client.get("/api/v1/logs/filters/preview", headers=UI).status_code == 400
    r = client.get("/api/v1/logs/volume", headers=UI)
    assert r.status_code == 200, r.text
    assert set(r.json()) == {"total", "by_service", "by_host", "excluded"}


def test_facets_with_attributes(client):
    r = client.get("/api/v1/logs/facets", headers=UI, params={"q": "service:nginx", "attrs": "http.status_code,@http.method"})
    assert r.status_code == 200, r.text
    assert set(r.json()) == {"status", "service", "host", "source", "@http.status_code", "@http.method"}
    sql = [e for e in client.db.log if e[0] == "SQL" and "GROUPING SETS" in e[1]][-1]
    assert "coalesce(attributes #>> %s::text[], attributes ->> %s) AS a1" in sql[1]
    assert sql[2][:4] == [["http", "status_code"], "http.status_code", ["http", "method"], "http.method"]
    r = client.get("/api/v1/logs/facets", headers=UI, params={"attrs": "bad path!"})
    assert r.status_code == 400


def test_slo_routes(client):
    body = {"name": "API disponible", "type": "metric", "target": 99.9, "timeframe": "30d",
            "good_query": "sum:trace.hits{service:api,!status:error}", "total_query": "sum:trace.hits{service:api}"}
    r = client.post("/api/v1/slos", headers=UI, json=body)
    assert r.status_code == 201, r.text
    assert r.json()["target"] == 99.9 and "org_id" not in r.json()
    assert client.post("/api/v1/slos", headers=UI, json={"name": "m", "type": "monitor", "monitor_id": 3,
                                                          "target": 99.5}).status_code == 201
    for bad in ({**body, "target": 100}, {**body, "type": "x"}, {**body, "good_query": "median:x"},
                {**body, "warning": 99.0}, {**body, "timeframe": "1y"}, {"name": "m", "type": "monitor", "target": 99}):
        assert client.post("/api/v1/slos", headers=UI, json=bad).status_code == 400, bad
    r = client.get("/api/v1/slos", headers=UI)
    assert r.status_code == 200 and r.json()[0]["sli"] == 100.0 and r.json()[0]["state"] == "ok"
    assert "history" not in r.json()[0]
    r = client.get("/api/v1/slos/4/status", headers=UI)
    assert r.status_code == 200, r.text
    assert set(r.json()) == {"sli", "target", "state", "error_budget_remaining", "burn_rate_1h", "burn_rate_6h",
                             "history"}
    assert r.json()["burn_rate_1h"] == 0.0
    assert client.put("/api/v1/slos/4", headers=UI, json={"target": 99.5}).status_code == 200
    r = client.post("/api/v1/monitors", headers=UI, json={"name": "budget", "type": "slo",
                                                         "query": {"slo_id": 4, "on": "budget"},
                                                         "thresholds": {"critical": 10}})
    assert r.status_code == 201, r.text


def test_slo_monitor_in_worker():
    import asyncio

    from hypha.monitors import runner

    conn = FakeConn([])
    m = {"id": 9, "org_id": 1, "name": "budget", "type": "slo", "thresholds": {"critical": 10.0},
         "query": {"slo_id": 4, "on": "budget", "window": "1h", "comparator": "<"}, "no_data_s": 600}
    measured = asyncio.run(runner.measure(conn, m, {}))
    assert measured == {"*": ("OK", 100.0)}
    m["query"] = {"slo_id": 4, "on": "burn_rate", "window": "6h", "comparator": ">"}
    m["thresholds"] = {"critical": -1.0}
    assert asyncio.run(runner.measure(conn, m, {}))["*"][0] == "ALERT"
