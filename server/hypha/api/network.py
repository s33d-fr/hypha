"""Carte du réseau (contrat § 26) : hôtes et équipements de l'organisation, et leurs connexions.

Une arête relie un hôte à un autre quand une connexion sortante vise une adresse de cet autre hôte
(`hosts.ip_addresses`). Une adresse portée par plusieurs hôtes (pont docker, adresse de bouclage)
ne désigne personne et n'est pas retenue. Les autres destinations publiques ou privées vont dans
`external`. Un équipement réseau (§ 14) est un nœud, relié à l'hôte de l'agent qui l'interroge.
"""
import ipaddress
import json

from fastapi import APIRouter, Depends, Query

from ..auth.principal import Principal, viewer
from ..db import fetch_all
from ..runtime import rt
from ..util import ms_to_dt
from .common import ok, parse_tag_filter, time_range
from .infra import HOST_DOWN_S

router = APIRouter()

DEVICE_DOWN_S = 4 * HOST_DOWN_S          # un équipement est interrogé chaque minute au plus souvent
MAX_EXTERNAL = 100


def _usable(addr: str) -> bool:
    try:
        ip = ipaddress.ip_address(addr.split("%")[0])
    except ValueError:
        return False
    return not (ip.is_loopback or ip.is_unspecified or ip.is_link_local or ip.is_multicast)


def address_owners(hosts: list[dict]) -> dict[str, int]:
    """Adresse → hôte, pour les adresses qui n'appartiennent qu'à un seul hôte."""
    owners: dict[str, set] = {}
    for h in hosts:
        ips = h.get("ip_addresses") or []
        if isinstance(ips, str):
            ips = json.loads(ips)
        for ip in ips:
            if isinstance(ip, str) and _usable(ip):
                owners.setdefault(ip.split("/")[0], set()).add(h["id"])
    return {ip: next(iter(ids)) for ip, ids in owners.items() if len(ids) == 1}


def build_network(hosts: list[dict], connections: list[dict], keep: set[int] | None = None) -> dict:
    owners = address_owners(hosts)
    visible = {h["id"] for h in hosts} if keep is None else keep
    nodes = [{"id": h["id"], "hostname": h["hostname"], "kind": h.get("kind") or "agent", "os": h.get("os"),
              "state": h.get("state"), "tags": h.get("tags") or {}, "via_host_id": h.get("via_host_id"),
              "address": (h.get("device") or {}).get("address")}
             for h in hosts if h["id"] in visible]
    edges: dict[tuple, dict] = {}
    external: dict[tuple, int] = {}
    for c in connections:
        src = c["host_id"]
        if src not in visible or not c.get("raddr"):
            continue
        dst = owners.get(c["raddr"])
        if dst is not None:
            if dst == src or dst not in visible:
                continue
            key = (src, dst, c.get("rport"), c.get("proto"), c.get("process"))
            e = edges.get(key)
            if e is None:
                edges[key] = {"from": src, "to": dst, "port": c.get("rport"), "proto": c.get("proto"),
                              "process": c.get("process"), "count": int(c.get("count") or 0)}
            else:
                e["count"] += int(c.get("count") or 0)
        elif _usable(c["raddr"]):
            k = (c["raddr"], c.get("rport"))
            external[k] = external.get(k, 0) + int(c.get("count") or 0)
    for h in hosts:
        if h.get("kind") in ("network_device", "cloud_resource") and h.get("via_host_id") in visible and h["id"] in visible:
            snmp = h.get("kind") == "network_device"
            edges[(h["via_host_id"], h["id"], 161 if snmp else 443, "snmp" if snmp else "api", "hypha-agent")] = {
                "from": h["via_host_id"], "to": h["id"], "port": 161 if snmp else 443, "proto": "snmp" if snmp else "api",
                "process": "hypha-agent", "count": 1}
    ext = sorted(({"addr": a, "port": p, "count": n} for (a, p), n in external.items()), key=lambda x: -x["count"])
    return {"nodes": sorted(nodes, key=lambda n: (n["kind"], n["hostname"] or "")),
            "edges": sorted(edges.values(), key=lambda e: (e["from"], e["to"], e["port"] or 0)),
            "external": ext[:MAX_EXTERNAL]}


@router.get("/network/map")
async def network_map(from_: int | None = Query(None, alias="from"), to: int | None = None,
                      tag: list[str] = Query(default=[]), p: Principal = Depends(viewer)):
    start, end = time_range(from_, to)
    filters = [parse_tag_filter(t) for t in tag]
    async with rt().db.reader() as conn:
        hosts = await fetch_all(conn, """
            SELECT id, hostname, kind, os, tags, ip_addresses, via_host_id, device,
                   CASE WHEN last_seen > now() - make_interval(secs => CASE WHEN kind <> 'agent' THEN %s ELSE %s END)
                        THEN 'up' ELSE 'down' END AS state
              FROM hosts WHERE org_id = %s ORDER BY hostname LIMIT 5000
        """, (DEVICE_DOWN_S, HOST_DOWN_S, p.org_id))
        connections = await fetch_all(conn, """
            SELECT host_id, raddr, rport, proto, process, max(count) AS count FROM net_connections
             WHERE org_id = %s AND ts >= %s AND ts < %s AND direction = 'outgoing'
             GROUP BY 1, 2, 3, 4, 5 ORDER BY count DESC LIMIT 20000
        """, (p.org_id, ms_to_dt(start), ms_to_dt(end)))
    keep = None
    if filters:
        def match(h):
            tags = h.get("tags") or {}
            return all((h["hostname"] == v) if k == "host" else (tags.get(k) == v) for k, v in filters)
        keep = {h["id"] for h in hosts if match(h)}
        keep |= {h["id"] for h in hosts if h.get("kind") != "agent" and h.get("via_host_id") in keep}
    return ok(build_network(hosts, connections, keep))
