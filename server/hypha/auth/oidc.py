"""Client OIDC (code d'autorisation + PKCE S256), sur le modèle du SSO S33D-INCIDENTS.

Différences voulues avec ce modèle : appels asynchrones (httpx), état dans un cookie signé, et
l'identité lue dans l'`id_token` vérifié (signature JWKS, émetteur, audience, nonce) ; userinfo
ne sert qu'en repli si le jeton ne porte pas l'e-mail.
"""
import base64
import hashlib
import secrets
import time
import urllib.parse

import httpx
import jwt

ALLOWED_ALGS = ("RS256", "RS384", "RS512", "PS256", "PS384", "PS512", "ES256", "ES384", "ES512", "EdDSA")


class OidcError(Exception):
    pass


def pkce_pair() -> tuple[str, str]:
    verifier = secrets.token_urlsafe(48)
    challenge = base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).decode().rstrip("=")
    return verifier, challenge


def safe_next(path: str | None, default: str = "/") -> str:
    """N'accepte qu'un chemin local : jamais une redirection vers un autre site."""
    path = str(path or default)
    if not path.startswith("/") or path.startswith("//") or "\\" in path or len(path) > 500:
        return default
    return path


class OidcClient:
    def __init__(self, issuer: str, client_id: str, client_secret: str, redirect_uri: str):
        self.issuer = issuer.rstrip("/")
        self.client_id = client_id
        self.client_secret = client_secret
        self.redirect_uri = redirect_uri
        self._discovery: tuple[float, dict] | None = None
        self._jwks: tuple[float, dict] | None = None

    async def _get_json(self, url: str, **kw) -> dict:
        async with httpx.AsyncClient(timeout=15) as client:
            r = await client.get(url, **kw)
            r.raise_for_status()
            return r.json()

    async def discovery(self) -> dict:
        if not self._discovery or time.time() - self._discovery[0] > 3600:
            doc = await self._get_json(self.issuer + "/.well-known/openid-configuration")
            self._discovery = (time.time(), doc)
        return self._discovery[1]

    async def authorization_url(self, state: str, challenge: str, nonce: str) -> str:
        doc = await self.discovery()
        params = {
            "client_id": self.client_id, "response_type": "code", "scope": "openid profile email",
            "redirect_uri": self.redirect_uri, "state": state, "nonce": nonce,
            "code_challenge": challenge, "code_challenge_method": "S256",
        }
        return doc["authorization_endpoint"] + "?" + urllib.parse.urlencode(params)

    async def exchange(self, code: str, verifier: str) -> dict:
        doc = await self.discovery()
        basic = base64.b64encode(
            f"{urllib.parse.quote(self.client_id)}:{urllib.parse.quote(self.client_secret)}".encode()).decode()
        async with httpx.AsyncClient(timeout=15) as client:
            r = await client.post(doc["token_endpoint"], data={
                "grant_type": "authorization_code", "code": code, "redirect_uri": self.redirect_uri,
                "code_verifier": verifier,
            }, headers={"Authorization": "Basic " + basic})
        if r.status_code != 200:
            raise OidcError(f"échange du code refusé ({r.status_code})")
        return r.json()

    async def _key_for(self, kid: str | None, refresh: bool = False):
        doc = await self.discovery()
        if refresh or not self._jwks or time.time() - self._jwks[0] > 3600:
            self._jwks = (time.time(), await self._get_json(doc["jwks_uri"]))
        for jwk in self._jwks[1].get("keys", []):
            if kid is None or jwk.get("kid") == kid:
                return jwt.PyJWK(jwk)
        return None

    async def verify_id_token(self, id_token: str, nonce: str) -> dict:
        try:
            header = jwt.get_unverified_header(id_token)
        except jwt.PyJWTError as e:
            raise OidcError(f"id_token illisible : {e}")
        alg = header.get("alg")
        if alg not in ALLOWED_ALGS:
            raise OidcError(f"algorithme d'id_token refusé : {alg}")
        key = await self._key_for(header.get("kid")) or await self._key_for(header.get("kid"), refresh=True)
        if key is None:
            raise OidcError("clé de signature de l'id_token introuvable")
        try:
            claims = jwt.decode(id_token, key=key.key, algorithms=[alg], audience=self.client_id,
                                issuer=self.issuer, leeway=60, options={"require": ["exp", "iat", "sub"]})
        except jwt.PyJWTError as e:
            raise OidcError(f"id_token refusé : {e}")
        if claims.get("nonce") != nonce:
            raise OidcError("nonce de l'id_token incorrect")
        return claims

    async def userinfo(self, access_token: str) -> dict:
        doc = await self.discovery()
        if not doc.get("userinfo_endpoint"):
            return {}
        return await self._get_json(doc["userinfo_endpoint"], headers={"Authorization": "Bearer " + access_token})

    async def end_session_url(self, post_logout: str) -> str | None:
        try:
            doc = await self.discovery()
        except (httpx.HTTPError, ValueError):
            return None
        end = doc.get("end_session_endpoint")
        if not end:
            return None
        return end + "?" + urllib.parse.urlencode({"client_id": self.client_id,
                                                   "post_logout_redirect_uri": post_logout})
