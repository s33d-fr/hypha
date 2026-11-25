"""Routes 0.4.0 (collecte, données, plateforme) exercées sans base : une fausse connexion répond selon
des règles (fragments de SQL → lignes) et vérifie que chaque requête reçoit autant de paramètres
qu'elle a de `%s`. Aucun réseau, aucune base."""
import gzip
import json
import os
import re
import time
from contextlib import asynccontextmanager
from datetime import datetime, timedelta, timezone

import pytest

import test_routes_nodb  # noqa: F401  (remplace psycopg s'il manque, pose l'environnement)
from conftest import placeholders

os.environ.setdefault("HYPHA_DB_WRITE", "postgresql://essai@localhost/inexistant")
os.environ.setdefault("HYPHA_MODE", "saas")

from fastapi.testclient import TestClient  # noqa: E402

from hypha.app import create_app  # noqa: E402
from hypha.config import get_settings  # noqa: E402
from hypha.ingest import extras  # noqa: E402
from hypha.runtime import Runtime, set_runtime  # noqa: E402
from hypha.util import generate_api_key, key_hash  # noqa: E402

NOW = datetime.now(timezone.utc).replace(microsecond=0)
NOW_MS = int(NOW.timestamp() * 1000)
INGEST_KEY = generate_api_key()
APP_KEY = generate_api_key()
UI = {"X-Hypha-App-Key": APP_KEY}
AGENT = {"id": "6f1c2b8e-0000-4000-8000-000000000001", "version": "0.4.0", "hostname": "web-01", "os": "linux",
         "arch": "amd64"}
AGENT_ID = AGENT["id"]


class Cur:
    def __init__(self, rows, log):
        self.rows = rows
        self.rowcount = len(rows)
        self.log = log

    async def fetchall(self):
        return self.rows

    async def fetchone(self):
        return self.rows[0] if self.rows else None

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


def _base_rules():
    def app_key(s, params):
        kind = "app" if params[0] == key_hash(APP_KEY) else "ingest"
        return [{"id": 2, "org_id": 1, "kind": kind, "revoked": False, "created_by": 1, "role": "owner",
                 "email": "alice@example.org", "name": "Alice"}]

    def series(s, params):
        return [{"id": 100 + i, "name": n, "tags_hash": h} for i, (n, h) in enumerate(zip(params[2], params[4]))]

    def nextval(s, params):
        return [{"id": 5000 + i} for i in range(params[0])]

    return [
        (("FROM api_keys WHERE key_hash",), [{"id": 1, "org_id": 1, "kind": "ingest", "revoked": False}], None),
        (("FROM api_keys k",), None, app_key),
        (("INSERT INTO hosts (org_id, agent_id, hostname, kind, via_host_id)",), [{"id": 20}], None),
        (("INSERT INTO hosts",), [{"id": 10}], None),
        (("INSERT INTO metric_series",), None, series),
        (("nextval('hypha.logs_id_seq')",), None, nextval),
        (("settings ? 'default_dashboards'",), [{"done": True}], None),
        (("INSERT INTO dashboards",), [{"id": 6, "title": "T"}], None),
        (("settings ? 'sensitive_rules_seeded'",), [{"done": True}], None),
    ]


class DB:
    def __init__(self):
        self.log = []
        self.rules = []
        self.state = "ok"

    def on(self, *needles, rows=None, fn=None):
        self.rules.insert(0, (needles, rows, fn))

    def respond(self, sql, params):
        s = " ".join(sql.split())
        for needles, rows, fn in self.rules + _base_rules():
            if all(n in s for n in needles):
                return fn(s, params) if fn else [dict(r) for r in rows]
        return []

    def mark(self, ok):
        self.state = "ok" if ok else "erreur"

    @asynccontextmanager
    async def writer(self, timeout=5.0):
        yield Conn(self)

    reader = writer

    def sql(self, needle):
        return [e for e in self.log if e[0] == "SQL" and needle in " ".join(e[1].split())]

    def copies(self, table):
        return [e for e in self.log if e[0] == "COPY" and f"COPY {table} (" in e[1]]


class Conn:
    def __init__(self, db):
        self.db = db

    async def execute(self, sql, params=None):
        n = placeholders(sql)
        assert n == len(params or ()), f"{n} %s pour {len(params or ())} paramètres :\n{sql}\n{params}"
        self.db.log.append(("SQL", sql, params))
        return Cur(self.db.respond(sql, params), self.db.log)

    def cursor(self):
        return Cur([], self.db.log)

    @asynccontextmanager
    async def transaction(self):
        yield self


@pytest.fixture()
def c():
    get_settings.cache_clear()
    db = DB()
    set_runtime(Runtime(settings=get_settings(), db=db))
    client = TestClient(create_app(), raise_server_exceptions=True)
    client.db = db
    yield client
    set_runtime(None)


def post_gz(client, path, body, key=INGEST_KEY, headers=None):
    return client.post(path, content=gzip.compress(json.dumps(body).encode()),
                       headers={"Content-Encoding": "gzip", "X-Hypha-Api-Key": key,
                                "Content-Type": "application/json", **(headers or {})})


def audited(db, object_type):
    return [e for e in db.sql("INSERT INTO hypha.audit_log") if e[2][4] == object_type]


# ── § 14 équipements, § 27 capacités ───────────────────────────────────────────

def test_intake_host_network_device(c):
    device = {**AGENT, "id": "6f1c2b8e-0000-4000-8000-0000000000aa", "hostname": "switch-vlan1", "via": AGENT_ID}
    r = post_gz(c, "/api/v1/intake/host", {"agent": device, "host": {
        "hostname": "switch-vlan1", "kind": "network_device",
        "device": {"address": "192.0.2.252", "sys_name": "sw1", "vendor": "MikroTik", "interfaces": 24}}})
    assert r.status_code == 202, r.text
    db = c.db
    assert db.sql("INSERT INTO hosts (org_id, agent_id, hostname, kind, via_host_id)")
    assert not db.sql("INSERT INTO agents") and not db.sql("UPDATE agents")
    upd = db.sql("UPDATE hosts SET kind = %s, device = %s::jsonb")[0]
    assert upd[2][0] == "network_device" and json.loads(upd[2][1])["vendor"] == "MikroTik" and upd[2][3] == AGENT_ID
    full = db.sql("UPDATE hosts SET hostname = %s")[0]
    assert json.loads(full[2][9]) == ["192.0.2.252"]


def test_intake_host_cloud_resource(c):
    # § 34-35 : serveur OVH ou VM vSphere lu par un connecteur, rattaché à l'agent qui l'interroge.
    res = {**AGENT, "id": "6f1c2b8e-0000-4000-8000-0000000000bb", "hostname": "mail-01.example.org", "os": "cloud", "via": AGENT_ID}
    r = post_gz(c, "/api/v1/intake/host", {"agent": res, "host": {
        "hostname": "mail-01.example.org", "kind": "cloud_resource", "os": "cloud",
        "device": {"provider": "ovh", "type": "dedicated_server", "service": "ns1234567.ip-1-2-3.eu",
                   "address": "1.2.3.4"}}})
    assert r.status_code == 202, r.text
    db = c.db
    ins = db.sql("INSERT INTO hosts (org_id, agent_id, hostname, kind, via_host_id)")[0]
    assert ins[2][3] == "cloud_resource"
    upd = db.sql("UPDATE hosts SET kind = %s, device = %s::jsonb")[0]
    assert upd[2][0] == "cloud_resource" and json.loads(upd[2][1])["provider"] == "ovh"
    assert not db.sql("INSERT INTO agents")


def test_intake_host_capabilities_and_update_status(c):
    r = post_gz(c, "/api/v1/intake/host", {"agent": AGENT, "host": {
        "hostname": "web-01", "capabilities": {"actions": {"enabled": True, "kinds": ["restart_service"]},
                                              "synthetics": True},
        "status": {"collectors": {"logs": "ok"}, "update": {"state": "verifying", "to": "0.4.0"}}}})
    assert r.status_code == 202, r.text
    upd = c.db.sql("UPDATE agents SET status = %s::jsonb")[0]
    assert json.loads(upd[2][2])["synthetics"] is True and json.loads(upd[2][3])["state"] == "verifying"
    assert c.db.sql("INSERT INTO agents")


# ── § 15 inventaire ────────────────────────────────────────────────────────────

def test_intake_inventory_replaces_packages(c):
    r = post_gz(c, "/api/v1/intake/inventory", {"agent": AGENT, "ts": NOW_MS, "hardware": {"vendor": "Dell Inc."},
                                                "os": {"name": "Ubuntu", "reboot_required": True},
                                                "updates": {"pending": 3, "security": 1},
                                                "packages": [{"name": "openssl", "version": "3.5.1", "arch": "amd64"},
                                                             {"name": "bash", "version": "5.2"}]})
    assert r.status_code == 202 and r.json()["accepted"] == 2, r.text
    log = c.db.log
    kinds = [e[1].split()[0] if e[0] == "SQL" else "COPY" for e in log]
    i_ins = next(i for i, e in enumerate(log) if e[0] == "SQL" and "INSERT INTO host_inventory" in e[1])
    i_del = next(i for i, e in enumerate(log) if e[0] == "SQL" and "DELETE FROM host_packages" in e[1])
    i_copy = next(i for i, e in enumerate(log) if e[0] == "COPY" and "host_packages" in e[1])
    assert i_ins < i_del < i_copy, kinds
    assert [row[2] for row in log[i_copy][2]] == ["bash", "openssl"]
    # Sans liste de paquets : l'inventaire change, les paquets restent.
    c.db.log.clear()
    r = post_gz(c, "/api/v1/intake/inventory", {"agent": AGENT, "hardware": {}})
    assert r.status_code == 202 and not c.db.sql("DELETE FROM host_packages")


def test_inventory_routes(c):
    c.db.on("FROM host_inventory i JOIN hosts h", rows=[{"host_id": 10, "hostname": "web-01", "os": "linux",
                                                          "updates_pending": 3}])
    assert c.get("/api/v1/inventory?q=dell&os=ubuntu&tag=env:prod", headers=UI).json()[0]["host_id"] == 10
    c.db.on("FROM hosts h LEFT JOIN host_inventory i", rows=[{"host_id": 10, "hostname": "web-01", "hardware": None,
                                                               "os": None, "updates": None, "packages_count": None}])
    c.db.on("FROM host_packages WHERE org_id = %s AND host_id = %s",
            fn=lambda s, p: [{"name": f"p{i}", "version": "1", "arch": "", "source": "dpkg"} for i in range(p[-1])])
    body = c.get("/api/v1/hosts/10/inventory?limit=2&q=p", headers=UI).json()
    assert len(body["packages"]) == 2 and body["next_cursor"] and body["hardware"] == {}
    assert c.get(f"/api/v1/hosts/10/inventory?cursor={body['next_cursor']}", headers=UI).status_code == 200
    assert c.get("/api/v1/hosts/10/inventory?cursor=xx", headers=UI).status_code == 400
    c.db.on("FROM host_packages s JOIN hosts h", rows=[
        {"host_id": 10, "hostname": "web-01", "name": "openssl", "version": "3.5.1-1ubuntu1", "arch": "amd64"},
        {"host_id": 11, "hostname": "web-02", "name": "openssl", "version": "3.5.2-0ubuntu1", "arch": "amd64"}])
    r = c.get("/api/v1/inventory/packages?name=openssl&version_lt=3.5.2", headers=UI)
    assert [x["hostname"] for x in r.json()] == ["web-01"]
    assert c.get("/api/v1/inventory/packages", headers=UI).status_code == 400
    r = c.get("/api/v1/inventory/summary", headers=UI)
    assert r.status_code == 200 and set(r.json()) >= {"os", "vendors", "updates_pending_hosts", "reboot_required_hosts"}


# ── § 30 DBM ───────────────────────────────────────────────────────────────────

def test_dbm_intake_and_routes(c):
    r = post_gz(c, "/api/v1/intake/dbm", {"agent": AGENT, "queries": [
        {"instance": "pg:5432", "db": "hypha", "query_id": "42", "query": "SELECT $1", "calls": 4, "total_ms": 8.0}]})
    assert r.status_code == 202 and r.json()["accepted"] == 1
    assert c.db.copies("dbm_queries")[0][2][0][6] == "42"
    for path in ("/api/v1/dbm/queries?sort=calls&db=hypha&host_id=10", "/api/v1/dbm/instances"):
        assert c.get(path, headers=UI).status_code == 200, path
    assert c.get("/api/v1/dbm/queries?sort=x", headers=UI).status_code == 400
    assert c.get("/api/v1/dbm/queries/42", headers=UI).status_code == 404
    c.db.on("count(*) AS samples", rows=[{"query": "SELECT $1", "db": "hypha", "instance": "pg", "calls": 4,
                                          "total_ms": 8.0, "samples": 1}])
    c.db.on("date_bin(%s::interval, q.ts", fn=lambda s, p: [{"b": p[3], "calls": 4, "total_ms": 8.0, "rows": 4}])
    body = c.get("/api/v1/dbm/queries/42", headers=UI).json()
    assert body["query"]["query_id"] == "42" and body["query"]["mean_ms"] == 2.0
    assert set(body["series"]) == {"calls", "mean_ms", "total_ms", "rows"}
    assert all(len(p) == 2 for p in body["series"]["calls"])


# ── § 29 synthétiques privés, § 27 canal de contrôle ───────────────────────────

def test_intake_synthetics_only_assigned_tests(c):
    c.db.on("SELECT id, locations FROM synthetic_tests", rows=[{"id": 3, "locations": ["agent:10"]},
                                                              {"id": 4, "locations": ["server"]}])
    r = post_gz(c, "/api/v1/intake/synthetics", {"agent": AGENT, "results": [
        {"test_id": 3, "ts": NOW_MS, "ok": True, "latency_ms": 10, "location": "agent:usurpé"},
        {"test_id": 4, "ts": NOW_MS, "ok": False}, {"test_id": 9, "ts": NOW_MS, "ok": True}]})
    assert r.json() == {"status": "ok", "accepted": 1, "dropped": 2}
    row = c.db.copies("synthetic_results")[0][2][0]
    assert row[1] == 3 and row[3] == "agent:web-01"


def test_agent_control(c):
    hdr = {"X-Hypha-Api-Key": INGEST_KEY, "X-Hypha-Agent-Id": AGENT_ID, "X-Hypha-Agent-Version": "0.4.0"}
    r = c.get("/api/v1/agent/control", headers=hdr)
    assert r.status_code == 200 and r.json()["actions"] == [] and r.json()["target_version"] is None
    c.db.on("FROM agents a LEFT JOIN hosts h", rows=[{"host_id": 10, "target_version": "0.4.0",
                                                       "capabilities": {"synthetics": True}, "hostname": "web-01"}])
    c.db.on("UPDATE agent_actions SET status = 'sent'", rows=[{"id": 7, "kind": "restart_service",
                                                               "params": {"unit": "nginx.service"}, "expires_at": NOW}])
    c.db.on("FROM synthetic_tests WHERE org_id = %s AND enabled AND %s = ANY(locations)",
            rows=[{"id": 3, "kind": "http", "target": "http://172.16.1.10/", "config": {}, "steps": [],
                   "interval_s": 60}])
    body = c.get("/api/v1/agent/control", headers=hdr).json()
    assert body["target_version"] == "0.4.0" and body["actions"][0]["id"] == 7
    assert body["actions"][0]["expires_at"] == NOW_MS and body["synthetics"][0]["id"] == 3
    assert isinstance(body["server_time"], int)
    take = c.db.sql("UPDATE agent_actions SET status = 'sent'")[0]
    assert take[2] == (1, AGENT_ID)
    assert c.db.sql("FROM synthetic_tests WHERE org_id = %s AND enabled")[0][2] == (1, "agent:10")
    sent = audited(c.db, "action")
    assert sent and sent[0][2][2] == "agent:web-01" and sent[0][2][3] == "send"
    assert c.get("/api/v1/agent/control", headers={"X-Hypha-Api-Key": INGEST_KEY}).status_code == 400
    assert c.get("/api/v1/agent/control", headers={"X-Hypha-Agent-Id": AGENT_ID}).status_code == 401


def test_action_result(c):
    hdr = {"X-Hypha-Agent-Id": AGENT_ID}
    assert post_gz(c, "/api/v1/agent/actions/7/result", {"status": "succeeded"}, headers=hdr).status_code == 404
    c.db.on("SELECT status FROM agent_actions", rows=[{"status": "succeeded"}])
    # Renvoi du tampon de l'agent (même état final, ou « running » tardif) : accepté sans effet.
    dup = post_gz(c, "/api/v1/agent/actions/7/result", {"status": "succeeded"}, headers=hdr)
    assert dup.status_code == 200 and dup.json().get("duplicate") is True
    assert post_gz(c, "/api/v1/agent/actions/7/result", {"status": "running"}, headers=hdr).status_code == 200
    # Un autre état final après coup reste refusé.
    assert post_gz(c, "/api/v1/agent/actions/7/result", {"status": "failed"}, headers=hdr).status_code == 409
    c.db.on("UPDATE agent_actions SET status = %s", rows=[{"id": 7, "host_id": 10, "kind": "restart_service",
                                                            "params": {}, "status": "failed", "exit_code": 1}])
    r = post_gz(c, "/api/v1/agent/actions/7/result", {"status": "failed", "exit_code": 1, "output": "Job failed"},
                headers=hdr)
    assert r.status_code == 200, r.text
    upd = c.db.sql("UPDATE agent_actions SET status = %s")[-1]
    assert upd[2][:3] == ("failed", 1, "Job failed") and upd[2][-1] == AGENT_ID
    assert audited(c.db, "action")[-1][2][3] == "result"
    assert post_gz(c, "/api/v1/agent/actions/7/result", {"status": "sent"}, headers=hdr).status_code == 400


def test_fleet(c, monkeypatch):
    c.db.on("FROM agents a LEFT JOIN hosts h", rows=[{"agent_id": AGENT_ID, "collectors": None, "capabilities": None,
                                                       "update_status": None}])
    assert c.get("/api/v1/fleet", headers=UI).json()[0]["collectors"] == {}
    c.db.on("count(*) FILTER (WHERE last_seen >", rows=[{"total": 3, "up": 2, "upgrading": 1, "update_failed": 0}])
    s = c.get("/api/v1/fleet/summary", headers=UI).json()
    assert s["down"] == 1 and "latest" in s and "latest_version" in s
    c.db.on("UPDATE agents SET target_version = %s", rows=[{"id": AGENT_ID}])
    r = c.post("/api/v1/fleet/upgrade", headers=UI, json={"all": True, "os": "linux", "version": "0.4.0"})
    assert r.status_code == 200 and r.json()["updated"] == 1
    upd = c.db.sql("UPDATE agents SET target_version = %s")[0]
    assert upd[2] == ["0.4.0", 1, "linux"]
    r = c.post("/api/v1/fleet/upgrade", headers=UI, json={"agent_ids": [AGENT_ID], "version": "0.4.0"})
    assert c.db.sql("UPDATE agents SET target_version = %s")[-1][2][2] == [AGENT_ID]
    # Sans version : la dernière publiée, pas la valeur par défaut de la configuration.
    from hypha.api import fleet
    monkeypatch.setattr(fleet, "published_version", lambda install_dir: "9.8.7")
    r = c.post("/api/v1/fleet/upgrade", headers=UI, json={"all": True})
    assert r.status_code == 200 and r.json()["version"] == "9.8.7"
    for bad in ({"version": "0.4.0"}, {"all": True, "version": "dernière"}, {"agent_ids": ["x"], "version": "0.4.0"},
                {"all": True, "os": "macos"}):
        assert c.post("/api/v1/fleet/upgrade", headers=UI, json=bad).status_code == 400, bad
    assert c.post("/api/v1/fleet/cancel-upgrade", headers=UI, json={"all": True}).status_code == 200
    assert len(audited(c.db, "fleet")) == 4


def test_published_agent_version(tmp_path):
    from hypha.api import fleet
    assert fleet.published_version(str(tmp_path / "absent")) is None
    (tmp_path / "version.txt").write_text("0.4.0\n", encoding="utf-8")
    assert fleet.published_version(str(tmp_path)) == "0.4.0"
    bad = tmp_path / "bad"
    bad.mkdir()
    (bad / "version.txt").write_text("<html>", encoding="utf-8")
    assert fleet.published_version(str(bad)) is None


def test_hosts_kind_filter(c):
    assert c.get("/api/v1/hosts?kind=network_device", headers=UI).status_code == 200
    listed = c.db.sql("h.kind = %s")[0][1]
    assert "h.kind, h.via_host_id, h.device" in listed and "AS via_hostname" in listed
    assert c.get("/api/v1/hosts?kind=imprimante", headers=UI).status_code == 400
    c.db.on("FROM hosts h WHERE h.org_id = %s AND h.id = %s",
            rows=[{"id": 20, "hostname": "switch", "kind": "network_device", "via_host_id": 10, "via_hostname": "web-01",
                   "device": {"vendor": "MikroTik"}, "status": "up", "last_seen": NOW, "meta": {}}])
    body = c.get("/api/v1/hosts/20", headers=UI).json()
    assert body["kind"] == "network_device" and body["via_hostname"] == "web-01" and "via" in body


# ── § 23 journaux à l'ingestion : masquage, erreurs, métriques, consommation ───

PY = """Traceback (most recent call last):
  File "/opt/app/views.py", line 42, in view
    total = int(value)
ValueError: invalid literal for int() with base 10: 'abc'"""


def test_ingest_logs_masks_tracks_errors_and_counts(c):
    c.db.on("FROM sensitive_rules WHERE org_id = %s AND enabled", rows=[
        {"id": 1, "name": "Secrets", "builtin": "secrets", "pattern": None, "replacement": "[masqué:secrets]",
         "scope_query": "", "targets": ["message", "attributes"]}])
    c.db.on("FROM log_metrics WHERE org_id = %s AND enabled", rows=[
        {"id": 1, "name": "logs.ssh.failures", "query": "@evt.outcome:failure", "group_by": ["@network.client.ip"],
         "aggregation": "count", "attribute": None}])
    c.db.on("INSERT INTO error_issues", fn=lambda s, p: [{"id": 55, "fingerprint": p[1][0]}])
    old = NOW_MS - 60_000
    r = post_gz(c, "/api/v1/intake/logs", {"agent": AGENT, "logs": [
        {"ts": NOW_MS, "message": "login password=hunter2", "status": "info", "service": "app"},
        {"ts": old, "message": "Failed password for root from 198.51.100.4 port 22 ssh2", "status": "info",
         "service": "sshd"},
        {"ts": NOW_MS, "message": PY, "status": "error", "service": "api"},
    ]})
    assert r.status_code == 202 and r.json()["accepted"] == 3, r.text
    plain = c.db.copies("logs")
    with_id = [e for e in plain if ", id) FROM STDIN" in e[1]]
    assert len(with_id) == 1 and with_id[0][2][0][-1] == 5000
    messages = [row[7] for e in plain for row in e[2]]
    assert "login password=[masqué:secrets]" in messages
    tagged = json.loads(with_id[0][2][0][8])
    assert len(tagged["error"]["fingerprint"]) == 40
    issue = c.db.sql("INSERT INTO error_issues")[0][2]
    assert issue[3] == ["api"] and issue[4] == ["ValueError"] and issue[11] == [5000]
    assert c.db.sql("INSERT INTO error_issue_hits")
    from hypha.runtime import rt
    st = extras.state_of(rt())
    assert st.sensitive_pending == {(1, 1): 1}
    # Écritures échues : compteurs de masquage, consommation, seaux de métriques clos.
    st.sensitive_flushed_at = time.monotonic() - 120
    st.usage.flushed_at = time.monotonic() - 120
    st.metrics_flushed_at = 0.0
    r = post_gz(c, "/api/v1/intake/checks", {"agent": AGENT, "checks": [{"ts": NOW_MS, "name": "x", "status": 0}]})
    assert r.status_code == 202
    usage = c.db.sql("INSERT INTO usage_daily")[0][2]
    assert usage[0] == [1] and usage[2] == [3] and usage[6] == [0] and usage[7] == [1]
    assert c.db.sql("UPDATE sensitive_rules r SET matched_total")[0][2] == ([1], [1], [1])
    series = c.db.sql("INSERT INTO metric_series")
    assert any("logs.ssh.failures" in e[2][2] for e in series)
    points = [e for e in c.db.copies("metric_points")]
    assert points and points[-1][2][0][2] == 1.0


def test_otlp_traces_and_rum_errors(c):
    c.db.on("INSERT INTO error_issues", fn=lambda s, p: [{"id": 56, "fingerprint": fp} for fp in p[1]])
    ts_ns = NOW_MS * 1_000_000
    body = {"resourceSpans": [{"resource": {"attributes": [{"key": "service.name", "value": {"stringValue": "api"}}]},
            "scopeSpans": [{"spans": [{"traceId": "5b8efff798038103d269b633813fc60c", "spanId": "eee19b7ec3c1b174",
                                       "name": "GET /", "kind": 2, "startTimeUnixNano": str(ts_ns),
                                       "endTimeUnixNano": str(ts_ns + 1000), "status": {"code": 2},
                                       "events": [{"name": "exception", "timeUnixNano": str(ts_ns), "attributes": [
                                           {"key": "exception.type", "value": {"stringValue": "KeyError"}},
                                           {"key": "exception.message", "value": {"stringValue": "'id'"}}]}]}]}]}]}
    r = c.post("/v1/traces", json=body, headers={"Authorization": f"Bearer {INGEST_KEY}"})
    assert r.status_code == 200, r.text
    span = c.db.copies("spans")[0][2][0]
    assert "error.fingerprint" in json.loads(span[14])
    assert c.db.sql("INSERT INTO error_issues")[0][2][2] == ["trace"]
    r = c.post("/api/v1/intake/rum?token=pubabc", content=json.dumps(
        {"session_id": "s", "events": [{"type": "error", "name": "TypeError: x", "attributes": {"stack": "x"}}]}),
        headers={"Content-Type": "text/plain"})
    c.db.on("FROM rum_apps WHERE client_token", rows=[{"id": 5, "org_id": 1, "name": "Boutique"}])
    from hypha.runtime import rt
    rt().rum_apps.clear()
    r = c.post("/api/v1/intake/rum?token=pubabc", content=json.dumps(
        {"session_id": "s", "events": [{"type": "error", "name": "TypeError: x", "attributes": {"stack": "x"}}]}),
        headers={"Content-Type": "text/plain"})
    assert r.status_code == 202, r.text
    assert c.db.sql("INSERT INTO error_issues")[-1][2][3] == ["Boutique"]


# ── § 23 routes des journaux ───────────────────────────────────────────────────

def test_log_patterns_and_context(c):
    c.db.on("SELECT message, status, service, ts FROM logs", rows=[
        {"message": f"timeout after {i} ms", "status": "error", "service": "api", "ts": NOW} for i in range(4)])
    r = c.get("/api/v1/logs/patterns?q=service:api&limit=5", headers=UI)
    assert r.status_code == 200 and r.json()[0]["pattern"] == "timeout after <num> ms" and r.json()[0]["count"] == 4
    assert c.get(f"/api/v1/logs/5/context?ts={NOW_MS}", headers=UI).status_code == 404
    c.db.on("FROM logs WHERE org_id = %s AND id = %s AND ts >= %s AND ts <= %s",
            rows=[{"id": 5, "ts": NOW, "host_id": 10, "hostname": "web-01", "service": "nginx", "message": "x"}])
    c.db.on("(ts, id) < (%s, %s)", rows=[{"id": 4, "ts": NOW}, {"id": 3, "ts": NOW}])
    c.db.on("(ts, id) > (%s, %s)", rows=[{"id": 6, "ts": NOW}])
    body = c.get(f"/api/v1/logs/5/context?ts={NOW_MS}&before=2&after=1", headers=UI).json()
    assert [x["id"] for x in body["before"]] == [3, 4] and body["line"]["id"] == 5 and body["after"][0]["id"] == 6
    older = c.db.sql("(ts, id) < (%s, %s)")[0][2]
    assert older[1] == 10 and older[2] == "nginx" and older[-1] == 2


def test_views(c):
    c.db.on("INSERT INTO saved_views", fn=lambda s, p: [{"id": 9, "page": p[1], "name": p[2], "state": {},
                                                         "shared": p[4], "created_by": 1, "created_at": NOW}])
    r = c.post("/api/v1/views", headers=UI, json={"page": "logs", "name": "Erreurs nginx",
                                                  "state": {"q": "service:nginx status:error"}})
    assert r.status_code == 201 and audited(c.db, "view")
    assert c.post("/api/v1/views", headers=UI, json={"page": "nulle", "name": "x"}).status_code == 400
    assert c.get("/api/v1/views?page=logs", headers=UI).status_code == 200
    assert c.get("/api/v1/views?page=x", headers=UI).status_code == 400
    assert c.delete("/api/v1/views/9", headers=UI).status_code == 404
    c.db.on("FROM saved_views WHERE org_id = %s AND id = %s", rows=[{"id": 9, "page": "logs", "name": "x",
                                                                      "shared": True, "created_by": 1}])
    assert c.delete("/api/v1/views/9", headers=UI).status_code == 200


def test_log_metrics_routes(c):
    c.db.on("INSERT INTO log_metrics", rows=[{"id": 1, "name": "logs.ssh.failures"}])
    body = {"name": "logs.ssh.failures", "query": "@evt.outcome:failure", "group_by": ["@network.client.ip"]}
    assert c.post("/api/v1/logs/metrics", headers=UI, json=body).status_code == 201
    ins = c.db.sql("INSERT INTO log_metrics")[0][2]
    assert ins[3] == ["@network.client.ip"] and ins[4] == "count" and ins[5] is None
    for bad in ({**body, "name": "ssh"}, {**body, "query": '"x'}, {**body, "group_by": ["pid"]},
                {**body, "aggregation": "distribution"}, {**body, "aggregation": "p99"}):
        assert c.post("/api/v1/logs/metrics", headers=UI, json=bad).status_code == 400, bad
    r = c.post("/api/v1/logs/metrics", headers=UI, json={**body, "name": "logs.http.duration",
                                                         "aggregation": "distribution", "attribute": "duration"})
    assert r.status_code == 201 and c.db.sql("INSERT INTO log_metrics")[-1][2][5] == "@duration"
    assert c.delete("/api/v1/logs/metrics/2", headers=UI).status_code == 404
    c.db.on("FROM log_metrics WHERE org_id = %s AND id = %s", rows=[{"id": 1, "name": "logs.ssh.failures", "query": "",
                                                                      "group_by": [], "aggregation": "count",
                                                                      "attribute": None, "enabled": True}])
    c.db.on("UPDATE log_metrics SET name", rows=[{"id": 1}])
    assert c.put("/api/v1/logs/metrics/1", headers=UI, json={"enabled": False}).status_code == 200
    assert len(audited(c.db, "log_metric")) == 3


def test_sensitive_routes(c):
    c.db.rules.insert(0, (("settings ? 'sensitive_rules_seeded'",), [{"done": False}], None))
    c.db.on("SELECT o.created_at >= v.applied_at", rows=[{"new": True}])
    assert c.get("/api/v1/logs/sensitive", headers=UI).status_code == 200
    seed = c.db.sql("INSERT INTO sensitive_rules (org_id, name, builtin, replacement, enabled, position)")[0][2]
    assert seed[2][0] == "secrets" and seed[4] == [True, False, False, False, False, False, False]
    assert c.db.sql("sensitive_rules_seeded\": true")
    c.db.on("INSERT INTO sensitive_rules (org_id, name, builtin, pattern", rows=[{"id": 8}])
    r = c.post("/api/v1/logs/sensitive", headers=UI, json={"name": "Matricule", "pattern": r"MAT-\d{6}"})
    assert r.status_code == 201
    ins = c.db.sql("INSERT INTO sensitive_rules (org_id, name, builtin, pattern")[0][2]
    assert ins[4] == "[masqué:matricule]" and ins[6] == ["message", "attributes"]
    for bad in ({"name": "x", "pattern": "(a+)+"}, {"name": "x"}, {"builtin": "inconnu"},
                {"name": "x", "pattern": "a", "targets": ["tags"]}, {"name": "x", "pattern": "a", "scope_query": '"'}):
        assert c.post("/api/v1/logs/sensitive", headers=UI, json=bad).status_code == 400, bad
    r = c.post("/api/v1/logs/sensitive/test", headers=UI, json={"text": "carte 4111 1111 1111 1111",
                                                                 "rule": {"builtin": "credit_card"}})
    assert r.json() == {"masked": "carte [masqué:credit_card]",
                        "matched": [{"rule_id": None, "name": "Cartes bancaires", "count": 1}]}
    c.db.on("FROM sensitive_rules WHERE org_id = %s AND enabled ORDER BY", rows=[
        {"id": 1, "name": "Courriels", "builtin": "email", "pattern": None, "replacement": "[masqué:email]",
         "scope_query": "", "targets": ["message"], "enabled": True}])
    r = c.post("/api/v1/logs/sensitive/test", headers=UI, json={"text": "écrire à alice@example.org"})
    assert r.json()["masked"] == "écrire à [masqué:email]" and r.json()["matched"][0]["rule_id"] == 1
    # Règle livrée créée par sa seule clé : nom et remplacement par défaut.
    assert c.post("/api/v1/logs/sensitive", headers=UI, json={"builtin": "phone_fr"}).status_code == 201
    ins = c.db.sql("INSERT INTO sensitive_rules (org_id, name, builtin, pattern")[-1][2]
    assert ins[1:5] == ("Téléphones (France)", "phone_fr", None, "[masqué:phone_fr]")
    c.db.on("SELECT id FROM sensitive_rules WHERE org_id = %s ORDER BY position", rows=[{"id": 1}, {"id": 2}, {"id": 3}])
    r = c.post("/api/v1/logs/sensitive/reorder", headers=UI, json={"ids": [3, 1]})
    assert r.status_code == 200
    assert c.db.sql("UPDATE sensitive_rules r SET position")[0][2][:2] == ([3, 1, 2], [0, 1, 2])
    for bad in ({"ids": [9]}, {"ids": [1, 1]}, {"ids": "1"}):
        assert c.post("/api/v1/logs/sensitive/reorder", headers=UI, json=bad).status_code == 400, bad
    assert c.put("/api/v1/logs/sensitive/3", headers=UI, json={"enabled": True}).status_code == 404
    assert c.delete("/api/v1/logs/sensitive/3", headers=UI).status_code == 404
    assert [e[2][3] for e in audited(c.db, "sensitive_rule")] == ["create", "create", "reorder"]


def test_security_facets_accepted(c):
    r = c.get("/api/v1/logs/facets", headers=UI,
              params={"attrs": "evt.name,evt.outcome,usr.name,network.client.ip,fail2ban.jail"})
    assert r.status_code == 200, r.text
    assert {"@evt.name", "@evt.outcome", "@usr.name", "@network.client.ip", "@fail2ban.jail"} <= set(r.json())


# ── § 25 suivi des erreurs ─────────────────────────────────────────────────────

ISSUE = {"id": 5, "origin": "log", "service": "api", "error_type": "ValueError", "message": "invalid literal",
         "first_seen": NOW - timedelta(days=2), "last_seen": NOW, "count": 12, "status": "open",
         "regressed_at": None, "resolved_at": None, "last_trace_id": None, "last_log_id": 77, "last_host_id": 10,
         "fingerprint": "f" * 40, "stack": PY, "last_host": "web-01", "count_in_range": 3}


def test_errors_routes(c):
    c.db.on("WITH page AS", rows=[dict(ISSUE)])
    c.db.on("FROM error_issue_hits WHERE issue_id = ANY", rows=[{"issue_id": 5, "hour": NOW, "count": 3}])
    body = c.get("/api/v1/errors?status=open&service=api&q=int&sort=count", headers=UI).json()
    hist = body[0]["histogram"]
    assert body[0]["hosts"] == ["web-01"] and body[0]["new"] is False
    assert len(hist) == 24 and sum(n for _, n in hist) == 3 and hist[1][0] - hist[0][0] == 3_600_000
    week = c.get(f"/api/v1/errors?from={NOW_MS - 7 * 86400_000}&to={NOW_MS}", headers=UI).json()
    assert len(week[0]["histogram"]) == 24 and week[0]["histogram"][1][0] - week[0]["histogram"][0][0] == 7 * 3_600_000
    for bad in ("status=x", "sort=x"):
        assert c.get(f"/api/v1/errors?{bad}", headers=UI).status_code == 400
    assert c.get("/api/v1/errors/5", headers=UI).status_code == 404
    c.db.on("FROM error_issues i LEFT JOIN hosts h ON h.id = i.last_host_id AND h.org_id = i.org_id WHERE i.org_id",
            rows=[dict(ISSUE)])
    c.db.on("FROM logs WHERE org_id = %s AND ts >= %s AND ts <= %s AND (attributes @> %s::jsonb",
            rows=[{"kind": "log", "log_id": 77, "ts": NOW, "hostname": "web-02", "message": "boom"}])
    body = c.get("/api/v1/errors/5", headers=UI).json()
    assert body["stack"] == PY and body["occurrences"][0]["log_id"] == 77 and body["hosts"] == ["web-01", "web-02"]
    occ = c.db.sql("FROM logs WHERE org_id = %s AND ts >= %s AND ts <= %s AND (attributes @>")[0][2]
    assert json.loads(occ[3]) == {"error": {"fingerprint": "f" * 40}} and json.loads(occ[4]) == {"error.fingerprint": "f" * 40}
    r = c.put("/api/v1/errors/5", headers=UI, json={"status": "resolved"})
    assert r.status_code == 200 and audited(c.db, "error_issue")
    assert c.put("/api/v1/errors/5", headers=UI, json={"status": "fermé"}).status_code == 400


def test_explain_error_kind(monkeypatch):
    from hypha.api import explain as route
    from hypha.explain.llm import HourlyQuota
    monkeypatch.setenv("HYPHA_LLM_URL", "http://llm.invalid:11434")
    calls = []

    async def fake_chat(url, model, messages, timeout_s):
        calls.append(messages)
        return "## Ce qui se passe\nune conversion échoue.", 10

    monkeypatch.setattr(route, "chat", fake_chat)
    monkeypatch.setattr(route, "_quota", HourlyQuota(60))
    get_settings.cache_clear()
    db = DB()
    db.on("FROM error_issues i LEFT JOIN hosts h", rows=[dict(ISSUE)])
    db.on("FROM logs WHERE org_id = %s AND ts >= %s AND ts <= %s AND (attributes @>",
          rows=[{"kind": "log", "log_id": 77, "ts": NOW, "hostname": "web-01", "message": "boom"}])
    set_runtime(Runtime(settings=get_settings(), db=db))
    try:
        client = TestClient(create_app())
        r = client.post("/api/v1/explain", headers=UI, json={"kind": "error", "issue_id": 5})
        assert r.status_code == 200, r.text
        user = calls[0][1]["content"]
        assert user.startswith("Explique cette erreur applicative") and "### Pile d'appels" in user
        assert "ValueError: invalid literal" in user and "### Dernières occurrences" in user
        assert client.post("/api/v1/explain", headers=UI, json={"kind": "error"}).status_code == 400
    finally:
        set_runtime(None)
        get_settings.cache_clear()


# ── § 26 services, cartes ──────────────────────────────────────────────────────

def test_service_catalog(c):
    c.db.on("WITH RECURSIVE s(service)", fn=lambda s, p: [{"service": "api"}] if "FROM spans" in s else
            [{"service": "api"}, {"service": "nginx"}])
    c.db.on("FROM spans WHERE org_id = %s AND ts >= %s AND ts < %s AND (parent_span_id IS NULL",
            rows=[{"service": "api", "hits": 100, "errors": 20, "p95_ms": 120.0}])
    c.db.on("FROM monitors WHERE org_id = %s AND enabled", rows=[
        {"id": 1, "name": "API lente", "type": "apm", "query": {"service": "api"}, "tags": {}},
        {"id": 2, "name": "Erreurs nginx", "type": "log", "query": {"q": "service:nginx status:error"}, "tags": {}},
        {"id": 3, "name": "Autre", "type": "log", "query": {"q": "service:nginx-old"}, "tags": {}}])
    c.db.on("FROM monitor_states s JOIN monitors m", rows=[{"monitor_id": 2, "state": "ALERT"},
                                                           {"monitor_id": 1, "state": "OK"}])
    c.db.on("FROM slos WHERE org_id = %s", rows=[{"id": 4, "name": "API", "type": "monitor", "target": 99.9,
                                                  "monitor_id": 1, "good_query": None, "total_query": None, "tags": {}}])
    items = {s["name"]: s for s in c.get("/api/v1/services", headers=UI).json()}
    assert set(items) == {"api", "nginx"} and items["api"]["seen"] == ["apm", "logs"]
    assert items["api"]["apm"]["error_rate"] == 0.2 and items["api"]["slos"][0]["id"] == 4
    assert [m["id"] for m in items["nginx"]["monitors"]] == [2] and items["nginx"]["worst_state"] == "ALERT"
    r = c.put("/api/v1/services/api", headers=UI, json={"team": "plateforme", "tier": 1,
                                                         "runbook_url": "https://wiki/runbook", "depends_on": ["pg"]})
    assert r.status_code == 200 and audited(c.db, "service")
    for bad in ({"tier": 9}, {"runbook_url": "javascript:alert(1)"}, {"depends_on": ["a b"]}):
        assert c.put("/api/v1/services/api", headers=UI, json=bad).status_code == 400, bad
    assert c.post("/api/v1/services", headers=UI, json={"name": "a b"}).status_code == 400
    assert c.get("/api/v1/services/absent", headers=UI).status_code == 404
    body = c.get("/api/v1/services/api", headers=UI).json()
    assert body["name"] == "api" and "deploys" in body and "dependencies" in body
    assert c.delete("/api/v1/services/api", headers=UI).status_code == 404


def test_service_map(c):
    c.db.on("FROM spans WHERE org_id = %s AND ts >= %s AND ts < %s AND (parent_span_id IS NULL",
            rows=[{"service": "web", "hits": 10, "errors": 0, "p95_ms": 5.0},
                  {"service": "api", "hits": 10, "errors": 2, "p95_ms": 50.0}])
    c.db.on("JOIN s p ON p.trace_id = c.trace_id", rows=[{"src": "web", "dst": "api", "hits": 10, "errors": 2,
                                                          "p95_ms": 50.0}])
    c.db.on("c.kind = 'client'", rows=[{"src": "api", "peer": None, "db_system": "postgresql",
                                       "address": "192.0.2.245", "hits": 30, "errors": 0, "p95_ms": 3.0},
                                      {"src": "api", "peer": "stripe", "db_system": None, "address": None, "hits": 1,
                                       "errors": 1, "p95_ms": 300.0}])
    body = c.get("/api/v1/apm/service-map", headers=UI).json()
    kinds = {n["service"]: n["kind"] for n in body["nodes"]}
    assert kinds == {"web": "service", "api": "service", "postgresql:192.0.2.245": "database", "stripe": "external"}
    assert {(e["from"], e["to"]) for e in body["edges"]} == {("web", "api"), ("api", "postgresql:192.0.2.245"),
                                                            ("api", "stripe")}
    api = next(n for n in body["nodes"] if n["service"] == "api")
    assert api["state"] == "ALERT"                                         # 20 % d'erreurs, sans moniteur


def test_network_map(c):
    c.db.on("FROM hosts WHERE org_id = %s ORDER BY hostname", rows=[
        {"id": 10, "hostname": "web-01", "kind": "agent", "os": "linux", "tags": {"role": "web"},
         "ip_addresses": ["192.0.2.63", "172.17.0.1"], "via_host_id": None, "device": {}, "state": "up"},
        {"id": 11, "hostname": "pg03", "kind": "agent", "os": "linux", "tags": {"role": "db"},
         "ip_addresses": ["192.0.2.245", "172.17.0.1"], "via_host_id": None, "device": {}, "state": "up"},
        {"id": 20, "hostname": "switch", "kind": "network_device", "os": None, "tags": {},
         "ip_addresses": ["192.0.2.252"], "via_host_id": 10, "device": {"address": "192.0.2.252"}, "state": "up"}])
    c.db.on("FROM net_connections", rows=[
        {"host_id": 10, "raddr": "192.0.2.245", "rport": 5432, "proto": "tcp", "process": "python3", "count": 4},
        {"host_id": 10, "raddr": "172.17.0.1", "rport": 80, "proto": "tcp", "process": "curl", "count": 1},
        {"host_id": 10, "raddr": "8.8.8.8", "rport": 53, "proto": "udp", "process": "systemd-resolved", "count": 2},
        {"host_id": 10, "raddr": "127.0.0.1", "rport": 8125, "proto": "udp", "process": "app", "count": 9}])
    body = c.get("/api/v1/network/map", headers=UI).json()
    edges = {(e["from"], e["to"], e["port"]) for e in body["edges"]}
    assert edges == {(10, 11, 5432), (10, 20, 161)}
    assert {(x["addr"], x["port"]) for x in body["external"]} == {("172.17.0.1", 80), ("8.8.8.8", 53)}
    body = c.get("/api/v1/network/map?tag=role:web", headers=UI).json()
    assert {n["id"] for n in body["nodes"]} == {10, 20}


# ── § 30 métadonnées, consommation ─────────────────────────────────────────────

def test_metadata_and_usage(c):
    r = c.get("/api/v1/metrics/metadata?metric=system.cpu.usage", headers=UI).json()
    assert r["unit"] == "percent" and r["source"] == "catalog"
    r = c.get("/api/v1/metrics/metadata?metric=haproxy.backend.response.5xx", headers=UI).json()
    assert r["unit"] == "operation_per_second"
    assert c.get("/api/v1/metrics/metadata?metric=app.x", headers=UI).json()["source"] is None
    assert len(c.get("/api/v1/metrics/metadata?q=snmp.if", headers=UI).json()) >= 8
    c.db.on("INSERT INTO metric_metadata", rows=[{"metric": "app.x", "unit": "millisecond"}])
    assert c.put("/api/v1/metrics/metadata?metric=app.x", headers=UI,
                 json={"unit": "millisecond", "description": "Durée"}).status_code == 200
    assert c.put("/api/v1/metrics/metadata", headers=UI, json={"metric": "app.x", "unit": "Mo/s"}).status_code == 400
    assert c.delete("/api/v1/metrics/metadata?metric=app.x", headers=UI).status_code == 404
    assert audited(c.db, "metric_metadata")
    c.db.on("FROM usage_daily", rows=[{"day": NOW.date(), "log_lines": 10, "log_bytes": 100, "metric_points": 5,
                                       "spans": 0, "rum_events": 0, "checks": 2}])
    body = c.get("/api/v1/usage", headers=UI).json()
    assert body["days"][0]["day"] == NOW.date().isoformat() and set(body) >= {"hosts", "devices", "series",
                                                                              "top_services_by_logs"}


# ── § 30 tableaux, partage public, formules ────────────────────────────────────

DASH = {"id": 6, "title": "T", "description": "", "widgets": [
    {"id": "w1", "type": "timeseries", "x": 0, "y": 0, "w": 6, "h": 4,
     "queries": [{"q": "avg:system.cpu.usage{$host} by {host}"}], "options": {"formulas": [{"id": "f", "formula": "a * 2"}]}},
    {"id": "g", "type": "group", "x": 6, "w": 6, "widgets": [
        {"id": "w2", "type": "query_value", "queries": [{"q": "max:system.mem.pct_used{*}"}]}]}],
    "template_vars": [{"name": "host", "tag": "host", "default": "*"}], "folder": "Parc", "refresh_s": 60,
    "share_token": None, "created_by": 1, "created_at": NOW, "updated_at": NOW, "org_id": 1}


def test_dashboard_widgets_and_functions(c):
    widgets = [{"id": "a", "type": "heatmap", "queries": [{"q": "avg:x{*}"}]},
               {"id": "b", "type": "iframe", "options": {"url": "/status/?p=s33d"}},
               {"id": "c", "type": "image", "options": {"url": "https://s33d.fr/logo.png"}},
               {"id": "d", "type": "alert_value", "options": {"monitor_id": 3}},
               {"id": "e", "type": "group", "widgets": [{"id": "f", "type": "pie"}, {"id": "h", "type": "slo_list"}]},
               {"id": "i", "type": "scatter", "queries": [{"q": "avg:x{*}"}, {"q": "avg:y{*}"}]}]
    r = c.post("/api/v1/dashboards", headers=UI, json={"title": "Nouveau", "widgets": widgets, "folder": "Parc",
                                                       "refresh_s": 30})
    assert r.status_code == 201, r.text
    ins = c.db.sql("INSERT INTO dashboards")[0][2]
    assert ins[5] == "Parc" and ins[6] == 30
    for bad in ([{"id": "b", "type": "iframe", "options": {"url": "https://ailleurs.fr/"}}],
                [{"id": "b", "type": "iframe", "options": {"url": "//ailleurs.fr/"}}],
                [{"id": "a", "type": "group", "widgets": [{"id": "x", "type": "group"}]}],
                [{"id": "a", "type": "note", "widgets": []}],
                [{"id": "a", "type": "timeseries", "options": {"formulas": [{"formula": "a +"}]}}],
                [{"id": "a", "type": "alert_graph", "options": {}}],
                [{"id": "a", "type": "group", "widgets": [{"id": "a", "type": "pie"}]}]):
        assert c.post("/api/v1/dashboards", headers=UI, json={"title": "x", "widgets": bad}).status_code == 400, bad
    assert c.post("/api/v1/dashboards", headers=UI, json={"title": "x", "refresh_s": 5}).status_code == 400
    assert c.post("/api/v1/dashboards", headers=UI, json={"title": "x", "template_vars": [
        {"name": "host", "tag": "host", "multi": True, "default": ["web-01", "web-02"]}]}).status_code == 201
    assert len(audited(c.db, "dashboard")) == 2


def test_dashboard_export_import_clone_share(c):
    c.db.on("FROM dashboards WHERE org_id = %s AND id = %s", rows=[dict(DASH)])
    exp = c.get("/api/v1/dashboards/6/export", headers=UI).json()
    assert exp["format"] == "hypha-dashboard/1" and exp["folder"] == "Parc" and "id" not in exp
    assert c.post("/api/v1/dashboards/import", headers=UI, json=exp).status_code == 201
    assert c.post("/api/v1/dashboards/import", headers=UI, json={"dashboard": exp}).status_code == 201
    assert c.post("/api/v1/dashboards/6/clone", headers=UI).status_code == 201
    assert c.db.sql("INSERT INTO dashboards")[-1][2][1] == "T (copie)"
    r = c.post("/api/v1/dashboards/6/share", headers=UI)
    token = r.json()["share_token"]
    assert len(token) >= 24 and r.json()["url"].endswith(f"/share/?t={token}")
    assert c.db.sql("UPDATE dashboards SET share_token = %s")[0][2][0] == token
    assert c.delete("/api/v1/dashboards/6/share", headers=UI).status_code == 200
    assert c.put("/api/v1/dashboards/6", headers=UI, json={"folder": "Réseau"}).status_code in (200, 404)
    actions = [e[2][3] for e in audited(c.db, "dashboard")]
    assert actions[:4] == ["import", "import", "clone", "share"]


def test_dashboard_catalogue_install(c):
    r = c.get("/api/v1/dashboards/catalogue", headers=UI)
    assert r.status_code == 200, r.text
    cat = {d["id"]: d for d in r.json()}
    assert {"postgresql", "openmetrics", "plugins", "syslog", "node_exporter"} <= set(cat)
    assert cat["postgresql"]["widget_count"] > 5 and "widgets" not in cat["postgresql"]
    r = c.post("/api/v1/dashboards/catalogue/syslog", headers=UI)
    assert r.status_code == 201, r.text
    ins = c.db.sql("INSERT INTO dashboards")[-1][2]
    assert ins[1] == "Journaux reçus par syslog" and ins[5] == "Collecte générique"
    assert any(w["type"] == "log_stream" for w in json.loads(ins[3]))
    assert c.post("/api/v1/dashboards/catalogue/inconnu", headers=UI).status_code == 404
    assert [e[2][3] for e in audited(c.db, "dashboard")][-1] == "install"


def test_public_dashboard(c, monkeypatch):
    from hypha.api import public_dashboards as pub
    from hypha.query import compose
    pub._cache.clear()

    async def fake_exec(conn, org_id, mq, from_ms, to_ms, source, step_s):
        return [{"group": {}, "unit": None, "points": [[from_ms, 1.0]]}]
    monkeypatch.setattr(compose.metrics_exec, "execute", fake_exec)
    token = "t" * 32
    assert c.get(f"/api/public/dashboards/{token}").status_code == 404
    pub._cache.clear()
    c.db.on("FROM dashboards WHERE share_token = %s", rows=[dict(DASH)])
    body = c.get(f"/api/public/dashboards/{token}").json()
    assert body["title"] == "T" and "org_id" not in body and "created_by" not in body
    ok_body = {"queries": [{"id": "a", "q": "avg:system.cpu.usage{*} by {host}"},
                           {"id": "b", "q": "max:system.mem.pct_used{*}"}],
               "formulas": [{"id": "f", "formula": "a*2"}]}
    r = c.post(f"/api/public/dashboards/{token}/query", json=ok_body)
    assert r.status_code == 200, r.text
    assert c.db.sql("FROM dashboards WHERE share_token = %s")
    for q in ("avg:system.cpu.usage{host:web-01} by {host}", "avg:secret.metric{*}"):
        r = c.post(f"/api/public/dashboards/{token}/query", json={"queries": [{"id": "a", "q": q}]})
        assert r.status_code == 403 and r.json()["error"] == "query_not_allowed", q
    r = c.post(f"/api/public/dashboards/{token}/query", json={**ok_body, "formulas": [{"id": "f", "formula": "a / a"}]})
    assert r.status_code == 403
    assert c.get("/api/public/dashboards/court").status_code == 404
    assert pub.substitute("avg:x{$host,env:prod} by {host}", DASH["template_vars"]) == "avg:x{env:prod} by {host}"
    multi = [{"name": "host", "tag": "host", "multi": True, "defaults": ["web-01"]}]
    assert pub.substitute("avg:x{$host,env:prod}", multi) == "avg:x{host:web-01,env:prod}"
    multi[0]["defaults"] = ["web-01", "web-02"]
    assert pub.substitute("avg:x{$host}", multi) == "avg:x{*}"
    assert pub.substitute("avg:x{$h}", [{"name": "h", "tag": "host", "default": "web 03"}]) == 'avg:x{host:"web 03"}'
    # Le tableau partagé accepte la requête telle que l'interface l'envoie (valeur par défaut posée).
    pub._cache.clear()
    c.db.rules.insert(0, (("FROM dashboards WHERE share_token = %s",),
                          [{**DASH, "template_vars": [{"name": "host", "tag": "host", "default": "web-01"}]}], None))
    r = c.post(f"/api/public/dashboards/{token}/query",
               json={"queries": [{"id": "a", "q": "avg:system.cpu.usage{host:web-01} by {host}"}]})
    assert r.status_code == 200, r.text


def test_metrics_query_formulas_route(c):
    c.db.on("SELECT id, tags, unit FROM metric_series", rows=[{"id": 1, "tags": {"host": "web-01"}, "unit": "percent"},
                                                             {"id": 2, "tags": {"host": "web-02"}, "unit": "percent"}])
    c.db.on("WITH g AS", fn=lambda s, p: [{"gi": 0, "bucket": p[5], "v": 2.0}, {"gi": 1, "bucket": p[5], "v": 1.0}])
    r = c.post("/api/v1/metrics/query", headers=UI, json={
        "from": NOW_MS - 3600_000, "to": NOW_MS,
        "queries": [{"id": "a", "q": "avg:system.cpu.user{*} by {host}"},
                    {"id": "b", "q": "timeshift(avg:system.cpu.user{*} by {host}, -86400)"}],
        "formulas": [{"id": "f", "formula": "a - b", "alias": "écart"}]})
    assert r.status_code == 200, r.text
    queries = {s["query"] for s in r.json()["series"]}
    assert queries == {"a", "b", "f"}
    r = c.post("/api/v1/metrics/query", headers=UI, json={"queries": [{"id": "a", "q": "avg:x{*}"}],
                                                          "formulas": [{"id": "f", "formula": "a + z"}]})
    assert r.status_code == 400 and "« f »" in r.json()["detail"]


# ── § 29 synthétiques par l'API ────────────────────────────────────────────────

def test_synthetics_multistep_and_locations(c):
    body = {"name": "Connexion", "kind": "multistep", "locations": ["server", "agent:10"],
            "steps": [{"url": "https://hypha.s33d.fr/connexion"}]}
    assert c.post("/api/v1/synthetics", headers=UI, json=body).status_code == 400   # agent:10 inconnu
    c.db.on("SELECT id FROM hosts WHERE org_id = %s AND id = ANY", rows=[{"id": 10}])
    c.db.on("INSERT INTO synthetic_tests", rows=[{"id": 3}])
    r = c.post("/api/v1/synthetics", headers=UI, json=body)
    assert r.status_code in (201, 404), r.text
    ins = c.db.sql("INSERT INTO synthetic_tests")[0][2]
    assert ins[3] == "https://hypha.s33d.fr/connexion" and ins[8] == ["server", "agent:10"]
    assert audited(c.db, "synthetic")
    private = {"name": "Intranet", "kind": "http", "target": "http://172.16.1.10/", "locations": ["agent:10"]}
    assert c.post("/api/v1/synthetics", headers=UI, json=private).status_code in (201, 404)
    step_private = {"name": "x", "kind": "multistep", "steps": [{"url": "http://172.16.0.1/"}]}
    r = c.post("/api/v1/synthetics", headers=UI, json=step_private)
    assert r.status_code == 400 and r.json()["error"] == "target_not_allowed"


def test_audit_on_existing_write_routes(c):
    c.db.on("INSERT INTO log_filters", rows=[{"id": 1, "name": "sondes", "query": "x", "action": "exclude",
                                              "sample_rate": 0.0, "enabled": True, "position": 0, "excluded_total": 0,
                                              "created_at": NOW, "updated_at": NOW}])
    assert c.post("/api/v1/logs/filters", headers=UI, json={"name": "sondes", "query": '"GET /api/sante"'}).status_code == 201
    assert audited(c.db, "log_filter")[-1][2][3] == "create"
    c.db.on("DELETE FROM dashboards", rows=[{"id": 6, "title": "T"}])
    assert c.delete("/api/v1/dashboards/6", headers=UI).status_code == 200
    assert audited(c.db, "dashboard")[-1][2][3] == "delete"
    c.db.on("DELETE FROM synthetic_tests", rows=[{"id": 3, "name": "s", "kind": "http", "target": "https://x"}])
    assert c.delete("/api/v1/synthetics/3", headers=UI).status_code == 200
    assert audited(c.db, "synthetic")[-1][2][3] == "delete"
    c.db.on("DELETE FROM log_filters", rows=[{"id": 1, "name": "sondes", "query": "x"}])
    assert c.delete("/api/v1/logs/filters/1", headers=UI).status_code == 200
    assert audited(c.db, "log_filter")[-1][2][3] == "delete"
