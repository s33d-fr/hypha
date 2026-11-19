"""Client Stripe minimal (API REST v1, corps en formulaire) et vérification des webhooks.

La clé secrète ne sort jamais d'ici : en-tête `Authorization` seulement, jamais dans une URL, un
journal ni un message d'erreur. Version d'API figée : les formes d'objets lues par le serveur
(périodes portées par les postes d'abonnement, `invoice.parent`) sont celles de cette version.
"""
import asyncio
import hashlib
import hmac
import json
import logging
import time
import uuid

import httpx

log = logging.getLogger("hypha.billing")

API = "https://api.stripe.com"
API_VERSION = "2025-03-31.basil"
SIGNATURE_TOLERANCE_S = 300


class StripeError(Exception):
    def __init__(self, status: int, code: str, message: str):
        super().__init__(f"Stripe {status} {code} : {message}")
        self.status = status
        self.code = code
        self.message = message


def encode(params: dict | None, prefix: str = "") -> list[tuple[str, str]]:
    """Paramètres imbriqués au format de Stripe : a[b][0][c]=v ; None omis, booléens en true/false."""
    out: list[tuple[str, str]] = []
    if params is None:
        return out
    items = params.items() if isinstance(params, dict) else enumerate(params)
    for k, v in items:
        key = f"{prefix}[{k}]" if prefix else str(k)
        if v is None:
            continue
        if isinstance(v, dict):
            out += encode(v, key)
        elif isinstance(v, (list, tuple)):
            if not v:
                out.append((key, ""))           # liste vide : efface (ex. default_tax_rates)
            out += encode(list(v), key)
        elif isinstance(v, bool):
            out.append((key, "true" if v else "false"))
        else:
            out.append((key, str(v)))
    return out


class Stripe:
    def __init__(self, secret_key: str, timeout: float = 20.0):
        if not secret_key:
            raise StripeError(0, "no_key", "clé Stripe absente (HYPHA_STRIPE_SECRET_KEY)")
        self._key = secret_key
        self.timeout = timeout

    def __repr__(self) -> str:
        return "Stripe(clé masquée)"

    async def request(self, method: str, path: str, params: dict | None = None,
                      idempotency_key: str | None = None) -> dict:
        headers = {"Authorization": f"Bearer {self._key}", "Stripe-Version": API_VERSION}
        data = encode(params)
        if method == "POST":
            headers["Idempotency-Key"] = idempotency_key or str(uuid.uuid4())
        delay = 1.0
        for attempt in range(3):
            try:
                async with httpx.AsyncClient(timeout=self.timeout) as client:
                    if method in ("GET", "DELETE"):
                        resp = await client.request(method, API + path, params=data, headers=headers)
                    else:
                        resp = await client.request(method, API + path, data=data, headers=headers)
            except httpx.HTTPError as e:
                if attempt == 2:
                    raise StripeError(0, "network", f"Stripe injoignable ({type(e).__name__})")
                await asyncio.sleep(delay)
                delay *= 2
                continue
            if resp.status_code == 429 or resp.status_code >= 500:
                if attempt < 2:
                    await asyncio.sleep(delay)
                    delay *= 2
                    continue
            try:
                body = resp.json()
            except ValueError:
                body = {}
            if resp.status_code >= 400:
                err = body.get("error") or {}
                raise StripeError(resp.status_code, err.get("code") or err.get("type") or "error",
                                  (err.get("message") or "erreur Stripe")[:500])
            return body
        raise StripeError(0, "retry", "Stripe : trois essais sans réponse")

    async def get(self, path: str, params: dict | None = None) -> dict:
        return await self.request("GET", path, params)

    async def post(self, path: str, params: dict | None = None, idempotency_key: str | None = None) -> dict:
        return await self.request("POST", path, params, idempotency_key)

    async def delete(self, path: str, params: dict | None = None) -> dict:
        return await self.request("DELETE", path, params)

    async def list_all(self, path: str, params: dict | None = None, limit: int = 1000) -> list[dict]:
        params = dict(params or {}, limit=100)
        out: list[dict] = []
        while len(out) < limit:
            page = await self.get(path, params)
            out += page.get("data") or []
            if not page.get("has_more") or not page.get("data"):
                break
            params["starting_after"] = page["data"][-1]["id"]
        return out


class SignatureError(ValueError):
    pass


def verify_webhook(payload: bytes, header: str | None, secret: str, now: float | None = None) -> dict:
    """Vérifie `Stripe-Signature` (HMAC-SHA256 de « t.corps ») et rend l'événement décodé."""
    if not secret:
        raise SignatureError("secret du webhook absent (HYPHA_STRIPE_WEBHOOK_SECRET)")
    if not header:
        raise SignatureError("en-tête Stripe-Signature absent")
    stamp, sigs = None, []
    for part in header.split(","):
        k, _, v = part.strip().partition("=")
        if k == "t":
            stamp = v
        elif k == "v1":
            sigs.append(v)
    if not stamp or not stamp.isdigit() or not sigs:
        raise SignatureError("en-tête Stripe-Signature illisible")
    expected = hmac.new(secret.encode(), stamp.encode() + b"." + payload, hashlib.sha256).hexdigest()
    if not any(hmac.compare_digest(expected, s) for s in sigs):
        raise SignatureError("signature invalide")
    now = time.time() if now is None else now
    if abs(now - int(stamp)) > SIGNATURE_TOLERANCE_S:
        raise SignatureError("signature trop ancienne (rejeu ?)")
    try:
        return json.loads(payload)
    except ValueError:
        raise SignatureError("corps illisible")
