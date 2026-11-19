"""Inventaire matériel et logiciel (contrat § 15) : liste du parc, fiche d'un hôte, recherche d'un
paquet sur tout le parc (versions comparées à la façon de dpkg), synthèse."""
import base64
import json

from fastapi import APIRouter, Depends, Query

from ..auth.principal import Principal, viewer
from ..db import fetch_all, fetch_one
from ..errors import bad_request, not_found
from ..inventory import versions
from ..runtime import rt
from ..util import like_escape
from .common import ok, parse_tag_filter

router = APIRouter()

LIST_FIELDS = """
    h.id AS host_id, h.hostname, h.os, h.platform, h.platform_version,
    i.hardware ->> 'vendor' AS vendor, i.hardware ->> 'product' AS product, i.hardware ->> 'serial' AS serial,
    coalesce(i.hardware -> 'cpu', to_jsonb(h.cpu_model)) AS cpu, h.mem_total, i.packages_count,
    (i.updates ->> 'pending')::int AS updates_pending, (i.updates ->> 'security')::int AS updates_security,
    (i.os ->> 'reboot_required')::boolean AS reboot_required, i.ts
"""
MAX_PACKAGE_MATCHES = 5000


def _host_filters(q: str, os_: str, tag: list[str], params: list) -> list[str]:
    where = []
    if q.strip():
        pattern = "%" + like_escape(q.strip()) + "%"
        where.append("(h.hostname ILIKE %s OR i.hardware ->> 'vendor' ILIKE %s OR i.hardware ->> 'product' ILIKE %s "
                     "OR i.hardware ->> 'serial' ILIKE %s)")
        params += [pattern] * 4
    if os_.strip():
        where.append("(h.os = %s OR h.platform ILIKE %s OR i.os ->> 'name' ILIKE %s)")
        params += [os_.strip(), like_escape(os_.strip()), like_escape(os_.strip())]
    for t in tag:
        key, value = parse_tag_filter(t)
        if key == "host":
            where.append("h.hostname = %s")
            params.append(value)
        else:
            where.append("h.tags @> %s::jsonb")
            params.append(json.dumps({key: value}, ensure_ascii=False))
    return where


@router.get("/inventory")
async def list_inventory(q: str = "", os: str = "", tag: list[str] = Query(default=[]),
                         p: Principal = Depends(viewer)):
    params: list = [p.org_id]
    where = ["i.org_id = %s"] + _host_filters(q, os, tag, params)
    async with rt().db.reader() as conn:
        rows = await fetch_all(conn, f"""
            SELECT {LIST_FIELDS}
              FROM host_inventory i JOIN hosts h ON h.id = i.host_id AND h.org_id = i.org_id
             WHERE {' AND '.join(where)}
             ORDER BY h.hostname LIMIT 5000
        """, params)
    return ok(rows)


def _encode_cursor(name: str, arch: str) -> str:
    return base64.urlsafe_b64encode(json.dumps([name, arch]).encode()).decode().rstrip("=")


def _decode_cursor(cursor: str) -> tuple[str, str]:
    try:
        name, arch = json.loads(base64.urlsafe_b64decode(cursor + "=" * (-len(cursor) % 4)))
        return str(name), str(arch)
    except (ValueError, TypeError):
        raise bad_request("curseur invalide")


@router.get("/hosts/{host_id}/inventory")
async def host_inventory(host_id: int, q: str = "", limit: int = Query(100, ge=1, le=1000), cursor: str = "",
                         p: Principal = Depends(viewer)):
    params: list = [p.org_id, host_id]
    cond = ""
    if q.strip():
        cond += " AND (name ILIKE %s OR version ILIKE %s)"
        pattern = "%" + like_escape(q.strip()) + "%"
        params += [pattern, pattern]
    if cursor:
        cond += " AND (name, arch) > (%s, %s)"
        params += list(_decode_cursor(cursor))
    params.append(limit + 1)
    async with rt().db.reader() as conn:
        inv = await fetch_one(conn, """
            SELECT h.id AS host_id, h.hostname, h.os AS host_os, h.platform, h.platform_version, h.kernel,
                   h.cpu_model, h.cpu_cores, h.mem_total, i.ts, i.hardware, i.os, i.updates, i.packages_count
              FROM hosts h LEFT JOIN host_inventory i ON i.host_id = h.id AND i.org_id = h.org_id
             WHERE h.org_id = %s AND h.id = %s
        """, (p.org_id, host_id))
        if inv is None:
            raise not_found("hôte introuvable")
        packages = await fetch_all(conn, f"""
            SELECT name, version, arch, source FROM host_packages
             WHERE org_id = %s AND host_id = %s {cond} ORDER BY name, arch LIMIT %s
        """, params)
    next_cursor = None
    if len(packages) > limit:
        packages = packages[:limit]
        next_cursor = _encode_cursor(packages[-1]["name"], packages[-1]["arch"])
    inv["hardware"] = inv["hardware"] or {}
    inv["os"] = inv["os"] or {}
    inv["updates"] = inv["updates"] or {}
    inv["packages_count"] = inv["packages_count"] or 0
    return ok({**inv, "packages": packages, "next_cursor": next_cursor})


@router.get("/inventory/packages")
async def find_packages(name: str = "", version_lt: str | None = None, version_le: str | None = None,
                        version_gt: str | None = None, version_ge: str | None = None, version: str | None = None,
                        p: Principal = Depends(viewer)):
    name = name.strip()
    if not name or len(name) > 300:
        raise bad_request("name est obligatoire (nom exact, ou préfixe terminé par *)")
    if name.endswith("*"):
        cond, value = "s.name LIKE %s", like_escape(name[:-1]) + "%"
    else:
        cond, value = "s.name = %s", name
    async with rt().db.reader() as conn:
        rows = await fetch_all(conn, f"""
            SELECT s.host_id, h.hostname, s.name, s.version, s.arch, s.source
              FROM host_packages s JOIN hosts h ON h.id = s.host_id AND h.org_id = s.org_id
             WHERE s.org_id = %s AND {cond}
             ORDER BY h.hostname, s.name, s.arch LIMIT 50000
        """, (p.org_id, value))
    out = [r for r in rows if versions.matches(r["version"], lt=version_lt, le=version_le, gt=version_gt,
                                               ge=version_ge, eq=version)]
    return ok(out[:MAX_PACKAGE_MATCHES])


@router.get("/inventory/summary")
async def inventory_summary(p: Principal = Depends(viewer)):
    async with rt().db.reader() as conn:
        systems = await fetch_all(conn, """
            SELECT coalesce(i.os ->> 'name', h.platform, h.os) AS name,
                   coalesce(i.os ->> 'version', h.platform_version) AS version, count(*) AS count
              FROM host_inventory i JOIN hosts h ON h.id = i.host_id AND h.org_id = i.org_id
             WHERE i.org_id = %s GROUP BY 1, 2 ORDER BY count DESC, 1, 2
        """, (p.org_id,))
        vendors = await fetch_all(conn, """
            SELECT i.hardware ->> 'vendor' AS vendor, count(*) AS count FROM host_inventory i
             WHERE i.org_id = %s GROUP BY 1 ORDER BY count DESC, 1
        """, (p.org_id,))
        totals = await fetch_one(conn, """
            SELECT count(*) AS hosts,
                   count(*) FILTER (WHERE (i.updates ->> 'pending')::int > 0) AS updates_pending_hosts,
                   count(*) FILTER (WHERE (i.updates ->> 'security')::int > 0) AS security_updates_hosts,
                   count(*) FILTER (WHERE (i.os ->> 'reboot_required')::boolean) AS reboot_required_hosts
              FROM host_inventory i WHERE i.org_id = %s
        """, (p.org_id,))
    totals = totals or {}
    return ok({"os": systems, "vendors": vendors, "hosts": totals.get("hosts", 0),
               "updates_pending_hosts": totals.get("updates_pending_hosts", 0),
               "security_updates_hosts": totals.get("security_updates_hosts", 0),
               "reboot_required_hosts": totals.get("reboot_required_hosts", 0)})
