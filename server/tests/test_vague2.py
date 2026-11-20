"""Vague 2 des intégrations (profils OpenMetrics : logiciels qui publient leurs mesures au format
Prometheus, et logiciels Java par l'exportateur JMX) : modèles de moniteurs et tableaux du catalogue
visent des noms que l'agent publie vraiment (omprofiles/vague2.go)."""
import re

import pytest

from hypha.api import dashboard_catalogue as CAT
from hypha.monitors import templates
from hypha.monitors.validate import normalize_monitor
from test_dashboard_catalogue import AGENT, _agent_names

V2_TEMPLATES = ("prom_app_down", "jmx_scrape_error", "consul_autopilot", "vault_sealed", "etcd_no_leader", "etcd_fsync_slow",
                "coredns_servfail", "traefik_server_down", "envoy_upstream_5xx", "kong_datastore", "kong_target_unhealthy",
                "cockroach_unavailable", "cockroach_underreplicated", "temporal_backlog_age", "temporal_persistence_errors",
                "n8n_workflow_failed", "celery_worker_offline", "celery_task_failed", "tomcat_errors", "kafka_under_replicated",
                "kafka_offline_partitions", "activemq_memory", "activemq_store", "activemq_queue_backlog", "cassandra_timeouts",
                "cassandra_unavailables", "cassandra_pending_compactions", "solr_errors", "wildfly_errors", "wildfly_tx_timeouts",
                "pulsar_backlog", "scylla_reactor_saturated", "scylla_write_failures", "sonarqube_health", "sonarqube_es_disk",
                "gitlab_5xx", "gitlab_puma_queue", "gitlab_sidekiq_failures", "cert_manager_not_ready", "velero_backup_failed",
                "velero_location_unavailable", "argocd_app_degraded", "argocd_app_out_of_sync", "linkerd_failures",
                "keda_scaler_errors")
V2_BOARDS = ("consul", "vault", "etcd", "coredns", "traefik", "envoy", "kong", "cockroachdb", "n8n", "temporal", "celery", "tomcat",
             "kafka", "activemq", "cassandra", "solr", "wildfly", "pulsar", "scylla", "sonarqube", "gitlab", "cert_manager", "velero",
             "argocd", "linkerd", "keda")


def test_v2_templates_build_valid_monitors():
    for tid in V2_TEMPLATES:
        m = normalize_monitor(templates.build(tid, {}))
        assert m["name"], tid
    down = templates.build("prom_app_down", {"profile": "kafka"})
    assert down["query"]["tags"] == {"instance_name": "kafka"} and down["query"]["check"] == "openmetrics.can_connect"
    with pytest.raises(ValueError):
        templates.build("prom_app_down", {"profile": "Pas Valide"})
    with pytest.raises(ValueError):
        templates.build("jmx_scrape_error", {"profile": "solr"})
    with pytest.raises(ValueError):
        templates.build("sonarqube_health", {"component": "base"})
    servfail = templates.build("coredns_servfail", {"scope": "env:prod"})
    assert servfail["query"]["q"] == "avg:coredns.responses{env:prod,rcode:SERVFAIL} by {host}"
    assert templates.build("coredns_servfail", {})["query"]["q"] == "avg:coredns.responses{rcode:SERVFAIL} by {host}"
    es = templates.build("sonarqube_health", {"component": "elasticsearch"})
    assert es["query"]["q"] == "min:sonarqube.health.elasticsearch{*} by {host}"


@pytest.mark.skipif(not AGENT.is_dir(), reason="arbre de l'agent absent")
def test_v2_names_exist_in_agent():
    known = _agent_names()
    for tid in V2_TEMPLATES:
        d = templates.build(tid, {})
        q = d["query"]
        name = q["check"] if d["type"] == "service_check" else re.match(r"^[a-z]+:([^{]+)\{", q["q"]).group(1)
        assert name in known, (tid, name)
    for comp in ("web", "compute_engine", "elasticsearch"):
        assert f"sonarqube.health.{comp}" in known
    for prof in ("tomcat", "kafka", "activemq", "cassandra"):
        assert f"{prof}.jmx.scrape_error" in known
    for cid in V2_BOARDS:
        board = CAT.get(cid)
        assert board is not None, cid
        for w in board["widgets"]:
            if w["type"] == "check_status":
                assert w["options"]["check"] in known, (cid, w["id"])
            for q in w.get("queries", []):
                assert re.match(r"^[a-z]+:([^{]+)\{", q["q"]).group(1) in known, (cid, q["q"])
