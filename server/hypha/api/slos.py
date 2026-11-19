"""SLO (contrat § 13) : `viewer` lit, `member` écrit."""
import logging

from fastapi import APIRouter, Body, Depends, Request

from .. import audit
from ..auth.principal import Principal, member, viewer
from ..db import fetch_all, fetch_one
from ..errors import QueryError, bad_request, not_found
from ..query import metrics_ql as mql
from ..runtime import rt
from ..slo.compute import TIMEFRAME_S
from ..slo.service import FIELDS, load_slo, slo_status
from ..util import clean_tags, jdump, real_value
from .common import done, ok

log = logging.getLogger("hypha.slo")
router = APIRouter()

EDITABLE = ("name", "description", "type", "monitor_id", "good_query", "total_query", "target", "warning",
            "timeframe", "tags")


def _present(row: dict) -> dict:
    out = {k: v for k, v in row.items() if k != "org_id"}
    # `real` : 99.9 revient en 99.9000015, on rend la valeur saisie.
    out["target"] = real_value(row["target"])
    out["warning"] = real_value(row["warning"])
    return out


def _number(v, what: str) -> float:
    if isinstance(v, bool) or not isinstance(v, (int, float)) or not 0 < v < 100:
        raise bad_request(f"{what} doit être un nombre strictement entre 0 et 100", "invalid_slo")
    return float(v)


async def validate(conn, org_id: int, data: dict, current: dict | None = None) -> dict:
    merged = {"description": "", "warning": None, "timeframe": "30d", "tags": {}, "monitor_id": None,
              "good_query": None, "total_query": None,
              **({k: current[k] for k in EDITABLE} if current else {}),
              **{k: v for k, v in data.items() if k in EDITABLE}}
    name = merged.get("name")
    if not isinstance(name, str) or not name.strip() or len(name) > 200:
        raise bad_request("name est obligatoire (200 caractères au plus)", "invalid_slo")
    desc = merged["description"] or ""
    if not isinstance(desc, str) or len(desc) > 5000:
        raise bad_request("description doit être une chaîne", "invalid_slo")
    kind = merged.get("type")
    if kind not in ("monitor", "metric"):
        raise bad_request("type attendu : monitor|metric", "invalid_slo")
    target = real_value(_number(merged.get("target"), "target"))
    warning = None if merged["warning"] is None else real_value(_number(merged["warning"], "warning"))
    if warning is not None and warning <= target:
        raise bad_request("warning doit être plus exigeant que target (ex. cible 99.9, avertissement 99.95)",
                          "invalid_slo")
    if merged["timeframe"] not in TIMEFRAME_S:
        raise bad_request(f"timeframe attendu : {'|'.join(TIMEFRAME_S)}", "invalid_slo")
    tags = merged["tags"] or {}
    if not isinstance(tags, dict) or len(clean_tags(tags)) != len(tags):
        raise bad_request("tags : objet {clé: valeur texte}", "invalid_slo")
    out = {"name": name.strip(), "description": desc, "type": kind, "target": target, "warning": warning,
           "timeframe": merged["timeframe"], "tags": clean_tags(tags), "monitor_id": None,
           "good_query": None, "total_query": None}
    if kind == "monitor":
        mid = merged["monitor_id"]
        if isinstance(mid, bool) or not isinstance(mid, int):
            raise bad_request("monitor_id est obligatoire pour un SLO de type monitor", "invalid_slo")
        row = await fetch_one(conn, "SELECT type FROM monitors WHERE org_id = %s AND id = %s", (org_id, mid))
        if row is None:
            raise bad_request("moniteur inconnu", "invalid_slo")
        if row["type"] == "slo":
            raise bad_request("un SLO ne peut pas reposer sur un moniteur de SLO", "invalid_slo")
        out["monitor_id"] = mid
    else:
        for key in ("good_query", "total_query"):
            q = merged[key]
            if not isinstance(q, str) or not q.strip():
                raise bad_request(f"{key} est obligatoire pour un SLO de type metric", "invalid_slo")
            try:
                mql.parse(q)
            except QueryError as e:
                raise bad_request(f"{key} : {e}", "invalid_query")
            out[key] = q.strip()
    return out


async def _summary(conn, slo: dict, with_history: bool) -> dict:
    try:
        return await slo_status(conn, slo, with_history)
    except QueryError as e:
        return {"sli": None, "state": None, "error": str(e)}


@router.get("/slos")
async def list_slos(p: Principal = Depends(viewer)):
    async with rt().db.reader() as conn:
        rows = await fetch_all(conn, f"SELECT org_id, {FIELDS} FROM slos WHERE org_id = %s ORDER BY name, id",
                               (p.org_id,))
        out = []
        for r in rows:
            summary = await _summary(conn, r, with_history=False)
            out.append({**_present(r), **{k: v for k, v in summary.items() if k != "target"}})
    return ok(out)


@router.post("/slos")
async def create_slo(request: Request, data: dict = Body(...), p: Principal = Depends(member)):
    async with rt().db.writer() as conn:
        s = await validate(conn, p.org_id, data)
        row = await fetch_one(conn, f"""
            INSERT INTO slos (org_id, name, description, type, monitor_id, good_query, total_query, target, warning,
                              timeframe, tags, created_by)
            VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s::jsonb, %s)
            RETURNING org_id, {FIELDS}
        """, (p.org_id, s["name"], s["description"], s["type"], s["monitor_id"], s["good_query"], s["total_query"],
              s["target"], s["warning"], s["timeframe"], jdump(s["tags"]), p.user_id))
        await audit.record(conn, org_id=p.org_id, principal=p, action="create", object_type="slo", object_id=row["id"],
                           summary=f"SLO « {s['name']} » créé", after=s, request=request)
    return ok(_present(row), 201)


@router.get("/slos/{slo_id}")
async def get_slo(slo_id: int, p: Principal = Depends(viewer)):
    async with rt().db.reader() as conn:
        row = await load_slo(conn, p.org_id, slo_id)
        if row is None:
            raise not_found("SLO introuvable")
        summary = await _summary(conn, row, with_history=False)
    return ok({**_present(row), **{k: v for k, v in summary.items() if k != "target"}})


@router.put("/slos/{slo_id}")
async def update_slo(slo_id: int, request: Request, data: dict = Body(...), p: Principal = Depends(member)):
    async with rt().db.writer() as conn:
        current = await load_slo(conn, p.org_id, slo_id)
        if current is None:
            raise not_found("SLO introuvable")
        s = await validate(conn, p.org_id, data, current)
        row = await fetch_one(conn, f"""
            UPDATE slos SET name = %s, description = %s, type = %s, monitor_id = %s, good_query = %s,
                   total_query = %s, target = %s, warning = %s, timeframe = %s, tags = %s::jsonb, updated_at = now()
             WHERE org_id = %s AND id = %s RETURNING org_id, {FIELDS}
        """, (s["name"], s["description"], s["type"], s["monitor_id"], s["good_query"], s["total_query"],
              s["target"], s["warning"], s["timeframe"], jdump(s["tags"]), p.org_id, slo_id))
        await audit.record(conn, org_id=p.org_id, principal=p, action="update", object_type="slo", object_id=slo_id,
                           summary=f"SLO « {s['name']} » modifié", before={k: current[k] for k in EDITABLE},
                           after=s, request=request)
    return ok(_present(row))


@router.delete("/slos/{slo_id}")
async def delete_slo(slo_id: int, request: Request, p: Principal = Depends(member)):
    async with rt().db.writer() as conn:
        row = await fetch_one(conn, "DELETE FROM slos WHERE org_id = %s AND id = %s RETURNING id, name, type",
                              (p.org_id, slo_id))
        if row is None:
            raise not_found("SLO introuvable")
        await audit.record(conn, org_id=p.org_id, principal=p, action="delete", object_type="slo", object_id=slo_id,
                           summary=f"SLO « {row['name']} » supprimé", before=row, request=request)
    return done()


@router.get("/slos/{slo_id}/status")
async def slo_status_route(slo_id: int, p: Principal = Depends(viewer)):
    async with rt().db.reader() as conn:
        row = await load_slo(conn, p.org_id, slo_id)
        if row is None:
            raise not_found("SLO introuvable")
        try:
            return ok(await slo_status(conn, row, with_history=True))
        except QueryError as e:
            raise bad_request(str(e), "invalid_query")
