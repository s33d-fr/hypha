"""Routes de session (contrat § 7, « Session »)."""
import asyncio
import logging
import secrets

from fastapi import APIRouter, Depends, Request
from fastapi.responses import JSONResponse, RedirectResponse
from pydantic import BaseModel, Field

from .. import audit
from ..db import fetch_one
from ..errors import ApiError
from ..runtime import rt
from . import accounts, signing
from .accounts import COOKIE_NAME, SESSION_DAYS
from .oidc import OidcError, pkce_pair, safe_next
from .passwords import verify_password
from .principal import Principal, any_user

log = logging.getLogger("hypha.auth")
router = APIRouter()

STATE_COOKIE = "hypha_oidc"
STATE_MAX_AGE = 600


def _set_session_cookie(resp, sid: str) -> None:
    resp.set_cookie(COOKIE_NAME, sid, max_age=SESSION_DAYS * 86400, httponly=True,
                    secure=rt().settings.cookie_secure, samesite="lax", path="/")


def _oidc():
    runtime = rt()
    if runtime.oidc is None:
        raise ApiError(404, "oidc_disabled", "la connexion OIDC n'est pas configurée")
    if not runtime.settings.secret:
        raise ApiError(500, "missing_secret", "HYPHA_SECRET absent : l'état OIDC ne peut être signé")
    return runtime.oidc


@router.get("/api/auth/connexion")
async def connexion(next: str = "/"):
    oidc = _oidc()
    state, nonce = secrets.token_urlsafe(24), secrets.token_urlsafe(24)
    verifier, challenge = pkce_pair()
    try:
        url = await oidc.authorization_url(state, challenge, nonce)
    except Exception as e:
        log.warning("découverte OIDC impossible : %s", e)
        raise ApiError(502, "oidc_unavailable", "le fournisseur d'identité ne répond pas")
    token = signing.sign({"s": state, "v": verifier, "n": nonce, "r": safe_next(next)}, rt().settings.secret)
    resp = RedirectResponse(url, status_code=302)
    resp.set_cookie(STATE_COOKIE, token, max_age=STATE_MAX_AGE, httponly=True,
                    secure=rt().settings.cookie_secure, samesite="lax", path="/api/auth")
    return resp


def _fail(reason: str) -> RedirectResponse:
    resp = RedirectResponse(f"/?connexion={reason}", status_code=302)
    resp.delete_cookie(STATE_COOKIE, path="/api/auth")
    return resp


@router.get("/api/auth/rappel")
async def rappel(request: Request, code: str = "", state: str = "", error: str = ""):
    if error:
        return _fail("refusee")
    oidc = _oidc()
    runtime = rt()
    payload = signing.unsign(request.cookies.get(STATE_COOKIE), runtime.settings.secret, STATE_MAX_AGE)
    if not payload or not code or not secrets.compare_digest(str(payload.get("s", "")), state):
        return _fail("expiree")
    try:
        tokens = await oidc.exchange(code, payload["v"])
        claims = await oidc.verify_id_token(tokens.get("id_token", ""), payload["n"])
        if not claims.get("email") and tokens.get("access_token"):
            info = await oidc.userinfo(tokens["access_token"])
            if info.get("sub") == claims["sub"]:
                claims = {**info, **claims}
    except (OidcError, KeyError) as e:
        log.warning("connexion OIDC refusée : %s", e)
        return _fail("echec")
    except Exception as e:
        log.warning("connexion OIDC en échec : %s", e)
        return _fail("echec")
    async with runtime.db.writer() as conn:
        try:
            user_id, org_id = await accounts.oidc_login(conn, claims, runtime.settings.saas)
        except accounts.LoginRefused:
            return _fail("interdite")
        ip = request.client.host if request.client else None
        sid = await accounts.create_session(conn, user_id, org_id, ip, request.headers.get("user-agent"))
        email = (claims.get("email") or "").strip().lower() or None
        await audit.record(conn, org_id=org_id, principal=Principal(user_id, email, None, org_id, None, session_id=sid),
                           action="login", object_type="session", object_id=user_id,
                           summary=f"connexion SSO de {email or user_id}", request=request)
    resp = RedirectResponse(safe_next(payload.get("r")), status_code=302)
    resp.delete_cookie(STATE_COOKIE, path="/api/auth")
    _set_session_cookie(resp, sid)
    return resp


class LocalLogin(BaseModel):
    login: str = Field(min_length=1, max_length=100)
    password: str = Field(min_length=1, max_length=500)


@router.post("/api/auth/local")
async def local_login(body: LocalLogin, request: Request):
    runtime = rt()
    if runtime.settings.saas:
        raise ApiError(403, "local_auth_disabled", "les comptes locaux n'existent qu'en auto-hébergé")
    login = body.login.strip().lower()
    ip = request.client.host if request.client else None
    if not runtime.login_limiter.allow((ip, login)):
        raise ApiError(429, "rate_limited", "trop d'essais ; réessayez dans une minute")
    async with runtime.db.writer() as conn:
        user = await fetch_one(conn, "SELECT id, email, password_hash FROM users WHERE sub = %s", ("local:" + login,))
    stored = user["password_hash"] if user else None
    ok = await asyncio.to_thread(verify_password, body.password, stored)
    if not ok:
        raise ApiError(401, "invalid_credentials", "identifiant ou mot de passe incorrect")
    async with runtime.db.writer() as conn:
        member = await accounts.first_membership(conn, user["id"])
        if member is None:
            raise ApiError(403, "no_org", "ce compte n'appartient à aucune organisation")
        sid = await accounts.create_session(conn, user["id"], member["org_id"], ip, request.headers.get("user-agent"))
        await audit.record(conn, org_id=member["org_id"],
                           principal=Principal(user["id"], user.get("email") or f"local:{login}", None,
                                               member["org_id"], member["role"], session_id=sid),
                           action="login", object_type="session", object_id=user["id"],
                           summary=f"connexion locale de {login}", request=request)
    resp = JSONResponse({"status": "ok"})
    _set_session_cookie(resp, sid)
    return resp


@router.post("/api/auth/deconnexion")
async def deconnexion(request: Request):
    runtime = rt()
    sid = request.cookies.get(COOKIE_NAME)
    if sid and len(sid) <= 100:
        async with runtime.db.writer() as conn:
            gone = await fetch_one(conn, """
                DELETE FROM sessions s USING users u WHERE s.id = %s AND u.id = s.user_id
                RETURNING s.user_id, s.org_id, u.email
            """, (sid,))
            if gone:
                await audit.record(conn, org_id=gone["org_id"],
                                   principal=Principal(gone["user_id"], gone["email"], None, gone["org_id"], None,
                                                       session_id=sid),
                                   action="logout", object_type="session", object_id=gone["user_id"],
                                   summary=f"déconnexion de {gone['email'] or gone['user_id']}", request=request)
        runtime.sessions.pop(sid)
    logout_url = "/"
    if runtime.oidc is not None and runtime.settings.saas:
        logout_url = await runtime.oidc.end_session_url(runtime.settings.public_url + "/") or "/"
    resp = JSONResponse({"status": "ok", "logout_url": logout_url})
    resp.delete_cookie(COOKIE_NAME, path="/")
    return resp


async def me_payload(conn, p: Principal) -> dict:
    org = None
    if p.org_id is not None:
        org = await fetch_one(conn, "SELECT id, slug, name FROM orgs WHERE id = %s", (p.org_id,))
    if p.user_id is not None and p.app_key_id is None:
        orgs = await accounts.user_orgs(conn, p.user_id)
    else:
        orgs = [dict(org, role=p.role)] if org else []
    user = {"id": p.user_id, "email": p.email, "name": p.name} if p.user_id is not None else None
    s = rt().settings
    plan = None
    if p.org_id is not None and s.saas:
        from ..plans import get_plan
        plan = (await get_plan(p.org_id, conn)).code
    return {"user": user, "org": org, "orgs": orgs, "role": p.role, "mode": s.mode, "plan": plan,
            "features": {"explain": s.explain_enabled, "explain_timeout_s": s.llm_timeout_s,
                         "billing": s.billing_enabled, "billing_open": s.billing_open}}


@router.get("/api/v1/me")
async def me(p: Principal = Depends(any_user)):
    async with rt().db.writer() as conn:
        return JSONResponse(await me_payload(conn, p))


class SwitchOrg(BaseModel):
    org_id: int


@router.post("/api/v1/orgs/switch")
async def switch_org(body: SwitchOrg, request: Request, p: Principal = Depends(any_user)):
    if p.session_id is None:
        raise ApiError(400, "not_a_session", "une clé d'application est liée à une seule organisation")
    runtime = rt()
    async with runtime.db.writer() as conn:
        row = await fetch_one(conn, "SELECT role FROM memberships WHERE org_id = %s AND user_id = %s",
                              (body.org_id, p.user_id))
        if row is None:
            raise ApiError(403, "not_a_member", "vous n'êtes pas membre de cette organisation")
        await conn.execute("UPDATE sessions SET org_id = %s WHERE id = %s", (body.org_id, p.session_id))
        await audit.record(conn, org_id=body.org_id, principal=p, action="switch_org", object_type="session",
                           summary=f"passage dans l'organisation n° {body.org_id}", request=request)
        runtime.sessions.pop(p.session_id)
        p = Principal(p.user_id, p.email, p.name, body.org_id, row["role"], session_id=p.session_id)
        return JSONResponse(await me_payload(conn, p))
