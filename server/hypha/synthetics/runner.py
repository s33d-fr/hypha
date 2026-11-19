"""Exécuter un test et ranger son résultat (worker et `POST /synthetics/{id}/run`)."""
from ..config import Settings
from ..db import fetch_all, fetch_one
from ..util import jdump
from .mail import DNSBL_MAX_AGE_S, DNSBL_NOT_READ
from .probes import ProbeResult, run_test


def allow_private(settings: Settings, org_settings: dict | None) -> bool:
    return settings.allow_private_targets or bool((org_settings or {}).get("allow_private_targets"))


def uses_dnsbl(test: dict) -> bool:
    return test["kind"] == "mail_dns" and bool((test.get("config") or {}).get("dnsbl"))


def merge_dnsbl(rows: list[dict]) -> dict:
    """{ip: {zone: (verdict, lu)}} : pour chaque couple, le verdict lu le plus récent. Un résultat
    antérieur à `dnsbl_lu` date ses verdicts de son propre horodatage."""
    cache: dict = {}
    for r in rows:
        lu = r.get("lu") or {}
        for ip, zones in (r.get("dnsbl") or {}).items():
            for zone, verdict in (zones or {}).items():
                if verdict == DNSBL_NOT_READ:
                    continue
                at = int((lu.get(ip) or {}).get(zone, r["ts"]))
                old = cache.setdefault(ip, {}).get(zone)
                if old is None or at > old[1]:
                    cache[ip][zone] = (verdict, at)
    return cache


async def dnsbl_cache(conn, org_id: int) -> dict:
    """Derniers verdicts des listes noires lus pour l'organisation (dernier résultat de chaque test
    DNS de messagerie) : chaque résultat reporte les verdicts encore valables avec leur date de lecture."""
    rows = await fetch_all(conn, """
        SELECT DISTINCT ON (r.test_id) extract(epoch FROM r.ts)::bigint AS ts,
               r.details->'dnsbl' AS dnsbl, r.details->'dnsbl_lu' AS lu
          FROM synthetic_results r JOIN synthetic_tests t ON t.org_id = r.org_id AND t.id = r.test_id
         WHERE r.org_id = %s AND t.kind = 'mail_dns' AND r.details ? 'dnsbl'
           AND r.ts > now() - make_interval(secs => %s)
         ORDER BY r.test_id, r.ts DESC
    """, (org_id, 2 * DNSBL_MAX_AGE_S))
    return merge_dnsbl(rows)


async def record(conn, org_id: int, test_id: int, location: str, r: ProbeResult) -> dict:
    return await fetch_one(conn, """
        INSERT INTO synthetic_results (org_id, test_id, ts, location, ok, status_code, latency_ms, error, details)
        VALUES (%s, %s, now(), %s, %s, %s, %s, %s, %s::jsonb)
        RETURNING ts, location, ok, status_code, latency_ms, error, details
    """, (org_id, test_id, location, r.ok, r.status_code, r.latency_ms, (r.error or None) and r.error[:2000],
          jdump(r.details)))


async def execute(settings: Settings, test: dict, org_settings: dict | None, dnsbl: dict | None = None) -> ProbeResult:
    """`dnsbl` : verdicts déjà lus (dnsbl_cache), obligatoires pour un test qui interroge les listes noires."""
    allow = allow_private(settings, org_settings)
    if test["kind"] == "multistep":
        import asyncio

        from .multistep import MAX_STEPS, run_multistep
        steps = test.get("steps") or []
        budget = sum(float(s.get("timeout_s") or 10) for s in steps[:MAX_STEPS]) * 2 + 10
        try:
            return await asyncio.wait_for(run_multistep(steps, test["config"] or {}, allow), budget)
        except asyncio.TimeoutError:
            return ProbeResult(False, error=f"test interrompu après {budget:g} s")
        except Exception as e:              # une sonde ne doit jamais faire tomber le worker
            return ProbeResult(False, error=f"erreur interne de la sonde : {type(e).__name__}: {e}")
    config = test["config"] or {}
    if uses_dnsbl(test):
        if dnsbl is None:
            return ProbeResult(False, error="verdicts des listes noires non chargés : test non lancé")
        config = {**config, "_dnsbl_cache": dnsbl}
    return await run_test(test["kind"], test["target"], config, allow)
