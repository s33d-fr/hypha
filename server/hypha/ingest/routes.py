"""Routes d'ingestion (contrat § 3) : agent, OTLP/HTTP, RUM."""
import logging

from fastapi import APIRouter, Request
from fastapi.responses import JSONResponse, Response

from .. import quotas
from ..db import fetch_one
from ..errors import ApiError
from ..runtime import rt
from ..util import jdump, ms_to_dt, now_ms
from . import body as B
from . import extras, otlp, pipelines
from .log_filters import LogFilters
from .store import Store, extract_key

log = logging.getLogger("hypha.ingest")
router = APIRouter()

SPAN_COLUMNS = ("org_id", "ts", "trace_id", "span_id", "parent_span_id", "service", "name", "resource",
                "kind", "duration_ns", "status", "status_message", "host_id", "hostname", "attributes",
                "resource_attributes", "events")


def span_bytes(rows: list[tuple]) -> int:
    """Taille des spans reçus (§ 52) : textes, identifiants et JSON tels qu'écrits en base, en caractères."""
    total = 0
    for r in rows:
        total += sum(len(r[i]) for i in (2, 3, 4, 5, 6, 7, 8, 10, 11, 13, 14, 15, 16) if isinstance(r[i], str)) + 24
    return total


def store() -> Store:
    return Store(rt())


def accepted(n: int, dropped: int) -> JSONResponse:
    return JSONResponse({"status": "ok", "accepted": n, "dropped": dropped}, status_code=202)


def client_ip(request: Request) -> str | None:
    return request.client.host if request.client else None


async def read_raw(request: Request, limit: int = B.MAX_BODY) -> bytes:
    """Lit le corps brut en coupant au-delà de la limite (corps compressé compris)."""
    length = request.headers.get("content-length", "")
    if length.isdigit() and int(length) > limit:
        raise B.BodyError(413, "payload_too_large", f"corps de plus de {limit} octets")
    chunks, size = [], 0
    async for chunk in request.stream():
        size += len(chunk)
        if size > limit:
            raise B.BodyError(413, "payload_too_large", f"corps de plus de {limit} octets")
        chunks.append(chunk)
    return b"".join(chunks)


async def prologue(request: Request):
    """Clé (401/403 avant tout décodage), puis corps décompressé, puis objet `agent`."""
    st = store()
    key = await st.auth_ingest(extract_key(request.headers))
    raw = await read_raw(request)
    data = B.decompress(raw, request.headers.get("content-encoding"))
    body = B.parse_json_object(data)
    agent = B.parse_agent(body)
    return st, key, body, agent


# ── agent ──────────────────────────────────────────────────────────────────────

@router.post("/api/v1/intake/host")
async def intake_host(request: Request):
    st, key, body, agent = await prologue(request)
    h = B.parse_host(body, agent)
    async with rt().db.writer(timeout=3) as conn:
        host_id = await st.ensure_host(conn, key.org_id, agent, client_ip(request))
        await conn.execute("""
            UPDATE hosts SET hostname = %s, os = %s, platform = %s, platform_version = %s, kernel = %s,
                   arch = %s, cpu_cores = %s, cpu_model = %s, mem_total = %s, ip_addresses = %s::jsonb,
                   tags = %s::jsonb, agent_version = %s, boot_time = %s, meta = %s::jsonb, last_seen = now()
             WHERE id = %s AND org_id = %s
        """, (h["hostname"], h["os"], h["platform"], h["platform_version"], h["kernel"], h["arch"],
              h["cpu_cores"], h["cpu_model"], h["mem_total"], h["ip_addresses"], h["tags"],
              h["agent_version"], h["boot_time"], h["meta"], host_id, key.org_id))
        if h["kind"] != "agent":
            # § 14 : aucune ligne `agents` pour un équipement ; l'agent qui l'interroge en est le lien.
            await conn.execute("""
                UPDATE hosts SET kind = %s, device = %s::jsonb,
                       via_host_id = COALESCE((SELECT id FROM hosts WHERE org_id = %s AND agent_id = %s::uuid),
                                              via_host_id)
                 WHERE id = %s AND org_id = %s
            """, (h["kind"], h["device"], key.org_id, agent.via, host_id, key.org_id))
        else:
            await conn.execute("UPDATE agents SET status = %s::jsonb, version = COALESCE(%s, version), "
                               "capabilities = COALESCE(%s::jsonb, capabilities), "
                               "update_status = COALESCE(%s::jsonb, update_status), "
                               "config_hash = COALESCE(%s, config_hash), "
                               "last_seen = now() WHERE id = %s AND org_id = %s",
                               (h["status"], h["agent_version"], h["capabilities"], h["update_status"],
                                h.get("config_hash"), agent.id, key.org_id))
        await st.touch_key(conn, key)
    if h["hostname"] != agent.hostname:
        st.forget_host(key.org_id, agent.id)
    return accepted(1, 0)


@router.post("/api/v1/intake/metrics")
async def intake_metrics(request: Request):
    st, key, body, agent = await prologue(request)
    batches, dropped = B.parse_series(B.require_list(body, "series"), B.Window.around(), agent.hostname)
    written = 0
    async with rt().db.writer(timeout=3) as conn:
        host_id = await st.ensure_host(conn, key.org_id, agent, client_ip(request))
        if batches:
            ids = await st.resolve_series(conn, key.org_id, host_id,
                                          [(b.name, b.type, b.unit, b.tags) for b in batches])
            rows = [(sid, ts, v) for sid, b in zip(ids, batches) for ts, v in b.points]
            written, d = await st.copy(conn, "metric_points", ("series_id", "ts", "value"), rows, 1)
            dropped += d
        extras.state_of(rt()).usage.add(key.org_id, metric_points=written)
        await extras.flush_due(conn, rt(), st)
        await st.touch_key(conn, key)
    return accepted(written, dropped)


async def write_logs(conn, st: Store, org_id: int, rows: list[tuple]) -> tuple[int, int, int]:
    """Filtres (§ 11), découpage (§ 12), masquage et erreurs (§§ 23, 25), COPY ; rend (écrites,
    écartées hors partition, jetées par un filtre)."""
    runtime = rt()
    filters = LogFilters(runtime)
    rows, excluded = await filters.apply(conn, org_id, rows)
    rows = pipelines.process_rows(rows)             # § 12 : après les filtres du § 11
    batch = await extras.prepare_logs(conn, runtime, org_id, rows)
    written, d = await st.copy(conn, "logs", B.LOG_COLUMNS, batch.rows, 1)
    if batch.id_rows:
        w2, d2 = await st.copy(conn, "logs", extras.LOG_ID_COLUMNS, batch.id_rows, 1)
        written, d = written + w2, d + d2
    await extras.finish_logs(conn, runtime, st, org_id, batch, written)
    await filters.flush_if_due(conn, st)
    return written, d, excluded


@router.post("/api/v1/intake/logs")
async def intake_logs(request: Request):
    st, key, body, agent = await prologue(request)
    items = B.require_list(body, "logs")
    async with rt().db.writer(timeout=3) as conn:
        host_id = await st.ensure_host(conn, key.org_id, agent, client_ip(request))
        rows, dropped = B.parse_logs(items, B.Window.around(), key.org_id, host_id, agent.hostname)
        await quotas.admit_ingest(conn, key.org_id, "logs", len(rows), extras.state_of(rt()).usage)
        written, d, excluded = await write_logs(conn, st, key.org_id, rows)
        await st.touch_key(conn, key)
    # Les lignes jetées par un filtre comptent dans `dropped` (contrat § 11) : l'agent ne les renvoie pas.
    return accepted(written, dropped + d + excluded)


@router.post("/api/v1/intake/checks")
async def intake_checks(request: Request):
    st, key, body, agent = await prologue(request)
    items = B.require_list(body, "checks")
    async with rt().db.writer(timeout=3) as conn:
        host_id = await st.ensure_host(conn, key.org_id, agent, client_ip(request))
        rows, dropped = B.parse_checks(items, B.Window.around(), key.org_id, host_id)
        written, d = await st.copy(conn, "check_runs", B.CHECK_COLUMNS, rows, 2)
        extras.state_of(rt()).usage.add(key.org_id, checks=written)
        await extras.flush_due(conn, rt(), st)
        await st.touch_key(conn, key)
    return accepted(written, dropped + d)


@router.post("/api/v1/intake/events")
async def intake_events(request: Request):
    st, key, body, agent = await prologue(request)
    items = B.require_list(body, "events")
    async with rt().db.writer(timeout=3) as conn:
        host_id = await st.ensure_host(conn, key.org_id, agent, client_ip(request))
        rows, dropped = B.parse_events(items, B.Window.around(), key.org_id, host_id)
        written, d = await st.copy(conn, "events", B.EVENT_COLUMNS, rows, 1)
        await st.touch_key(conn, key)
    return accepted(written, dropped + d)


async def _snapshot(request: Request, list_key: str, table: str, columns, parser):
    st, key, body, agent = await prologue(request)
    items = B.require_list(body, list_key)
    ts = B.snapshot_ts(body, B.Window.around())
    async with rt().db.writer(timeout=3) as conn:
        host_id = await st.ensure_host(conn, key.org_id, agent, client_ip(request))
        if ts is None:
            written, dropped = 0, len(items)
        else:
            rows, dropped = parser(items, ts, key.org_id, host_id)
            written, d = await st.copy(conn, table, columns, rows, 2)
            dropped += d
        await st.touch_key(conn, key)
    return accepted(written, dropped)


@router.post("/api/v1/intake/processes")
async def intake_processes(request: Request):
    return await _snapshot(request, "processes", "process_snapshots", B.PROCESS_COLUMNS, B.parse_processes)


@router.post("/api/v1/intake/connections")
async def intake_connections(request: Request):
    return await _snapshot(request, "connections", "net_connections", B.CONNECTION_COLUMNS, B.parse_connections)


@router.post("/api/v1/intake/containers")
async def intake_containers(request: Request):
    return await _snapshot(request, "containers", "container_snapshots", B.CONTAINER_COLUMNS, B.parse_containers)


# ── OTLP/HTTP ──────────────────────────────────────────────────────────────────

def _otlp_format(request: Request) -> bool:
    ctype = request.headers.get("content-type", "").split(";")[0].strip().lower()
    if ctype in ("application/x-protobuf", "application/protobuf"):
        return True
    if ctype == "application/json":
        return False
    raise ApiError(415, "unsupported_media_type", "OTLP attend application/x-protobuf ou application/json")


async def _otlp(request: Request, signal: str) -> Response:
    st = store()
    key = await st.auth_ingest(extract_key(request.headers, allow_bearer=True))
    protobuf = _otlp_format(request)
    data = B.decompress(await read_raw(request), request.headers.get("content-encoding"))
    try:
        doc = otlp.decode(signal, data, protobuf)
    except otlp.OtlpError as e:
        raise ApiError(400, "invalid_otlp", str(e))
    window = B.Window.around()
    org = key.org_id
    async with rt().db.writer(timeout=3) as conn:
        if signal == "traces":
            spans, dropped = otlp.convert_traces(doc, window)
            await quotas.admit_ingest(conn, org, "spans", len(spans), extras.state_of(rt()).usage)
            host_ids = [await st.host_by_name(conn, org, s["hostname"]) for s in spans]
            occurrences = extras.span_occurrences(spans, host_ids)      # § 25, avant la sérialisation
            rows = []
            for s, host_id in zip(spans, host_ids):
                rows.append((org, ms_to_dt(s["ts_ms"]), s["trace_id"], s["span_id"], s["parent_span_id"],
                             s["service"], s["name"], s["resource"], s["kind"], s["duration_ns"], s["status"],
                             s["status_message"], host_id, s["hostname"], jdump(s["attributes"]),
                             jdump(s["resource_attributes"]), jdump(s["events"])))
            written, d = await st.copy(conn, "spans", SPAN_COLUMNS, rows, 1)
            await extras.record_occurrences(conn, rt(), org, occurrences)
            nbytes = span_bytes(rows)
            if d:
                nbytes = nbytes * written // max(1, written + d)
            extras.state_of(rt()).usage.add(org, spans=written, span_bytes=nbytes)
            await extras.flush_due(conn, rt(), st)
        elif signal == "logs":
            logs, dropped = otlp.convert_logs(doc, window, now_ms())
            await quotas.admit_ingest(conn, org, "logs", len(logs), extras.state_of(rt()).usage)
            rows = []
            for lg in logs:
                host_id = await st.host_by_name(conn, org, lg["hostname"])
                rows.append(B.log_row(org, host_id, lg["hostname"], lg["ts_ms"], lg["message"], lg["status"],
                                      lg["service"], lg["source"], lg["attributes"], lg["tags"],
                                      lg["trace_id"], lg["span_id"]))
            # Lignes filtrées volontairement : non signalées en `rejected` OTLP, sans quoi chaque
            # application instrumentée journaliserait un avertissement à chaque lot.
            _, d, _ = await write_logs(conn, st, org, rows)
        else:
            points, dropped = otlp.convert_metrics(doc, window, rt().otlp_state)
            by_host: dict = {}
            for p in points:
                by_host.setdefault(p.hostname, []).append(p)
            rows = []
            for hostname, group in by_host.items():
                host_id = await st.host_by_name(conn, org, hostname)
                ids = await st.resolve_series(conn, org, host_id, [(p.name, p.type, p.unit, p.tags) for p in group])
                rows += [(sid, ms_to_dt(p.ts_ms), p.value) for sid, p in zip(ids, group)]
            written, d = await st.copy(conn, "metric_points", ("series_id", "ts", "value"), rows, 1)
            extras.state_of(rt()).usage.add(org, metric_points=written)
            await extras.flush_due(conn, rt(), st)
        await st.touch_key(conn, key)
    return Response(otlp.encode_response(signal, protobuf, dropped + d),
                    media_type="application/x-protobuf" if protobuf else "application/json")


@router.post("/v1/traces")
async def otlp_traces(request: Request):
    return await _otlp(request, "traces")


@router.post("/v1/metrics")
async def otlp_metrics(request: Request):
    return await _otlp(request, "metrics")


@router.post("/v1/logs")
async def otlp_logs(request: Request):
    return await _otlp(request, "logs")


# ── RUM (navigateur, CORS ouvert) ──────────────────────────────────────────────

CORS_HEADERS = {
    "Access-Control-Allow-Origin": "*",
    "Access-Control-Allow-Methods": "POST, OPTIONS",
    "Access-Control-Allow-Headers": "Content-Type, Content-Encoding",
    "Access-Control-Max-Age": "86400",
}


@router.options("/api/v1/intake/rum")
async def rum_preflight():
    return Response(status_code=204, headers=CORS_HEADERS)


@router.post("/api/v1/intake/rum")
async def intake_rum(request: Request, token: str = ""):
    # Les erreurs portent aussi les en-têtes CORS : sans eux le navigateur masque la réponse.
    try:
        return await _rum(request, token)
    except ApiError as e:
        return JSONResponse(e.body(), status_code=e.status, headers=CORS_HEADERS)
    except B.BodyError as e:
        return JSONResponse({"error": e.code, "detail": e.detail}, status_code=e.status, headers=CORS_HEADERS)


async def _rum(request: Request, token: str) -> JSONResponse:
    runtime = rt()
    token = token.strip()
    if not token or len(token) > 100:
        raise ApiError(401, "invalid_token", "jeton client RUM absent")
    if not runtime.rum_limiter.allow((token, client_ip(request))):
        raise ApiError(429, "rate_limited", "trop d'envois depuis ce navigateur")
    app = runtime.rum_apps.get(token, False)
    if app is False:
        async with runtime.db.writer(timeout=3) as conn:
            app = await fetch_one(conn, "SELECT id, org_id, name FROM rum_apps WHERE client_token = %s", (token,))
        runtime.rum_apps.set(token, app)
    if not app:
        raise ApiError(401, "invalid_token", "jeton client RUM inconnu")
    data = B.decompress(await read_raw(request), request.headers.get("content-encoding"))
    body = B.parse_json_object(data)
    country = request.headers.get("x-country-code") or request.headers.get("cf-ipcountry")
    rows, dropped = B.parse_rum(body, B.Window.around(), app["org_id"], app["id"],
                                request.headers.get("user-agent"), country)
    rows, occurrences = extras.rum_occurrences(rows, app.get("name") or f"rum-{app['id']}")
    async with runtime.db.writer(timeout=3) as conn:
        st = store()
        written, d = await st.copy(conn, "rum_events", B.RUM_COLUMNS, rows, 2)
        await extras.record_occurrences(conn, runtime, app["org_id"], occurrences)
        extras.state_of(runtime).usage.add(app["org_id"], rum_events=written)
        await extras.flush_due(conn, runtime, st)
    return JSONResponse({"status": "ok", "accepted": written, "dropped": dropped + d},
                        status_code=202, headers=CORS_HEADERS)

