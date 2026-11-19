"""Métadonnées des métriques et consommation (contrat § 30).

`GET /metrics/metadata` : l'entrée de l'organisation, sinon celle du catalogue livré ;
`PUT` (member) la remplace pour l'organisation, `DELETE` revient au catalogue.
`GET /usage` : volumes par jour (`usage_daily`), hôtes, équipements, séries et services bavards.
"""
import re
from datetime import timedelta

from fastapi import APIRouter, Body, Depends, Query, Request

from .. import audit
from ..auth.principal import Principal, member, viewer
from ..db import fetch_all, fetch_one
from ..errors import bad_request, not_found
from ..query.metric_catalog import CATALOG, lookup
from ..runtime import rt
from ..util import METRIC_NAME_RE, like_escape, ms_to_dt
from .common import done, ok, time_range

router = APIRouter()

UNIT_RE = re.compile(r"^[a-z_]{1,50}$")
META_FIELDS = "metric, unit, per_unit, description, integration, updated_at"
TOP_SERVICES_WINDOW_S = 3600


def _metric(metric: str) -> str:
    metric = (metric or "").strip()
    if not METRIC_NAME_RE.match(metric):
        raise bad_request("metric : nom de métrique invalide")
    return metric


@router.get("/metrics/metadata")
async def get_metadata(metric: str = "", q: str = "", p: Principal = Depends(viewer)):
    async with rt().db.reader() as conn:
        if metric.strip():
            name = _metric(metric)
            row = await fetch_one(conn, f"SELECT {META_FIELDS} FROM metric_metadata WHERE org_id = %s AND metric = %s",
                                  (p.org_id, name))
            if row is not None:
                return ok({**row, "source": "org"})
            entry = lookup(name)
            return ok(entry or {"metric": name, "unit": None, "per_unit": None, "description": "",
                                "integration": "", "source": None})
        params: list = [p.org_id]
        cond = ""
        if q.strip():
            cond = "AND metric ILIKE %s"
            params.append("%" + like_escape(q.strip()) + "%")
        rows = await fetch_all(conn, f"SELECT {META_FIELDS} FROM metric_metadata WHERE org_id = %s {cond} "
                                     "ORDER BY metric LIMIT 5000", params)
    out = {r["metric"]: {**r, "source": "org"} for r in rows}
    needle = q.strip().lower()
    for name in CATALOG:
        if name not in out and (not needle or needle in name.lower()):
            out[name] = lookup(name)
    return ok([out[k] for k in sorted(out)])


@router.put("/metrics/metadata")
async def put_metadata(request: Request, metric: str = "", data: dict = Body(...), p: Principal = Depends(member)):
    name = _metric(metric or str(data.get("metric") or ""))
    unit, per_unit = data.get("unit"), data.get("per_unit")
    for label, value in (("unit", unit), ("per_unit", per_unit)):
        if value not in (None, "") and (not isinstance(value, str) or not UNIT_RE.match(value)):
            raise bad_request(f"{label} : unité en minuscules (percent, byte, millisecond…)")
    description = data.get("description", "") or ""
    integration = data.get("integration", "") or ""
    if not isinstance(description, str) or len(description) > 2000:
        raise bad_request("description : texte de 2000 caractères au plus")
    if not isinstance(integration, str) or len(integration) > 100:
        raise bad_request("integration : texte de 100 caractères au plus")
    async with rt().db.writer() as conn:
        before = await fetch_one(conn, f"SELECT {META_FIELDS} FROM metric_metadata WHERE org_id = %s AND metric = %s",
                                 (p.org_id, name))
        row = await fetch_one(conn, f"""
            INSERT INTO metric_metadata (org_id, metric, unit, per_unit, description, integration)
            VALUES (%s, %s, %s, %s, %s, %s)
            ON CONFLICT (org_id, metric) DO UPDATE
               SET unit = EXCLUDED.unit, per_unit = EXCLUDED.per_unit, description = EXCLUDED.description,
                   integration = EXCLUDED.integration, updated_at = now()
            RETURNING {META_FIELDS}
        """, (p.org_id, name, unit or None, per_unit or None, description, integration))
        await audit.record(conn, org_id=p.org_id, principal=p, action="update", object_type="metric_metadata",
                           object_id=name, summary=f"métadonnées de {name}", before=before, after=row, request=request)
    return ok({**row, "source": "org"})


@router.delete("/metrics/metadata")
async def delete_metadata(request: Request, metric: str = "", p: Principal = Depends(member)):
    name = _metric(metric)
    async with rt().db.writer() as conn:
        row = await fetch_one(conn, f"DELETE FROM metric_metadata WHERE org_id = %s AND metric = %s RETURNING {META_FIELDS}",
                              (p.org_id, name))
        if row is None:
            raise not_found("aucune métadonnée propre à l'organisation pour cette métrique")
        await audit.record(conn, org_id=p.org_id, principal=p, action="delete", object_type="metric_metadata",
                           object_id=name, summary=f"métadonnées de {name} rendues au catalogue", before=row,
                           request=request)
    return done()


@router.get("/usage")
async def usage(from_: int | None = Query(None, alias="from"), to: int | None = None,
                p: Principal = Depends(viewer)):
    start, end = time_range(from_, to, default_s=30 * 86400)
    first_day, last_day = ms_to_dt(start).date(), ms_to_dt(end).date()
    recent = max(start, end - TOP_SERVICES_WINDOW_S * 1000)
    async with rt().db.reader() as conn:
        days = await fetch_all(conn, """
            SELECT day, log_lines, log_bytes, metric_points, spans, rum_events, checks, span_bytes, synthetic_runs,
                   synthetic_billable_runs, explanations, sms, refused_log_lines, refused_spans FROM usage_daily
             WHERE org_id = %s AND day >= %s AND day <= %s ORDER BY day
        """, (p.org_id, first_day, last_day))
        hosts = await fetch_one(conn, """
            SELECT count(*) FILTER (WHERE kind = 'agent') AS hosts,
                   count(*) FILTER (WHERE kind = 'network_device') AS devices
              FROM hosts WHERE org_id = %s AND last_seen >= %s
        """, (p.org_id, ms_to_dt(start)))
        series = await fetch_one(conn, "SELECT count(*) AS n FROM metric_series WHERE org_id = %s AND last_seen >= %s",
                                 (p.org_id, ms_to_dt(start) - timedelta(hours=1)))
        services = await fetch_all(conn, """
            SELECT service, count(*) AS count, sum(length(message))::bigint AS bytes FROM logs
             WHERE org_id = %s AND ts >= %s AND ts < %s GROUP BY service ORDER BY count DESC LIMIT 10
        """, (p.org_id, ms_to_dt(recent), ms_to_dt(end)))
    for d in days:
        d["day"] = d["day"].isoformat()
    hosts = hosts or {"hosts": 0, "devices": 0}
    return ok({"days": days, "hosts": hosts["hosts"], "devices": hosts["devices"],
               "series": (series or {"n": 0})["n"], "top_services_by_logs": services,
               "top_services_window_s": (end - recent) // 1000})
