"""Qui appelle l'API de l'interface : session (cookie `hypha_session`) ou clé `app` (`X-Hypha-App-Key`).

Rôles : `viewer` lit ; `member` écrit tableaux, moniteurs, synthétiques, événements ; `admin`
gère clés, membres et canaux ; `owner` a tout, et lui seul nomme un autre owner.
"""
import time
from dataclasses import dataclass

from fastapi import Request

from ..db import fetch_one
from ..errors import ApiError, forbidden, unauthenticated
from ..runtime import rt
from ..util import KEY_RE, key_hash
from .accounts import COOKIE_NAME, first_membership

ROLE_RANK = {"viewer": 1, "member": 2, "admin": 3, "owner": 4}
APP_KEY_DEFAULT_ROLE = "member"     # clé `app` créée en ligne de commande, sans auteur


@dataclass
class Principal:
    user_id: int | None
    email: str | None
    name: str | None
    org_id: int | None
    role: str | None
    session_id: str | None = None
    app_key_id: int | None = None

    def at_least(self, role: str) -> bool:
        return ROLE_RANK.get(self.role or "", 0) >= ROLE_RANK[role]


async def _from_app_key(key: str) -> Principal:
    runtime = rt()
    if not KEY_RE.match(key):
        raise unauthenticated("clé d'application inconnue")
    digest = key_hash(key)
    row = runtime.app_keys.get(digest, False)
    if row is False:
        async with runtime.db.writer() as conn:
            row = await fetch_one(conn, """
                SELECT k.id, k.org_id, k.kind, k.revoked_at IS NOT NULL AS revoked, k.created_by,
                       m.role, u.email, u.name
                  FROM api_keys k
                  LEFT JOIN memberships m ON m.org_id = k.org_id AND m.user_id = k.created_by
                  LEFT JOIN users u ON u.id = k.created_by
                 WHERE k.key_hash = %s
            """, (digest,))
            if row and row["kind"] == "app" and not row["revoked"]:
                last = runtime.key_touched.get(row["id"], 0.0)
                if time.monotonic() - last >= 60:
                    runtime.key_touched[row["id"]] = time.monotonic()
                    await conn.execute("UPDATE api_keys SET last_used_at = now() WHERE id = %s", (row["id"],))
        runtime.app_keys.set(digest, row)
    if not row:
        raise unauthenticated("clé d'application inconnue")
    if row["revoked"]:
        raise forbidden("clé d'application révoquée", "revoked_api_key")
    if row["kind"] != "app":
        raise forbidden("une clé d'ingestion n'ouvre pas l'API de l'interface", "wrong_key_kind")
    role = row["role"] or (APP_KEY_DEFAULT_ROLE if row["created_by"] is None else "viewer")
    return Principal(row["created_by"], row["email"], row["name"], row["org_id"], role, app_key_id=row["id"])


async def _from_session(sid: str) -> Principal | None:
    runtime = rt()
    cached = runtime.sessions.get(sid)
    if cached is not None:
        return cached or None
    # Les sessions se lisent sur le primaire : une réplique en retard ignorerait une connexion fraîche.
    async with runtime.db.writer() as conn:
        row = await fetch_one(conn, """
            SELECT s.id, s.user_id, s.org_id, u.email, u.name, m.role
              FROM sessions s JOIN users u ON u.id = s.user_id
              LEFT JOIN memberships m ON m.org_id = s.org_id AND m.user_id = s.user_id
             WHERE s.id = %s AND s.expires_at > now()
        """, (sid,))
        if row and row["role"] is None:
            # Organisation courante quittée ou supprimée : on bascule sur une autre adhésion.
            member = await first_membership(conn, row["user_id"])
            row["org_id"] = member["org_id"] if member else None
            row["role"] = member["role"] if member else None
            await conn.execute("UPDATE sessions SET org_id = %s WHERE id = %s", (row["org_id"], sid))
    principal = (Principal(row["user_id"], row["email"], row["name"], row["org_id"], row["role"], session_id=sid)
                 if row else None)
    runtime.sessions.set(sid, principal or False)
    return principal


async def authenticate(request: Request) -> Principal:
    key = request.headers.get("x-hypha-app-key")
    if key:
        return await _from_app_key(key.strip())
    sid = request.cookies.get(COOKIE_NAME)
    if sid and len(sid) <= 100:
        principal = await _from_session(sid)
        if principal:
            return principal
    raise unauthenticated(mode=rt().settings.mode)


async def _need(request: Request, role: str) -> Principal:
    p = await authenticate(request)
    if p.org_id is None:
        raise ApiError(403, "no_org", "aucune organisation pour ce compte")
    if not p.at_least(role):
        raise forbidden(f"rôle « {role} » requis")
    return p


async def any_user(request: Request) -> Principal:
    return await authenticate(request)


async def viewer(request: Request) -> Principal:
    return await _need(request, "viewer")


async def member(request: Request) -> Principal:
    return await _need(request, "member")


async def admin(request: Request) -> Principal:
    return await _need(request, "admin")


async def owner(request: Request) -> Principal:
    return await _need(request, "owner")
