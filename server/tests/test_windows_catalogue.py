"""Vague 3 (Windows) : modèles de moniteurs et tableaux prêts des journaux d'événements, des compteurs et de
WMI, d'Active Directory et du DNS Microsoft. Les noms visés existent dans le code de l'agent ; les requêtes se
lisent, variable $host posée ou non."""
import re
from pathlib import Path

import pytest

from hypha.api import dashboard_catalogue as CAT
from hypha.monitors import templates
from hypha.monitors.validate import normalize_monitor
from hypha.query import logs_ql, metrics_ql

AGENT = Path(__file__).resolve().parents[2] / "agent" / "internal"

WINDOWS_TEMPLATES = ("windows_event_critical", "windows_unexpected_shutdown", "windows_logon_failures", "windows_account_lockout",
                     "windows_eventlog_errors", "perf_counters_collect", "wmi_query", "ad_down", "ad_service", "ad_replication",
                     "ad_sysvol", "ad_ldap_bind_time", "ad_netlogon_semaphore", "windows_dns_down", "windows_dns_zone",
                     "windows_dns_recursion_failures")
WINDOWS_BOARDS = ("windows_eventlog", "windows_perf", "active_directory", "windows_dns")


def _agent_literals() -> set[str]:
    lit = re.compile(r'"([A-Za-z][A-Za-z0-9_]*(?:\.[A-Za-z0-9_]+)+)"')
    names = set()
    for p in AGENT.rglob("*.go"):
        if not p.name.endswith("_test.go"):
            names.update(lit.findall(p.read_text(encoding="utf-8")))
    return names


def test_windows_templates_build_and_validate():
    for tid in WINDOWS_TEMPLATES:
        d = templates.build(tid, {})
        m = normalize_monitor(d)
        assert m["name"] and m["priority"] in (1, 2, 3, 4), tid
        if d["type"] == "log":
            logs_ql.parse(d["query"]["q"])
            assert d["query"]["by"] == ["host"] and "{{#is_recovery}}" in d["message"]
    q = templates.build("windows_eventlog_errors", {"scope": "env:prod", "channel": "Security", "critical": 5})["query"]["q"]
    assert q == "sum:windows.eventlog.events{env:prod,status:error,channel:Security} by {host,channel}"
    # Un canal à espace ne passe pas dans un filtre d'étiquettes : tous les canaux, regroupés.
    q = templates.build("windows_eventlog_errors", {"channel": "Directory Service"})["query"]["q"]
    assert q == "sum:windows.eventlog.events{status:error} by {host,channel}"
    metrics_ql.parse(q)
    lf = templates.build("windows_logon_failures", {"critical": 50, "warning": 10})
    assert lf["thresholds"] == {"critical": 50, "warning": 10} and lf["query"]["q"] == "source:eventlog @eventlog.event_id:4625"


@pytest.mark.skipif(not AGENT.is_dir(), reason="arbre de l'agent absent")
def test_windows_templates_target_agent_names():
    known = _agent_literals()
    for tid in WINDOWS_TEMPLATES:
        d = templates.build(tid, {})
        q = d["query"]
        if d["type"] == "service_check":
            assert q["check"] in known, tid
        elif d["type"] == "metric":
            assert re.match(r"^[a-z]+:([^{]+)\{", q["q"]).group(1) in known, tid


def test_windows_boards_queries_parse():
    ids = {c["id"] for c in CAT.CATALOGUE}
    assert set(WINDOWS_BOARDS) <= ids
    for c in CAT.CATALOGUE:
        if c["id"] not in WINDOWS_BOARDS:
            continue
        for w in c["dashboard"]["widgets"]:
            for qd in w.get("queries", []):
                for host in ("*", "host:dc-01"):
                    metrics_ql.parse(metrics_ql.apply_template_vars(qd["q"], {"host": host}))
            if w["type"] == "log_stream":
                logs_ql.parse(w["options"]["log_query"])
