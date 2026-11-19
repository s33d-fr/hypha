"""Traitements d'ingestion de la 0.4.0 : masquage (§ 23), suivi des erreurs (§ 25), métriques tirées
des journaux (§ 23) et consommation (§ 30). État en mémoire du processus, écritures en lot.

Ordre pour un lot de journaux, après les filtres (§ 11) et le découpage des formats (§ 12) :
masquage → empreinte des erreurs → métriques tirées des journaux → COPY → problèmes (upsert).
Les lignes porteuses d'une erreur reçoivent leur identifiant avant le COPY, pour que le problème
pointe vers sa dernière ligne (`last_log_id`).

Un échec d'écriture d'un traitement annexe (problèmes, compteurs) est isolé dans un point de
sauvegarde : il ne fait jamais perdre le lot de journaux. Seule une base perdue interrompt.
"""
import logging
import time
from dataclasses import dataclass, field

from ..util import TTLCache, clean_tags, ms_to_dt, now_ms
from . import error_tracking as ET
from . import sensitive as S
from .body import LOG_COLUMNS
from .log_metrics import BUCKET_MS, LOG_METRICS_SQL, Aggregator, CompiledMetric, compile_metric
from .usage import UPSERT as USAGE_UPSERT, Usage

log = logging.getLogger("hypha.ingest")

SENSITIVE_FLUSH_S = 60.0
METRICS_FLUSH_S = BUCKET_MS / 1000
_I_MESSAGE = LOG_COLUMNS.index("message")
LOG_ID_COLUMNS = LOG_COLUMNS + ("id",)


class IngestState:
    def __init__(self):
        self.sensitive = TTLCache(60)          # org → règles compilées
        self.sensitive_pending: dict = {}      # (org, règle) → remplacements
        self.sensitive_flushed_at = time.monotonic()
        self.seeded: set[int] = set()          # organisations dont les règles livrées existent
        self.bad_rules: set[int] = set()
        self.log_metrics = TTLCache(60)        # org → métriques compilées
        self.bad_metrics: set[int] = set()
        self.aggregator = Aggregator()
        self.metrics_flushed_at = 0.0
        self.usage = Usage()
        self.warned_at: dict[str, float] = {}

    def warn(self, what: str, err: Exception) -> None:
        now = time.monotonic()
        if now - self.warned_at.get(what, 0.0) >= 60:
            self.warned_at[what] = now
            log.warning("%s : %s", what, str(err).splitlines()[0] if str(err) else type(err).__name__)


def state_of(runtime) -> IngestState:
    st = getattr(runtime, "v04", None)
    if st is None:
        st = runtime.v04 = IngestState()
    return st


async def _isolated(conn, state: IngestState, what: str, fn) -> bool:
    """Exécute `fn` dans un point de sauvegarde ; une erreur de données est journalisée, pas propagée."""
    import psycopg
    try:
        async with conn.transaction():
            await fn()
        return True
    except psycopg.OperationalError:
        raise
    except psycopg.Error as e:
        state.warn(what, e)
        return False


# ── configuration par organisation ─────────────────────────────────────────────

async def rules_for(conn, runtime, org_id: int) -> list[S.Rule]:
    state = state_of(runtime)
    cached = state.sensitive.get(org_id)
    if cached is not None:
        return cached
    if org_id not in state.seeded:
        await _isolated(conn, state, "règles de masquage livrées non posées", lambda: S.seed_builtin(conn, org_id))
        state.seeded.add(org_id)
    rules: list = []

    async def load():
        rules.extend(await S.load_rules(conn, org_id, state.bad_rules))
    await _isolated(conn, state, "règles de masquage illisibles", load)
    state.sensitive.set(org_id, rules)
    return rules


async def metrics_for(conn, runtime, org_id: int) -> list[CompiledMetric]:
    from ..db import fetch_all
    from ..errors import QueryError
    state = state_of(runtime)
    cached = state.log_metrics.get(org_id)
    if cached is not None:
        return cached
    rows: list = []

    async def load():
        rows.extend(await fetch_all(conn, LOG_METRICS_SQL, (org_id,)))
    await _isolated(conn, state, "métriques de journaux illisibles", load)
    compiled = []
    for r in rows:
        try:
            compiled.append(compile_metric(r))
        except QueryError as e:
            if r["id"] not in state.bad_metrics:
                state.bad_metrics.add(r["id"])
                log.warning("métrique de journaux %s (« %s ») ignorée : %s", r["id"], r["name"], e)
    state.log_metrics.set(org_id, compiled)
    return compiled


def forget_org(runtime, org_id: int) -> None:
    """Après une modification de règle ou de métrique : ce processus relit tout de suite."""
    state = state_of(runtime)
    state.sensitive.pop(org_id)
    state.log_metrics.pop(org_id)


# ── journaux ───────────────────────────────────────────────────────────────────

@dataclass
class LogBatch:
    rows: list = field(default_factory=list)        # LOG_COLUMNS
    id_rows: list = field(default_factory=list)     # LOG_COLUMNS + id (lignes porteuses d'une erreur)
    deltas: list = field(default_factory=list)


async def prepare_logs(conn, runtime, org_id: int, rows: list[tuple]) -> LogBatch:
    from ..db import fetch_all
    state = state_of(runtime)
    if not rows:
        return LogBatch()
    rules = await rules_for(conn, runtime, org_id)
    if rules:
        counts: dict = {}
        rows = S.mask_rows(rows, rules, counts)
        for rule_id, n in counts.items():
            state.sensitive_pending[(org_id, rule_id)] = state.sensitive_pending.get((org_id, rule_id), 0) + n
    occurrences = [o for o in (ET.from_log_row(r, i) for i, r in enumerate(rows)) if o is not None]
    batch = LogBatch()
    if occurrences:
        ids = [r["id"] for r in await fetch_all(conn, "SELECT nextval('hypha.logs_id_seq') AS id "
                                                      "FROM generate_series(1, %s)", (len(occurrences),))]
        log_ids = {}
        tagged = {}
        for o, log_id in zip(occurrences, ids):
            log_ids[o.index] = log_id
            tagged[o.index] = ET.tag_log_row(rows[o.index], o.fingerprint, log_id)
        rows_out = []
        for i, r in enumerate(rows):
            if i in tagged:
                batch.id_rows.append(tagged[i])
                rows_out.append(tagged[i][:-1])
            else:
                batch.rows.append(r)
                rows_out.append(r)
        batch.deltas = ET.aggregate(occurrences, log_ids)
        rows = rows_out
    else:
        batch.rows = rows
    metrics = await metrics_for(conn, runtime, org_id)
    if metrics:
        state.aggregator.observe(org_id, metrics, rows)
    return batch


async def finish_logs(conn, runtime, store, org_id: int, batch: LogBatch, written: int) -> None:
    state = state_of(runtime)
    if batch.deltas:
        await _isolated(conn, state, "problèmes (suivi des erreurs) non écrits",
                        lambda: ET.upsert(conn, org_id, batch.deltas))
    nbytes = sum(len(r[_I_MESSAGE] or "") for r in batch.rows) + sum(len(r[_I_MESSAGE] or "") for r in batch.id_rows)
    state.usage.add(org_id, log_lines=written, log_bytes=nbytes)
    await flush_due(conn, runtime, store)


# ── traces et RUM ──────────────────────────────────────────────────────────────

def span_occurrences(spans: list[dict], host_ids: list) -> list:
    """Occurrences des spans en erreur ; l'empreinte est posée dans les attributs du span."""
    out = []
    for s, host_id in zip(spans, host_ids):
        o = ET.from_span(s, ms_to_dt(s["ts_ms"]), host_id)
        if o is not None:
            s["attributes"]["error.fingerprint"] = o.fingerprint
            out.append(o)
    return out


def rum_occurrences(rows: list[tuple], service: str) -> tuple[list[tuple], list]:
    """Événements RUM `error` (lignes de RUM_COLUMNS) : occurrences, empreinte posée dans les attributs."""
    import json

    from .body import RUM_COLUMNS
    i_type, i_name, i_attrs, i_ts = (RUM_COLUMNS.index(c) for c in ("event_type", "name", "attributes", "ts"))
    out, occurrences = [], []
    for r in rows:
        if r[i_type] != "error":
            out.append(r)
            continue
        attrs = json.loads(r[i_attrs] or "{}")
        o = ET.from_rum(r[i_name], attrs, r[i_ts], service)
        occurrences.append(o)
        attrs["error.fingerprint"] = o.fingerprint
        row = list(r)
        row[i_attrs] = json.dumps(attrs, ensure_ascii=False, separators=(",", ":"))
        out.append(tuple(row))
    return out, occurrences


async def record_occurrences(conn, runtime, org_id: int, occurrences: list) -> None:
    if occurrences:
        deltas = ET.aggregate(occurrences)
        await _isolated(conn, state_of(runtime), "problèmes (suivi des erreurs) non écrits",
                        lambda: ET.upsert(conn, org_id, deltas))


# ── écritures en lot ───────────────────────────────────────────────────────────

async def flush_due(conn, runtime, store) -> None:
    """Écrit ce qui est échu : consommation (minute), compteurs de masquage (minute), métriques
    tirées des journaux (seaux de 15 s clos). Appelée à la fin des routes d'ingestion."""
    state = state_of(runtime)
    now = time.monotonic()
    if state.usage.due(now):
        pending = state.usage.take()
        if not await _isolated(conn, state, "consommation non écrite",
                               lambda: conn.execute(USAGE_UPSERT, Usage.params(pending))):
            state.usage.restore(pending)
    if state.sensitive_pending and now - state.sensitive_flushed_at >= SENSITIVE_FLUSH_S:
        pending, state.sensitive_pending = state.sensitive_pending, {}
        state.sensitive_flushed_at = now
        if not await _isolated(conn, state, "compteurs de masquage non écrits",
                               lambda: S.write_counts(conn, pending)):
            for k, n in pending.items():
                state.sensitive_pending[k] = state.sensitive_pending.get(k, 0) + n
    if state.aggregator.buckets and now - state.metrics_flushed_at >= METRICS_FLUSH_S:
        state.metrics_flushed_at = now
        closed = state.aggregator.take_closed(now_ms())
        if closed and not await _isolated(conn, state, "métriques de journaux non écrites",
                                          lambda: write_log_metrics(conn, store, closed)):
            state.aggregator.restore(closed)


async def write_log_metrics(conn, store, items: list[tuple]) -> None:
    by_org: dict[int, list] = {}
    for item in items:
        by_org.setdefault(item[0], []).append(item)
    for org_id, group in sorted(by_org.items()):
        specs = [(name, mtype, None, clean_tags(tags)) for _, name, mtype, tags, _, _ in group]
        ids = await store.resolve_series(conn, org_id, None, specs)
        await store.copy(conn, "metric_points", ("series_id", "ts", "value"),
                         [(sid, ms_to_dt(ts), value) for sid, (_, _, _, _, ts, value) in zip(ids, group)], 1)
