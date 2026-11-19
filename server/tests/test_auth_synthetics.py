import pytest

from hypha.auth import passwords, signing
from hypha.auth.naming import org_name_from_email, slugify
from hypha.auth.oidc import pkce_pair, safe_next
from hypha.synthetics import netguard
from hypha.synthetics.probes import split_host_port
from hypha.synthetics.validate import normalize_test


def test_password_roundtrip():
    h = passwords.hash_password("correct horse")
    assert h.startswith("scrypt$32768$8$1$")
    assert passwords.verify_password("correct horse", h)
    assert not passwords.verify_password("wrong", h)
    assert not passwords.verify_password("x", None)
    assert not passwords.verify_password("x", "md5$abc")


def test_signed_state():
    secret = bytes(32)
    token = signing.sign({"s": "etat", "v": "verif"}, secret)
    assert signing.unsign(token, secret, 600)["s"] == "etat"
    assert signing.unsign(token, b"\x01" * 32, 600) is None
    assert signing.unsign(token[:-2] + "xx", secret, 600) is None
    assert signing.unsign(token, secret, -1) is None
    assert signing.unsign(None, secret, 600) is None


def test_pkce_and_next():
    import base64
    import hashlib
    verifier, challenge = pkce_pair()
    assert challenge == base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).decode().rstrip("=")
    assert safe_next("/dashboards/3") == "/dashboards/3"
    assert safe_next("https://evil.example/") == "/"
    assert safe_next("//evil.example") == "/"
    assert safe_next("/\\evil") == "/"


def test_org_names():
    assert org_name_from_email("alice@example.org") == "example"
    assert org_name_from_email("jean.dupont@gmail.com") == "jean.dupont"
    assert org_name_from_email(None) == "Mon organisation"
    assert slugify("Mairie d'Arras — Services") == "mairie-d-arras-services"
    assert slugify("!!!") == "org"


def test_netguard_public():
    assert netguard.is_public("8.8.8.8")
    for addr in ("172.16.1.245", "172.31.0.1", "127.0.0.1", "169.254.169.254", "::1", "fd00::1",
                 "::ffff:172.16.1.1", "100.64.0.1", "0.0.0.0"):
        assert not netguard.is_public(addr), addr


def test_split_host_port():
    assert split_host_port("192.0.2.1:5432") == ("192.0.2.1", 5432)
    assert split_host_port("[::1]:443") == ("::1", 443)
    assert split_host_port("https://hypha.s33d.fr/x", 443) == ("hypha.s33d.fr", 443)
    assert split_host_port("example.org") == ("example.org", None)
    with pytest.raises(ValueError):
        split_host_port("h:99999")


def test_normalize_synthetics():
    t = normalize_test({"name": "Site", "kind": "http", "target": "https://hypha.s33d.fr/"}, icmp_ok=False)
    assert t["config"]["method"] == "GET" and t["config"]["expect_status"] == 200
    assert t["config"]["follow_redirects"] is True and t["interval_s"] == 60
    assert "ssl_days_warn" not in t["config"]
    t = normalize_test({"name": "Cert", "kind": "ssl", "target": "hypha.s33d.fr"}, icmp_ok=False)
    assert t["config"]["ssl_days_warn"] == 21
    t = normalize_test({"name": "DNS", "kind": "dns", "target": "s33d.fr", "config": {"record_type": "mx"}},
                       icmp_ok=False)
    assert t["config"]["record_type"] == "MX"
    t = normalize_test({"name": "Ping", "kind": "icmp", "target": "1.1.1.1"}, icmp_ok=True)
    assert t["config"]["count"] == 3
    with pytest.raises(ValueError, match="icmp"):
        normalize_test({"name": "Ping", "kind": "icmp", "target": "1.1.1.1"}, icmp_ok=False)


@pytest.mark.parametrize("data", [
    {"name": "x", "kind": "http", "target": "ftp://x"},
    {"name": "x", "kind": "http", "target": "https://x", "config": {"method": "TRACE"}},
    {"name": "x", "kind": "http", "target": "https://x", "config": {"expect_status": 999}},
    {"name": "x", "kind": "http", "target": "https://x", "config": {"timeout_s": 600}},
    {"name": "x", "kind": "tcp", "target": "noport"},
    {"name": "x", "kind": "dns", "target": "a b"},
    {"name": "x", "kind": "dns", "target": "s33d.fr", "config": {"nameserver": "pas-une-ip"}},
    {"name": "x", "kind": "http", "target": "https://x", "interval_s": 10},
    {"name": "", "kind": "tcp", "target": "a:1"},
    {"name": "x", "kind": "ftp", "target": "a:21"},
])
def test_normalize_synthetics_errors(data):
    with pytest.raises(ValueError):
        normalize_test(data, icmp_ok=True)
