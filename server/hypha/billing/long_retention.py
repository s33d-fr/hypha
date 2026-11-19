"""Conservation longue des journaux et des traces (option Pro 30 ou 90 jours, contrat § 52).

Les partitions journalières de `logs` et `spans` sont communes et purgées à 15 jours (fin du jour
+ 15 jours). Un jour J est clos pour l'ingestion à J+15 00:00 UTC (fenêtre de 14 jours) et sa
partition existe jusqu'à J+16 00:00 : le worker le copie dans cet intervalle, la nuit (01:00 à
05:00 UTC), pour les seuls espaces qui ont l'option, par tranches d'une heure (requêtes courtes,
index `(org_id, ts)` de la partition). Puis la borne `long_retention_state.copied_until` avance :
les vues `*_etendus` lisent alors ce jour dans `*_long`. Si la nuit est passée sans copie (base
indisponible), la copie se fait dans les 6 heures qui précèdent la purge.

Purge, de nuit aussi : lignes d'un espace à 30 jours qui passent 30 jours, et toutes les lignes
d'un espace dont l'option s'est arrêtée. Le reste part avec les partitions mensuelles (92 jours).
Coût : proportionnel au volume des seuls espaces qui paient l'option.
"""
import logging
from datetime import date, datetime, timedelta, timezone

from ..db import fetch_all

log = logging.getLogger("hypha.billing")
UTC = timezone.utc

NIGHT_HOURS = range(1, 5)
INGEST_WINDOW = timedelta(days=14)
SLICE = timedelta(hours=1)

COLUMNS = {
    "logs": ("org_id", "ts", "id", "host_id", "hostname", "service", "source", "status", "message", "attributes",
             "tags", "trace_id", "span_id"),
    "spans": ("org_id", "ts", "trace_id", "span_id", "parent_span_id", "service", "name", "resource", "kind",
              "duration_ns", "status", "status_message", "host_id", "hostname", "attributes", "resource_attributes",
              "events"),
}

ENTITLED_SQL = """
    SELECT b.org_id, b.retention_days FROM org_billing b JOIN orgs o ON o.id = b.org_id
     WHERE o.plan IN ('pro', 'entreprise') AND b.retention_days > 15
"""


def _night(now: datetime) -> bool:
    return now.hour in NIGHT_HOURS


async def run(db, now: datetime | None = None) -> str:
    now = (now or datetime.now(UTC)).astimezone(UTC)
    copied, purged = [], 0
    async with db.writer() as conn:
        entitled = {r["org_id"]: int(r["retention_days"]) for r in await fetch_all(conn, ENTITLED_SQL)}
        state = {r["parent"]: r["copied_until"] for r in
                 await fetch_all(conn, "SELECT parent, copied_until FROM long_retention_state")}
    for parent in ("logs", "spans"):
        day = state.get(parent)
        while day is not None and day + timedelta(days=1) <= now - INGEST_WINDOW:
            deadline = day + timedelta(days=16)               # purge de la partition du jour
            if not _night(now) and now < deadline - timedelta(hours=6):
                break
            for org_id in sorted(entitled):
                await _copy_day(db, parent, org_id, day)
            async with db.writer() as conn:
                await conn.execute("UPDATE long_retention_state SET copied_until = %s "
                                   "WHERE parent = %s AND copied_until = %s", (day + timedelta(days=1), parent, day))
                for org_id, days in entitled.items():
                    await conn.execute("""
                        INSERT INTO long_retention_orgs (org_id, retention_days, first_day, last_day)
                        VALUES (%s, %s, %s, %s)
                        ON CONFLICT (org_id) DO UPDATE SET retention_days = EXCLUDED.retention_days,
                               last_day = GREATEST(long_retention_orgs.last_day, EXCLUDED.last_day)
                    """, (org_id, days, day.date(), day.date()))
            copied.append(f"{parent} {day:%Y-%m-%d} ({len(entitled)} espace(s))")
            day += timedelta(days=1)
    if _night(now):
        purged = await _purge(db, entitled, now)
    if not copied and not purged:
        return "rien à copier ni à purger"
    return f"copiés : {', '.join(copied) or 'aucun'} ; tranches purgées : {purged}"


async def _copy_day(db, parent: str, org_id: int, day: datetime) -> None:
    cols = ", ".join(COLUMNS[parent])
    t = day
    while t < day + timedelta(days=1):
        async with db.writer() as conn:
            await conn.execute(f"INSERT INTO {parent}_long ({cols}) SELECT {cols} FROM {parent} "
                               f"WHERE org_id = %s AND ts >= %s AND ts < %s", (org_id, t, t + SLICE))
        t += SLICE


async def _purge(db, entitled: dict, now: datetime) -> int:
    """Espace qui garde l'option : au plus 48 tranches d'une heure par nuit (lignes qui passent 30 ou
    90 jours, effacées au plus tard le lendemain). Espace dont l'option s'est arrêtée : tranches d'un jour,
    120 par nuit, pour que tout parte en une nuit (rien n'est gardé au-delà de ce qui est annoncé)."""
    n = 0
    async with db.writer() as conn:
        tracked = await fetch_all(conn, "SELECT org_id, retention_days, first_day, last_day FROM long_retention_orgs")
    for r in tracked:
        keep = entitled.get(r["org_id"])
        floor = now - timedelta(days=keep) if keep else now        # sans option : tout ce qui est dans *_long
        first = datetime.combine(r["first_day"], datetime.min.time(), UTC)
        stop = min(floor, datetime.combine(r["last_day"] + timedelta(days=1), datetime.min.time(), UTC))
        step = SLICE if keep else timedelta(days=1)
        t, budget = first, (48 if keep else 120)
        while t < stop and budget > 0:
            async with db.writer() as conn:
                for parent in ("logs", "spans"):
                    await conn.execute(f"DELETE FROM {parent}_long WHERE org_id = %s AND ts >= %s AND ts < %s",
                                       (r["org_id"], t, min(t + step, stop)))
            t += step
            budget -= 1
            n += 1
        async with db.writer() as conn:
            if keep is None and t >= stop:
                await conn.execute("DELETE FROM long_retention_orgs WHERE org_id = %s", (r["org_id"],))
            elif t > first:
                new_first: date = t.date() if t < stop else stop.date()
                await conn.execute("UPDATE long_retention_orgs SET first_day = %s WHERE org_id = %s",
                                   (new_first, r["org_id"]))
    return n
