"""Traces OpenTelemetry de hypha-api et de hypha-worker, FACULTATIVES.

Rien n'est actif tant que HYPHA_OTEL_ENDPOINT n'est pas posé (prod : le relais OTLP de l'agent
local, http://127.0.0.1:4318, qui ajoute la clé). Ce module n'importe pas le SDK OpenTelemetry :
seul `setup()` le charge (hypha/otel.py), si bien qu'un serveur sans ces paquets fonctionne.

Deux protections contre la boucle « chaque envoi de spans produit des spans » :
- les routes d'ingestion (/v1/*, /api/v1/intake/*) et la sonde /api/sante ne sont jamais tracées ;
- un span racine n'est gardé que s'il est un span serveur (requête HTTP) ou une tâche nommée
  « hypha.* » : une requête SQL ou un appel httpx sans parent (ingestion, sonde de santé du pool,
  notification hors tâche) ne crée aucune trace.
"""
import logging
import re
import socket
from contextlib import nullcontext
from dataclasses import dataclass

from . import __version__

log = logging.getLogger("hypha.telemetry")

# Jamais tracés : l'ingestion (sinon l'envoi des spans du serveur en produirait d'autres), la sonde
# HAProxy (toutes les 10 s sur chaque nœud), et la connexion (code et state OIDC dans l'URL).
EXCLUDED_PREFIXES = ("/v1/", "/api/v1/intake/", "/api/auth/")
EXCLUDED_EXACT = ("/v1", "/api/v1/intake", "/api/sante", "/api/sante/")
ROOT_TASK_PREFIX = "hypha."
MAX_STATEMENT = 300
EXPORT_TIMEOUT_S = 5

_SLASHES_RE = re.compile(r"/{2,}")
_SPACES_RE = re.compile(r"\s+")


def is_excluded(path: str | None) -> bool:
    """Vrai si la requête ne doit produire aucun span (chemin ASGI, sans la chaîne de requête)."""
    p = _SLASHES_RE.sub("/", path or "/")
    return p in EXCLUDED_EXACT or p.startswith(EXCLUDED_PREFIXES) or p.startswith("/api/sante?")


def exclude_scope(scope) -> bool:
    """Fonction d'exclusion passée à la télémétrie native de FastAPI (reçoit le scope ASGI)."""
    return is_excluded(scope.get("path"))


def root_allowed(is_server: bool, name: str) -> bool:
    """Un span sans parent n'ouvre une trace que s'il est un span serveur ou une tâche hypha.*."""
    return is_server or (name or "").startswith(ROOT_TASK_PREFIX)


def statement_text(query) -> str:
    """Texte de la requête SQL pour `db.statement` : espaces réduits, 300 caractères au plus.

    Seul le texte part : les valeurs des paramètres (%s) ne sont jamais lues ici.
    """
    text = _SPACES_RE.sub(" ", query if isinstance(query, str) else str(query)).strip()
    return text if len(text) <= MAX_STATEMENT else text[: MAX_STATEMENT - 1] + "…"


def statement_operation(text: str) -> str:
    word = (text.split(" ", 1)[0] if text else "").upper()
    return word if word.isalpha() else "SQL"


@dataclass(frozen=True)
class OtelConfig:
    endpoint: str
    sample: float
    service_name: str
    service_version: str
    environment: str
    host_name: str

    @property
    def traces_url(self) -> str:
        base = self.endpoint.rstrip("/")
        return base if base.endswith("/v1/traces") else base + "/v1/traces"

    def resource_attributes(self) -> dict:
        return {"service.name": self.service_name, "service.version": self.service_version,
                "deployment.environment": self.environment, "deployment.environment.name": self.environment,
                "host.name": self.host_name}


def config_from(settings, service_name: str) -> OtelConfig | None:
    if not settings.otel_endpoint:
        return None
    return OtelConfig(endpoint=settings.otel_endpoint, sample=settings.otel_sample, service_name=service_name,
                      service_version=__version__, environment=settings.mode, host_name=socket.gethostname())


class _State:
    provider = None
    tracer = None
    cursor_class = None


_state = _State()


def enabled() -> bool:
    return _state.tracer is not None


def setup(settings, service_name: str) -> bool:
    """Active les traces si HYPHA_OTEL_ENDPOINT est posé ; ne lève jamais (au pire, rien n'est tracé)."""
    cfg = config_from(settings, service_name)
    if cfg is None or enabled():
        return enabled()
    try:
        from . import otel
        _state.provider, _state.tracer, _state.cursor_class = otel.build(cfg)
    except ImportError as e:
        log.warning("HYPHA_OTEL_ENDPOINT posé mais OpenTelemetry absent (%s) : aucune trace", e)
        return False
    except Exception as e:                      # une trace ne vaut pas un service qui ne démarre pas
        log.warning("traces OpenTelemetry non activées : %s", e)
        return False
    log.info("traces OpenTelemetry de %s vers %s (échantillon %.0f %%)", service_name, cfg.traces_url,
             cfg.sample * 100)
    return True


def fastapi_telemetry() -> dict:
    """Configuration de la télémétrie native de FastAPI (≥ 0.163) : traces seules, jamais en global."""
    base = {"metrics": False, "logs": False, "auto_configure": False}
    if not enabled():
        return {**base, "tracing": False}
    return {**base, "tracing": True, "tracer_provider": _state.provider, "operation_spans": True,
            "exclude": exclude_scope}


def cursor_factory():
    """Classe de curseur psycopg tracé, ou None sans traces."""
    return _state.cursor_class


def task_span(name: str, **attributes):
    """Span racine d'une tâche du worker (`hypha.<name>`) ; sans traces, un contexte vide."""
    if not enabled():
        return nullcontext()
    from opentelemetry.trace import SpanKind
    return _state.tracer.start_as_current_span(ROOT_TASK_PREFIX + name, kind=SpanKind.INTERNAL,
                                               attributes={k: v for k, v in attributes.items() if v is not None})


def mark_error(exc: BaseException) -> None:
    """Marque en erreur le span courant d'une tâche qui avale ses exceptions (le worker ne lève jamais)."""
    if not enabled():
        return
    from opentelemetry import trace
    from opentelemetry.trace import Status, StatusCode
    span = trace.get_current_span()
    if span.is_recording():
        span.record_exception(exc)
        span.set_status(Status(StatusCode.ERROR, f"{type(exc).__name__}: {exc}"[:300]))


def set_attributes(attributes: dict) -> None:
    if not enabled():
        return
    from opentelemetry import trace
    span = trace.get_current_span()
    if span.is_recording():
        span.set_attributes({k: v for k, v in attributes.items() if v is not None})


def shutdown() -> None:
    """Vide la file des spans (au plus le délai d'export) puis ferme l'exportateur."""
    provider, _state.provider, _state.tracer, _state.cursor_class = _state.provider, None, None, None
    if provider is not None:
        try:
            provider.shutdown()
        except Exception as e:
            log.debug("arrêt des traces : %s", e)
