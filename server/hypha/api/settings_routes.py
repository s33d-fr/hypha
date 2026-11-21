"""Réglages (contrat §§ 7, 18) : clés d'API, canaux de notification, membres, installation de l'agent."""
import asyncio
import re
from urllib.parse import urlsplit

from fastapi import APIRouter, Body, Depends, Request
from pydantic import BaseModel, Field

from .. import audit
from ..auth import accounts
from ..auth.passwords import hash_password
from ..auth.principal import Principal, admin, viewer
from ..db import fetch_all, fetch_one
from ..errors import bad_request, forbidden, not_found
from ..monitors import notify
from ..push import service as push
from ..runtime import rt
from ..synthetics.netguard import TargetRefused
from ..util import jdump
from . import fleet
from .common import done, ok

router = APIRouter()

KEY_FIELDS = "id, name, kind, prefix, created_at, last_used_at, revoked_at, created_by"
CHANNEL_FIELDS = "id, kind, name, config, enabled, created_at"
CHANNEL_KINDS = ("email", "webhook", "slack", "teams", "telegram", "discord", "googlechat", "mattermost", "ntfy",
                 "pagerduty", "opsgenie", "push", "glpi")
PUSH_MAX_USERS = 50
SECRET_FIELDS = ("headers", "bot_token", "routing_key", "api_key", "token", "app_token", "user_token")
_EMAIL_RE = re.compile(r"^[^@\s]+@[^@\s]+\.[^@\s]+$")
_TELEGRAM_TOKEN_RE = re.compile(r"^\d{3,15}:[A-Za-z0-9_-]{20,100}$")
_TELEGRAM_CHAT_RE = re.compile(r"^(-?\d{1,20}|@[A-Za-z0-9_]{4,64})$")
_NTFY_TOPIC_RE = re.compile(r"^[A-Za-z0-9_-]{1,64}$")
_PAGERDUTY_KEY_RE = re.compile(r"^[A-Za-z0-9]{32}$")
_OPSGENIE_KEY_RE = re.compile(r"^[A-Za-z0-9-]{20,64}$")
_GLPI_TOKEN_RE = re.compile(r"^[A-Za-z0-9]{20,100}$")
KEY_PLACEHOLDER = "<HYPHA_API_KEY>"


# ── clés ───────────────────────────────────────────────────────────────────────

@router.get("/keys")
async def list_keys(p: Principal = Depends(admin)):
    async with rt().db.reader() as conn:
        return ok(await fetch_all(conn, f"SELECT {KEY_FIELDS} FROM api_keys WHERE org_id = %s "
                                        "ORDER BY revoked_at IS NOT NULL, created_at DESC", (p.org_id,)))


class KeyIn(BaseModel):
    name: str = Field(min_length=1, max_length=200)
    kind: str = Field("ingest", pattern="^(ingest|app)$")


@router.post("/keys")
async def create_key(body: KeyIn, request: Request, p: Principal = Depends(admin)):
    async with rt().db.writer() as conn:
        row, key = await accounts.create_api_key(conn, p.org_id, body.name.strip(), body.kind, p.user_id)
        await audit.record(conn, org_id=p.org_id, principal=p, action="create", object_type="key", object_id=row["id"],
                           summary=f"clé {body.kind} « {row['name']} » créée ({row['prefix']}…)",
                           after={"name": row["name"], "kind": row["kind"], "prefix": row["prefix"]}, request=request)
    # La clé entière n'existe qu'ici : elle n'est stockée qu'en empreinte sha256.
    return ok({**row, "key": key}, 201)


@router.delete("/keys/{key_id}")
async def revoke_key(key_id: int, request: Request, p: Principal = Depends(admin)):
    async with rt().db.writer() as conn:
        row = await fetch_one(conn, "UPDATE api_keys SET revoked_at = coalesce(revoked_at, now()) "
                                    "WHERE org_id = %s AND id = %s RETURNING key_hash, name, kind, prefix",
                              (p.org_id, key_id))
        if row is None:
            raise not_found("clé introuvable")
        await audit.record(conn, org_id=p.org_id, principal=p, action="revoke", object_type="key", object_id=key_id,
                           summary=f"clé {row['kind']} « {row['name']} » révoquée ({row['prefix']}…)", request=request)
    # Ce nœud oublie la clé tout de suite ; les autres au plus tard après 60 s (durée du cache).
    rt().ingest_keys.pop(bytes(row["key_hash"]))
    rt().app_keys.pop(bytes(row["key_hash"]))
    return done()


# ── canaux ─────────────────────────────────────────────────────────────────────

def _url(value, https_only: bool) -> str:
    if not isinstance(value, str) or len(value) > 2000:
        raise ValueError("config.url est obligatoire")
    parts = urlsplit(value)
    schemes = ("https",) if https_only else ("http", "https")
    if parts.scheme not in schemes or not parts.hostname:
        raise ValueError("config.url doit être une URL " + ("https://" if https_only else "http(s)://"))
    return value


def _match(value, regex, what: str) -> str:
    if isinstance(value, int) and not isinstance(value, bool):
        value = str(value)
    if not isinstance(value, str) or not regex.match(value.strip()):
        raise ValueError(what)
    return value.strip()


def validate_channel(kind: str, config) -> dict:
    if kind not in CHANNEL_KINDS:
        raise ValueError(f"kind attendu : {'|'.join(CHANNEL_KINDS)}")
    if not isinstance(config, dict):
        raise ValueError("config doit être un objet")
    if kind == "email":
        to = config.get("to")
        if isinstance(to, str):
            to = [to]
        if not isinstance(to, list) or not to or len(to) > 20 or not all(
                isinstance(a, str) and _EMAIL_RE.match(a) for a in to):
            raise ValueError("config.to : liste d'adresses e-mail (20 au plus)")
        return {"to": to}
    if kind == "webhook":
        headers = config.get("headers", {}) or {}
        if not isinstance(headers, dict) or not all(isinstance(k, str) and isinstance(v, str) for k, v in headers.items()):
            raise ValueError("config.headers doit être un objet {nom: valeur texte}")
        return {"url": _url(config.get("url"), False), "headers": headers}
    if kind == "telegram":
        return {"bot_token": _match(config.get("bot_token"), _TELEGRAM_TOKEN_RE,
                                    "config.bot_token : jeton du robot Telegram (123456:ABC…)"),
                "chat_id": _match(config.get("chat_id"), _TELEGRAM_CHAT_RE,
                                  "config.chat_id : identifiant numérique ou @canal")}
    if kind == "ntfy":
        out = {"url": _url(config.get("url") or "https://ntfy.sh", False),
               "topic": _match(config.get("topic"), _NTFY_TOPIC_RE, "config.topic : 1 à 64 caractères A-Z a-z 0-9 _ -")}
        token = config.get("token")
        if token:
            if not isinstance(token, str) or len(token) > 200:
                raise ValueError("config.token : jeton d'accès ntfy (texte)")
            out["token"] = token
        return out
    if kind == "pagerduty":
        return {"routing_key": _match(config.get("routing_key"), _PAGERDUTY_KEY_RE,
                                      "config.routing_key : clé d'intégration Events API v2 (32 caractères)")}
    if kind == "push":
        # Destinataires : tout l'espace, ou des membres nommés (ids) ; leurs appareils abonnés reçoivent.
        if config.get("all") is True:
            return {"all": True}
        users = config.get("users")
        if not isinstance(users, list) or not users or len(users) > PUSH_MAX_USERS or not all(
                isinstance(u, int) and not isinstance(u, bool) and u > 0 for u in users):
            raise ValueError(f"config.users : liste d'identifiants de membres (1 à {PUSH_MAX_USERS}), ou config.all = true")
        return {"users": sorted(set(users))}
    if kind == "opsgenie":
        region = config.get("region", "eu")
        if region not in ("eu", "us"):
            raise ValueError("config.region attendu : eu|us")
        return {"api_key": _match(config.get("api_key"), _OPSGENIE_KEY_RE, "config.api_key : clé d'API Opsgenie"),
                "region": region}
    if kind == "glpi":
        # API REST de GLPI : adresse (…/apirest.php), jeton de l'application et jeton d'API d'un utilisateur ;
        # entité, catégorie, source de la demande et type (1 incident, 2 demande) facultatifs.
        out = {"url": _url(config.get("url"), False),
               "app_token": _match(config.get("app_token"), _GLPI_TOKEN_RE, "config.app_token : jeton du client d'API de GLPI"),
               "user_token": _match(config.get("user_token"), _GLPI_TOKEN_RE,
                                    "config.user_token : jeton d'API de l'utilisateur GLPI (Préférences › Clés d'accès distant)")}
        for key in ("entities_id", "itilcategories_id", "requesttypes_id"):
            v = config.get(key)
            if v in (None, ""):
                continue
            if isinstance(v, str) and v.strip().isdigit():
                v = int(v.strip())
            if isinstance(v, bool) or not isinstance(v, int) or not 0 <= v < 10 ** 9:
                raise ValueError(f"config.{key} : identifiant numérique GLPI")
            out[key] = v
        t = config.get("ticket_type", 1)
        if isinstance(t, str) and t.strip().isdigit():
            t = int(t.strip())
        if t not in (1, 2):
            raise ValueError("config.ticket_type attendu : 1 (incident) ou 2 (demande)")
        out["ticket_type"] = t
        return out
    # slack, teams, discord, googlechat, mattermost : un webhook entrant en https.
    return {"url": _url(config.get("url"), True)}


def _redact(row: dict, p: Principal) -> dict:
    """Une URL de webhook, un jeton ou une clé valent un secret : seuls les administrateurs les voient."""
    if p.at_least("admin"):
        return row
    cfg = dict(row.get("config") or {})
    if "url" in cfg:
        parts = urlsplit(cfg["url"])
        cfg["url"] = f"{parts.scheme}://{parts.hostname}/…"
    for key in SECRET_FIELDS:
        cfg.pop(key, None)
    return {**row, "config": cfg}


class ChannelIn(BaseModel):
    kind: str
    name: str = Field(min_length=1, max_length=200)
    config: dict = Field(default_factory=dict)
    enabled: bool = True


@router.get("/channels")
async def list_channels(p: Principal = Depends(viewer)):
    async with rt().db.reader() as conn:
        rows = await fetch_all(conn, f"SELECT {CHANNEL_FIELDS} FROM notification_channels WHERE org_id = %s "
                                     "ORDER BY name, id", (p.org_id,))
    return ok([_redact(r, p) for r in rows])


async def _check_push_members(conn, org_id: int, kind: str, config: dict) -> None:
    """Un canal push ne nomme que des membres de l'espace."""
    if kind != "push" or not config.get("users"):
        return
    rows = await fetch_all(conn, "SELECT user_id FROM memberships WHERE org_id = %s AND user_id = ANY(%s::bigint[])",
                           (org_id, config["users"]))
    missing = sorted(set(config["users"]) - {r["user_id"] for r in rows})
    if missing:
        raise bad_request(f"config.users : pas membres de l'espace : {', '.join(map(str, missing))}", "invalid_channel")


@router.post("/channels")
async def create_channel(body: ChannelIn, request: Request, p: Principal = Depends(admin)):
    try:
        config = validate_channel(body.kind, body.config)
    except ValueError as e:
        raise bad_request(str(e), "invalid_channel")
    async with rt().db.writer() as conn:
        await _check_push_members(conn, p.org_id, body.kind, config)
        row = await fetch_one(conn, f"""
            INSERT INTO notification_channels (org_id, kind, name, config, enabled)
            VALUES (%s, %s, %s, %s::jsonb, %s) RETURNING {CHANNEL_FIELDS}
        """, (p.org_id, body.kind, body.name.strip(), jdump(config), body.enabled))
        await audit.record(conn, org_id=p.org_id, principal=p, action="create", object_type="channel",
                           object_id=row["id"], summary=f"canal {body.kind} « {row['name']} » créé",
                           after={"kind": body.kind, "name": row["name"], "config": config, "enabled": body.enabled},
                           request=request)
    return ok(row, 201)


async def _update_channel(channel_id: int, data: dict, p: Principal, request: Request):
    async with rt().db.writer() as conn:
        cur = await fetch_one(conn, f"SELECT {CHANNEL_FIELDS} FROM notification_channels WHERE org_id = %s AND id = %s",
                              (p.org_id, channel_id))
        if cur is None:
            raise not_found("canal introuvable")
        kind = data.get("kind", cur["kind"])
        name = data.get("name", cur["name"])
        enabled = data.get("enabled", cur["enabled"])
        if not isinstance(name, str) or not name.strip() or not isinstance(enabled, bool):
            raise bad_request("name (texte) et enabled (booléen) attendus", "invalid_channel")
        try:
            config = validate_channel(kind, data.get("config", cur["config"]))
        except ValueError as e:
            raise bad_request(str(e), "invalid_channel")
        await _check_push_members(conn, p.org_id, kind, config)
        row = await fetch_one(conn, f"""
            UPDATE notification_channels SET kind = %s, name = %s, config = %s::jsonb, enabled = %s
             WHERE org_id = %s AND id = %s RETURNING {CHANNEL_FIELDS}
        """, (kind, name.strip(), jdump(config), enabled, p.org_id, channel_id))
        await audit.record(conn, org_id=p.org_id, principal=p, action="update", object_type="channel",
                           object_id=channel_id, summary=f"canal {kind} « {name.strip()} » modifié",
                           before={k: cur[k] for k in ("kind", "name", "config", "enabled")},
                           after={"kind": kind, "name": name.strip(), "config": config, "enabled": enabled},
                           request=request)
    return ok(row)


async def _delete_channel(channel_id: int, p: Principal, request: Request):
    async with rt().db.writer() as conn:
        row = await fetch_one(conn, "DELETE FROM notification_channels WHERE org_id = %s AND id = %s "
                                    "RETURNING id, kind, name", (p.org_id, channel_id))
        if row is None:
            raise not_found("canal introuvable")
        # Un moniteur ne doit pas garder l'id d'un canal disparu.
        await conn.execute("UPDATE monitors SET channel_ids = array_remove(channel_ids, %s::bigint) "
                           "WHERE org_id = %s AND %s::bigint = ANY(channel_ids)", (channel_id, p.org_id, channel_id))
        await audit.record(conn, org_id=p.org_id, principal=p, action="delete", object_type="channel",
                           object_id=channel_id, summary=f"canal {row['kind']} « {row['name']} » supprimé",
                           before=row, request=request)
    return done()


@router.put("/channels/{channel_id}")
async def update_channel(channel_id: int, request: Request, data: dict = Body(...), p: Principal = Depends(admin)):
    return await _update_channel(channel_id, data, p, request)


@router.put("/channels")
async def update_channel_body(request: Request, data: dict = Body(...), p: Principal = Depends(admin)):
    if not isinstance(data.get("id"), int):
        raise bad_request("id du canal attendu dans le corps (ou PUT /channels/{id})")
    return await _update_channel(data["id"], data, p, request)


@router.delete("/channels/{channel_id}")
async def delete_channel(channel_id: int, request: Request, p: Principal = Depends(admin)):
    return await _delete_channel(channel_id, p, request)


@router.delete("/channels")
async def delete_channel_query(id: int, request: Request, p: Principal = Depends(admin)):
    return await _delete_channel(id, p, request)


@router.post("/channels/{channel_id}/test")
async def test_channel(channel_id: int, request: Request, p: Principal = Depends(admin)):
    """Envoie un message d'essai, un seul essai (pas de reprise) : rend {ok, detail}."""
    runtime = rt()
    settings = runtime.settings
    async with runtime.db.reader() as conn:
        channel = await fetch_one(conn, "SELECT id, kind, name, config FROM notification_channels "
                                        "WHERE org_id = %s AND id = %s", (p.org_id, channel_id))
        if channel is None:
            raise not_found("canal introuvable")
        org = await fetch_one(conn, "SELECT settings FROM orgs WHERE id = %s", (p.org_id,))
    allow = settings.allow_private_targets or bool(((org or {}).get("settings") or {}).get("allow_private_targets"))
    who = p.email or (f"clé:{p.app_key_id}" if p.app_key_id else "")
    notice = notify.Notice(channel, {"id": 0, "name": "Essai de canal", "type": "test"}, "*", "OK", None, None,
                           f"Message d'essai envoyé depuis HYPHA par {who}. Si vous le lisez, le canal "
                           f"« {channel['name']} » fonctionne.", settings.public_url, allow_private=allow, kind="test",
                           dedup_key=f"test:{channel_id}")
    try:
        async with notify.make_client() as client:
            detail = await notify.send(settings, client, notice)
        result = {"ok": True, "detail": detail or "message d'essai envoyé"}
    except TargetRefused as e:
        result = {"ok": False, "detail": f"adresse refusée : {e}"}
    except push.PushRefused as e:
        result = {"ok": False, "detail": str(e)}
    except Exception as e:
        result = {"ok": False, "detail": str(e) or type(e).__name__}
    async with runtime.db.writer() as conn:
        await audit.record(conn, org_id=p.org_id, principal=p, action="test", object_type="channel",
                           object_id=channel_id, summary=f"essai du canal {channel['kind']} « {channel['name']} » : "
                           + ("réussi" if result["ok"] else "échec"), after=result, request=request)
    return ok(result)


# ── membres ────────────────────────────────────────────────────────────────────

MEMBER_SQL = """
    SELECT u.id AS user_id, u.email, u.name, m.role, m.created_at, u.last_login_at,
           (u.sub LIKE 'invite:%%') AS pending,
           CASE WHEN u.sub LIKE 'local:%%' THEN substr(u.sub, 7) END AS login
      FROM memberships m JOIN users u ON u.id = m.user_id
     WHERE m.org_id = %s
"""


@router.get("/org/members")
async def list_members(p: Principal = Depends(viewer)):
    async with rt().db.reader() as conn:
        return ok(await fetch_all(conn, MEMBER_SQL + " ORDER BY u.email NULLS LAST, u.id", (p.org_id,)))


class MemberIn(BaseModel):
    email: str = Field(min_length=3, max_length=320)
    role: str = Field("member", pattern="^(owner|admin|member|viewer)$")
    login: str | None = Field(None, min_length=1, max_length=100, pattern=r"^[a-z0-9._\-]+$")
    password: str | None = Field(None, min_length=8, max_length=500)
    name: str | None = Field(None, max_length=200)


@router.post("/org/members")
async def add_member(body: MemberIn, request: Request, p: Principal = Depends(admin)):
    if body.role == "owner" and p.role != "owner":
        raise forbidden("seul un owner nomme un autre owner")
    email = body.email.strip().lower()
    if not _EMAIL_RE.match(email):
        raise bad_request("adresse e-mail invalide")
    runtime = rt()
    async with runtime.db.writer() as conn:
        if body.login and body.password:
            if runtime.settings.saas:
                raise bad_request("les comptes locaux n'existent qu'en auto-hébergé")
            pw_hash = await asyncio.to_thread(hash_password, body.password)
            user_id = await accounts.upsert_local_user(conn, body.login, pw_hash, email, body.name)
        else:
            row = await fetch_one(conn, "SELECT id FROM users WHERE lower(email) = %s "
                                        "ORDER BY last_login_at DESC NULLS LAST LIMIT 1", (email,))
            if row is None:
                # Compte réservé : la première connexion SSO avec cet e-mail le reprend (oidc_login).
                row = await fetch_one(conn, "INSERT INTO users (sub, email, name) VALUES (%s, %s, %s) "
                                            "ON CONFLICT (sub) DO UPDATE SET email = EXCLUDED.email RETURNING id",
                                      ("invite:" + email, email, body.name))
            user_id = row["id"]
        before = await fetch_one(conn, "SELECT role FROM memberships WHERE org_id = %s AND user_id = %s",
                                 (p.org_id, user_id))
        await accounts.add_membership(conn, p.org_id, user_id, body.role)
        member = await fetch_one(conn, MEMBER_SQL + " AND u.id = %s", (p.org_id, user_id))
        await audit.record(conn, org_id=p.org_id, principal=p, action="update" if before else "create",
                           object_type="member", object_id=user_id,
                           summary=f"membre {email} : rôle {body.role}" + (f" (avant {before['role']})" if before else ""),
                           before=before, after={"email": email, "role": body.role, "login": body.login},
                           request=request)
    return ok(member, 201)


@router.delete("/org/members/{user_id}")
async def remove_member(user_id: int, request: Request, p: Principal = Depends(admin)):
    async with rt().db.writer() as conn:
        target = await fetch_one(conn, "SELECT m.role, u.email FROM memberships m JOIN users u ON u.id = m.user_id "
                                       "WHERE m.org_id = %s AND m.user_id = %s", (p.org_id, user_id))
        if target is None:
            raise not_found("membre introuvable")
        if target["role"] == "owner":
            if p.role != "owner":
                raise forbidden("seul un owner retire un owner")
            owners = await fetch_one(conn, "SELECT count(*) AS n FROM memberships WHERE org_id = %s AND role = 'owner'",
                                     (p.org_id,))
            if owners["n"] <= 1:
                raise bad_request("l'organisation garderait zéro owner")
        await conn.execute("DELETE FROM memberships WHERE org_id = %s AND user_id = %s", (p.org_id, user_id))
        await audit.record(conn, org_id=p.org_id, principal=p, action="delete", object_type="member",
                           object_id=user_id, summary=f"membre {target['email'] or user_id} retiré",
                           before=target, request=request)
    return done()


# ── installation de l'agent ────────────────────────────────────────────────────

@router.get("/install")
async def install(p: Principal = Depends(viewer)):
    s = rt().settings
    url = s.public_url
    # La version publiée (version.txt), comme la mise à jour de la flotte : la valeur de la
    # configuration retarde dès qu'un agent est publié sans redéployer le serveur.
    version = fleet.published_version(s.install_dir) or s.agent_version
    custom = s.public_url != "https://hypha.s33d.fr"
    linux_env = f"HYPHA_API_KEY={KEY_PLACEHOLDER}" + (f" HYPHA_URL={url}" if custom else "")
    win_env = f"$env:HYPHA_API_KEY='{KEY_PLACEHOLDER}'; " + (f"$env:HYPHA_URL='{url}'; " if custom else "")
    # SYS_PTRACE et apparmor=unconfined : sans eux, l'agent en conteneur ne peut pas lire les
    # descripteurs des processus de l'hôte et ne rattache aucune connexion à son processus.
    docker = (f"docker run -d --name hypha-agent --restart unless-stopped --network host --pid host "
              f"--cap-add SYS_PTRACE --security-opt apparmor=unconfined "
              f"-v /proc:/host/proc:ro -v /sys:/host/sys:ro -v /:/host/root:ro "
              f"-v /var/run/docker.sock:/var/run/docker.sock:ro "
              f"-v hypha-agent:/var/lib/hypha -e HYPHA_API_KEY={KEY_PLACEHOLDER} -e HYPHA_URL={url} "
              f"{s.agent_image}:{version}")
    return ok({
        "url": url,
        "linux": f"curl -fsSL {url}/install/install.sh | sudo {linux_env} bash",
        # PowerShell 5.1 (Windows Server 2016/2019) ne propose pas TLS 1.2 par défaut : sans ce
        # préfixe, le premier téléchargement échoue avant même que install.ps1 ne s'exécute.
        "windows": (f"[Net.ServicePointManager]::SecurityProtocol = "
                    f"[Net.ServicePointManager]::SecurityProtocol -bor 3072; "
                    f"{win_env}iwr -useb {url}/install/install.ps1 | iex"),
        "docker": docker,
        "version": version,
        "key_placeholder": KEY_PLACEHOLDER,
    })
