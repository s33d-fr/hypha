"""Métriques (contrat § 7) : noms, étiquettes, requêtes."""
from fastapi import APIRouter, Depends
from pydantic import BaseModel, Field

from .. import retention
from ..auth.principal import Principal, viewer
from ..db import fetch_all
from ..errors import QueryError, bad_request
from ..plans import get_plan
from ..query import compose
from ..query import metrics_ql as mql
from ..runtime import rt
from ..util import like_escape, now_ms
from .common import ok, time_range

router = APIRouter()

# Une série muette depuis 30 jours (hôte retiré, disque démonté) n'encombre plus les listes.
ACTIVE_SERIES = "last_seen > now() - interval '30 days'"


@router.get("/metrics/names")
async def metric_names(q: str = "", p: Principal = Depends(viewer)):
    params: list = [p.org_id]
    cond = ""
    if q.strip():
        cond = "AND name ILIKE %s"
        params.append("%" + like_escape(q.strip()) + "%")
    async with rt().db.reader() as conn:
        rows = await fetch_all(conn, f"SELECT DISTINCT name FROM metric_series WHERE org_id = %s "
                                     f"AND {ACTIVE_SERIES} {cond} ORDER BY name LIMIT 100", params)
    return ok([r["name"] for r in rows])


@router.get("/metrics/tags")
async def metric_tags(metric: str = "", p: Principal = Depends(viewer)):
    params: list = [p.org_id]
    cond = ""
    if metric.strip():
        cond = "AND s.name = %s"
        params.append(metric.strip())
    async with rt().db.reader() as conn:
        rows = await fetch_all(conn, f"""
            SELECT t.key, (array_agg(DISTINCT t.value ORDER BY t.value))[1:500] AS vals
              FROM metric_series s, jsonb_each_text(s.tags) AS t(key, value)
             WHERE s.org_id = %s AND s.{ACTIVE_SERIES} {cond}
             GROUP BY t.key ORDER BY t.key
        """, params)
    return ok({r["key"]: r["vals"] for r in rows})


class QueryItem(BaseModel):
    id: str = Field(min_length=1, max_length=50)
    q: str = Field(min_length=1, max_length=2000)


class FormulaItem(BaseModel):
    id: str = Field(min_length=1, max_length=50)
    formula: str = Field(min_length=1, max_length=2000)
    alias: str | None = Field(None, max_length=200)


class QueryBody(BaseModel):
    from_: int | None = Field(None, alias="from")
    to: int | None = None
    queries: list[QueryItem] = Field(min_length=1, max_length=20)
    formulas: list[FormulaItem] = Field(default_factory=list, max_length=20)
    max_points: int = Field(300, ge=10, le=2000)


async def run_query(conn, org_id: int, body: QueryBody) -> dict:
    """Corps de `/metrics/query` exécuté pour une organisation (aussi pour un tableau partagé)."""
    now = now_ms()
    # Conservation de la formule (§ 52) : Découverte lit 30 jours, dont 1 jour en pleine résolution.
    start, end, raw_s = retention.metrics_bounds(await get_plan(org_id, conn), *time_range(body.from_, body.to), now)
    token = mql.RAW_RETENTION_OVERRIDE.set(raw_s)
    try:
        return await compose.run(conn, org_id, start, end, body.max_points, now,
                                 [(q.id, q.q) for q in body.queries],
                                 [(f.id, f.formula, f.alias) for f in body.formulas])
    except QueryError as e:
        raise bad_request(str(e), "invalid_query")
    finally:
        mql.RAW_RETENTION_OVERRIDE.reset(token)


@router.post("/metrics/query")
async def metrics_query(body: QueryBody, p: Principal = Depends(viewer)):
    async with rt().db.reader() as conn:
        return ok(await run_query(conn, p.org_id, body))

