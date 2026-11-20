"""Catalogue des tableaux prêts (0.5.0) : chaque tableau passe la validation d'un tableau ordinaire,
et chaque métrique ou contrôle visé existe dans le code de l'agent (dossier agent/ du dépôt ; test
sauté si l'arbre de l'agent n'est pas à côté du serveur)."""
import re
from pathlib import Path

import pytest

from hypha.api import dashboard_catalogue as CAT
from hypha.api.dashboards import validate_template_vars, validate_widgets
from hypha.monitors import templates

AGENT = Path(__file__).resolve().parents[2] / "agent" / "internal"


def test_catalogue_entries_validate():
    ids = [c["id"] for c in CAT.CATALOGUE]
    assert len(ids) == len(set(ids)) and len(ids) >= 25
    for c in CAT.CATALOGUE:
        d = c["dashboard"]
        assert d["title"] and d["description"] and c["family"] and c["integrations"]
        validate_widgets(d["widgets"])
        validate_template_vars(d["template_vars"])
        for w in d["widgets"]:
            assert 0 <= w["x"] and w["x"] + w["w"] <= 12
            for q in w.get("queries", []):
                assert re.match(r"^(avg|sum|min|max|last):[A-Za-z0-9_.\-/]+\{[^}]*\}( by \{[a-z0-9_.,\-/]+\})?$", q["q"]), q["q"]
                if "$host" in q["q"]:
                    assert any(v["name"] == "host" for v in d["template_vars"]), (c["id"], q["q"])
    listing = CAT.catalogue()
    assert all("widgets" not in x and x["widget_count"] > 0 for x in listing)
    assert CAT.get("inconnu") is None


def _agent_names() -> set[str]:
    names = set()
    lit = re.compile(r'"([A-Za-z][A-Za-z0-9_]*(?:\.[A-Za-z0-9_]+)+)"')
    for p in AGENT.rglob("*.go"):
        if p.name.endswith("_test.go"):
            continue
        text = p.read_text(encoding="utf-8")
        names.update(lit.findall(text))
        if p.name == "haproxy.go":
            for kind in ("frontend", "backend"):
                for suffix in re.findall(r'prefix\+"([a-z0-9_.]+)"', text) + re.findall(r'rate\("([a-z0-9_.]+)"', text):
                    names.add(f"haproxy.{kind}.{suffix}")
        if p.name == "platform_windows.go":
            for metric in re.findall(r'\{"[A-Za-z ]+ Messages", "([a-z]+)"\}', text):
                names.add("exchange.queue." + metric)
        if p.name == "omprofiles.go":
            # Profils OpenMetrics : espace de noms + nom renommé, et suffixes des histogrammes et résumés.
            for block in re.split(r"register\(Profile\{", text)[1:]:
                ns = re.search(r'Namespace:\s*"([a-z_]+)"', block).group(1)
                for base in re.findall(r'"[A-Za-z0-9_]+":\s*"([a-z0-9_.]+)"', block):
                    names.add(f"{ns}.{base}")
                    for suffix in ("count", "sum", "avg", "p50", "p95", "p99", "p90"):
                        names.add(f"{ns}.{base}.{suffix}")
    return names


@pytest.mark.skipif(not AGENT.is_dir(), reason="arbre de l'agent absent")
def test_catalogue_metrics_exist_in_agent():
    known = _agent_names()
    missing = sorted(m for m in CAT.metric_names() if m not in known)
    assert not missing, f"métriques inconnues de l'agent : {missing}"
    missing = sorted(c for c in CAT.check_names() if c not in known)
    assert not missing, f"contrôles inconnus de l'agent : {missing}"


@pytest.mark.skipif(not AGENT.is_dir(), reason="arbre de l'agent absent")
def test_new_monitor_templates_target_real_names():
    known = _agent_names()
    for tid in ("openmetrics_down", "openmetrics_cardinality", "nagios_check", "syslog_receiver", "syslog_silent",
                "syslog_dropped", "syslog_device_errors"):
        d = templates.build(tid, {})
        q = d["query"]
        if d["type"] == "service_check":
            assert q["check"] in known, tid
        elif d["type"] == "metric":
            assert re.match(r"^[a-z]+:([^{]+)\{", q["q"]).group(1) in known, tid
    silent = templates.build("syslog_silent", {"minutes": 30, "scope": "site:lab"})
    assert silent["query"]["window_s"] == 1800 and silent["query"]["q"] == "sum:syslog.messages{site:lab} by {syslog_host}"
