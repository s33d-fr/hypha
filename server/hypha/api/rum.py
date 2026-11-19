"""RUM (contrat § 7) : applications et synthèse.

Les signaux web (LCP, INP, CLS) arrivent en événements `vital` dont `name` porte le nom du
signal et `duration_ms` sa valeur (sans unité pour CLS). Le temps de chargement d'une page est le
`duration_ms` de son événement `view`.
"""
import secrets

from fastapi import APIRouter, Depends, Query, Request
from pydantic import BaseModel, Field

from .. import audit
from ..auth.principal import Principal, member, viewer
from ..db import fetch_all, fetch_one
from ..errors import not_found
from ..runtime import rt
from ..util import ms_to_dt
from .common import ok, time_range

router = APIRouter()

FIELDS = "id, name, client_token, created_at"


@router.get("/rum/apps")
async def list_apps(p: Principal = Depends(viewer)):
    async with rt().db.reader() as conn:
        return ok(await fetch_all(conn, f"SELECT {FIELDS} FROM rum_apps WHERE org_id = %s ORDER BY name, id",
                                  (p.org_id,)))


class RumAppIn(BaseModel):
    name: str = Field(min_length=1, max_length=200)


@router.post("/rum/apps")
async def create_app(body: RumAppIn, request: Request, p: Principal = Depends(member)):
    token = "pub" + secrets.token_hex(16)   # public par nature : il est lu dans la page du client
    async with rt().db.writer() as conn:
        row = await fetch_one(conn, f"INSERT INTO rum_apps (org_id, name, client_token) VALUES (%s, %s, %s) "
                                    f"RETURNING {FIELDS}", (p.org_id, body.name.strip(), token))
        await audit.record(conn, org_id=p.org_id, principal=p, action="create", object_type="rum_app",
                           object_id=row["id"], summary=f"application RUM « {body.name.strip()} » créée", request=request)
    return ok(row, 201)


@router.get("/rum/apps/{app_id}/summary")
async def app_summary(app_id: int, from_: int | None = Query(None, alias="from"), to: int | None = None,
                      p: Principal = Depends(viewer)):
    start, end = time_range(from_, to)
    params = (p.org_id, app_id, ms_to_dt(start), ms_to_dt(end))
    where = "org_id = %s AND app_id = %s AND ts >= %s AND ts < %s"
    async with rt().db.reader() as conn:
        app = await fetch_one(conn, "SELECT id FROM rum_apps WHERE org_id = %s AND id = %s", (p.org_id, app_id))
        if app is None:
            raise not_found("application RUM introuvable")
        s = await fetch_one(conn, f"""
            SELECT count(*) FILTER (WHERE event_type = 'view') AS views,
                   count(DISTINCT session_id) AS sessions,
                   count(*) FILTER (WHERE event_type = 'error') AS errors,
                   percentile_cont(0.75) WITHIN GROUP (ORDER BY duration_ms)
                       FILTER (WHERE event_type = 'vital' AND upper(name) = 'LCP') AS lcp_p75_ms,
                   percentile_cont(0.75) WITHIN GROUP (ORDER BY duration_ms)
                       FILTER (WHERE event_type = 'vital' AND upper(name) = 'INP') AS inp_p75_ms,
                   percentile_cont(0.75) WITHIN GROUP (ORDER BY duration_ms)
                       FILTER (WHERE event_type = 'vital' AND upper(name) = 'CLS') AS cls_p75,
                   percentile_cont(0.75) WITHIN GROUP (ORDER BY duration_ms)
                       FILTER (WHERE event_type = 'view') AS load_p75_ms
              FROM rum_events WHERE {where}
        """, params)
        slowest = await fetch_all(conn, f"""
            SELECT view_url AS url, count(*) AS views,
                   percentile_cont(0.75) WITHIN GROUP (ORDER BY duration_ms) AS p75_ms
              FROM rum_events WHERE {where} AND event_type = 'view' AND duration_ms IS NOT NULL
             GROUP BY view_url ORDER BY p75_ms DESC NULLS LAST LIMIT 10
        """, params)
        top_errors = await fetch_all(conn, f"""
            SELECT name AS message, count(*) AS count FROM rum_events
             WHERE {where} AND event_type = 'error' GROUP BY name ORDER BY count DESC LIMIT 10
        """, params)
    return ok({**s, "slowest_pages": slowest, "top_errors": top_errors})
