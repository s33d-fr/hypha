"""Journaux (contrat § 7) : recherche paginée, facettes, histogramme, suivi en direct."""
import base64
import json
from datetime import datetime, timedelta

from fastapi import APIRouter, Depends, Query

from .. import retention
from ..auth.principal import Principal, viewer
from ..db import fetch_all
from ..errors import QueryError, bad_request
from ..query import logs_ql
from ..runtime import rt
from ..util import EPOCH_2000, bucket_floor, dt_to_ms, ms_to_dt, nice_step
from .common import ok, time_range

router = APIRouter()

LOG_FIELDS = "id, ts, host_id, hostname, service, source, status, message, attributes, tags, trace_id"
FACET_SAMPLE = 100_000     # les facettes d'une plage énorme se comptent sur les lignes les plus récentes
FACET_TOP = 25


def compile_or_400(q: str | None) -> tuple[str, list]:
    try:
        return logs_ql.compile_query(q)
    except QueryError as e:
        raise bad_request(str(e), "invalid_query")


def _encode_cursor(ts: datetime, row_id: int) -> str:
    return base64.urlsafe_b64encode(json.dumps([ts.isoformat(), row_id]).encode()).decode().rstrip("=")


def _decode_cursor(cursor: str) -> tuple[datetime, int]:
    try:
        ts, row_id = json.loads(base64.urlsafe_b64decode(cursor + "=" * (-len(cursor) % 4)))
        return datetime.fromisoformat(ts), int(row_id)
    except (ValueError, TypeError):
        raise bad_request("curseur invalide")


@router.get("/logs/search")
async def search(q: str = "", from_: int | None = Query(None, alias="from"), to: int | None = None,
                 limit: int = Query(100, ge=1, le=1000), cursor: str = "", p: Principal = Depends(viewer)):
    start, end, rel = await retention.window(p.org_id, "logs", *time_range(from_, to))
    frag, qparams = compile_or_400(q)
    params: list = [p.org_id, ms_to_dt(start), ms_to_dt(end)] + qparams
    page = ""
    if cursor:
        cts, cid = _decode_cursor(cursor)
        page = "AND (ts, id) < (%s, %s)"
        params += [cts, cid]
    params.append(limit + 1)
    async with rt().db.reader() as conn:
        rows = await fetch_all(conn, f"""
            SELECT {LOG_FIELDS} FROM {rel}
             WHERE org_id = %s AND ts >= %s AND ts < %s AND {frag} {page}
             ORDER BY ts DESC, id DESC LIMIT %s
        """, params)
    next_cursor = None
    if len(rows) > limit:
        rows = rows[:limit]
        next_cursor = _encode_cursor(rows[-1]["ts"], rows[-1]["id"])
    return ok({"logs": rows, "next_cursor": next_cursor})


def facet_attrs(attrs: str) -> list[tuple[str, list[str]]]:
    """`attrs=http.status_code,http.method` : chemins d'attributs validés (10 au plus)."""
    out = []
    for name in (a.strip().removeprefix("@") for a in (attrs or "").split(",")):
        if not name or any(name == n for n, _ in out):
            continue
        try:
            out.append((name, logs_ql.attribute_path(name)))
        except QueryError as e:
            raise bad_request(str(e), "invalid_query")
    if len(out) > 10:
        raise bad_request("10 facettes d'attribut au plus")
    return out


@router.get("/logs/facets")
async def facets(q: str = "", from_: int | None = Query(None, alias="from"), to: int | None = None,
                 attrs: str = "", p: Principal = Depends(viewer)):
    start, end, rel = await retention.window(p.org_id, "logs", *time_range(from_, to))
    frag, qparams = compile_or_400(q)
    extra = facet_attrs(attrs)
    # Un attribut se lit imbriqué ({"http":{"status_code":500}}) ou à plat ({"http.status_code":500}, OTLP).
    cols = "".join(f", coalesce(attributes #>> %s::text[], attributes ->> %s) AS a{i}" for i in range(len(extra)))
    outer = "".join(f", a{i}, GROUPING(a{i}) AS g_a{i}" for i in range(len(extra)))
    sets = "".join(f", (a{i})" for i in range(len(extra)))
    params = [x for _, path in extra for x in (path, ".".join(path))]
    params += [p.org_id, ms_to_dt(start), ms_to_dt(end)] + qparams + [FACET_SAMPLE]
    async with rt().db.reader() as conn:
        rows = await fetch_all(conn, f"""
            WITH m AS (
                SELECT status, service, hostname, source{cols} FROM {rel}
                 WHERE org_id = %s AND ts >= %s AND ts < %s AND {frag}
                 ORDER BY ts DESC LIMIT %s
            )
            SELECT status, service, hostname, source{outer}, count(*) AS n,
                   GROUPING(status) AS g_status, GROUPING(service) AS g_service,
                   GROUPING(hostname) AS g_host, GROUPING(source) AS g_source
              FROM m GROUP BY GROUPING SETS ((status), (service), (hostname), (source){sets})
        """, params)
    out: dict[str, list] = {"status": [], "service": [], "host": [], "source": []}
    for name, _ in extra:
        out["@" + name] = []
    for r in rows:
        for facet, column, flag in (("status", "status", "g_status"), ("service", "service", "g_service"),
                                    ("host", "hostname", "g_host"), ("source", "source", "g_source")):
            if r[flag] == 0:
                out[facet].append({"value": r[column], "count": r["n"]})
        for i, (name, _) in enumerate(extra):
            # Lignes sans l'attribut : pas une valeur de facette (elles écraseraient les autres).
            if r[f"g_a{i}"] == 0 and r[f"a{i}"] is not None:
                out["@" + name].append({"value": r[f"a{i}"], "count": r["n"]})
    for facet in out:
        out[facet] = sorted(out[facet], key=lambda x: -x["count"])[:FACET_TOP]
    return ok(out)


@router.get("/logs/histogram")
async def histogram(q: str = "", from_: int | None = Query(None, alias="from"), to: int | None = None,
                    buckets: int = Query(60, ge=1, le=1000), p: Principal = Depends(viewer)):
    start, end, rel = await retention.window(p.org_id, "logs", *time_range(from_, to))
    frag, qparams = compile_or_400(q)
    step = nice_step((end - start) / 1000 / buckets)
    first = bucket_floor(start, step)
    params = [timedelta(seconds=step), EPOCH_2000, p.org_id, ms_to_dt(start), ms_to_dt(end)] + qparams
    async with rt().db.reader() as conn:
        rows = await fetch_all(conn, f"""
            SELECT date_bin(%s::interval, ts, %s::timestamptz) AS b, status, count(*) AS n
              FROM {rel} WHERE org_id = %s AND ts >= %s AND ts < %s AND {frag}
             GROUP BY 1, 2
        """, params)
    series = {b: {} for b in range(first, end, step * 1000)}
    for r in rows:
        bucket = series.get(dt_to_ms(r["b"]))
        if bucket is not None:
            bucket[r["status"]] = r["n"]
    return ok({"interval_s": step, "buckets": [[b, counts] for b, counts in series.items()]})


@router.get("/logs/tail")
async def tail(q: str = "", after_id: int | None = None, p: Principal = Depends(viewer)):
    frag, qparams = compile_or_400(q)
    # Fenêtre de 10 minutes : le suivi en direct ne lit que les partitions du jour.
    base = f"SELECT {LOG_FIELDS} FROM logs WHERE org_id = %s AND ts > now() - interval '10 minutes' AND {frag}"
    async with rt().db.reader() as conn:
        if after_id is None:
            rows = await fetch_all(conn, base + " ORDER BY ts DESC, id DESC LIMIT 100", [p.org_id] + qparams)
            rows.sort(key=lambda r: r["id"])
        else:
            rows = await fetch_all(conn, base + " AND id > %s ORDER BY id LIMIT 500",
                                   [p.org_id] + qparams + [after_id])
    last_id = max((r["id"] for r in rows), default=after_id)
    return ok({"logs": rows, "last_id": last_id})
