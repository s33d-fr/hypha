"""§§ 14, 15, 27 à 30 côté agent : versions dpkg, corps d'inventaire, DBM, synthétiques privés, actions."""
import json

import pytest

from hypha.ingest import agent_bodies as AB
from hypha.ingest import body as B
from hypha.inventory import versions as V

NOW = 1_800_000_000_000
WIN = B.Window.around(NOW)
AGENT = {"id": "6f1c2b8e-0000-4000-8000-000000000001", "hostname": "web-01"}


@pytest.mark.parametrize("a,b,expected", [
    ("1.0", "1.0", 0), ("1.0", "1.0.1", -1), ("1.0~rc1", "1.0", -1), ("1.0", "1.0~rc1", 1),
    ("1:0.1", "2.0", 1), ("0:2.0", "2.0", 0), ("3.5.1-1ubuntu1", "3.5.2", -1), ("3.5.2-0ubuntu1", "3.5.2", 1),
    ("1.2a", "1.2", 1), ("1.2+dfsg", "1.2", 1), ("1.2+dfsg", "1.2a", 1), ("2.10", "2.9", 1), ("2.010", "2.10", 0),
    ("1.0-1", "1.0-1~bpo1", 1), ("7.4.3-1+deb12u2", "7.4.3-1+deb12u10", -1), ("3.5.2.0", "3.5.10.0", -1),
    ("", "0", 0), ("1.0~~", "1.0~", -1), ("a", "b", -1), ("1.0-a", "1.0-1", 1),
])
def test_dpkg_compare(a, b, expected):
    assert V.compare(a, b) == expected
    assert V.compare(b, a) == -expected


def test_version_bounds():
    assert V.matches("3.5.1-1ubuntu1", lt="3.5.2") and not V.matches("3.5.2", lt="3.5.2")
    assert V.matches("3.5.2", le="3.5.2", ge="3.5.2", eq="3.5.2")
    assert V.matches("3.6", gt="3.5.2") and not V.matches("3.5", gt="3.5.2")
    assert V.split_version("2:1.2-3-4") == (2, "1.2-3", "4")


def test_agent_via_and_device_host():
    agent = B.parse_agent({"agent": {**AGENT, "via": "6f1c2b8e-0000-4000-8000-000000000099"}})
    assert agent.via == "6f1c2b8e-0000-4000-8000-000000000099"
    with pytest.raises(B.BodyError):
        B.parse_agent({"agent": {**AGENT, "via": "pas-un-uuid"}})
    with pytest.raises(B.BodyError):
        B.parse_agent({"agent": {**AGENT, "via": AGENT["id"]}})
    h = B.parse_host({"host": {"hostname": "switch-vlan1", "kind": "network_device",
                               "device": {"address": "192.0.2.252", "vendor": "MikroTik", "interfaces": 24},
                               "capabilities": {"actions": {"enabled": True}}}}, agent)
    assert h["kind"] == "network_device" and json.loads(h["device"])["vendor"] == "MikroTik"
    assert json.loads(h["ip_addresses"]) == ["192.0.2.252"] and h["capabilities"] is None
    # Ressource sans adresse connue (VM Azure lue sans ses interfaces, VM vSphere éteinte) : liste vide,
    # jamais [""] (19/11).
    for address in ("", "  ", None):
        h = B.parse_host({"host": {"hostname": "vm-sans-ip", "kind": "cloud_resource", "ip_addresses": [],
                                   "device": {"address": address, "provider": "azure"}}}, agent)
        assert json.loads(h["ip_addresses"]) == []
    plain = B.parse_agent({"agent": AGENT})
    h = B.parse_host({"host": {"hostname": "web-01", "capabilities": {"synthetics": True, "auto_update": True},
                               "status": {"update": {"state": "downloading", "to": "0.4.0"}}}}, plain)
    assert h["kind"] == "agent" and h["device"] is None
    assert json.loads(h["capabilities"]) == {"synthetics": True, "auto_update": True}
    assert json.loads(h["update_status"])["state"] == "downloading"
    assert B.parse_host({"host": {"hostname": "web-01"}}, plain)["capabilities"] is None


def test_parse_inventory():
    inv = AB.parse_inventory({"ts": NOW, "hardware": {"vendor": "Dell Inc.", "cpu": {"cores": 16}},
                              "os": {"name": "Ubuntu", "version": "26.04", "reboot_required": "true"},
                              "updates": {"pending": 12, "security": "x", "list": [
                                  {"name": "openssl", "current": "3.5.1", "candidate": "3.5.2", "security": True},
                                  {"pas": "de nom"}]},
                              "packages": [{"name": "openssl", "version": "3.5.1-1ubuntu1", "arch": "amd64",
                                            "source": "dpkg"},
                                           {"name": "openssl", "version": "3.5.1-1ubuntu2", "arch": "amd64"},
                                           {"name": "zlib1g", "version": "1:1.3"}, {"version": "sans nom"}, "x"]})
    assert json.loads(inv["os"])["reboot_required"] is True
    updates = json.loads(inv["updates"])
    assert updates["pending"] == 12 and updates["security"] is None and len(updates["list"]) == 1
    assert inv["packages"] == [("openssl", "3.5.1-1ubuntu2", "amd64", ""), ("zlib1g", "1:1.3", "", "")]
    assert inv["dropped"] == 2
    with pytest.raises(B.BodyError):
        AB.parse_inventory({"packages": {"a": 1}})


def test_parse_dbm():
    rows, dropped = AB.parse_dbm({"ts": NOW, "queries": [
        {"engine": "postgres", "instance": "db-01:5432", "db": "hypha", "query_id": "-123", "query": "SELECT $1",
         "calls": 10, "total_ms": 25.0, "rows": 10, "shared_blks_hit": 90, "shared_blks_read": 10},
        {"query_id": "1", "query": "SELECT 1", "calls": 0, "total_ms": 0},
        {"query_id": "", "query": "x", "calls": 1, "total_ms": 1}, "x"]}, 1, 10, WIN)
    assert len(rows) == 2 and dropped == 2 and len(rows[0]) == len(AB.DBM_COLUMNS)
    assert rows[0][AB.DBM_COLUMNS.index("mean_ms")] == 2.5 and rows[1][AB.DBM_COLUMNS.index("mean_ms")] is None
    with pytest.raises(B.BodyError):
        AB.parse_dbm({}, 1, 10, WIN)


def test_parse_synthetic_results_and_action_result():
    out, dropped = AB.parse_synthetic_results({"results": [
        {"test_id": 3, "ts": NOW, "ok": False, "status_code": 503, "latency_ms": 12.5, "error": "boom",
         "details": {"steps": []}, "location": "agent:web-01"},
        {"test_id": 3, "ok": "oui"}, {"test_id": 0, "ok": True}, {"test_id": 3, "ts": NOW - 30 * 86400_000, "ok": True},
    ]}, WIN)
    assert len(out) == 1 and dropped == 3 and out[0]["status_code"] == 503
    r = AB.parse_action_result({"status": "succeeded", "exit_code": 0, "output": "é" * 70_000})
    assert r["status"] == "succeeded" and len(r["output"].encode()) <= AB.MAX_OUTPUT
    with pytest.raises(B.BodyError):
        AB.parse_action_result({"status": "sent"})
    assert AB.header_agent_id("6F1C2B8E00004000800000000000000A") == "6f1c2b8e-0000-4000-8000-00000000000a"
    with pytest.raises(B.BodyError):
        AB.header_agent_id(None)
