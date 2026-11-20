"""Vague 5 des intégrations (IA et GPU) : modèles de moniteurs et tableaux du catalogue visent des
noms que l'agent publie vraiment (sonde Ollama, sonde nvidia-smi, profils OpenMetrics vllm, litellm,
tgi, llamacpp et dcgm)."""
import re

import pytest

from hypha.api import dashboard_catalogue as CAT
from hypha.monitors import templates
from hypha.monitors.validate import normalize_monitor
from test_dashboard_catalogue import AGENT, _agent_names

V5_TEMPLATES = ("ollama_down", "ollama_slow", "inference_down", "vllm_queue", "vllm_latency", "vllm_kv_cache", "tgi_queue",
                "llamacpp_deferred", "litellm_deployment", "gpu_hot", "gpu_saturated", "gpu_memory_full", "gpu_health",
                "gpu_smi_failed", "gpu_xid")
V5_BOARDS = ("gpu_nvidia", "ollama", "vllm", "litellm", "inference_servers")


def test_v5_templates_build_valid_monitors():
    for tid in V5_TEMPLATES:
        t = templates.BY_ID[tid]
        assert t["family"] == "IA et GPU"
        m = normalize_monitor(templates.build(tid, {}))
        assert m["name"]
    down = templates.build("inference_down", {"profile": "litellm"})
    assert down["query"]["tags"] == {"instance_name": "litellm"} and down["query"]["check"] == "openmetrics.can_connect"
    with pytest.raises(ValueError):
        templates.build("inference_down", {"profile": "Pas Valide"})
    hot = templates.build("gpu_hot", {"scope": "env:prod", "critical": 90, "warning": 82})
    assert hot["query"]["q"] == "max:gpu.temperature{env:prod} by {host,gpu}" and hot["thresholds"] == {"critical": 90, "warning": 82}


@pytest.mark.skipif(not AGENT.is_dir(), reason="arbre de l'agent absent")
def test_v5_names_exist_in_agent():
    known = _agent_names()
    for tid in V5_TEMPLATES:
        d = templates.build(tid, {})
        q = d["query"]
        name = q["check"] if d["type"] == "service_check" else re.match(r"^[a-z]+:([^{]+)\{", q["q"]).group(1)
        assert name in known, (tid, name)
    for cid in V5_BOARDS:
        board = CAT.get(cid)
        assert board is not None, cid
        for w in board["widgets"]:
            if w["type"] == "check_status":
                assert w["options"]["check"] in known, (cid, w["id"])
            for q in w.get("queries", []):
                assert re.match(r"^[a-z]+:([^{]+)\{", q["q"]).group(1) in known, (cid, q["q"])
    # Noms Prometheus avec « : » (vllm:…, llamacpp:…) lus dans omprofiles/vague5.go.
    assert {"vllm.requests.waiting", "vllm.time_to_first_token.p95", "llamacpp.requests.deferred", "gpu.fb.used_mib"} <= known
