"""Catalogue de services et carte des services (contrat § 26).

Le catalogue fusionne les services déclarés (`services`) et ceux vus sur 24 h (spans APM, champ
`service` des journaux). Pour chacun : fiche déclarée, chiffres APM et erreurs de journaux sur la
période (1 h par défaut), moniteurs liés (étiquette `service`, ou `service:x` dans la requête, ou
moniteur `apm` / `error_tracking` du service) avec leur pire état, SLO liés, problèmes ouverts.

Les services vus se listent par un parcours d'index (recherche du nom suivant), sans compter les
lignes : quelques dizaines de sondes d'index au lieu de millions de lignes.
"""
import json
import re
from datetime import datetime, timedelta, timezone

from fastapi import APIRouter, Body, Depends, Query, Request

from .. import audit
from ..auth.principal import Principal, member, viewer
from ..db import fetch_all, fetch_one
from ..errors import bad_request, not_found
from ..runtime import rt
from ..util import clean_tags, jdump, ms_to_dt
from .common import done, ok, time_range

router = APIRouter()

NAME_RE = re.compile(r"^[A-Za-z0-9_.\-/:@]{1,100}$")
URL_RE = re.compile(r"^https?://[^\s\"'<>]{1,1990}$")
FIELDS = ("id, name, description, team, owner, tier, runbook_url, repo_url, docs_url, chat_url, tags, depends_on, "
          "created_at, updated_at")
TEXT_FIELDS = {"description": 5000, "team": 200, "owner": 200}
URL_FIELDS = ("runbook_url", "repo_url", "docs_url", "chat_url")
SEEN_WINDOW = timedelta(hours=24)
SEVERE = ["emergency", "alert", "critical", "error"]
STATE_RANK = {"OK": 0, "NODATA": 1, "WARN": 2, "ALERT": 3}
ENTRY = "(parent_span_id IS NULL OR kind IN ('server', 'consumer'))"


def distinct_sql(table: str) -> str:
    """Services distincts d'une table depuis un instant, par sauts d'index (`table` : constante du code)."""
    return f"""
        WITH RECURSIVE s(service) AS (
            (SELECT service FROM {table} WHERE org_id = %s AND ts > %s AND service IS NOT NULL
              ORDER BY service LIMIT 1)
            UNION ALL
            SELECT (SELECT t.service FROM {table} t WHERE t.org_id = %s AND t.ts > %s AND t.service > s.service
                     ORDER BY t.service LIMIT 1)
              FROM s WHERE s.service IS NOT NULL
        )
        SELECT service FROM s WHERE service IS NOT NULL LIMIT 1000
    """


def links_monitor(m: dict, name: str) -> bool:
    """Un moniteur concerne le service : étiquette, requête qui cite `service:x`, ou champ `service`."""
    if (m.get("tags") or {}).get("service") == name:
        return True
    q = m.get("query") or {}
    if isinstance(q, dict) and q.get("service") == name:
        return True
    text = json.dumps(q, ensure_ascii=False)
    return re.search(r"(?<![\w.\-])service:" + re.escape(name) + r"(?![\w.\-])", text) is not None


def links_slo(s: dict, name: str, monitor_ids: set) -> bool:
    if (s.get("tags") or {}).get("service") == name or (s.get("monitor_id") in monitor_ids):
        return True
    pattern = r"(?<![\w.\-])service:" + re.escape(name) + r"(?![\w.\-])"
    return any(re.search(pattern, s.get(k) or "") for k in ("good_query", "total_query"))


def worst_state(states: list[str]) -> str | None:
    return max(states, key=lambda s: STATE_RANK.get(s, 0)) if states else None


def derived_state(apm: dict | None) -> str | None:
    """État d'un service sans moniteur : tiré de son taux d'erreur APM (≥ 10 % ALERT, ≥ 1 % WARN)."""
    if not apm or not apm.get("hits"):
        return None
    rate = apm["errors"] / apm["hits"]
    return "ALERT" if rate >= 0.10 else "WARN" if rate >= 0.01 else "OK"


async def monitor_states(conn, org_id: int) -> list[dict]:
    monitors = await fetch_all(conn, "SELECT id, name, type, query, tags FROM monitors WHERE org_id = %s AND enabled",
                               (org_id,))
    if not monitors:
        return []
    states = await fetch_all(conn, """
        SELECT s.monitor_id, s.state FROM monitor_states s JOIN monitors m ON m.id = s.monitor_id AND m.org_id = %s
    """, (org_id,))
    by_monitor: dict[int, list] = {}
    for s in states:
        by_monitor.setdefault(s["monitor_id"], []).append(s["state"])
    for m in monitors:
        m["state"] = worst_state(by_monitor.get(m["id"], [])) or "NODATA"
    return monitors


async def apm_stats(conn, org_id: int, start, end) -> dict[str, dict]:
    rows = await fetch_all(conn, f"""
        SELECT service, count(*) AS hits, count(*) FILTER (WHERE status = 'error') AS errors,
               percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ns) / 1e6 AS p95_ms
          FROM spans WHERE org_id = %s AND ts >= %s AND ts < %s AND {ENTRY}
         GROUP BY service
    """, (org_id, start, end))
    return {r["service"]: {"hits": r["hits"], "errors": r["errors"], "p95_ms": r["p95_ms"],
                           "error_rate": r["errors"] / r["hits"] if r["hits"] else 0.0} for r in rows}


async def catalog(conn, org_id: int, start, end, only: str | None = None) -> list[dict]:
    now = datetime.now(timezone.utc)
    declared = {r["name"]: r for r in await fetch_all(
        conn, f"SELECT {FIELDS} FROM services WHERE org_id = %s" + (" AND name = %s" if only else ""),
        (org_id, only) if only else (org_id,))}
    seen: dict[str, set] = {}
    for table, label in (("spans", "apm"), ("logs", "logs")):
        for r in await fetch_all(conn, distinct_sql(table), (org_id, now - SEEN_WINDOW, org_id, now - SEEN_WINDOW)):
            seen.setdefault(r["service"], set()).add(label)
    names = sorted((set(declared) | set(seen)) if not only else ({only} & (set(declared) | set(seen))))
    if not names:
        return []
    apm = await apm_stats(conn, org_id, start, end)
    log_errors = {r["service"]: r["n"] for r in await fetch_all(conn, """
        SELECT service, count(*) AS n FROM logs
         WHERE org_id = %s AND ts > now() - interval '1 hour' AND status = ANY(%s::text[]) AND service = ANY(%s::text[])
         GROUP BY service
    """, (org_id, SEVERE, names))}
    issues = {r["service"]: r["n"] for r in await fetch_all(conn, """
        SELECT service, count(*) AS n FROM error_issues WHERE org_id = %s AND status = 'open'
           AND service = ANY(%s::text[]) GROUP BY service
    """, (org_id, names))}
    monitors = await monitor_states(conn, org_id)
    slos = await fetch_all(conn, "SELECT id, name, type, target, monitor_id, good_query, total_query, tags "
                                 "FROM slos WHERE org_id = %s", (org_id,))
    out = []
    for name in names:
        d = declared.get(name) or {}
        linked = [{"id": m["id"], "name": m["name"], "type": m["type"], "state": m["state"]}
                  for m in monitors if links_monitor(m, name)]
        ids = {m["id"] for m in linked}
        stats = apm.get(name)
        out.append({
            "name": name, "declared": bool(d), "seen": sorted(seen.get(name, set())),
            "id": d.get("id"), "description": d.get("description", ""), "team": d.get("team", ""),
            "owner": d.get("owner", ""), "tier": d.get("tier"), "tags": d.get("tags") or {},
            "depends_on": d.get("depends_on") or [],
            "links": {"runbook": d.get("runbook_url", ""), "repo": d.get("repo_url", ""),
                      "docs": d.get("docs_url", ""), "chat": d.get("chat_url", "")},
            "apm": stats, "log_errors_1h": log_errors.get(name, 0), "monitors": linked,
            "worst_state": worst_state([m["state"] for m in linked]) or derived_state(stats),
            "slos": [{"id": s["id"], "name": s["name"], "type": s["type"], "target": s["target"]}
                     for s in slos if links_slo(s, name, ids)],
            "open_issues": issues.get(name, 0),
        })
    return out


def validate_service(data: dict, name: str | None = None) -> dict:
    name = name if name is not None else data.get("name")
    if not isinstance(name, str) or not NAME_RE.match(name.strip()):
        raise bad_request("name : 1 à 100 caractères parmi lettres, chiffres, . _ - / : @", "invalid_service")
    out = {"name": name.strip()}
    for field, limit in TEXT_FIELDS.items():
        v = data.get(field, "") or ""
        if not isinstance(v, str) or len(v) > limit:
            raise bad_request(f"{field} : texte de {limit} caractères au plus", "invalid_service")
        out[field] = v.strip()
    for field in URL_FIELDS:
        v = (data.get(field, "") or "").strip()
        if v and not URL_RE.match(v):
            raise bad_request(f"{field} : URL http(s):// attendue", "invalid_service")
        out[field] = v
    tier = data.get("tier")
    if tier is not None and (isinstance(tier, bool) or not isinstance(tier, int) or not 1 <= tier <= 4):
        raise bad_request("tier : entier de 1 (critique) à 4, ou null", "invalid_service")
    out["tier"] = tier
    tags = data.get("tags", {}) or {}
    if not isinstance(tags, dict):
        raise bad_request("tags doit être un objet", "invalid_service")
    out["tags"] = clean_tags(tags)
    deps = data.get("depends_on", []) or []
    if not isinstance(deps, list) or len(deps) > 50 or not all(isinstance(x, str) and NAME_RE.match(x) for x in deps):
        raise bad_request("depends_on : liste de noms de services (50 au plus)", "invalid_service")
    out["depends_on"] = sorted(set(deps) - {out["name"]})
    return out


UPSERT = f"""
    INSERT INTO services (org_id, name, description, team, owner, tier, runbook_url, repo_url, docs_url, chat_url,
                          tags, depends_on)
    VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s::jsonb, %s::text[])
    ON CONFLICT (org_id, name) DO UPDATE
       SET description = EXCLUDED.description, team = EXCLUDED.team, owner = EXCLUDED.owner, tier = EXCLUDED.tier,
           runbook_url = EXCLUDED.runbook_url, repo_url = EXCLUDED.repo_url, docs_url = EXCLUDED.docs_url,
           chat_url = EXCLUDED.chat_url, tags = EXCLUDED.tags, depends_on = EXCLUDED.depends_on, updated_at = now()
    RETURNING {FIELDS}
"""


def _upsert_params(org_id: int, s: dict) -> tuple:
    return (org_id, s["name"], s["description"], s["team"], s["owner"], s["tier"], s["runbook_url"], s["repo_url"],
            s["docs_url"], s["chat_url"], jdump(s["tags"]), s["depends_on"])


@router.get("/services")
async def list_services(from_: int | None = Query(None, alias="from"), to: int | None = None,
                        p: Principal = Depends(viewer)):
    start, end = time_range(from_, to)
    async with rt().db.reader() as conn:
        return ok(await catalog(conn, p.org_id, ms_to_dt(start), ms_to_dt(end)))


@router.post("/services")
async def create_service(request: Request, data: dict = Body(...), p: Principal = Depends(member)):
    s = validate_service(data)
    async with rt().db.writer() as conn:
        if await fetch_one(conn, "SELECT id FROM services WHERE org_id = %s AND name = %s", (p.org_id, s["name"])):
            raise bad_request(f"le service « {s['name']} » est déjà déclaré (PUT pour le modifier)", "duplicate")
        row = await fetch_one(conn, UPSERT, _upsert_params(p.org_id, s))
        await audit.record(conn, org_id=p.org_id, principal=p, action="create", object_type="service",
                           object_id=s["name"], summary=f"service « {s['name']} » déclaré", after=s, request=request)
    return ok(row, 201)


@router.get("/services/{name:path}")
async def get_service(name: str, from_: int | None = Query(None, alias="from"), to: int | None = None,
                      p: Principal = Depends(viewer)):
    start, end = time_range(from_, to)
    async with rt().db.reader() as conn:
        items = await catalog(conn, p.org_id, ms_to_dt(start), ms_to_dt(end), only=name)
        if not items:
            raise not_found("service inconnu (ni déclaré ni vu depuis 24 h)")
        item = items[0]
        item["deploys"] = await fetch_all(conn, """
            SELECT ts, title, text, tags FROM events
             WHERE org_id = %s AND source = 'deploy' AND tags @> %s::jsonb AND ts > now() - interval '30 days'
             ORDER BY ts DESC LIMIT 20
        """, (p.org_id, json.dumps({"service": name})))
        item["issues"] = await fetch_all(conn, """
            SELECT id, error_type, message, count, last_seen, status, regressed_at FROM error_issues
             WHERE org_id = %s AND service = %s AND status <> 'ignored' ORDER BY last_seen DESC LIMIT 20
        """, (p.org_id, name))
        edges = await service_edges(conn, p.org_id, ms_to_dt(start), ms_to_dt(end))
    item["dependencies"] = sorted({e["to"] for e in edges if e["from"] == name} | set(item["depends_on"]))
    item["dependents"] = sorted({e["from"] for e in edges if e["to"] == name})
    return ok(item)


@router.put("/services/{name:path}")
async def put_service(name: str, request: Request, data: dict = Body(...), p: Principal = Depends(member)):
    s = validate_service(data, name)
    async with rt().db.writer() as conn:
        before = await fetch_one(conn, f"SELECT {FIELDS} FROM services WHERE org_id = %s AND name = %s",
                                 (p.org_id, s["name"]))
        row = await fetch_one(conn, UPSERT, _upsert_params(p.org_id, s))
        await audit.record(conn, org_id=p.org_id, principal=p, action="update" if before else "create",
                           object_type="service", object_id=s["name"], summary=f"service « {s['name']} » enregistré",
                           before=before, after=s, request=request)
    return ok(row)


@router.delete("/services/{name:path}")
async def delete_service(name: str, request: Request, p: Principal = Depends(member)):
    async with rt().db.writer() as conn:
        row = await fetch_one(conn, f"DELETE FROM services WHERE org_id = %s AND name = %s RETURNING {FIELDS}",
                              (p.org_id, name))
        if row is None:
            raise not_found("service non déclaré")
        await audit.record(conn, org_id=p.org_id, principal=p, action="delete", object_type="service",
                           object_id=name, summary=f"déclaration du service « {name} » supprimée", before=row,
                           request=request)
    return done()


# ── carte des services (APM) ───────────────────────────────────────────────────

MAP_SAMPLE = 50_000

EDGES_SQL = f"""
    WITH s AS (
        SELECT trace_id, span_id, parent_span_id, service, status, duration_ns FROM spans
         WHERE org_id = %s AND ts >= %s AND ts < %s ORDER BY ts DESC LIMIT {MAP_SAMPLE}
    )
    SELECT p.service AS src, c.service AS dst, count(*) AS hits, count(*) FILTER (WHERE c.status = 'error') AS errors,
           percentile_cont(0.95) WITHIN GROUP (ORDER BY c.duration_ns) / 1e6 AS p95_ms
      FROM s c JOIN s p ON p.trace_id = c.trace_id AND p.span_id = c.parent_span_id
     WHERE p.service <> c.service
     GROUP BY 1, 2
"""
LEAVES_SQL = f"""
    WITH s AS (
        SELECT trace_id, span_id, parent_span_id, service, kind, status, duration_ns, attributes FROM spans
         WHERE org_id = %s AND ts >= %s AND ts < %s ORDER BY ts DESC LIMIT {MAP_SAMPLE}
    )
    SELECT c.service AS src, c.attributes ->> 'peer.service' AS peer, c.attributes ->> 'db.system' AS db_system,
           coalesce(c.attributes ->> 'server.address', c.attributes ->> 'net.peer.name', c.attributes ->> 'db.name')
               AS address,
           count(*) AS hits, count(*) FILTER (WHERE c.status = 'error') AS errors,
           percentile_cont(0.95) WITHIN GROUP (ORDER BY c.duration_ns) / 1e6 AS p95_ms
      FROM s c
     WHERE c.kind = 'client'
       AND (c.attributes ? 'peer.service' OR c.attributes ? 'db.system' OR c.attributes ? 'server.address'
            OR c.attributes ? 'net.peer.name')
       AND NOT EXISTS (SELECT 1 FROM s x WHERE x.trace_id = c.trace_id AND x.parent_span_id = c.span_id)
     GROUP BY 1, 2, 3, 4
"""


def leaf_node(r: dict) -> tuple[str, str]:
    """(nom, genre) du nœud d'un span client sans enfant : base de données ou service externe."""
    if r.get("db_system"):
        name = f"{r['db_system']}:{r['address']}" if r.get("address") else r["db_system"]
        return name, "database"
    return (r.get("peer") or r.get("address") or "externe"), "external"


async def service_edges(conn, org_id: int, start, end) -> list[dict]:
    edges = [{"from": r["src"], "to": r["dst"], "hits": r["hits"], "errors": r["errors"], "p95_ms": r["p95_ms"]}
             for r in await fetch_all(conn, EDGES_SQL, (org_id, start, end))]
    for r in await fetch_all(conn, LEAVES_SQL, (org_id, start, end)):
        name, _ = leaf_node(r)
        edges.append({"from": r["src"], "to": name, "hits": r["hits"], "errors": r["errors"], "p95_ms": r["p95_ms"]})
    return edges


def build_map(stats: dict[str, dict], edge_rows: list[dict], leaf_rows: list[dict], states: dict[str, str]) -> dict:
    """Nœuds et arêtes de la carte, à partir des chiffres par service et des arêtes relevées."""
    nodes: dict[str, dict] = {}

    def node(name: str, kind: str = "service") -> dict:
        n = nodes.get(name)
        if n is None:
            st = stats.get(name) if kind == "service" else None
            n = nodes[name] = {"service": name, "kind": kind, "hits": (st or {}).get("hits", 0),
                               "errors": (st or {}).get("errors", 0), "p95_ms": (st or {}).get("p95_ms"),
                               "state": states.get(name) or derived_state(st)}
        return n

    for name in stats:
        node(name)
    edges: dict[tuple, dict] = {}
    for r in edge_rows:
        node(r["src"])
        node(r["dst"])
        edges[(r["src"], r["dst"])] = {"from": r["src"], "to": r["dst"], "hits": r["hits"], "errors": r["errors"],
                                       "p95_ms": r["p95_ms"]}
    for r in leaf_rows:
        name, kind = leaf_node(r)
        node(r["src"])
        target = node(name, kind)
        if kind != "service":
            target["hits"] += r["hits"]
            target["errors"] += r["errors"]
            known = [x for x in (target["p95_ms"], r["p95_ms"]) if x is not None]
            target["p95_ms"] = max(known) if known else None
            if target["hits"]:
                target["state"] = derived_state({"hits": target["hits"], "errors": target["errors"]})
        key = (r["src"], name)
        e = edges.get(key)
        if e is None:
            edges[key] = {"from": r["src"], "to": name, "hits": r["hits"], "errors": r["errors"], "p95_ms": r["p95_ms"]}
        else:
            e["hits"] += r["hits"]
            e["errors"] += r["errors"]
    return {"nodes": sorted(nodes.values(), key=lambda n: (n["kind"], n["service"])),
            "edges": sorted(edges.values(), key=lambda e: (e["from"], e["to"]))}


@router.get("/apm/service-map")
async def service_map(from_: int | None = Query(None, alias="from"), to: int | None = None,
                      p: Principal = Depends(viewer)):
    start, end = time_range(from_, to)
    s, e = ms_to_dt(start), ms_to_dt(end)
    async with rt().db.reader() as conn:
        stats = await apm_stats(conn, p.org_id, s, e)
        edge_rows = await fetch_all(conn, EDGES_SQL, (p.org_id, s, e))
        leaf_rows = await fetch_all(conn, LEAVES_SQL, (p.org_id, s, e))
        monitors = await monitor_states(conn, p.org_id)
    names = set(stats) | {r["src"] for r in edge_rows} | {r["dst"] for r in edge_rows}
    states = {}
    for name in names:
        st = worst_state([m["state"] for m in monitors if links_monitor(m, name)])
        if st:
            states[name] = st
    return ok(build_map(stats, edge_rows, leaf_rows, states))
