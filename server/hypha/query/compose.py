"""Exécution de `POST /metrics/query` (contrat § 7, § 30) : requêtes, fonctions et formules.

Une requête sans fonction garde le chemin et le format d'avant la 0.4.0. Une requête enveloppée de
fonctions, et toute formule, se calcule sur la grille de la requête (même intervalle pour toutes) :
chaque série lue est replacée sur cette grille, un point d'intervalle plus large couvrant les seaux
qu'il recouvre. `timeshift` relit la base sur la plage décalée ; `anomalies(…, 'agile')` relit
les 7 jours précédents aux mêmes créneaux ; `forecast` ajoute des séries `future: true`.
"""
from dataclasses import dataclass

from ..errors import QueryError
from ..util import bucket_floor
from . import formulas as FX
from . import metrics_exec
from . import metrics_ql as mql
from . import series_fn as F

MAX_EXECUTIONS = 60          # lectures de la base par requête HTTP, décalages compris
AGILE_DAYS = 7


@dataclass
class Grid:
    first: int
    step_s: int
    end: int

    @property
    def buckets(self) -> list[int]:
        return list(range(self.first, self.end, self.step_s * 1000)) or [self.first]


class Composer:
    def __init__(self, conn, org_id: int, start: int, end: int, max_points: int, now: int):
        self.conn = conn
        self.org_id = org_id
        self.start, self.end, self.max_points, self.now = start, end, max_points, now
        self.source = mql.choose_source(start, end, now)
        self.interval = mql.choose_interval(start, end, max_points, self.source)
        self.grid = Grid(bucket_floor(start, self.interval), self.interval, end)
        self.buckets = self.grid.buckets
        self.queries: dict[str, object] = {}
        self.cache: dict[tuple, list] = {}
        self.executions = 0

    # ── lecture ────────────────────────────────────────────────────────────────

    def regrid(self, points: list, shift_ms: int, step_s: int) -> list:
        values = [None] * len(self.buckets)
        main_ms = self.interval * 1000
        width = max(step_s * 1000, main_ms)
        for ts, v in points:
            if v is None:
                continue
            t = ts - shift_ms
            i = max(0, -((self.grid.first - t) // main_ms))          # premier seau ≥ t
            while i < len(values) and self.buckets[i] < t + width:
                values[i] = v
                i += 1
        return values

    async def leaf(self, node: FX.Metric, shift_s: int) -> list[FX.Series]:
        key = (FX.canonical(node), shift_s)
        if key in self.cache:
            return self.cache[key]
        self.executions += 1
        if self.executions > MAX_EXECUTIONS:
            raise QueryError(f"requête trop coûteuse : {MAX_EXECUTIONS} lectures au plus (décalages compris)")
        mq = node.mq
        s, e = self.start + shift_s * 1000, self.end + shift_s * 1000
        source = mql.choose_source(s, e, self.now)
        step = mql.choose_interval(s, e, self.max_points, source, mq.rollup_s)
        rows = await metrics_exec.execute(self.conn, self.org_id, mq, s, e, source, step)
        out = [FX.Series(r["group"], self.regrid(r["points"], shift_s * 1000, step), r["unit"], mq.metric)
               for r in rows]
        self.cache[key] = out
        return out

    # ── évaluation ─────────────────────────────────────────────────────────────

    async def eval(self, node, shift_s: int = 0, seen: tuple = ()):
        if isinstance(node, FX.Num):
            return float(node.value)
        if isinstance(node, FX.Str):
            raise QueryError("chaîne inattendue hors d'un argument de fonction")
        if isinstance(node, FX.Ref):
            if node.name in seen:
                raise QueryError(f"référence circulaire : {node.name}")
            target = self.queries.get(node.name)
            if target is None:
                raise QueryError(f"requête inconnue dans la formule : {node.name}")
            return await self.eval(target, shift_s, seen + (node.name,))
        if isinstance(node, FX.Metric):
            return await self.leaf(node, shift_s)
        if isinstance(node, FX.Neg):
            return FX.negate(await self.eval(node.node, shift_s, seen))
        if isinstance(node, FX.BinOp):
            left = await self.eval(node.left, shift_s, seen)
            right = await self.eval(node.right, shift_s, seen)
            return FX.binop(node.op, left, right)
        if isinstance(node, FX.Call):
            return await self.call(node, shift_s, seen)
        raise QueryError("expression invalide")

    async def series_of(self, node, shift_s: int, seen: tuple) -> list[FX.Series]:
        value = await self.eval(node, shift_s, seen)
        if isinstance(value, float):
            raise QueryError("une fonction s'applique à une série, pas à une constante")
        return value

    async def call(self, node: FX.Call, shift_s: int, seen: tuple):
        name, args = node.name, node.args
        if name == "timeshift":
            return await self.series_of(args[0], shift_s + int(args[1].value), seen)
        series = await self.series_of(args[0], shift_s, seen)
        if name == "forecast":
            return self.forecast(series, args[1].value, int(args[2].value))
        if name == "anomalies" and args[1].value == "agile":
            days = [await self.series_of(args[0], shift_s - 86400 * k, seen) for k in range(1, AGILE_DAYS + 1)]
            out = []
            for s in series:
                key = FX._gkey(s.group)
                history = [next((h.values for h in day if FX._gkey(h.group) == key), [None] * len(s.values))
                           for day in days]
                out.append(FX._with_bounds(s, F.anomaly_bounds_agile(history, float(args[2].value))))
            return out
        return FX.apply_simple(name, args[1:], series, self.interval)

    def forecast(self, series: list[FX.Series], algorithm: str, horizon_s: int) -> list[FX.Series]:
        out = list(series)
        for s in series:
            future, bounds = F.forecast(self.buckets, s.values, horizon_s, self.interval, algorithm, self.max_points)
            if future:
                out.append(FX.Series(s.group, [v for _, v in future], s.unit, s.metric,
                                     {"future": True, "future_points": future, "future_bounds": bounds}))
        return out

    # ── rendu ──────────────────────────────────────────────────────────────────

    def render(self, qid: str, series: list[FX.Series], metric: str | None, extra: dict | None = None) -> list[dict]:
        entries = []
        for s in series:
            if s.extra.get("future"):
                entry = {"query": qid, "metric": s.metric or metric, "group": s.group, "unit": s.unit,
                         "future": True, "points": s.extra["future_points"], "bounds": s.extra["future_bounds"]}
            else:
                entry = {"query": qid, "metric": s.metric or metric, "group": s.group, "unit": s.unit,
                         "points": [[b, v] for b, v in zip(self.buckets, s.values)]}
                if "bounds_values" in s.extra:
                    entry["bounds"] = [[b, None, None] if bd is None else [b, bd[0], bd[1]]
                                       for b, bd in zip(self.buckets, s.extra["bounds_values"])]
                    entry["outside"] = s.extra.get("outside", 0)
                if "outlier" in s.extra:
                    entry["outlier"] = s.extra["outlier"]
                    entry["outlier_pct"] = s.extra["outlier_pct"]
            if extra:
                entry.update(extra)
            entries.append(entry)
        return entries


def parse_items(queries: list[tuple[str, str]], formulas: list[tuple[str, str]]) -> tuple[dict, dict]:
    """Arbres des requêtes et des formules ; lève QueryError avec l'id fautif en tête."""
    qnodes, fnodes = {}, {}
    for qid, q in queries:
        try:
            qnodes[qid] = FX.parse_query(q)
        except QueryError as e:
            raise QueryError(f"requête « {qid} » : {e}")
    for fid, text in formulas:
        if fid in qnodes or fid in fnodes:
            raise QueryError(f"identifiant en double : « {fid} »")
        try:
            node = FX.parse_formula(text)
        except QueryError as e:
            raise QueryError(f"formule « {fid} » : {e}")
        unknown = FX.refs(node) - set(qnodes)
        if unknown:
            raise QueryError(f"formule « {fid} » : requête inconnue {', '.join(sorted(unknown))}")
        if not FX.refs(node):
            raise QueryError(f"formule « {fid} » : elle doit citer au moins une requête")
        fnodes[fid] = node
    return qnodes, fnodes


async def run(conn, org_id: int, start: int, end: int, max_points: int, now: int,
              queries: list[tuple[str, str]], formulas: list[tuple[str, str, str | None]]) -> dict:
    """Réponse complète : {"interval_s", "series":[…]} ; requêtes simples au format d'origine."""
    qnodes, fnodes = parse_items(queries, [(f[0], f[1]) for f in formulas])
    comp = Composer(conn, org_id, start, end, max_points, now)
    comp.queries = qnodes
    series = []
    for qid, _ in queries:
        node = qnodes[qid]
        if FX.is_plain(node):
            mq = node.mq
            step = mql.choose_interval(start, end, max_points, comp.source, mq.rollup_s) if mq.rollup_s \
                else comp.interval
            try:
                results = await metrics_exec.execute(conn, org_id, mq, start, end, comp.source, step)
            except QueryError as e:
                raise QueryError(f"requête « {qid} » : {e}")
            for r in results:
                entry = {"query": qid, "metric": mq.metric, "group": r["group"], "unit": r["unit"],
                         "points": r["points"]}
                if step != comp.interval:
                    entry["interval_s"] = step
                series.append(entry)
            # Une formule qui cite cette requête reprend ces séries sans relire la base.
            comp.cache[(FX.canonical(node), 0)] = [FX.Series(r["group"], comp.regrid(r["points"], 0, step),
                                                             r["unit"], mq.metric) for r in results]
            continue
        try:
            value = await comp.eval(node)
        except QueryError as e:
            raise QueryError(f"requête « {qid} » : {e}")
        leaf = FX.leaf_metric(node)
        series += comp.render(qid, value, leaf.metric if leaf else None)
    for fid, text, alias in formulas:
        try:
            value = await comp.eval(fnodes[fid])
        except QueryError as e:
            raise QueryError(f"formule « {fid} » : {e}")
        if isinstance(value, float):
            raise QueryError(f"formule « {fid} » : le résultat n'est pas une série")
        series += comp.render(fid, value, alias or text, {"formula": text, "alias": alias})
    return {"interval_s": comp.interval, "series": series}
