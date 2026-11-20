"""Notifications push : appareils d'un utilisateur, essai, acquittement depuis une notification.

Un appareil (navigateur ou application installée) appartient à une personne, pas à un espace : il
s'abonne depuis le profil, avec la session de l'utilisateur (jamais une clé d'application), et reçoit
les canaux « push » de chaque espace où cette personne est membre et nommée destinataire.

Le bouton « Acquitter » d'une notification appelle POST /push/ack avec le cookie de session du
navigateur : aucun jeton n'est mis dans la notification. L'espace visé est vérifié (membre, rôle
« member » au moins), sans changer l'espace courant de la session.
"""
import logging

from fastapi import APIRouter, Depends, Request
from pydantic import BaseModel, Field

from .. import audit
from ..auth.principal import ROLE_RANK, Principal, any_user
from ..db import fetch_all, fetch_one
from ..errors import ApiError, bad_request, forbidden, not_found
from ..monitors import notify
from ..push import service as push
from ..runtime import rt
from ..util import RateLimiter
from . import monitors
from .common import done, ok

log = logging.getLogger("hypha.push")
router = APIRouter()

SUB_FIELDS = ("id, endpoint, label, lang, created_at, last_success_at, last_error_at, last_error, failures")
_test_limiter = RateLimiter(6)


def _person(p: Principal) -> Principal:
    if p.session_id is None or p.user_id is None:
        raise ApiError(400, "not_a_session", "les appareils s'abonnent depuis une session de l'interface, "
                                             "pas avec une clé d'application")
    return p


@router.get("/push/config")
async def config(p: Principal = Depends(any_user)):
    s = rt().settings
    key = push.public_key(s)
    return ok({"enabled": key is not None, "public_key": key, "max_devices": push.MAX_DEVICES_PER_USER})


@router.get("/push/subscriptions")
async def list_devices(p: Principal = Depends(any_user)):
    p = _person(p)
    async with rt().db.writer() as conn:
        rows = await fetch_all(conn, f"SELECT {SUB_FIELDS} FROM push_subscriptions WHERE user_id = %s "
                                     "ORDER BY created_at DESC", (p.user_id,))
    return ok(rows)


class Keys(BaseModel):
    p256dh: str = Field(min_length=10, max_length=200)
    auth: str = Field(min_length=10, max_length=100)


class SubscriptionIn(BaseModel):
    endpoint: str = Field(min_length=10, max_length=1000)
    keys: Keys
    label: str = Field("", max_length=100)
    lang: str = Field("fr", pattern="^(fr|en)$")


@router.post("/push/subscriptions")
async def subscribe(body: SubscriptionIn, request: Request, p: Principal = Depends(any_user)):
    p = _person(p)
    if push.public_key(rt().settings) is None:
        raise ApiError(409, "push_disabled", "notifications push non configurées sur ce serveur")
    if not push.endpoint_ok(body.endpoint):
        raise bad_request("adresse d'abonnement refusée : service de push du navigateur non reconnu (https)",
                          "invalid_subscription")
    try:
        p256dh, auth = push.validate_keys(body.keys.p256dh, body.keys.auth)
    except ValueError as e:
        raise bad_request(str(e), "invalid_subscription")
    label = " ".join(body.label.split())[:100]
    ua = (request.headers.get("user-agent") or "")[:300]
    async with rt().db.writer() as conn:
        n = await fetch_one(conn, "SELECT count(*) AS n FROM push_subscriptions WHERE user_id = %s AND endpoint <> %s",
                            (p.user_id, body.endpoint))
        if n and n["n"] >= push.MAX_DEVICES_PER_USER:
            raise ApiError(409, "too_many_devices", f"{push.MAX_DEVICES_PER_USER} appareils au plus : retirez-en un "
                                                    "dans votre profil")
        # Même navigateur réabonné (ou passé à un autre compte) : la ligne existante est reprise.
        row = await fetch_one(conn, f"""
            INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth, label, lang, user_agent)
            VALUES (%s, %s, %s, %s, %s, %s, %s)
            ON CONFLICT (endpoint) DO UPDATE
               SET user_id = EXCLUDED.user_id, p256dh = EXCLUDED.p256dh, auth = EXCLUDED.auth,
                   label = EXCLUDED.label, lang = EXCLUDED.lang, user_agent = EXCLUDED.user_agent,
                   failures = 0, last_error = NULL, last_error_at = NULL
            RETURNING {SUB_FIELDS}
        """, (p.user_id, body.endpoint, p256dh, auth, label, body.lang, ua))
        await audit.record(conn, org_id=p.org_id, principal=p, action="create", object_type="push_device",
                           object_id=row["id"], summary=f"appareil « {label or 'sans nom'} » abonné aux notifications push",
                           after={"label": label, "lang": body.lang}, request=request)
    return ok(row, 201)


@router.delete("/push/subscriptions/{sub_id}")
async def unsubscribe(sub_id: int, request: Request, p: Principal = Depends(any_user)):
    p = _person(p)
    async with rt().db.writer() as conn:
        row = await fetch_one(conn, "DELETE FROM push_subscriptions WHERE id = %s AND user_id = %s RETURNING id, label",
                              (sub_id, p.user_id))
        if row is None:
            raise not_found("appareil introuvable")
        await audit.record(conn, org_id=p.org_id, principal=p, action="delete", object_type="push_device",
                           object_id=sub_id, summary=f"appareil « {row['label'] or 'sans nom'} » désabonné",
                           request=request)
    return done()


@router.post("/push/subscriptions/{sub_id}/test")
async def test_device(sub_id: int, p: Principal = Depends(any_user)):
    """Notification d'essai sur un appareil de l'utilisateur ; rend {ok, detail}."""
    p = _person(p)
    s = rt().settings
    if push.public_key(s) is None:
        raise ApiError(409, "push_disabled", "notifications push non configurées sur ce serveur")
    if not _test_limiter.allow(p.user_id):
        raise ApiError(429, "rate_limited", "trop d'essais ; réessayez dans une minute")
    async with rt().db.writer() as conn:
        sub = await fetch_one(conn, "SELECT id, user_id, endpoint, p256dh, auth, lang FROM push_subscriptions "
                                    "WHERE id = %s AND user_id = %s", (sub_id, p.user_id))
    if sub is None:
        raise not_found("appareil introuvable")
    lang = sub.get("lang") or "fr"
    text = {"fr": "Si vous lisez ceci, les alertes de HYPHA arriveront sur cet appareil.",
            "en": "If you can read this, HYPHA alerts will reach this device."}
    notice = notify.Notice({"id": 0, "kind": "push", "name": "", "config": {}}, {"id": 0, "name": "", "type": "test"},
                           "*", "OK", None, None, text.get(lang, text["fr"]), s.public_url, kind="test")
    async with notify.make_client() as client:
        res = await push.deliver(s, client, rt().db, [sub], lambda d: push.payload(notice, p.org_id or 0, lang, False,
                                                                                  s.public_url),
                                 "high", f"test:{sub_id}", f"essai de l'appareil {sub_id}")
    return ok({"ok": res.sent == 1, "detail": res.detail})


class AckIn(BaseModel):
    org_id: int
    monitor_id: int
    group: str = Field(min_length=1, max_length=1000)


@router.post("/push/ack")
async def ack(body: AckIn, request: Request, p: Principal = Depends(any_user)):
    """« Acquitter » d'une notification : l'espace de la notification, pas forcément l'espace courant."""
    p = _person(p)
    async with rt().db.writer() as conn:
        m = await fetch_one(conn, "SELECT role FROM memberships WHERE org_id = %s AND user_id = %s",
                            (body.org_id, p.user_id))
    if m is None:
        raise forbidden("vous n'êtes pas membre de cet espace")
    if ROLE_RANK.get(m["role"], 0) < ROLE_RANK["member"]:
        raise forbidden("rôle « member » requis")
    target = Principal(p.user_id, p.email, p.name, body.org_id, m["role"], session_id=p.session_id)
    rows = await monitors.apply_ack(target, body.monitor_id, [body.group], "notification push", request)
    return ok({"acked": [{"group": r["group_key"], "state": r["state"], "acked_at": r["acked_at"]} for r in rows]})
