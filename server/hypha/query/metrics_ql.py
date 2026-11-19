"""Langage de requête des métriques (contrat § 7) : analyse et traduction en SQL paramétré.

    <agrégat_espace>:<métrique>{<filtres>} by {<clés>}[.rollup(<fn>, <secondes>)]

Exemples : `avg:system.cpu.user{host:web-01,env:prod} by {host}` ;
`max:system.disk.in_use{!mount:/boot,device:sd*} by {host,mount}.rollup(max, 300)` ;
`avg:system.cpu.usage{host IN (web-01, web-02)}` (valeurs multiples, `NOT IN` ou `!` pour exclure).

Sécurité : aucune valeur saisie n'entre dans le texte SQL. Les noms d'étiquettes sont validés par
expression régulière puis passés eux aussi en paramètre (`tags ->> %s`) ; tables et fonctions
d'agrégat viennent de listes fermées.
"""
import json
import math
import re
from contextvars import ContextVar
from dataclasses import dataclass, field
from datetime import datetime, timedelta

from ..errors import QueryError
from ..util import EPOCH_2000, METRIC_NAME_RE, TAG_KEY_RE, like_escape, nice_step

SPACE_AGGS = ("avg", "sum", "min", "max", "count")
ROLLUP_FNS = ("avg", "sum", "min", "max", "last")
MAX_FILTERS = 30
MAX_GROUP_KEYS = 10

_ROLLUP_RE = re.compile(r"\.rollup\(\s*([a-z]+)\s*(?:,\s*(\d+)\s*)?\)")
_BY_RE = re.compile(r"\bby\s*\{([^{}]*)\}\s*$")
_HEAD_RE = re.compile(r"^([a-z]+)\s*:\s*(.*)$", re.S)
_BODY_RE = re.compile(r"^([A-Za-z0-9_.\-/]+)\s*(?:\{(.*)\})?\s*$", re.S)

DAY_S = 86400
RAW_RETENTION_S = 15 * DAY_S
ROLLUP_5M_RETENTION_S = 92 * DAY_S


@dataclass
class TagFilter:
    key: str
    value: str
    negate: bool = False
    prefix: bool = False
    values: list[str] | None = None     # `clé IN (a, b)` : variable de modèle à valeurs multiples (§ 30)


@dataclass
class MetricQuery:
    space_agg: str
    metric: str
    filters: list[TagFilter] = field(default_factory=list)
    group_by: list[str] = field(default_factory=list)
    rollup_fn: str | None = None
    rollup_s: int | None = None


def _split_items(text: str) -> list[str]:
    """Découpe sur les virgules hors guillemets et hors parenthèses (`clé IN (a, b)`)."""
    items, buf, quoted, escape, depth = [], [], False, False, 0
    for ch in text:
        if escape:
            buf.append(ch)
            escape = False
        elif ch == "\\" and quoted:
            buf.append(ch)
            escape = True
        elif ch == '"':
            quoted = not quoted
            buf.append(ch)
        elif ch in "()" and not quoted:
            depth += 1 if ch == "(" else -1
            buf.append(ch)
        elif ch == "," and not quoted and depth <= 0:
            items.append("".join(buf))
            buf = []
        else:
            buf.append(ch)
    if quoted:
        raise QueryError("guillemet non fermé dans les filtres")
    items.append("".join(buf))
    return items


_IN_RE = re.compile(r"^([a-z0-9_.\-/]{1,100})\s+(NOT\s+)?IN\s*\((.*)\)$", re.I | re.S)


def _parse_in(item: str, negate: bool) -> TagFilter | None:
    m = _IN_RE.match(item)
    if not m:
        return None
    key = m.group(1)
    if not TAG_KEY_RE.match(key):
        raise QueryError(f"nom d'étiquette invalide : « {key} »")
    values = []
    for raw in _split_items(m.group(3)):
        v = raw.strip()
        if len(v) >= 2 and v[0] == '"' and v[-1] == '"':
            v = v[1:-1].replace('\\"', '"').replace("\\\\", "\\")
        if not v or len(v) > 200:
            raise QueryError(f"valeur vide ou trop longue dans « {key} IN (…) »")
        if v not in values:
            values.append(v)
    if not values or len(values) > 100:
        raise QueryError(f"« {key} IN (…) » attend de 1 à 100 valeurs")
    return TagFilter(key, values[0], negate != bool(m.group(2)), False, values)


def _parse_filter(item: str) -> TagFilter | None:
    item = item.strip()
    if item in ("", "*"):
        return None
    if item.startswith("$"):
        raise QueryError(f"variable de modèle non remplacée : {item}")
    negate = item.startswith("!") or item.startswith("-")
    if negate:
        item = item[1:].strip()
    multi = _parse_in(item, negate)
    if multi is not None:
        return multi
    key, sep, value = item.partition(":")
    key, value = key.strip(), value.strip()
    if not sep:
        raise QueryError(f"filtre sans clé : « {item} » (attendu clé:valeur)")
    if not TAG_KEY_RE.match(key):
        raise QueryError(f"nom d'étiquette invalide : « {key} »")
    prefix = False
    if len(value) >= 2 and value[0] == '"' and value[-1] == '"':
        value = value[1:-1].replace('\\"', '"').replace("\\\\", "\\")
    elif value.endswith("*"):
        prefix, value = True, value[:-1]
        if "*" in value:
            raise QueryError(f"joker « * » accepté seulement en fin de valeur : « {item} »")
    elif "*" in value:
        raise QueryError(f"joker « * » accepté seulement en fin de valeur : « {item} »")
    if value == "" and not prefix:
        raise QueryError(f"valeur vide pour l'étiquette « {key} »")
    if len(value) > 200:
        raise QueryError(f"valeur trop longue pour l'étiquette « {key} »")
    return TagFilter(key, value, negate, prefix)


def parse(q: str) -> MetricQuery:
    if not isinstance(q, str) or not q.strip():
        raise QueryError("requête vide")
    text = q.strip()
    if len(text) > 2000:
        raise QueryError("requête trop longue")

    rollup_fn = rollup_s = None
    rollups = list(_ROLLUP_RE.finditer(text))
    if len(rollups) > 1:
        raise QueryError("un seul .rollup() par requête")
    if rollups:
        m = rollups[0]
        rollup_fn = m.group(1)
        if rollup_fn not in ROLLUP_FNS:
            raise QueryError(f"fonction de rollup inconnue : {rollup_fn} (attendu {', '.join(ROLLUP_FNS)})")
        if m.group(2):
            rollup_s = int(m.group(2))
            if not 1 <= rollup_s <= 31 * DAY_S:
                raise QueryError("intervalle de rollup hors bornes (1 s à 31 jours)")
        text = (text[:m.start()] + text[m.end():]).strip()
    elif ".rollup" in text:
        raise QueryError("syntaxe attendue : .rollup(avg|sum|min|max|last, secondes)")

    group_by: list[str] = []
    m = _BY_RE.search(text)
    if m:
        for key in (k.strip() for k in m.group(1).split(",")):
            if not key:
                continue
            if not TAG_KEY_RE.match(key):
                raise QueryError(f"clé de regroupement invalide : « {key} »")
            if key not in group_by:
                group_by.append(key)
        if len(group_by) > MAX_GROUP_KEYS:
            raise QueryError(f"{MAX_GROUP_KEYS} clés de regroupement au plus")
        text = text[:m.start()].strip()
    elif re.search(r"\sby(\s|\{|$)", text):
        raise QueryError("syntaxe attendue : by {clé1,clé2}")

    space_agg = "avg"
    m = _HEAD_RE.match(text)
    if m and m.group(1) in SPACE_AGGS:
        space_agg, text = m.group(1), m.group(2).strip()
    elif m and not METRIC_NAME_RE.match(text.split("{")[0].strip()):
        raise QueryError(f"agrégat inconnu : {m.group(1)} (attendu {', '.join(SPACE_AGGS)})")

    m = _BODY_RE.match(text)
    if not m:
        raise QueryError("syntaxe attendue : agrégat:métrique{filtres} by {clés}")
    metric = m.group(1)
    if not METRIC_NAME_RE.match(metric):
        raise QueryError(f"nom de métrique invalide : « {metric} »")
    filters = [f for f in (_parse_filter(i) for i in _split_items(m.group(2) or "")) if f]
    if len(filters) > MAX_FILTERS:
        raise QueryError(f"{MAX_FILTERS} filtres au plus")
    return MetricQuery(space_agg, metric, filters, group_by, rollup_fn, rollup_s)


def apply_template_vars(q: str, values: dict[str, str]) -> str:
    """`$host` devient `host:<valeur>`, ou disparaît si la valeur est `*` (contrat § Tableaux)."""
    def repl(m):
        name = m.group(1)
        if name not in values:
            return m.group(0)
        value = values[name]
        return "" if value in ("*", "", None) else value
    return re.sub(r"\$([a-z0-9_]+)", repl, q)


# ── choix de la table et de l'intervalle ──────────────────────────────────────

@dataclass(frozen=True)
class Source:
    table: str
    min_step: int
    rollup: bool


RAW = Source("metric_points", 15, False)
R5M = Source("metric_rollup_5m", 300, True)
R1H = Source("metric_rollup_1h", 3600, True)


# Conservation du brut propre à la formule de l'espace (§ 52, Découverte : 1 jour), posée par la route
# le temps de la requête ; None = conservation de la table.
RAW_RETENTION_OVERRIDE: ContextVar[int | None] = ContextVar("raw_retention_override", default=None)


def choose_source(from_ms: int, to_ms: int, now_ms: int) -> Source:
    """Brut si la plage ≤ 2 jours, 5 min si ≤ 60 jours, sinon 1 h (contrat).

    Une plage courte mais ancienne passe à la table suivante quand la table prévue a déjà purgé
    ces jours-là : un zoom sur une heure d'il y a trois semaines lit les agrégats 5 min.
    """
    span_s = (to_ms - from_ms) / 1000
    age_s = (now_ms - from_ms) / 1000
    raw_s = RAW_RETENTION_OVERRIDE.get() or RAW_RETENTION_S
    if span_s <= 2 * DAY_S and age_s <= raw_s:
        return RAW
    if span_s <= 60 * DAY_S and age_s <= ROLLUP_5M_RETENTION_S:
        return R5M
    return R1H


def choose_interval(from_ms: int, to_ms: int, max_points: int, source: Source,
                    rollup_s: int | None = None) -> int:
    span_s = max(1.0, (to_ms - from_ms) / 1000)
    need = max(source.min_step, math.ceil(span_s / max(1, max_points)))
    if rollup_s and rollup_s >= need:
        return rollup_s
    return nice_step(need)


# ── traduction SQL ────────────────────────────────────────────────────────────

def series_sql(mq: MetricQuery, org_id: int, limit: int = 5000) -> tuple[str, list]:
    where = ["org_id = %s", "name = %s"]
    params: list = [org_id, mq.metric]
    for f in mq.filters:
        if f.values:
            # Une inclusion par valeur, réunies par OR : l'index GIN sert chacune.
            parts = []
            for v in f.values:
                parts.append("tags @> %s::jsonb")
                params.append(json.dumps({f.key: v}, ensure_ascii=False))
            cond = "(" + " OR ".join(parts) + ")"
            where.append(f"NOT COALESCE({cond}, false)" if f.negate else cond)
            continue
        if f.prefix:
            cond = "(tags ->> %s) LIKE %s"
            params += [f.key, like_escape(f.value) + "%"]
        else:
            # Égalité par inclusion : l'index GIN jsonb_path_ops de metric_series s'applique.
            cond = "tags @> %s::jsonb"
            params.append(json.dumps({f.key: f.value}, ensure_ascii=False))
        where.append(f"NOT COALESCE({cond}, false)" if f.negate else cond)
    params.append(limit)
    return ("SELECT id, tags, unit FROM metric_series WHERE " + " AND ".join(where) +
            " ORDER BY id LIMIT %s"), params


_TIME_RAW = {
    "avg": "avg(value)", "sum": "sum(value)", "min": "min(value)", "max": "max(value)",
    "last": "(array_agg(value ORDER BY ts DESC))[1]",
}
_TIME_ROLLUP = {
    "avg": "sum(sum) / NULLIF(sum(cnt), 0)", "sum": "sum(sum)", "min": "min(min)", "max": "max(max)",
    "last": "(array_agg(last ORDER BY ts DESC))[1]",
}
_SPACE = {"avg": "avg(v)", "sum": "sum(v)", "min": "min(v)", "max": "max(v)", "count": "count(v)::float8"}


def points_sql(source: Source, time_agg: str, space_agg: str, series_ids: list[int], group_idx: list[int],
               step_s: int, start: datetime, end: datetime) -> tuple[str, list]:
    """Agrégat dans le temps par série et par seau, puis agrégat d'espace par groupe et par seau."""
    time_expr = (_TIME_ROLLUP if source.rollup else _TIME_RAW)[time_agg]
    space_expr = _SPACE[space_agg]
    sql = f"""
        WITH g AS (SELECT * FROM unnest(%s::bigint[], %s::int[]) AS g(series_id, gi)),
        b AS (
            SELECT series_id, date_bin(%s::interval, ts, %s::timestamptz) AS bucket, {time_expr} AS v
              FROM {source.table}
             WHERE series_id = ANY(%s::bigint[]) AND ts >= %s AND ts < %s
             GROUP BY 1, 2
        )
        SELECT g.gi, b.bucket, ({space_expr})::float8 AS v
          FROM b JOIN g USING (series_id)
         GROUP BY 1, 2
         ORDER BY 1, 2
    """
    params = [series_ids, group_idx, timedelta(seconds=step_s), EPOCH_2000, series_ids, start, end]
    return sql, params


def group_series(rows: list[dict], group_by: list[str]) -> tuple[list[dict], list[int], list[int], list[str | None]]:
    """Range les séries trouvées par groupe : rend (groupes, ids, indices de groupe, unités)."""
    groups: dict[tuple, int] = {}
    group_list: list[dict] = []
    units: list[str | None] = []
    ids, idx = [], []
    for r in rows:
        tags = r["tags"] or {}
        key = tuple(tags.get(k) for k in group_by)
        gi = groups.get(key)
        if gi is None:
            gi = groups[key] = len(group_list)
            group_list.append({k: v for k, v in zip(group_by, key)})
            units.append(r.get("unit"))
        elif units[gi] is None and r.get("unit"):
            units[gi] = r["unit"]
        ids.append(r["id"])
        idx.append(gi)
    return group_list, ids, idx, units
