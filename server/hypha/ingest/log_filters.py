"""Filtres d'exclusion des journaux appliqués à l'arrivée, avant l'écriture (contrat § 11).

Pour chaque ligne, les filtres actifs de l'organisation sont essayés dans l'ordre de `position` ;
le premier qui correspond décide : `exclude` jette la ligne, `sample` la garde avec la probabilité
`sample_rate`. Le tirage est déterministe (hachage de l'horodatage et du message) : un agent qui
renvoie son tampon obtient la même décision. Une ligne de niveau error ou plus grave n'est jamais
jetée par un `sample`, ni par un `exclude` qui ne vise pas explicitement `status:` ; pour elle, ces
filtres sont sautés et l'évaluation continue.

Compteurs : `excluded_total` (cumul par filtre) et la métrique `hypha.logs.excluded{filter_id}`
(pour /logs/volume sur une période) sont écrits en lot, au plus une fois par minute et par
processus. Un processus arrêté avant son écriture perd au plus une minute de comptage.
"""
import hashlib
import json
import logging
import time
from dataclasses import dataclass

from ..errors import QueryError
from ..query import logs_ql, logs_match
from .body import LOG_COLUMNS

log = logging.getLogger("hypha.ingest")

SEVERE = frozenset(("emergency", "alert", "critical", "error"))
FLUSH_EVERY_S = 60.0
EXCLUDED_METRIC = "hypha.logs.excluded"
_COL = {name: i for i, name in enumerate(LOG_COLUMNS)}


class LogView:
    """Ligne prête pour le COPY (tuple de LOG_COLUMNS) vue comme un dictionnaire : les colonnes
    jsonb ne sont décodées que si un filtre les lit, et une seule fois."""

    __slots__ = ("row", "_attributes", "_tags")

    def __init__(self, row: tuple):
        self.row = row
        self._attributes = None
        self._tags = None

    def get(self, key: str, default=None):
        if key == "attributes":
            if self._attributes is None:
                self._attributes = json.loads(self.row[_COL["attributes"]] or "{}")
            return self._attributes
        if key == "tags":
            if self._tags is None:
                self._tags = json.loads(self.row[_COL["tags"]] or "{}")
            return self._tags
        i = _COL.get(key)
        return self.row[i] if i is not None else default

    @property
    def ts_ms(self) -> int:
        return round(self.row[_COL["ts"]].timestamp() * 1000)


def keep_sampled(message: str, ts_ms: int, rate: float) -> bool:
    """Tirage déterministe : même message et même horodatage, même décision."""
    if rate >= 1.0:
        return True
    if rate <= 0.0:
        return False
    digest = hashlib.blake2b(f"{ts_ms}\x1f{message}".encode("utf-8"), digest_size=8).digest()
    return int.from_bytes(digest, "big") / 2.0 ** 64 < rate


@dataclass(frozen=True)
class CompiledFilter:
    id: int
    action: str
    rate: float
    predicate: logs_match.Predicate
    targets_status: bool


def compile_filter(row: dict) -> CompiledFilter:
    node = logs_ql.parse(row["query"])
    if node is None:
        raise QueryError("requête vide")
    logs_ql.to_sql(node)
    return CompiledFilter(row["id"], row["action"], float(row["sample_rate"] or 0.0),
                          logs_match.compile_node(node), logs_match.targets_status(node))


def decide(filters: list[CompiledFilter], line) -> int | None:
    """Id du filtre qui jette la ligne, ou None si elle est gardée."""
    severe = line.get("status") in SEVERE
    for f in filters:
        if severe and (f.action == "sample" or not f.targets_status):
            continue
        if not f.predicate(line):
            continue
        if f.action == "exclude":
            return f.id
        return None if keep_sampled(line.get("message") or "", line.ts_ms, f.rate) else f.id
    return None


def apply_filters(filters: list[CompiledFilter], rows: list[tuple]) -> tuple[list[tuple], dict[int, int]]:
    """Rend (lignes gardées, {filtre: lignes jetées})."""
    if not filters:
        return rows, {}
    kept, counts = [], {}
    for row in rows:
        fid = decide(filters, LogView(row))
        if fid is None:
            kept.append(row)
        else:
            counts[fid] = counts.get(fid, 0) + 1
    return kept, counts


FILTERS_SQL = """
    SELECT id, name, query, action, sample_rate FROM log_filters
     WHERE org_id = %s AND enabled ORDER BY position, id
"""


class LogFilters:
    def __init__(self, runtime):
        self.rt = runtime

    async def for_org(self, conn, org_id: int) -> list[CompiledFilter]:
        # psycopg et db ne s'importent qu'ici : la décision (decide, apply_filters) reste testable sans base.
        import psycopg

        from ..db import fetch_all
        cached = self.rt.log_filters.get(org_id)
        if cached is not None:
            return cached
        try:
            # Point de sauvegarde : une table absente (migration 003 pas encore passée) ne doit pas
            # rendre inutilisable la transaction d'ingestion.
            async with conn.transaction():
                rows = await fetch_all(conn, FILTERS_SQL, (org_id,))
        except psycopg.errors.UndefinedTable:
            rows = []
        filters = []
        for r in rows:
            try:
                filters.append(compile_filter(r))
            except QueryError as e:
                if r["id"] not in self.rt.bad_filters:
                    self.rt.bad_filters.add(r["id"])
                    log.warning("filtre de journaux %s (« %s ») ignoré : %s", r["id"], r["name"], e)
        self.rt.log_filters.set(org_id, filters)
        return filters

    async def apply(self, conn, org_id: int, rows: list[tuple]) -> tuple[list[tuple], int]:
        """Rend (lignes gardées, nombre de lignes jetées) et tient les compteurs en mémoire."""
        if not rows:
            return rows, 0
        filters = await self.for_org(conn, org_id)
        kept, counts = apply_filters(filters, rows)
        pending = self.rt.filter_pending
        for fid, n in counts.items():
            pending[(org_id, fid)] = pending.get((org_id, fid), 0) + n
        return kept, len(rows) - len(kept)

    async def flush_if_due(self, conn, store) -> None:
        """Écrit les compteurs en attente, au plus une fois par minute pour ce processus."""
        import psycopg
        now = time.monotonic()
        if not self.rt.filter_pending or now - self.rt.filter_flushed_at < FLUSH_EVERY_S:
            return
        pending, self.rt.filter_pending = self.rt.filter_pending, {}
        self.rt.filter_flushed_at = now
        try:
            async with conn.transaction():
                await write_counts(conn, store, pending)
        except psycopg.Error as e:
            # Comptes remis en attente pour la minute suivante : un échec ne les perd pas.
            for k, n in pending.items():
                self.rt.filter_pending[k] = self.rt.filter_pending.get(k, 0) + n
            if isinstance(e, psycopg.OperationalError):
                raise
            log.warning("compteurs des filtres de journaux non écrits : %s", str(e).splitlines()[0])


async def write_counts(conn, store, pending: dict) -> None:
    from datetime import datetime, timezone
    items = sorted(pending.items())
    await conn.execute("""
        UPDATE log_filters f SET excluded_total = f.excluded_total + x.n
          FROM unnest(%s::bigint[], %s::bigint[], %s::bigint[]) AS x(org_id, id, n)
         WHERE f.id = x.id AND f.org_id = x.org_id
    """, ([o for (o, _), _ in items], [f for (_, f), _ in items], [n for _, n in items]))
    by_org: dict[int, list] = {}
    for (org_id, fid), n in items:
        by_org.setdefault(org_id, []).append((fid, n))
    now = datetime.now(timezone.utc)
    for org_id, entries in by_org.items():
        ids = await store.resolve_series(conn, org_id, None, [(EXCLUDED_METRIC, "count", None, {"filter_id": str(fid)})
                                                             for fid, _ in entries])
        await store.copy(conn, "metric_points", ("series_id", "ts", "value"),
                         [(sid, now, float(n)) for sid, (_, n) in zip(ids, entries)], 1)
