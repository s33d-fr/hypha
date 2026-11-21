"""Vague 7 des intégrations (applications auto-hébergées) : chaque modèle de moniteur se construit, passe
la validation d'un moniteur ordinaire et vise un contrôle ou une métrique que l'agent publie vraiment ;
chaque application a son tableau prêt."""
import re

import pytest

from hypha.api import dashboard_catalogue as CAT
from hypha.monitors import templates
from hypha.monitors.templates_vague7 import vague7
from hypha.monitors.validate import normalize_monitor

from test_dashboard_catalogue import AGENT, _agent_names

V7 = [t["id"] for t in vague7(templates._v, templates._metric, templates._check, templates.two, templates.SCOPE)]
V7_BOARDS = ("nextcloud", "jellyfin", "syncthing", "immich", "gitea", "synapse", "homeassistant", "mastodon")


def test_v7_templates_build_and_validate():
    assert len(V7) == len(set(V7)) >= 25
    ids = [t["id"] for t in templates.TEMPLATES]
    assert len(ids) == len(set(ids)), "identifiant de modèle en double"
    for tid in V7:
        m = normalize_monitor(templates.build(tid, {}))
        assert m["name"] and m["type"] in ("metric", "service_check"), tid
    down = templates.build("gitea_down", {"profile": "forgejo"})
    assert down["query"]["tags"] == {"instance_name": "forgejo"} and down["query"]["check"] == "openmetrics.can_connect"
    with pytest.raises(ValueError):
        templates.build("mastodon_down", {"profile": "Pas Valide"})
    free = templates.build("nextcloud_storage_free", {"critical": 2, "warning": 10})
    assert free["thresholds"] == {"critical": 2 * 1024 ** 3, "warning": 10 * 1024 ** 3}
    q = templates.build("immich_backlog", {"scope": "env:prod"})["query"]["q"]
    assert q == "max:immich.jobs{env:prod,state:waiting} by {host,queue}"
    assert templates.build("immich_backlog", {})["query"]["q"] == "max:immich.jobs{state:waiting} by {host,queue}"
    lag = templates.build("synapse_event_lag", {})
    assert lag["thresholds"] == {"critical": 60000, "warning": 10000}
    absent = templates.build("syncthing_device_absent", {})
    assert absent["thresholds"] == {"critical": 72 * 3600, "warning": 24 * 3600}


@pytest.mark.skipif(not AGENT.is_dir(), reason="arbre de l'agent absent")
def test_v7_names_exist_in_agent():
    known = _agent_names()
    for tid in V7:
        d = templates.build(tid, {})
        q = d["query"]
        name = q["check"] if d["type"] == "service_check" else re.match(r"^[a-z]+:([^{]+)\{", q["q"]).group(1)
        assert name in known, f"{tid} : {name} inconnu de l'agent"
    for cid in V7_BOARDS:
        board = CAT.get(cid)
        assert board is not None, cid
        for w in board["widgets"]:
            if w["type"] == "check_status":
                assert w["options"]["check"] in known, (cid, w["id"])
            for q in w.get("queries", []):
                assert re.match(r"^[a-z]+:([^{]+)\{", q["q"]).group(1) in known, (cid, q["q"])


def test_v7_one_board_per_application():
    covered = {i for c in CAT.CATALOGUE for i in c["integrations"]}
    for i in ("nextcloud", "jellyfin", "syncthing", "immich", "openmetrics:gitea", "openmetrics:forgejo", "openmetrics:synapse",
              "openmetrics:homeassistant", "openmetrics:mastodon", "openmetrics:mastodon_streaming"):
        assert i in covered, i
    ids = [c["id"] for c in CAT.CATALOGUE]
    assert len(ids) == len(set(ids)), "identifiant de tableau en double"
    if AGENT.is_dir():
        reg = (AGENT / "integrations" / "vague7.go").read_text(encoding="utf-8")
        for p in ("nextcloud", "jellyfin", "syncthing", "immich"):
            assert f'add("{p}"' in reg, p
