"""Maintenances planifiées (contrat § 16) : `viewer` lit, `member` écrit."""
from datetime import datetime, timezone

from fastapi import APIRouter, Body, Depends, Request

from .. import audit
from ..auth.principal import Principal, member, viewer
from ..db import fetch_all, fetch_one
from ..errors import bad_request, not_found
from ..monitors import downtime as D
from ..monitors import maintenance
from ..runtime import rt
from ..util import dt_to_ms, jdump, ms_to_dt, now_ms
from .common import done, ok

router = APIRouter()

FIELDS = "id, name, message, scope, start_ts, end_ts, recurrence, enabled, created_by, created_at, updated_at"


def present(row: dict, now: datetime) -> dict:
    out = dict(row)
    active = D.active_window(row, now)
    nxt = active or D.next_window(row, now)
    out["active"] = active is not None
    out["next_start"] = dt_to_ms(nxt.start) if nxt else None
    out["next_end"] = dt_to_ms(nxt.end) if nxt and nxt.end else None
    return out


def _normalized(row: dict) -> dict:
    """Ligne en base → forme d'entrée de normalize_downtime (horodatages en ms)."""
    return {"name": row["name"], "message": row["message"], "scope": row["scope"],
            "start_ts": dt_to_ms(row["start_ts"]), "end_ts": dt_to_ms(row["end_ts"]),
            "recurrence": row["recurrence"], "enabled": row["enabled"]}


def _validated(data: dict, current: dict | None = None) -> dict:
    try:
        return D.normalize_downtime(data, now_ms(), current)
    except ValueError as e:
        raise bad_request(str(e), "invalid_downtime")


async def _check_monitors(conn, org_id: int, dt: dict) -> None:
    ids = dt["scope"]["monitor_ids"]
    if not ids:
        return
    rows = await fetch_all(conn, "SELECT id FROM monitors WHERE org_id = %s AND id = ANY(%s::bigint[])", (org_id, ids))
    missing = set(ids) - {r["id"] for r in rows}
    if missing:
        raise bad_request(f"scope.monitor_ids : moniteurs inconnus {sorted(missing)}", "invalid_downtime")


async def _load(conn, org_id: int, downtime_id: int) -> dict:
    row = await fetch_one(conn, f"SELECT {FIELDS} FROM downtimes WHERE org_id = %s AND id = %s", (org_id, downtime_id))
    if row is None:
        raise not_found("maintenance introuvable")
    return row


@router.get("/downtimes")
async def list_downtimes(p: Principal = Depends(viewer)):
    now = datetime.now(timezone.utc)
    async with rt().db.reader() as conn:
        rows = await fetch_all(conn, f"SELECT {FIELDS} FROM downtimes WHERE org_id = %s ORDER BY start_ts DESC, id DESC "
                                     "LIMIT 1000", (p.org_id,))
    out = [present(r, now) for r in rows]
    # Actives en tête, puis les prochaines à venir, puis le reste (terminées).
    out.sort(key=lambda d: (not d["active"], d["next_start"] is None, d["next_start"] or 0))
    return ok(out)


@router.get("/downtimes/active")
async def active_downtimes(p: Principal = Depends(viewer)):
    now = datetime.now(timezone.utc)
    async with rt().db.reader() as conn:
        rows = await fetch_all(conn, f"""
            SELECT {FIELDS} FROM downtimes
             WHERE org_id = %s AND enabled AND start_ts <= now() AND (end_ts IS NULL OR end_ts > now())
             ORDER BY id
        """, (p.org_id,))
    return ok([d for d in (present(r, now) for r in rows) if d["active"]])


@router.post("/downtimes")
async def create_downtime(request: Request, data: dict = Body(...), p: Principal = Depends(member)):
    dt = _validated(data)
    async with rt().db.writer() as conn:
        await _check_monitors(conn, p.org_id, dt)
        row = await fetch_one(conn, f"""
            INSERT INTO downtimes (org_id, name, message, scope, start_ts, end_ts, recurrence, enabled, created_by)
            VALUES (%s, %s, %s, %s::jsonb, %s, %s, %s::jsonb, %s, %s) RETURNING {FIELDS}
        """, (p.org_id, dt["name"], dt["message"], jdump(dt["scope"]), ms_to_dt(dt["start_ts"]),
              ms_to_dt(dt["end_ts"]) if dt["end_ts"] else None,
              jdump(dt["recurrence"]) if dt["recurrence"] else None, dt["enabled"], p.user_id))
        await audit.record(conn, org_id=p.org_id, principal=p, action="create", object_type="downtime",
                           object_id=row["id"], summary=f"maintenance « {dt['name']} » planifiée", after=dt,
                           request=request)
    return ok(present(row, datetime.now(timezone.utc)), 201)


@router.get("/downtimes/{downtime_id}")
async def get_downtime(downtime_id: int, p: Principal = Depends(viewer)):
    async with rt().db.reader() as conn:
        row = await _load(conn, p.org_id, downtime_id)
    return ok(present(row, datetime.now(timezone.utc)))


@router.put("/downtimes/{downtime_id}")
async def update_downtime(downtime_id: int, request: Request, data: dict = Body(...), p: Principal = Depends(member)):
    async with rt().db.writer() as conn:
        current = await _load(conn, p.org_id, downtime_id)
        before = _normalized(current)
        dt = _validated(data, before)
        await _check_monitors(conn, p.org_id, dt)
        row = await fetch_one(conn, f"""
            UPDATE downtimes SET name = %s, message = %s, scope = %s::jsonb, start_ts = %s, end_ts = %s,
                   recurrence = %s::jsonb, enabled = %s, updated_at = now()
             WHERE org_id = %s AND id = %s RETURNING {FIELDS}
        """, (dt["name"], dt["message"], jdump(dt["scope"]), ms_to_dt(dt["start_ts"]),
              ms_to_dt(dt["end_ts"]) if dt["end_ts"] else None,
              jdump(dt["recurrence"]) if dt["recurrence"] else None, dt["enabled"], p.org_id, downtime_id))
        await audit.record(conn, org_id=p.org_id, principal=p, action="update", object_type="downtime",
                           object_id=downtime_id, summary=f"maintenance « {dt['name']} » modifiée",
                           before=before, after=dt, request=request)
    return ok(present(row, datetime.now(timezone.utc)))


@router.delete("/downtimes/{downtime_id}")
async def delete_downtime(downtime_id: int, request: Request, p: Principal = Depends(member)):
    now = datetime.now(timezone.utc)
    async with rt().db.writer() as conn:
        row = await fetch_one(conn, f"DELETE FROM downtimes WHERE org_id = %s AND id = %s RETURNING org_id, {FIELDS}",
                              (p.org_id, downtime_id))
        if row is None:
            raise not_found("maintenance introuvable")
        w = D.active_window(row, now)
        if w:
            # Supprimée en cours : la fin se marque tout de suite (le worker ne la verra plus).
            await maintenance.event(conn, row, now, "downtime_end", f"Maintenance terminée : {row['name']}",
                                    "Maintenance supprimée ; notifications rétablies", maintenance.key(row["id"], w))
        await audit.record(conn, org_id=p.org_id, principal=p, action="delete", object_type="downtime",
                           object_id=downtime_id, summary=f"maintenance « {row['name']} » supprimée", before=row,
                           request=request)
    return done()
