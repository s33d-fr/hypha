"""Exécution d'une requête de métriques sur la base (API `/metrics/query` et moniteurs `metric`)."""
from ..db import fetch_all
from ..errors import QueryError
from ..util import bucket_floor, dt_to_ms, ms_to_dt
from . import metrics_ql as mql

MAX_GROUPS = 500
MAX_SERIES = 5000


async def execute(conn, org_id: int, mq: mql.MetricQuery, from_ms: int, to_ms: int,
                  source: mql.Source, step_s: int) -> list[dict]:
    """Rend [{group, unit, points:[[ts, v|None], …]}] ; un seau sans donnée vaut None."""
    sql, params = mql.series_sql(mq, org_id, MAX_SERIES + 1)
    rows = await fetch_all(conn, sql, params)
    if not rows:
        return []
    if len(rows) > MAX_SERIES:
        raise QueryError(f"plus de {MAX_SERIES} séries : ajoutez des filtres")
    groups, ids, idx, units = mql.group_series(rows, mq.group_by)
    if len(groups) > MAX_GROUPS:
        raise QueryError(f"plus de {MAX_GROUPS} groupes : réduisez le « by »")
    first = bucket_floor(from_ms, step_s)
    step_ms = step_s * 1000
    buckets = list(range(first, to_ms, step_ms)) or [first]
    position = {b: i for i, b in enumerate(buckets)}
    sql, params = mql.points_sql(source, mq.rollup_fn or "avg", mq.space_agg, ids, idx, step_s,
                                 ms_to_dt(first), ms_to_dt(to_ms))
    values: dict[int, list] = {}
    for r in await fetch_all(conn, sql, params):
        i = position.get(dt_to_ms(r["bucket"]))
        if i is None:
            continue
        values.setdefault(r["gi"], [None] * len(buckets))[i] = r["v"]
    out = []
    for gi in sorted(values, key=lambda g: tuple(str(v) for v in groups[g].values())):
        out.append({"group": groups[gi], "unit": units[gi],
                    "points": [[b, v] for b, v in zip(buckets, values[gi])]})
    return out


def window_aggregate(points: list, how: str) -> float | None:
    """Agrégat dans le temps d'une série sur la fenêtre d'un moniteur (seaux vides ignorés)."""
    vals = [v for _, v in points if v is not None]
    if not vals:
        return None
    if how == "max":
        return max(vals)
    if how == "min":
        return min(vals)
    if how == "sum":
        return sum(vals)
    if how == "last":
        return vals[-1]
    return sum(vals) / len(vals)
