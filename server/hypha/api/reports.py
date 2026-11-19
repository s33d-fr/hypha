"""Rapports programmés (contrat § 22) : `viewer` lit, `member` écrit, envoie et prévisualise."""
from datetime import datetime, timezone

from fastapi import APIRouter, Body, Depends, Request
from fastapi.responses import HTMLResponse

from .. import audit
from ..auth.principal import Principal, member, viewer
from ..db import fetch_all, fetch_one
from ..errors import bad_request, not_found
from ..monitors.notify import deliver_all
from ..reports import schedule as SCH
from ..reports import service as S
from ..runtime import rt
from ..util import dt_to_ms, jdump
from .common import done, ok

router = APIRouter()

EDITABLE = ("name", "kind", "dashboard_id", "channel_ids", "schedule", "period_s", "enabled")


def _present(row: dict) -> dict:
    out = dict(row)
    try:
        out["next_send"] = dt_to_ms(SCH.next_due(row["schedule"] or {}, datetime.now(timezone.utc)))
    except (ValueError, KeyError, TypeError):
        out["next_send"] = None
    return out


async def _validated(conn, org_id: int, data: dict, current: dict | None = None) -> dict:
    merged = {**(current or {"kind": "summary", "dashboard_id": None, "channel_ids": [], "schedule": {},
                             "period_s": 604800, "enabled": True}),
              **{k: v for k, v in data.items() if k in EDITABLE}}
    name = merged.get("name")
    if not isinstance(name, str) or not name.strip() or len(name) > 200:
        raise bad_request("name est obligatoire (200 caractères au plus)", "invalid_report")
    if merged["kind"] not in SCH.REPORT_KINDS:
        raise bad_request(f"kind attendu : {'|'.join(SCH.REPORT_KINDS)}", "invalid_report")
    try:
        schedule = SCH.normalize_schedule(merged.get("schedule") or {})
    except ValueError as e:
        raise bad_request(str(e), "invalid_report")
    period = merged.get("period_s")
    if isinstance(period, bool) or not isinstance(period, int) or not 3600 <= period <= 92 * 86400:
        raise bad_request("period_s doit être un entier entre 3600 et 7948800 (92 jours)", "invalid_report")
    if not isinstance(merged.get("enabled"), bool):
        raise bad_request("enabled doit être un booléen", "invalid_report")
    ids = merged.get("channel_ids") or []
    if not isinstance(ids, list) or len(ids) > 20 or not all(isinstance(i, int) and not isinstance(i, bool) for i in ids):
        raise bad_request("channel_ids : liste d'identifiants de canaux (20 au plus)", "invalid_report")
    ids = sorted(set(ids))
    if ids:
        rows = await fetch_all(conn, "SELECT id, kind FROM notification_channels WHERE org_id = %s AND id = ANY(%s::bigint[])",
                               (org_id, ids))
        missing = set(ids) - {r["id"] for r in rows}
        if missing:
            raise bad_request(f"canaux inconnus : {sorted(missing)}", "invalid_report")
        if not any(r["kind"] == "email" for r in rows):
            raise bad_request("un rapport part par courriel : au moins un canal email est attendu", "invalid_report")
    dashboard_id = merged.get("dashboard_id")
    if merged["kind"] == "dashboard":
        if isinstance(dashboard_id, bool) or not isinstance(dashboard_id, int):
            raise bad_request("dashboard_id est obligatoire pour un rapport de tableau de bord", "invalid_report")
        row = await fetch_one(conn, "SELECT id FROM dashboards WHERE org_id = %s AND id = %s", (org_id, dashboard_id))
        if row is None:
            raise bad_request("tableau de bord inconnu", "invalid_report")
    else:
        dashboard_id = None
    return {"name": name.strip(), "kind": merged["kind"], "dashboard_id": dashboard_id, "channel_ids": ids,
            "schedule": schedule, "period_s": period, "enabled": merged["enabled"]}


async def _load(conn, org_id: int, report_id: int) -> dict:
    row = await fetch_one(conn, f"SELECT {S.FIELDS} FROM report_schedules WHERE org_id = %s AND id = %s",
                          (org_id, report_id))
    if row is None:
        raise not_found("rapport introuvable")
    return row


@router.get("/reports")
async def list_reports(p: Principal = Depends(viewer)):
    async with rt().db.reader() as conn:
        rows = await fetch_all(conn, f"SELECT {S.FIELDS} FROM report_schedules WHERE org_id = %s ORDER BY name, id",
                               (p.org_id,))
    return ok([_present(r) for r in rows])


@router.post("/reports")
async def create_report(request: Request, data: dict = Body(...), p: Principal = Depends(member)):
    async with rt().db.writer() as conn:
        r = await _validated(conn, p.org_id, data)
        row = await fetch_one(conn, f"""
            INSERT INTO report_schedules (org_id, name, kind, dashboard_id, channel_ids, schedule, period_s, enabled,
                                          created_by)
            VALUES (%s, %s, %s, %s, %s::bigint[], %s::jsonb, %s, %s, %s) RETURNING {S.FIELDS}
        """, (p.org_id, r["name"], r["kind"], r["dashboard_id"], r["channel_ids"], jdump(r["schedule"]), r["period_s"],
              r["enabled"], p.user_id))
        await audit.record(conn, org_id=p.org_id, principal=p, action="create", object_type="report",
                           object_id=row["id"], summary=f"rapport « {r['name']} » ({r['kind']}) créé", after=r,
                           request=request)
    return ok(_present(row), 201)


@router.get("/reports/{report_id}")
async def get_report(report_id: int, p: Principal = Depends(viewer)):
    async with rt().db.reader() as conn:
        return ok(_present(await _load(conn, p.org_id, report_id)))


@router.put("/reports/{report_id}")
async def update_report(report_id: int, request: Request, data: dict = Body(...), p: Principal = Depends(member)):
    async with rt().db.writer() as conn:
        current = await _load(conn, p.org_id, report_id)
        r = await _validated(conn, p.org_id, data, {k: current[k] for k in EDITABLE})
        row = await fetch_one(conn, f"""
            UPDATE report_schedules SET name = %s, kind = %s, dashboard_id = %s, channel_ids = %s::bigint[],
                   schedule = %s::jsonb, period_s = %s, enabled = %s
             WHERE org_id = %s AND id = %s RETURNING {S.FIELDS}
        """, (r["name"], r["kind"], r["dashboard_id"], r["channel_ids"], jdump(r["schedule"]), r["period_s"],
              r["enabled"], p.org_id, report_id))
        await audit.record(conn, org_id=p.org_id, principal=p, action="update", object_type="report",
                           object_id=report_id, summary=f"rapport « {r['name']} » modifié",
                           before={k: current[k] for k in EDITABLE}, after=r, request=request)
    return ok(_present(row))


@router.delete("/reports/{report_id}")
async def delete_report(report_id: int, request: Request, p: Principal = Depends(member)):
    async with rt().db.writer() as conn:
        row = await fetch_one(conn, "DELETE FROM report_schedules WHERE org_id = %s AND id = %s RETURNING id, name, kind",
                              (p.org_id, report_id))
        if row is None:
            raise not_found("rapport introuvable")
        await audit.record(conn, org_id=p.org_id, principal=p, action="delete", object_type="report",
                           object_id=report_id, summary=f"rapport « {row['name']} » supprimé", before=row,
                           request=request)
    return done()


@router.get("/reports/{report_id}/preview")
async def preview_report(report_id: int, p: Principal = Depends(member)):
    async with rt().db.reader() as conn:
        report = await _load(conn, p.org_id, report_id)
        _, html, _ = await S.build(conn, rt().settings, report, p.org_id)
    # Rendu dans un cadre de l'interface : aucune ressource ni script, rien à exécuter.
    return HTMLResponse(html, headers={"Content-Security-Policy": "default-src 'none'; style-src 'unsafe-inline'; img-src data:",
                                       "X-Content-Type-Options": "nosniff"})


@router.post("/reports/{report_id}/send")
async def send_report(report_id: int, request: Request, p: Principal = Depends(member)):
    settings = rt().settings
    async with rt().db.writer() as conn:
        report = await _load(conn, p.org_id, report_id)
        subject, html, text = await S.build(conn, settings, report, p.org_id)
        channels = await S.email_channels(conn, p.org_id, report["channel_ids"] or [])
        if not channels:
            raise bad_request("aucun canal courriel actif pour ce rapport", "no_channel")
        await conn.execute("UPDATE report_schedules SET last_sent_at = now() WHERE org_id = %s AND id = %s",
                           (p.org_id, report_id))
        await audit.record(conn, org_id=p.org_id, principal=p, action="send", object_type="report",
                           object_id=report_id, summary=f"rapport « {report['name']} » envoyé à la demande",
                           after={"channel_ids": [c["id"] for c in channels]}, request=request)
    results = await deliver_all(settings, S.notices(settings, report, channels, subject, html, text))
    return ok({"ok": all(r[0] for r in results), "sent_to": [c["id"] for c in channels],
               "detail": "; ".join(f"{c['name']} : {r[1]}" for c, r in zip(channels, results))})
