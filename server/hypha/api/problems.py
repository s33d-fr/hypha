"""Vue « Problèmes » (contrat § 17) : tous les groupes hors OK de l'organisation, priorité puis ancienneté."""
from fastapi import APIRouter, Depends, Query

from ..auth.principal import Principal, viewer
from ..db import fetch_all
from ..errors import bad_request
from ..monitors import service_link
from ..runtime import rt
from ..util import now_ms, parse_group_key
from .common import ok

router = APIRouter()

SUPPRESSED = ("downtime", "dependency", "flapping")


def _priorities(raw: str | None) -> list[int] | None:
    if not raw:
        return None
    out = []
    for part in raw.split(","):
        part = part.strip()
        if not part.isdigit() or not 1 <= int(part) <= 5:
            raise bad_request("priority : 1 à 5, séparés par des virgules")
        out.append(int(part))
    return out


def present(rows: list[dict], incidents: dict[str, int], now: int, books: dict[str, str] | None = None) -> list[dict]:
    out = []
    books = books or {}
    for r in rows:
        since = r["since"].timestamp() * 1000 if r.get("since") else None
        acked = None
        if r.get("acked_at"):
            acked = {"by": r.get("acked_by_email") or r.get("acked_by"), "at": r["acked_at"],
                     "comment": r.get("ack_comment") or ""}
        mid, group = r["monitor_id"], r["group"]
        service = service_link.monitor_service({"tags": r.get("tags"), "query": r.get("query")}, group)
        out.append({
            "service": service, "runbook_url": books.get(service or ""),
            "monitor_id": mid, "monitor_name": r["monitor_name"], "type": r["type"], "priority": r["priority"],
            "group": group, "host": parse_group_key(group).get("host"), "state": r["state"], "value": r["value"],
            "since": r["since"], "duration_s": int((now - since) / 1000) if since else None, "acked": acked,
            "suppressed": r.get("suppressed"), "flapping": bool(r.get("flapping")),
            "escalation_step": r.get("escalation_step") or 0, "muted": bool(r.get("muted")),
            "incident_id": incidents.get(f"{mid}|{group}") or incidents.get(str(mid)),
            "last_notified": r.get("last_notified"),
        })
    # Priorité (non classée en dernier), puis le plus ancien d'abord.
    out.sort(key=lambda x: (x["priority"] or 9, x["duration_s"] is None, -(x["duration_s"] or 0)))
    return out


@router.get("/problems")
async def list_problems(priority: str | None = None, acked: bool | None = None,
                        suppressed: str | None = Query(None, max_length=20), q: str = Query("", max_length=200),
                        p: Principal = Depends(viewer)):
    prios = _priorities(priority)
    where, params = ["m.org_id = %s", "m.enabled", "ms.state <> 'OK'"], [p.org_id]
    if prios:
        where.append("m.priority = ANY(%s::smallint[])")
        params.append(prios)
    if acked is True:
        where.append("ms.acked_at IS NOT NULL")
    elif acked is False:
        where.append("ms.acked_at IS NULL")
    if suppressed:
        if suppressed in ("none", "false"):
            where.append("ms.suppressed IS NULL")
        elif suppressed in ("any", "true"):
            where.append("ms.suppressed IS NOT NULL")
        elif suppressed in SUPPRESSED:
            where.append("ms.suppressed = %s")
            params.append(suppressed)
        else:
            raise bad_request("suppressed attendu : downtime|dependency|flapping|none|any")
    if q.strip():
        pattern = "%" + q.strip().replace("\\", "\\\\").replace("%", "\\%").replace("_", "\\_") + "%"
        where.append("(m.name ILIKE %s OR ms.group_key ILIKE %s)")
        params += [pattern, pattern]
    async with rt().db.reader() as conn:
        rows = await fetch_all(conn, f"""
            SELECT m.id AS monitor_id, m.name AS monitor_name, m.type, m.priority, m.tags, m.query,
                   ms.group_key AS "group",
                   ms.state, ms.value, ms.since, ms.last_notified, ms.acked_by, ms.acked_at, ms.ack_comment,
                   u.email AS acked_by_email, ms.suppressed, ms.flapping, ms.escalation_step,
                   (m.muted_until IS NOT NULL AND m.muted_until > now()) AS muted
              FROM monitor_states ms JOIN monitors m ON m.id = ms.monitor_id
              LEFT JOIN users u ON u.id = ms.acked_by
             WHERE {' AND '.join(where)}
             LIMIT 5000
        """, params)
        links = await fetch_all(conn, """
            SELECT l.ref, max(i.id) AS incident_id FROM incident_links l JOIN incidents i ON i.id = l.incident_id
             WHERE i.org_id = %s AND i.status <> 'resolved' AND l.kind = 'monitor'
             GROUP BY l.ref
        """, (p.org_id,)) if rows else []
        books = await service_link.runbooks(conn, p.org_id) if rows else {}
    return ok(present(rows, {r["ref"]: r["incident_id"] for r in links}, now_ms(), books))


@router.get("/problems/count")
async def count_problems(p: Principal = Depends(viewer)):
    """Compteur de la barre latérale : problèmes non acquittés et non supprimés."""
    async with rt().db.reader() as conn:
        rows = await fetch_all(conn, """
            SELECT count(*) FILTER (WHERE ms.acked_at IS NULL AND ms.suppressed IS NULL) AS unacked,
                   count(*) AS total
              FROM monitor_states ms JOIN monitors m ON m.id = ms.monitor_id
             WHERE m.org_id = %s AND m.enabled AND ms.state <> 'OK'
        """, (p.org_id,))
    row = rows[0] if rows else {"unacked": 0, "total": 0}
    return ok({"unacked": row["unacked"] or 0, "total": row["total"] or 0})
