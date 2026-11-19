"""APM (contrat § 7), calculé à la requête sur `spans`.

Un « hit » est un span d'entrée de service : span racine, ou span `server` / `consumer`. Les
spans internes et clients d'un service ne comptent pas une seconde fois la même requête.
`error_rate` est une fraction (0 à 1), comme les moniteurs `apm`.
"""
import re
from datetime import timedelta

from fastapi import APIRouter, Depends, Query

from .. import retention
from ..auth.principal import Principal, viewer
from ..db import fetch_all
from ..errors import bad_request, not_found
from ..runtime import rt
from ..util import EPOCH_2000, bucket_floor, dt_to_ms, ms_to_dt, nice_step, now_ms
from .common import ok, time_range
from .logs import LOG_FIELDS

router = APIRouter()

ENTRY = "(parent_span_id IS NULL OR kind IN ('server', 'consumer'))"
_TRACE_RE = re.compile(r"^[0-9a-f]{16,32}$")


def _pct(col: str = "duration_ns") -> str:
    return f"percentile_cont(ARRAY[0.5, 0.95, 0.99]) WITHIN GROUP (ORDER BY {col})"


def _ms(v) -> float | None:
    return None if v is None else v / 1e6


@router.get("/apm/services")
async def services(from_: int | None = Query(None, alias="from"), to: int | None = None,
                   p: Principal = Depends(viewer)):
    start, end, rel = await retention.window(p.org_id, "spans", *time_range(from_, to))
    async with rt().db.reader() as conn:
        rows = await fetch_all(conn, f"""
            SELECT service, count(*) AS hits, count(*) FILTER (WHERE status = 'error') AS errors,
                   {_pct()} AS pct
              FROM {rel} WHERE org_id = %s AND ts >= %s AND ts < %s AND {ENTRY}
             GROUP BY service ORDER BY hits DESC LIMIT 500
        """, (p.org_id, ms_to_dt(start), ms_to_dt(end)))
    span_s = (end - start) / 1000
    out = []
    for r in rows:
        pct = r["pct"] or [None, None, None]
        out.append({"service": r["service"], "hits": r["hits"], "errors": r["errors"],
                    "error_rate": r["errors"] / r["hits"] if r["hits"] else 0.0,
                    "p50_ms": _ms(pct[0]), "p95_ms": _ms(pct[1]), "p99_ms": _ms(pct[2]),
                    "rps": r["hits"] / span_s})
    return ok(out)


@router.get("/apm/services/{service}")
async def service_detail(service: str, from_: int | None = Query(None, alias="from"), to: int | None = None,
                         p: Principal = Depends(viewer)):
    start, end, rel = await retention.window(p.org_id, "spans", *time_range(from_, to))
    base = (p.org_id, service, ms_to_dt(start), ms_to_dt(end))
    where = f"org_id = %s AND service = %s AND ts >= %s AND ts < %s AND {ENTRY}"
    step = nice_step((end - start) / 1000 / 60)
    first = bucket_floor(start, step)
    async with rt().db.reader() as conn:
        operations = await fetch_all(conn, f"""
            SELECT name, count(*) AS hits, count(*) FILTER (WHERE status = 'error') AS errors,
                   percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ns) / 1e6 AS p95_ms
              FROM {rel} WHERE {where} GROUP BY name ORDER BY hits DESC LIMIT 200
        """, base)
        resources = await fetch_all(conn, f"""
            SELECT coalesce(resource, name) AS resource, count(*) AS hits,
                   count(*) FILTER (WHERE status = 'error') AS errors,
                   percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ns) / 1e6 AS p95_ms
              FROM {rel} WHERE {where} GROUP BY 1 ORDER BY hits DESC LIMIT 200
        """, base)
        buckets = await fetch_all(conn, f"""
            SELECT date_bin(%s::interval, ts, %s::timestamptz) AS b, count(*) AS hits,
                   count(*) FILTER (WHERE status = 'error') AS errors,
                   percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ns) / 1e6 AS p95_ms
              FROM {rel} WHERE {where} GROUP BY 1
        """, (timedelta(seconds=step), EPOCH_2000) + base)
    by_ts = {dt_to_ms(r["b"]): r for r in buckets}
    hits, errors, p95 = [], [], []
    for b in range(first, end, step * 1000):
        r = by_ts.get(b)
        hits.append([b, r["hits"] if r else 0])
        errors.append([b, r["errors"] if r else 0])
        p95.append([b, r["p95_ms"] if r else None])
    return ok({"operations": operations, "resources": resources,
               "timeseries": {"hits": hits, "errors": errors, "p95_ms": p95}})


@router.get("/apm/traces")
async def traces(service: str = "", operation: str = "", status: str = "", min_ms: float | None = None,
                 from_: int | None = Query(None, alias="from"), to: int | None = None,
                 limit: int = Query(50, ge=1, le=500), p: Principal = Depends(viewer)):
    start, end, rel = await retention.window(p.org_id, "spans", *time_range(from_, to))
    where = ["s.org_id = %s", "s.ts >= %s", "s.ts < %s"]
    params: list = [p.org_id, ms_to_dt(start), ms_to_dt(end)]
    if service:
        # Filtré par service : ses spans d'entrée (la trace peut avoir commencé ailleurs).
        where += ["s.service = %s", "(s.parent_span_id IS NULL OR s.kind IN ('server', 'consumer'))"]
        params.append(service)
    else:
        where.append("s.parent_span_id IS NULL")
    if operation:
        where.append("s.name = %s")
        params.append(operation)
    if status == "error":
        where.append("s.status = 'error'")
    elif status == "ok":
        where.append("s.status <> 'error'")
    elif status:
        raise bad_request("status attendu : ok ou error")
    if min_ms is not None:
        where.append("s.duration_ns >= %s")
        params.append(int(min_ms * 1e6))
    # Bornes élargies d'une heure : les spans d'une même trace peuvent tomber hors de la plage.
    lateral = [p.org_id, ms_to_dt(start - 3600_000), ms_to_dt(end + 3600_000)]
    async with rt().db.reader() as conn:
        rows = await fetch_all(conn, f"""
            SELECT s.trace_id, s.ts, s.service, s.name, coalesce(s.resource, s.name) AS resource,
                   s.duration_ns::float8 / 1e6 AS duration_ms, t.span_count,
                   (t.error OR s.status = 'error') AS error
              FROM {rel} s
              CROSS JOIN LATERAL (
                  SELECT count(*) AS span_count, coalesce(bool_or(x.status = 'error'), false) AS error
                    FROM {rel} x WHERE x.org_id = %s AND x.trace_id = s.trace_id AND x.ts >= %s AND x.ts < %s
              ) t
             WHERE {' AND '.join(where)}
             ORDER BY s.ts DESC LIMIT %s
        """, lateral + params + [limit])
    return ok(rows)


@router.get("/apm/traces/{trace_id}")
async def trace(trace_id: str, p: Principal = Depends(viewer)):
    trace_id = trace_id.lower()
    if not _TRACE_RE.match(trace_id):
        raise bad_request("identifiant de trace invalide")
    # Conservation de la formule (§ 52) : une trace plus ancienne n'existe plus pour l'espace.
    now = now_ms()
    lo, _, srel = await retention.window(p.org_id, "spans", 0, now + 600_000)
    _, _, lrel = await retention.window(p.org_id, "logs", 0, now + 600_000)
    async with rt().db.reader() as conn:
        spans = await fetch_all(conn, f"""
            SELECT span_id, parent_span_id, service, name, coalesce(resource, name) AS resource, kind, ts,
                   duration_ns::float8 / 1e6 AS duration_ms, status, status_message, attributes, events, hostname
              FROM {srel} WHERE org_id = %s AND trace_id = %s AND ts >= %s ORDER BY ts LIMIT 5000
        """, (p.org_id, trace_id, ms_to_dt(lo)))
        if not spans:
            raise not_found("trace introuvable")
        logs = await fetch_all(conn, f"""
            SELECT {LOG_FIELDS} FROM {lrel} WHERE org_id = %s AND trace_id = %s AND ts >= %s ORDER BY ts LIMIT 1000
        """, (p.org_id, trace_id, ms_to_dt(lo)))
    return ok({"spans": spans, "logs": logs})
