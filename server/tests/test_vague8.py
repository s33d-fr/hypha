"""Vague 8 (réseau, stockage et sauvegardes auto-hébergés) : chaque modèle de moniteur se construit, passe la
validation d'un moniteur ordinaire et vise un contrôle ou une métrique que l'agent publie vraiment ; chaque
logiciel a son tableau prêt, dont chaque requête et chaque contrôle existent dans l'agent."""
import re

import pytest

from hypha.api import dashboard_catalogue as CAT
from hypha.monitors import templates
from hypha.monitors.templates_vague8 import vague8
from hypha.monitors.validate import normalize_monitor

from test_dashboard_catalogue import AGENT, _agent_names

V8 = [t["id"] for t in vague8(templates._v, templates._metric, templates._check, templates.two, templates.SCOPE)]
BOARDS = ("pihole", "adguard", "unbound", "caddy", "minio", "mosquitto", "proxmox_backup", "borg_restic")


def test_templates_build_and_validate():
    assert len(V8) == len(set(V8)) >= 35
    ids = [t["id"] for t in templates.TEMPLATES]
    assert len(ids) == len(set(ids)), "identifiant de modèle en double"
    for tid in V8:
        m = normalize_monitor(templates.build(tid, {}))
        assert m["name"] and m["type"] in ("metric", "service_check"), tid
    # « Dernière sauvegarde trop ancienne » : seuils en heures, comparés à l'âge en secondes.
    for tid, metric in (("pbs_backup_too_old", "proxmox_backup.group.last_backup.age"), ("borg_backup_too_old", "borg.last_archive.age"),
                        ("restic_backup_too_old", "restic.last_snapshot.age")):
        d = templates.build(tid, {"critical": 24, "warning": 12, "scope": "env:prod"})
        assert d["thresholds"] == {"critical": 86400, "warning": 43200}, tid
        assert d["query"]["q"].startswith(f"max:{metric}{{env:prod}} by {{host,"), d["query"]["q"]
        assert d["query"]["comparator"] == ">" and d["query"]["aggregation"] == "min"
    q = templates.build("caddy_5xx", {"scope": "env:prod"})["query"]["q"]
    assert q == "sum:caddy.http.request.duration.count{env:prod,code:5*} by {host,server}"
    assert templates.build("unbound_servfail", {})["query"]["q"] == "sum:unbound.num.answer.rcode{rcode:SERVFAIL} by {host}"
    assert templates.build("minio_low_space", {"critical": 1, "warning": 2})["thresholds"] == {"critical": 1024 ** 3, "warning": 2 * 1024 ** 3}
    assert templates.build("pbs_backup_failed", {})["query"]["by"] == ["host", "datastore", "backup_type", "backup_id"]


@pytest.mark.skipif(not AGENT.is_dir(), reason="arbre de l'agent absent")
def test_templates_target_real_names():
    known = _agent_names()
    for tid in V8:
        d = templates.build(tid, {})
        q = d["query"]
        name = q["check"] if d["type"] == "service_check" else re.match(r"^[a-z]+:([^{]+)\{", q["q"]).group(1)
        assert name in known, f"{tid} : {name} inconnu de l'agent"


def test_one_board_per_software():
    for cid in BOARDS:
        board = CAT.get(cid)
        assert board is not None, cid
    covered = {i for c in CAT.CATALOGUE for i in c["integrations"]}
    assert {"pihole", "adguard", "unbound", "caddy", "mosquitto", "proxmox_backup", "borg", "restic"} <= covered


@pytest.mark.skipif(not AGENT.is_dir(), reason="arbre de l'agent absent")
def test_boards_target_real_names():
    known = _agent_names()
    for cid in BOARDS:
        for w in CAT.get(cid)["widgets"]:
            if w["type"] == "check_status":
                assert w["options"]["check"] in known, (cid, w["id"])
            for q in w.get("queries", []):
                assert re.match(r"^[a-z]+:([^{]+)\{", q["q"]).group(1) in known, (cid, q["q"])
    # Les intégrations du registre de l'agent existent sous ces noms (état des collecteurs).
    reg = (AGENT / "integrations" / "vague8.go").read_text(encoding="utf-8")
    for p in ("pihole", "adguard", "unbound", "caddy", "mosquitto", "proxmox_backup", "borg", "restic"):
        assert f'"{p}"' in reg, p
