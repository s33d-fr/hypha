"""Jumeaux (§ 51) : une ressource lue par un connecteur (§ 34-35, serveur OVH, VM vSphere,
instance cloud) et l'hôte d'un agent HYPHA qui désignent la même machine. Rapprochement calculé à la
lecture, jamais écrit : il suit les changements d'adresse sans rien à défaire.

Même machine si :
1. leurs numéros de série concordent (VMware donne à l'invité « VMware-… », que l'agent lit) et
   n'appartiennent qu'à elles (les séries génériques des cartes mères sont écartées) ;
2. sinon, elles partagent une adresse que ne porte aucun autre hôte à agent, et cette adresse est
   publique, ou le nom court de la ressource est celui de l'hôte (une adresse privée revient d'un
   site à l'autre : seule, elle ne suffit pas).
"""
import ipaddress
from typing import Any, Iterable

from ..db import fetch_all

# Séries d'usine sans valeur d'identité (relevées sur le parc et chez les fabricants courants).
GENERIC_SERIALS = {
    "", "0", "00000000", "0123456789", "1234567890", "123456789", "none", "null", "n/a", "na",
    "not specified", "not applicable", "default string", "to be filled by o.e.m.", "system serial number",
    "chassis serial number", "base board serial number", "serial", "xxxxxxxx",
}

LOOKBACK = "7 days"


def _serial(v: Any) -> str:
    s = " ".join(str(v or "").split()).lower()
    return "" if s in GENERIC_SERIALS or len(s) < 5 or len(set(s)) < 3 else s


def _short(name: Any) -> str:
    return str(name or "").strip().lower().split(".")[0]


def _ips(values: Any) -> set:
    out = set()
    for v in values or []:
        try:
            ip = ipaddress.ip_address(str(v).strip().split("/")[0])
        except ValueError:
            continue
        if ip.is_loopback or ip.is_link_local or ip.is_unspecified or ip.is_multicast:
            continue
        out.add(ip)
    return out


def match(resources: Iterable[dict], agents: Iterable[dict]) -> dict[int, int]:
    """Ressource → hôte à agent. Chaque entrée : id, hostname, ip_addresses, serial, device."""
    agents = list(agents)
    by_serial: dict[str, list[int]] = {}
    by_ip: dict[Any, list[int]] = {}
    names = {}
    for a in agents:
        names[a["id"]] = _short(a.get("hostname"))
        s = _serial(a.get("serial"))
        if s:
            by_serial.setdefault(s, []).append(a["id"])
        for ip in _ips(a.get("ip_addresses")):
            by_ip.setdefault(ip, []).append(a["id"])
    out: dict[int, int] = {}
    for r in resources:
        dev = r.get("device") or {}
        s = _serial(dev.get("serial") or r.get("serial"))
        if s and len(by_serial.get(s, [])) == 1:
            out[r["id"]] = by_serial[s][0]
            continue
        owners = {ip: by_ip[ip][0] for ip in _ips(r.get("ip_addresses")) if len(by_ip.get(ip, [])) == 1}
        ids = set(owners.values())
        if len(ids) != 1:
            continue
        aid = ids.pop()
        own_names = {_short(r.get("hostname")), _short(dev.get("display_name"))} - {""}
        if any(ip.is_global for ip in owners) or names.get(aid) in own_names:
            out[r["id"]] = aid
    return out


async def load_twins(conn, org_id: int) -> tuple[dict[int, int], dict[int, dict]]:
    """(ressource → hôte à agent, fiches des hôtes concernés) pour toute l'organisation."""
    rows = await fetch_all(conn, f"""
        SELECT h.id, h.kind, h.hostname, h.ip_addresses, h.device, i.hardware->>'serial' AS serial
          FROM hosts h LEFT JOIN host_inventory i ON i.host_id = h.id AND i.org_id = h.org_id
         WHERE h.org_id = %s AND h.kind IN ('agent', 'cloud_resource') AND h.last_seen > now() - interval '{LOOKBACK}'
    """, (org_id,))
    links = match([r for r in rows if r["kind"] == "cloud_resource"], [r for r in rows if r["kind"] == "agent"])
    return links, {r["id"]: r for r in rows}


def brief(r: dict) -> dict:
    """Résumé d'une ressource pour la fiche de son jumeau."""
    d = r.get("device") or {}
    return {"id": r["id"], "hostname": r["hostname"], "provider": d.get("provider"), "type": d.get("type"),
            "power_state": d.get("power_state"), "display_name": d.get("display_name"), "region": d.get("region"),
            "service": d.get("service"), "parent": d.get("parent"), "model": d.get("model")}


def annotate(hosts: list[dict], links: dict[int, int], index: dict[int, dict]) -> None:
    """same_as sur les ressources, seen_by sur les hôtes à agent."""
    seen: dict[int, list[dict]] = {}
    for rid, aid in links.items():
        if rid in index:
            seen.setdefault(aid, []).append(brief(index[rid]))
    for h in hosts:
        aid = links.get(h["id"])
        if aid is not None and aid in index:
            h["same_as"] = {"id": aid, "hostname": index[aid]["hostname"]}
        if h["id"] in seen:
            h["seen_by"] = sorted(seen[h["id"]], key=lambda x: (x["provider"] or "", x["hostname"]))
