"""Événements (contrat § 7) : liste filtrée et création manuelle ou par API."""
import json

from fastapi import APIRouter, Depends, Query, Request
from pydantic import BaseModel, Field

from .. import audit
from ..auth.principal import Principal, member, viewer
from ..db import fetch_all, fetch_one
from ..errors import QueryError, bad_request
from ..ingest.body import EVENT_COLUMNS, Window, event_row
from ..query import logs_ql
from ..runtime import rt
from ..util import ms_to_dt, now_ms
from .common import ok, time_range

router = APIRouter()

EVENT_FIELDS = """e.id, e.ts, e.host_id, h.hostname, e.source, e.event_type AS type, e.title, e.text,
                  e.alert_type, e.priority, e.tags, e.aggregation_key"""


@router.get("/events")
async def list_events(q: str = "", from_: int | None = Query(None, alias="from"), to: int | None = None,
                      limit: int = Query(500, ge=1, le=2000), p: Principal = Depends(viewer)):
    start, end = time_range(from_, to)
    try:
        frag, qparams = logs_ql.compile_query(q, logs_ql.EventsResolver())
    except QueryError as e:
        raise bad_request(str(e), "invalid_query")
    async with rt().db.reader() as conn:
        rows = await fetch_all(conn, f"""
            SELECT {EVENT_FIELDS}
              FROM events e LEFT JOIN hosts h ON h.id = e.host_id AND h.org_id = e.org_id
             WHERE e.org_id = %s AND e.ts >= %s AND e.ts < %s AND {frag}
             ORDER BY e.ts DESC, e.id DESC LIMIT %s
        """, [p.org_id, ms_to_dt(start), ms_to_dt(end)] + qparams + [limit])
    return ok(rows)


class EventIn(BaseModel):
    title: str = Field(min_length=1, max_length=500)
    text: str | None = Field(None, max_length=8000)
    ts: int | None = None
    alert_type: str = "info"
    priority: str = "normal"
    tags: dict = Field(default_factory=dict)
    aggregation_key: str | None = Field(None, max_length=200)
    source: str | None = Field(None, max_length=50)
    type: str | None = Field(None, max_length=100)
    host: str | None = Field(None, max_length=255)


@router.post("/events")
async def create_event(body: EventIn, request: Request, p: Principal = Depends(member)):
    ts = body.ts if body.ts is not None else now_ms()
    if not Window.around().ok(ts):
        raise bad_request("ts hors fenêtre acceptée (14 jours passés, 10 minutes à venir)")
    async with rt().db.writer() as conn:
        host_id = None
        if body.host:
            row = await fetch_one(conn, "SELECT id FROM hosts WHERE org_id = %s AND hostname = %s "
                                        "ORDER BY last_seen DESC LIMIT 1", (p.org_id, body.host))
            host_id = row["id"] if row else None
        values = event_row(p.org_id, host_id, body.model_dump(), ts, "user" if p.session_id else "api")
        placeholders = ", ".join("%s::jsonb" if c == "tags" else "%s" for c in EVENT_COLUMNS)
        created = await fetch_one(conn, f"INSERT INTO events ({', '.join(EVENT_COLUMNS)}) VALUES ({placeholders}) "
                                        "RETURNING id, ts", values)
        await audit.record(conn, org_id=p.org_id, principal=p, action="create", object_type="event",
                           object_id=created["id"], summary=f"événement {values[3]} « {values[5]} »",
                           after={"title": values[5], "source": values[3], "type": values[4],
                                  "tags": json.loads(values[9])}, request=request)
    return ok({"id": created["id"], "ts": created["ts"], "host_id": host_id, "hostname": body.host if host_id else None,
               "source": values[3], "type": values[4], "title": values[5], "text": values[6],
               "alert_type": values[7], "priority": values[8], "tags": json.loads(values[9]),
               "aggregation_key": values[10]}, 201)
