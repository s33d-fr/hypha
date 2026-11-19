"""Synthétiques de messagerie (§ 32) et liste des certificats (§ 33)."""
import asyncio
import time

import pytest

import test_routes_nodb  # noqa: F401  (pose l'environnement)
from hypha.api import certificates as C
from hypha.synthetics import mail as M
from hypha.synthetics import validate as V


def norm(kind, target, config=None, **kw):
    return V.normalize_test({"name": "t", "kind": kind, "target": target, "config": config or {}, **kw}, True)


def test_validate_mail_kinds():
    t = norm("smtp", "mx.example.com")
    assert t["config"]["tls"] == "auto" and t["config"]["ssl_days_warn"] == 21
    assert norm("imap", "imap.example.com:143", {"tls": "starttls"})["config"]["tls"] == "starttls"
    assert norm("ssl", "mx.example.com:25", {"starttls": "smtp"})["config"]["starttls"] == "smtp"
    with pytest.raises(ValueError):
        norm("ssl", "mx.example.com:25", {"starttls": "ftp"})
    rt = {"smtp": "smtp.example.com:587", "imap": "imap.example.com:993", "password": "s3cret"}
    t = norm("mail_roundtrip", "sonde@example.com", rt, interval_s=600)
    assert t["config"]["password"] == "s3cret" and t["config"]["wait_s"] == 180 and t["locations"] == ["server"]
    with pytest.raises(ValueError, match="password"):
        norm("mail_roundtrip", "sonde@example.com", {**rt, "password": V.MASK}, interval_s=600)
    with pytest.raises(ValueError, match="300 s"):
        norm("mail_roundtrip", "sonde@example.com", rt, interval_s=60)
    with pytest.raises(ValueError, match="serveur"):
        norm("mail_roundtrip", "sonde@example.com", rt, interval_s=600, locations=["server", "agent:3"])
    with pytest.raises(ValueError, match="jamais en clair"):
        norm("mail_roundtrip", "sonde@example.com", {**rt, "smtp_tls": "off"}, interval_s=600)
    d = norm("mail_dns", "example.com.", {"dkim_selectors": ["s1"], "dmarc_policy": "reject", "dnsbl": True},
             interval_s=300)
    assert d["target"] == "example.com" and d["config"]["dnsbl"] is True
    with pytest.raises(ValueError, match="300 s"):
        norm("mail_dns", "example.com", {"dnsbl": True}, interval_s=60)


def test_secret_masking():
    cfg = {"smtp": "a:587", "password": "s3cret"}
    assert V.public_config("mail_roundtrip", cfg)["password"] == V.MASK and cfg["password"] == "s3cret"
    assert V.keep_secret({"smtp": "b:587", "password": V.MASK}, cfg)["password"] == "s3cret"
    assert V.keep_secret({"smtp": "b:587"}, cfg)["password"] == "s3cret"
    assert V.keep_secret({"password": "neuf"}, cfg)["password"] == "neuf"
    assert V.public_config("http", {"method": "GET"}) == {"method": "GET"}


# ── faux serveurs en clair (tls: off) ───────────────────────────────────────────────────

async def _serve(handler):
    server = await asyncio.start_server(handler, "127.0.0.1", 0)
    return server, server.sockets[0].getsockname()[1]


async def _smtp_handler(reader, writer, starttls=False):
    writer.write(b"220 mx.test ESMTP Postfix\r\n")
    while True:
        line = await reader.readline()
        if not line:
            break
        cmd = line.decode().strip().upper()
        if cmd.startswith("EHLO"):
            caps = [b"250-mx.test", b"250-PIPELINING", b"250-AUTH PLAIN LOGIN"] + ([b"250-STARTTLS"] if starttls else [])
            writer.write(b"\r\n".join(caps + [b"250 SIZE 52428800"]) + b"\r\n")
        elif cmd == "QUIT":
            writer.write(b"221 bye\r\n")
            break
        else:
            writer.write(b"502 non\r\n")
        await writer.drain()
    writer.close()


def test_probe_smtp_plain_and_missing_starttls():
    async def run():
        server, port = await _serve(_smtp_handler)
        async with server:
            ok = await M.probe_smtp(f"127.0.0.1:{port}", {"tls": "off", "expect_banner": "postfix"}, True)
            bad = await M.probe_smtp(f"127.0.0.1:{port}", {"tls": "starttls"}, True)
            refused = await M.probe_smtp(f"127.0.0.1:{port}", {"tls": "off"}, False)
        return ok, bad, refused
    ok, bad, refused = asyncio.run(run())
    assert ok.ok, ok.error
    assert "AUTH=PLAIN" in ok.details["capabilities"] and "SIZE" in ok.details["capabilities"]
    assert ok.details["banner"].startswith("mx.test ESMTP")
    assert not bad.ok and "STARTTLS non proposé" in bad.error
    assert not refused.ok and "non publique" in refused.error


async def _imap_handler(reader, writer):
    writer.write(b"* OK IMAP4rev1 pret\r\n")
    while True:
        line = await reader.readline()
        if not line:
            break
        tag, _, cmd = line.decode().strip().partition(" ")
        if cmd.upper() == "CAPABILITY":
            writer.write(b"* CAPABILITY IMAP4rev1 IDLE AUTH=PLAIN\r\n" + tag.encode() + b" OK fini\r\n")
        elif cmd.upper() == "LOGOUT":
            writer.write(b"* BYE\r\n" + tag.encode() + b" OK\r\n")
            break
        else:
            writer.write(tag.encode() + b" BAD\r\n")
        await writer.drain()
    writer.close()


def test_probe_imap_plain():
    async def run():
        server, port = await _serve(_imap_handler)
        async with server:
            return await M.probe_imap(f"127.0.0.1:{port}", {"tls": "off"}, True)
    r = asyncio.run(run())
    assert r.ok, r.error
    assert r.details["capabilities"] == ["IMAP4REV1", "IDLE", "AUTH=PLAIN"]


# ── DNS : SPF, DMARC (faux résolveur) ───────────────────────────────────────────────────

class _Rec:
    def __init__(self, text):
        self.strings = [text.encode()]


class FakeResolver:
    def __init__(self, txt):
        self.txt = txt

    async def resolve(self, name, rtype):
        import dns.resolver
        if rtype == "TXT" and name in self.txt:
            return [_Rec(x) for x in self.txt[name]]
        raise dns.resolver.NXDOMAIN()


def test_spf_lookups_and_tags():
    r = FakeResolver({"_spf.example.net": ["v=spf1 include:a.example.net include:b.example.net ~all"],
                      "a.example.net": ["v=spf1 ip4:192.0.2.1 -all"], "b.example.net": ["v=spf1 mx a -all"]})
    n = asyncio.run(M._spf_lookups(r, "v=spf1 ip4:203.0.113.7 include:_spf.example.net mx -all"))
    assert n == 1 + 2 + 2 + 1          # include, ses deux include, mx et a de b, mx
    assert M._tags("v=DMARC1; p=reject; rua=mailto:dmarc@example.com") == {
        "v": "DMARC1", "p": "reject", "rua": "mailto:dmarc@example.com"}


class CountingResolver(FakeResolver):
    """Liste noire factice : 192.0.2.66 y figure, tout le reste en est absent ; compte les lectures."""

    def __init__(self):
        super().__init__({})
        self.reads = []

    async def resolve(self, name, rtype):
        self.reads.append(name)
        if name.startswith("66.2.0.192."):
            return [_A("127.0.0.2")]
        return await super().resolve(name, rtype)


class _A:
    def __init__(self, ip):
        self.ip = ip

    def to_text(self):
        return self.ip


def test_dnsbl_listing_reads_twice_a_day_and_follows_current_ips():
    now = 1_800_000_000
    z = ("bl.example",)
    run = lambda r, ips, cache: asyncio.run(M._dnsbl_listing(r, ips, z, cache, time.perf_counter() + 60, now))
    # L'adresse listée a quitté le SPF : son verdict ne compte plus, et rien n'est relu.
    r = CountingResolver()
    cache = {"192.0.2.66": {"bl.example": ("listée (127.0.0.2)", now - 3600)},
             "192.0.2.1": {"bl.example": ("propre", now - 3600)}}
    listing, read_at, errors = run(r, ["192.0.2.1"], cache)
    assert errors == [] and r.reads == [] and listing == {"192.0.2.1": {"bl.example": "propre"}}
    assert read_at == {"192.0.2.1": {"bl.example": now - 3600}}
    # Tant qu'elle y est, le verdict lu il y a moins de 12 h sert, sans relecture.
    listing, _, errors = run(r, ["192.0.2.66"], cache)
    assert errors == ["192.0.2.66 listée sur bl.example"] and r.reads == []
    # Verdicts de plus de 12 h et adresses jamais lues : 3 lectures au plus par passage, les jamais
    # lues d'abord ; celles qui attendent gardent leur dernier verdict.
    old = {ip: {"bl.example": ("propre", now - M.DNSBL_MAX_AGE_S)} for ip in ("192.0.2.2", "192.0.2.3")}
    ips = ["192.0.2.2", "192.0.2.3", "192.0.2.4", "192.0.2.5", "192.0.2.66"]
    listing, read_at, errors = run(r, ips, old)
    assert len(r.reads) == 3 and r.reads[:3] == ["4.2.0.192.bl.example", "5.2.0.192.bl.example",
                                                  "66.2.0.192.bl.example"]
    assert errors == ["192.0.2.66 listée sur bl.example"]
    assert read_at["192.0.2.66"]["bl.example"] == now and read_at["192.0.2.2"]["bl.example"] == now - M.DNSBL_MAX_AGE_S
    # Délai épuisé : plus aucune lecture ne commence.
    r = CountingResolver()
    listing, _, _ = asyncio.run(M._dnsbl_listing(r, ["192.0.2.9"], z, {}, time.perf_counter() - 1, now))
    assert r.reads == [] and listing == {"192.0.2.9": {"bl.example": M.DNSBL_NOT_READ}}


def test_merge_dnsbl_keeps_latest_read():
    from hypha.synthetics.runner import merge_dnsbl
    rows = [{"ts": 1000, "dnsbl": {"192.0.2.1": {"z": "listée (127.0.0.2)"}}, "lu": None},     # avant dnsbl_lu
            {"ts": 5000, "dnsbl": {"192.0.2.1": {"z": "propre"}, "192.0.2.2": {"z": M.DNSBL_NOT_READ}},
             "lu": {"192.0.2.1": {"z": 4000}}}]
    assert merge_dnsbl(rows) == {"192.0.2.1": {"z": ("propre", 4000)}}
    assert merge_dnsbl(rows[:1]) == {"192.0.2.1": {"z": ("listée (127.0.0.2)", 1000)}}


def test_report_lines():
    raw = (b"From: verifier@port25.com\r\nSubject: Authentication Report\r\nContent-Type: text/plain\r\n\r\n"
           b"SPF check:          pass\r\nDKIM check:         fail\r\niprev check:        pass\r\n")
    assert M._report(raw) == {"spf": "pass", "dkim": "fail", "iprev": "pass"}


# ── certificats ─────────────────────────────────────────────────────────────────────────

def test_certificate_items():
    soon = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(time.time() + 5 * 86400))
    h = C.host_item({"host_id": 4, "hostname": "proxy-01", "status": 0, "message": "", "ts": None,
                     "tags": {"subject": "example.com", "cert_path": "/etc/apache2/conf/ssl/example.org.crt",
                              "cert_source": "apache", "issuer": "R11 (Let's Encrypt)", "not_after": soon}})
    assert h["status"] == 2 and 4 <= h["days_left"] <= 5 and h["origin"] == "apache"
    s = C.synth_item({"test_id": 5, "name": "MX", "kind": "smtp", "target": "mx.example.org:25", "location": "server",
                      "ts": None, "ok": True, "error": None,
                      "details": {"ssl_days_left": 60, "ssl_not_after": int((time.time() + 60 * 86400) * 1000),
                                  "subject": "commonName=mx.example.org",
                                  "issuer": "countryName=US, organizationName=Let's Encrypt, commonName=R11"}})
    assert s["status"] == 0 and s["subject"] == "mx.example.org" and s["issuer"] == "R11 (Let's Encrypt)"
    refused = C.synth_item({"test_id": 6, "name": "x", "kind": "ssl", "target": "a:443", "location": "server",
                            "ts": None, "ok": False, "error": "certificat refusé : expiré",
                            "details": {"ssl_days_left": -3}})
    assert refused["status"] == 2


def test_internaldate_and_reply_sender():
    from datetime import datetime, timezone
    meta = b'1 (UID 5 INTERNALDATE "18-Nov-2026 12:54:08 +0100" BODY[] {12}'
    assert M._internaldate(meta) == datetime(2026, 11, 18, 11, 54, 8, tzinfo=timezone.utc).timestamp()
    raw = b"From: auth-results@verifier.port25.com\r\nSubject: Authentication Report\r\n\r\nSPF check: pass\r\n"
    assert M._from_matches(raw, "port25.com") and not M._from_matches(raw, "example.org")
