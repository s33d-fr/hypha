"""Journal d'audit (contrat § 24) : lecture réservée aux administrateurs, pagination par curseur."""
import base64
from datetime import datetime

from fastapi import APIRouter, Depends, Query

from ..auth.principal import Principal, admin
from ..db import fetch_all
from ..errors import bad_request
from ..runtime import rt
from ..util import like_escape, ms_to_dt
from .common import ok, time_range

router = APIRouter()


def encode_cursor(ts: datetime, entry_id: int) -> str:
    # Horodatage complet (microsecondes) : en millisecondes, deux lignes de la même milliseconde
    # seraient sautées d'une page à l'autre.
    return base64.urlsafe_b64encode(f"{ts.isoformat()}|{entry_id}".encode()).decode().rstrip("=")


def decode_cursor(cursor: str) -> tuple[datetime, int]:
    try:
        raw = base64.urlsafe_b64decode(cursor + "=" * (-len(cursor) % 4)).decode()
        ts, _, entry_id = raw.partition("|")
        dt = datetime.fromisoformat(ts)
        if dt.tzinfo is None:
            raise ValueError
        return dt, int(entry_id)
    except (ValueError, UnicodeDecodeError):
        raise bad_request("curseur invalide")


@router.get("/audit")
async def list_audit(from_: int | None = Query(None, alias="from"), to: int | None = None,
                     actor: str | None = Query(None, max_length=320), object_type: str | None = Query(None, max_length=50),
                     action: str | None = Query(None, max_length=50), q: str = Query("", max_length=200),
                     limit: int = Query(100, ge=1, le=1000), cursor: str | None = Query(None, max_length=200),
                     p: Principal = Depends(admin)):
    start, end = time_range(from_, to, default_s=30 * 86400)
    where = ["org_id = %s", "ts >= %s", "ts < %s"]
    params: list = [p.org_id, ms_to_dt(start), ms_to_dt(end)]
    if actor:
        where.append("actor ILIKE %s")
        params.append("%" + like_escape(actor) + "%")
    if object_type:
        where.append("object_type = %s")
        params.append(object_type)
    if action:
        where.append("action = %s")
        params.append(action)
    if q.strip():
        pattern = "%" + like_escape(q.strip()) + "%"
        where.append("(summary ILIKE %s OR coalesce(object_id, '') ILIKE %s OR diff::text ILIKE %s)")
        params += [pattern, pattern, pattern]
    if cursor:
        ts, entry_id = decode_cursor(cursor)
        where.append("(ts, id) < (%s, %s)")
        params += [ts, entry_id]
    async with rt().db.reader() as conn:
        rows = await fetch_all(conn, f"""
            SELECT id, ts, actor, user_id, action, object_type, object_id, summary, diff, ip
              FROM audit_log WHERE {' AND '.join(where)}
             ORDER BY ts DESC, id DESC LIMIT %s
        """, params + [limit + 1])
    more = len(rows) > limit
    rows = rows[:limit]
    nxt = encode_cursor(rows[-1]["ts"], rows[-1]["id"]) if more and rows else None
    return ok({"entries": rows, "next_cursor": nxt})


async def purge(db) -> int:
    """Efface les entrées de plus de 13 mois (tâche du worker)."""
    async with db.writer() as conn:
        cur = await conn.execute("DELETE FROM audit_log WHERE ts < now() - interval '13 months'")
        return cur.rowcount
