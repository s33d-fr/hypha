"""Sondes de messagerie (contrat § 32). Comme les autres sondes, aucune ne lève : une erreur est un
résultat en échec, avec son message.

- smtp : bannière 220, EHLO, STARTTLS (ou TLS implicite sur le 465), certificat vérifié, capacités ;
- imap : accueil « * OK », CAPABILITY, TLS implicite (993) ou STARTTLS (143), certificat vérifié ;
- ssl avec `starttls` (smtp, imap, pop3) : le certificat d'un port de messagerie en clair au départ ;
- mail_roundtrip : un message part par la soumission authentifiée et doit revenir dans la boîte
  relevée en IMAP : vers la boîte elle-même (chemin interne du serveur de messagerie), ou vers un
  service d'écho extérieur qui répond (aller et retour par Internet ; le rapport SPF/DKIM qu'il
  renvoie est relevé) ;
- mail_dns : MX, SPF (un seul, qualificatif final, au plus 10 recherches DNS), DMARC, DKIM des
  sélecteurs donnés, adresses d'envoi sur les listes noires (DNSBL). Les DNS du domaine sont relus à
  chaque passage ; une liste noire, source extérieure, l'est au plus une fois toutes les 12 h par
  adresse, et son dernier verdict s'applique aux adresses du SPF et des MX du moment.
"""
import asyncio
import email
import email.policy
import email.utils
import imaplib
import ipaddress
import re
import smtplib
import ssl
import time
import uuid
from email.message import EmailMessage

from .netguard import TargetRefused, is_public, resolve
from .probes import ProbeResult, _cert_days, _connect, _ms, _name, _unverified_days, split_host_port

EHLO_NAME = "hypha-synthetics.s33d.fr"
PROBE_SUBJECT = "Sonde HYPHA"
DEFAULT_PORTS = {"smtp": 25, "imap": 993, "pop3": 110}
IMPLICIT_PORTS = (465, 993, 995)
DNSBL_ZONES = ("zen.spamhaus.org", "bl.spamcop.net", "b.barracudacentral.org")
# Une liste noire est une source extérieure : chaque couple (adresse, liste) est relu au plus une fois
# toutes les 12 h, et un passage en relit 3 au plus. Entre deux lectures, le dernier verdict lu sert.
DNSBL_MAX_AGE_S = 43200
DNSBL_READS_PER_RUN = 3
DNSBL_NOT_READ = "pas encore lue"
AUTH_LINE = re.compile(r"^\s*(SPF|DKIM|DMARC|iprev|SpamAssassin|Sender-ID|DomainKeys)\s+check:\s*(\S+)", re.I | re.M)


class StartTLSRefused(Exception):
    pass


# ── conversation en clair (smtp, imap, pop3) ──────────────────────────────────────────────

async def _line(reader, timeout: float) -> str:
    raw = await asyncio.wait_for(reader.readline(), timeout)
    if not raw:
        raise ConnectionError("connexion fermée par le serveur")
    return raw.decode("utf-8", "replace").rstrip("\r\n")


async def _smtp_reply(reader, timeout: float) -> tuple[int, list[str]]:
    lines: list[str] = []
    while True:
        line = await _line(reader, timeout)
        lines.append(line)
        if len(line) < 4 or line[3] != "-":
            break
        if len(lines) > 200:
            raise ConnectionError("réponse SMTP trop longue")
    try:
        code = int(lines[-1][:3])
    except ValueError:
        raise ConnectionError(f"réponse SMTP illisible : {lines[-1][:80]}")
    return code, [x[4:] for x in lines]


async def _smtp(reader, writer, cmd: str, timeout: float) -> tuple[int, list[str]]:
    writer.write(cmd.encode() + b"\r\n")
    await writer.drain()
    return await _smtp_reply(reader, timeout)


async def _imap(reader, writer, tag: str, cmd: str, timeout: float) -> tuple[str, list[str]]:
    writer.write(f"{tag} {cmd}\r\n".encode())
    await writer.drain()
    untagged: list[str] = []
    for _ in range(500):
        line = await _line(reader, timeout)
        if line.startswith(tag + " "):
            return line[len(tag) + 1:], untagged
        untagged.append(line)
    raise ConnectionError("réponse IMAP trop longue")


def _imap_caps(untagged: list[str]) -> list[str]:
    for line in untagged:
        if line.upper().startswith("* CAPABILITY"):
            return line.split()[2:]
    return []


async def _greeting(proto: str, reader, timeout: float) -> str:
    if proto == "smtp":
        code, lines = await _smtp_reply(reader, timeout)
        banner = (lines[0] if lines else "")[:200]
        if code != 220:
            raise StartTLSRefused(f"bannière SMTP {code} : {banner}")
        return banner
    line = await _line(reader, timeout)
    ok = line.startswith("* OK") if proto == "imap" else line.startswith("+OK")
    if not ok:
        raise StartTLSRefused(f"accueil {proto.upper()} inattendu : {line[:80]}")
    return line[:200]


async def _capabilities(proto: str, reader, writer, timeout: float, ehlo: str, tag: str) -> list[str]:
    if proto == "smtp":
        code, lines = await _smtp(reader, writer, f"EHLO {ehlo}", timeout)
        if code != 250:
            raise StartTLSRefused(f"EHLO refusé ({code})")
        return [x.split()[0].upper() for x in lines[1:] if x.strip()] + \
            [f"AUTH={m.upper()}" for x in lines[1:] if x.upper().startswith("AUTH") for m in x.split()[1:]]
    if proto == "imap":
        status, untagged = await _imap(reader, writer, tag, "CAPABILITY", timeout)
        if not status.upper().startswith("OK"):
            raise StartTLSRefused(f"CAPABILITY refusé : {status[:80]}")
        return [c.upper() for c in _imap_caps(untagged)]
    return []


async def starttls_prelude(proto: str, reader, writer, timeout: float, ehlo: str = EHLO_NAME) -> dict:
    """Conversation en clair jusqu'au feu vert du serveur pour la poignée de main TLS."""
    info = {"banner": await _greeting(proto, reader, timeout)}
    if proto == "pop3":
        writer.write(b"STLS\r\n")
        await writer.drain()
        line = await _line(reader, timeout)
        if not line.startswith("+OK"):
            raise StartTLSRefused(f"STLS refusé : {line[:80]}")
        return info
    caps = await _capabilities(proto, reader, writer, timeout, ehlo, "h1")
    info["capabilities"] = caps
    if "STARTTLS" not in caps:
        raise StartTLSRefused("STARTTLS non proposé par le serveur")
    if proto == "smtp":
        code, lines = await _smtp(reader, writer, "STARTTLS", timeout)
        if code != 220:
            raise StartTLSRefused(f"STARTTLS refusé ({code} {(lines[-1] if lines else '')[:80]})")
    else:
        status, _ = await _imap(reader, writer, "h2", "STARTTLS", timeout)
        if not status.upper().startswith("OK"):
            raise StartTLSRefused(f"STARTTLS refusé : {status[:80]}")
    return info


def tls_details(writer) -> dict:
    obj = writer.get_extra_info("ssl_object")
    cert = obj.getpeercert() if obj else None
    days, not_after = _cert_days(cert)
    return {"ssl_days_left": days, "ssl_not_after": not_after, "tls_version": obj.version() if obj else None,
            "cipher": obj.cipher()[0] if obj and obj.cipher() else None,
            "issuer": _name(cert.get("issuer")) if cert else None,
            "subject": _name(cert.get("subject")) if cert else None}


async def _close(proto: str, writer) -> None:
    try:
        writer.write(b"QUIT\r\n" if proto in ("smtp", "pop3") else b"z LOGOUT\r\n")
        await writer.drain()
    except (OSError, RuntimeError):
        pass
    writer.close()
    try:
        await asyncio.wait_for(writer.wait_closed(), 2)
    except (OSError, asyncio.TimeoutError, ssl.SSLError):
        pass


def tls_mode(cfg: dict, port: int) -> str:
    mode = cfg.get("tls") or "auto"
    if mode == "auto":
        return "implicit" if port in IMPLICIT_PORTS else "starttls"
    return mode


async def _unverified(addr: str, port: int, name: str, proto: str, mode: str, timeout: float, ehlo: str):
    """Échéance d'un certificat refusé (expiré, auto-signé, nom faux), lue sans vérification."""
    ctx = ssl.create_default_context()
    ctx.check_hostname = False
    ctx.verify_mode = ssl.CERT_NONE
    try:
        reader, writer = await _connect(addr, port, timeout)
        if mode == "starttls":
            await starttls_prelude(proto, reader, writer, timeout, ehlo)
        await asyncio.wait_for(writer.start_tls(ctx, server_hostname=name), timeout)
        obj = writer.get_extra_info("ssl_object")
        der = obj.getpeercert(binary_form=True) if obj else None
        writer.close()
        return _unverified_days(der)
    except (OSError, ssl.SSLError, asyncio.TimeoutError, ValueError, ConnectionError, StartTLSRefused):
        return None, None


async def probe_mail_server(proto: str, target: str, cfg: dict, allow_private: bool) -> ProbeResult:
    """smtp, imap, ou le test ssl d'un port en STARTTLS (proto = cfg.starttls)."""
    timeout = float(cfg.get("timeout_s", 10))
    warn = cfg.get("ssl_days_warn", 21)
    ehlo = cfg.get("ehlo_name") or EHLO_NAME
    details: dict = {"protocol": proto}
    start = time.perf_counter()
    writer = None
    try:
        host, port = split_host_port(target, DEFAULT_PORTS[proto])
        mode = tls_mode(cfg, port)
        name = cfg.get("tls_server_name") or host
        details["tls_mode"] = mode
        addrs, details["dns_ms"] = await resolve(host, port, allow_private)
        details["address"] = addrs[0]
        t0 = time.perf_counter()
        reader, writer = await _connect(addrs[0], port, timeout)
        details["connect_ms"] = _ms(t0, time.perf_counter())
        ctx = ssl.create_default_context()
        if mode == "implicit":
            t1 = time.perf_counter()
            await asyncio.wait_for(writer.start_tls(ctx, server_hostname=name), timeout)
            details["tls_ms"] = _ms(t1, time.perf_counter())
            details.update(tls_details(writer))
            details["banner"] = await _greeting(proto, reader, timeout)
            details["capabilities"] = await _capabilities(proto, reader, writer, timeout, ehlo, "h1")
        elif mode == "starttls":
            t1 = time.perf_counter()
            details.update(await starttls_prelude(proto, reader, writer, timeout, ehlo))
            details["banner_ms"] = _ms(t1, time.perf_counter())
            t2 = time.perf_counter()
            await asyncio.wait_for(writer.start_tls(ctx, server_hostname=name), timeout)
            details["tls_ms"] = _ms(t2, time.perf_counter())
            details.update(tls_details(writer))
            details["capabilities_tls"] = await _capabilities(proto, reader, writer, timeout, ehlo, "h3")
        else:
            details["banner"] = await _greeting(proto, reader, timeout)
            details["capabilities"] = await _capabilities(proto, reader, writer, timeout, ehlo, "h1")
        await _close(proto, writer)
        writer = None
    except ssl.SSLCertVerificationError as e:
        days, not_after = await _unverified(details.get("address"), port, name, proto, mode, timeout, ehlo)
        details.update({"ssl_days_left": days, "ssl_not_after": not_after})
        return ProbeResult(False, error=f"certificat refusé : {e.verify_message or e}", details=details)
    except StartTLSRefused as e:
        return ProbeResult(False, error=str(e), details=details)
    except TargetRefused as e:
        return ProbeResult(False, error=str(e), details=details)
    except asyncio.TimeoutError:
        return ProbeResult(False, error=f"délai dépassé ({timeout:g} s)", details=details)
    except (OSError, ssl.SSLError, ValueError, ConnectionError) as e:
        return ProbeResult(False, error=f"échange {proto.upper()} impossible : {e}", details=details)
    finally:
        if writer is not None:
            writer.close()
    latency = _ms(start, time.perf_counter())
    errors = []
    days = details.get("ssl_days_left")
    if days is not None and warn is not None and days < warn:
        errors.append(f"certificat expirant dans {days:g} jours")
    expect = cfg.get("expect_banner") or ""
    if expect and expect.lower() not in (details.get("banner") or "").lower():
        errors.append(f"« {expect} » absent de la bannière")
    if cfg.get("assertions"):
        import json as _json

        from . import assertions as A
        results = A.evaluate(cfg["assertions"], A.Response(None, {}, _json.dumps(details, default=str), latency,
                                                           doc=details))
        details["assertions"] = results
        errors += [A.describe_failure(r) for r in results if not r["ok"]]
    if errors:
        return ProbeResult(False, latency_ms=latency, error="; ".join(errors), details=details)
    return ProbeResult(True, latency_ms=latency, details=details)


async def probe_smtp(target: str, cfg: dict, allow_private: bool) -> ProbeResult:
    return await probe_mail_server("smtp", target, cfg, allow_private)


async def probe_imap(target: str, cfg: dict, allow_private: bool) -> ProbeResult:
    return await probe_mail_server("imap", target, cfg, allow_private)


# ── aller-retour d'un message ───────────────────────────────────────────────────────────

def _probe_message(sender: str, to: str, token: str) -> EmailMessage:
    msg = EmailMessage()
    msg["From"] = sender
    msg["To"] = to
    msg["Subject"] = f"{PROBE_SUBJECT} {token}"
    msg["Date"] = email.utils.formatdate(localtime=False)
    msg["Message-ID"] = email.utils.make_msgid(idstring=token, domain=sender.rsplit("@", 1)[-1])
    msg["X-Hypha-Probe"] = token
    msg.set_content("Message de test envoyé par HYPHA pour mesurer la remise du courrier de bout en bout.\n"
                    f"Jeton : {token}\nAucune action n'est attendue.\n")
    return msg


def _send(cfg: dict, host: str, port: int, mode: str, msg: EmailMessage, timeout: float) -> None:
    ctx = ssl.create_default_context()
    ehlo = cfg.get("ehlo_name") or EHLO_NAME
    if mode == "implicit":
        s = smtplib.SMTP_SSL(host, port, local_hostname=ehlo, timeout=timeout, context=ctx)
    else:
        s = smtplib.SMTP(host, port, local_hostname=ehlo, timeout=timeout)
    try:
        s.ehlo()
        if mode == "starttls":
            s.starttls(context=ctx)
            s.ehlo()
        s.login(cfg["username"], cfg["password"])
        s.send_message(msg)
    finally:
        try:
            s.quit()
        except (smtplib.SMTPException, OSError):
            s.close()


def _report(raw: bytes) -> dict:
    """Résultats d'authentification qu'un service d'écho renvoie (« SPF check: pass »…)."""
    text = raw.decode("utf-8", "replace")
    try:
        m = email.message_from_bytes(raw, policy=email.policy.default)
        part = m.get_body(preferencelist=("plain",))
        if part is not None:
            text = part.get_content()
    except (TypeError, ValueError, LookupError):
        pass
    return {k.lower(): v.lower() for k, v in AUTH_LINE.findall(text)}


STALE_S = 3600   # un message de sonde (ou une réponse d'écho) plus vieux est un reste : effacé


def _from_matches(raw: bytes, reply_from: str) -> bool:
    """Réponse d'un service d'écho qui ne recopie pas le jeton : reconnue à son expéditeur."""
    try:
        m = email.message_from_bytes(raw, policy=email.policy.default)
    except (TypeError, ValueError, IndexError):
        return False
    return reply_from.lower() in str(m.get("From", "")).lower()


def _internaldate(meta: bytes) -> float | None:
    """Date de réception du serveur IMAP (INTERNALDATE), en secondes depuis l'époque."""
    tup = imaplib.Internaldate2tuple(meta)
    return time.mktime(tup) if tup else None


def _wait(cfg: dict, host: str, port: int, mode: str, token: str, deadline: float, timeout: float,
          sent: float = 0.0) -> dict:
    """Relève la boîte jusqu'à trouver le jeton, ou la réponse de l'écho reçue après l'envoi.
    N'efface que ce qui appartient à ce passage, plus les restes de sonde de plus d'une heure : deux
    tests qui partagent la boîte ne s'effacent pas leurs messages."""
    reply_from = (cfg.get("reply_from") or "").strip()
    ctx = ssl.create_default_context()
    if mode == "implicit":
        m = imaplib.IMAP4_SSL(host, port, ssl_context=ctx, timeout=timeout)
    else:
        m = imaplib.IMAP4(host, port, timeout=timeout)
        if mode == "starttls":
            m.starttls(ssl_context=ctx)
    found: dict = {}
    try:
        m.login(cfg["username"], cfg["password"])
        m.select("INBOX")
        seen: set[bytes] = set()
        mine: list[bytes] = []
        while True:
            typ, data = m.uid("SEARCH", None, "ALL")
            uids = data[0].split() if typ == "OK" and data and data[0] else []
            for uid in [u for u in uids if u not in seen][-100:]:
                seen.add(uid)
                typ, parts = m.uid("FETCH", uid, "(INTERNALDATE BODY.PEEK[])")
                tuples = [p for p in parts or [] if isinstance(p, tuple)]
                raw = b"".join(p[1] for p in tuples)
                received = _internaldate(tuples[0][0]) if tuples else None
                probe = PROBE_SUBJECT.encode() + b" hypha-" in raw
                echo = bool(reply_from) and _from_matches(raw, reply_from)
                own = token.encode() in raw
                fresh_reply = echo and received is not None and received >= sent - 60
                if not found and (own or fresh_reply):
                    found = {"at": received if received and received >= sent - 60 else time.time(),
                             "report": _report(raw)}
                    mine.append(uid)
                elif (probe or echo) and received is not None and time.time() - received > STALE_S:
                    mine.append(uid)
            if found or time.monotonic() > deadline:
                break
            time.sleep(5)
            m.noop()
        for uid in mine:
            m.uid("STORE", uid, "+FLAGS", r"(\Deleted)")
        if mine:
            m.expunge()
        found["cleaned"] = len(mine)
    finally:
        try:
            m.logout()
        except (imaplib.IMAP4.error, OSError):
            pass
    return found


async def probe_mail_roundtrip(target: str, cfg: dict, allow_private: bool) -> ProbeResult:
    timeout = float(cfg.get("timeout_s", 20))
    wait_s = float(cfg.get("wait_s", 180))
    mailbox = target.strip()
    to = (cfg.get("to") or mailbox).strip()
    mode = "direct" if to.lower() == mailbox.lower() else "echo"
    cfg = {**cfg, "username": cfg.get("username") or mailbox}
    token = "hypha-" + uuid.uuid4().hex[:16]
    details: dict = {"mode": mode, "to": to, "token": token}
    start = time.perf_counter()
    try:
        s_host, s_port = split_host_port(cfg["smtp"], 587)
        i_host, i_port = split_host_port(cfg["imap"], 993)
        await resolve(s_host, s_port, allow_private)
        await resolve(i_host, i_port, allow_private)
        msg = _probe_message(cfg.get("from") or mailbox, to, token)
        t0 = time.perf_counter()
        await asyncio.to_thread(_send, cfg, s_host, s_port, tls_mode({"tls": cfg.get("smtp_tls")}, s_port), msg, timeout)
        sent = time.time()
        details["submit_ms"] = _ms(t0, time.perf_counter())
        deadline = time.monotonic() + wait_s
        got = await asyncio.to_thread(_wait, cfg, i_host, i_port, tls_mode({"tls": cfg.get("imap_tls")}, i_port),
                                      token, deadline, timeout, sent)
    except TargetRefused as e:
        return ProbeResult(False, error=str(e), details=details)
    except smtplib.SMTPAuthenticationError as e:
        return ProbeResult(False, error=f"soumission refusée : authentification ({e.smtp_code})", details=details)
    except smtplib.SMTPException as e:
        return ProbeResult(False, error=f"soumission impossible : {e}", details=details)
    except imaplib.IMAP4.error as e:
        return ProbeResult(False, error=f"relève IMAP impossible : {e}", details=details)
    except (OSError, ssl.SSLError, ValueError) as e:
        return ProbeResult(False, error=f"aller-retour impossible : {e}", details=details)
    details["cleaned"] = got.get("cleaned", 0)
    if not got.get("at"):
        return ProbeResult(False, latency_ms=_ms(start, time.perf_counter()),
                           error=f"message non revenu après {wait_s:g} s", details=details)
    details["delivery_s"] = round(max(0.0, got["at"] - sent), 1)
    report = got.get("report") or {}
    if report:
        details["report"] = report
    latency = round(details["delivery_s"] * 1000, 1)
    if mode == "echo" and cfg.get("expect_auth_pass", True):
        bad = {k: v for k, v in report.items() if k in ("spf", "dkim", "dmarc") and v != "pass"}
        if bad:
            return ProbeResult(False, latency_ms=latency, details=details,
                               error="authentification en échec selon l'écho : " +
                                     ", ".join(f"{k.upper()} {v}" for k, v in sorted(bad.items())))
    return ProbeResult(True, latency_ms=latency, details=details)


# ── DNS d'un domaine de messagerie ──────────────────────────────────────────────────────

SPF_LOOKUP = re.compile(r"^[+\-~?]?(include:|a\b|a:|a/|mx\b|mx:|mx/|ptr\b|ptr:|exists:)|^redirect=", re.I)


async def _txt(resolver, name: str) -> list[str]:
    import dns.exception
    import dns.resolver
    try:
        answer = await resolver.resolve(name, "TXT")
    except (dns.resolver.NXDOMAIN, dns.resolver.NoAnswer, dns.resolver.NoNameservers):
        return []
    except dns.exception.Timeout:
        raise asyncio.TimeoutError
    return [b"".join(r.strings).decode("utf-8", "replace") for r in answer]


async def _spf_lookups(resolver, record: str, depth: int = 0, seen: set | None = None) -> int:
    """Recherches DNS qu'un récepteur fait pour évaluer le SPF (RFC 7208 § 4.6.4 : 10 au plus)."""
    seen = seen if seen is not None else set()
    n = 0
    for term in record.split()[1:]:
        if not SPF_LOOKUP.match(term):
            continue
        n += 1
        low = term.lower().lstrip("+-~?")
        inner = low[8:] if low.startswith("include:") else low[9:] if low.startswith("redirect=") else None
        if inner and depth < 5 and inner not in seen:
            seen.add(inner)
            sub = [t for t in await _txt(resolver, inner) if t.lower().startswith("v=spf1")]
            if sub:
                n += await _spf_lookups(resolver, sub[0], depth + 1, seen)
    return n


def _tags(record: str) -> dict:
    out = {}
    for part in record.split(";"):
        if "=" in part:
            k, v = part.split("=", 1)
            out[k.strip().lower()] = v.strip()
    return out


async def _dnsbl(resolver, ip: str, zone: str) -> str:
    import dns.exception
    import dns.resolver
    name = ".".join(reversed(ip.split("."))) + "." + zone
    try:
        answer = await resolver.resolve(name, "A")
    except (dns.resolver.NXDOMAIN, dns.resolver.NoAnswer):
        return "propre"
    except (dns.exception.Timeout, dns.resolver.NoNameservers):
        return "inconnue"
    codes = sorted(r.to_text() for r in answer)
    if any(c.startswith("127.255.255.") for c in codes):
        return "inconnue (requête refusée par la liste)"
    if zone.endswith("spamhaus.org") and all(c in ("127.0.0.10", "127.0.0.11") for c in codes):
        return "PBL (" + ", ".join(codes) + ")"
    return "listée (" + ", ".join(codes) + ")"


async def _dnsbl_listing(resolver, ips: list[str], zones, cache: dict, deadline: float,
                         now: int) -> tuple[dict, dict, list[str]]:
    """Verdicts des listes noires pour les adresses du moment. `cache` : {ip: {zone: (verdict, lu)}},
    derniers verdicts lus (runner.dnsbl_cache). Seuls les couples jamais lus ou lus depuis 12 h sont
    relus, les plus anciens d'abord, 3 au plus, et aucun après `deadline` (perf_counter) ; un couple
    qui attend son tour garde son dernier verdict. Une adresse sortie du SPF n'est plus regardée."""
    pairs = [(ip, zone) for ip in ips for zone in zones]
    known = {p: (cache.get(p[0]) or {}).get(p[1]) for p in pairs}
    due = sorted((p for p in pairs if known[p] is None or now - known[p][1] >= DNSBL_MAX_AGE_S),
                 key=lambda p: known[p][1] if known[p] else -1)
    fresh: dict = {}
    for p in due[:DNSBL_READS_PER_RUN]:
        if time.perf_counter() > deadline:
            break
        fresh[p] = (await _dnsbl(resolver, *p), now)
    listing: dict = {}
    read_at: dict = {}
    errors: list[str] = []
    for ip, zone in pairs:
        verdict, at = fresh.get((ip, zone)) or known[(ip, zone)] or (DNSBL_NOT_READ, None)
        listing.setdefault(ip, {})[zone] = verdict
        if at is not None:
            read_at.setdefault(ip, {})[zone] = int(at)
        if verdict.startswith("listée"):
            errors.append(f"{ip} listée sur {zone}")
    return listing, read_at, errors


async def probe_mail_dns(target: str, cfg: dict, allow_private: bool) -> ProbeResult:
    import dns.asyncresolver
    import dns.exception
    import dns.resolver

    timeout = float(cfg.get("timeout_s", 10))
    domain = target.strip().rstrip(".").lower()
    resolver = dns.asyncresolver.Resolver()
    resolver.lifetime = timeout
    details: dict = {"domain": domain, "warnings": []}
    errors: list[str] = []
    warnings = details["warnings"]
    start = time.perf_counter()
    try:
        try:
            mx = sorted((r.preference, r.exchange.to_text().rstrip(".")) for r in await resolver.resolve(domain, "MX"))
        except (dns.resolver.NXDOMAIN, dns.resolver.NoAnswer):
            mx = []
        details["mx"] = [f"{p} {h}" for p, h in mx]
        if not mx or (len(mx) == 1 and mx[0][1] in ("", ".")):
            errors.append("aucun MX : le domaine ne reçoit pas de courrier")

        spf = [t for t in await _txt(resolver, domain) if t.lower().startswith("v=spf1")]
        spf_ips: list[str] = []
        if not spf:
            errors.append("aucun enregistrement SPF")
        elif len(spf) > 1:
            errors.append(f"{len(spf)} enregistrements SPF : un seul est permis (permerror)")
        else:
            record = spf[0]
            details["spf"] = record
            terms = record.split()[1:]
            final = next((t.lower() for t in terms if t.lower().lstrip("+-~?") == "all"), None)
            details["spf_all"] = final
            if final in ("all", "+all"):
                errors.append("SPF « +all » : tout serveur peut envoyer pour le domaine")
            elif final == "?all":
                errors.append("SPF « ?all » : aucune protection")
            elif final == "~all":
                warnings.append("SPF en « ~all » (échec doux)")
            elif final is None and not any(t.lower().startswith("redirect=") for t in terms):
                warnings.append("SPF sans mécanisme « all » final")
            want = cfg.get("spf_all")
            if want and final != want:
                errors.append(f"SPF : « {want} » attendu, « {final or 'aucun'} » trouvé")
            lookups = await _spf_lookups(resolver, record)
            details["spf_lookups"] = lookups
            if lookups > 10:
                errors.append(f"SPF : {lookups} recherches DNS (10 au plus, sinon permerror)")
            for t in terms:
                low = t.lower().lstrip("+")
                if low.startswith("ip4:"):
                    net = low[4:]
                    if "/" not in net or net.endswith("/32"):
                        spf_ips.append(net.split("/")[0])

        dmarc = [t for t in await _txt(resolver, "_dmarc." + domain) if t.lower().startswith("v=dmarc1")]
        if not dmarc:
            errors.append("aucun enregistrement DMARC (_dmarc)")
        elif len(dmarc) > 1:
            errors.append(f"{len(dmarc)} enregistrements DMARC : un seul est permis")
        else:
            tags = _tags(dmarc[0])
            details["dmarc"] = dmarc[0]
            policy = (tags.get("p") or "").lower()
            details["dmarc_policy"] = policy
            if policy not in ("none", "quarantine", "reject"):
                errors.append(f"DMARC : politique « p » absente ou invalide ({policy or 'vide'})")
            elif policy == "none":
                warnings.append("DMARC en « p=none » : surveillance seule")
            want = cfg.get("dmarc_policy")
            if want and policy != want:
                errors.append(f"DMARC : « p={want} » attendu, « p={policy} » trouvé")
            if not tags.get("rua"):
                warnings.append("DMARC sans adresse de rapports (rua)")

        dkim = {}
        for sel in cfg.get("dkim_selectors") or []:
            recs = await _txt(resolver, f"{sel}._domainkey.{domain}")
            key = next((_tags(r).get("p", "") for r in recs if "p=" in r), None)
            dkim[sel] = "présente" if key else ("révoquée (p vide)" if key == "" else "absente")
            if not key:
                errors.append(f"DKIM : clé du sélecteur « {sel} » {dkim[sel]}")
        if dkim:
            details["dkim"] = dkim

        if cfg.get("dnsbl"):
            ips = list(cfg.get("dnsbl_ips") or [])
            if not ips:
                ips = spf_ips[:]
                for _, h in mx[:5]:
                    try:
                        ips += [r.to_text() for r in await resolver.resolve(h, "A")]
                    except dns.exception.DNSException:
                        pass
            ips = [ip for ip in dict.fromkeys(ips) if _ipv4_public(ip)][:20]
            # Lectures commencées avant 4 délais : le passage finit dans son budget (runner.budget_for),
            # sinon il serait coupé et ses lectures perdues, donc refaites au passage suivant.
            listing, read_at, bl_errors = await _dnsbl_listing(
                resolver, ips, cfg.get("dnsbl_zones") or DNSBL_ZONES, cfg.get("_dnsbl_cache") or {},
                start + timeout * 4, int(time.time()))
            errors += bl_errors
            details["dnsbl"] = listing
            details["dnsbl_lu"] = read_at
    except asyncio.TimeoutError:
        return ProbeResult(False, error=f"délai DNS dépassé ({timeout:g} s)", details=details)
    except dns.exception.DNSException as e:
        return ProbeResult(False, error=f"erreur DNS : {e}", details=details)
    latency = _ms(start, time.perf_counter())
    if errors:
        return ProbeResult(False, latency_ms=latency, error="; ".join(errors), details=details)
    return ProbeResult(True, latency_ms=latency, details=details)


def _ipv4_public(ip: str) -> bool:
    try:
        return ipaddress.ip_address(ip).version == 4 and is_public(ip)
    except ValueError:
        return False
