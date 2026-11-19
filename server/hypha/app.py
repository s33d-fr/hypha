"""Application FastAPI : routes, erreurs au format du contrat, cycle de vie (pools, tâches de fond)."""
import asyncio
import inspect
import logging
from contextlib import asynccontextmanager

import fastapi
import psycopg
from fastapi import FastAPI, Request
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse
from psycopg_pool import PoolTimeout
from starlette.exceptions import HTTPException as StarletteHTTPException

from . import __version__, telemetry
from .api import (apm, dashboards, events, explain, health, infra, log_filters, logs, metrics, monitors, rum,
                  settings_routes, slos, synthetics)
from .api import actions, audit_routes, downtimes, incidents, problems, reports, status_pages
from .api import (certificates, dbm, error_tracking, fleet, inventory, logs_extra, metadata, network,
                  public_dashboards, services)
from .api import billing
from .ingest import agent_routes
from .auth import accounts, routes as auth_routes
from .auth.oidc import OidcClient
from .auth.passwords import hash_password
from .config import get_settings
from .db import Database
from .errors import ApiError, QueryError
from .ingest import routes as ingest_routes
from .ingest.body import BodyError
from .logsetup import setup_logging
from .runtime import Runtime, rt, set_runtime

log = logging.getLogger("hypha.api")


async def _bootstrap_selfhosted(runtime: Runtime) -> None:
    """Organisation et compte d'administration au premier démarrage ; attend la base si besoin."""
    s = runtime.settings
    pw_hash = await asyncio.to_thread(hash_password, s.admin_password) if s.admin_password else None
    delay = 5
    while True:
        try:
            async with runtime.db.writer() as conn:
                await accounts.bootstrap_selfhosted(conn, s.org_name, s.admin_login, pw_hash)
            return
        except (psycopg.OperationalError, PoolTimeout):
            log.warning("base indisponible : création du compte initial reportée de %s s", delay)
        except psycopg.Error as e:
            log.error("création du compte initial impossible : %s", e)
            return
        await asyncio.sleep(delay)
        delay = min(delay * 2, 60)


@asynccontextmanager
async def lifespan(app: FastAPI):
    settings = get_settings()
    setup_logging(settings.log_level)
    if not settings.db_write:
        raise SystemExit("HYPHA_DB_WRITE absent")
    db = Database(settings, "hypha-api")
    runtime = Runtime(settings=settings, db=db)
    if settings.oidc_enabled:
        runtime.oidc = OidcClient(settings.oidc_issuer, settings.oidc_client_id, settings.oidc_client_secret,
                                  settings.public_url + "/api/auth/rappel")
    if settings.oidc_enabled and not settings.secret:
        log.error("HYPHA_SECRET absent : la connexion OIDC échouera (l'état ne peut être signé)")
    set_runtime(runtime)
    await db.open()
    tasks = [asyncio.create_task(db.health_loop(), name="db-health")]
    if not settings.saas:
        tasks.append(asyncio.create_task(_bootstrap_selfhosted(runtime), name="bootstrap"))
    log.info("hypha-api %s démarré (mode %s)", __version__, settings.mode)
    try:
        yield
    finally:
        for t in tasks:
            t.cancel()
        await asyncio.gather(*tasks, return_exceptions=True)
        await db.close()
        set_runtime(None)
        await asyncio.to_thread(telemetry.shutdown)     # vide la file des spans, 5 s au plus


def _error(status: int, code: str, detail: str, headers=None) -> JSONResponse:
    return JSONResponse({"error": code, "detail": detail}, status_code=status, headers=headers)


async def _api_error(request: Request, exc: ApiError):
    return JSONResponse(exc.body(), status_code=exc.status)


async def _body_error(request: Request, exc: BodyError):
    return _error(exc.status, exc.code, exc.detail)


async def _query_error(request: Request, exc: QueryError):
    return _error(400, "invalid_query", str(exc))


async def _validation_error(request: Request, exc: RequestValidationError):
    parts = []
    for err in exc.errors()[:5]:
        where = ".".join(str(x) for x in err.get("loc", ()) if x not in ("body", "query", "path"))
        parts.append(f"{where} : {err.get('msg')}" if where else str(err.get("msg")))
    return _error(400, "invalid_request", " ; ".join(parts) or "requête invalide")


async def _http_error(request: Request, exc: StarletteHTTPException):
    code = {404: "not_found", 405: "method_not_allowed"}.get(exc.status_code, "http_error")
    return _error(exc.status_code, code, str(exc.detail))


async def _db_down(request: Request, exc: Exception):
    rt().db.mark(False)
    log.warning("base indisponible (%s %s) : %s", request.method, request.url.path, exc)
    return _error(503, "db_unavailable", "base de données indisponible, réessayez plus tard",
                  headers={"Retry-After": "10"})


async def _db_timeout(request: Request, exc: Exception):
    return _error(503, "query_timeout", "requête trop longue : réduisez la période ou ajoutez des filtres")


async def _db_rejected(request: Request, exc: Exception):
    log.warning("donnée refusée par la base (%s) : %s", request.url.path, exc)
    return _error(400, "rejected", f"donnée refusée par la base : {str(exc).splitlines()[0]}")


async def _internal(request: Request, exc: Exception):
    log.exception("erreur interne sur %s %s", request.method, request.url.path, exc_info=exc)
    return _error(500, "internal", "erreur interne du serveur")


# La classe la plus précise gagne : QueryCanceled (délai dépassé) avant OperationalError (base perdue).
EXCEPTION_HANDLERS = {
    ApiError: _api_error,
    BodyError: _body_error,
    QueryError: _query_error,
    RequestValidationError: _validation_error,
    StarletteHTTPException: _http_error,
    PoolTimeout: _db_down,
    psycopg.OperationalError: _db_down,
    psycopg.errors.QueryCanceled: _db_timeout,
    psycopg.DataError: _db_rejected,
    psycopg.IntegrityError: _db_rejected,
    Exception: _internal,
}


def create_app() -> FastAPI:
    # Appelée dans chaque processus uvicorn : journalisation et traces sont posées avant le démarrage
    # (le fournisseur de traces doit exister avant FastAPI() et avant les pools de connexions).
    settings = get_settings()
    setup_logging(settings.log_level)
    telemetry.setup(settings, "hypha-api")
    if telemetry.enabled() and "telemetry" not in inspect.signature(FastAPI.__init__).parameters:
        log.warning("FastAPI %s sans télémétrie native : spans SQL et httpx seulement, sans span serveur",
                    fastapi.__version__)
    app = FastAPI(title="HYPHA", version=__version__, lifespan=lifespan, exception_handlers=EXCEPTION_HANDLERS,
                  docs_url=None, redoc_url=None, openapi_url=None, telemetry=telemetry.fastapi_telemetry())
    app.include_router(health.router)
    app.include_router(ingest_routes.router)
    app.include_router(auth_routes.router)
    for module in (infra, metrics, logs, log_filters, apm, events, monitors, slos, dashboards, synthetics, rum,
                   settings_routes, explain):
        app.include_router(module.router, prefix="/api/v1")
    # Alertes, incidents, exploitation (contrat §§ 16 à 24, 28).
    for module in (problems, downtimes, incidents, status_pages, reports, audit_routes, actions):
        app.include_router(module.router, prefix="/api/v1")
    app.include_router(status_pages.public_router)
    # Collecte, données, plateforme (contrat §§ 14, 15, 23, 25 à 27, 29, 30).
    app.include_router(agent_routes.router)
    for module in (inventory, fleet, logs_extra, error_tracking, services, network, dbm, metadata, certificates):
        app.include_router(module.router, prefix="/api/v1")
    app.include_router(public_dashboards.router)
    # Formules et facturation (contrat § 52) ; le webhook Stripe est public et signé.
    app.include_router(billing.router, prefix="/api/v1")
    app.include_router(billing.public_router)
    return app
