"""Tests synthétiques (contrat § 7 ; § 29 : multi-étapes, assertions, emplacements privés)."""
from fastapi import APIRouter, Body, Depends, Query, Request

from .. import audit, quotas
from ..auth.principal import Principal, member, viewer
from ..db import fetch_all, fetch_one
from ..errors import bad_request, not_found
from ..runtime import rt
from ..synthetics import runner
from ..synthetics.netguard import is_public
from ..synthetics.probes import icmp_available
from ..synthetics.validate import (keep_secret, literal_ip, location_host_ids, normalize_test, public_config,
                                   step_hosts, target_host)
from ..util import jdump, ms_to_dt
from .common import done, ok, time_range

router = APIRouter()

FIELDS = ("t.id, t.name, t.kind, t.target, t.config, t.interval_s, t.enabled, t.created_at, t.last_run, "
          "t.locations, t.steps")
LAST = """
    LEFT JOIN LATERAL (
        SELECT r.ok AS last_ok, r.latency_ms AS last_latency_ms, r.ts AS last_ts, r.error AS last_error,
               r.status_code AS last_status_code
          FROM synthetic_results r
         WHERE r.org_id = t.org_id AND r.test_id = t.id AND r.ts > now() - interval '1 day'
         ORDER BY r.ts DESC LIMIT 1
    ) lr ON true
    LEFT JOIN LATERAL (
        SELECT avg(CASE WHEN r.ok THEN 1.0 ELSE 0.0 END)::float8 AS uptime_24h
          FROM synthetic_results r
         WHERE r.org_id = t.org_id AND r.test_id = t.id AND r.ts > now() - interval '1 day'
    ) upt ON true
"""
EDITABLE = ("name", "kind", "target", "config", "interval_s", "enabled", "steps", "locations")


async def _org_settings(conn, org_id: int) -> dict:
    row = await fetch_one(conn, "SELECT settings FROM orgs WHERE id = %s", (org_id,))
    return (row or {}).get("settings") or {}


async def _validated(conn, org_id: int, data: dict, org_settings: dict, test_id: int | None = None) -> dict:
    try:
        test = normalize_test(data, icmp_available() if data.get("kind") == "icmp" else True)
    except ValueError as e:
        raise bad_request(str(e), "invalid_synthetic")
    # Garde réseau du SaaS : seul l'emplacement `server` sonde depuis le réseau d'S33D ; un agent
    # privé teste justement le réseau de son client.
    if "server" in test["locations"] and not runner.allow_private(rt().settings, org_settings):
        hosts = step_hosts(test["steps"]) if test["kind"] == "multistep" else [target_host(test["kind"], test["target"])]
        for host in hosts:
            ip = literal_ip(host) if host else None
            if ip and not is_public(ip):
                raise bad_request("cible à adresse non publique refusée sur ce service", "target_not_allowed")
    wanted = location_host_ids(test["locations"])
    if wanted:
        found = {r["id"] for r in await fetch_all(conn, "SELECT id FROM hosts WHERE org_id = %s AND id = ANY(%s::bigint[])",
                                                    (org_id, wanted))}
        missing = sorted(set(wanted) - found)
        if missing:
            raise bad_request(f"emplacements inconnus : {', '.join(f'agent:{m}' for m in missing)}", "invalid_synthetic")
    await quotas.check_synthetic(conn, org_id, test, test_id)      # limites de formule (§ 52)
    return test


async def _load(conn, org_id: int, test_id: int) -> dict:
    row = await fetch_one(conn, f"SELECT {FIELDS}, lr.*, upt.* FROM synthetic_tests t {LAST} "
                                f"WHERE t.org_id = %s AND t.id = %s", (org_id, test_id))
    if row is None:
        raise not_found("test synthétique introuvable")
    row["by_location"] = await fetch_all(conn, """
        SELECT location, count(*) AS runs, avg(CASE WHEN ok THEN 1.0 ELSE 0.0 END)::float8 AS uptime_24h,
               (array_agg(ok ORDER BY ts DESC))[1] AS last_ok, max(ts) AS last_ts,
               (array_agg(latency_ms ORDER BY ts DESC))[1] AS last_latency_ms,
               (array_agg(error ORDER BY ts DESC))[1] AS last_error
          FROM synthetic_results
         WHERE org_id = %s AND test_id = %s AND ts > now() - interval '1 day'
         GROUP BY location ORDER BY location
    """, (org_id, test_id))
    return row


def _snapshot(row: dict) -> dict:
    return {k: row.get(k) for k in EDITABLE}


def _public(row: dict) -> dict:
    """Ligne rendue par l'API : config sans mot de passe en clair."""
    if row and isinstance(row.get("config"), dict):
        return {**row, "config": public_config(row.get("kind"), row["config"])}
    return row


@router.get("/synthetics")
async def list_synthetics(p: Principal = Depends(viewer)):
    async with rt().db.reader() as conn:
        rows = await fetch_all(conn, f"SELECT {FIELDS}, lr.*, upt.* FROM synthetic_tests t {LAST} "
                                     f"WHERE t.org_id = %s ORDER BY t.name, t.id", (p.org_id,))
    return ok([_public(r) for r in rows])


@router.post("/synthetics")
async def create_synthetic(request: Request, data: dict = Body(...), p: Principal = Depends(member)):
    async with rt().db.writer() as conn:
        test = await _validated(conn, p.org_id, data, await _org_settings(conn, p.org_id))
        row = await fetch_one(conn, """
            INSERT INTO synthetic_tests (org_id, name, kind, target, config, interval_s, enabled, steps, locations)
            VALUES (%s, %s, %s, %s, %s::jsonb, %s, %s, %s::jsonb, %s::text[]) RETURNING id
        """, (p.org_id, test["name"], test["kind"], test["target"], jdump(test["config"]), test["interval_s"],
              test["enabled"], jdump(test["steps"]), test["locations"]))
        await audit.record(conn, org_id=p.org_id, principal=p, action="create", object_type="synthetic",
                           object_id=row["id"], summary=f"test « {test['name']} » créé", after=test, request=request)
        return ok(_public(await _load(conn, p.org_id, row["id"])), 201)


@router.get("/synthetics/{test_id}")
async def get_synthetic(test_id: int, p: Principal = Depends(viewer)):
    async with rt().db.reader() as conn:
        return ok(_public(await _load(conn, p.org_id, test_id)))


@router.put("/synthetics/{test_id}")
async def update_synthetic(test_id: int, request: Request, data: dict = Body(...), p: Principal = Depends(member)):
    async with rt().db.writer() as conn:
        current = await _load(conn, p.org_id, test_id)
        merged = _snapshot(current)
        merged.update({k: v for k, v in data.items() if k in merged})
        merged["config"] = keep_secret(merged.get("config"), current.get("config"))
        test = await _validated(conn, p.org_id, merged, await _org_settings(conn, p.org_id), test_id)
        await conn.execute("""
            UPDATE synthetic_tests SET name = %s, kind = %s, target = %s, config = %s::jsonb, interval_s = %s,
                   enabled = %s, steps = %s::jsonb, locations = %s::text[] WHERE org_id = %s AND id = %s
        """, (test["name"], test["kind"], test["target"], jdump(test["config"]), test["interval_s"], test["enabled"],
              jdump(test["steps"]), test["locations"], p.org_id, test_id))
        await audit.record(conn, org_id=p.org_id, principal=p, action="update", object_type="synthetic",
                           object_id=test_id, summary=f"test « {test['name']} » modifié",
                           before=_snapshot(current), after=test, request=request)
        return ok(_public(await _load(conn, p.org_id, test_id)))


@router.delete("/synthetics/{test_id}")
async def delete_synthetic(test_id: int, request: Request, p: Principal = Depends(member)):
    async with rt().db.writer() as conn:
        row = await fetch_one(conn, "DELETE FROM synthetic_tests WHERE org_id = %s AND id = %s "
                                    "RETURNING id, name, kind, target", (p.org_id, test_id))
        if row is None:
            raise not_found("test synthétique introuvable")
        await audit.record(conn, org_id=p.org_id, principal=p, action="delete", object_type="synthetic",
                           object_id=test_id, summary=f"test « {row['name']} » supprimé", before=row, request=request)
    return done()


@router.get("/synthetics/{test_id}/results")
async def synthetic_results(test_id: int, from_: int | None = Query(None, alias="from"), to: int | None = None,
                            location: str = "", p: Principal = Depends(viewer)):
    start, end = time_range(from_, to)
    params: list = [p.org_id, test_id, ms_to_dt(start), ms_to_dt(end)]
    cond = ""
    if location.strip():
        cond = "AND location = %s"
        params.append(location.strip())
    async with rt().db.reader() as conn:
        exists = await fetch_one(conn, "SELECT id FROM synthetic_tests WHERE org_id = %s AND id = %s", (p.org_id, test_id))
        if exists is None:
            raise not_found("test synthétique introuvable")
        rows = await fetch_all(conn, f"""
            SELECT ts, location, ok, status_code, latency_ms, error, details FROM synthetic_results
             WHERE org_id = %s AND test_id = %s AND ts >= %s AND ts < %s {cond} ORDER BY ts DESC LIMIT 5000
        """, params)
    return ok(rows)


@router.post("/synthetics/{test_id}/run")
async def run_synthetic(test_id: int, request: Request, p: Principal = Depends(member)):
    runtime = rt()
    async with runtime.db.reader() as conn:
        test = await _load(conn, p.org_id, test_id)
        org_settings = await _org_settings(conn, p.org_id)
        dnsbl = await runner.dnsbl_cache(conn, p.org_id) if runner.uses_dnsbl(test) else None
    if "server" not in (test["locations"] or ["server"]):
        raise bad_request("ce test ne tourne que sur des agents : il s'exécute à leur prochain passage",
                          "agent_only")
    # La sonde tourne hors transaction : un délai réseau de 30 s ne doit pas tenir une connexion.
    result = await runner.execute(runtime.settings, test, org_settings, dnsbl)
    async with runtime.db.writer() as conn:
        row = await runner.record(conn, p.org_id, test_id, runtime.settings.synthetics_location, result)
        await conn.execute("UPDATE synthetic_tests SET last_run = now() WHERE org_id = %s AND id = %s",
                           (p.org_id, test_id))
        await audit.record(conn, org_id=p.org_id, principal=p, action="run", object_type="synthetic",
                           object_id=test_id, summary=f"test « {test['name']} » exécuté : "
                                                      f"{'réussi' if result.ok else 'en échec'}", request=request)
    return ok(row)
