"""Journaux, ajouts de la 0.4.0 (contrat § 23) : motifs, contexte d'une ligne, vues enregistrées,
métriques tirées des journaux, masquage des données sensibles."""
import re
from datetime import timedelta

from fastapi import APIRouter, Body, Depends, Query, Request

from .. import audit, retention
from ..auth.principal import Principal, admin, member, viewer
from ..db import fetch_all, fetch_one
from ..errors import QueryError, bad_request, forbidden, not_found
from ..ingest import extras
from ..ingest import sensitive as S
from ..ingest.log_metrics import compile_metric
from ..query import logs_ql
from ..query.patterns import group_patterns
from ..runtime import rt
from ..util import jdump, ms_to_dt
from .common import done, ok, time_range
from .logs import LOG_FIELDS, compile_or_400

router = APIRouter()

PATTERN_SAMPLE = 20_000
VIEW_PAGES = ("logs", "traces", "metrics", "hosts", "processes", "errors")
VIEW_FIELDS = "id, page, name, state, shared, created_by, created_at"
METRIC_FIELDS = "id, name, query, group_by, aggregation, attribute, enabled, created_by, created_at, updated_at"
RULE_FIELDS = ("id, name, builtin, pattern, replacement, scope_query, targets, enabled, position, matched_total, "
               "created_at, updated_at")


# ── motifs ─────────────────────────────────────────────────────────────────────

@router.get("/logs/patterns")
async def log_patterns(q: str = "", from_: int | None = Query(None, alias="from"), to: int | None = None,
                       limit: int = Query(50, ge=1, le=500), p: Principal = Depends(viewer)):
    start, end, rel = await retention.window(p.org_id, "logs", *time_range(from_, to))
    frag, qparams = compile_or_400(q)
    async with rt().db.reader() as conn:
        rows = await fetch_all(conn, f"""
            SELECT message, status, service, ts FROM {rel}
             WHERE org_id = %s AND ts >= %s AND ts < %s AND {frag}
             ORDER BY ts DESC LIMIT %s
        """, [p.org_id, ms_to_dt(start), ms_to_dt(end)] + qparams + [PATTERN_SAMPLE])
    return ok(group_patterns(rows, limit))


# ── contexte d'une ligne ───────────────────────────────────────────────────────

@router.get("/logs/{log_id}/context")
async def log_context(log_id: int, ts: int, before: int = Query(20, ge=0, le=500), after: int = Query(20, ge=0, le=500),
                      p: Principal = Depends(viewer)):
    at = ms_to_dt(ts)
    # Conservation de la formule (§ 52) : une ligne hors de la période lisible n'existe plus pour l'espace.
    lo, _, rel = await retention.window(p.org_id, "logs", ts - 86_400_000, ts + 1000)
    if lo > ts:
        raise not_found("ligne de journal introuvable (hors de la période conservée par la formule)")
    async with rt().db.reader() as conn:
        line = await fetch_one(conn, f"""
            SELECT {LOG_FIELDS} FROM {rel} WHERE org_id = %s AND id = %s AND ts >= %s AND ts <= %s
        """, (p.org_id, log_id, at - timedelta(seconds=1), at + timedelta(seconds=1)))
        if line is None:
            raise not_found("ligne de journal introuvable")
        if line["host_id"] is not None:
            same, sparams = "host_id = %s", [line["host_id"]]
        else:
            same, sparams = "host_id IS NULL AND hostname IS NOT DISTINCT FROM %s", [line["hostname"]]
        same += " AND service IS NOT DISTINCT FROM %s"
        sparams.append(line["service"])
        older = await fetch_all(conn, f"""
            SELECT {LOG_FIELDS} FROM {rel}
             WHERE org_id = %s AND {same} AND ts >= %s AND (ts, id) < (%s, %s)
             ORDER BY ts DESC, id DESC LIMIT %s
        """, [p.org_id] + sparams + [max(line["ts"] - timedelta(days=1), ms_to_dt(lo)), line["ts"], line["id"], before])
        newer = await fetch_all(conn, f"""
            SELECT {LOG_FIELDS} FROM {rel}
             WHERE org_id = %s AND {same} AND ts <= %s AND (ts, id) > (%s, %s)
             ORDER BY ts, id LIMIT %s
        """, [p.org_id] + sparams + [line["ts"] + timedelta(days=1), line["ts"], line["id"], after])
    return ok({"line": line, "before": list(reversed(older)), "after": newer})


# ── vues enregistrées ──────────────────────────────────────────────────────────

@router.get("/views")
async def list_views(page: str = "", p: Principal = Depends(viewer)):
    params: list = [p.org_id, p.user_id]
    cond = ""
    if page:
        if page not in VIEW_PAGES:
            raise bad_request(f"page attendue : {'|'.join(VIEW_PAGES)}")
        cond = "AND page = %s"
        params.append(page)
    async with rt().db.reader() as conn:
        rows = await fetch_all(conn, f"""
            SELECT {VIEW_FIELDS} FROM saved_views
             WHERE org_id = %s AND (shared OR created_by IS NOT DISTINCT FROM %s) {cond}
             ORDER BY page, name, id
        """, params)
    return ok(rows)


@router.post("/views")
async def create_view(request: Request, data: dict = Body(...), p: Principal = Depends(member)):
    page, name, state = data.get("page"), data.get("name"), data.get("state", {})
    if page not in VIEW_PAGES:
        raise bad_request(f"page attendue : {'|'.join(VIEW_PAGES)}")
    if not isinstance(name, str) or not name.strip() or len(name) > 200:
        raise bad_request("name est obligatoire (200 caractères au plus)")
    if not isinstance(state, dict) or len(jdump(state)) > 50_000:
        raise bad_request("state doit être un objet (50 Kio au plus)")
    shared = data.get("shared", True)
    if not isinstance(shared, bool):
        raise bad_request("shared doit être un booléen")
    async with rt().db.writer() as conn:
        row = await fetch_one(conn, f"""
            INSERT INTO saved_views (org_id, page, name, state, shared, created_by)
            VALUES (%s, %s, %s, %s::jsonb, %s, %s) RETURNING {VIEW_FIELDS}
        """, (p.org_id, page, name.strip(), jdump(state), shared, p.user_id))
        await audit.record(conn, org_id=p.org_id, principal=p, action="create", object_type="view",
                           object_id=row["id"], summary=f"vue « {row['name']} » ({page}) enregistrée", after=row,
                           request=request)
    return ok(row, 201)


@router.delete("/views/{view_id}")
async def delete_view(view_id: int, request: Request, p: Principal = Depends(member)):
    async with rt().db.writer() as conn:
        row = await fetch_one(conn, f"SELECT {VIEW_FIELDS} FROM saved_views WHERE org_id = %s AND id = %s",
                              (p.org_id, view_id))
        if row is None or (not row["shared"] and row["created_by"] != p.user_id):
            raise not_found("vue introuvable")
        if row["created_by"] != p.user_id and not p.at_least("admin"):
            raise forbidden("seul l'auteur ou un administrateur supprime une vue")
        await conn.execute("DELETE FROM saved_views WHERE org_id = %s AND id = %s", (p.org_id, view_id))
        await audit.record(conn, org_id=p.org_id, principal=p, action="delete", object_type="view",
                           object_id=view_id, summary=f"vue « {row['name']} » supprimée", before=row, request=request)
    return done()


# ── métriques tirées des journaux ──────────────────────────────────────────────

def validate_log_metric(data: dict, current: dict | None = None) -> dict:
    keys = ("name", "query", "group_by", "aggregation", "attribute", "enabled")
    merged = {"name": None, "query": "", "group_by": [], "aggregation": "count", "attribute": None, "enabled": True,
              **({k: current[k] for k in keys} if current else {}), **{k: v for k, v in data.items() if k in keys}}
    name = merged["name"]
    if not isinstance(name, str) or not re.fullmatch(r"logs\.[a-z0-9_.]{1,120}", name.strip()):
        raise bad_request("name : « logs. » suivi de [a-z0-9_.] (ex. logs.ssh.echecs)", "invalid_log_metric")
    if not isinstance(merged["query"], str) or len(merged["query"]) > 2000:
        raise bad_request("query : requête de journaux (2000 caractères au plus)", "invalid_log_metric")
    if merged["aggregation"] not in ("count", "distribution"):
        raise bad_request("aggregation attendue : count|distribution", "invalid_log_metric")
    group_by = merged["group_by"] or []
    if not isinstance(group_by, list) or not all(isinstance(g, str) for g in group_by):
        raise bad_request("group_by : liste de clés (service host status source @attribut)", "invalid_log_metric")
    attribute = merged["attribute"]
    if merged["aggregation"] == "distribution":
        if not isinstance(attribute, str) or not attribute.strip():
            raise bad_request("attribute (chemin numérique, ex. @duration) est obligatoire pour une distribution",
                              "invalid_log_metric")
        attribute = "@" + attribute.strip().removeprefix("@")
    else:
        attribute = None
    if not isinstance(merged["enabled"], bool):
        raise bad_request("enabled doit être un booléen", "invalid_log_metric")
    out = {"name": name.strip(), "query": merged["query"].strip(), "group_by": [g.strip() for g in group_by],
           "aggregation": merged["aggregation"], "attribute": attribute, "enabled": merged["enabled"]}
    try:
        compile_metric({**out, "id": 0})
    except QueryError as e:
        raise bad_request(str(e), "invalid_query")
    return out


@router.get("/logs/metrics")
async def list_log_metrics(p: Principal = Depends(viewer)):
    async with rt().db.reader() as conn:
        return ok(await fetch_all(conn, f"SELECT {METRIC_FIELDS} FROM log_metrics WHERE org_id = %s ORDER BY name",
                                  (p.org_id,)))


@router.post("/logs/metrics")
async def create_log_metric(request: Request, data: dict = Body(...), p: Principal = Depends(member)):
    m = validate_log_metric(data)
    async with rt().db.writer() as conn:
        exists = await fetch_one(conn, "SELECT id FROM log_metrics WHERE org_id = %s AND name = %s", (p.org_id, m["name"]))
        if exists:
            raise bad_request(f"une métrique « {m['name']} » existe déjà", "duplicate")
        row = await fetch_one(conn, f"""
            INSERT INTO log_metrics (org_id, name, query, group_by, aggregation, attribute, enabled, created_by)
            VALUES (%s, %s, %s, %s::text[], %s, %s, %s, %s) RETURNING {METRIC_FIELDS}
        """, (p.org_id, m["name"], m["query"], m["group_by"], m["aggregation"], m["attribute"], m["enabled"], p.user_id))
        await audit.record(conn, org_id=p.org_id, principal=p, action="create", object_type="log_metric",
                           object_id=row["id"], summary=f"métrique {m['name']} créée", after=m, request=request)
    extras.forget_org(rt(), p.org_id)
    return ok(row, 201)


@router.put("/logs/metrics/{metric_id}")
async def update_log_metric(metric_id: int, request: Request, data: dict = Body(...), p: Principal = Depends(member)):
    async with rt().db.writer() as conn:
        current = await fetch_one(conn, f"SELECT {METRIC_FIELDS} FROM log_metrics WHERE org_id = %s AND id = %s",
                                  (p.org_id, metric_id))
        if current is None:
            raise not_found("métrique de journaux introuvable")
        m = validate_log_metric(data, current)
        row = await fetch_one(conn, f"""
            UPDATE log_metrics SET name = %s, query = %s, group_by = %s::text[], aggregation = %s, attribute = %s,
                   enabled = %s, updated_at = now()
             WHERE org_id = %s AND id = %s RETURNING {METRIC_FIELDS}
        """, (m["name"], m["query"], m["group_by"], m["aggregation"], m["attribute"], m["enabled"], p.org_id, metric_id))
        await audit.record(conn, org_id=p.org_id, principal=p, action="update", object_type="log_metric",
                           object_id=metric_id, summary=f"métrique {m['name']} modifiée", before=current, after=m,
                           request=request)
    extras.forget_org(rt(), p.org_id)
    return ok(row)


@router.delete("/logs/metrics/{metric_id}")
async def delete_log_metric(metric_id: int, request: Request, p: Principal = Depends(member)):
    async with rt().db.writer() as conn:
        row = await fetch_one(conn, "DELETE FROM log_metrics WHERE org_id = %s AND id = %s RETURNING id, name",
                              (p.org_id, metric_id))
        if row is None:
            raise not_found("métrique de journaux introuvable")
        await audit.record(conn, org_id=p.org_id, principal=p, action="delete", object_type="log_metric",
                           object_id=metric_id, summary=f"métrique {row['name']} supprimée", before=row, request=request)
    extras.forget_org(rt(), p.org_id)
    return done()


# ── masquage des données sensibles ─────────────────────────────────────────────

def validate_rule(data: dict, current: dict | None = None) -> dict:
    keys = ("name", "builtin", "pattern", "replacement", "scope_query", "targets", "enabled")
    merged = {"name": None, "builtin": None, "pattern": None, "replacement": None, "scope_query": "",
              "targets": list(S.TARGETS), "enabled": True,
              **({k: current[k] for k in keys} if current else {}), **{k: v for k, v in data.items() if k in keys}}
    builtin = merged["builtin"] or None
    if builtin is not None and builtin not in S.BUILTINS:
        raise bad_request(f"builtin attendu : {'|'.join(S.BUILTIN_ORDER)}", "invalid_rule")
    name = merged["name"] or (S.BUILTIN_LABELS.get(builtin) if builtin else None)
    if not isinstance(name, str) or not name.strip() or len(name) > 200:
        raise bad_request("name est obligatoire (200 caractères au plus)", "invalid_rule")
    pattern = merged["pattern"] if builtin is None else None
    if builtin is None:
        try:
            S.compile_pattern(pattern)
        except QueryError as e:
            raise bad_request(str(e), "invalid_pattern")
    replacement = merged["replacement"]
    if replacement is None or replacement == "":
        replacement = S.replacement_for(builtin or re.sub(r"[^a-z0-9_]+", "_", name.strip().lower()).strip("_")[:40]
                                        or "propre")
    if not isinstance(replacement, str) or len(replacement) > 100:
        raise bad_request("replacement : texte de 100 caractères au plus", "invalid_rule")
    scope = merged["scope_query"] or ""
    if not isinstance(scope, str) or len(scope) > 2000:
        raise bad_request("scope_query : requête de journaux (2000 caractères au plus)", "invalid_rule")
    if scope.strip():
        try:
            logs_ql.compile_query(scope)
        except QueryError as e:
            raise bad_request(f"scope_query : {e}", "invalid_query")
    targets = merged["targets"]
    if not isinstance(targets, list) or not targets or not set(targets) <= set(S.TARGETS):
        raise bad_request("targets : liste parmi message, attributes", "invalid_rule")
    if not isinstance(merged["enabled"], bool):
        raise bad_request("enabled doit être un booléen", "invalid_rule")
    return {"name": name.strip(), "builtin": builtin, "pattern": pattern, "replacement": replacement,
            "scope_query": scope.strip(), "targets": sorted(set(targets), key=S.TARGETS.index),
            "enabled": merged["enabled"]}


@router.get("/logs/sensitive")
async def list_rules(p: Principal = Depends(admin)):
    async with rt().db.writer() as conn:
        await S.seed_builtin(conn, p.org_id)
        rows = await fetch_all(conn, f"SELECT {RULE_FIELDS} FROM sensitive_rules WHERE org_id = %s "
                                     "ORDER BY position, id", (p.org_id,))
    return ok(rows)


@router.post("/logs/sensitive")
async def create_rule(request: Request, data: dict = Body(...), p: Principal = Depends(admin)):
    r = validate_rule(data)
    async with rt().db.writer() as conn:
        row = await fetch_one(conn, f"""
            INSERT INTO sensitive_rules (org_id, name, builtin, pattern, replacement, scope_query, targets, enabled,
                                         position)
            VALUES (%s, %s, %s, %s, %s, %s, %s::text[], %s,
                    (SELECT coalesce(max(position) + 1, 0) FROM sensitive_rules WHERE org_id = %s))
            RETURNING {RULE_FIELDS}
        """, (p.org_id, r["name"], r["builtin"], r["pattern"], r["replacement"], r["scope_query"], r["targets"],
              r["enabled"], p.org_id))
        await audit.record(conn, org_id=p.org_id, principal=p, action="create", object_type="sensitive_rule",
                           object_id=row["id"], summary=f"règle de masquage « {r['name']} » créée", after=r,
                           request=request)
    extras.forget_org(rt(), p.org_id)
    return ok(row, 201)


@router.post("/logs/sensitive/reorder")
async def reorder_rules(request: Request, data: dict = Body(...), p: Principal = Depends(admin)):
    ids = data.get("ids")
    if not isinstance(ids, list) or not all(isinstance(i, int) and not isinstance(i, bool) for i in ids) \
            or len(ids) != len(set(ids)):
        raise bad_request("ids : liste d'identifiants de règles, sans doublon")
    async with rt().db.writer() as conn:
        existing = [r["id"] for r in await fetch_all(
            conn, "SELECT id FROM sensitive_rules WHERE org_id = %s ORDER BY position, id", (p.org_id,))]
        unknown = set(ids) - set(existing)
        if unknown:
            raise bad_request(f"règles inconnues : {sorted(unknown)}")
        # Les règles absentes de la liste gardent leur ordre relatif, après celles qui sont listées.
        order = ids + [i for i in existing if i not in set(ids)]
        await conn.execute("""
            UPDATE sensitive_rules r SET position = x.pos, updated_at = now()
              FROM unnest(%s::bigint[], %s::int[]) AS x(id, pos)
             WHERE r.id = x.id AND r.org_id = %s
        """, (order, list(range(len(order))), p.org_id))
        rows = await fetch_all(conn, f"SELECT {RULE_FIELDS} FROM sensitive_rules WHERE org_id = %s "
                                     "ORDER BY position, id", (p.org_id,))
        await audit.record(conn, org_id=p.org_id, principal=p, action="reorder", object_type="sensitive_rule",
                           summary="ordre des règles de masquage modifié", before={"ids": existing},
                           after={"ids": order}, request=request)
    extras.forget_org(rt(), p.org_id)
    return ok(rows)


@router.post("/logs/sensitive/test")
async def test_rules(data: dict = Body(...), p: Principal = Depends(admin)):
    text = data.get("text")
    if not isinstance(text, str) or len(text) > 32 * 1024:
        raise bad_request("text est obligatoire (32 Kio au plus)")
    rules = []
    if isinstance(data.get("rule"), dict):
        r = validate_rule(data["rule"])
        rules.append(S.compile_rule({**r, "id": 0}))
    else:
        include_disabled = data.get("include_disabled") is True
        async with rt().db.reader() as conn:
            rows = await fetch_all(conn, f"SELECT {RULE_FIELDS} FROM sensitive_rules WHERE org_id = %s "
                                         f"{'' if include_disabled else 'AND enabled'} ORDER BY position, id",
                                   (p.org_id,))
        for row in rows:
            try:
                rules.append(S.compile_rule(row))
            except QueryError:
                continue
    masked, hits = S.mask_text(text, [r for r in rules if r.on_message])
    names = {r.id: r.name for r in rules}
    return ok({"masked": masked, "matched": [{"rule_id": rid or None, "name": names[rid], "count": n}
                                             for rid, n in hits.items()]})


@router.put("/logs/sensitive/{rule_id}")
async def update_rule(rule_id: int, request: Request, data: dict = Body(...), p: Principal = Depends(admin)):
    async with rt().db.writer() as conn:
        current = await fetch_one(conn, f"SELECT {RULE_FIELDS} FROM sensitive_rules WHERE org_id = %s AND id = %s",
                                  (p.org_id, rule_id))
        if current is None:
            raise not_found("règle de masquage introuvable")
        r = validate_rule(data, current)
        row = await fetch_one(conn, f"""
            UPDATE sensitive_rules SET name = %s, builtin = %s, pattern = %s, replacement = %s, scope_query = %s,
                   targets = %s::text[], enabled = %s, updated_at = now()
             WHERE org_id = %s AND id = %s RETURNING {RULE_FIELDS}
        """, (r["name"], r["builtin"], r["pattern"], r["replacement"], r["scope_query"], r["targets"], r["enabled"],
              p.org_id, rule_id))
        await audit.record(conn, org_id=p.org_id, principal=p, action="update", object_type="sensitive_rule",
                           object_id=rule_id, summary=f"règle de masquage « {r['name']} » modifiée",
                           before=current, after=r, request=request)
    extras.state_of(rt()).bad_rules.discard(rule_id)
    extras.forget_org(rt(), p.org_id)
    return ok(row)


@router.delete("/logs/sensitive/{rule_id}")
async def delete_rule(rule_id: int, request: Request, p: Principal = Depends(admin)):
    async with rt().db.writer() as conn:
        row = await fetch_one(conn, "DELETE FROM sensitive_rules WHERE org_id = %s AND id = %s RETURNING id, name, builtin",
                              (p.org_id, rule_id))
        if row is None:
            raise not_found("règle de masquage introuvable")
        await audit.record(conn, org_id=p.org_id, principal=p, action="delete", object_type="sensitive_rule",
                           object_id=rule_id, summary=f"règle de masquage « {row['name']} » supprimée", before=row,
                           request=request)
    extras.forget_org(rt(), p.org_id)
    return done()
