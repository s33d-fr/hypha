"""Incidents en base (contrat § 20) : numérotation sous verrou, chronologie, liens, notifications.
Chaque requête filtre l'organisation, y compris celles qui suivent un incident déjà vérifié."""
from datetime import datetime

from ..config import Settings
from ..db import fetch_all, fetch_one
from ..errors import not_found
from ..monitors.notify import Notice
from ..util import jdump

FIELDS = """i.id, i.number, i.title, i.severity, i.status, i.commander_id, cu.email AS commander_email,
            i.customer_impact, i.summary, i.postmortem, i.fields, i.status_page_ids, i.public_message,
            i.detected_at, i.resolved_at, i.created_by, i.created_at, i.updated_at"""
FROM = "FROM incidents i LEFT JOIN users cu ON cu.id = i.commander_id"
STATUSES = ("active", "stable", "resolved")
LINK_KINDS = ("monitor", "host", "slo", "synthetic", "service", "issue")
TIMELINE_KINDS = ("note", "public_update")
STATUS_LABELS = {"active": "actif", "stable": "stabilisé", "resolved": "résolu"}
STATE_OF = {"active": "ALERT", "stable": "WARN", "resolved": "OK"}
NUMBER_LOCK = 7268005


async def load(conn, org_id: int, incident_id: int) -> dict:
    row = await fetch_one(conn, f"SELECT {FIELDS} {FROM} WHERE i.org_id = %s AND i.id = %s", (org_id, incident_id))
    if row is None:
        raise not_found("incident introuvable")
    return row


async def next_number(conn, org_id: int) -> int:
    """Numéro suivant dans l'organisation, sous verrou de transaction : deux déclarations simultanées
    ne prennent jamais le même numéro."""
    await conn.execute("SELECT pg_advisory_xact_lock(%s, (%s)::int)", (NUMBER_LOCK, org_id % 2_000_000_000))
    row = await fetch_one(conn, "SELECT coalesce(max(number), 0) + 1 AS n FROM incidents WHERE org_id = %s", (org_id,))
    return int(row["n"]) if row else 1


async def add_timeline(conn, incident_id: int, kind: str, body: str, author_id: int | None = None,
                       data: dict | None = None, ts: datetime | None = None) -> dict | None:
    return await fetch_one(conn, """
        INSERT INTO incident_timeline (incident_id, ts, kind, author_id, body, data)
        VALUES (%s, coalesce(%s::timestamptz, now()), %s, %s, %s, %s::jsonb)
        RETURNING id, ts, kind, author_id, body, data
    """, (incident_id, ts, kind, author_id, body[:20000], jdump(data or {})))


async def check_link(conn, org_id: int, kind, ref) -> str:
    """Référence d'un lien, vérifiée dans l'organisation ; ValueError sinon.

    monitor : « id » ou « id|groupe » ; host, slo, synthetic, issue : id ; service : nom.
    """
    if kind not in LINK_KINDS:
        raise ValueError(f"kind de lien attendu : {'|'.join(LINK_KINDS)}")
    if isinstance(ref, int) and not isinstance(ref, bool):
        ref = str(ref)
    if not isinstance(ref, str) or not ref.strip() or len(ref) > 1200:
        raise ValueError("ref (texte) est obligatoire")
    ref = ref.strip()
    if kind == "service":
        if len(ref) > 200:
            raise ValueError("nom de service trop long")
        return ref
    head = ref.split("|", 1)[0] if kind == "monitor" else ref
    if not head.isdigit():
        raise ValueError(f"ref d'un lien {kind} : identifiant numérique" + (" (ou id|groupe)" if kind == "monitor" else ""))
    table = {"monitor": "monitors", "host": "hosts", "slo": "slos", "synthetic": "synthetic_tests",
             "issue": "error_issues"}[kind]
    row = await fetch_one(conn, f"SELECT id FROM {table} WHERE org_id = %s AND id = %s", (org_id, int(head)))
    if row is None:
        raise ValueError(f"{kind} {head} introuvable dans l'organisation")
    return ref


async def add_link(conn, incident_id: int, kind: str, ref: str) -> bool:
    row = await fetch_one(conn, """
        INSERT INTO incident_links (incident_id, kind, ref) VALUES (%s, %s, %s)
        ON CONFLICT DO NOTHING RETURNING incident_id
    """, (incident_id, kind, ref))
    return row is not None


async def links(conn, incident_id: int) -> list[dict]:
    return await fetch_all(conn, "SELECT kind, ref FROM incident_links WHERE incident_id = %s ORDER BY kind, ref",
                           (incident_id,))


async def timeline(conn, incident_id: int, limit: int = 2000) -> list[dict]:
    return await fetch_all(conn, """
        SELECT t.id, t.ts, t.kind, t.author_id, u.email AS author, t.body, t.data
          FROM incident_timeline t LEFT JOIN users u ON u.id = t.author_id
         WHERE t.incident_id = %s ORDER BY t.ts, t.id LIMIT %s
    """, (incident_id, limit))


async def detail(conn, org_id: int, incident_id: int) -> dict:
    inc = await load(conn, org_id, incident_id)
    return {**inc, "links": await links(conn, incident_id), "timeline": await timeline(conn, incident_id)}


async def incident_channels(conn, settings: Settings, org_id: int) -> tuple[list[dict], bool]:
    """Canaux de `orgs.settings.incident_channel_ids`, et l'autorisation des cibles privées de l'org."""
    org = await fetch_one(conn, "SELECT settings FROM orgs WHERE id = %s", (org_id,))
    org_settings = (org or {}).get("settings") or {}
    allow = settings.allow_private_targets or bool(org_settings.get("allow_private_targets"))
    ids = [i for i in org_settings.get("incident_channel_ids") or [] if isinstance(i, int) and not isinstance(i, bool)]
    if not ids:
        return [], allow
    return await fetch_all(conn, """
        SELECT id, kind, name, config FROM notification_channels WHERE org_id = %s AND id = ANY(%s::bigint[]) AND enabled
    """, (org_id, ids)), allow


def notices(settings: Settings, inc: dict, channels: list[dict], message: str, previous_status: str | None,
            allow_private: bool) -> list[Notice]:
    """Notifications d'une déclaration ou d'un changement de statut (PagerDuty/Opsgenie : résolu = résolution)."""
    url = f"{settings.public_url}/#/incidents/{inc['id']}"
    status = inc["status"]
    subject = (f"[HYPHA] INC-{inc['number']} · {STATUS_LABELS.get(status, status)} · SEV-{inc['severity']} · "
               f"{inc['title']}")[:300]
    mon = {"id": inc["id"], "name": f"INC-{inc['number']} {inc['title']}", "type": "incident",
           "priority": inc["severity"]}
    return [Notice(c, mon, "*", STATE_OF.get(status, "ALERT"), STATE_OF.get(previous_status) if previous_status else None,
                   None, message, url, False, allow_private, kind="incident", subject_override=subject,
                   dedup_key=f"incident:{inc['id']}") for c in channels]
