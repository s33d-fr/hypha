"""Actions à distance, côté interface (contrat § 28) : proposer (member), approuver ou rejeter (admin),
suivre. Le canal de l'agent (`/agent/control`, `/agent/actions/{id}/result`) est servi ailleurs."""
from fastapi import APIRouter, Body, Depends, Query, Request

from .. import audit
from ..actions import propose as P
from ..actions.service import host_agent
from ..auth.principal import Principal, admin, member, viewer
from ..db import fetch_all, fetch_one
from ..errors import ApiError, bad_request, forbidden, not_found
from ..runtime import rt
from ..util import jdump
from .common import ok

router = APIRouter()

STATUSES = ("pending_approval", "approved", "sent", "running", "succeeded", "failed", "rejected", "expired",
            "refused_by_agent")
FIELDS = """a.id, a.host_id, h.hostname, a.agent_id, a.kind, a.params, a.reason, a.origin, a.status, a.proposed_by,
            pu.email AS proposed_by_email, a.proposed_by_ai, a.approved_by, au.email AS approved_by_email,
            a.approved_at, a.sent_at, a.finished_at, a.exit_code, a.output, a.expires_at, a.created_at"""
FROM = """FROM agent_actions a LEFT JOIN hosts h ON h.id = a.host_id AND h.org_id = a.org_id
          LEFT JOIN users pu ON pu.id = a.proposed_by LEFT JOIN users au ON au.id = a.approved_by"""


async def _load(conn, org_id: int, action_id: int) -> dict:
    row = await fetch_one(conn, f"SELECT {FIELDS} {FROM} WHERE a.org_id = %s AND a.id = %s", (org_id, action_id))
    if row is None:
        raise not_found("action introuvable")
    return row


def _origin(value) -> dict:
    if value is None:
        return {}
    if not isinstance(value, dict) or len(value) > 10:
        raise bad_request("origin doit être un objet ({\"monitor_id\",\"group\"} ou {\"explain\":true})")
    out = {}
    for k, v in value.items():
        if k not in ("monitor_id", "group", "explain", "incident_id", "kind"):
            raise bad_request(f"origin : clé inconnue {k}")
        if isinstance(v, str) and len(v) > 1000:
            raise bad_request("origin : valeur trop longue")
        out[k] = v
    return out


@router.post("/actions")
async def propose_action(request: Request, data: dict = Body(...), p: Principal = Depends(member)):
    host_id = data.get("host_id")
    if isinstance(host_id, bool) or not isinstance(host_id, int):
        raise bad_request("host_id (entier) est obligatoire")
    reason = data.get("reason") or ""
    if not isinstance(reason, str) or len(reason) > 2000:
        raise bad_request("reason : chaîne de 2 000 caractères au plus")
    origin = _origin(data.get("origin"))
    async with rt().db.writer() as conn:
        host = await host_agent(conn, p.org_id, host_id=host_id)
        if host is None:
            raise not_found("hôte introuvable")
        try:
            params = P.validate_params(data.get("kind"), data.get("params"), host["os"])
        except ValueError as e:
            raise bad_request(str(e), "invalid_action")
        if host["agent_id"] is None or host.get("kind") != "agent":
            raise ApiError(409, "capability_missing", "cet hôte n'a pas d'agent qui exécute des actions")
        capable, why = P.capable(host["capabilities"], data["kind"], params)
        if not capable:
            raise ApiError(409, "capability_missing", why)
        row = await fetch_one(conn, """
            INSERT INTO agent_actions (org_id, host_id, agent_id, kind, params, reason, origin, proposed_by, proposed_by_ai)
            VALUES (%s, %s, %s, %s, %s::jsonb, %s, %s::jsonb, %s, %s) RETURNING id
        """, (p.org_id, host_id, host["agent_id"], data["kind"], jdump(params), reason, jdump(origin), p.user_id,
              bool(origin.get("explain"))))
        action = await _load(conn, p.org_id, row["id"])
        await audit.record(conn, org_id=p.org_id, principal=p, action="propose", object_type="action",
                           object_id=row["id"],
                           summary=f"{P.label(data['kind'], params, host['hostname'], 'fr')} : proposé",
                           after={"kind": data["kind"], "params": params, "host_id": host_id, "reason": reason,
                                  "origin": origin}, request=request)
    return ok(action, 201)


@router.post("/actions/{action_id}/approve")
async def approve_action(action_id: int, request: Request, p: Principal = Depends(admin)):
    async with rt().db.writer() as conn:
        row = await fetch_one(conn, """
            UPDATE agent_actions SET status = 'approved', approved_by = %s, approved_at = now(),
                   expires_at = now() + interval '10 minutes'
             WHERE org_id = %s AND id = %s AND status = 'pending_approval' AND expires_at > now()
            RETURNING id
        """, (p.user_id, p.org_id, action_id))
        current = await _load(conn, p.org_id, action_id)
        if row is None:
            raise ApiError(409, "not_pending", f"action à l'état {current['status']} : rien à approuver")
        await audit.record(conn, org_id=p.org_id, principal=p, action="approve", object_type="action",
                           object_id=action_id,
                           summary=f"{P.label(current['kind'], current['params'], current['hostname'] or '?', 'fr')} : approuvé",
                           after={"kind": current["kind"], "params": current["params"]}, request=request)
    return ok(current)


@router.post("/actions/{action_id}/reject")
async def reject_action(action_id: int, request: Request, data: dict | None = Body(None),
                        p: Principal = Depends(member)):
    """Rejet par un administrateur, ou retrait par celui qui l'a proposée."""
    comment = (data or {}).get("comment") or ""
    if not isinstance(comment, str) or len(comment) > 2000:
        raise bad_request("comment : chaîne de 2 000 caractères au plus")
    async with rt().db.writer() as conn:
        current = await _load(conn, p.org_id, action_id)
        if not p.at_least("admin") and current["proposed_by"] != p.user_id:
            raise forbidden("rôle « admin » requis (ou être l'auteur de la proposition)")
        row = await fetch_one(conn, """
            UPDATE agent_actions SET status = 'rejected', finished_at = now(),
                   output = CASE WHEN %s <> '' THEN %s ELSE output END
             WHERE org_id = %s AND id = %s AND status = 'pending_approval' RETURNING id
        """, (comment, comment, p.org_id, action_id))
        if row is None:
            raise ApiError(409, "not_pending", f"action à l'état {current['status']} : rien à rejeter")
        current = await _load(conn, p.org_id, action_id)
        await audit.record(conn, org_id=p.org_id, principal=p, action="reject", object_type="action",
                           object_id=action_id,
                           summary=f"{P.label(current['kind'], current['params'], current['hostname'] or '?', 'fr')} : rejeté",
                           after={"comment": comment}, request=request)
    return ok(current)


@router.get("/actions")
async def list_actions(status: str | None = None, host_id: int | None = None,
                       limit: int = Query(100, ge=1, le=1000), p: Principal = Depends(viewer)):
    where, params = ["a.org_id = %s"], [p.org_id]
    if status:
        wanted = [s.strip() for s in status.split(",") if s.strip()]
        if not all(s in STATUSES for s in wanted):
            raise bad_request(f"status attendu : {'|'.join(STATUSES)}")
        where.append("a.status = ANY(%s::text[])")
        params.append(wanted)
    if host_id is not None:
        where.append("a.host_id = %s")
        params.append(host_id)
    async with rt().db.reader() as conn:
        rows = await fetch_all(conn, f"SELECT {FIELDS} {FROM} WHERE {' AND '.join(where)} "
                                     "ORDER BY a.created_at DESC, a.id DESC LIMIT %s", params + [limit])
    return ok(rows)


@router.get("/actions/{action_id}")
async def get_action(action_id: int, p: Principal = Depends(viewer)):
    async with rt().db.reader() as conn:
        return ok(await _load(conn, p.org_id, action_id))
