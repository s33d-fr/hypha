"""Envoi des notifications push aux appareils des destinataires d'un canal « push ».

Un canal push nomme ses destinataires (`{"users": [ids]}`) ou tout l'espace (`{"all": true}`) ; chaque
destinataire reçoit sur chacun des appareils qu'il a abonnés depuis son profil, tant qu'il est membre de
l'espace. Le message est chiffré pour l'appareil (RFC 8291) : le service de push du navigateur (Google,
Mozilla, Apple, Microsoft) ne le lit pas.

Bornes : 100 appareils par envoi, 5 en parallèle, 10 s par appel, une reprise après 2 s (ou Retry-After,
10 s au plus) sur 429, 5xx ou coupure ; 404 et 410 = abonnement expiré, supprimé. Chaque envoi est
journalisé (nombre d'appareils, réussites, échecs, retraits) sans l'adresse de l'abonnement, qui vaut
un jeton.
"""
import asyncio
import json
import logging
import time
from dataclasses import dataclass
from urllib.parse import quote, urlsplit

import httpx

from ..config import Settings
from . import webpush

log = logging.getLogger("hypha.push")

MAX_DEVICES_PER_SEND = 100
MAX_DEVICES_PER_USER = 10
CONCURRENCY = 5
TTL_S = 86400
TITLE_MAX = 120
BODY_MAX = 400
GROUP_MAX = 300
HOST_MAX = 100

# Services de push des navigateurs (suffixes d'hôte), en https seulement : une adresse d'abonnement
# ailleurs serait une porte ouverte vers le réseau du serveur (le serveur y envoie des requêtes).
PUSH_HOSTS = ("fcm.googleapis.com", "updates.push.services.mozilla.com", "push.services.mozilla.com",
              "web.push.apple.com", "notify.windows.com")

STATE_TITLES = {
    "fr": {"ALERT": "Alerte", "WARN": "Avertissement", "NODATA": "Sans données", "OK": "Rétabli"},
    "en": {"ALERT": "Alert", "WARN": "Warning", "NODATA": "No data", "OK": "Recovered"},
}
KIND_PREFIX = {
    "fr": {"renotify": "Relance · ", "escalation": "Escalade · ", "flapping": "Instable · ", "reminder": "Toujours · ",
           "test": "Essai · "},
    "en": {"renotify": "Reminder · ", "escalation": "Escalation · ", "flapping": "Flapping · ", "reminder": "Still · ",
           "test": "Test · "},
}
MONITOR_KINDS = ("alert", "renotify", "escalation", "flapping", "reminder")
TEST_TITLE = {"fr": "HYPHA · notification d'essai", "en": "HYPHA · test notification"}


class PushRefused(Exception):
    """Envoi impossible sans nouvel essai utile (aucun appareil, clé absente, tous en échec définitif)."""


def endpoint_ok(endpoint) -> bool:
    if not isinstance(endpoint, str) or len(endpoint) > 1000:
        return False
    parts = urlsplit(endpoint)
    host = (parts.hostname or "").lower()
    return parts.scheme == "https" and not parts.username and not parts.password and (parts.port in (None, 443)) \
        and any(host == h or host.endswith("." + h) for h in PUSH_HOSTS)


def validate_keys(p256dh, auth) -> tuple[str, str]:
    """Clés d'un abonnement du navigateur, rendues normalisées ; ValueError si l'une est fausse."""
    if not isinstance(p256dh, str) or not isinstance(auth, str) or len(p256dh) > 200 or len(auth) > 100:
        raise ValueError("keys.p256dh et keys.auth (base64url) attendus")
    try:
        raw_pub, raw_auth = webpush.unb64u(p256dh), webpush.unb64u(auth)
    except Exception:
        raise ValueError("keys.p256dh et keys.auth : base64url illisible")
    webpush.load_public(raw_pub)
    if len(raw_auth) != 16:
        raise ValueError("keys.auth : 16 octets attendus")
    return webpush.b64u(raw_pub), webpush.b64u(raw_auth)


def enabled(settings: Settings) -> bool:
    return bool(settings.vapid_private_key)


def public_key(settings: Settings) -> str | None:
    """Clé publique VAPID (applicationServerKey), ou None si la clé privée manque ou est illisible."""
    if not settings.vapid_private_key:
        return None
    try:
        return webpush.b64u(webpush.public_bytes(webpush.load_private(settings.vapid_private_key).public_key()))
    except Exception:
        log.error("HYPHA_VAPID_PRIVATE_KEY illisible : notifications push coupées")
        return None


def subject(settings: Settings) -> str:
    """`sub` du jeton VAPID : contact de l'exploitant (mailto: ou https:), l'adresse publique par défaut."""
    if settings.vapid_subject:
        return settings.vapid_subject
    if settings.public_url.startswith("https://"):
        return settings.public_url
    return "mailto:admin@localhost"


def _host_of(group: str) -> str | None:
    for part in group.split(","):
        k, _, v = part.partition(":")
        if k == "host" and v:
            return v
    return None


def _relative(url: str, public_url: str) -> str:
    """Lien profond relatif à l'interface : le service worker ne suit que sa propre origine."""
    if url.startswith(public_url + "/"):
        return url[len(public_url):]
    if url.startswith("/") and not url.startswith("//"):
        return url
    return "/#/problems"


def _cut(text: str, n: int) -> str:
    text = " ".join(str(text or "").split())
    return text if len(text) <= n else text[: n - 1] + "…"


def payload(n, org_id: int, lang: str, can_ack: bool, public_url: str) -> dict:
    """Charge utile d'un appareil : titre, priorité, hôte, lien profond vers le problème (et l'explication)."""
    lang = lang if lang in STATE_TITLES else "fr"
    mon = n.monitor or {}
    mid = mon.get("id") or 0
    prio = mon.get("priority")
    group = n.group or "*"
    host = _cut(_host_of(group) or "", HOST_MAX) or None
    # Un groupe démesuré ne tient pas dans un message push (4 Kio) : lien vers la liste, sans acquittement.
    exact = len(group) <= GROUP_MAX
    monitor_notice = n.kind in MONITOR_KINDS and bool(mid)
    if monitor_notice:
        state = STATE_TITLES[lang].get(n.state, n.state)
        title = KIND_PREFIX[lang].get(n.kind, "") + state + (f" · P{prio}" if prio else "") + " · " + mon.get("name", "")
        url = (f"/#/problems?explain={mid}&group={quote(group, safe='')}&org={org_id}" if exact
               else f"/#/problems?org={org_id}")
    elif n.kind == "test":
        title = TEST_TITLE.get(lang, TEST_TITLE["fr"])
        url = "/#/settings/profile"
    else:
        title = (n.subject_override or mon.get("name") or "HYPHA").removeprefix("[HYPHA] ")
        url = _relative(n.url or "", public_url)
        if url.startswith("/#/"):
            url += ("&" if "?" in url else "?") + f"org={org_id}"
    body = n.message or ""
    if host and host not in body:
        body = f"{host} — {body}" if body else host
    tag = f"hypha-{org_id}-{mid}-{webpush.topic(group)[:12]}" if mid else f"hypha-{org_id}-{n.kind}"
    return {
        "v": 1, "title": _cut(title, TITLE_MAX), "body": _cut(body, BODY_MAX), "url": url, "tag": tag,
        "org_id": org_id, "monitor_id": mid, "group": group if exact else "", "state": n.state, "priority": prio,
        "host": host, "kind": n.kind, "lang": lang,
        "can_ack": bool(can_ack and monitor_notice and exact and n.state != "OK"),
        "ts": int(time.time() * 1000),
    }


@dataclass
class Result:
    sent: int = 0
    failed: int = 0
    removed: int = 0
    first_error: str = ""

    @property
    def detail(self) -> str:
        total = self.sent + self.failed + self.removed
        out = f"envoyé sur {self.sent} appareil(s) sur {total}"
        if self.removed:
            out += f", {self.removed} abonnement(s) expiré(s) retiré(s)"
        if self.failed:
            out += f", {self.failed} échec(s) : {self.first_error}"
        return out


async def _send_one(client: httpx.AsyncClient, settings: Settings, key, sub: dict, body: dict,
                    urgency: str, topic_key: str) -> tuple[str, str]:
    """Rend ('ok' | 'gone' | 'fail', détail)."""
    endpoint = sub["endpoint"]
    if not endpoint_ok(endpoint):
        return "gone", "adresse d'abonnement refusée"
    data = json.dumps(body, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
    try:
        encrypted = webpush.encrypt(data, webpush.unb64u(sub["p256dh"]), webpush.unb64u(sub["auth"]))
    except ValueError as e:
        return "gone", f"clés de l'abonnement invalides : {e}"
    headers = {"TTL": str(TTL_S), "Urgency": urgency, "Topic": webpush.topic(topic_key),
               "Content-Encoding": "aes128gcm", "Content-Type": "application/octet-stream",
               "Authorization": webpush.vapid_header(endpoint, key, subject(settings))}
    detail = ""
    for attempt in (1, 2):
        try:
            r = await client.post(endpoint, content=encrypted, headers=headers)
        except httpx.HTTPError as e:
            detail = f"réseau : {type(e).__name__}"
            if attempt == 1:
                await asyncio.sleep(2)
                continue
            return "fail", detail
        if r.status_code in (200, 201, 202):
            return "ok", ""
        if r.status_code in (404, 410):
            return "gone", f"réponse {r.status_code}"
        detail = f"réponse {r.status_code}" + (f" ({_cut(r.text, 120)})" if r.text else "")
        if attempt == 1 and (r.status_code == 429 or r.status_code >= 500):
            try:
                wait = min(10.0, float(r.headers.get("Retry-After", "2")))
            except ValueError:
                wait = 2.0
            await asyncio.sleep(max(0.0, wait))
            continue
        return "fail", detail
    return "fail", detail


async def recipients(conn, channel_id: int) -> tuple[int | None, list[dict]]:
    """(espace du canal, appareils des destinataires encore membres), 100 au plus."""
    from ..db import fetch_all, fetch_one
    ch = await fetch_one(conn, "SELECT org_id, config FROM notification_channels WHERE id = %s", (channel_id,))
    if ch is None:
        return None, []
    cfg = ch["config"] or {}
    every = cfg.get("all") is True
    users = [u for u in (cfg.get("users") or []) if isinstance(u, int)]
    if not every and not users:
        return ch["org_id"], []
    rows = await fetch_all(conn, """
        SELECT s.id, s.user_id, s.endpoint, s.p256dh, s.auth, s.lang, m.role
          FROM push_subscriptions s JOIN memberships m ON m.user_id = s.user_id AND m.org_id = %s
         WHERE (%s OR s.user_id = ANY(%s::bigint[]))
         ORDER BY s.user_id, s.id
         LIMIT %s
    """, (ch["org_id"], every, users, MAX_DEVICES_PER_SEND))
    return ch["org_id"], rows


async def deliver(settings: Settings, client: httpx.AsyncClient, db, devices: list[dict], make_body,
                  urgency: str, topic_key: str, label: str) -> Result:
    """Envoie à chaque appareil (5 à la fois), retire les abonnements expirés, note réussites et échecs."""
    key = webpush.load_private(settings.vapid_private_key)
    sem = asyncio.Semaphore(CONCURRENCY)
    result = Result()
    ok_ids, gone_ids, failed = [], [], []

    async def one(sub):
        async with sem:
            status, detail = await _send_one(client, settings, key, sub, make_body(sub), urgency, topic_key)
        host = urlsplit(sub["endpoint"]).hostname or "?"
        if status == "ok":
            ok_ids.append(sub["id"])
        elif status == "gone":
            gone_ids.append(sub["id"])
            log.info("push %s : abonnement n° %s (%s) expiré, retiré (%s)", label, sub["id"], host, detail)
        else:
            failed.append((sub["id"], detail))
            log.warning("push %s : appareil n° %s (%s) en échec : %s", label, sub["id"], host, detail)

    await asyncio.gather(*(one(s) for s in devices))
    result.sent, result.removed, result.failed = len(ok_ids), len(gone_ids), len(failed)
    result.first_error = failed[0][1] if failed else ""
    async with db.writer() as conn:
        if ok_ids:
            await conn.execute("UPDATE push_subscriptions SET last_success_at = now(), failures = 0 "
                               "WHERE id = ANY(%s::bigint[])", (ok_ids,))
        if gone_ids:
            await conn.execute("DELETE FROM push_subscriptions WHERE id = ANY(%s::bigint[])", (gone_ids,))
        for sid, detail in failed:
            await conn.execute("UPDATE push_subscriptions SET last_error_at = now(), last_error = %s, "
                               "failures = failures + 1 WHERE id = %s", (detail[:300], sid))
    log.info("push %s : %s appareil(s), %s envoyé(s), %s échec(s), %s retiré(s)", label, len(devices),
             result.sent, result.failed, result.removed)
    return result


async def send_notice(settings: Settings, client: httpx.AsyncClient, n, db) -> str:
    """Envoi d'une notification de moniteur (ou d'essai, d'incident) par un canal push."""
    if not enabled(settings) or public_key(settings) is None:
        raise PushRefused("notifications push non configurées sur ce serveur (HYPHA_VAPID_PRIVATE_KEY)")
    async with db.writer() as conn:
        org_id, devices = await recipients(conn, n.channel["id"])
    if not devices:
        raise PushRefused("aucun appareil abonné parmi les destinataires de ce canal")
    urgency = "normal" if n.state == "OK" and n.kind != "test" else "high"
    topic_key = f"{org_id}:{(n.monitor or {}).get('id')}:{n.group}:{n.kind == 'test'}"
    res = await deliver(settings, client, db, devices,
                        lambda s: payload(n, org_id, s.get("lang") or "fr", s.get("role") in ("member", "admin", "owner"),
                                          settings.public_url),
                        urgency, topic_key, f"canal {n.channel['id']}")
    if not res.sent:
        raise PushRefused(res.detail)
    return res.detail
