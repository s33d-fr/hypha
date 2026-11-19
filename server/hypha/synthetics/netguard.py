"""Garde réseau des sorties du serveur (tests synthétiques, webhooks).

En SaaS, une organisation ne doit pas pouvoir faire sonder par HYPHA le réseau interne d'S33D
(réseaux privés, 127.0.0.1, métadonnées 169.254.x…) : toute adresse non publique est refusée, sauf
si l'auto-hébergé l'autorise (HYPHA_ALLOW_PRIVATE_TARGETS) ou si l'organisation porte le réglage
`allow_private_targets`. Risque restant : un nom qui change d'adresse entre la vérification et la
connexion (rebinding DNS) n'est pas couvert.
"""
import asyncio
import ipaddress
import socket
import time
from urllib.parse import urlsplit


class TargetRefused(Exception):
    pass


def is_public(addr: str) -> bool:
    try:
        ip = ipaddress.ip_address(addr.split("%")[0])
    except ValueError:
        return False
    if ip.version == 6 and ip.ipv4_mapped:
        ip = ip.ipv4_mapped
    return ip.is_global


async def resolve(host: str, port: int | None, allow_private: bool) -> tuple[list[str], float]:
    """Adresses du nom (et durée de résolution en ms) ; lève TargetRefused si une adresse est privée."""
    loop = asyncio.get_running_loop()
    t0 = time.perf_counter()
    infos = await loop.getaddrinfo(host, port, type=socket.SOCK_STREAM)
    dns_ms = (time.perf_counter() - t0) * 1000
    addrs = list(dict.fromkeys(info[4][0] for info in infos))
    if not allow_private:
        for a in addrs:
            if not is_public(a):
                raise TargetRefused(f"adresse non publique refusée : {a}")
    return addrs, dns_ms


async def check_url(url: str, allow_private: bool) -> None:
    parts = urlsplit(url)
    if parts.scheme not in ("http", "https") or not parts.hostname:
        raise TargetRefused("URL http(s) attendue")
    await resolve(parts.hostname, parts.port or (443 if parts.scheme == "https" else 80), allow_private)
