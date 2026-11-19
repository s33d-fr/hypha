"""Routes de l'agent ajoutées en 0.4.0 : inventaire (§ 15), requêtes des bases (§ 30), synthétiques
privés (§ 29), canal de contrôle et résultat d'une action (§§ 27, 28).

Toutes passent par la clé d'ingestion. Le canal de contrôle est tiré par l'agent (aucun port ouvert
chez le client) : il livre les actions approuvées (marquées `sent`), les tests synthétiques
assignés à l'hôte de l'agent et la version visée par une mise à jour.
"""
from types import SimpleNamespace

from fastapi import APIRouter, Request

from .. import audit
from ..api.common import ok
from ..db import copy_rows, fetch_all, fetch_one
from ..errors import ApiError, not_found
from ..runtime import rt
from ..util import now_ms
from . import agent_bodies as AB
from . import body as B
from .routes import accepted, client_ip, prologue, read_raw, store
from .store import extract_key

router = APIRouter()

PACKAGE_COLUMNS = ("org_id", "host_id", "name", "version", "arch", "source")
MAX_ACTIONS_PER_POLL = 20
FINAL_STATES = ("succeeded", "failed", "refused_by_agent")


def agent_principal(agent_id: str, hostname: str | None = None):
    """Auteur d'une écriture faite par un agent, pour le journal d'audit (« agent:<hôte> »)."""
    return SimpleNamespace(user_id=None, email=f"agent:{hostname or agent_id}", app_key_id=None, session_id=None)


# ── § 15 inventaire ────────────────────────────────────────────────────────────

@router.post("/api/v1/intake/inventory")
async def intake_inventory(request: Request):
    st, key, body, agent = await prologue(request)
    inv = AB.parse_inventory(body)
    replace = "packages" in body
    async with rt().db.writer(timeout=3) as conn:
        host_id = await st.ensure_host(conn, key.org_id, agent, client_ip(request))
        # L'upsert verrouille la ligne d'inventaire : deux envois du même hôte se suivent sans se mêler.
        count = len(inv["packages"]) if replace else None
        await conn.execute("""
            INSERT INTO host_inventory (host_id, org_id, ts, hardware, os, updates, packages_count)
            VALUES (%s, %s, %s, %s::jsonb, %s::jsonb, %s::jsonb, COALESCE(%s::int, 0))
            ON CONFLICT (host_id) DO UPDATE
               SET org_id = EXCLUDED.org_id, ts = EXCLUDED.ts, hardware = EXCLUDED.hardware, os = EXCLUDED.os,
                   updates = EXCLUDED.updates,
                   packages_count = COALESCE(%s::int, host_inventory.packages_count)
        """, (host_id, key.org_id, inv["ts"], inv["hardware"], inv["os"], inv["updates"], count, count))
        written = 0
        if replace:
            await conn.execute("DELETE FROM host_packages WHERE host_id = %s", (host_id,))
            written = await copy_rows(conn, "host_packages", PACKAGE_COLUMNS,
                                      [(key.org_id, host_id) + p for p in inv["packages"]])
        await st.touch_key(conn, key)
    return accepted(written, inv["dropped"])


# ── § 30 requêtes des bases (DBM) ──────────────────────────────────────────────

@router.post("/api/v1/intake/dbm")
async def intake_dbm(request: Request):
    st, key, body, agent = await prologue(request)
    async with rt().db.writer(timeout=3) as conn:
        host_id = await st.ensure_host(conn, key.org_id, agent, client_ip(request))
        rows, dropped = AB.parse_dbm(body, key.org_id, host_id, B.Window.around())
        written, d = await st.copy(conn, "dbm_queries", AB.DBM_COLUMNS, rows, 2)
        await st.touch_key(conn, key)
    return accepted(written, dropped + d)


# ── § 29 résultats des synthétiques privés ─────────────────────────────────────

@router.post("/api/v1/intake/synthetics")
async def intake_synthetics(request: Request):
    st, key, body, agent = await prologue(request)
    results, dropped = AB.parse_synthetic_results(body, B.Window.around())
    async with rt().db.writer(timeout=3) as conn:
        host_id = await st.ensure_host(conn, key.org_id, agent, client_ip(request))
        tests = await fetch_all(conn, "SELECT id, locations FROM synthetic_tests WHERE org_id = %s AND id = ANY(%s::bigint[])",
                                (key.org_id, sorted({r["test_id"] for r in results})))
        # Seul un test assigné à l'hôte de cet agent est accepté de lui.
        mine = {t["id"] for t in tests if f"agent:{host_id}" in (t["locations"] or [])}
        location = f"agent:{agent.hostname}"
        rows = [(key.org_id, r["test_id"], r["ts"], location, r["ok"], r["status_code"], r["latency_ms"], r["error"],
                 r["details"]) for r in results if r["test_id"] in mine]
        dropped += len(results) - len(rows)
        written, d = await st.copy(conn, "synthetic_results", AB.SYNTHETIC_COLUMNS, rows, 2)
        await st.touch_key(conn, key)
    return accepted(written, dropped + d)


# ── § 27 canal de contrôle ─────────────────────────────────────────────────────

CONTROL_AGENT = """
    SELECT a.host_id, a.target_version, a.capabilities, h.hostname
      FROM agents a LEFT JOIN hosts h ON h.id = a.host_id AND h.org_id = a.org_id
     WHERE a.id = %s AND a.org_id = %s
"""
TAKE_ACTIONS = f"""
    UPDATE agent_actions SET status = 'sent', sent_at = now()
     WHERE id IN (SELECT id FROM agent_actions
                   WHERE org_id = %s AND agent_id = %s AND status = 'approved' AND expires_at > now()
                   ORDER BY id LIMIT {MAX_ACTIONS_PER_POLL} FOR UPDATE SKIP LOCKED)
    RETURNING id, kind, params, expires_at
"""
AGENT_SYNTHETICS = """
    SELECT id, kind, target, config, steps, interval_s FROM synthetic_tests
     WHERE org_id = %s AND enabled AND %s = ANY(locations)
       AND kind NOT IN ('mail_roundtrip', 'mail_dns')      -- serveur seul : le mot de passe ne part pas
     ORDER BY id LIMIT 500
"""


@router.get("/api/v1/agent/control")
async def agent_control(request: Request):
    st = store()
    key = await st.auth_ingest(extract_key(request.headers))
    agent_id = AB.header_agent_id(request.headers.get("x-hypha-agent-id"))
    version = B.opt_text(request.headers.get("x-hypha-agent-version"), 50)
    out = {"server_time": now_ms(), "target_version": None, "actions": [], "synthetics": []}
    async with rt().db.writer(timeout=3) as conn:
        agent = await fetch_one(conn, CONTROL_AGENT, (agent_id, key.org_id))
        if agent is None:
            # Agent pas encore inscrit (aucun envoi /intake/host) : rien à lui confier.
            return ok(out)
        await conn.execute("UPDATE agents SET last_seen = now(), version = COALESCE(%s, version) "
                           "WHERE id = %s AND org_id = %s", (version, agent_id, key.org_id))
        out["target_version"] = agent["target_version"]
        actions = await fetch_all(conn, TAKE_ACTIONS, (key.org_id, agent_id))
        who = agent_principal(agent_id, agent["hostname"])
        for a in actions:
            await audit.record(conn, org_id=key.org_id, principal=who, action="send", object_type="action",
                               object_id=a["id"], summary=f"action {a['kind']} livrée à l'agent",
                               after={"status": "sent", "kind": a["kind"], "params": a["params"]}, request=request)
        out["actions"] = [{"id": a["id"], "kind": a["kind"], "params": a["params"], "expires_at": a["expires_at"]}
                          for a in actions]
        if (agent["capabilities"] or {}).get("synthetics") and agent["host_id"] is not None:
            out["synthetics"] = await fetch_all(conn, AGENT_SYNTHETICS, (key.org_id, f"agent:{agent['host_id']}"))
        await st.touch_key(conn, key)
    return ok(out)


# ── § 28 résultat d'une action ─────────────────────────────────────────────────

@router.post("/api/v1/agent/actions/{action_id}/result")
async def action_result(action_id: int, request: Request):
    st = store()
    key = await st.auth_ingest(extract_key(request.headers))
    data = B.decompress(await read_raw(request), request.headers.get("content-encoding"))
    body = B.parse_json_object(data)
    header = request.headers.get("x-hypha-agent-id")
    if not header and isinstance(body.get("agent"), dict):
        header = str(body["agent"].get("id") or "")
    agent_id = AB.header_agent_id(header)
    res = AB.parse_action_result(body)
    async with rt().db.writer(timeout=3) as conn:
        row = await fetch_one(conn, """
            UPDATE agent_actions SET status = %s, exit_code = COALESCE(%s, exit_code), output = %s,
                   finished_at = CASE WHEN %s = 'running' THEN NULL ELSE now() END
             WHERE id = %s AND org_id = %s AND agent_id = %s AND status IN ('sent', 'running')
            RETURNING id, host_id, kind, params, status, exit_code
        """, (res["status"], res["exit_code"], res["output"], res["status"], action_id, key.org_id, agent_id))
        if row is None:
            current = await fetch_one(conn, "SELECT status FROM agent_actions WHERE id = %s AND org_id = %s "
                                            "AND agent_id = %s", (action_id, key.org_id, agent_id))
            if current is None:
                raise not_found("action introuvable pour cet agent")
            # Renvoi du tampon disque de l'agent : un compte rendu déjà reçu (ou un « running »
            # arrivé après l'état final) est accepté sans rien changer.
            if current["status"] in FINAL_STATES and res["status"] in (current["status"], "running"):
                return ok({"status": "ok", "duplicate": True})
            raise ApiError(409, "invalid_state", f"action dans l'état « {current['status']} » : résultat refusé")
        host = await fetch_one(conn, "SELECT hostname FROM hosts WHERE id = %s AND org_id = %s",
                               (row["host_id"], key.org_id))
        await audit.record(conn, org_id=key.org_id, principal=agent_principal(agent_id, host and host["hostname"]),
                           action="result", object_type="action", object_id=action_id,
                           summary=f"action {row['kind']} : {row['status']}",
                           after={"status": row["status"], "exit_code": row["exit_code"],
                                  "output": res["output"][:2000]}, request=request)
        await st.touch_key(conn, key)
    return ok({"status": "ok"})
