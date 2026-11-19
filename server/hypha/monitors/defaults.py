"""Réglages posés à la création d'un espace : un nouvel utilisateur est prévenu d'une panne sans
rien avoir configuré. Un canal e-mail vers l'adresse du compte (quand elle est connue) et les
moniteurs de base du catalogue (templates.py), reliés à ce canal. Posés une seule fois par espace
(drapeau `default_monitors` des réglages de l'organisation)."""
import re

from .. import audit
from ..db import fetch_one
from ..util import jdump
from . import templates
from .validate import normalize_monitor

DEFAULT_TEMPLATES = ("host_down", "disk_full", "memory_high", "systemd_unit", "windows_service", "cert_expiry")
_EMAIL_RE = re.compile(r"^[^@\s]+@[^@\s]+\.[^@\s]+$")


def default_monitors(channel_ids: list[int]) -> list[dict]:
    """Définitions validées des moniteurs de base, reliées aux canaux donnés."""
    out = []
    for tid in DEFAULT_TEMPLATES:
        definition = templates.build(tid, {})
        definition["channel_ids"] = list(channel_ids)
        out.append(normalize_monitor(definition))
    return out


async def provision_org(conn, org_id: int, user_id: int | None, email: str | None) -> dict:
    """Canal e-mail et moniteurs de base d'un espace neuf ; rien si l'espace les a déjà reçus."""
    row = await fetch_one(conn, "SELECT settings ? 'default_monitors' AS done FROM orgs WHERE id = %s", (org_id,))
    if row is None or row["done"]:
        return {"channel_id": None, "monitor_ids": []}
    channel_ids: list[int] = []
    if email and _EMAIL_RE.match(email):
        ch = await fetch_one(conn, """
            INSERT INTO notification_channels (org_id, kind, name, config, enabled)
            VALUES (%s, 'email', %s, %s::jsonb, true) RETURNING id
        """, (org_id, f"E-mail ({email})"[:200], jdump({"to": [email]})))
        channel_ids.append(ch["id"])
        await audit.record(conn, org_id=org_id, principal=None, action="create", object_type="channel",
                           object_id=ch["id"], summary=f"canal e-mail vers {email} posé à la création de l'espace")
    ids = []
    for mon in default_monitors(channel_ids):
        m = await fetch_one(conn, """
            INSERT INTO monitors (org_id, name, type, query, thresholds, message, channel_ids, notify_no_data,
                                  no_data_s, renotify_s, tags, enabled, priority, options, created_by)
            VALUES (%s, %s, %s, %s::jsonb, %s::jsonb, %s, %s::bigint[], %s, %s, %s, %s::jsonb, %s, %s, %s::jsonb, %s)
            RETURNING id
        """, (org_id, mon["name"], mon["type"], jdump(mon["query"]), jdump(mon["thresholds"]), mon["message"],
              mon["channel_ids"], mon["notify_no_data"], mon["no_data_s"], mon["renotify_s"], jdump(mon["tags"]),
              mon["enabled"], mon["priority"], jdump(mon["options"]), user_id))
        ids.append(m["id"])
    await audit.record(conn, org_id=org_id, principal=None, action="create", object_type="monitor",
                       summary=f"{len(ids)} moniteurs de base posés à la création de l'espace")
    await conn.execute("UPDATE orgs SET settings = settings || '{\"default_monitors\": true}'::jsonb WHERE id = %s",
                       (org_id,))
    return {"channel_id": channel_ids[0] if channel_ids else None, "monitor_ids": ids}
