"""Jetons signés HMAC-SHA256 (cookie d'état OIDC).

L'état de la connexion voyage dans un cookie signé par HYPHA_SECRET plutôt qu'en base : le retour
de l'IdP peut arriver sur un autre nœud, et le secret est commun aux trois.
"""
import base64
import hashlib
import hmac
import json
import time


def _b64e(b: bytes) -> str:
    return base64.urlsafe_b64encode(b).decode("ascii").rstrip("=")


def _b64d(s: str) -> bytes:
    return base64.urlsafe_b64decode(s + "=" * (-len(s) % 4))


def sign(payload: dict, secret: bytes) -> str:
    body = dict(payload, iat=int(time.time()))
    raw = _b64e(json.dumps(body, separators=(",", ":")).encode("utf-8"))
    mac = _b64e(hmac.new(secret, raw.encode("ascii"), hashlib.sha256).digest())
    return f"{raw}.{mac}"


def unsign(token: str | None, secret: bytes, max_age: int) -> dict | None:
    if not token or "." not in token or not secret:
        return None
    raw, _, mac = token.partition(".")
    expected = _b64e(hmac.new(secret, raw.encode("ascii"), hashlib.sha256).digest())
    if not hmac.compare_digest(mac, expected):
        return None
    try:
        body = json.loads(_b64d(raw))
    except ValueError:
        return None
    if not isinstance(body, dict) or time.time() - body.get("iat", 0) > max_age:
        return None
    return body
