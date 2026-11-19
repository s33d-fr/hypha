"""Journal d'audit (contrat § 24) : qui a fait quoi, quand, d'où, sur quel objet.

Chaque route qui écrit appelle `record()` dans la même transaction que l'écriture, après elle.
Les secrets ne sont jamais consignés : `diff` passe par `scrub()`, qui masque les champs dont le
nom évoque un secret (clé, jeton, mot de passe, url de webhook…).
"""
from typing import Any

from .util import jdump

SECRET_HINTS = ("password", "secret", "token", "api_key", "apikey", "key_hash", "authorization",
                "webhook", "routing_key", "community", "auth_key", "priv_key")
SECRET_EXACT = {"url", "headers"}      # config d'un canal : l'URL d'un webhook entrant vaut un secret
MAX_TEXT = 2000


def scrub(value: Any, depth: int = 0) -> Any:
    """Copie de `value` sans secret, bornée en profondeur et en longueur."""
    if depth > 6:
        return "…"
    if isinstance(value, dict):
        out = {}
        for k, v in value.items():
            low = str(k).lower()
            secret = low in SECRET_EXACT or any(h in low for h in SECRET_HINTS)
            out[k] = "[masqué]" if secret and v not in (None, "", [], {}) else scrub(v, depth + 1)
        return out
    if isinstance(value, list):
        return [scrub(v, depth + 1) for v in value[:100]]
    if isinstance(value, str) and len(value) > MAX_TEXT:
        return value[:MAX_TEXT] + "…"
    return value


def actor_of(principal) -> str:
    if principal is None:
        return "système"
    if getattr(principal, "app_key_id", None) and not getattr(principal, "session_id", None):
        return f"clé:{principal.app_key_id}"
    return principal.email or (f"utilisateur:{principal.user_id}" if principal.user_id else "inconnu")


async def record(conn, *, org_id: int | None, principal, action: str, object_type: str,
                 object_id: Any = None, summary: str = "", before: Any = None, after: Any = None,
                 request=None) -> None:
    """Consigne une action. `before`/`after` : état de l'objet avant et après (facultatifs)."""
    diff = {}
    if before is not None:
        diff["avant"] = scrub(before)
    if after is not None:
        diff["apres"] = scrub(after)
    ip = ua = None
    if request is not None:
        ip = request.headers.get("x-forwarded-for", "").split(",")[0].strip() or \
            (request.client.host if request.client else None)
        ua = (request.headers.get("user-agent") or "")[:300] or None
    await conn.execute(
        "INSERT INTO hypha.audit_log (org_id, user_id, actor, action, object_type, object_id, summary, diff, ip, user_agent) "
        "VALUES (%s, %s, %s, %s, %s, %s, %s, %s::jsonb, %s, %s)",
        (org_id, getattr(principal, "user_id", None), actor_of(principal), action, object_type,
         None if object_id is None else str(object_id), summary[:500], jdump(diff), ip, ua))
