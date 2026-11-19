"""Accès PostgreSQL : pools asynchrones (écriture, lecture), état de santé, carte des partitions.

Toutes les connexions travaillent dans le schéma `hypha`. Une requête de l'API qui ne trouve pas
de connexion en quelques secondes lève une erreur traduite en 503 : l'agent garde alors son
tampon et réessaie, plutôt que de perdre ses données.
"""
import asyncio
import bisect
import logging
import time
from contextlib import asynccontextmanager
from datetime import datetime

import psycopg
from psycopg.rows import dict_row
from psycopg_pool import AsyncConnectionPool, PoolTimeout

from . import telemetry
from .config import Settings

log = logging.getLogger("hypha.db")

DB_ERRORS = (psycopg.OperationalError, PoolTimeout)


def _configurer(statement_timeout: str | None):
    async def configure(conn: psycopg.AsyncConnection) -> None:
        await conn.execute("SET search_path TO hypha, public")
        await conn.execute("SET TIME ZONE 'UTC'")
        if statement_timeout:
            await conn.execute(f"SET statement_timeout = '{statement_timeout}'")
        await conn.commit()
    return configure


def make_pool(dsn: str, name: str, min_size: int, max_size: int,
              statement_timeout: str | None = "30s") -> AsyncConnectionPool:
    kwargs = {"row_factory": dict_row, "application_name": name}
    traced = telemetry.cursor_factory()
    if traced is not None:
        # Curseur tracé (telemetry.py) : spans SQL sous une requête ou une tâche tracée, jamais seuls.
        kwargs["cursor_factory"] = traced
    return AsyncConnectionPool(
        dsn,
        min_size=min_size,
        max_size=max_size,
        open=False,
        name=name,
        timeout=5,
        kwargs=kwargs,
        configure=_configurer(statement_timeout),
        check=AsyncConnectionPool.check_connection,
    )


class Database:
    def __init__(self, settings: Settings, app_name: str = "hypha-api",
                 statement_timeout: str | None = "30s"):
        self.settings = settings
        self.write = make_pool(settings.db_write, app_name, settings.pool_min, settings.pool_max,
                               statement_timeout)
        if settings.db_read and settings.db_read != settings.db_write:
            self.read = make_pool(settings.db_read, app_name + "-read", 1, settings.pool_max,
                                  statement_timeout)
        else:
            self.read = self.write
        self.state = "erreur"          # dernier état connu, lu par /api/sante sans requête
        self.state_at = 0.0

    async def open(self) -> None:
        await self.write.open(wait=False)
        if self.read is not self.write:
            await self.read.open(wait=False)

    async def close(self) -> None:
        if self.read is not self.write:
            await self.read.close()
        await self.write.close()

    def mark(self, ok: bool) -> None:
        self.state = "ok" if ok else "erreur"
        self.state_at = time.monotonic()

    @asynccontextmanager
    async def writer(self, timeout: float = 5.0):
        try:
            async with self.write.connection(timeout=timeout) as conn:
                yield conn
        except DB_ERRORS:
            self.mark(False)
            raise

    @asynccontextmanager
    async def reader(self, timeout: float = 5.0):
        try:
            async with self.read.connection(timeout=timeout) as conn:
                yield conn
        except DB_ERRORS:
            self.mark(False)
            raise

    async def health_loop(self, period: float = 10.0) -> None:
        """Sonde légère en tâche de fond ; la route de santé ne fait que lire `state`."""
        while True:
            try:
                async with self.write.connection(timeout=3) as conn:
                    await conn.execute("SELECT 1")
                self.mark(True)
            except Exception:
                self.mark(False)
            await asyncio.sleep(period)


async def fetch_all(conn, sql: str, params=None) -> list[dict]:
    cur = await conn.execute(sql, params)
    return await cur.fetchall()


async def fetch_one(conn, sql: str, params=None) -> dict | None:
    cur = await conn.execute(sql, params)
    return await cur.fetchone()


async def copy_rows(conn, table: str, columns: tuple[str, ...], rows) -> int:
    """COPY d'un lot de lignes. `table` et `columns` sont des constantes du code, jamais saisies."""
    if not rows:
        return 0
    n = 0
    async with conn.cursor() as cur:
        async with cur.copy(f"COPY {table} ({', '.join(columns)}) FROM STDIN") as cp:
            for row in rows:
                await cp.write_row(row)
                n += 1
    return n


class PartitionMap:
    """Bornes des partitions existantes, par table.

    Une ligne dont `ts` ne tombe dans aucune partition ferait échouer tout le COPY (le lot entier
    serait perdu) : elle est écartée avant l'écriture et comptée dans `dropped`. Cas réels : tampon
    d'agent vidé après une semaine de coupure sur une table à 3 jours de rétention, horloge fausse.
    """

    SQL = r"""
        SELECT pc.relname AS parent,
               (regexp_match(pg_get_expr(c.relpartbound, c.oid), 'FROM \(''([^'']+)''\)'))[1]::timestamptz AS lo,
               (regexp_match(pg_get_expr(c.relpartbound, c.oid), 'TO \(''([^'']+)''\)'))[1]::timestamptz AS hi
          FROM pg_inherits i
          JOIN pg_class c ON c.oid = i.inhrelid
          JOIN pg_class pc ON pc.oid = i.inhparent
          JOIN pg_namespace n ON n.oid = pc.relnamespace
         WHERE n.nspname = 'hypha'
    """

    def __init__(self):
        self._ranges: dict[str, tuple[list[float], list[float]]] = {}
        self.loaded_at = 0.0

    async def refresh(self, conn) -> None:
        rows = await fetch_all(conn, self.SQL)
        ranges: dict[str, list[tuple[float, float]]] = {}
        for r in rows:
            if r["lo"] is None or r["hi"] is None:
                continue
            ranges.setdefault(r["parent"], []).append((r["lo"].timestamp(), r["hi"].timestamp()))
        self._ranges = {}
        for parent, items in ranges.items():
            items.sort()
            self._ranges[parent] = ([a for a, _ in items], [b for _, b in items])
        self.loaded_at = time.monotonic()

    def stale(self, max_age: float = 300.0) -> bool:
        return time.monotonic() - self.loaded_at > max_age

    def covers(self, table: str, ts: datetime) -> bool:
        bounds = self._ranges.get(table)
        if bounds is None:
            # Carte absente (pas encore lue, ou table inconnue) : on laisse la base trancher.
            return True
        starts, ends = bounds
        t = ts.timestamp()
        i = bisect.bisect_right(starts, t) - 1
        return i >= 0 and t < ends[i]
