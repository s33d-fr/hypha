"""Organisations, utilisateurs, sessions et clés : opérations partagées par l'API et la ligne de commande."""
import logging
import secrets

from ..db import fetch_all, fetch_one
from ..util import generate_api_key, key_hash, key_prefix
from .naming import org_name_from_email, slugify

log = logging.getLogger("hypha.auth")

SESSION_DAYS = 7
COOKIE_NAME = "hypha_session"
ROLES = ("owner", "admin", "member", "viewer")

async def create_org(conn, name: str, slug: str | None = None) -> dict:
    """Crée une organisation ; le slug reçoit un suffixe -2, -3… s'il est déjà pris."""
    base = slugify(slug or name)
    for i in range(1, 1000):
        candidate = base if i == 1 else f"{base}-{i}"
        row = await fetch_one(conn, "INSERT INTO orgs (slug, name) VALUES (%s, %s) "
                                    "ON CONFLICT (slug) DO NOTHING RETURNING id, slug, name",
                              (candidate, name[:200]))
        if row:
            return row
    raise RuntimeError("aucun slug libre pour l'organisation")


async def create_api_key(conn, org_id: int, name: str, kind: str = "ingest",
                         created_by: int | None = None) -> tuple[dict, str]:
    """Rend (ligne, clé entière). La clé entière n'est jamais stockée : seule son empreinte l'est."""
    key = generate_api_key()
    row = await fetch_one(conn, """
        INSERT INTO api_keys (org_id, name, kind, prefix, key_hash, created_by)
        VALUES (%s, %s, %s, %s, %s, %s)
        RETURNING id, name, kind, prefix, created_at, last_used_at, revoked_at, created_by
    """, (org_id, name[:200], kind, key_prefix(key), key_hash(key), created_by))
    return row, key


async def add_membership(conn, org_id: int, user_id: int, role: str) -> None:
    await conn.execute("INSERT INTO memberships (org_id, user_id, role) VALUES (%s, %s, %s) "
                       "ON CONFLICT (org_id, user_id) DO UPDATE SET role = EXCLUDED.role",
                       (org_id, user_id, role))


async def first_membership(conn, user_id: int) -> dict | None:
    return await fetch_one(conn, "SELECT org_id, role FROM memberships WHERE user_id = %s "
                                 "ORDER BY created_at, org_id LIMIT 1", (user_id,))


async def user_orgs(conn, user_id: int) -> list[dict]:
    return await fetch_all(conn, """
        SELECT o.id, o.slug, o.name, m.role FROM memberships m JOIN orgs o ON o.id = m.org_id
         WHERE m.user_id = %s ORDER BY o.name, o.id
    """, (user_id,))


async def create_session(conn, user_id: int, org_id: int | None, ip: str | None, user_agent: str | None) -> str:
    sid = secrets.token_urlsafe(32)          # 43 caractères
    await conn.execute("""
        INSERT INTO sessions (id, user_id, org_id, expires_at, ip, user_agent)
        VALUES (%s, %s, %s, now() + make_interval(days => %s), %s, %s)
    """, (sid, user_id, org_id, SESSION_DAYS, ip, (user_agent or "")[:500]))
    await conn.execute("UPDATE users SET last_login_at = now() WHERE id = %s", (user_id,))
    return sid


async def upsert_local_user(conn, login: str, password_hash: str, email: str | None = None,
                            name: str | None = None) -> int:
    row = await fetch_one(conn, """
        INSERT INTO users (sub, email, name, password_hash) VALUES (%s, %s, %s, %s)
        ON CONFLICT (sub) DO UPDATE SET password_hash = EXCLUDED.password_hash,
               email = COALESCE(EXCLUDED.email, users.email), name = COALESCE(EXCLUDED.name, users.name)
        RETURNING id
    """, ("local:" + login, email, name or login, password_hash))
    return row["id"]


class LoginRefused(Exception):
    pass


async def oidc_login(conn, claims: dict, saas: bool) -> tuple[int, int]:
    """Retrouve ou crée l'utilisateur d'un id_token ; rend (user_id, org_id).

    SaaS : un utilisateur sans organisation en reçoit une (rôle owner, clé « par défaut »).
    Auto-hébergé avec OIDC : seul un utilisateur invité (adhésion existante) entre.
    """
    sub = str(claims["sub"])
    email = (claims.get("email") or "").strip().lower() or None
    name = claims.get("name") or claims.get("preferred_username") or email
    row = await fetch_one(conn, "SELECT id FROM users WHERE sub = %s", (sub,))
    if row is None and email and claims.get("email_verified", True) is not False:
        # Invitation par e-mail (POST /org/members) : le compte réservé prend le vrai sujet OIDC.
        row = await fetch_one(conn, "UPDATE users SET sub = %s WHERE sub = %s RETURNING id",
                              (sub, "invite:" + email))
    if row is None:
        row = await fetch_one(conn, """
            INSERT INTO users (sub, email, name) VALUES (%s, %s, %s)
            ON CONFLICT (sub) DO UPDATE SET email = EXCLUDED.email RETURNING id
        """, (sub, email, name))
    user_id = row["id"]
    await conn.execute("UPDATE users SET email = COALESCE(%s, email), name = COALESCE(%s, name), "
                       "last_login_at = now() WHERE id = %s", (email, name, user_id))
    # Deux onglets qui se connectent en même temps ne doivent pas créer deux organisations.
    await conn.execute("SELECT pg_advisory_xact_lock(7268003, (%s)::int)", (user_id % 2_000_000_000,))
    member = await first_membership(conn, user_id)
    if member:
        return user_id, member["org_id"]
    if not saas:
        raise LoginRefused("aucune invitation pour ce compte")
    org = await create_org(conn, org_name_from_email(email))
    await add_membership(conn, org["id"], user_id, "owner")
    # Pas de clé créée ici : son secret ne s'afficherait nulle part. La page d'installation en crée
    # une et l'insère dans la commande.
    from ..monitors.defaults import provision_org
    await provision_org(conn, org["id"], user_id, email)
    log.info("organisation %s créée à la première connexion de l'utilisateur %s", org["slug"], user_id)
    return user_id, org["id"]


async def bootstrap_selfhosted(conn, org_name: str, admin_login: str, admin_password_hash: str | None) -> None:
    """Premier démarrage de l'auto-hébergé : l'organisation unique et le compte d'administration."""
    await conn.execute("SELECT pg_advisory_xact_lock(7268004)")
    org = await fetch_one(conn, "SELECT id FROM orgs ORDER BY id LIMIT 1")
    if org is None:
        org = await create_org(conn, org_name)
        log.info("organisation « %s » créée au premier démarrage", org_name)
        from ..monitors.defaults import provision_org
        await provision_org(conn, org["id"], None, None)
    if admin_login and admin_password_hash:
        exists = await fetch_one(conn, "SELECT id FROM users WHERE sub = %s", ("local:" + admin_login,))
        if exists is None:
            user_id = await upsert_local_user(conn, admin_login, admin_password_hash)
            await add_membership(conn, org["id"], user_id, "owner")
            log.info("compte local %s créé au premier démarrage", admin_login)
