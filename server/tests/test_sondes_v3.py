"""Sondes dédiées (vague 3) : chaque modèle de moniteur se construit, passe la validation d'un
moniteur ordinaire et vise un contrôle ou une métrique que l'agent publie vraiment ; chaque sonde a
son tableau prêt."""
import re
from pathlib import Path

import pytest

from hypha.api import dashboard_catalogue as CAT
from hypha.monitors import templates
from hypha.monitors.validate import normalize_monitor
from hypha.monitors.templates_sondes import sondes

from test_dashboard_catalogue import AGENT, _agent_names

V3 = [t["id"] for t in sondes(templates._v, templates._metric, templates._check, templates.two, templates.SCOPE)]


def test_templates_build_and_validate():
    assert len(V3) == len(set(V3)) >= 50
    ids = [t["id"] for t in templates.TEMPLATES]
    assert len(ids) == len(set(ids)), "identifiant de modèle en double"
    for tid in V3:
        m = normalize_monitor(templates.build(tid, {}))
        assert m["name"] and m["type"] in ("metric", "service_check"), tid
    q = templates.build("gunicorn_busy", {"scope": "env:prod"})["query"]["q"]
    assert q == "max:gunicorn.worker_processes{env:prod,state:idle} by {host,app}"
    assert templates.build("wazuh_agents_disconnected", {})["query"]["q"] == "max:wazuh.agents{status:disconnected} by {host}"


@pytest.mark.skipif(not AGENT.is_dir(), reason="arbre de l'agent absent")
def test_templates_target_real_names():
    known = _agent_names()
    for tid in V3:
        d = templates.build(tid, {})
        q = d["query"]
        name = q["check"] if d["type"] == "service_check" else re.match(r"^[a-z]+:([^{]+)\{", q["q"]).group(1)
        assert name in known, f"{tid} : {name} inconnu de l'agent"


def test_one_board_per_probe():
    probes = {"phpfpm", "varnish", "squid", "lighttpd", "gunicorn", "supervisord", "pgbouncer", "proxysql", "clickhouse",
              "couchdb", "couchbase", "zookeeper", "ceph", "btrfs", "nfs", "openldap", "keycloak", "powerdns",
              "powerdns_recursor", "nexus", "harbor", "fluentd", "ssh", "wazuh", "suricata"}
    covered = {i for c in CAT.CATALOGUE for i in c["integrations"]}
    assert probes <= covered, sorted(probes - covered)
    # Les intégrations du registre de l'agent existent bien sous ces noms (état des collecteurs).
    if AGENT.is_dir():
        reg = (AGENT / "integrations" / "dedicated.go").read_text(encoding="utf-8")
        for p in probes:
            assert f'"{p}"' in reg or f'"{p}",' in reg or p in ("gunicorn", "btrfs", "nfs", "ssh"), p
