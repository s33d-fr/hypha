"""Notifications push : chiffrement (vecteur de la RFC 8291), VAPID (RFC 8292), charge utile, envoi borné
(abonnements expirés retirés, reprise sur 5xx), canal « push », routes des appareils et acquittement
depuis une notification. Aucun réseau (transport httpx simulé), aucune base (fausse connexion)."""
import asyncio
import json
from contextlib import asynccontextmanager
from datetime import datetime, timezone

import httpx
import jwt
import pytest
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import ec
from cryptography.hazmat.primitives.ciphers.aead import AESGCM
from cryptography.hazmat.primitives.kdf.hkdf import HKDF

from conftest import placeholders
from test_routes_nodb import APP_KEY, FakeCursor  # pose aussi l'environnement et les modules de remplacement

from fastapi.testclient import TestClient  # noqa: E402

from hypha.api.settings_routes import validate_channel  # noqa: E402
from hypha.app import create_app  # noqa: E402
from hypha.config import get_settings  # noqa: E402
from hypha.monitors.notify import Notice  # noqa: E402
from hypha.push import service, webpush  # noqa: E402
from hypha.runtime import Runtime, set_runtime  # noqa: E402

NOW = datetime.now(timezone.utc)

# RFC 8291, annexe A.
RFC_PLAINTEXT = b"When I grow up, I want to be a watermelon"
RFC_AS_PRIVATE = "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"
RFC_AS_PUBLIC = ("BP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A8")
RFC_UA_PRIVATE = "q1dXpw3UpT5VOmu_cf_v6ih07Aems3njxI-JWgLcM94"
RFC_UA_PUBLIC = ("BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4")
RFC_AUTH = "BTBZMqHH6r4Tts7J_aSIgg"
RFC_SALT = "DGv6ra1nlYgDCS1FRnbzlw"
RFC_BODY = ("DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_y"
            "l95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN")


def decrypt(body: bytes, ua_private: ec.EllipticCurvePrivateKey, auth: bytes) -> bytes:
    """Déchiffrement côté navigateur (RFC 8291 § 3.4), écrit à part du code testé."""
    salt, rs, idlen = body[:16], int.from_bytes(body[16:20], "big"), body[20]
    as_public = body[21:21 + idlen]
    assert rs == 4096 and idlen == 65
    ua_public = ua_private.public_key().public_bytes(serialization.Encoding.X962,
                                                    serialization.PublicFormat.UncompressedPoint)
    shared = ua_private.exchange(ec.ECDH(), ec.EllipticCurvePublicKey.from_encoded_point(ec.SECP256R1(), as_public))
    ikm = HKDF(hashes.SHA256(), 32, auth, b"WebPush: info\x00" + ua_public + as_public).derive(shared)
    cek = HKDF(hashes.SHA256(), 16, salt, b"Content-Encoding: aes128gcm\x00").derive(ikm)
    nonce = HKDF(hashes.SHA256(), 12, salt, b"Content-Encoding: nonce\x00").derive(ikm)
    plain = AESGCM(cek).decrypt(nonce, body[21 + idlen:], None)
    assert plain.endswith(b"\x02")
    return plain[:-1]


def ua_keys():
    key = ec.generate_private_key(ec.SECP256R1())
    pub = key.public_key().public_bytes(serialization.Encoding.X962, serialization.PublicFormat.UncompressedPoint)
    return key, webpush.b64u(pub), webpush.b64u(b"0123456789abcdef")


# ── chiffrement et VAPID ───────────────────────────────────────────────────────

def test_rfc8291_vector():
    asp = webpush.load_private(RFC_AS_PRIVATE)
    assert webpush.b64u(webpush.public_bytes(asp.public_key())) == RFC_AS_PUBLIC
    body = webpush.encrypt(RFC_PLAINTEXT, webpush.unb64u(RFC_UA_PUBLIC), webpush.unb64u(RFC_AUTH),
                           as_private=asp, salt=webpush.unb64u(RFC_SALT))
    assert webpush.b64u(body) == RFC_BODY
    assert decrypt(body, webpush.load_private(RFC_UA_PRIVATE), webpush.unb64u(RFC_AUTH)) == RFC_PLAINTEXT


def test_encrypt_round_trip_fresh_keys_and_limits():
    key, pub, auth = ua_keys()
    data = json.dumps({"title": "Alerte · P1 · Disque plein", "body": "é" * 500}).encode()
    a = webpush.encrypt(data, webpush.unb64u(pub), webpush.unb64u(auth))
    b = webpush.encrypt(data, webpush.unb64u(pub), webpush.unb64u(auth))
    assert a != b                                       # clé éphémère et sel neufs à chaque message
    assert decrypt(a, key, webpush.unb64u(auth)) == data
    with pytest.raises(ValueError):
        webpush.encrypt(b"x" * 4000, webpush.unb64u(pub), webpush.unb64u(auth))
    with pytest.raises(ValueError):
        webpush.load_public(b"\x04" + b"\x01" * 64)     # hors de la courbe
    with pytest.raises(ValueError):
        webpush.load_private("abc")


def test_vapid_header():
    private, public = webpush.generate_vapid()
    key = webpush.load_private(private)
    h = webpush.vapid_header("https://fcm.googleapis.com/fcm/send/abc:def", key, "https://hypha.s33d.fr", now=1_000_000)
    assert h.startswith("vapid t=") and h.endswith(", k=" + public)
    token = h[len("vapid t="):h.index(",")]
    assert jwt.get_unverified_header(token) == {"typ": "JWT", "alg": "ES256"}
    claims = jwt.decode(token, key.public_key(), algorithms=["ES256"], audience="https://fcm.googleapis.com",
                        options={"verify_exp": False})
    assert claims == {"aud": "https://fcm.googleapis.com", "exp": 1_000_000 + 12 * 3600, "sub": "https://hypha.s33d.fr"}
    t = webpush.topic("1:3:host:web-01:False")
    assert len(t) == 32 and t == webpush.topic("1:3:host:web-01:False") and "=" not in t


def test_endpoint_and_keys_validation():
    ok = ["https://fcm.googleapis.com/fcm/send/x", "https://updates.push.services.mozilla.com/wpush/v2/x",
          "https://web.push.apple.com/QW", "https://db5p.notify.windows.com/w/?token=x"]
    bad = ["http://fcm.googleapis.com/fcm/send/x", "https://evil.example/fcm.googleapis.com", "https://192.0.2.10/x",
           "https://fcm.googleapis.com.evil.example/x", "https://user@fcm.googleapis.com/x",
           "https://fcm.googleapis.com:8443/x", "https://notify.windows.com.evil/x", None, "x" * 2000]
    assert all(service.endpoint_ok(u) for u in ok)
    assert not any(service.endpoint_ok(u) for u in bad)
    _, pub, auth = ua_keys()
    assert service.validate_keys(pub + "==", auth) == (pub, auth)
    for p256dh, a in [(pub, "AAAA"), ("AAAA", auth), (pub, "!!"), (1, auth)]:
        with pytest.raises(ValueError):
            service.validate_keys(p256dh, a)


# ── charge utile ───────────────────────────────────────────────────────────────

def notice(**kw):
    base = dict(channel={"id": 5, "kind": "push", "name": "Astreinte", "config": {"all": True}},
                monitor={"id": 3, "name": "Disque presque plein", "type": "metric", "priority": 1},
                group="device:/dev/sda1,host:web-01", state="ALERT", previous="OK", value=95.0,
                message="Le disque / de web-01 est rempli à 95 %.", url="https://hypha.s33d.fr/#/monitors/3")
    base.update(kw)
    return Notice(**base)


def test_payload_monitor_alert_recovery_and_roles():
    p = service.payload(notice(), 1, "fr", True, "https://hypha.s33d.fr")
    assert p["title"] == "Alerte · P1 · Disque presque plein"
    assert p["url"] == "/#/problems?explain=3&group=device%3A%2Fdev%2Fsda1%2Chost%3Aweb-01&org=1"
    assert p["host"] == "web-01" and p["can_ack"] and p["state"] == "ALERT" and p["priority"] == 1
    assert p["tag"].startswith("hypha-1-3-") and p["lang"] == "fr"
    en = service.payload(notice(kind="escalation"), 1, "en", True, "https://hypha.s33d.fr")
    assert en["title"] == "Escalation · Alert · P1 · Disque presque plein"
    ok = service.payload(notice(state="OK", message="Rétabli."), 1, "fr", True, "https://hypha.s33d.fr")
    assert ok["title"].startswith("Rétabli") and not ok["can_ack"] and ok["tag"] == p["tag"]
    assert ok["body"] == "web-01 — Rétabli."
    assert not service.payload(notice(), 1, "fr", False, "https://hypha.s33d.fr")["can_ack"]   # lecteur
    big = service.payload(notice(message="é" * 5000, group="host:" + "h" * 2000, monitor={"id": 3, "priority": 1,
                                 "name": "€" * 500}), 1, "fr", True, "https://hypha.s33d.fr")
    assert len(big["body"]) <= service.BODY_MAX and big["group"] == "" and big["url"] == "/#/problems?org=1"
    assert not big["can_ack"] and len(big["host"]) <= service.HOST_MAX
    assert len(json.dumps(big, ensure_ascii=False).encode()) < webpush.MAX_PLAINTEXT


def test_payload_test_and_incident():
    t = service.payload(notice(monitor={"id": 0, "name": "Essai de canal", "type": "test"}, group="*", state="OK",
                               kind="test", url="https://hypha.s33d.fr"), 1, "fr", True, "https://hypha.s33d.fr")
    assert t["title"] == "HYPHA · notification d'essai" and t["url"] == "/#/settings/profile" and not t["can_ack"]
    inc = service.payload(notice(monitor={"id": 7, "name": "INC-12 API", "type": "incident", "priority": 2},
                                 group="*", kind="incident", subject_override="[HYPHA] INC-12 · Déclaré · SEV-2 · API",
                                 url="https://hypha.s33d.fr/#/incidents/7"), 4, "fr", True, "https://hypha.s33d.fr")
    assert inc["title"] == "INC-12 · Déclaré · SEV-2 · API" and inc["url"] == "/#/incidents/7?org=4"
    assert not inc["can_ack"]
    other = service.payload(notice(kind="incident", url="https://ailleurs.example/x"), 1, "fr", True,
                            "https://hypha.s33d.fr")
    assert other["url"] == "/#/problems?org=1"


def test_validate_push_channel():
    assert validate_channel("push", {"all": True}) == {"all": True}
    assert validate_channel("push", {"users": [4, 2, 4]}) == {"users": [2, 4]}
    for bad in [{}, {"users": []}, {"users": ["a"]}, {"users": [True]}, {"users": [0]}, {"users": list(range(1, 60))},
                {"all": "oui"}]:
        with pytest.raises(ValueError):
            validate_channel("push", bad)


# ── envoi : transport simulé, fausse base ──────────────────────────────────────

class LogConn:
    def __init__(self, log, rows=None):
        self.log, self.rows = log, rows or {}

    async def execute(self, sql, params=None):
        n = placeholders(sql)
        assert n == len(params or ()), f"{n} %s pour {len(params or ())} paramètres :\n{sql}"
        self.log.append((" ".join(sql.split()), params))
        s = " ".join(sql.split())
        for prefix, rows in self.rows.items():
            if s.startswith(prefix):
                return FakeCursor(rows, [])
        return FakeCursor([], [])


class LogDB:
    def __init__(self, rows=None):
        self.log, self.rows = [], rows or {}

    @asynccontextmanager
    async def writer(self, timeout=5.0):
        yield LogConn(self.log, self.rows)

    reader = writer


def settings_with_key(monkeypatch):
    private, _ = webpush.generate_vapid()
    monkeypatch.setenv("HYPHA_VAPID_PRIVATE_KEY", private)
    get_settings.cache_clear()
    return get_settings()


@pytest.fixture()
def clear_settings():
    yield
    get_settings.cache_clear()


def device(i, endpoint, pub, auth, lang="fr", role="member"):
    return {"id": i, "user_id": 1, "endpoint": endpoint, "p256dh": pub, "auth": auth, "lang": lang, "role": role}


def test_deliver_statuses_retry_and_cleanup(monkeypatch, clear_settings):
    s = settings_with_key(monkeypatch)
    key, pub, auth = ua_keys()
    seen = {}

    def handler(request: httpx.Request) -> httpx.Response:
        path = request.url.path
        seen.setdefault(path, 0)
        seen[path] += 1
        assert request.headers["content-encoding"] == "aes128gcm" and request.headers["ttl"] == "86400"
        assert request.headers["authorization"].startswith("vapid t=") and len(request.headers["topic"]) == 32
        body = json.loads(decrypt(request.content, key, webpush.unb64u(auth)))
        assert body["title"].startswith("Alerte")
        if path == "/gone":
            return httpx.Response(410)
        if path == "/flaky" and seen[path] == 1:
            return httpx.Response(503, headers={"Retry-After": "0"})
        if path == "/refused":
            return httpx.Response(403, text="BadJwtToken")
        return httpx.Response(201)

    async def no_sleep(_):
        return None

    monkeypatch.setattr(service.asyncio, "sleep", no_sleep)
    db = LogDB()
    devices = [device(1, "https://fcm.googleapis.com/ok", pub, auth), device(2, "https://fcm.googleapis.com/gone", pub, auth),
               device(3, "https://fcm.googleapis.com/flaky", pub, auth),
               device(4, "https://fcm.googleapis.com/refused", pub, auth),
               device(5, "https://evil.example/x", pub, auth)]

    async def run():
        async with httpx.AsyncClient(transport=httpx.MockTransport(handler)) as client:
            return await service.deliver(s, client, db, devices,
                                         lambda d: service.payload(notice(), 1, d["lang"], True, s.public_url),
                                         "high", "1:3:x", "canal 5")

    res = asyncio.run(run())
    assert (res.sent, res.failed, res.removed) == (2, 1, 2)
    assert seen == {"/ok": 1, "/gone": 1, "/flaky": 2, "/refused": 1}
    assert "403" in res.first_error and "2 abonnement(s) expiré(s)" in res.detail
    sqls = [q for q, _ in db.log]
    assert any(q.startswith("UPDATE push_subscriptions SET last_success_at") for q in sqls)
    deleted = [p for q, p in db.log if q.startswith("DELETE FROM push_subscriptions")]
    assert deleted == [([2, 5],)] or sorted(deleted[0][0]) == [2, 5]
    assert any(q.startswith("UPDATE push_subscriptions SET last_error_at") for q in sqls)


def test_send_notice_refusals(monkeypatch, clear_settings):
    monkeypatch.delenv("HYPHA_VAPID_PRIVATE_KEY", raising=False)
    get_settings.cache_clear()
    with pytest.raises(service.PushRefused):
        asyncio.run(service.send_notice(get_settings(), None, notice(), LogDB()))
    s = settings_with_key(monkeypatch)
    db = LogDB({"SELECT org_id, config FROM notification_channels": [{"org_id": 1, "config": {"users": [9]}}]})
    with pytest.raises(service.PushRefused, match="aucun appareil"):
        asyncio.run(service.send_notice(s, None, notice(), db))
    sql, params = [x for x in db.log if "FROM push_subscriptions s JOIN memberships" in x[0]][0]
    assert params == (1, False, [9], service.MAX_DEVICES_PER_SEND)


def test_send_notice_viewer_cannot_ack(monkeypatch, clear_settings):
    s = settings_with_key(monkeypatch)
    key, pub, auth = ua_keys()
    got = []

    def handler(request):
        got.append(json.loads(decrypt(request.content, key, webpush.unb64u(auth))))
        return httpx.Response(201)

    db = LogDB({"SELECT org_id, config FROM notification_channels": [{"org_id": 2, "config": {"all": True}}],
                "SELECT s.id, s.user_id": [device(1, "https://fcm.googleapis.com/a", pub, auth, role="viewer"),
                                           device(2, "https://fcm.googleapis.com/b", pub, auth, "en", "admin")]})

    async def run():
        async with httpx.AsyncClient(transport=httpx.MockTransport(handler)) as client:
            return await service.send_notice(s, client, notice(), db)

    assert asyncio.run(run()) == "envoyé sur 2 appareil(s) sur 2"
    by_lang = {g["lang"]: g for g in got}
    assert by_lang["fr"]["can_ack"] is False and by_lang["en"]["can_ack"] is True
    assert by_lang["en"]["title"].startswith("Alert · P1") and by_lang["en"]["org_id"] == 2


# ── routes ─────────────────────────────────────────────────────────────────────

SESSION = "s" * 43
STATE: dict = {}


def respond(sql, params):
    s = " ".join(sql.split())
    if "FROM sessions s JOIN users u" in s:
        return [{"id": SESSION, "user_id": 1, "org_id": 1, "email": "alice@example.org", "name": "Alice",
                 "role": STATE.get("session_role", "owner")}]
    if "FROM api_keys k" in s:
        return [{"id": 2, "org_id": 1, "kind": "app", "revoked": False, "created_by": 1, "role": "owner",
                 "email": "alice@example.org", "name": "Alice"}]
    if s.startswith("SELECT count(*) AS n FROM push_subscriptions"):
        return [{"n": STATE.get("devices", 0)}]
    if s.startswith("INSERT INTO push_subscriptions"):
        return [{"id": 12, "endpoint": params[1], "label": params[4], "lang": params[5], "created_at": NOW,
                 "last_success_at": None, "last_error_at": None, "last_error": None, "failures": 0}]
    if s.startswith("SELECT id, endpoint, label"):
        return [{"id": 12, "endpoint": "https://fcm.googleapis.com/x", "label": "Chrome · Android", "lang": "fr",
                 "created_at": NOW, "last_success_at": None, "last_error_at": None, "last_error": None, "failures": 0}]
    if s.startswith("DELETE FROM push_subscriptions WHERE id = %s AND user_id = %s"):
        return [{"id": 12, "label": "Chrome"}] if params[0] == 12 else []
    if s.startswith("SELECT role FROM memberships WHERE org_id = %s AND user_id = %s"):
        return [{"role": STATE["ack_role"]}] if STATE.get("ack_role") else []
    if "FROM monitors WHERE org_id = %s AND id = %s" in s:
        return [{"id": params[1], "name": "Disque", "type": "metric", "org": params[0]}]
    if s.startswith("UPDATE monitor_states SET acked_by = %s"):
        return [{"group_key": g, "state": "ALERT", "acked_at": NOW} for g in params[3]]
    if s.startswith("SELECT user_id FROM memberships WHERE org_id = %s AND user_id = ANY"):
        return [{"user_id": u} for u in params[1] if u != 999]
    if s.startswith("INSERT INTO notification_channels"):
        return [{"id": 40, "kind": params[1], "name": params[2], "config": json.loads(params[3]), "enabled": True,
                 "created_at": NOW}]
    return []


class RouteConn:
    def __init__(self, log):
        self.log = log

    async def execute(self, sql, params=None):
        n = placeholders(sql)
        assert n == len(params or ()), f"{n} %s pour {len(params or ())} paramètres :\n{sql}\n{params}"
        self.log.append((" ".join(sql.split()), params))
        return FakeCursor(respond(sql, params), [])


class RouteDB:
    def __init__(self):
        self.log = []
        self.state = "ok"

    def mark(self, ok):
        self.state = "ok" if ok else "erreur"

    @asynccontextmanager
    async def writer(self, timeout=5.0):
        yield RouteConn(self.log)

    reader = writer


@pytest.fixture()
def client(monkeypatch):
    STATE.clear()
    private, _ = webpush.generate_vapid()
    monkeypatch.setenv("HYPHA_VAPID_PRIVATE_KEY", private)
    get_settings.cache_clear()
    db = RouteDB()
    set_runtime(Runtime(settings=get_settings(), db=db))
    c = TestClient(create_app(), raise_server_exceptions=True)
    c.cookies.set("hypha_session", SESSION)
    c.db = db
    yield c
    set_runtime(None)
    get_settings.cache_clear()


def sub_body(**kw):
    _, pub, auth = ua_keys()
    body = {"endpoint": "https://fcm.googleapis.com/fcm/send/abc", "keys": {"p256dh": pub, "auth": auth},
            "label": "Chrome · Android", "lang": "fr"}
    body.update(kw)
    return body


def test_config_and_subscribe(client):
    r = client.get("/api/v1/push/config")
    assert r.status_code == 200 and r.json()["enabled"] and len(webpush.unb64u(r.json()["public_key"])) == 65
    r = client.post("/api/v1/push/subscriptions", json=sub_body())
    assert r.status_code == 201, r.text
    assert r.json()["id"] == 12 and r.json()["label"] == "Chrome · Android"
    ins = [p for q, p in client.db.log if q.startswith("INSERT INTO push_subscriptions")][0]
    assert ins[0] == 1 and ins[1].startswith("https://fcm.googleapis.com/")
    assert any(q.startswith("INSERT INTO hypha.audit_log") for q, _ in client.db.log)
    assert client.post("/api/v1/push/subscriptions", json=sub_body(endpoint="https://evil.example/x")).status_code == 400
    assert client.post("/api/v1/push/subscriptions", json=sub_body(keys={"p256dh": "A" * 87, "auth": "B" * 22})
                       ).status_code == 400
    STATE["devices"] = 10
    assert client.post("/api/v1/push/subscriptions", json=sub_body()).json()["error"] == "too_many_devices"


def test_devices_need_a_session(client):
    client.cookies.clear()
    r = client.post("/api/v1/push/subscriptions", json=sub_body(), headers={"X-Hypha-App-Key": APP_KEY})
    assert r.status_code == 400 and r.json()["error"] == "not_a_session"
    assert client.get("/api/v1/push/subscriptions").status_code == 401


def test_list_and_delete_own_devices(client):
    assert client.get("/api/v1/push/subscriptions").json()[0]["label"] == "Chrome · Android"
    q, params = [x for x in client.db.log if x[0].startswith("SELECT id, endpoint, label")][0]
    assert params == (1,)
    assert client.delete("/api/v1/push/subscriptions/12").status_code == 200
    assert client.delete("/api/v1/push/subscriptions/13").status_code == 404


def test_disabled_server(client, monkeypatch):
    monkeypatch.delenv("HYPHA_VAPID_PRIVATE_KEY")
    get_settings.cache_clear()
    set_runtime(Runtime(settings=get_settings(), db=client.db))
    assert client.get("/api/v1/push/config").json() == {"enabled": False, "public_key": None, "max_devices": 10}
    assert client.post("/api/v1/push/subscriptions", json=sub_body()).json()["error"] == "push_disabled"


def test_ack_from_notification(client):
    STATE["ack_role"] = "member"
    r = client.post("/api/v1/push/ack", json={"org_id": 7, "monitor_id": 3, "group": "host:web-01"})
    assert r.status_code == 200, r.text
    assert r.json()["acked"][0]["group"] == "host:web-01"
    upd = [p for q, p in client.db.log if q.startswith("UPDATE monitor_states SET acked_by")][0]
    assert upd == (1, "notification push", 3, ["host:web-01"])
    load = [p for q, p in client.db.log if "FROM monitors WHERE org_id = %s AND id = %s" in q][0]
    assert load == (7, 3)                               # l'espace de la notification, pas celui de la session
    audit = [p for q, p in client.db.log if q.startswith("INSERT INTO hypha.audit_log")][0]
    assert audit[0] == 7 and audit[3] == "ack"
    STATE["ack_role"] = "viewer"
    assert client.post("/api/v1/push/ack", json={"org_id": 7, "monitor_id": 3, "group": "*"}).status_code == 403
    STATE["ack_role"] = None
    assert client.post("/api/v1/push/ack", json={"org_id": 8, "monitor_id": 3, "group": "*"}).status_code == 403
    client.cookies.clear()
    assert client.post("/api/v1/push/ack", json={"org_id": 7, "monitor_id": 3, "group": "*"}).status_code == 401


def test_push_channel_members_checked(client):
    r = client.post("/api/v1/channels", json={"kind": "push", "name": "Astreinte", "config": {"users": [1, 999]}})
    assert r.status_code == 400 and "999" in r.json()["detail"]
    r = client.post("/api/v1/channels", json={"kind": "push", "name": "Astreinte", "config": {"users": [1]}})
    assert r.status_code == 201 and r.json()["config"] == {"users": [1]}
    r = client.post("/api/v1/channels", json={"kind": "push", "name": "Tous", "config": {"all": True}})
    assert r.status_code == 201
