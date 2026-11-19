"""Actions à distance côté base (contrat § 28) : hôte et capacités de son agent, propositions de
l'IA retenues, expiration par le worker. Chaque requête filtre l'organisation."""
import logging

from .. import audit
from ..db import fetch_all, fetch_one
from . import propose as P

log = logging.getLogger("hypha.actions")

HOST_SQL = """
    SELECT h.id AS host_id, h.hostname, h.os, h.kind, h.agent_id, a.capabilities
      FROM hosts h LEFT JOIN agents a ON a.id = h.agent_id AND a.org_id = h.org_id
     WHERE h.org_id = %s AND {cond}
     ORDER BY h.last_seen DESC LIMIT 1
"""


async def host_agent(conn, org_id: int, host_id: int | None = None, hostname: str | None = None) -> dict | None:
    if host_id is not None:
        return await fetch_one(conn, HOST_SQL.format(cond="h.id = %s"), (org_id, host_id))
    if hostname:
        return await fetch_one(conn, HOST_SQL.format(cond="h.hostname = %s"), (org_id, hostname))
    return None


async def known_services(conn, org_id: int, host_id: int) -> tuple[set[str], set[str], set[str]]:
    """Unités systemd et services Windows vus par les contrôles (2 jours), processus du dernier instantané."""
    units, services, procs = set(), set(), set()
    for r in await fetch_all(conn, """
        SELECT DISTINCT check_name, tags ->> 'unit' AS unit, tags ->> 'windows_service' AS service
          FROM check_runs
         WHERE org_id = %s AND host_id = %s AND check_name IN ('systemd.unit', 'windows.service')
           AND ts > now() - interval '2 days'
    """, (org_id, host_id)):
        if r["unit"]:
            units.add(r["unit"])
        if r["service"]:
            services.add(r["service"].lower())
    for r in await fetch_all(conn, """
        SELECT DISTINCT name FROM process_snapshots
         WHERE org_id = %s AND host_id = %s AND ts = (
               SELECT max(ts) FROM process_snapshots
                WHERE org_id = %s AND host_id = %s AND ts > now() - interval '3 days')
    """, (org_id, host_id, org_id, host_id)):
        procs.add((r["name"] or "").lower())
    return units, services, procs


def exists(gesture: dict, units: set[str], services: set[str], procs: set[str]) -> bool:
    if "unit" in gesture["params"]:
        unit = gesture["params"]["unit"]
        base = unit.rsplit(".", 1)[0].split("@")[0].lower()
        return unit in units or base in procs
    name = gesture["params"]["service"].lower()
    return name in services or name in procs or name + ".exe" in procs


async def proposals(conn, org_id: int, explanation: str, lang: str, hostname: str | None,
                    host_id: int | None = None) -> list[dict]:
    """Gestes tirés du « Correctif », retenus si l'agent de l'hôte sait les faire et que l'unité existe."""
    gestures = P.find_gestures(P.fix_section(explanation))
    if not gestures or not (hostname or host_id):
        return []
    host = await host_agent(conn, org_id, host_id, hostname)
    if host is None or host["agent_id"] is None or host.get("kind") != "agent":
        return []
    os_name = (host["os"] or "").lower()
    candidates = [g for g in gestures if g["os"] == ("windows" if os_name == "windows" else "linux")]
    candidates = [g for g in candidates if P.capable(host["capabilities"], g["kind"], g["params"])[0]]
    if not candidates:
        return []
    units, services, procs = await known_services(conn, org_id, host["host_id"])
    out = []
    for g in candidates:
        if exists(g, units, services, procs):
            out.append({"kind": g["kind"], "params": g["params"], "reason": P.reason(g["command"], lang),
                        "label": P.label(g["kind"], g["params"], host["hostname"], lang),
                        "host_id": host["host_id"], "hostname": host["hostname"]})
    return out[:5]


async def expire(db) -> int:
    """Expire les actions restées 1 h sans approbation, ou 10 min après approbation sans prise par l'agent."""
    async with db.writer() as conn:
        rows = await fetch_all(conn, """
            UPDATE agent_actions SET status = 'expired', finished_at = now()
             WHERE status IN ('pending_approval', 'approved') AND expires_at < now()
            RETURNING id, org_id, kind, params, host_id, approved_at IS NOT NULL AS was_approved
        """)
        for r in rows:
            why = "approuvée mais jamais prise par l'agent" if r["was_approved"] else "jamais approuvée"
            await audit.record(conn, org_id=r["org_id"], principal=None, action="expire", object_type="action",
                               object_id=r["id"], summary=f"action {r['kind']} expirée ({why})",
                               after={"kind": r["kind"], "params": r["params"], "host_id": r["host_id"]})
    if rows:
        log.info("%s action(s) à distance expirée(s)", len(rows))
    return len(rows)
