"""Infrastructure (contrat § 7) : hôtes, processus, connexions, conteneurs, contrôles, agents."""
import json

from fastapi import APIRouter, Depends, Query

from ..auth.principal import Principal, viewer
from ..db import fetch_all, fetch_one
from ..errors import bad_request, not_found
from ..runtime import rt
from ..util import like_escape
from .common import ok, parse_tag_filter, time_range
from .twins import annotate, load_twins

router = APIRouter()

HOST_DOWN_S = 120          # l'agent envoie toutes les 15 s ; 2 min sans rien = hôte en panne
# Hôte sans agent (équipement SNMP, ressource de connecteur) : sa fiche repart toutes les 5 min au plus.
DEVICE_DOWN_S = 4 * HOST_DOWN_S
SUMMARY_METRICS = ("system.cpu.usage", "system.mem.pct_used", "system.load.norm.1", "system.disk.in_use")

HOST_COLUMNS = f"""h.id, h.hostname, h.os, h.platform, h.platform_version, h.arch, h.cpu_cores, h.mem_total,
                  h.agent_version, h.ip_addresses, h.tags, h.last_seen,
                  CASE WHEN h.last_seen > now() - make_interval(secs => CASE WHEN h.kind = 'agent' THEN %s ELSE {DEVICE_DOWN_S} END)
                       THEN 'up' ELSE 'down' END AS status,
                  h.kind, h.via_host_id, h.device,
                  (SELECT v.hostname FROM hosts v WHERE v.id = h.via_host_id AND v.org_id = h.org_id) AS via_hostname"""
HOST_KINDS = ("agent", "network_device", "cloud_resource")


async def host_summaries(conn, org_id: int, host_ids: list[int]) -> dict[int, dict]:
    """Dernières valeurs (2 dernières minutes) des quatre métriques de la liste des hôtes."""
    out = {hid: {"cpu": None, "mem_pct": None, "load_norm": None, "disk_max_pct": None} for hid in host_ids}
    if not host_ids:
        return out
    rows = await fetch_all(conn, """
        SELECT s.host_id, s.name, (array_agg(p.value ORDER BY p.ts DESC))[1] AS v, max(p.ts) AS ts
          FROM metric_series s JOIN metric_points p ON p.series_id = s.id
         WHERE s.org_id = %s AND s.host_id = ANY(%s) AND s.name = ANY(%s)
           AND p.ts > now() - interval '2 minutes'
         GROUP BY s.host_id, s.name, s.id
    """, (org_id, host_ids, list(SUMMARY_METRICS)))
    latest: dict = {}
    for r in rows:
        summary = out.get(r["host_id"])
        if summary is None:
            continue
        if r["name"] == "system.disk.in_use":
            cur = summary["disk_max_pct"]
            summary["disk_max_pct"] = r["v"] if cur is None else max(cur, r["v"])
            continue
        field = {"system.cpu.usage": "cpu", "system.mem.pct_used": "mem_pct",
                 "system.load.norm.1": "load_norm"}[r["name"]]
        key = (r["host_id"], field)
        # Plusieurs séries pour une même métrique (étiquettes changées) : la plus récente gagne.
        if key not in latest or r["ts"] > latest[key]:
            latest[key] = r["ts"]
            summary[field] = r["v"]
    return out


@router.get("/hosts")
async def list_hosts(q: str = "", tag: list[str] = Query(default=[]), kind: str = "", p: Principal = Depends(viewer)):
    where, params = ["h.org_id = %s"], [HOST_DOWN_S, p.org_id]
    if kind:
        # § 14 : serveurs (agent installé) ou équipements réseau interrogés par un agent.
        if kind not in HOST_KINDS:
            raise bad_request(f"kind attendu : {'|'.join(HOST_KINDS)}")
        where.append("h.kind = %s")
        params.append(kind)
    if q.strip():
        pattern = "%" + like_escape(q.strip()) + "%"
        where.append("(h.hostname ILIKE %s OR h.ip_addresses::text ILIKE %s)")
        params += [pattern, pattern]
    for t in tag:
        key, value = parse_tag_filter(t)
        if key == "host":
            where.append("h.hostname = %s")
            params.append(value)
        else:
            where.append("h.tags @> %s::jsonb")
            params.append(json.dumps({key: value}, ensure_ascii=False))
    async with rt().db.reader() as conn:
        hosts = await fetch_all(conn, f"SELECT {HOST_COLUMNS} FROM hosts h WHERE {' AND '.join(where)} "
                                      "ORDER BY h.hostname LIMIT 5000", params)
        summaries = await host_summaries(conn, p.org_id, [h["id"] for h in hosts])
        links, index = await load_twins(conn, p.org_id)
    for h in hosts:
        h.update(summaries[h["id"]])
    annotate(hosts, links, index)  # § 51 : ressource et hôte à agent d'une même machine
    return ok(hosts)


@router.get("/hosts/{host_id}")
async def get_host(host_id: int, p: Principal = Depends(viewer)):
    async with rt().db.reader() as conn:
        host = await fetch_one(conn, f"""
            SELECT {HOST_COLUMNS}, h.kernel, h.cpu_model, h.boot_time, h.meta, h.first_seen, h.agent_id
              FROM hosts h WHERE h.org_id = %s AND h.id = %s
        """, (HOST_DOWN_S, p.org_id, host_id))
        if host is None:
            raise not_found("hôte introuvable")
        host.update((await host_summaries(conn, p.org_id, [host_id]))[host_id])
        if host.get("via_host_id"):
            # Équipement réseau (§ 14) : l'hôte de l'agent qui l'interroge.
            host["via"] = await fetch_one(conn, "SELECT id, hostname, last_seen FROM hosts WHERE org_id = %s "
                                                "AND id = %s", (p.org_id, host["via_host_id"]))
        host["agent"] = await fetch_one(conn, """
            SELECT id, version, os, arch, last_ip, status, first_seen, last_seen
              FROM agents WHERE org_id = %s AND host_id = %s ORDER BY last_seen DESC LIMIT 1
        """, (p.org_id, host_id))
        host["checks"] = await fetch_all(conn, """
            SELECT DISTINCT ON (check_name, tags) check_name AS name, status, message, tags, ts
              FROM check_runs
             WHERE org_id = %s AND host_id = %s AND ts > now() - interval '15 minutes'
             ORDER BY check_name, tags, ts DESC
        """, (p.org_id, host_id))
        # § 51 : la même machine vue par un connecteur (ou, pour une ressource, par son agent).
        links, index = await load_twins(conn, p.org_id)
        annotate([host], links, index)
        for twin in host.get("seen_by") or []:
            twin["checks"] = await fetch_all(conn, """
                SELECT DISTINCT ON (check_name, tags) check_name AS name, status, message, tags, ts
                  FROM check_runs
                 WHERE org_id = %s AND host_id = %s AND ts > now() - interval '15 minutes'
                 ORDER BY check_name, tags, ts DESC
            """, (p.org_id, twin["id"]))
    return ok(host)


PROCESS_FIELDS = """p.host_id, h.hostname, p.ts, p.pid, p.ppid, p.name, p.username AS "user", p.cmdline,
                    p.state, p.cpu_pct, p.mem_rss, p.mem_pct, p.threads, p.started_at AS started,
                    p.io_read_bps, p.io_write_bps"""


def _latest_snapshot_sql(table: str, fields: str, alias: str, host_filter: bool, extra: str, order: str) -> str:
    # Hôte précis : son dernier instantané, même ancien (rétention 3 jours). Tous les hôtes :
    # ceux qui ont envoyé dans les 15 dernières minutes, pour ne pas montrer d'hôtes éteints.
    window = "3 days" if host_filter else "15 minutes"
    host_cond = "AND host_id = %s" if host_filter else ""
    return f"""
        WITH last AS (
            SELECT host_id, max(ts) AS ts FROM {table}
             WHERE org_id = %s AND ts > now() - interval '{window}' {host_cond}
             GROUP BY host_id
        )
        SELECT {fields}
          FROM {table} {alias}
          JOIN last l ON l.host_id = {alias}.host_id AND l.ts = {alias}.ts
          JOIN hosts h ON h.id = {alias}.host_id AND h.org_id = %s
         WHERE {alias}.org_id = %s AND {alias}.ts > now() - interval '{window}' {extra}
         ORDER BY {order}
         LIMIT 500
    """


@router.get("/processes")
async def list_processes(host_id: int | None = None, q: str = "", p: Principal = Depends(viewer)):
    params: list = [p.org_id] + ([host_id] if host_id is not None else []) + [p.org_id, p.org_id]
    extra = ""
    if q.strip():
        pattern = "%" + like_escape(q.strip()) + "%"
        extra = "AND (p.name ILIKE %s OR p.cmdline ILIKE %s OR p.username ILIKE %s)"
        params += [pattern, pattern, pattern]
    sql = _latest_snapshot_sql("process_snapshots", PROCESS_FIELDS, "p", host_id is not None, extra,
                               "p.cpu_pct DESC NULLS LAST, p.mem_rss DESC NULLS LAST")
    async with rt().db.reader() as conn:
        return ok(await fetch_all(conn, sql, params))


@router.get("/containers")
async def list_containers(host_id: int | None = None, p: Principal = Depends(viewer)):
    params: list = [p.org_id] + ([host_id] if host_id is not None else []) + [p.org_id, p.org_id]
    fields = """c.host_id, h.hostname, c.ts, c.container_id AS id, c.name, c.image, c.state, c.cpu_pct,
                c.mem_usage, c.mem_limit, c.net_rx_bps, c.net_tx_bps, c.labels"""
    sql = _latest_snapshot_sql("container_snapshots", fields, "c", host_id is not None, "",
                               "h.hostname, c.name")
    async with rt().db.reader() as conn:
        return ok(await fetch_all(conn, sql, params))


@router.get("/connections")
async def list_connections(host_id: int | None = None, from_: int | None = Query(None, alias="from"),
                           to: int | None = None, p: Principal = Depends(viewer)):
    start, end = time_range(from_, to)
    params: list = [p.org_id, start / 1000, end / 1000]
    host_cond = ""
    if host_id is not None:
        host_cond = "AND c.host_id = %s"
        params.append(host_id)
    params.append(p.org_id)
    # Une connexion vue à chaque instantané ne doit pas compter 60 fois sur une heure :
    # maximum par hôte sur la période, puis somme entre hôtes.
    sql = f"""
        WITH per_host AS (
            SELECT c.host_id, c.process, c.direction, c.proto, c.raddr, c.rport, c.lport, c.state,
                   max(c.count) AS cnt
              FROM net_connections c
             WHERE c.org_id = %s AND c.ts >= to_timestamp(%s) AND c.ts < to_timestamp(%s) {host_cond}
             GROUP BY 1, 2, 3, 4, 5, 6, 7, 8
        )
        SELECT x.process, x.direction, x.proto, x.raddr, x.rport, x.lport, x.state,
               sum(x.cnt)::bigint AS count, array_agg(DISTINCT h.hostname ORDER BY h.hostname) AS hosts
          FROM per_host x JOIN hosts h ON h.id = x.host_id AND h.org_id = %s
         GROUP BY 1, 2, 3, 4, 5, 6, 7
         ORDER BY count DESC
         LIMIT 1000
    """
    async with rt().db.reader() as conn:
        return ok(await fetch_all(conn, sql, params))


@router.get("/checks")
async def list_checks(host_id: int | None = None, name: str = "", p: Principal = Depends(viewer)):
    where, params = ["c.org_id = %s", "c.ts > now() - interval '15 minutes'"], [p.org_id]
    if host_id is not None:
        where.append("c.host_id = %s")
        params.append(host_id)
    if name.strip():
        where.append("c.check_name = %s")
        params.append(name.strip())
    async with rt().db.reader() as conn:
        rows = await fetch_all(conn, f"""
            SELECT DISTINCT ON (c.host_id, c.check_name, c.tags)
                   c.host_id, h.hostname, c.check_name AS name, c.status, c.message, c.tags, c.ts
              FROM check_runs c LEFT JOIN hosts h ON h.id = c.host_id AND h.org_id = c.org_id
             WHERE {' AND '.join(where)}
             ORDER BY c.host_id, c.check_name, c.tags, c.ts DESC
        """, params)
    return ok(rows)


@router.get("/agents")
async def list_agents(p: Principal = Depends(viewer)):
    async with rt().db.reader() as conn:
        rows = await fetch_all(conn, """
            SELECT a.id, h.hostname, a.version, a.os, a.arch, a.last_seen,
                   CASE WHEN a.last_seen > now() - make_interval(secs => %s) THEN 'up' ELSE 'down' END AS status,
                   a.status AS collectors, a.host_id, a.last_ip, a.first_seen
              FROM agents a LEFT JOIN hosts h ON h.id = a.host_id AND h.org_id = a.org_id
             WHERE a.org_id = %s
             ORDER BY h.hostname NULLS LAST, a.id
        """, (HOST_DOWN_S, p.org_id))
    return ok(rows)
