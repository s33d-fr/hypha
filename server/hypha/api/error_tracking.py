"""Suivi des erreurs (contrat § 25) : problèmes regroupés par empreinte, détail, changement de statut.

`viewer` lit, `member` change le statut. Les occurrences d'un problème se retrouvent par
`error.fingerprint`, posé à l'ingestion dans les attributs de la ligne, du span ou de l'événement RUM.
"""
import json
import math
from datetime import timedelta

from fastapi import APIRouter, Body, Depends, Query, Request

from .. import audit
from ..auth.principal import Principal, member, viewer
from ..db import fetch_all, fetch_one
from ..errors import bad_request, not_found
from ..runtime import rt
from ..util import dt_to_ms, like_escape, ms_to_dt
from .common import ok, time_range

router = APIRouter()

STATUSES = ("open", "ignored", "resolved")
SORTS = {"last_seen": "last_seen DESC", "count": "count DESC", "first_seen": "first_seen DESC",
         "count_in_range": "count_in_range DESC"}
ISSUE_FIELDS = ("i.id, i.origin, i.service, i.error_type, i.message, i.first_seen, i.last_seen, i.count, i.status, "
                "i.regressed_at, i.resolved_at, i.last_trace_id, i.last_log_id, i.last_host_id")
OCCURRENCE_WINDOW = timedelta(days=7)


def fingerprint_docs(fp: str) -> tuple[str, str]:
    return (json.dumps({"error": {"fingerprint": fp}}), json.dumps({"error.fingerprint": fp}))


async def histogram(conn, ids: list[int], start, end) -> dict[int, list]:
    rows = await fetch_all(conn, """
        SELECT issue_id, hour, count FROM error_issue_hits
         WHERE issue_id = ANY(%s::bigint[]) AND hour >= date_trunc('hour', %s::timestamptz) AND hour < %s
         ORDER BY issue_id, hour
    """, (ids, start, end))
    out: dict[int, list] = {i: [] for i in ids}
    for r in rows:
        out[r["issue_id"]].append([dt_to_ms(r["hour"]), r["count"]])
    return out


def compact_histogram(points: list, start, end, max_points: int = 24) -> list:
    """Comptes horaires replacés sur `max_points` seaux au plus (une ou plusieurs heures chacun),
    calés sur l'heure de fin, seaux vides compris : la mini-courbe garde une largeur fixe."""
    hours = max(1, math.ceil((end - start).total_seconds() / 3600))
    width = max(1, math.ceil(hours / max_points))
    n = math.ceil(hours / width)
    step_ms = width * 3_600_000
    last_ms = dt_to_ms((end - timedelta(milliseconds=1)).replace(minute=0, second=0, microsecond=0))
    first_ms = last_ms - (n - 1) * step_ms - (width - 1) * 3_600_000
    out = [[first_ms + i * step_ms, 0] for i in range(n)]
    for ts, count in points:
        i = (ts - first_ms) // step_ms
        if 0 <= i < n:
            out[i][1] += count
    return out


@router.get("/errors")
async def list_errors(status: str = "", service: str = "", q: str = "", from_: int | None = Query(None, alias="from"),
                      to: int | None = None, sort: str = "last_seen", limit: int = Query(100, ge=1, le=1000),
                      p: Principal = Depends(viewer)):
    start, end = time_range(from_, to, default_s=86400)
    if sort not in SORTS:
        raise bad_request(f"sort attendu : {'|'.join(SORTS)}")
    s, e = ms_to_dt(start), ms_to_dt(end)
    where, params = ["i.org_id = %s", "i.last_seen >= %s", "i.first_seen < %s"], [p.org_id, s, e]
    if status:
        if status not in STATUSES:
            raise bad_request(f"status attendu : {'|'.join(STATUSES)}")
        where.append("i.status = %s")
        params.append(status)
    if service:
        where.append("i.service = %s")
        params.append(service)
    if q.strip():
        pattern = "%" + like_escape(q.strip()) + "%"
        where.append("(i.message ILIKE %s OR i.error_type ILIKE %s OR i.service ILIKE %s)")
        params += [pattern] * 3
    order = SORTS[sort]
    async with rt().db.reader() as conn:
        rows = await fetch_all(conn, f"""
            WITH page AS (
                SELECT {ISSUE_FIELDS},
                       coalesce((SELECT sum(c.count) FROM error_issue_hits c
                                  WHERE c.issue_id = i.id AND c.hour >= date_trunc('hour', %s::timestamptz)
                                    AND c.hour < %s), 0)::bigint AS count_in_range
                  FROM error_issues i
                 WHERE {' AND '.join(where)}
                 ORDER BY {'i.' + order if sort != 'count_in_range' else order}
                 LIMIT %s
            )
            SELECT page.*, h.hostname AS last_host
              FROM page LEFT JOIN hosts h ON h.id = page.last_host_id AND h.org_id = %s
             ORDER BY page.{order}
        """, [s, e] + params + [limit, p.org_id])
        hist = await histogram(conn, [r["id"] for r in rows], s, e) if rows else {}
    for r in rows:
        r["hosts"] = [r["last_host"]] if r["last_host"] else []
        r["histogram"] = compact_histogram(hist.get(r["id"], []), s, e)
        r["new"] = r["first_seen"] >= s
    return ok(rows)


async def load_issue(conn, org_id: int, issue_id: int) -> dict:
    row = await fetch_one(conn, f"""
        SELECT {ISSUE_FIELDS}, i.fingerprint, i.stack, h.hostname AS last_host
          FROM error_issues i LEFT JOIN hosts h ON h.id = i.last_host_id AND h.org_id = i.org_id
         WHERE i.org_id = %s AND i.id = %s
    """, (org_id, issue_id))
    if row is None:
        raise not_found("problème introuvable")
    return row


async def occurrences(conn, org_id: int, issue: dict, limit: int = 10) -> list[dict]:
    """Dernières occurrences (ligne, span ou événement RUM) d'un problème, la plus récente d'abord."""
    lo, hi = issue["last_seen"] - OCCURRENCE_WINDOW, issue["last_seen"] + timedelta(seconds=1)
    nested, flat = fingerprint_docs(issue["fingerprint"])
    if issue["origin"] == "log":
        return await fetch_all(conn, """
            SELECT 'log' AS kind, id AS log_id, ts, host_id, hostname, service, status, left(message, 2000) AS message,
                   trace_id
              FROM logs WHERE org_id = %s AND ts >= %s AND ts <= %s AND (attributes @> %s::jsonb OR attributes @> %s::jsonb)
             ORDER BY ts DESC LIMIT %s
        """, (org_id, lo, hi, nested, flat, limit))
    if issue["origin"] == "trace":
        return await fetch_all(conn, """
            SELECT 'trace' AS kind, trace_id, span_id, ts, host_id, hostname, service, name, resource,
                   duration_ns::float8 / 1e6 AS duration_ms, status_message AS message
              FROM spans WHERE org_id = %s AND service IS NOT DISTINCT FROM %s AND status = 'error'
               AND ts >= %s AND ts <= %s AND attributes @> %s::jsonb
             ORDER BY ts DESC LIMIT %s
        """, (org_id, issue["service"], lo, hi, flat, limit))
    return await fetch_all(conn, """
        SELECT 'rum' AS kind, ts, app_id, session_id, view_url, name AS message, user_agent, country
          FROM rum_events WHERE org_id = %s AND event_type = 'error' AND ts >= %s AND ts <= %s
           AND attributes @> %s::jsonb
         ORDER BY ts DESC LIMIT %s
    """, (org_id, lo, hi, flat, limit))


@router.get("/errors/{issue_id}")
async def get_error(issue_id: int, from_: int | None = Query(None, alias="from"), to: int | None = None,
                    p: Principal = Depends(viewer)):
    start, end = time_range(from_, to, default_s=7 * 86400)
    async with rt().db.reader() as conn:
        issue = await load_issue(conn, p.org_id, issue_id)
        hist = await histogram(conn, [issue_id], ms_to_dt(start), ms_to_dt(end))
        last = await occurrences(conn, p.org_id, issue)
    hosts = sorted({o["hostname"] for o in last if o.get("hostname")} | ({issue["last_host"]} if issue["last_host"] else set()))
    return ok({**issue, "histogram": hist.get(issue_id, []), "occurrences": last, "hosts": hosts,
               "count_in_range": sum(n for _, n in hist.get(issue_id, []))})


@router.put("/errors/{issue_id}")
async def update_error(issue_id: int, request: Request, data: dict = Body(...), p: Principal = Depends(member)):
    status = data.get("status")
    if status not in STATUSES:
        raise bad_request(f"status attendu : {'|'.join(STATUSES)}")
    async with rt().db.writer() as conn:
        before = await load_issue(conn, p.org_id, issue_id)
        await conn.execute("""
            UPDATE error_issues SET status = %s,
                   resolved_at = CASE WHEN %s = 'resolved' THEN now() WHEN %s = 'open' THEN NULL ELSE resolved_at END
             WHERE org_id = %s AND id = %s
        """, (status, status, status, p.org_id, issue_id))
        await audit.record(conn, org_id=p.org_id, principal=p, action="update", object_type="error_issue",
                           object_id=issue_id, summary=f"problème {before['error_type'] or before['message'][:60]} : "
                                                       f"{status}",
                           before={"status": before["status"]}, after={"status": status}, request=request)
        issue = await load_issue(conn, p.org_id, issue_id)
    return ok(issue)
