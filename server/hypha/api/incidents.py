"""Incidents (contrat § 20) : `viewer` lit, `member` déclare, suit, résout et fait rédiger le post-mortem."""
import logging
from datetime import timedelta

from fastapi import APIRouter, BackgroundTasks, Body, Depends, Query, Request

from .. import audit, quotas
from ..auth.principal import Principal, admin, member, viewer
from ..db import fetch_all, fetch_one
from ..errors import ApiError, bad_request, not_found
from ..explain.llm import HourlyQuota, LlmUnavailable, chat
from ..incidents import postmortem as PM
from ..incidents import service as S
from ..ingest.extras import state_of
from ..monitors.notify import deliver_all
from ..runtime import rt
from ..util import jdump, now_ms, parse_group_key
from .common import ok

log = logging.getLogger("hypha.incidents")
router = APIRouter()

EDITABLE = ("title", "severity", "status", "commander_id", "customer_impact", "summary", "postmortem", "fields",
            "status_page_ids", "public_message")
POSTMORTEM_PER_HOUR = 10
_quota = HourlyQuota(POSTMORTEM_PER_HOUR)
ERROR_STATUSES = ["emergency", "alert", "critical", "error"]


def _str(data: dict, key: str, limit: int, required: bool = False) -> str:
    v = data.get(key)
    if v is None and not required:
        return ""
    if not isinstance(v, str) or (required and not v.strip()) or len(v) > limit:
        raise bad_request(f"{key} : texte de {limit} caractères au plus" + (" (obligatoire)" if required else ""),
                          "invalid_incident")
    return v.strip() if required else v


def _severity(v) -> int:
    if isinstance(v, bool) or not isinstance(v, int) or not 1 <= v <= 5:
        raise bad_request("severity vaut 1 (SEV-1, le plus grave) à 5", "invalid_incident")
    return v


async def _validated(conn, org_id: int, data: dict, current: dict | None = None) -> dict:
    merged = {**({k: current[k] for k in EDITABLE} if current else
                 {"severity": 3, "status": "active", "commander_id": None, "customer_impact": "", "summary": "",
                  "postmortem": "", "fields": {}, "status_page_ids": [], "public_message": ""}),
              **{k: v for k, v in data.items() if k in EDITABLE}}
    out = {"title": _str(merged, "title", 300, True), "severity": _severity(merged["severity"]),
           "customer_impact": _str(merged, "customer_impact", 5000), "summary": _str(merged, "summary", 10000),
           "postmortem": _str(merged, "postmortem", 100000), "public_message": _str(merged, "public_message", 5000)}
    if merged["status"] not in S.STATUSES:
        raise bad_request(f"status attendu : {'|'.join(S.STATUSES)}", "invalid_incident")
    out["status"] = merged["status"]
    fields = merged.get("fields") or {}
    if not isinstance(fields, dict) or len(jdump(fields)) > 20000:
        raise bad_request("fields doit être un objet (services, hôtes, équipes…)", "invalid_incident")
    out["fields"] = fields
    commander = merged.get("commander_id")
    if commander is not None:
        if isinstance(commander, bool) or not isinstance(commander, int):
            raise bad_request("commander_id : identifiant d'un membre", "invalid_incident")
        row = await fetch_one(conn, "SELECT user_id FROM memberships WHERE org_id = %s AND user_id = %s",
                              (org_id, commander))
        if row is None:
            raise bad_request("commander_id : ce compte n'est pas membre de l'organisation", "invalid_incident")
    out["commander_id"] = commander
    pages = merged.get("status_page_ids") or []
    if not isinstance(pages, list) or len(pages) > 20 or not all(isinstance(i, int) and not isinstance(i, bool)
                                                                 for i in pages):
        raise bad_request("status_page_ids : liste d'identifiants de pages de statut", "invalid_incident")
    if pages:
        rows = await fetch_all(conn, "SELECT id FROM status_pages WHERE org_id = %s AND id = ANY(%s::bigint[])",
                               (org_id, pages))
        missing = set(pages) - {r["id"] for r in rows}
        if missing:
            raise bad_request(f"pages de statut inconnues : {sorted(missing)}", "invalid_incident")
    out["status_page_ids"] = sorted(set(pages))
    return out


async def _links(conn, org_id: int, raw) -> list[tuple[str, str]]:
    if raw is None:
        return []
    if not isinstance(raw, list) or len(raw) > 50:
        raise bad_request("links : liste de {kind, ref} (50 au plus)", "invalid_incident")
    out = []
    for item in raw:
        if not isinstance(item, dict):
            raise bad_request("links : chaque lien est un objet {kind, ref}", "invalid_incident")
        try:
            out.append((item.get("kind"), await S.check_link(conn, org_id, item.get("kind"), item.get("ref"))))
        except ValueError as e:
            raise bad_request(str(e), "invalid_link")
    return list(dict.fromkeys(out))


async def _insert(conn, p: Principal, inc: dict) -> dict:
    number = await S.next_number(conn, p.org_id)
    row = await fetch_one(conn, """
        INSERT INTO incidents (org_id, number, title, severity, status, commander_id, customer_impact, summary,
                               postmortem, fields, status_page_ids, public_message, resolved_at, created_by)
        VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s::jsonb, %s::bigint[], %s,
                CASE WHEN %s = 'resolved' THEN now() END, %s)
        RETURNING id
    """, (p.org_id, number, inc["title"], inc["severity"], inc["status"], inc["commander_id"], inc["customer_impact"],
          inc["summary"], inc["postmortem"], jdump(inc["fields"]), inc["status_page_ids"], inc["public_message"],
          inc["status"], p.user_id))
    return await S.load(conn, p.org_id, row["id"])


async def _notify(background: BackgroundTasks, conn, org_id: int, inc: dict, message: str, previous: str | None):
    settings = rt().settings
    channels, allow = await S.incident_channels(conn, settings, org_id)
    notices = S.notices(settings, inc, channels, message, previous, allow)
    if notices:
        background.add_task(deliver_all, settings, notices)


def _who(p: Principal) -> str:
    return p.email or (f"clé:{p.app_key_id}" if p.app_key_id else "système")


@router.get("/incidents")
async def list_incidents(status: str | None = None, severity: str | None = None,
                         limit: int = Query(200, ge=1, le=1000), p: Principal = Depends(viewer)):
    where, params = ["i.org_id = %s"], [p.org_id]
    if status:
        wanted = [s.strip() for s in status.split(",") if s.strip()]
        if not all(s in S.STATUSES for s in wanted):
            raise bad_request(f"status attendu : {'|'.join(S.STATUSES)}")
        where.append("i.status = ANY(%s::text[])")
        params.append(wanted)
    if severity:
        sev = [s.strip() for s in severity.split(",") if s.strip()]
        if not all(s.isdigit() and 1 <= int(s) <= 5 for s in sev):
            raise bad_request("severity : 1 à 5, séparés par des virgules")
        where.append("i.severity = ANY(%s::smallint[])")
        params.append([int(s) for s in sev])
    async with rt().db.reader() as conn:
        rows = await fetch_all(conn, f"""
            SELECT {S.FIELDS}, (SELECT count(*) FROM incident_links l WHERE l.incident_id = i.id) AS links_count
              {S.FROM} WHERE {' AND '.join(where)}
             ORDER BY CASE i.status WHEN 'active' THEN 0 WHEN 'stable' THEN 1 ELSE 2 END, i.detected_at DESC
             LIMIT %s
        """, params + [limit])
    now = now_ms()
    for r in rows:
        end = r["resolved_at"].timestamp() * 1000 if r["resolved_at"] else now
        r["duration_s"] = int((end - r["detected_at"].timestamp() * 1000) / 1000)
    return ok(rows)


@router.post("/incidents")
async def create_incident(request: Request, background: BackgroundTasks, data: dict = Body(...),
                          p: Principal = Depends(member)):
    async with rt().db.writer() as conn:
        inc = await _validated(conn, p.org_id, data)
        links = await _links(conn, p.org_id, data.get("links"))
        row = await _insert(conn, p, inc)
        await S.add_timeline(conn, row["id"], "system", f"Incident déclaré (SEV-{row['severity']}) par {_who(p)}",
                             p.user_id, {"status": row["status"], "severity": row["severity"]})
        for kind, ref in links:
            await S.add_link(conn, row["id"], kind, ref)
        await audit.record(conn, org_id=p.org_id, principal=p, action="create", object_type="incident",
                           object_id=row["id"], summary=f"INC-{row['number']} « {row['title']} » déclaré",
                           after={**inc, "links": links}, request=request)
        await _notify(background, conn, p.org_id, row,
                      f"Incident déclaré par {_who(p)} : {row['title']}"
                      + (f"\nImpact : {row['customer_impact']}" if row["customer_impact"] else ""), None)
        out = await S.detail(conn, p.org_id, row["id"])
    return ok(out, 201)


@router.get("/incidents/settings")
async def incident_settings(p: Principal = Depends(viewer)):
    """Canaux prévenus à la déclaration et à chaque changement de statut (`orgs.settings.incident_channel_ids`)."""
    async with rt().db.reader() as conn:
        org = await fetch_one(conn, "SELECT settings FROM orgs WHERE id = %s", (p.org_id,))
    ids = ((org or {}).get("settings") or {}).get("incident_channel_ids") or []
    return ok({"incident_channel_ids": [i for i in ids if isinstance(i, int) and not isinstance(i, bool)]})


@router.put("/incidents/settings")
async def set_incident_settings(request: Request, data: dict = Body(...), p: Principal = Depends(admin)):
    ids = data.get("incident_channel_ids")
    if not isinstance(ids, list) or len(ids) > 20 or not all(isinstance(i, int) and not isinstance(i, bool) for i in ids):
        raise bad_request("incident_channel_ids : liste d'identifiants de canaux (20 au plus)")
    ids = sorted(set(ids))
    async with rt().db.writer() as conn:
        if ids:
            rows = await fetch_all(conn, "SELECT id FROM notification_channels WHERE org_id = %s AND id = ANY(%s::bigint[])",
                                   (p.org_id, ids))
            missing = set(ids) - {r["id"] for r in rows}
            if missing:
                raise bad_request(f"canaux inconnus : {sorted(missing)}")
        before = await fetch_one(conn, "SELECT settings -> 'incident_channel_ids' AS ids FROM orgs WHERE id = %s",
                                 (p.org_id,))
        await conn.execute("UPDATE orgs SET settings = settings || %s::jsonb WHERE id = %s",
                           (jdump({"incident_channel_ids": ids}), p.org_id))
        await audit.record(conn, org_id=p.org_id, principal=p, action="update", object_type="org_settings",
                           object_id=p.org_id, summary="canaux des incidents modifiés",
                           before={"incident_channel_ids": (before or {}).get("ids")},
                           after={"incident_channel_ids": ids}, request=request)
    return ok({"incident_channel_ids": ids})


@router.post("/incidents/from-alert")
async def incident_from_alert(request: Request, background: BackgroundTasks, data: dict = Body(...),
                              p: Principal = Depends(member)):
    monitor_id, group = data.get("monitor_id"), data.get("group") or "*"
    if isinstance(monitor_id, bool) or not isinstance(monitor_id, int):
        raise bad_request("monitor_id (entier) est obligatoire")
    if not isinstance(group, str) or len(group) > 1000:
        raise bad_request("group : clé de groupe du moniteur")
    async with rt().db.writer() as conn:
        mon = await fetch_one(conn, "SELECT id, name, priority FROM monitors WHERE org_id = %s AND id = %s",
                              (p.org_id, monitor_id))
        if mon is None:
            raise not_found("moniteur introuvable")
        state = await fetch_one(conn, "SELECT state, value, since FROM monitor_states WHERE monitor_id = %s "
                                      "AND group_key = %s", (monitor_id, group))
        title = mon["name"] + ("" if group == "*" else f" ({group})")
        inc = await _validated(conn, p.org_id, {"title": title[:300], "severity": mon["priority"] or 3,
                                                **{k: v for k, v in data.items() if k in ("commander_id",
                                                                                          "customer_impact")}})
        row = await _insert(conn, p, inc)
        await S.add_link(conn, row["id"], "monitor", f"{monitor_id}|{group}")
        host = parse_group_key(group).get("host")
        if host:
            h = await fetch_one(conn, "SELECT id FROM hosts WHERE org_id = %s AND hostname = %s "
                                      "ORDER BY last_seen DESC LIMIT 1", (p.org_id, host))
            if h:
                await S.add_link(conn, row["id"], "host", str(h["id"]))
        history = await fetch_all(conn, """
            SELECT ts, from_state, to_state, value FROM alert_events
             WHERE org_id = %s AND monitor_id = %s AND group_key = %s ORDER BY ts DESC LIMIT 20
        """, (p.org_id, monitor_id, group))
        for h in reversed(history):
            await S.add_timeline(conn, row["id"], "alert",
                                 f"{title} : {h['from_state'] or '?'} → {h['to_state']}"
                                 + (f", valeur {h['value']:.4g}" if h["value"] is not None else ""),
                                 None, {"monitor_id": monitor_id, "group": group, "from": h["from_state"],
                                        "to": h["to_state"], "value": h["value"]}, h["ts"])
        await S.add_timeline(conn, row["id"], "system",
                             f"Incident ouvert depuis l'alerte par {_who(p)}"
                             + (f" (état {state['state']})" if state else ""), p.user_id)
        await audit.record(conn, org_id=p.org_id, principal=p, action="create", object_type="incident",
                           object_id=row["id"], summary=f"INC-{row['number']} ouvert depuis l'alerte {title}",
                           after={**inc, "monitor_id": monitor_id, "group": group}, request=request)
        await _notify(background, conn, p.org_id, row, f"Incident ouvert depuis l'alerte {title}", None)
        out = await S.detail(conn, p.org_id, row["id"])
    return ok(out, 201)


@router.get("/incidents/{incident_id}")
async def get_incident(incident_id: int, p: Principal = Depends(viewer)):
    async with rt().db.reader() as conn:
        return ok(await S.detail(conn, p.org_id, incident_id))


@router.put("/incidents/{incident_id}")
async def update_incident(incident_id: int, request: Request, background: BackgroundTasks, data: dict = Body(...),
                          p: Principal = Depends(member)):
    async with rt().db.writer() as conn:
        current = await S.load(conn, p.org_id, incident_id)
        inc = await _validated(conn, p.org_id, data, current)
        await conn.execute("""
            UPDATE incidents SET title = %s, severity = %s, status = %s, commander_id = %s, customer_impact = %s,
                   summary = %s, postmortem = %s, fields = %s::jsonb, status_page_ids = %s::bigint[],
                   public_message = %s,
                   resolved_at = CASE WHEN %s = 'resolved' THEN coalesce(resolved_at, now()) END,
                   updated_at = now()
             WHERE org_id = %s AND id = %s
        """, (inc["title"], inc["severity"], inc["status"], inc["commander_id"], inc["customer_impact"],
              inc["summary"], inc["postmortem"], jdump(inc["fields"]), inc["status_page_ids"], inc["public_message"],
              inc["status"], p.org_id, incident_id))
        if inc["status"] != current["status"]:
            await S.add_timeline(conn, incident_id, "status",
                                 f"Statut : {S.STATUS_LABELS[current['status']]} → {S.STATUS_LABELS[inc['status']]}",
                                 p.user_id, {"from": current["status"], "to": inc["status"]})
        if inc["severity"] != current["severity"]:
            await S.add_timeline(conn, incident_id, "severity",
                                 f"Sévérité : SEV-{current['severity']} → SEV-{inc['severity']}",
                                 p.user_id, {"from": current["severity"], "to": inc["severity"]})
        row = await S.load(conn, p.org_id, incident_id)
        await audit.record(conn, org_id=p.org_id, principal=p, action="update", object_type="incident",
                           object_id=incident_id, summary=f"INC-{row['number']} modifié",
                           before={k: current[k] for k in EDITABLE if k != "postmortem"},
                           after={k: inc[k] for k in EDITABLE if k != "postmortem"}, request=request)
        if inc["status"] != current["status"]:
            await _notify(background, conn, p.org_id, row,
                          f"INC-{row['number']} {row['title']} : {S.STATUS_LABELS[current['status']]} → "
                          f"{S.STATUS_LABELS[inc['status']]} ({_who(p)})", current["status"])
        out = await S.detail(conn, p.org_id, incident_id)
    return ok(out)


@router.post("/incidents/{incident_id}/timeline")
async def add_timeline_entry(incident_id: int, request: Request, data: dict = Body(...),
                             p: Principal = Depends(member)):
    kind = data.get("kind", "note")
    if kind not in S.TIMELINE_KINDS:
        raise bad_request(f"kind attendu : {'|'.join(S.TIMELINE_KINDS)}")
    body = _str(data, "body", 20000, True)
    async with rt().db.writer() as conn:
        inc = await S.load(conn, p.org_id, incident_id)
        entry = await S.add_timeline(conn, incident_id, kind, body, p.user_id)
        if kind == "public_update":
            await conn.execute("UPDATE incidents SET public_message = %s, updated_at = now() "
                               "WHERE org_id = %s AND id = %s", (body[:5000], p.org_id, incident_id))
        await audit.record(conn, org_id=p.org_id, principal=p, action="create", object_type="incident_timeline",
                           object_id=incident_id, summary=f"INC-{inc['number']} : entrée {kind}",
                           after={"kind": kind, "body": body}, request=request)
    return ok({**entry, "author": p.email}, 201)


@router.post("/incidents/{incident_id}/links")
async def add_incident_link(incident_id: int, request: Request, data: dict = Body(...),
                            p: Principal = Depends(member)):
    async with rt().db.writer() as conn:
        inc = await S.load(conn, p.org_id, incident_id)
        try:
            ref = await S.check_link(conn, p.org_id, data.get("kind"), data.get("ref"))
        except ValueError as e:
            raise bad_request(str(e), "invalid_link")
        if await S.add_link(conn, incident_id, data["kind"], ref):
            await S.add_timeline(conn, incident_id, "link", f"Lien ajouté : {data['kind']} {ref}", p.user_id,
                                 {"kind": data["kind"], "ref": ref, "added": True})
            await audit.record(conn, org_id=p.org_id, principal=p, action="link", object_type="incident",
                               object_id=incident_id, summary=f"INC-{inc['number']} : lien {data['kind']} {ref}",
                               after={"kind": data["kind"], "ref": ref}, request=request)
        out = await S.links(conn, incident_id)
    return ok(out, 201)


@router.delete("/incidents/{incident_id}/links")
async def delete_incident_link(incident_id: int, request: Request, kind: str = Query(..., max_length=20),
                               ref: str = Query(..., max_length=1200), p: Principal = Depends(member)):
    async with rt().db.writer() as conn:
        inc = await S.load(conn, p.org_id, incident_id)
        row = await fetch_one(conn, "DELETE FROM incident_links WHERE incident_id = %s AND kind = %s AND ref = %s "
                                    "RETURNING kind", (incident_id, kind, ref))
        if row is None:
            raise not_found("lien introuvable")
        await S.add_timeline(conn, incident_id, "link", f"Lien retiré : {kind} {ref}", p.user_id,
                             {"kind": kind, "ref": ref, "added": False})
        await audit.record(conn, org_id=p.org_id, principal=p, action="unlink", object_type="incident",
                           object_id=incident_id, summary=f"INC-{inc['number']} : lien {kind} {ref} retiré",
                           before={"kind": kind, "ref": ref}, request=request)
        out = await S.links(conn, incident_id)
    return ok(out)


async def _postmortem_context(conn, org_id: int, inc: dict, links: list[dict]):
    start = inc["detected_at"] - timedelta(minutes=30)
    end = inc["resolved_at"] or None
    timeline = await S.timeline(conn, inc["id"], 300)
    monitor_ids, groups, host_ids = set(), set(), set()
    for link in links:
        if link["kind"] == "monitor":
            mid, _, group = link["ref"].partition("|")
            monitor_ids.add(int(mid))
            if group:
                groups.add((int(mid), group))
                if parse_group_key(group).get("host"):
                    host_ids.add(("name", parse_group_key(group)["host"]))
        elif link["kind"] == "host" and link["ref"].isdigit():
            host_ids.add(("id", int(link["ref"])))
    alerts = []
    if monitor_ids:
        alerts = await fetch_all(conn, """
            SELECT a.ts, m.name AS monitor, a.monitor_id, a.group_key, a.from_state, a.to_state, a.value
              FROM alert_events a JOIN monitors m ON m.id = a.monitor_id
             WHERE a.org_id = %s AND a.monitor_id = ANY(%s::bigint[]) AND a.ts >= %s
               AND (%s::timestamptz IS NULL OR a.ts <= %s::timestamptz)
             ORDER BY a.ts LIMIT 300
        """, (org_id, sorted(monitor_ids), start, end, end))
        alerts = [a for a in alerts if not any(m == a["monitor_id"] for m, _ in groups)
                  or (a["monitor_id"], a["group_key"]) in groups]
    names = {v for k, v in host_ids if k == "name"}
    ids = [v for k, v in host_ids if k == "id"]
    if ids:
        for r in await fetch_all(conn, "SELECT hostname FROM hosts WHERE org_id = %s AND id = ANY(%s::bigint[])",
                                 (org_id, ids)):
            names.add(r["hostname"])
    logs = []
    if names:
        logs = await fetch_all(conn, """
            SELECT ts, status, service, left(message, 600) AS message FROM logs
             WHERE org_id = %s AND hostname = ANY(%s::text[]) AND status = ANY(%s::text[])
               AND ts >= %s AND ts < coalesce(%s::timestamptz, now())
             ORDER BY ts DESC LIMIT 1000
        """, (org_id, sorted(names), ERROR_STATUSES, start, end))
    return timeline, alerts, logs


@router.post("/incidents/{incident_id}/postmortem")
async def write_postmortem(incident_id: int, request: Request, data: dict | None = Body(None),
                           p: Principal = Depends(member)):
    from ..explain.prompt import normalize_lang
    settings = rt().settings
    if not settings.explain_enabled:
        raise ApiError(503, "llm_unavailable", "rédaction par l'IA non configurée sur ce serveur")
    lang = normalize_lang((data or {}).get("lang"))
    async with rt().db.reader() as conn:
        inc = await S.load(conn, p.org_id, incident_id)
        links = await S.links(conn, incident_id)
        timeline, alerts, logs = await _postmortem_context(conn, p.org_id, inc, links)
        await quotas.check_explanation(conn, p.org_id, state_of(rt()).usage)     # même compte que l'IA (§ 52)
    if not _quota.allow(p.org_id):
        raise ApiError(429, "rate_limited", f"{POSTMORTEM_PER_HOUR} post-mortems par heure au plus pour l'organisation")
    messages = PM.build_messages(lang, inc, links, timeline, alerts, logs)
    try:
        text, duration_ms = await chat(settings.llm_url, settings.llm_model, messages, settings.llm_timeout_s * 2)
    except LlmUnavailable as e:
        log.warning("post-mortem de l'incident %s (org %s) impossible : %s", incident_id, p.org_id, e)
        raise ApiError(503, "llm_unavailable", f"le modèle ne répond pas ({e})")
    state_of(rt()).usage.add(p.org_id, explanations=1)
    async with rt().db.writer() as conn:
        await conn.execute("UPDATE incidents SET postmortem = %s, updated_at = now() WHERE org_id = %s AND id = %s",
                           (text[:100000], p.org_id, incident_id))
        await S.add_timeline(conn, incident_id, "ai", f"Post-mortem rédigé par l'IA ({settings.llm_model}, "
                                                      f"{duration_ms} ms) à la demande de {_who(p)}", p.user_id,
                             {"model": settings.llm_model, "duration_ms": duration_ms})
        await audit.record(conn, org_id=p.org_id, principal=p, action="postmortem", object_type="incident",
                           object_id=incident_id, summary=f"INC-{inc['number']} : post-mortem rédigé par l'IA",
                           request=request)
    return ok({"postmortem": text, "model": settings.llm_model, "duration_ms": duration_ms,
               "context": {"timeline": len(timeline), "alerts": len(alerts), "logs": len(logs)}})
