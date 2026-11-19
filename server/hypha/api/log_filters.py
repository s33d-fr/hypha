"""Filtres d'exclusion des journaux et volume (contrat § 11) : `viewer` lit, `admin` écrit."""
from fastapi import APIRouter, Body, Depends, Query, Request

from .. import audit
from ..auth.principal import Principal, admin, viewer
from ..db import fetch_all, fetch_one
from ..errors import QueryError, bad_request, not_found
from ..ingest.log_filters import EXCLUDED_METRIC, SEVERE
from ..query import logs_ql
from ..runtime import rt
from ..util import ms_to_dt, now_ms, real_value
from .common import done, ok, time_range
from .logs import LOG_FIELDS

router = APIRouter()

FIELDS = "id, name, query, action, sample_rate, enabled, position, excluded_total, created_at, updated_at"
ACTIONS = ("exclude", "sample")
LENGTH_SAMPLE = 20_000          # lignes lues pour estimer la taille moyenne d'un message
RAW_DAYS = 14                   # au-delà, les points de la métrique d'exclusion se lisent en agrégat 5 min


def _present(row: dict) -> dict:
    # sample_rate est un `real` : 0,2 revient en 0,200000003, on rend la valeur saisie.
    return {**row, "sample_rate": real_value(row["sample_rate"])}


def validate(data: dict, current: dict | None = None) -> dict:
    """Définition complète d'un filtre (création, ou modification partielle de `current`)."""
    merged = {"name": None, "query": None, "action": "exclude", "sample_rate": 0.0, "enabled": True,
              **({k: current[k] for k in ("name", "query", "action", "sample_rate", "enabled")} if current else {}),
              **{k: v for k, v in data.items() if k in ("name", "query", "action", "sample_rate", "enabled")}}
    name, query = merged["name"], merged["query"]
    if not isinstance(name, str) or not name.strip() or len(name) > 200:
        raise bad_request("name est obligatoire (200 caractères au plus)", "invalid_filter")
    if not isinstance(query, str) or not query.strip() or len(query) > 2000:
        raise bad_request("query est obligatoire : un filtre vide jetterait toutes les lignes", "invalid_filter")
    try:
        logs_ql.compile_query(query)
    except QueryError as e:
        raise bad_request(f"requête : {e}", "invalid_query")
    if merged["action"] not in ACTIONS:
        raise bad_request(f"action attendue : {'|'.join(ACTIONS)}", "invalid_filter")
    rate = merged["sample_rate"]
    if isinstance(rate, bool) or not isinstance(rate, (int, float)) or not 0 <= rate <= 1:
        raise bad_request("sample_rate doit être un nombre entre 0 et 1 (part gardée)", "invalid_filter")
    if not isinstance(merged["enabled"], bool):
        raise bad_request("enabled doit être un booléen", "invalid_filter")
    return {"name": name.strip(), "query": query.strip(), "action": merged["action"],
            "sample_rate": float(rate) if merged["action"] == "sample" else 0.0, "enabled": merged["enabled"]}


def _forget(org_id: int) -> None:
    # Ce processus relit tout de suite ; les autres au plus tard après 60 s (durée du cache).
    rt().log_filters.pop(org_id)


@router.get("/logs/filters")
async def list_filters(p: Principal = Depends(viewer)):
    async with rt().db.reader() as conn:
        rows = await fetch_all(conn, f"SELECT {FIELDS} FROM log_filters WHERE org_id = %s ORDER BY position, id",
                               (p.org_id,))
    return ok([_present(r) for r in rows])


@router.post("/logs/filters")
async def create_filter(request: Request, data: dict = Body(...), p: Principal = Depends(admin)):
    f = validate(data)
    async with rt().db.writer() as conn:
        row = await fetch_one(conn, f"""
            INSERT INTO log_filters (org_id, name, query, action, sample_rate, enabled, position, created_by)
            VALUES (%s, %s, %s, %s, %s, %s,
                    (SELECT coalesce(max(position) + 1, 0) FROM log_filters WHERE org_id = %s), %s)
            RETURNING {FIELDS}
        """, (p.org_id, f["name"], f["query"], f["action"], f["sample_rate"], f["enabled"], p.org_id, p.user_id))
        await audit.record(conn, org_id=p.org_id, principal=p, action="create", object_type="log_filter",
                           object_id=row["id"], summary=f"filtre de journaux « {f['name']} » créé", after=f,
                           request=request)
    _forget(p.org_id)
    return ok(_present(row), 201)


@router.post("/logs/filters/reorder")
async def reorder_filters(request: Request, data: dict = Body(...), p: Principal = Depends(admin)):
    ids = data.get("ids")
    if not isinstance(ids, list) or not all(isinstance(i, int) and not isinstance(i, bool) for i in ids) \
            or len(ids) != len(set(ids)):
        raise bad_request("ids : liste d'identifiants de filtres, sans doublon")
    async with rt().db.writer() as conn:
        existing = [r["id"] for r in await fetch_all(
            conn, "SELECT id FROM log_filters WHERE org_id = %s ORDER BY position, id", (p.org_id,))]
        unknown = set(ids) - set(existing)
        if unknown:
            raise bad_request(f"filtres inconnus : {sorted(unknown)}")
        # Les filtres absents de la liste gardent leur ordre relatif, après ceux qui sont listés.
        order = ids + [i for i in existing if i not in set(ids)]
        await conn.execute("""
            UPDATE log_filters f SET position = x.pos, updated_at = now()
              FROM unnest(%s::bigint[], %s::int[]) AS x(id, pos)
             WHERE f.id = x.id AND f.org_id = %s
        """, (order, list(range(len(order))), p.org_id))
        rows = await fetch_all(conn, f"SELECT {FIELDS} FROM log_filters WHERE org_id = %s ORDER BY position, id",
                               (p.org_id,))
        await audit.record(conn, org_id=p.org_id, principal=p, action="reorder", object_type="log_filter",
                           summary="ordre des filtres de journaux modifié", before={"ids": existing},
                           after={"ids": order}, request=request)
    _forget(p.org_id)
    return ok([_present(r) for r in rows])


@router.get("/logs/filters/preview")
async def preview_filter(q: str = "", p: Principal = Depends(viewer)):
    if not q.strip():
        raise bad_request("q est obligatoire")
    try:
        frag, qparams = logs_ql.compile_query(q)
    except QueryError as e:
        raise bad_request(str(e), "invalid_query")
    window = "ts > now() - interval '1 hour'"
    async with rt().db.reader() as conn:
        match = await fetch_one(conn, f"""
            SELECT count(*) AS n, count(*) FILTER (WHERE status = ANY(%s::text[])) AS severe
              FROM logs WHERE org_id = %s AND {window} AND {frag}
        """, [sorted(SEVERE), p.org_id] + qparams)
        total = await fetch_one(conn, f"SELECT count(*) AS n FROM logs WHERE org_id = %s AND {window}", (p.org_id,))
        sample = await fetch_all(conn, f"""
            SELECT {LOG_FIELDS} FROM logs WHERE org_id = %s AND {window} AND {frag}
             ORDER BY ts DESC, id DESC LIMIT 5
        """, [p.org_id] + qparams)
    per_hour, all_lines = match["n"], total["n"]
    # `errors` : lignes error et plus graves de l'aperçu, qu'un « sample » ne jette jamais.
    return ok({"per_hour": per_hour, "share": per_hour / all_lines if all_lines else 0.0, "sample": sample,
               "errors": match["severe"]})


@router.put("/logs/filters/{filter_id}")
async def update_filter(filter_id: int, request: Request, data: dict = Body(...), p: Principal = Depends(admin)):
    async with rt().db.writer() as conn:
        current = await fetch_one(conn, f"SELECT {FIELDS} FROM log_filters WHERE org_id = %s AND id = %s",
                                  (p.org_id, filter_id))
        if current is None:
            raise not_found("filtre introuvable")
        f = validate(data, current)
        row = await fetch_one(conn, f"""
            UPDATE log_filters SET name = %s, query = %s, action = %s, sample_rate = %s, enabled = %s,
                   updated_at = now()
             WHERE org_id = %s AND id = %s RETURNING {FIELDS}
        """, (f["name"], f["query"], f["action"], f["sample_rate"], f["enabled"], p.org_id, filter_id))
        await audit.record(conn, org_id=p.org_id, principal=p, action="update", object_type="log_filter",
                           object_id=filter_id, summary=f"filtre de journaux « {f['name']} » modifié",
                           before=_present(current), after=f, request=request)
    rt().bad_filters.discard(filter_id)
    _forget(p.org_id)
    return ok(_present(row))


@router.delete("/logs/filters/{filter_id}")
async def delete_filter(filter_id: int, request: Request, p: Principal = Depends(admin)):
    async with rt().db.writer() as conn:
        row = await fetch_one(conn, "DELETE FROM log_filters WHERE org_id = %s AND id = %s RETURNING id, name, query",
                              (p.org_id, filter_id))
        if row is None:
            raise not_found("filtre introuvable")
        await audit.record(conn, org_id=p.org_id, principal=p, action="delete", object_type="log_filter",
                           object_id=filter_id, summary=f"filtre de journaux « {row['name']} » supprimé", before=row,
                           request=request)
    _forget(p.org_id)
    return done()


@router.get("/logs/volume")
async def log_volume(from_: int | None = Query(None, alias="from"), to: int | None = None,
                     p: Principal = Depends(viewer)):
    """Volume par service et par hôte. Les comptes sont exacts (index org/service/hôte + temps) ; les
    octets sont approchés : taille moyenne des messages sur les 20 000 lignes les plus récentes."""
    start, end = time_range(from_, to)
    rng = (p.org_id, ms_to_dt(start), ms_to_dt(end))
    where = "org_id = %s AND ts >= %s AND ts < %s"
    async with rt().db.reader() as conn:
        total = (await fetch_one(conn, f"SELECT count(*) AS n FROM logs WHERE {where}", rng))["n"]
        services = await fetch_all(conn, f"""
            SELECT service, count(*) AS count FROM logs WHERE {where} GROUP BY service ORDER BY count DESC LIMIT 50
        """, rng)
        hosts = await fetch_all(conn, f"""
            SELECT host_id, count(*) AS count FROM logs WHERE {where} GROUP BY host_id ORDER BY count DESC LIMIT 50
        """, rng)
        lengths = await fetch_all(conn, f"""
            SELECT service, host_id, avg(length(message))::float8 AS avg_len,
                   GROUPING(service) AS g_service, GROUPING(host_id) AS g_host
              FROM (SELECT service, host_id, message FROM logs WHERE {where} ORDER BY ts DESC LIMIT %s) s
             GROUP BY GROUPING SETS ((service), (host_id), ())
        """, rng + (LENGTH_SAMPLE,))
        names = {r["id"]: r["hostname"] for r in await fetch_all(
            conn, "SELECT id, hostname FROM hosts WHERE org_id = %s AND id = ANY(%s::bigint[])",
            (p.org_id, [h["host_id"] for h in hosts if h["host_id"] is not None]))}
        filters = await fetch_all(conn, "SELECT id, name FROM log_filters WHERE org_id = %s ORDER BY position, id",
                                  (p.org_id,))
        rollup = start < now_ms() - RAW_DAYS * 86400_000
        table, value = ("metric_rollup_5m", "p.sum") if rollup else ("metric_points", "p.value")
        excluded = await fetch_all(conn, f"""
            SELECT s.tags ->> 'filter_id' AS filter_id, sum({value})::float8 AS n
              FROM metric_series s JOIN {table} p ON p.series_id = s.id
             WHERE s.org_id = %s AND s.name = %s AND p.ts >= %s AND p.ts < %s
             GROUP BY 1
        """, (p.org_id, EXCLUDED_METRIC, ms_to_dt(start - (300_000 if rollup else 0)), ms_to_dt(end)))
    overall = next((r["avg_len"] for r in lengths if r["g_service"] and r["g_host"]), None) or 0.0
    by_service_len = {r["service"]: r["avg_len"] for r in lengths if not r["g_service"] and r["g_host"]}
    by_host_len = {r["host_id"]: r["avg_len"] for r in lengths if r["g_service"] and not r["g_host"]}
    counts = {r["filter_id"]: int(r["n"] or 0) for r in excluded}
    return ok({
        "total": total,
        "by_service": [{"service": s["service"], "count": s["count"],
                        "bytes": round(s["count"] * (by_service_len.get(s["service"]) or overall))} for s in services],
        "by_host": [{"host": names.get(h["host_id"]), "host_id": h["host_id"], "count": h["count"],
                     "bytes": round(h["count"] * (by_host_len.get(h["host_id"]) or overall))} for h in hosts],
        "excluded": sorted(({"filter_id": f["id"], "name": f["name"], "count": counts.get(str(f["id"]), 0)}
                            for f in filters), key=lambda e: -e["count"]),
    })
