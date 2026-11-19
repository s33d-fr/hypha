"""Bases de données (contrat § 30) : requêtes les plus coûteuses tirées de `pg_stat_statements`.

Chaque échantillon de l'agent porte les écarts depuis le précédent (appels, durée totale, lignes,
blocs lus) : une somme sur la période donne donc l'activité de la période.
"""
from datetime import timedelta

from fastapi import APIRouter, Depends, Query

from ..auth.principal import Principal, viewer
from ..db import fetch_all, fetch_one
from ..errors import bad_request, not_found
from ..runtime import rt
from ..util import EPOCH_2000, bucket_floor, dt_to_ms, ms_to_dt, nice_step
from .common import ok, time_range

router = APIRouter()

SORTS = {"total_ms": "total_ms", "calls": "calls", "mean_ms": "mean_ms", "rows": "rows"}


def _filters(p: Principal, start: int, end: int, host_id: int | None, db: str, instance: str = "") -> tuple[str, list]:
    where = ["q.org_id = %s", "q.ts >= %s", "q.ts < %s"]
    params: list = [p.org_id, ms_to_dt(start), ms_to_dt(end)]
    if host_id is not None:
        where.append("q.host_id = %s")
        params.append(host_id)
    if db:
        where.append("q.db = %s")
        params.append(db)
    if instance:
        where.append("q.instance = %s")
        params.append(instance)
    return " AND ".join(where), params


@router.get("/dbm/queries")
async def top_queries(from_: int | None = Query(None, alias="from"), to: int | None = None, host_id: int | None = None,
                      db: str = "", instance: str = "", sort: str = "total_ms", limit: int = Query(100, ge=1, le=1000),
                      p: Principal = Depends(viewer)):
    start, end = time_range(from_, to)
    if sort not in SORTS:
        raise bad_request(f"sort attendu : {'|'.join(SORTS)}")
    where, params = _filters(p, start, end, host_id, db, instance)
    async with rt().db.reader() as conn:
        rows = await fetch_all(conn, f"""
            SELECT q.query_id, (array_agg(q.query ORDER BY q.ts DESC))[1] AS query, q.db, q.instance, q.engine,
                   array_remove(array_agg(DISTINCT h.hostname), NULL) AS hosts,
                   sum(q.calls)::bigint AS calls, sum(q.total_ms)::float8 AS total_ms,
                   (sum(q.total_ms) / NULLIF(sum(q.calls), 0))::float8 AS mean_ms, sum(q.rows)::bigint AS rows,
                   (sum(q.shared_blks_hit)::float8 / NULLIF(sum(q.shared_blks_hit) + sum(q.shared_blks_read), 0))
                       AS hit_ratio
              FROM dbm_queries q LEFT JOIN hosts h ON h.id = q.host_id AND h.org_id = q.org_id
             WHERE {where}
             GROUP BY q.query_id, q.db, q.instance, q.engine
             ORDER BY {SORTS[sort]} DESC NULLS LAST LIMIT %s
        """, params + [limit])
    return ok(rows)


@router.get("/dbm/instances")
async def instances(from_: int | None = Query(None, alias="from"), to: int | None = None,
                    p: Principal = Depends(viewer)):
    start, end = time_range(from_, to)
    where, params = _filters(p, start, end, None, "")
    async with rt().db.reader() as conn:
        rows = await fetch_all(conn, f"""
            SELECT q.engine, q.instance, q.db, array_remove(array_agg(DISTINCT h.hostname), NULL) AS hosts,
                   count(DISTINCT q.query_id) AS queries, sum(q.calls)::bigint AS calls,
                   sum(q.total_ms)::float8 AS total_ms, max(q.ts) AS last_seen
              FROM dbm_queries q LEFT JOIN hosts h ON h.id = q.host_id AND h.org_id = q.org_id
             WHERE {where}
             GROUP BY q.engine, q.instance, q.db ORDER BY total_ms DESC NULLS LAST LIMIT 500
        """, params)
    return ok(rows)


@router.get("/dbm/queries/{query_id}")
async def query_detail(query_id: str, from_: int | None = Query(None, alias="from"), to: int | None = None,
                       host_id: int | None = None, db: str = "", buckets: int = Query(60, ge=1, le=1000),
                       p: Principal = Depends(viewer)):
    start, end = time_range(from_, to)
    if not query_id or len(query_id) > 100:
        raise bad_request("query_id invalide")
    where, params = _filters(p, start, end, host_id, db)
    where += " AND q.query_id = %s"
    params.append(query_id)
    step = nice_step((end - start) / 1000 / buckets)
    first = bucket_floor(start, step)
    async with rt().db.reader() as conn:
        head = await fetch_one(conn, f"""
            SELECT (array_agg(q.query ORDER BY q.ts DESC))[1] AS query, (array_agg(q.db ORDER BY q.ts DESC))[1] AS db,
                   (array_agg(q.instance ORDER BY q.ts DESC))[1] AS instance, sum(q.calls)::bigint AS calls,
                   sum(q.total_ms)::float8 AS total_ms, count(*) AS samples
              FROM dbm_queries q WHERE {where}
        """, params)
        if head is None or not head["samples"]:
            raise not_found("requête inconnue sur cette période")
        rows = await fetch_all(conn, f"""
            SELECT date_bin(%s::interval, q.ts, %s::timestamptz) AS b, sum(q.calls)::bigint AS calls,
                   sum(q.total_ms)::float8 AS total_ms, sum(q.rows)::bigint AS rows
              FROM dbm_queries q WHERE {where} GROUP BY 1
        """, [timedelta(seconds=step), EPOCH_2000] + params)
    by_ts = {dt_to_ms(r["b"]): r for r in rows}
    calls, mean, total, nrows = [], [], [], []
    for b in range(first, end, step * 1000):
        r = by_ts.get(b)
        calls.append([b, r["calls"] if r else 0])
        total.append([b, r["total_ms"] if r else 0.0])
        nrows.append([b, r["rows"] if r else 0])
        mean.append([b, (r["total_ms"] / r["calls"]) if r and r["calls"] else None])
    query = {"query_id": query_id, "query": head["query"], "db": head["db"], "instance": head["instance"],
             "calls": head["calls"], "total_ms": head["total_ms"],
             "mean_ms": head["total_ms"] / head["calls"] if head["calls"] else None}
    return ok({"query": query, "interval_s": step,
               "series": {"calls": calls, "mean_ms": mean, "total_ms": total, "rows": nrows}})
