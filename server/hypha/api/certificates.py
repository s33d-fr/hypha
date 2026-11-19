"""Certificats du parc (contrat § 33), en une liste triée par échéance :

- ceux que les agents lisent sur les hôtes (contrôle `tls.cert.expiry`) : fichiers, certificats
  déclarés dans les configurations d'Apache, nginx, Postfix, Dovecot et HAProxy, magasin Windows ;
- ceux que les tests synthétiques voient sur le réseau (https, ssl, smtp, imap).
"""
import re
import time
from datetime import datetime

from fastapi import APIRouter, Depends

from ..auth.principal import Principal, viewer
from ..db import fetch_all
from ..runtime import rt
from .common import ok

router = APIRouter()

WARN_DAYS = 21
CRIT_DAYS = 7
_CN = re.compile(r"commonName=([^,]+)")
_O = re.compile(r"organizationName=([^,]+)")


def _ms(iso: str | None) -> int | None:
    if not iso:
        return None
    try:
        return int(datetime.fromisoformat(iso.replace("Z", "+00:00")).timestamp() * 1000)
    except ValueError:
        return None


def _days(not_after_ms: int | None) -> float | None:
    if not_after_ms is None:
        return None
    return round((not_after_ms - time.time() * 1000) / 86_400_000, 1)


def _status(days: float | None, fallback: int = 3) -> int:
    if days is None:
        return fallback
    if days < CRIT_DAYS:
        return 2
    if days < WARN_DAYS:
        return 1
    return 0


def _short(dn: str | None) -> str | None:
    """« organizationName=Let's Encrypt, commonName=R11 » → « R11 (Let's Encrypt) »."""
    if not dn:
        return None
    cn, org = _CN.search(dn), _O.search(dn)
    if cn and org:
        return f"{cn.group(1)} ({org.group(1)})"
    return (cn or org).group(1) if (cn or org) else dn


# Dernier relevé de chaque hôte seulement (l'agent rend tous ses certificats d'un coup) : un
# certificat qu'il ne rend plus (retiré, remplacé) disparaît de la liste au relevé suivant.
HOST_SQL = """
    WITH recent AS (
        SELECT k.host_id, k.ts, k.status, k.message, k.tags,
               max(k.ts) OVER (PARTITION BY k.host_id) AS last_ts
          FROM check_runs k
         WHERE k.org_id = %s AND k.check_name = 'tls.cert.expiry' AND k.ts > now() - interval '3 hours'
    )
    SELECT DISTINCT ON (r.host_id, coalesce(r.tags->>'cert_path', ''), coalesce(r.tags->>'subject', ''))
           r.host_id, h.hostname, r.ts, r.status, r.message, r.tags
      FROM recent r JOIN hosts h ON h.id = r.host_id
     WHERE r.ts > r.last_ts - interval '10 minutes'
     ORDER BY r.host_id, coalesce(r.tags->>'cert_path', ''), coalesce(r.tags->>'subject', ''), r.ts DESC
"""
SYNTH_SQL = """
    SELECT DISTINCT ON (r.test_id, r.location)
           t.id AS test_id, t.name, t.kind, t.target, r.location, r.ts, r.ok, r.error, r.details
      FROM synthetic_results r JOIN synthetic_tests t ON t.id = r.test_id AND t.org_id = r.org_id
     WHERE r.org_id = %s AND t.enabled AND t.kind IN ('http', 'ssl', 'smtp', 'imap')
       AND r.ts > now() - interval '1 day' AND r.details ? 'ssl_days_left'
     ORDER BY r.test_id, r.location, r.ts DESC
"""


def host_item(r: dict) -> dict:
    tags = r.get("tags") or {}
    not_after = _ms(tags.get("not_after"))
    days = _days(not_after)
    return {"source": "agent", "host_id": r["host_id"], "hostname": r["hostname"],
            "subject": tags.get("subject"), "issuer": tags.get("issuer"), "sans": tags.get("sans"),
            "where": tags.get("cert_path"), "origin": tags.get("cert_source") or "file",
            "not_after": not_after, "days_left": days,
            "status": r["status"] if days is None else max(r["status"] if r["status"] != 3 else 0, _status(days)),
            "message": r.get("message"), "ts": r["ts"]}


def synth_item(r: dict) -> dict:
    d = r.get("details") or {}
    not_after = d.get("ssl_not_after")
    days = _days(not_after) if isinstance(not_after, (int, float)) else d.get("ssl_days_left")
    refused = (not r["ok"]) and "certificat" in (r.get("error") or "")
    return {"source": "synthetic", "test_id": r["test_id"], "name": r["name"], "kind": r["kind"],
            "where": r["target"], "location": r["location"], "subject": _short(d.get("subject")),
            "issuer": _short(d.get("issuer")), "origin": r["kind"] if r["kind"] != "ssl" else (d.get("protocol") or "tls"),
            "not_after": not_after, "days_left": days, "status": 2 if refused else _status(days),
            "message": r.get("error"), "ts": r["ts"]}


@router.get("/certificates")
async def certificates(p: Principal = Depends(viewer)):
    async with rt().db.reader() as conn:
        hosts = await fetch_all(conn, HOST_SQL, (p.org_id,))
        synth = await fetch_all(conn, SYNTH_SQL, (p.org_id,))
    items = [host_item(r) for r in hosts] + [synth_item(r) for r in synth]
    items.sort(key=lambda x: (x["days_left"] is None, x["days_left"] if x["days_left"] is not None else 0))
    summary = {"total": len(items), "expired": sum(1 for x in items if x["days_left"] is not None and x["days_left"] < 0),
               "critical": sum(1 for x in items if x["status"] == 2), "warning": sum(1 for x in items if x["status"] == 1),
               "ok": sum(1 for x in items if x["status"] == 0)}
    return ok({"summary": summary, "items": items, "warn_days": WARN_DAYS, "crit_days": CRIT_DAYS})
