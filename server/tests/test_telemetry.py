import logging
from types import SimpleNamespace

import pytest

from hypha import telemetry
from hypha.config import _sample, get_settings

OFF = SimpleNamespace(otel_endpoint="", otel_sample=0.2, mode="saas")


# ── filtre d'exclusion des routes ──────────────────────────────────────────────

@pytest.mark.parametrize("path", [
    "/v1/traces", "/v1/metrics", "/v1/logs", "/v1", "//v1/traces",
    "/api/v1/intake/metrics", "/api/v1/intake/logs", "/api/v1/intake/rum", "/api/v1/intake",
    "/api/sante", "/api/sante/",
    "/api/auth/rappel", "/api/auth/connexion", "/api/auth/local",
])
def test_excluded_paths(path):
    assert telemetry.is_excluded(path)
    assert telemetry.exclude_scope({"type": "http", "path": path})


@pytest.mark.parametrize("path", [
    "/", "/api/v1/hosts", "/api/v1/hosts/14", "/api/v1/metrics/query", "/api/v1/explain", "/api/v1/me",
    "/api/v1/intakes", "/v10/traces", "/v1x", "/api/v1/logs/search", "/api/santeX", "/rum/hypha-rum.js",
])
def test_traced_paths(path):
    assert not telemetry.is_excluded(path)


def test_exclude_scope_without_path():
    assert not telemetry.exclude_scope({})


def test_root_filter_rule():
    assert telemetry.root_allowed(True, "GET /api/v1/hosts")
    assert telemetry.root_allowed(False, "hypha.worker.monitors")
    assert not telemetry.root_allowed(False, "SELECT hypha")
    assert not telemetry.root_allowed(False, "POST")
    assert not telemetry.root_allowed(False, None)


def test_statement_text_truncated_without_values():
    q = "SELECT  id,\n  name FROM metric_series\n WHERE org_id = %s AND name = %s"
    assert telemetry.statement_text(q) == "SELECT id, name FROM metric_series WHERE org_id = %s AND name = %s"
    long = "INSERT INTO logs (a) VALUES " + ", ".join(["(%s)"] * 200)
    text = telemetry.statement_text(long)
    assert len(text) == telemetry.MAX_STATEMENT and text.endswith("…")
    assert telemetry.statement_operation("copy logs (a) FROM STDIN") == "COPY"
    assert telemetry.statement_operation("(SELECT 1)") == "SQL"
    assert telemetry.statement_operation("") == "SQL"


# ── désactivé par défaut ───────────────────────────────────────────────────────

def test_disabled_by_default(monkeypatch):
    monkeypatch.delenv("HYPHA_OTEL_ENDPOINT", raising=False)
    monkeypatch.delenv("HYPHA_OTEL_SAMPLE", raising=False)
    get_settings.cache_clear()
    try:
        s = get_settings()
        assert s.otel_endpoint == "" and s.otel_sample == 0.2
        assert telemetry.config_from(s, "hypha-api") is None
    finally:
        get_settings.cache_clear()
    assert telemetry.setup(OFF, "hypha-api") is False
    assert not telemetry.enabled()
    assert telemetry.fastapi_telemetry() == {"metrics": False, "logs": False, "auto_configure": False,
                                             "tracing": False}
    assert telemetry.cursor_factory() is None
    with telemetry.task_span("worker.monitors", x=1) as span:
        assert span is None                      # contexte vide : aucun import du SDK
    telemetry.mark_error(ValueError("x"))
    telemetry.set_attributes({"a": 1})
    telemetry.shutdown()


def test_sample_parsing():
    assert _sample("") == 0.2
    assert _sample("0.5") == 0.5
    assert _sample("7") == 1.0
    assert _sample("-1") == 0.0
    assert _sample("beaucoup") == 0.2


def test_config_when_enabled():
    cfg = telemetry.config_from(SimpleNamespace(otel_endpoint="http://127.0.0.1:4318", otel_sample=0.2,
                                                mode="saas"), "hypha-worker")
    assert cfg.traces_url == "http://127.0.0.1:4318/v1/traces"
    attrs = cfg.resource_attributes()
    assert attrs["service.name"] == "hypha-worker" and attrs["deployment.environment"] == "saas"
    assert attrs["service.version"] and attrs["host.name"]
    assert telemetry.OtelConfig("http://x:4318/v1/traces/", 1, "s", "v", "e", "h").traces_url == \
        "http://x:4318/v1/traces"


# ── avec le SDK (sauté s'il n'est pas installé) ────────────────────────────────

@pytest.fixture()
def sdk():
    pytest.importorskip("opentelemetry.sdk")
    from opentelemetry.sdk.trace import TracerProvider
    from opentelemetry.sdk.trace.export import SimpleSpanProcessor
    from opentelemetry.sdk.trace.export.in_memory_span_exporter import InMemorySpanExporter

    from hypha import otel

    def make(ratio=1.0):
        exporter = InMemorySpanExporter()
        provider = TracerProvider(sampler=otel.make_sampler(ratio))
        provider.add_span_processor(SimpleSpanProcessor(exporter))
        return provider, provider.get_tracer("essai"), exporter

    return SimpleNamespace(otel=otel, make=make)


def test_orphan_client_spans_are_dropped(sdk):
    from opentelemetry.trace import SpanKind
    provider, tracer, exporter = sdk.make()
    with tracer.start_as_current_span("GET /api/v1/hosts", kind=SpanKind.SERVER):
        with tracer.start_as_current_span("SELECT hypha", kind=SpanKind.CLIENT):
            pass
    with tracer.start_as_current_span("SELECT hypha", kind=SpanKind.CLIENT) as orphan:
        assert not orphan.is_recording()
    with tracer.start_as_current_span("POST", kind=SpanKind.CLIENT) as orphan:
        assert not orphan.is_recording()
    with tracer.start_as_current_span("hypha.worker.monitors"):
        with tracer.start_as_current_span("SELECT hypha", kind=SpanKind.CLIENT):
            pass
    with tracer.start_as_current_span("autre tâche") as other:
        assert not other.is_recording()
    names = sorted(s.name for s in exporter.get_finished_spans())
    assert names == ["GET /api/v1/hosts", "SELECT hypha", "SELECT hypha", "hypha.worker.monitors"]
    provider, tracer, exporter = sdk.make(0.0)
    with tracer.start_as_current_span("GET /", kind=SpanKind.SERVER) as span:
        assert not span.is_recording()


def test_db_span_needs_a_recording_parent(sdk):
    from opentelemetry.trace import SpanKind
    provider, tracer, exporter = sdk.make()
    with sdk.otel.db_span(tracer, "SELECT 1", "hypha", "db", 5432):
        pass
    assert exporter.get_finished_spans() == ()
    long = "SELECT " + ", ".join(f"col{i}" for i in range(200)) + " FROM logs WHERE org_id = %s"
    with tracer.start_as_current_span("GET /api/v1/logs/search", kind=SpanKind.SERVER):
        with sdk.otel.db_span(tracer, long, "hypha", "192.0.2.245", 5432):
            pass
    db = [s for s in exporter.get_finished_spans() if s.kind == SpanKind.CLIENT][0]
    assert db.name == "SELECT hypha"
    assert db.attributes["db.system"] == "postgresql" and db.attributes["db.operation"] == "SELECT"
    assert len(db.attributes["db.statement"]) == 300 and db.attributes["server.port"] == 5432
    assert db.parent is not None


def test_quiet_exporter_logs_at_most_once_per_period(sdk, caplog):
    from opentelemetry.sdk.trace.export import SpanExportResult

    class Broken:
        def export(self, spans):
            raise ConnectionError("relais absent")

        def shutdown(self):
            raise RuntimeError("déjà fermé")

        def force_flush(self, timeout_millis=0):
            raise RuntimeError("x")

    quiet = sdk.otel.QuietExporter(Broken(), "http://127.0.0.1:4318/v1/traces")
    with caplog.at_level(logging.WARNING, logger="hypha.telemetry"):
        assert quiet.export([1, 2]) is SpanExportResult.FAILURE
        assert quiet.export([3]) is SpanExportResult.FAILURE
    messages = [r.getMessage() for r in caplog.records if r.name == "hypha.telemetry"]
    assert len(messages) == 1 and "2 spans perdus" in messages[0] and "relais absent" in messages[0]
    quiet.shutdown()
    assert quiet.force_flush() is False
    fast = sdk.otel.QuietExporter(Broken(), "x", period_s=0)
    caplog.clear()
    with caplog.at_level(logging.WARNING, logger="hypha.telemetry"):
        fast.export([1])
        fast.export([1])
    assert len([r for r in caplog.records if r.name == "hypha.telemetry"]) == 2


def test_setup_state_and_task_spans(sdk, monkeypatch):
    provider, tracer, exporter = sdk.make()
    monkeypatch.setattr(sdk.otel, "build", lambda cfg: (provider, tracer, None))
    on = SimpleNamespace(otel_endpoint="http://127.0.0.1:4318", otel_sample=1.0, mode="saas")
    try:
        assert telemetry.setup(on, "hypha-worker") is True and telemetry.enabled()
        conf = telemetry.fastapi_telemetry()
        assert conf["tracing"] is True and conf["tracer_provider"] is provider
        assert conf["exclude"] is telemetry.exclude_scope and conf["metrics"] is False
        with telemetry.task_span("worker.rollup_5m", attente=None, lot=3):
            telemetry.set_attributes({"hypha.test_ok": False})
            telemetry.mark_error(RuntimeError("base perdue"))
        (span,) = exporter.get_finished_spans()
        assert span.name == "hypha.worker.rollup_5m" and dict(span.attributes) == {"lot": 3, "hypha.test_ok": False}
        assert span.status.status_code.name == "ERROR" and span.events[0].name == "exception"
    finally:
        telemetry.shutdown()
    assert not telemetry.enabled()


def test_fastapi_native_telemetry_skips_excluded_routes(sdk):
    pytest.importorskip("fastapi.telemetry")          # FastAPI ≥ 0.163 (version de production)
    from fastapi import FastAPI
    from fastapi.testclient import TestClient
    from opentelemetry.trace import SpanKind

    provider, tracer, exporter = sdk.make()
    app = FastAPI(telemetry={"tracing": True, "tracer_provider": provider, "metrics": False, "logs": False,
                             "auto_configure": False, "operation_spans": False, "exclude": telemetry.exclude_scope})

    @app.get("/api/sante")
    def sante():
        return {"ok": True}

    @app.post("/v1/traces")
    def traces():
        return {}

    @app.post("/api/v1/intake/metrics")
    def intake():
        return {}

    @app.get("/api/v1/hosts/{host_id}")
    def host(host_id: int):
        return {"id": host_id}

    client = TestClient(app)
    client.get("/api/sante")
    client.post("/v1/traces")
    client.post("/api/v1/intake/metrics")
    client.get("/api/v1/hosts/14")
    servers = [s for s in exporter.get_finished_spans() if s.kind == SpanKind.SERVER]
    assert [s.name for s in servers] == ["GET /api/v1/hosts/{host_id}"]
    assert servers[0].attributes["http.route"] == "/api/v1/hosts/{host_id}"
