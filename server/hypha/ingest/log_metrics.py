"""Métriques tirées des journaux (contrat § 23), calculées à l'ingestion.

Chaque ligne gardée (après filtres et masquage) qui correspond à la requête d'une métrique de
l'organisation incrémente `name` (agrégation `count`) ou alimente la distribution d'un attribut
numérique (`name.avg .p50 .p95 .p99 .max .count`), par groupe `group_by`. Les valeurs sont
agrégées en mémoire par seau de 15 s (horodatage de la ligne) et écrites comme des séries
ordinaires, de type `count` ou `distribution`, quand le seau est clos.

Chaque processus d'API écrit ses propres points : plusieurs points au même instant pour une
même série sont normaux. Un compte se lit donc en `sum` (`sum:logs.x{*}.rollup(sum)`) ; les
quantiles d'une distribution sont ceux de chaque processus (approximation assumée).
"""
import json
import random
import re
from dataclasses import dataclass, field

from ..errors import QueryError
from ..query import logs_group, logs_match, logs_ql
from .body import LOG_COLUMNS
from .log_filters import LogView

BUCKET_MS = 15_000
CLOSE_DELAY_MS = 5_000          # un seau s'écrit 5 s après sa fin (lignes de l'agent en retard)
MAX_GROUPS = 20_000             # groupes (métrique × étiquettes × seau) gardés en mémoire au plus
RESERVOIR = 2_000               # valeurs gardées par seau de distribution pour les quantiles
DIST_SUFFIXES = ("avg", "p50", "p95", "p99", "max", "count")
_I_ATTRS = LOG_COLUMNS.index("attributes")
_NUMERIC = re.compile(r"-?\d+(?:\.\d+)?")


@dataclass(frozen=True)
class CompiledMetric:
    id: int
    name: str
    predicate: object
    keys: tuple
    distribution: bool
    path: tuple = ()
    flat: str = ""
    needles: tuple = ()          # textes qui doivent figurer dans le JSON brut des attributs


def attribute_needles(node) -> tuple:
    """Valeurs d'attribut exigées par la requête (ET de premier niveau, ni niées ni à joker) : une ligne
    dont le JSON brut des attributs ne les contient pas est écartée sans décodage. Les valeurs
    numériques n'en font pas partie (502 s'écrit aussi 502.0) ni celles qui s'échappent en JSON."""
    terms = node.nodes if isinstance(node, logs_ql.And) else [node] if node is not None else []
    out = []
    for t in terms:
        if not isinstance(t, logs_ql.Term) or not t.field or not t.field.startswith("@") or t.wildcard:
            continue
        v = t.value
        if not v or _NUMERIC.fullmatch(v) or v in ("true", "false") or json.dumps(v, ensure_ascii=False)[1:-1] != v:
            continue
        out.append(v)
    return tuple(out)


def compile_metric(row: dict) -> CompiledMetric:
    """Métrique d'après sa ligne `log_metrics` ; lève QueryError si la définition est inutilisable."""
    query = (row.get("query") or "").strip()
    node = logs_ql.parse(query) if query else None
    logs_ql.to_sql(node)
    keys = tuple(logs_group.parse_keys(list(row.get("group_by") or [])))
    distribution = row.get("aggregation") == "distribution"
    path, flat = (), ""
    if distribution:
        attr = (row.get("attribute") or "").strip().removeprefix("@")
        if not attr:
            raise QueryError("attribute est obligatoire pour une distribution")
        path, flat = tuple(logs_ql.attribute_path(attr)), attr
    return CompiledMetric(row["id"], row["name"], logs_match.compile_node(node), keys, distribution, path, flat,
                          attribute_needles(node))


@dataclass
class Dist:
    count: int = 0
    total: float = 0.0
    vmax: float = float("-inf")
    sample: list = field(default_factory=list)

    def add(self, v: float) -> None:
        self.count += 1
        self.total += v
        if v > self.vmax:
            self.vmax = v
        if len(self.sample) < RESERVOIR:
            self.sample.append(v)
        else:
            # Échantillon uniforme (algorithme R) : les quantiles restent justes en moyenne.
            j = random.randrange(self.count)
            if j < RESERVOIR:
                self.sample[j] = v


def quantile(sorted_values: list[float], q: float) -> float:
    """Quantile par interpolation linéaire (comme percentile_cont)."""
    if not sorted_values:
        return float("nan")
    pos = (len(sorted_values) - 1) * q
    lo = int(pos)
    hi = min(lo + 1, len(sorted_values) - 1)
    return sorted_values[lo] + (sorted_values[hi] - sorted_values[lo]) * (pos - lo)


class Aggregator:
    """Seaux en mémoire : (org, nom, distribution?, étiquettes, début du seau) → compte ou Dist."""

    def __init__(self):
        self.buckets: dict[tuple, object] = {}
        self.overflow = 0

    def observe(self, org_id: int, metrics: list[CompiledMetric], rows: list[tuple]) -> None:
        if not metrics or not rows:
            return
        for row in rows:
            raw = row[_I_ATTRS] or ""
            line = None
            bucket = None
            for m in metrics:
                if m.needles and not all(n in raw for n in m.needles):
                    continue
                if line is None:
                    line = LogView(row)
                if not m.predicate(line):
                    continue
                if bucket is None:
                    bucket = line.ts_ms // BUCKET_MS * BUCKET_MS
                tags = tuple((k.tag, v if (v := logs_group.line_value(k, line)) is not None else logs_group.MISSING)
                             for k in m.keys)
                key = (org_id, m.name, m.distribution, tags, bucket)
                if m.distribution:
                    v = logs_group.line_number(m.path, m.flat, line)
                    if v is None:
                        continue
                    d = self.buckets.get(key)
                    if d is None:
                        if len(self.buckets) >= MAX_GROUPS:
                            self.overflow += 1
                            continue
                        d = self.buckets[key] = Dist()
                    d.add(v)
                else:
                    n = self.buckets.get(key)
                    if n is None and len(self.buckets) >= MAX_GROUPS:
                        self.overflow += 1
                        continue
                    self.buckets[key] = (n or 0) + 1

    def take_closed(self, now_ms: int) -> list[tuple]:
        """Retire les seaux clos et rend [(org, nom de série, type, étiquettes, ts_ms, valeur)]."""
        limit = now_ms - BUCKET_MS - CLOSE_DELAY_MS
        closed = [k for k in self.buckets if k[4] <= limit]
        out = []
        for k in closed:
            org_id, name, distribution, tags, bucket = k
            value = self.buckets.pop(k)
            tag_dict = dict(tags)
            if not distribution:
                out.append((org_id, name, "count", tag_dict, bucket, float(value)))
                continue
            values = sorted(value.sample)
            stats = {"avg": value.total / value.count, "p50": quantile(values, 0.5), "p95": quantile(values, 0.95),
                     "p99": quantile(values, 0.99), "max": value.vmax, "count": float(value.count)}
            for suffix in DIST_SUFFIXES:
                out.append((org_id, f"{name}.{suffix}", "distribution", tag_dict, bucket, stats[suffix]))
        return out

    def restore(self, items: list[tuple]) -> None:
        """Remet en attente des comptes non écrits (base indisponible) ; les distributions sont perdues."""
        for org_id, name, mtype, tags, bucket, value in items:
            if mtype == "count":
                key = (org_id, name, False, tuple(tags.items()), bucket)
                self.buckets[key] = self.buckets.get(key, 0) + int(value)


LOG_METRICS_SQL = """
    SELECT id, name, query, group_by, aggregation, attribute FROM log_metrics
     WHERE org_id = %s AND enabled ORDER BY id
"""
