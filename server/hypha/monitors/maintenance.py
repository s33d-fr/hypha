"""Maintenances côté worker (contrat § 16) : maintenances actives par organisation, contexte de
suppression des moniteurs, événements `source=downtime` de début et de fin.

Les événements se retrouvent par leur clé d'agrégation `downtime:<id>:<début de l'occurrence ms>` :
aucun état mémoire, un worker qui prend la relève ne double ni n'oublie un début ou une fin (une
fin n'est écrite que si le début l'a été, et dans le jour qui suit).
"""
from dataclasses import dataclass, field
from datetime import datetime, timedelta

from ..db import fetch_all
from ..util import dt_to_ms, jdump
from . import downtime as D

FIELDS = "id, org_id, name, message, scope, start_ts, end_ts, recurrence, enabled"
END_LOOKBACK = timedelta(days=1)


@dataclass
class OrgContext:
    """Ce que l'évaluation d'un moniteur doit savoir de son organisation."""
    active: list = field(default_factory=list)          # [D.Active]
    host_tags: dict | None = None                       # hostname → étiquettes (si une maintenance en a besoin)
    down_hosts: set = field(default_factory=set)        # hôtes dont un moniteur `host` est en ALERT


async def load(conn, now: datetime) -> list[dict]:
    """Maintenances activées qui peuvent être actives maintenant ou finies depuis moins d'un jour."""
    return await fetch_all(conn, f"""
        SELECT {FIELDS} FROM downtimes
         WHERE enabled AND start_ts <= %s AND (end_ts IS NULL OR end_ts > %s)
         ORDER BY id
    """, (now + timedelta(minutes=1), now - END_LOOKBACK))


def active_by_org(rows: list[dict], now: datetime) -> dict[int, list[D.Active]]:
    out: dict[int, list[D.Active]] = {}
    for r in rows:
        w = D.active_window(r, now)
        if w:
            out.setdefault(r["org_id"], []).append(D.Active(r["id"], r["name"], r["scope"] or {}, w.end))
    return out


async def contexts(conn, rows: list[dict], now: datetime) -> dict[int, OrgContext]:
    ctx = {org: OrgContext(active=act) for org, act in active_by_org(rows, now).items()}
    need_tags = [org for org, c in ctx.items() if any(a.host_tags_needed for a in c.active)]
    if need_tags:
        for org in need_tags:
            ctx[org].host_tags = {}
        for r in await fetch_all(conn, """
            SELECT DISTINCT ON (org_id, hostname) org_id, hostname, tags FROM hosts
             WHERE org_id = ANY(%s::bigint[]) ORDER BY org_id, hostname, last_seen DESC
        """, (need_tags,)):
            ctx[r["org_id"]].host_tags[r["hostname"]] = r["tags"] or {}
    return ctx


def key(downtime_id: int, w: D.Window) -> str:
    return f"downtime:{downtime_id}:{dt_to_ms(w.start)}"


async def emit_events(conn, rows: list[dict], now: datetime) -> int:
    """Écrit les événements de début et de fin d'occurrence manquants ; rend leur nombre."""
    starts, ends = [], []
    for r in rows:
        w = D.active_window(r, now)
        if w:
            starts.append((r, w, key(r["id"], w)))
        e = D.last_ended(r, now, END_LOOKBACK)
        if e:
            ends.append((r, e, key(r["id"], e)))
    if not starts and not ends:
        return 0
    since = min(w.start for _, w, _ in starts + ends)
    keys = [k for _, _, k in starts + ends]
    orgs = sorted({r["org_id"] for r, _, _ in starts + ends})
    seen = {(x["aggregation_key"], x["event_type"]) for x in await fetch_all(conn, """
        SELECT aggregation_key, event_type FROM events
         WHERE org_id = ANY(%s::bigint[]) AND source = 'downtime' AND ts >= %s AND aggregation_key = ANY(%s::text[])
    """, (orgs, since, keys))}
    n = 0
    for r, w, k in starts:
        if (k, "downtime_start") not in seen:
            until = f" jusqu'à {w.end.strftime('%Y-%m-%d %H:%M')} UTC" if w.end else " sans fin prévue"
            await event(conn, r, now, "downtime_start", f"Maintenance commencée : {r['name']}",
                         (r["message"] + "\n" if r["message"] else "") + "Notifications suspendues" + until, k)
            seen.add((k, "downtime_start"))
            n += 1
    for r, w, k in ends:
        if (k, "downtime_start") in seen and (k, "downtime_end") not in seen:
            await event(conn, r, now, "downtime_end", f"Maintenance terminée : {r['name']}",
                         "Notifications rétablies", k)
            n += 1
    return n


async def event(conn, r: dict, now: datetime, kind: str, title: str, text: str, key: str) -> None:
    tags = {**((r.get("scope") or {}).get("tags") or {}), "downtime_id": str(r["id"])}
    await conn.execute("""
        INSERT INTO events (org_id, ts, host_id, source, event_type, title, text, alert_type, priority, tags, aggregation_key)
        VALUES (%s, %s, NULL, 'downtime', %s, %s, %s, 'info', 'normal', %s::jsonb, %s)
    """, (r["org_id"], now, kind, title[:500], text[:8000], jdump(tags), key[:200]))
