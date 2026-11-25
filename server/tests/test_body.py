import gzip
import json

import pytest

from hypha.ingest import body as B
from hypha.util import ms_to_dt

NOW = 1_800_000_000_000
WIN = B.Window.around(NOW)
AGENT = {"id": "6f1c2b8e-0000-4000-8000-000000000001", "version": "0.1.0", "hostname": "web-01",
         "os": "linux", "arch": "amd64"}


def test_decompress_gzip_and_identity():
    data = b'{"a":1}'
    assert B.decompress(gzip.compress(data), "gzip") == data
    assert B.decompress(data, None) == data
    assert B.decompress(data, "identity") == data


def test_decompress_limits():
    bomb = gzip.compress(b"0" * (2 * 1024 * 1024))
    with pytest.raises(B.BodyError) as e:
        B.decompress(bomb, "gzip", limit=1024 * 1024)
    assert e.value.status == 413
    with pytest.raises(B.BodyError) as e:
        B.decompress(gzip.compress(b"x" * 1000)[:-8], "gzip")
    assert e.value.status == 400
    with pytest.raises(B.BodyError) as e:
        B.decompress(b"not gzip", "gzip")
    assert e.value.status == 400
    with pytest.raises(B.BodyError) as e:
        B.decompress(b"x", "br")
    assert e.value.status == 400


def test_parse_json_and_agent():
    with pytest.raises(B.BodyError):
        B.parse_json_object(b"[1]")
    with pytest.raises(B.BodyError):
        B.parse_json_object(b"{bad")
    agent = B.parse_agent({"agent": AGENT})
    assert agent.id == AGENT["id"] and agent.hostname == "web-01"
    with pytest.raises(B.BodyError):
        B.parse_agent({})
    with pytest.raises(B.BodyError):
        B.parse_agent({"agent": dict(AGENT, id="pas-un-uuid")})
    with pytest.raises(B.BodyError):
        B.parse_agent({"agent": dict(AGENT, hostname="")})


def test_window_bounds():
    assert WIN.ok(NOW - 14 * 86400_000)
    assert not WIN.ok(NOW - 14 * 86400_000 - 1)
    assert WIN.ok(NOW + 600_000)
    assert not WIN.ok(NOW + 600_001)


def test_parse_series_drops_and_default_host():
    items = [
        {"metric": "system.cpu.user", "type": "gauge", "unit": "percent", "tags": {},
         "points": [[NOW, 12.5], [NOW - 15 * 86400_000, 1.0], [NOW, float("nan")], [NOW, "x"], [NOW]]},
        {"metric": "bad name!", "points": [[NOW, 1], [NOW, 2]]},
        {"metric": "app.count", "type": "weird", "tags": {"host": "other"}, "points": [[NOW, 3]]},
        "pas un objet",
    ]
    batches, dropped = B.parse_series(items, WIN, "web-01")
    assert dropped == 4 + 2 + 1
    assert [b.name for b in batches] == ["system.cpu.user", "app.count"]
    assert batches[0].tags == {"host": "web-01"}
    assert batches[0].points == [(ms_to_dt(NOW), 12.5)]
    assert batches[1].type == "gauge" and batches[1].tags == {"host": "other"}
    assert batches[1].unit is None                     # séries StatsD : unité absente, stockée NULL


def test_parse_logs():
    items = [
        {"ts": NOW, "message": "boom\x00", "status": "ERR", "service": "nginx", "source": "file",
         "attributes": {"http": {"status_code": 502}}, "tags": {"env": "prod"}, "trace_id": None},
        {"ts": NOW, "message": {"structured": True}, "status": 3},
        {"ts": NOW},
        {"ts": NOW - 20 * 86400_000, "message": "trop vieux"},
        {"message": "sans date"},
    ]
    rows, dropped = B.parse_logs(items, WIN, 1, 10, "web-01", now=NOW)
    assert dropped == 2
    assert len(rows) == 3
    first = dict(zip(B.LOG_COLUMNS, rows[0]))
    assert first["message"] == "boom" and first["status"] == "error" and first["host_id"] == 10
    assert json.loads(first["attributes"]) == {"http": {"status_code": 502}}
    second = dict(zip(B.LOG_COLUMNS, rows[1]))
    assert json.loads(second["message"]) == {"structured": True} and second["status"] == "error"
    assert dict(zip(B.LOG_COLUMNS, rows[2]))["ts"] == ms_to_dt(NOW)


def test_log_status_aliases():
    assert B.normalize_log_status("WARN") == "warning"
    assert B.normalize_log_status("fatal") == "critical"
    assert B.normalize_log_status("whatever") == "info"
    assert B.normalize_log_status(None) == "info"


def test_parse_checks_and_events():
    rows, dropped = B.parse_checks([
        {"ts": NOW, "name": "systemd.unit", "status": 2, "message": "failed", "tags": {"unit": "nginx.service"}},
        {"ts": NOW, "name": "x", "status": 7},
        {"ts": NOW, "status": 0},
    ], WIN, 1, 10, now=NOW)
    assert len(rows) == 1 and dropped == 2
    assert rows[0][3:5] == ("systemd.unit", 2)
    rows, dropped = B.parse_events([
        {"ts": NOW, "title": "nginx : active → failed", "alert_type": "critical", "priority": "urgent",
         "type": "service_state", "aggregation_key": "systemd:nginx"},
        {"ts": NOW, "text": "sans titre"},
    ], WIN, 1, 10, now=NOW)
    assert dropped == 1
    ev = dict(zip(B.EVENT_COLUMNS, rows[0]))
    assert ev["alert_type"] == "error" and ev["priority"] == "normal" and ev["source"] == "agent"
    assert ev["event_type"] == "service_state"


def test_snapshots():
    assert B.snapshot_ts({"ts": NOW}, WIN) == ms_to_dt(NOW)
    assert B.snapshot_ts({}, WIN) is None
    ts = ms_to_dt(NOW)
    rows, dropped = B.parse_processes([
        {"pid": 1234, "ppid": 1, "name": "nginx", "user": "www-data", "cmdline": "nginx: worker", "state": "S",
         "cpu_pct": 3.2, "mem_rss": 52428800, "mem_pct": 0.3, "threads": 4, "started": NOW - 1000,
         "io_read_bps": 0, "io_write_bps": 1024},
        {"name": "sans pid"},
    ], ts, 1, 10)
    assert len(rows) == 1 and dropped == 1 and len(rows[0]) == len(B.PROCESS_COLUMNS)
    rows, dropped = B.parse_connections([
        {"proto": "TCP", "direction": "outgoing", "laddr": "192.0.2.63", "lport": 0, "raddr": "192.0.2.245",
         "rport": 5432, "state": "ESTABLISHED", "pid": 901, "process": "python3", "count": 12},
        {"direction": "x"},
    ], ts, 1, 10)
    assert rows[0][3] == "tcp" and rows[0][-1] == 12 and dropped == 1
    rows, _ = B.parse_connections([{"proto": "tcp", "direction": "incoming", "lport": 443, "rport": 0}], ts, 1, 10)
    assert dict(zip(B.CONNECTION_COLUMNS, rows[0]))["rport"] == 0
    rows, dropped = B.parse_containers([{"id": "abc", "name": "zot", "labels": {"a": "b"}}], ts, 1, 10)
    assert len(rows[0]) == len(B.CONTAINER_COLUMNS) and dropped == 0


def test_parse_host():
    h = B.parse_host({"host": {"hostname": "web-01", "cpu_cores": 8, "mem_total": 17179869184,
                               "ip_addresses": ["192.0.2.63"], "boot_time": NOW, "tags": {"env": "prod"},
                               "meta": {"virtualization": "kvm"}, "status": {"collectors": {"logs": "ok"}}}},
                     B.parse_agent({"agent": AGENT}))
    assert h["cpu_cores"] == 8 and json.loads(h["tags"]) == {"env": "prod"}
    assert json.loads(h["status"]) == {"collectors": {"logs": "ok"}}
    assert h["os"] == "linux" and h["agent_version"] == "0.1.0"
    with pytest.raises(B.BodyError):
        B.parse_host({}, B.parse_agent({"agent": AGENT}))



def test_legacy_plugin_names():
    """Agent d'avant la 0.14.0 : les greffons arrivent sous l'ancien nom, rangés sous le nom actuel."""
    old_tag = "nagios" + "_check"
    batches, dropped = B.parse_series([
        {"metric": "nagios.perfdata.warn", "points": [[NOW, 50]], "tags": {"host": "web-01", old_tag: "fd", "perf_label": "pct"}},
        {"metric": "nagios.autre", "points": [[NOW, 1]], "tags": {old_tag: "garde"}},
    ], WIN, "web-01")
    assert dropped == 0
    assert (batches[0].name, batches[0].tags) == ("plugin.perfdata.warn", {"host": "web-01", "plugin": "fd", "perf_label": "pct"})
    assert batches[1].name == "nagios.autre" and batches[1].tags[old_tag] == "garde"
    rows, dropped = B.parse_checks([{"name": "nagios.check", "status": 1, "ts": NOW, "tags": {old_tag: "fd"}}], WIN, 1, 2)
    assert dropped == 0 and rows[0][3] == "plugin.check" and json.loads(rows[0][6]) == {"plugin": "fd"}
    h = B.parse_host({"host": {"hostname": "web-01", "status": {"nagios:fd": "ok", "syslog": "ok"},
                               "capabilities": {"integrations": ["openmetrics:x", "nagios:fd"]}}},
                     B.parse_agent({"agent": AGENT}))
    assert json.loads(h["status"]) == {"plugin:fd": "ok", "syslog": "ok"}
    assert json.loads(h["capabilities"]) == {"integrations": ["openmetrics:x", "plugin:fd"]}

def test_parse_rum():
    rows, dropped = B.parse_rum({"session_id": "s1", "events": [
        {"ts": NOW, "type": "view", "url": "https://x/", "duration_ms": 1200},
        {"ts": NOW, "type": "vital", "name": "LCP", "duration_ms": 2100.5},
        {"ts": NOW, "type": "inconnu"},
    ]}, WIN, 1, 2, "Mozilla", "FR", now=NOW)
    assert len(rows) == 2 and dropped == 1
    with pytest.raises(B.BodyError):
        B.parse_rum({"events": []}, WIN, 1, 2, None, None)
