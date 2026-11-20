"""Moniteurs et alertes (contrat §§ 7, 17, 19) : définition, sourdine, acquittement, aperçu, modèles."""
from datetime import datetime, timezone

from fastapi import APIRouter, Body, Depends, Query, Request

from .. import audit
from ..auth.principal import Principal, member, viewer
from ..db import fetch_all, fetch_one
from ..errors import ApiError, bad_request, not_found
from ..monitors import composite, runner, template, templates
from ..monitors.evaluate import SEVERITY, worst
from ..monitors.validate import normalize_monitor
from ..runtime import rt
from ..util import TAG_KEY_RE, jdump, ms_to_dt, now_ms, parse_group_key
from .common import done, ok, time_range

router = APIRouter()

MONITOR_FIELDS = """id, name, type, query, thresholds, message, channel_ids, notify_no_data, no_data_s,
                    renotify_s, tags, enabled, muted_until, priority, options, created_by, created_at, updated_at"""
EDITABLE = ("name", "type", "query", "thresholds", "message", "channel_ids", "notify_no_data", "no_data_s",
            "renotify_s", "tags", "enabled", "priority", "options")
FOREVER = datetime(9999, 1, 1, tzinfo=timezone.utc)
MAX_PREVIEW_GROUPS = 500


async def _summaries(conn, org_id: int, ids: list[int]) -> dict[int, dict]:
    if not ids:
        return {}
    rows = await fetch_all(conn, """
        SELECT ms.monitor_id, ms.state, count(*) AS n
          FROM monitor_states ms JOIN monitors m ON m.id = ms.monitor_id AND m.org_id = %s
         WHERE ms.monitor_id = ANY(%s::bigint[])
         GROUP BY 1, 2
    """, (org_id, ids))
    out: dict[int, dict] = {}
    for r in rows:
        out.setdefault(r["monitor_id"], {})[r["state"]] = r["n"]
    return out


def _present(row: dict, groups: dict) -> dict:
    row = dict(row)
    row["groups"] = groups
    # Jamais évalué (ou aucun groupe) : NODATA, plutôt qu'un OK qui rassurerait à tort.
    row["state"] = worst([s for s, n in groups.items() if n]) if groups else "NODATA"
    muted_until = row.get("muted_until")
    row["muted"] = bool(muted_until and muted_until.timestamp() * 1000 > now_ms())
    return row


async def _channels_exist(conn, org_id: int, ids: set[int], what: str) -> None:
    if not ids:
        return
    rows = await fetch_all(conn, "SELECT id FROM notification_channels WHERE org_id = %s AND id = ANY(%s::bigint[])",
                           (org_id, sorted(ids)))
    missing = ids - {r["id"] for r in rows}
    if missing:
        raise bad_request(f"{what} : canaux inconnus {sorted(missing)}", "invalid_monitor")


async def _check_composite(conn, org_id: int, ids: list[int], monitor_id: int | None) -> None:
    if monitor_id is not None and monitor_id in ids:
        raise bad_request("un composite ne peut pas se citer lui-même", "invalid_monitor")
    rows = await fetch_all(conn, "SELECT id FROM monitors WHERE org_id = %s AND id = ANY(%s::bigint[])", (org_id, ids))
    missing = set(ids) - {r["id"] for r in rows}
    if missing:
        raise bad_request(f"moniteurs inconnus dans l'expression : {sorted(missing)}", "invalid_monitor")
    graph: dict[int, set[int]] = {}
    for r in await fetch_all(conn, "SELECT id, query FROM monitors WHERE org_id = %s AND type = 'composite'", (org_id,)):
        q = r["query"] or {}
        try:
            graph[r["id"]] = set(q.get("ids") or composite.ids(composite.parse(q.get("expression", ""))))
        except ValueError:
            graph[r["id"]] = set()
    me = monitor_id if monitor_id is not None else 0
    graph[me] = set(ids)
    cycle = composite.find_cycle(graph, me)
    if cycle:
        raise bad_request("cycle de composites refusé : " + " → ".join(str(i) for i in cycle), "composite_cycle")


async def _check_refs(conn, org_id: int, mon: dict, monitor_id: int | None = None) -> None:
    """Canaux, test, SLO, moniteurs cités : tous doivent appartenir à l'organisation."""
    await _channels_exist(conn, org_id, set(mon["channel_ids"]), "channel_ids")
    mentioned = set(template.mentions(mon["message"]))
    for esc in mon["options"]["escalations"]:
        mentioned |= set(template.mentions(esc["message"]))
        await _channels_exist(conn, org_id, set(esc["channel_ids"]), "escalade")
    await _channels_exist(conn, org_id, mentioned, "mentions @canal-")
    if mon["type"] == "synthetic":
        row = await fetch_one(conn, "SELECT id FROM synthetic_tests WHERE org_id = %s AND id = %s",
                              (org_id, mon["query"]["test_id"]))
        if row is None:
            raise bad_request("test synthétique inconnu")
    if mon["type"] == "slo":
        row = await fetch_one(conn, "SELECT id FROM slos WHERE org_id = %s AND id = %s", (org_id, mon["query"]["slo_id"]))
        if row is None:
            raise bad_request("SLO inconnu")
    deps = mon["options"]["depends_on"]
    if deps:
        if monitor_id is not None and monitor_id in deps:
            raise bad_request("un moniteur ne peut pas dépendre de lui-même", "invalid_monitor")
        rows = await fetch_all(conn, "SELECT id FROM monitors WHERE org_id = %s AND id = ANY(%s::bigint[])",
                               (org_id, deps))
        missing = set(deps) - {r["id"] for r in rows}
        if missing:
            raise bad_request(f"options.depends_on : moniteurs inconnus {sorted(missing)}", "invalid_monitor")
    if mon["type"] == "composite":
        await _check_composite(conn, org_id, mon["query"]["ids"], monitor_id)


def _validated(data: dict) -> dict:
    try:
        return normalize_monitor(data)
    except ValueError as e:
        raise bad_request(str(e), "invalid_monitor")


async def _load(conn, org_id: int, monitor_id: int) -> dict:
    row = await fetch_one(conn, f"SELECT {MONITOR_FIELDS} FROM monitors WHERE org_id = %s AND id = %s",
                          (org_id, monitor_id))
    if row is None:
        raise not_found("moniteur introuvable")
    return row


async def _insert(conn, p: Principal, mon: dict, request: Request, summary: str) -> dict:
    await _check_refs(conn, p.org_id, mon)
    row = await fetch_one(conn, f"""
        INSERT INTO monitors (org_id, name, type, query, thresholds, message, channel_ids, notify_no_data,
                              no_data_s, renotify_s, tags, enabled, priority, options, created_by)
        VALUES (%s, %s, %s, %s::jsonb, %s::jsonb, %s, %s::bigint[], %s, %s, %s, %s::jsonb, %s, %s, %s::jsonb, %s)
        RETURNING {MONITOR_FIELDS}
    """, (p.org_id, mon["name"], mon["type"], jdump(mon["query"]), jdump(mon["thresholds"]), mon["message"],
          mon["channel_ids"], mon["notify_no_data"], mon["no_data_s"], mon["renotify_s"], jdump(mon["tags"]),
          mon["enabled"], mon["priority"], jdump(mon["options"]), p.user_id))
    await audit.record(conn, org_id=p.org_id, principal=p, action="create", object_type="monitor",
                       object_id=row["id"], summary=summary, after=mon, request=request)
    return row


# Routes fixes avant /monitors/{monitor_id} : « templates » ne doit pas être lu comme un identifiant.

@router.get("/monitors/templates")
async def monitor_templates(p: Principal = Depends(viewer)):
    return ok(templates.catalogue())


@router.post("/monitors/from-template")
async def monitor_from_template(request: Request, data: dict = Body(...), p: Principal = Depends(member)):
    try:
        definition = templates.build(data.get("template"), data.get("vars"))
    except ValueError as e:
        raise bad_request(str(e), "invalid_template")
    for key in ("name", "channel_ids", "priority", "message", "tags", "options", "renotify_s"):
        if key in data:
            definition[key] = data[key]
    mon = _validated(definition)
    async with rt().db.writer() as conn:
        row = await _insert(conn, p, mon, request, f"moniteur « {mon['name']} » créé depuis le modèle {data['template']}")
    return ok(_present(row, {}), 201)


@router.post("/monitors/preview")
async def monitor_preview(data: dict = Body(...), p: Principal = Depends(member)):
    """Évalue une définition sans rien enregistrer : groupes, séries, bornes (anomaly), prévision (forecast)."""
    mon = _validated({"name": data.get("name") or "aperçu", "type": data.get("type"), "query": data.get("query"),
                      "thresholds": data.get("thresholds"), "options": data.get("options"),
                      "no_data_s": data.get("no_data_s", 600)})
    m = {**mon, "id": 0, "org_id": p.org_id, "muted_until": None}
    detail: dict = {}
    async with rt().db.reader() as conn:
        if mon["type"] == "composite":
            await _check_composite(conn, p.org_id, mon["query"]["ids"], None)
        measured = await runner.measure(conn, m, {}, detail)
    groups = [{"group": g, "state": v[0] if v else "NODATA", "value": v[1] if v else None}
              for g, v in sorted(measured.items())][:MAX_PREVIEW_GROUPS]
    return ok({"groups": groups, "series": detail.get("series", []), "bounds": detail.get("bounds", []),
               "forecast": detail.get("forecast", [])})


@router.get("/monitors")
async def list_monitors(p: Principal = Depends(viewer)):
    async with rt().db.reader() as conn:
        rows = await fetch_all(conn, f"SELECT {MONITOR_FIELDS} FROM monitors WHERE org_id = %s ORDER BY name, id",
                               (p.org_id,))
        summaries = await _summaries(conn, p.org_id, [r["id"] for r in rows])
    return ok([_present(r, summaries.get(r["id"], {})) for r in rows])


@router.post("/monitors")
async def create_monitor(request: Request, data: dict = Body(...), p: Principal = Depends(member)):
    mon = _validated(data)
    async with rt().db.writer() as conn:
        row = await _insert(conn, p, mon, request, f"moniteur « {mon['name']} » créé")
    return ok(_present(row, {}), 201)


@router.get("/monitors/{monitor_id}")
async def get_monitor(monitor_id: int, p: Principal = Depends(viewer)):
    async with rt().db.reader() as conn:
        row = await _load(conn, p.org_id, monitor_id)
        states = await fetch_all(conn, """
            SELECT ms.group_key AS "group", ms.state, ms.value, ms.since, ms.last_eval, ms.last_notified,
                   ms.acked_by, ms.acked_at, ms.ack_comment, u.email AS acked_by_email, ms.flapping,
                   ms.escalation_step, ms.suppressed
              FROM monitor_states ms LEFT JOIN users u ON u.id = ms.acked_by
             WHERE ms.monitor_id = %s ORDER BY ms.group_key
        """, (monitor_id,))
    groups: dict = {}
    for s in states:
        groups[s["state"]] = groups.get(s["state"], 0) + 1
    out = _present(row, groups)
    out["states"] = sorted(states, key=lambda s: (-SEVERITY.get(s["state"], 0), s["group"]))
    return ok(out)


@router.put("/monitors/{monitor_id}")
async def update_monitor(monitor_id: int, request: Request, data: dict = Body(...), p: Principal = Depends(member)):
    async with rt().db.writer() as conn:
        current = await _load(conn, p.org_id, monitor_id)
        merged = {k: current.get(k) for k in EDITABLE}
        merged.update({k: v for k, v in data.items() if k in EDITABLE})
        mon = _validated(merged)
        await _check_refs(conn, p.org_id, mon, monitor_id)
        row = await fetch_one(conn, f"""
            UPDATE monitors SET name = %s, type = %s, query = %s::jsonb, thresholds = %s::jsonb, message = %s,
                   channel_ids = %s::bigint[], notify_no_data = %s, no_data_s = %s, renotify_s = %s,
                   tags = %s::jsonb, enabled = %s, priority = %s, options = %s::jsonb, updated_at = now()
             WHERE org_id = %s AND id = %s
            RETURNING {MONITOR_FIELDS}
        """, (mon["name"], mon["type"], jdump(mon["query"]), jdump(mon["thresholds"]), mon["message"],
              mon["channel_ids"], mon["notify_no_data"], mon["no_data_s"], mon["renotify_s"], jdump(mon["tags"]),
              mon["enabled"], mon["priority"], jdump(mon["options"]), p.org_id, monitor_id))
        if mon["type"] != current["type"] or mon["query"] != current["query"]:
            # Autre requête, autres groupes : les états d'avant ne veulent plus rien dire.
            await conn.execute("DELETE FROM monitor_states WHERE monitor_id = %s", (monitor_id,))
            runner.STREAKS.pop(monitor_id, None)
        await audit.record(conn, org_id=p.org_id, principal=p, action="update", object_type="monitor",
                           object_id=monitor_id, summary=f"moniteur « {mon['name']} » modifié",
                           before={k: current.get(k) for k in EDITABLE}, after=mon, request=request)
        summaries = await _summaries(conn, p.org_id, [monitor_id])
    return ok(_present(row, summaries.get(monitor_id, {})))


@router.delete("/monitors/{monitor_id}")
async def delete_monitor(monitor_id: int, request: Request, p: Principal = Depends(member)):
    async with rt().db.writer() as conn:
        row = await fetch_one(conn, "DELETE FROM monitors WHERE org_id = %s AND id = %s RETURNING id, name, type, query",
                              (p.org_id, monitor_id))
        if row is None:
            raise not_found("moniteur introuvable")
        await audit.record(conn, org_id=p.org_id, principal=p, action="delete", object_type="monitor",
                           object_id=monitor_id, summary=f"moniteur « {row['name']} » supprimé", before=row,
                           request=request)
    runner.STREAKS.pop(monitor_id, None)
    return done()


@router.post("/monitors/{monitor_id}/mute")
async def mute_monitor(monitor_id: int, request: Request, data: dict = Body(...), p: Principal = Depends(member)):
    if "until" not in data:
        raise bad_request("champ until attendu (ms, ou null pour lever la sourdine)")
    until = data["until"]
    if until is None:
        muted_until = None
    elif isinstance(until, (int, float)) and not isinstance(until, bool):
        muted_until = FOREVER if until <= 0 else ms_to_dt(until)
    else:
        raise bad_request("until doit être un horodatage en ms ou null")
    async with rt().db.writer() as conn:
        row = await fetch_one(conn, f"UPDATE monitors SET muted_until = %s, updated_at = now() "
                                    f"WHERE org_id = %s AND id = %s RETURNING {MONITOR_FIELDS}",
                              (muted_until, p.org_id, monitor_id))
        if row is None:
            raise not_found("moniteur introuvable")
        await audit.record(conn, org_id=p.org_id, principal=p, action="mute" if muted_until else "unmute",
                           object_type="monitor", object_id=monitor_id,
                           summary=f"moniteur « {row['name']} » " + ("mis en sourdine" if muted_until else "sorti de sourdine"),
                           after={"muted_until": muted_until}, request=request)
        summaries = await _summaries(conn, p.org_id, [monitor_id])
    return ok(_present(row, summaries.get(monitor_id, {})))


def _groups_arg(data: dict) -> list[str]:
    groups = data.get("groups")
    if groups is None:
        groups = [data.get("group")]
    if not isinstance(groups, list) or not groups or len(groups) > 500 or not all(
            isinstance(g, str) and 0 < len(g) <= 1000 for g in groups):
        raise bad_request("group (clé de groupe) ou groups (liste) attendu")
    return list(dict.fromkeys(groups))


@router.post("/monitors/{monitor_id}/groups/ack")
async def ack_groups(monitor_id: int, request: Request, data: dict = Body(...), p: Principal = Depends(member)):
    """Acquitte un groupe hors OK (ou une liste) : plus de relance ni d'escalade jusqu'au retour à OK."""
    groups = _groups_arg(data)
    comment = data.get("comment") or ""
    if not isinstance(comment, str) or len(comment) > 2000:
        raise bad_request("comment : chaîne de 2 000 caractères au plus")
    rows = await apply_ack(p, monitor_id, groups, comment, request)
    return ok({"acked": [{"group": r["group_key"], "state": r["state"], "acked_at": r["acked_at"]} for r in rows]})


async def apply_ack(p: Principal, monitor_id: int, groups: list[str], comment: str, request: Request) -> list[dict]:
    """Acquittement dans l'espace de `p` (route ci-dessus, et bouton « Acquitter » d'une notification push)."""
    async with rt().db.writer() as conn:
        mon = await _load(conn, p.org_id, monitor_id)
        rows = await fetch_all(conn, """
            UPDATE monitor_states SET acked_by = %s, acked_at = now(), ack_comment = %s
             WHERE monitor_id = %s AND group_key = ANY(%s::text[]) AND state <> 'OK'
            RETURNING group_key, state, acked_at
        """, (p.user_id, comment, monitor_id, groups))
        if not rows:
            known = await fetch_one(conn, "SELECT count(*) AS n FROM monitor_states WHERE monitor_id = %s "
                                          "AND group_key = ANY(%s::text[])", (monitor_id, groups))
            if known and known["n"]:
                raise ApiError(409, "not_in_alert", "groupe à l'état OK : rien à acquitter")
            raise not_found("groupe introuvable pour ce moniteur")
        who = p.email or (f"clé:{p.app_key_id}" if p.app_key_id else "")
        for r in rows:
            label = mon["name"] + ("" if r["group_key"] == "*" else f" ({r['group_key']})")
            tags = {k: v for k, v in parse_group_key(r["group_key"]).items() if TAG_KEY_RE.match(k)}
            await conn.execute("""
                INSERT INTO events (org_id, ts, source, event_type, title, text, alert_type, priority, tags, aggregation_key)
                VALUES (%s, now(), 'monitor', 'ack', %s, %s, 'info', 'normal', %s::jsonb, %s)
            """, (p.org_id, f"Acquitté : {label}"[:500], (f"{who} : {comment}" if comment else who)[:8000],
                  jdump({**tags, "monitor_id": str(monitor_id)}), f"monitor:{monitor_id}:{r['group_key']}"[:200]))
        await audit.record(conn, org_id=p.org_id, principal=p, action="ack", object_type="monitor",
                           object_id=monitor_id, summary=f"{len(rows)} groupe(s) de « {mon['name']} » acquitté(s)",
                           after={"groups": [r["group_key"] for r in rows], "comment": comment}, request=request)
    return rows


@router.delete("/monitors/{monitor_id}/groups/ack")
async def unack_group(monitor_id: int, request: Request, group: str = Query(..., max_length=1000),
                      p: Principal = Depends(member)):
    async with rt().db.writer() as conn:
        mon = await _load(conn, p.org_id, monitor_id)
        row = await fetch_one(conn, """
            UPDATE monitor_states SET acked_by = NULL, acked_at = NULL, ack_comment = NULL
             WHERE monitor_id = %s AND group_key = %s AND acked_at IS NOT NULL RETURNING group_key
        """, (monitor_id, group))
        if row is None:
            raise not_found("aucun acquittement sur ce groupe")
        await audit.record(conn, org_id=p.org_id, principal=p, action="unack", object_type="monitor",
                           object_id=monitor_id, summary=f"acquittement retiré : « {mon['name']} » ({group})",
                           request=request)
    return done()


@router.get("/monitors/{monitor_id}/history")
async def monitor_history(monitor_id: int, from_: int | None = Query(None, alias="from"), to: int | None = None,
                          p: Principal = Depends(viewer)):
    start, end = time_range(from_, to, default_s=30 * 86400)
    async with rt().db.reader() as conn:
        await _load(conn, p.org_id, monitor_id)
        rows = await fetch_all(conn, """
            SELECT id, ts, group_key AS "group", from_state, to_state, value, message, suppressed
              FROM alert_events
             WHERE org_id = %s AND monitor_id = %s AND ts >= %s AND ts < %s
             ORDER BY ts DESC LIMIT 1000
        """, (p.org_id, monitor_id, ms_to_dt(start), ms_to_dt(end)))
    return ok(rows)


@router.get("/alerts")
async def list_alerts(p: Principal = Depends(viewer)):
    async with rt().db.reader() as conn:
        rows = await fetch_all(conn, """
            SELECT m.id AS monitor_id, m.name AS monitor_name, m.type, m.tags, m.priority, ms.group_key AS "group",
                   ms.state, ms.value, ms.since, ms.last_eval, ms.last_notified, ms.acked_at IS NOT NULL AS acked,
                   ms.suppressed, ms.flapping,
                   (m.muted_until IS NOT NULL AND m.muted_until > now()) AS muted
              FROM monitor_states ms JOIN monitors m ON m.id = ms.monitor_id
             WHERE m.org_id = %s AND m.enabled AND ms.state <> 'OK'
             ORDER BY ms.since DESC
        """, (p.org_id,))
    rows.sort(key=lambda r: -SEVERITY.get(r["state"], 0))
    return ok(rows)
