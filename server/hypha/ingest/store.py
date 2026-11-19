"""Écriture de l'ingestion : clés, hôtes, séries, filtrage par partition, COPY."""
import json
import logging
import time
from dataclasses import dataclass

import psycopg

from .. import quotas
from ..db import copy_rows, fetch_all, fetch_one
from ..errors import ApiError
from ..runtime import Runtime
from ..util import KEY_RE, key_hash, tags_hash
from .body import AgentInfo

log = logging.getLogger("hypha.ingest")

HOST_TOUCH_S = 10          # last_seen d'un hôte : au plus une écriture toutes les 10 s par nœud
KEY_TOUCH_S = 60           # last_used_at d'une clé : au plus une fois par minute
SERIES_TOUCH_S = 3600      # last_seen d'une série : une fois par heure suffit à dire « vivante »
MAX_SERIES_CACHE = 1_000_000


@dataclass(frozen=True)
class KeyInfo:
    id: int
    org_id: int
    kind: str
    revoked: bool


_UNKNOWN = KeyInfo(0, 0, "", True)


def extract_key(headers, allow_bearer: bool = True) -> str | None:
    key = headers.get("x-hypha-api-key")
    if not key and allow_bearer:
        auth = headers.get("authorization", "")
        if auth[:7].lower() == "bearer ":
            key = auth[7:]
    return key.strip() if key else None


@dataclass
class _HostEntry:
    host_id: int
    hostname: str
    version: str | None
    touched: float


UPSERT_HOST = """
    INSERT INTO hosts (org_id, agent_id, hostname, os, arch, agent_version)
    VALUES (%s, %s, %s, %s, %s, %s)
    ON CONFLICT (org_id, agent_id) DO UPDATE
       SET hostname = EXCLUDED.hostname,
           os = COALESCE(EXCLUDED.os, hosts.os),
           arch = COALESCE(EXCLUDED.arch, hosts.arch),
           agent_version = COALESCE(EXCLUDED.agent_version, hosts.agent_version),
           last_seen = now()
    RETURNING id
"""

# Équipement réseau (§ 14) : hôte `network_device` rattaché à l'hôte de l'agent qui l'interroge,
# sans ligne `agents` (l'équipement n'a pas d'agent installé).
UPSERT_DEVICE = """
    INSERT INTO hosts (org_id, agent_id, hostname, kind, via_host_id)
    VALUES (%s, %s, %s, %s, (SELECT id FROM hosts WHERE org_id = %s AND agent_id = %s::uuid))
    ON CONFLICT (org_id, agent_id) DO UPDATE
       SET hostname = EXCLUDED.hostname, kind = EXCLUDED.kind,
           via_host_id = COALESCE(EXCLUDED.via_host_id, hosts.via_host_id), last_seen = now()
    RETURNING id
"""

# agents.id est unique pour tout le parc : un agent reconfiguré avec la clé d'une autre
# organisation change d'organisation, il ne se duplique pas.
UPSERT_AGENT = """
    INSERT INTO agents (id, org_id, host_id, version, os, arch, last_ip)
    VALUES (%s, %s, %s, %s, %s, %s, %s)
    ON CONFLICT (id) DO UPDATE
       SET org_id = EXCLUDED.org_id, host_id = EXCLUDED.host_id,
           version = COALESCE(EXCLUDED.version, agents.version),
           os = COALESCE(EXCLUDED.os, agents.os), arch = COALESCE(EXCLUDED.arch, agents.arch),
           last_ip = EXCLUDED.last_ip, last_seen = now()
"""

INSERT_SERIES = """
    INSERT INTO metric_series (org_id, name, tags, tags_hash, host_id, type, unit)
    SELECT %s, x.n, x.t::jsonb, x.h, %s, x.ty, x.u
      FROM unnest(%s::text[], %s::text[], %s::bytea[], %s::text[], %s::text[]) AS x(n, t, h, ty, u)
    ON CONFLICT (org_id, name, tags_hash) DO UPDATE
       SET last_seen = now(), host_id = COALESCE(EXCLUDED.host_id, metric_series.host_id)
    RETURNING id, name, tags_hash
"""


class Store:
    def __init__(self, runtime: Runtime):
        self.rt = runtime

    # ── clés ──────────────────────────────────────────────────────────────────

    async def auth_ingest(self, key: str | None) -> KeyInfo:
        if not key:
            raise ApiError(401, "missing_api_key", "en-tête X-Hypha-Api-Key absent")
        if not KEY_RE.match(key):
            raise ApiError(401, "invalid_api_key", "clé d'API inconnue")
        digest = key_hash(key)
        info = self.rt.ingest_keys.get(digest)
        if info is None:
            async with self.rt.db.writer(timeout=3) as conn:
                row = await fetch_one(conn, "SELECT id, org_id, kind, revoked_at IS NOT NULL AS revoked "
                                            "FROM api_keys WHERE key_hash = %s", (digest,))
            info = KeyInfo(row["id"], row["org_id"], row["kind"], row["revoked"]) if row else _UNKNOWN
            self.rt.ingest_keys.set(digest, info)
        if info is _UNKNOWN or info.id == 0:
            raise ApiError(401, "invalid_api_key", "clé d'API inconnue")
        if info.revoked:
            raise ApiError(403, "revoked_api_key", "clé d'API révoquée")
        if info.kind != "ingest":
            raise ApiError(403, "wrong_key_kind", "une clé d'application ne sert pas à l'envoi de données")
        if not self.rt.ingest_limiter.allow(info.id):
            raise ApiError(429, "rate_limited", "trop de requêtes pour cette clé")
        return info

    async def touch_key(self, conn, key: KeyInfo) -> None:
        now = time.monotonic()
        if now - self.rt.key_touched.get(key.id, 0.0) >= KEY_TOUCH_S:
            self.rt.key_touched[key.id] = now
            await conn.execute("UPDATE api_keys SET last_used_at = now() WHERE id = %s", (key.id,))

    # ── hôtes ─────────────────────────────────────────────────────────────────

    async def ensure_host(self, conn, org_id: int, agent: AgentInfo, ip: str | None) -> int:
        # Le système déclaré dit la nature : « cloud » pour une ressource de connecteur (§ 34-35).
        kind = ("cloud_resource" if (agent.os or "").lower() == "cloud" else "network_device") if agent.via             else "agent"
        # Limite de formule (§ 52) : refus 402 avant toute écriture, décision gardée une minute.
        await quotas.admit_host(conn, org_id, agent.id, agent.hostname, kind)
        cache_key = (org_id, agent.id)
        entry: _HostEntry | None = self.rt.hosts.get(cache_key)
        now = time.monotonic()
        if (entry and entry.hostname == agent.hostname and entry.version == agent.version
                and now - entry.touched < HOST_TOUCH_S):
            return entry.host_id
        if agent.via:
            row = await fetch_one(conn, UPSERT_DEVICE, (org_id, agent.id, agent.hostname, kind, org_id, agent.via))
            host_id = row["id"]
        else:
            row = await fetch_one(conn, UPSERT_HOST, (org_id, agent.id, agent.hostname, agent.os, agent.arch,
                                                      agent.version))
            host_id = row["id"]
            await conn.execute(UPSERT_AGENT, (agent.id, org_id, host_id, agent.version, agent.os, agent.arch, ip))
        self.rt.hosts.set(cache_key, _HostEntry(host_id, agent.hostname, agent.version, now))
        if entry is None or entry.hostname != agent.hostname:
            self.rt.hosts_by_name.pop((org_id, agent.hostname.lower()))
        return host_id

    def forget_host(self, org_id: int, agent_id: str) -> None:
        self.rt.hosts.pop((org_id, agent_id))

    async def host_by_name(self, conn, org_id: int, hostname: str | None) -> int | None:
        """Hôte d'une donnée OTLP d'après `host.name` (nom complet ou court)."""
        if not hostname:
            return None
        cache_key = (org_id, hostname.lower())
        cached = self.rt.hosts_by_name.get(cache_key, False)
        if cached is not False:
            return cached
        row = await fetch_one(conn, """
            SELECT id FROM hosts
             WHERE org_id = %s AND (lower(hostname) = lower(%s) OR lower(hostname) = lower(split_part(%s, '.', 1)))
             ORDER BY (lower(hostname) = lower(%s)) DESC, last_seen DESC LIMIT 1
        """, (org_id, hostname, hostname, hostname))
        host_id = row["id"] if row else None
        self.rt.hosts_by_name.set(cache_key, host_id)
        return host_id

    # ── séries ────────────────────────────────────────────────────────────────

    async def resolve_series(self, conn, org_id: int, host_id: int | None,
                             specs: list[tuple[str, str, str | None, dict]]) -> list[int]:
        """Id de chaque série (nom, type, unité, étiquettes), créées à la volée si besoin."""
        cache = self.rt.series
        now = time.monotonic()
        keys = []
        missing: dict = {}
        stale: list[int] = []
        rehost: list[int] = []
        for name, mtype, unit, tags in specs:
            k = (org_id, name, tags_hash(tags))
            keys.append(k)
            entry = cache.get(k)
            if entry is None:
                missing.setdefault(k, (name, mtype, unit, tags))
            elif host_id is not None and entry[1] != host_id:
                rehost.append(entry[0])
                cache[k] = (entry[0], host_id, now)
            elif now - entry[2] > SERIES_TOUCH_S:
                stale.append(entry[0])
                cache[k] = (entry[0], entry[1], now)
        if missing:
            await self._insert_series(conn, org_id, host_id, missing, now)
        if stale:
            await conn.execute("UPDATE metric_series SET last_seen = now() WHERE org_id = %s AND id = ANY(%s)",
                               (org_id, sorted(set(stale))))
        if rehost:
            # Agent réinstallé : même nom d'hôte, nouvel hôte ; les séries suivent le nouvel hôte.
            await conn.execute("UPDATE metric_series SET host_id = %s, last_seen = now() "
                               "WHERE org_id = %s AND id = ANY(%s)", (host_id, org_id, sorted(set(rehost))))
        return [cache[k][0] for k in keys]

    async def _insert_series(self, conn, org_id, host_id, missing: dict, now: float) -> None:
        # Ordre fixe : deux nœuds qui créent les mêmes séries prennent les verrous dans le même ordre.
        items = sorted(missing.items(), key=lambda kv: (kv[0][1], kv[0][2]))
        names = [spec[0] for _, spec in items]
        tags_json = [json.dumps(spec[3], sort_keys=True, separators=(",", ":"), ensure_ascii=False)
                     for _, spec in items]
        hashes = [k[2] for k, _ in items]
        types = [spec[1] for _, spec in items]
        units = [spec[2] for _, spec in items]
        for attempt in (1, 2):
            try:
                async with conn.transaction():
                    rows = await fetch_all(conn, INSERT_SERIES, (org_id, host_id, names, tags_json, hashes,
                                                                 types, units))
                break
            except psycopg.errors.DeadlockDetected:
                if attempt == 2:
                    raise
        if len(self.rt.series) + len(rows) > MAX_SERIES_CACHE:
            self.rt.series.clear()
        for r in rows:
            self.rt.series[(org_id, r["name"], bytes(r["tags_hash"]))] = (r["id"], host_id, now)

    # ── partitions et COPY ────────────────────────────────────────────────────

    async def keep_in_partitions(self, conn, table: str, rows: list[tuple], ts_index: int):
        pmap = self.rt.partitions
        if pmap.stale():
            await self._refresh_partitions(conn)
        kept = [r for r in rows if pmap.covers(table, r[ts_index])]
        if len(kept) < len(rows) and pmap.stale(30):
            # Une partition a peut-être été créée depuis la dernière lecture (passage de minuit).
            await self._refresh_partitions(conn)
            kept = [r for r in rows if pmap.covers(table, r[ts_index])]
        return kept, len(rows) - len(kept)

    async def _refresh_partitions(self, conn) -> None:
        try:
            async with conn.transaction():
                await self.rt.partitions.refresh(conn)
        except psycopg.OperationalError:
            raise
        except psycopg.Error as e:
            # On garde l'ancienne carte et on attend la prochaine échéance : pas un essai par requête.
            self.rt.partitions.loaded_at = time.monotonic()
            log.warning("lecture des partitions impossible : %s", e)

    async def copy(self, conn, table: str, columns: tuple[str, ...], rows: list[tuple], ts_index: int) -> tuple[int, int]:
        rows, dropped = await self.keep_in_partitions(conn, table, rows, ts_index)
        written = await copy_rows(conn, table, columns, rows)
        return written, dropped
