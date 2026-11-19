"""Flotte d'agents (contrat § 27) : état, versions, mise à jour signée posée par `target_version`.

`viewer` lit, `admin` lance ou annule une mise à jour. L'agent lit sa version visée dans le canal
de contrôle (`GET /agent/control`) et ne se met à jour que si sa conf le permet (`auto_update`).
"""
import os
import re
import uuid

from fastapi import APIRouter, Body, Depends, Request

from .. import audit
from ..auth.principal import Principal, admin, viewer
from ..db import fetch_all, fetch_one
from ..errors import bad_request
from ..runtime import rt
from ..util import TTLCache
from .common import ok
from .infra import HOST_DOWN_S

router = APIRouter()

VERSION_RE = re.compile(r"^\d{1,4}\.\d{1,4}\.\d{1,6}(?:[-+][0-9A-Za-z.\-]{1,40})?$")
OS_VALUES = ("linux", "windows")

FLEET_SQL = """
    SELECT a.id AS agent_id, a.host_id, h.hostname, a.version, a.os, a.arch, a.last_seen,
           CASE WHEN a.last_seen > now() - make_interval(secs => %s) THEN 'up' ELSE 'down' END AS status,
           a.capabilities, a.target_version, a.update_status, a.status -> 'collectors' AS collectors,
           a.status -> 'errors' AS errors, a.last_ip
      FROM agents a LEFT JOIN hosts h ON h.id = a.host_id AND h.org_id = a.org_id
     WHERE a.org_id = %s
     ORDER BY h.hostname NULLS LAST, a.id
"""


@router.get("/fleet")
async def fleet(p: Principal = Depends(viewer)):
    async with rt().db.reader() as conn:
        rows = await fetch_all(conn, FLEET_SQL, (HOST_DOWN_S, p.org_id))
    for r in rows:
        r["collectors"] = r["collectors"] or {}
        r["capabilities"] = r["capabilities"] or {}
        r["update_status"] = r["update_status"] or {}
    return ok(rows)


@router.get("/fleet/summary")
async def fleet_summary(p: Principal = Depends(viewer)):
    async with rt().db.reader() as conn:
        versions = await fetch_all(conn, """
            SELECT coalesce(version, '?') AS version, count(*) AS count FROM agents WHERE org_id = %s
             GROUP BY 1 ORDER BY count DESC, 1
        """, (p.org_id,))
        systems = await fetch_all(conn, """
            SELECT coalesce(os, '?') AS os, coalesce(arch, '?') AS arch, count(*) AS count FROM agents
             WHERE org_id = %s GROUP BY 1, 2 ORDER BY count DESC, 1, 2
        """, (p.org_id,))
        totals = await fetch_one(conn, """
            SELECT count(*) AS total,
                   count(*) FILTER (WHERE last_seen > now() - make_interval(secs => %s)) AS up,
                   count(*) FILTER (WHERE target_version IS NOT NULL AND target_version IS DISTINCT FROM version)
                       AS upgrading,
                   count(*) FILTER (WHERE update_status ->> 'state' = 'failed') AS update_failed
              FROM agents WHERE org_id = %s
        """, (HOST_DOWN_S, p.org_id))
    totals = totals or {"total": 0, "up": 0, "upgrading": 0, "update_failed": 0}
    return ok({"total": totals["total"], "up": totals["up"], "down": totals["total"] - totals["up"],
               "upgrading": totals["upgrading"], "update_failed": totals["update_failed"],
               "versions": versions, "os": systems, "latest": published_version(rt().settings.install_dir),
               "latest_version": rt().settings.agent_version})


_published = TTLCache(60)


def published_version(install_dir: str) -> str | None:
    """Version publiée de l'agent : première ligne de `<install>/version.txt` (relue au plus chaque
    minute), None si le fichier manque ou ne porte pas une version."""
    cached = _published.get(install_dir, False)
    if cached is not False:
        return cached
    try:
        with open(os.path.join(install_dir, "version.txt"), encoding="utf-8") as f:
            text = f.read(200).strip().splitlines()
        value = text[0].strip().lstrip("v") if text else ""
        version = value if VERSION_RE.match(value) else None
    except OSError:
        version = None
    _published.set(install_dir, version)
    return version


def _selection(data: dict) -> tuple[str, list]:
    """Condition SQL (et paramètres) des agents visés : `agent_ids` ou `all: true`, filtrés par `os`."""
    where, params = [], []
    ids = data.get("agent_ids")
    if data.get("all") is True:
        if ids not in (None, []):
            raise bad_request("agent_ids et all s'excluent")
    elif isinstance(ids, list) and ids:
        if len(ids) > 10_000:
            raise bad_request("10 000 agents au plus")
        try:
            clean = sorted({str(uuid.UUID(str(i))) for i in ids})
        except ValueError:
            raise bad_request("agent_ids : liste d'UUID")
        where.append("id = ANY(%s::uuid[])")
        params.append(clean)
    else:
        raise bad_request("agent_ids (liste) ou all: true est obligatoire")
    os_ = data.get("os")
    if os_ not in (None, "", "null"):
        if os_ not in OS_VALUES:
            raise bad_request(f"os attendu : {'|'.join(OS_VALUES)} ou null")
        where.append("os = %s")
        params.append(os_)
    return (" AND " + " AND ".join(where)) if where else "", params


@router.post("/fleet/upgrade")
async def fleet_upgrade(request: Request, data: dict = Body(...), p: Principal = Depends(admin)):
    # Sans version : la dernière publiée (version.txt), pour ne jamais faire redescendre la flotte
    # vers la valeur par défaut de la configuration quand elle retarde sur la publication.
    version = (data.get("version") or published_version(rt().settings.install_dir)
               or rt().settings.agent_version)
    if not isinstance(version, str) or not VERSION_RE.match(version):
        raise bad_request("version attendue de la forme 0.4.0")
    cond, params = _selection(data)
    async with rt().db.writer() as conn:
        rows = await fetch_all(conn, f"UPDATE agents SET target_version = %s WHERE org_id = %s{cond} RETURNING id",
                               [version, p.org_id] + params)
        ids = [str(r["id"]) for r in rows]
        await audit.record(conn, org_id=p.org_id, principal=p, action="upgrade", object_type="fleet",
                           object_id=version, summary=f"mise à jour vers {version} : {len(ids)} agent(s)",
                           after={"version": version, "agent_ids": ids[:200], "all": data.get("all") is True,
                                  "os": data.get("os")}, request=request)
    return ok({"updated": len(ids), "agent_ids": ids, "version": version})


@router.post("/fleet/cancel-upgrade")
async def fleet_cancel_upgrade(request: Request, data: dict = Body(...), p: Principal = Depends(admin)):
    cond, params = _selection(data)
    async with rt().db.writer() as conn:
        rows = await fetch_all(conn, f"UPDATE agents SET target_version = NULL WHERE org_id = %s{cond} "
                                     f"AND target_version IS NOT NULL RETURNING id", [p.org_id] + params)
        ids = [str(r["id"]) for r in rows]
        await audit.record(conn, org_id=p.org_id, principal=p, action="cancel_upgrade", object_type="fleet",
                           summary=f"mise à jour annulée : {len(ids)} agent(s)",
                           after={"agent_ids": ids[:200], "all": data.get("all") is True, "os": data.get("os")},
                           request=request)
    return ok({"updated": len(ids), "agent_ids": ids})
