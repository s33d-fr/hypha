"""Exécution des tests synthétiques : http, tcp, ssl, dns, icmp, avec le détail des temps.

Chaque sonde rend un ProbeResult ; aucune ne lève d'exception vers l'appelant (une erreur est
un résultat en échec, avec son message).
"""
import asyncio
import os
import socket
import ssl
import struct
import time
from dataclasses import dataclass, field
from datetime import datetime, timezone

import httpx

from .netguard import TargetRefused, is_public, resolve

USER_AGENT = "HYPHA-Synthetics/0.1 (+https://hypha.s33d.fr)"
MAX_BODY = 1024 * 1024
REDIRECT_CODES = (301, 302, 303, 307, 308)


@dataclass
class ProbeResult:
    ok: bool
    status_code: int | None = None
    latency_ms: float | None = None
    error: str | None = None
    details: dict = field(default_factory=dict)


def _ms(a: float | None, b: float | None) -> float | None:
    return None if a is None or b is None else round((b - a) * 1000, 2)


def split_host_port(target: str, default_port: int | None = None) -> tuple[str, int | None]:
    t = target.strip()
    if "://" in t:
        t = t.split("://", 1)[1]
    t = t.split("/", 1)[0]
    if t.startswith("["):
        host, _, rest = t[1:].partition("]")
        port = rest.lstrip(":")
    elif t.count(":") == 1:
        host, port = t.split(":")
    else:
        host, port = t, ""
    if port and not port.isdigit():
        raise ValueError(f"port invalide : {port}")
    p = int(port) if port else default_port
    if p is not None and not 1 <= p <= 65535:
        raise ValueError(f"port hors bornes : {p}")
    return host, p


def _cert_days(cert: dict | None) -> tuple[float | None, int | None]:
    if not cert or "notAfter" not in cert:
        return None, None
    not_after = ssl.cert_time_to_seconds(cert["notAfter"])
    return round((not_after - time.time()) / 86400, 1), int(not_after * 1000)


def _name(cert_part) -> str | None:
    try:
        return ", ".join(f"{k}={v}" for rdn in cert_part for k, v in rdn)
    except (TypeError, ValueError):
        return None


# ── http ───────────────────────────────────────────────────────────────────────

def _status_ok(code: int, expect, follow: bool) -> bool:
    if expect is None:
        return 200 <= code < 300 if follow else 200 <= code < 400
    if isinstance(expect, list):
        return code in expect
    return code == expect


async def probe_http(target: str, cfg: dict, allow_private: bool) -> ProbeResult:
    method = str(cfg.get("method", "GET")).upper()
    headers = {"User-Agent": USER_AGENT, **(cfg.get("headers") or {})}
    body = cfg.get("body") or None
    timeout = float(cfg.get("timeout_s", 10))
    follow = bool(cfg.get("follow_redirects", True))
    details: dict = {"redirects": 0}
    start = time.perf_counter()
    url = target
    try:
        async with httpx.AsyncClient(timeout=timeout, follow_redirects=False, verify=True) as client:
            while True:
                parsed = httpx.URL(url)
                if parsed.scheme not in ("http", "https") or not parsed.host:
                    return ProbeResult(False, error=f"URL refusée : {url}", details=details)
                port = parsed.port or (443 if parsed.scheme == "https" else 80)
                _, details["dns_ms"] = await resolve(parsed.host, port, allow_private)
                marks: dict[str, float] = {}
                tls: dict = {}

                async def trace(event: str, info: dict, marks=marks, tls=tls) -> None:
                    marks[event.split(".", 1)[-1] if event.startswith("http") else event] = time.perf_counter()
                    if event == "connection.start_tls.complete":
                        stream = info.get("return_value")
                        obj = stream.get_extra_info("ssl_object") if stream is not None else None
                        if obj is not None:
                            tls["cert"] = obj.getpeercert()
                            tls["version"] = obj.version()

                req = client.build_request(method, url, headers=headers, content=body, extensions={"trace": trace})
                resp = await client.send(req, stream=True)
                try:
                    chunks, size = [], 0
                    async for chunk in resp.aiter_bytes():
                        size += len(chunk)
                        if size <= MAX_BODY:
                            chunks.append(chunk)
                    done = time.perf_counter()
                finally:
                    await resp.aclose()
                location = resp.headers.get("location")
                if follow and resp.status_code in REDIRECT_CODES and location and details["redirects"] < 10:
                    url = str(resp.url.join(location))
                    details["redirects"] += 1
                    if resp.status_code == 303 or (resp.status_code in (301, 302) and method == "POST"):
                        method, body = "GET", None
                    continue
                break
    except TargetRefused as e:
        return ProbeResult(False, error=str(e), details=details)
    except socket.gaierror as e:
        return ProbeResult(False, error=f"résolution DNS impossible : {e}", details=details)
    except httpx.TimeoutException:
        return ProbeResult(False, latency_ms=round((time.perf_counter() - start) * 1000, 2),
                           error=f"délai dépassé ({timeout:g} s)", details=details)
    except httpx.HTTPError as e:
        return ProbeResult(False, error=f"connexion impossible : {e}", details=details)
    except (OSError, ssl.SSLError, ValueError) as e:
        return ProbeResult(False, error=f"erreur réseau : {e}", details=details)

    details.update({
        "connect_ms": _ms(marks.get("connection.connect_tcp.started"), marks.get("connection.connect_tcp.complete")),
        "tls_ms": _ms(marks.get("connection.start_tls.started"), marks.get("connection.start_tls.complete")),
        "ttfb_ms": _ms(marks.get("send_request_headers.started"), marks.get("receive_response_headers.complete")),
        "download_ms": _ms(marks.get("receive_response_headers.complete"), done),
        "body_bytes": size,
        "final_url": str(resp.url),
        "http_version": resp.http_version,
    })
    errors = []
    expect = cfg.get("expect_status")
    if not _status_ok(resp.status_code, expect, follow):
        errors.append(f"statut {resp.status_code}" + (f" (attendu {expect})" if expect is not None else ""))
    text = cfg.get("expect_text") or ""
    content = None
    if text:
        content = b"".join(chunks).decode(resp.encoding or "utf-8", errors="replace")
        if text not in content:
            errors.append("texte attendu absent de la réponse")
    if tls.get("cert"):
        days, not_after = _cert_days(tls["cert"])
        details.update({"ssl_days_left": days, "ssl_not_after": not_after, "tls_version": tls.get("version")})
        warn = cfg.get("ssl_days_warn")
        if warn is not None and days is not None and days < warn:
            errors.append(f"certificat expirant dans {days:g} jours")
    latency = round((done - start) * 1000, 2)
    if cfg.get("assertions"):
        # § 29 : assertions du test http (même forme que celles d'une étape multi-étapes).
        from . import assertions as A
        if content is None:
            content = b"".join(chunks).decode(resp.encoding or "utf-8", errors="replace")
        results = A.evaluate(cfg["assertions"], A.Response(resp.status_code, {k.lower(): v for k, v in resp.headers.items()},
                                                           content, latency))
        details["assertions"] = results
        errors += [A.describe_failure(r) for r in results if not r["ok"]]
    return ProbeResult(not errors, resp.status_code, latency, "; ".join(errors) or None, details)


# ── tcp ────────────────────────────────────────────────────────────────────────

async def _connect(addr: str, port: int, timeout: float):
    return await asyncio.wait_for(asyncio.open_connection(addr, port), timeout)


async def probe_tcp(target: str, cfg: dict, allow_private: bool) -> ProbeResult:
    timeout = float(cfg.get("timeout_s", 10))
    details: dict = {}
    start = time.perf_counter()
    try:
        host, port = split_host_port(target)
        addrs, details["dns_ms"] = await resolve(host, port, allow_private)
        t0 = time.perf_counter()
        _, writer = await _connect(addrs[0], port, timeout)
        details["connect_ms"] = _ms(t0, time.perf_counter())
        details["address"] = addrs[0]
        writer.close()
        try:
            await writer.wait_closed()
        except OSError:
            pass
    except TargetRefused as e:
        return ProbeResult(False, error=str(e), details=details)
    except asyncio.TimeoutError:
        return ProbeResult(False, error=f"délai dépassé ({timeout:g} s)", details=details)
    except (OSError, ValueError) as e:
        return ProbeResult(False, error=f"connexion impossible : {e}", details=details)
    return ProbeResult(True, latency_ms=_ms(start, time.perf_counter()), details=details)


# ── ssl ────────────────────────────────────────────────────────────────────────

def _unverified_days(der: bytes | None) -> tuple[float | None, int | None]:
    """Échéance d'un certificat refusé (expiré, auto-signé) : lue sans vérification de chaîne."""
    if not der:
        return None, None
    from cryptography import x509
    cert = x509.load_der_x509_certificate(der)
    not_after = cert.not_valid_after_utc
    days = (not_after - datetime.now(timezone.utc)).total_seconds() / 86400
    return round(days, 1), int(not_after.timestamp() * 1000)


async def probe_ssl(target: str, cfg: dict, allow_private: bool) -> ProbeResult:
    if cfg.get("starttls"):
        # Port de messagerie en clair au départ (25, 587, 143, 110) : STARTTLS, puis le certificat.
        from .mail import probe_mail_server
        return await probe_mail_server(cfg["starttls"], target, {**cfg, "tls": "starttls"}, allow_private)
    timeout = float(cfg.get("timeout_s", 10))
    warn = cfg.get("ssl_days_warn", 21)
    details: dict = {}
    start = time.perf_counter()
    try:
        host, port = split_host_port(target, 443)
        addrs, details["dns_ms"] = await resolve(host, port, allow_private)
        t0 = time.perf_counter()
        _, writer = await _connect(addrs[0], port, timeout)
        details["connect_ms"] = _ms(t0, time.perf_counter())
        t1 = time.perf_counter()
        try:
            await asyncio.wait_for(writer.start_tls(ssl.create_default_context(), server_hostname=host), timeout)
        except ssl.SSLCertVerificationError as e:
            writer.close()
            days, not_after = await _days_without_verification(addrs[0], port, host, timeout)
            details.update({"ssl_days_left": days, "ssl_not_after": not_after})
            return ProbeResult(False, error=f"certificat refusé : {e.verify_message or e}", details=details)
        details["tls_ms"] = _ms(t1, time.perf_counter())
        obj = writer.get_extra_info("ssl_object")
        cert = obj.getpeercert() if obj else None
        writer.close()
    except TargetRefused as e:
        return ProbeResult(False, error=str(e), details=details)
    except asyncio.TimeoutError:
        return ProbeResult(False, error=f"délai dépassé ({timeout:g} s)", details=details)
    except (OSError, ssl.SSLError, ValueError) as e:
        return ProbeResult(False, error=f"poignée de main TLS impossible : {e}", details=details)
    days, not_after = _cert_days(cert)
    details.update({"ssl_days_left": days, "ssl_not_after": not_after, "tls_version": obj.version() if obj else None,
                    "cipher": obj.cipher()[0] if obj and obj.cipher() else None,
                    "issuer": _name(cert.get("issuer")) if cert else None,
                    "subject": _name(cert.get("subject")) if cert else None})
    latency = _ms(start, time.perf_counter())
    errors = []
    if days is not None and warn is not None and days < warn:
        errors.append(f"certificat expirant dans {days:g} jours")
    if cfg.get("assertions"):
        # § 29 : un test ssl s'assure sur le détail mesuré (json_path $.ssl_days_left, latency_ms…).
        import json as _json

        from . import assertions as A
        results = A.evaluate(cfg["assertions"], A.Response(None, {}, _json.dumps(details, default=str), latency,
                                                           doc=details))
        details["assertions"] = results
        errors += [A.describe_failure(r) for r in results if not r["ok"]]
    if errors:
        return ProbeResult(False, latency_ms=latency, error="; ".join(errors), details=details)
    return ProbeResult(True, latency_ms=latency, details=details)


async def _days_without_verification(addr, port, host, timeout):
    ctx = ssl.create_default_context()
    ctx.check_hostname = False
    ctx.verify_mode = ssl.CERT_NONE
    try:
        _, writer = await _connect(addr, port, timeout)
        await asyncio.wait_for(writer.start_tls(ctx, server_hostname=host), timeout)
        obj = writer.get_extra_info("ssl_object")
        der = obj.getpeercert(binary_form=True) if obj else None
        writer.close()
        return _unverified_days(der)
    except (OSError, ssl.SSLError, asyncio.TimeoutError, ValueError):
        return None, None


# ── dns ────────────────────────────────────────────────────────────────────────

async def probe_dns(target: str, cfg: dict, allow_private: bool) -> ProbeResult:
    import dns.asyncresolver
    import dns.exception
    import dns.resolver

    timeout = float(cfg.get("timeout_s", 10))
    rtype = cfg.get("record_type", "A")
    nameserver = cfg.get("nameserver")
    expect = cfg.get("expect") or ""
    if nameserver and not allow_private and not is_public(nameserver):
        return ProbeResult(False, error=f"serveur DNS non public refusé : {nameserver}")
    resolver = dns.asyncresolver.Resolver(configure=not nameserver)
    if nameserver:
        resolver.nameservers = [nameserver]
    resolver.lifetime = timeout
    t0 = time.perf_counter()
    try:
        answer = await resolver.resolve(target.strip().rstrip("."), rtype)
    except dns.resolver.NXDOMAIN:
        return ProbeResult(False, latency_ms=_ms(t0, time.perf_counter()), error="NXDOMAIN : nom inexistant")
    except dns.resolver.NoAnswer:
        return ProbeResult(False, latency_ms=_ms(t0, time.perf_counter()), error=f"aucun enregistrement {rtype}")
    except dns.exception.Timeout:
        return ProbeResult(False, error=f"délai dépassé ({timeout:g} s)")
    except dns.exception.DNSException as e:
        return ProbeResult(False, error=f"erreur DNS : {e}")
    latency = _ms(t0, time.perf_counter())
    records = [r.to_text() for r in answer][:50]
    details = {"dns_ms": latency, "records": records, "ttl": answer.rrset.ttl if answer.rrset else None}
    if expect and not any(expect in r for r in records):
        return ProbeResult(False, latency_ms=latency, error=f"« {expect} » absent des réponses", details=details)
    return ProbeResult(True, latency_ms=latency, details=details)


# ── icmp (sans privilège : socket ICMP « datagramme » de Linux) ────────────────

def icmp_available() -> bool:
    """Vrai si le noyau autorise l'écho ICMP sans privilège (net.ipv4.ping_group_range)."""
    try:
        s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM, socket.IPPROTO_ICMP)
    except (OSError, AttributeError):
        return False
    s.close()
    return True


def _checksum(data: bytes) -> int:
    if len(data) % 2:
        data += b"\x00"
    total = sum(struct.unpack(f"!{len(data) // 2}H", data))
    total = (total >> 16) + (total & 0xFFFF)
    total += total >> 16
    return ~total & 0xFFFF


def _ping(addr: str, family: int, count: int, timeout: float) -> tuple[int, list[float]]:
    v4 = family == socket.AF_INET
    sock = socket.socket(family, socket.SOCK_DGRAM, socket.IPPROTO_ICMP if v4 else socket.IPPROTO_ICMPV6)
    sock.settimeout(timeout)
    rtts: list[float] = []
    ident = os.getpid() & 0xFFFF          # le noyau le remplace par l'identifiant de la socket
    try:
        for seq in range(count):
            payload = b"hypha-synthetics"
            req_type, reply_type = (8, 0) if v4 else (128, 129)
            header = struct.pack("!BBHHH", req_type, 0, 0, ident, seq)
            csum = _checksum(header + payload) if v4 else 0
            sock.sendto(struct.pack("!BBHHH", req_type, 0, csum, ident, seq) + payload, (addr, 0))
            t0 = time.perf_counter()
            deadline = t0 + timeout
            while time.perf_counter() < deadline:
                try:
                    data, _ = sock.recvfrom(2048)
                except socket.timeout:
                    break
                if len(data) >= 8 and data[0] == reply_type and struct.unpack("!H", data[6:8])[0] == seq:
                    rtts.append((time.perf_counter() - t0) * 1000)
                    break
            if seq < count - 1:
                time.sleep(0.2)
    finally:
        sock.close()
    return count, rtts


async def probe_icmp(target: str, cfg: dict, allow_private: bool) -> ProbeResult:
    timeout = float(cfg.get("timeout_s", 5))
    count = int(cfg.get("count", 3))
    try:
        host, _ = split_host_port(target)
        addrs, dns_ms = await resolve(host, None, allow_private)
        addr = addrs[0]
        family = socket.AF_INET6 if ":" in addr else socket.AF_INET
        sent, rtts = await asyncio.to_thread(_ping, addr, family, count, timeout)
    except TargetRefused as e:
        return ProbeResult(False, error=str(e))
    except PermissionError:
        return ProbeResult(False, error="icmp indisponible : le noyau refuse l'écho ICMP sans privilège")
    except (OSError, ValueError) as e:
        return ProbeResult(False, error=f"écho ICMP impossible : {e}")
    details = {"dns_ms": dns_ms, "address": addr, "packets_sent": sent, "packets_received": len(rtts),
               "packet_loss_pct": round(100 * (sent - len(rtts)) / sent, 1) if sent else None}
    if rtts:
        details.update({"rtt_min_ms": round(min(rtts), 2), "rtt_avg_ms": round(sum(rtts) / len(rtts), 2),
                        "rtt_max_ms": round(max(rtts), 2)})
        return ProbeResult(True, latency_ms=details["rtt_avg_ms"], details=details)
    return ProbeResult(False, error="aucune réponse à l'écho ICMP", details=details)


# ── messagerie (synthetics/mail.py, contrat § 32) ──────────────────────────────

async def probe_smtp(target: str, cfg: dict, allow_private: bool) -> ProbeResult:
    from .mail import probe_smtp as p
    return await p(target, cfg, allow_private)


async def probe_imap(target: str, cfg: dict, allow_private: bool) -> ProbeResult:
    from .mail import probe_imap as p
    return await p(target, cfg, allow_private)


async def probe_mail_roundtrip(target: str, cfg: dict, allow_private: bool) -> ProbeResult:
    from .mail import probe_mail_roundtrip as p
    return await p(target, cfg, allow_private)


async def probe_mail_dns(target: str, cfg: dict, allow_private: bool) -> ProbeResult:
    from .mail import probe_mail_dns as p
    return await p(target, cfg, allow_private)


PROBES = {"http": probe_http, "tcp": probe_tcp, "ssl": probe_ssl, "dns": probe_dns, "icmp": probe_icmp,
          "smtp": probe_smtp, "imap": probe_imap, "mail_roundtrip": probe_mail_roundtrip, "mail_dns": probe_mail_dns}


def budget_for(kind: str, config: dict) -> float:
    """Durée au-delà de laquelle un test est interrompu."""
    timeout = float(config.get("timeout_s", 10))
    if kind == "mail_roundtrip":
        return float(config.get("wait_s", 180)) + timeout * 4 + 30
    if kind == "mail_dns":
        return timeout * 8 + 30
    return timeout * 3 + 5


async def run_test(kind: str, target: str, config: dict, allow_private: bool) -> ProbeResult:
    probe = PROBES.get(kind)
    if probe is None:
        return ProbeResult(False, error=f"type de test inconnu : {kind}")
    budget = budget_for(kind, config or {})
    try:
        return await asyncio.wait_for(probe(target, config or {}, allow_private), budget)
    except asyncio.TimeoutError:
        return ProbeResult(False, error=f"test interrompu après {budget:g} s")
    except Exception as e:                  # une sonde ne doit jamais faire tomber le worker
        return ProbeResult(False, error=f"erreur interne de la sonde : {type(e).__name__}: {e}")
