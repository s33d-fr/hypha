"""Lecture de l'état d'un SLO en base (API `/slos` et moniteur de type `slo`). Chaque requête
filtre l'organisation, y compris celles qui suivent un moniteur ou des séries déjà vérifiés."""
import time
from datetime import timedelta

from ..db import fetch_all, fetch_one
from ..errors import QueryError
from ..query import metrics_ql as mql
from ..util import EPOCH_2000, dt_to_ms, ms_to_dt, real_value
from . import compute as C

FIELDS = """id, name, description, type, monitor_id, good_query, total_query, target, warning, timeframe, tags,
            created_by, created_at, updated_at"""
MAX_SERIES = 5000


async def load_slo(conn, org_id: int, slo_id: int) -> dict | None:
    return await fetch_one(conn, f"SELECT org_id, {FIELDS} FROM slos WHERE org_id = %s AND id = %s", (org_id, slo_id))


def _changes(rows) -> list[C.Change]:
    return [C.Change(r["ts"].timestamp(), r["group_key"], r["from_state"], r["to_state"]) for r in rows]


async def _monitor_figures(conn, slo: dict, win_start: float, end: float, step: float):
    org = slo["org_id"]
    mon = await fetch_one(conn, "SELECT id, created_at, no_data_s FROM monitors WHERE org_id = %s AND id = %s",
                          (org, slo["monitor_id"]))
    if mon is None:
        return 100.0, 100.0, 100.0, []
    start = max(win_start, mon["created_at"].timestamp())
    if start >= end:
        return 100.0, 100.0, 100.0, []
    before = await fetch_all(conn, """
        SELECT DISTINCT ON (group_key) ts, group_key, from_state, to_state FROM alert_events
         WHERE org_id = %s AND monitor_id = %s AND ts < %s ORDER BY group_key, ts DESC, id DESC
    """, (org, mon["id"], ms_to_dt(start * 1000)))
    inside = await fetch_all(conn, """
        SELECT ts, group_key, from_state, to_state FROM alert_events
         WHERE org_id = %s AND monitor_id = %s AND ts >= %s AND ts <= %s ORDER BY ts, id
    """, (org, mon["id"], ms_to_dt(start * 1000), ms_to_dt(end * 1000)))
    current = {r["group_key"]: r["state"] for r in await fetch_all(conn, """
        SELECT ms.group_key, ms.state FROM monitor_states ms JOIN monitors m ON m.id = ms.monitor_id AND m.org_id = %s
         WHERE ms.monitor_id = %s
    """, (org, mon["id"]))}
    merged = C.merge(C.alert_intervals(_changes(before), _changes(inside), current, start, end, mon["no_data_s"]))
    return C.monitor_figures(merged, start, end, step)


async def _series_ids(conn, org_id: int, query: str) -> list[int]:
    try:
        mq = mql.parse(query)
    except QueryError as e:
        raise QueryError(f"« {query} » : {e}")
    sql, params = mql.series_sql(mq, org_id, MAX_SERIES + 1)
    ids = [r["id"] for r in await fetch_all(conn, sql, params)]
    if len(ids) > MAX_SERIES:
        raise QueryError(f"« {query} » vise plus de {MAX_SERIES} séries")
    return ids


async def _bucket_sums(conn, ids: list[int], source: mql.Source, step_s: int, start_ms: int, end_ms: int) -> dict:
    if not ids:
        return {}
    expr = "sum(sum)" if source.rollup else "sum(value)"
    rows = await fetch_all(conn, f"""
        SELECT date_bin(%s::interval, ts, %s::timestamptz) AS b, {expr}::float8 AS v
          FROM {source.table} WHERE series_id = ANY(%s::bigint[]) AND ts >= %s AND ts < %s GROUP BY 1
    """, (timedelta(seconds=step_s), EPOCH_2000, ids, ms_to_dt(start_ms), ms_to_dt(end_ms)))
    return {dt_to_ms(r["b"]): r["v"] or 0.0 for r in rows}


async def _raw_sum(conn, ids: list[int], start_ms: int, end_ms: int) -> float:
    if not ids:
        return 0.0
    row = await fetch_one(conn, """
        SELECT coalesce(sum(value), 0)::float8 AS v FROM metric_points
         WHERE series_id = ANY(%s::bigint[]) AND ts >= %s AND ts < %s
    """, (ids, ms_to_dt(start_ms), ms_to_dt(end_ms)))
    return row["v"]


async def _metric_figures(conn, slo: dict, win_start: float, end: float, step: int):
    org = slo["org_id"]
    good_ids = await _series_ids(conn, org, slo["good_query"])
    total_ids = await _series_ids(conn, org, slo["total_query"])
    start_ms, end_ms = int(win_start * 1000), int(end * 1000)
    # Agrégats 5 min jusqu'à 60 jours, 1 h au-delà (contrat § 13) ; les dernières minutes, pas encore
    # agrégées, manquent des deux côtés à la fois : le rapport reste juste.
    source = mql.R5M if C.TIMEFRAME_S[slo["timeframe"]] <= 60 * 86400 else mql.R1H
    good = await _bucket_sums(conn, good_ids, source, step, start_ms, end_ms)
    total = await _bucket_sums(conn, total_ids, source, step, start_ms, end_ms)
    sli = C.ratio_sli(sum(good.values()), sum(total.values()))
    # Vitesses : points bruts, frais à la seconde près.
    windows = []
    for span in (3600, 6 * 3600):
        a = max(start_ms, end_ms - span * 1000)
        windows.append(C.ratio_sli(await _raw_sum(conn, good_ids, a, end_ms), await _raw_sum(conn, total_ids, a, end_ms)))
    return sli, windows[0], windows[1], C.ratio_history(good, total, start_ms, end_ms, step)


async def slo_status(conn, slo: dict, with_history: bool = True, now: float | None = None) -> dict:
    end = time.time() if now is None else now
    win_start = end - C.TIMEFRAME_S[slo["timeframe"]]
    step = C.HISTORY_STEP_S[slo["timeframe"]]
    if slo["type"] == "monitor":
        sli, sli_1h, sli_6h, history = await _monitor_figures(conn, slo, win_start, end, step)
    else:
        sli, sli_1h, sli_6h, history = await _metric_figures(conn, slo, win_start, end, step)
    out = C.summarize(sli, sli_1h, sli_6h, real_value(slo["target"]), real_value(slo["warning"]))
    if with_history:
        out["history"] = history
    return out
