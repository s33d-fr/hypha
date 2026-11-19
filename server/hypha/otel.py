"""Câblage du SDK OpenTelemetry (chargé seulement si HYPHA_OTEL_ENDPOINT est posé, voir telemetry.py).

- Fournisseur de traces propre au processus, jamais posé en global : la télémétrie native de
  FastAPI ne s'active ainsi que par la configuration passée à FastAPI(telemetry=…).
- Échantillonnage ParentBased(TraceIdRatioBased), avec un filtre sur les racines (telemetry.root_allowed).
- Export par lots, OTLP/HTTP protobuf, 5 s au plus, dans le fil du BatchSpanProcessor : l'API
  n'attend jamais l'exportateur. Ses erreurs sont avalées et journalisées une fois par minute.
- psycopg : curseur tracé maison plutôt que l'instrumentation contrib. Il ne crée de span que sous
  un span parent qui enregistre, écrit `db.statement` tronqué sans valeur de paramètre, et couvre
  COPY ; l'instrumentation contrib ouvrirait des traces racines pour chaque requête d'ingestion.
- httpx : instrumentation contrib (appels au modèle, sondes synthétiques, webhooks, OIDC).
"""
import logging
import time
from contextlib import asynccontextmanager, nullcontext

from opentelemetry import trace
from opentelemetry.sdk.resources import Resource
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor, SpanExporter, SpanExportResult
from opentelemetry.sdk.trace.sampling import Decision, ParentBased, Sampler, SamplingResult, TraceIdRatioBased
from opentelemetry.trace import SpanKind

from . import telemetry

log = logging.getLogger("hypha.telemetry")

# Bibliothèques dont les messages d'échec d'export sont remplacés par le nôtre (une fois par minute).
QUIET_LOGGERS = ("opentelemetry.exporter.otlp.proto.http", "opentelemetry.exporter.otlp.common",
                 "opentelemetry.exporter.http_transport")


class RootFilterSampler(Sampler):
    """Délègue la décision pour les racines admises, écarte les autres (requête SQL ou appel httpx orphelin)."""

    def __init__(self, delegate: Sampler):
        self._delegate = delegate

    def should_sample(self, parent_context, trace_id, name, kind=None, attributes=None, links=None,
                      trace_state=None) -> SamplingResult:
        if not telemetry.root_allowed(kind == SpanKind.SERVER, name):
            return SamplingResult(Decision.DROP)
        return self._delegate.should_sample(parent_context, trace_id, name, kind, attributes, links, trace_state)

    def get_description(self) -> str:
        return f"HyphaRootFilter{{{self._delegate.get_description()}}}"


def make_sampler(ratio: float) -> Sampler:
    return ParentBased(root=RootFilterSampler(TraceIdRatioBased(ratio)))


class QuietExporter(SpanExporter):
    """Enveloppe l'exportateur : aucune exception ne remonte, un échec est journalisé au plus une fois
    par minute (relais absent, réseau coupé), avec le nombre de spans perdus depuis le dernier message."""

    def __init__(self, inner: SpanExporter, target: str, period_s: float = 60.0):
        self._inner = inner
        self._target = target
        self._period_s = period_s
        self._last_log = float("-inf")
        self._lost = 0

    def export(self, spans) -> SpanExportResult:
        try:
            result = self._inner.export(spans)
            error = None
        except Exception as e:
            result, error = SpanExportResult.FAILURE, e
        if result is not SpanExportResult.SUCCESS:
            self._lost += len(spans)
            now = time.monotonic()
            if now - self._last_log >= self._period_s:
                log.warning("export des traces vers %s en échec (%s spans perdus%s)", self._target, self._lost,
                            f" : {error}" if error else "")
                self._last_log, self._lost = now, 0
        return result

    def shutdown(self) -> None:
        try:
            self._inner.shutdown()
        except Exception:
            pass

    def force_flush(self, timeout_millis: int = 30000) -> bool:
        try:
            return self._inner.force_flush(timeout_millis)
        except Exception:
            return False


def db_span(tracer, query, dbname: str | None, host: str | None, port: int | None):
    """Span client d'une requête SQL, seulement sous un parent qui enregistre ; sinon un contexte vide."""
    if not trace.get_current_span().is_recording():
        return nullcontext()
    text = telemetry.statement_text(query)
    op = telemetry.statement_operation(text)
    attributes = {"db.system": "postgresql", "db.statement": text, "db.operation": op}
    if dbname:
        attributes["db.name"] = dbname
    if host:
        attributes["server.address"] = host
    if port:
        attributes["server.port"] = int(port)
    return tracer.start_as_current_span(f"{op} {dbname}" if dbname else op, kind=SpanKind.CLIENT,
                                        attributes=attributes)


def make_cursor_class(tracer):
    import psycopg

    def _span(cur, query):
        if not trace.get_current_span().is_recording():
            return nullcontext()
        info = cur.connection.info
        if not isinstance(query, str):
            try:
                query = query.as_string(cur.connection)     # psycopg.sql.Composed
            except Exception:
                query = type(query).__name__
        return db_span(tracer, query, info.dbname, info.host, info.port)

    class TracedAsyncCursor(psycopg.AsyncCursor):
        async def execute(self, query, params=None, **kwargs):
            with _span(self, query):
                return await super().execute(query, params, **kwargs)

        async def executemany(self, query, params_seq, **kwargs):
            with _span(self, query):
                return await super().executemany(query, params_seq, **kwargs)

        @asynccontextmanager
        async def copy(self, statement, params=None, **kwargs):
            with _span(self, statement):
                async with super().copy(statement, params, **kwargs) as cp:
                    yield cp

    return TracedAsyncCursor


def build(cfg: "telemetry.OtelConfig"):
    """Rend (fournisseur, traceur, classe de curseur psycopg tracé)."""
    from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter

    for name in QUIET_LOGGERS:
        logging.getLogger(name).setLevel(logging.CRITICAL)
    provider = TracerProvider(resource=Resource.create(cfg.resource_attributes()), sampler=make_sampler(cfg.sample))
    exporter = QuietExporter(OTLPSpanExporter(endpoint=cfg.traces_url, timeout=telemetry.EXPORT_TIMEOUT_S),
                             cfg.traces_url)
    provider.add_span_processor(BatchSpanProcessor(exporter, max_queue_size=4096, schedule_delay_millis=5000,
                                                   max_export_batch_size=512,
                                                   export_timeout_millis=telemetry.EXPORT_TIMEOUT_S * 1000))
    tracer = provider.get_tracer("hypha", cfg.service_version)
    try:
        from opentelemetry.instrumentation.httpx import HTTPXClientInstrumentor
        HTTPXClientInstrumentor().instrument(tracer_provider=provider)
    except ImportError as e:
        log.warning("instrumentation httpx absente (%s) : appels sortants non tracés", e)
    try:
        cursor_class = make_cursor_class(tracer)
    except ImportError:
        cursor_class = None
    return provider, tracer, cursor_class
