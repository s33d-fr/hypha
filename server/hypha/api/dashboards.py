"""Tableaux de bord (contrat § 7 ; § 30 : nouveaux widgets, dossiers, rafraîchissement, export et
import, clonage, partage public en lecture)."""
import re
import secrets
from datetime import datetime, timezone

from fastapi import APIRouter, Body, Depends, Request

from .. import audit
from ..auth.principal import Principal, admin, member, viewer
from ..db import fetch_all, fetch_one
from ..errors import QueryError, bad_request, not_found
from ..query import formulas as FX
from ..runtime import rt
from ..util import TAG_KEY_RE, jdump
from .common import done, ok
from .default_dashboards import DEFAULTS

router = APIRouter()

WIDGET_TYPES = ("timeseries", "query_value", "toplist", "log_stream", "monitor_summary", "note", "host_map",
                "check_status", "slo",
                "heatmap", "distribution", "table", "change", "scatter", "pie", "event_timeline", "alert_graph",
                "alert_value", "service_map", "network_map", "problems", "slo_list", "iframe", "image", "group")
DISPLAYS = ("line", "area", "bars", "points")
FIELDS = ("id, title, description, widgets, template_vars, folder, refresh_s, share_token, created_by, created_at, "
          "updated_at")
EXPORT_KEYS = ("title", "description", "widgets", "template_vars", "folder", "refresh_s")
EXPORT_FORMAT = "hypha-dashboard/1"
_VAR_RE = re.compile(r"^[a-z0-9_]{1,50}$")
_IMAGE_URL_RE = re.compile(r"^(?:https?://[^\s\"'<>]{1,1990}|/[^\s\"'<>]{0,1990})$")
MAX_WIDGETS = 100


def _int(v, lo, hi, what) -> int:
    if not isinstance(v, int) or isinstance(v, bool) or not lo <= v <= hi:
        raise ValueError(f"{what} doit être un entier entre {lo} et {hi}")
    return v


def _same_origin(url) -> bool:
    """Chemin de la même origine : « /status/?p=x », jamais « //autre » ni un schéma."""
    return isinstance(url, str) and url.startswith("/") and not url.startswith("//") and "\\" not in url \
        and len(url) <= 2000 and not any(c.isspace() for c in url)


def _validate_options(wid: str, wtype: str, options: dict, queries: list) -> None:
    formulas = options.get("formulas")
    if formulas is not None:
        if not isinstance(formulas, list) or len(formulas) > 20:
            raise ValueError(f"widget {wid} : options.formulas doit être une liste (20 au plus)")
        for f in formulas:
            if not isinstance(f, dict) or not isinstance(f.get("formula"), str):
                raise ValueError(f"widget {wid} : chaque formule est un objet {{id, formula, alias}}")
            try:
                FX.parse_formula(f["formula"])
            except QueryError as e:
                raise ValueError(f"widget {wid} : formule invalide : {e}")
    if "events_q" in options and not isinstance(options["events_q"], str):
        raise ValueError(f"widget {wid} : options.events_q doit être une chaîne")
    if "refresh_s" in options:
        raise ValueError(f"widget {wid} : refresh_s se règle sur le tableau, pas sur un widget")
    if wtype == "iframe" and not _same_origin(options.get("url")):
        raise ValueError(f"widget {wid} : iframe : options.url doit être un chemin de la même origine (/…)")
    if wtype == "image" and not (isinstance(options.get("url"), str) and _IMAGE_URL_RE.match(options["url"])):
        raise ValueError(f"widget {wid} : image : options.url http(s):// ou chemin /… attendu")
    if wtype in ("alert_graph", "alert_value"):
        _int(options.get("monitor_id"), 1, 2 ** 62, f"widget {wid} : options.monitor_id")
    if wtype == "scatter" and len(queries) > 2:
        raise ValueError(f"widget {wid} : scatter compare deux requêtes au plus")


def validate_widgets(widgets, depth: int = 0, seen: set | None = None) -> list:
    """Grille de 12 colonnes ; on garde les champs inconnus, l'interface peut en avoir besoin.
    Un widget `group` contient ses propres widgets (un niveau), placés dans sa grille."""
    if not isinstance(widgets, list) or len(widgets) > MAX_WIDGETS:
        raise ValueError(f"widgets doit être une liste ({MAX_WIDGETS} au plus)")
    seen = set() if seen is None else seen
    out = []
    for w in widgets:
        if not isinstance(w, dict):
            raise ValueError("chaque widget est un objet")
        wid = w.get("id")
        if not isinstance(wid, str) or not wid or len(wid) > 50 or wid in seen:
            raise ValueError("chaque widget a un id texte unique")
        seen.add(wid)
        wtype = w.get("type")
        if wtype not in WIDGET_TYPES:
            raise ValueError(f"widget {wid} : type attendu {'|'.join(WIDGET_TYPES)}")
        x = _int(w.get("x", 0), 0, 11, f"widget {wid} : x")
        width = _int(w.get("w", 6), 1, 12, f"widget {wid} : w")
        _int(w.get("y", 0), 0, 10000, f"widget {wid} : y")
        _int(w.get("h", 4), 1, 100, f"widget {wid} : h")
        if x + width > 12:
            raise ValueError(f"widget {wid} : x + w dépasse les 12 colonnes")
        queries = w.get("queries", [])
        if not isinstance(queries, list) or len(queries) > 20:
            raise ValueError(f"widget {wid} : queries doit être une liste (20 au plus)")
        for q in queries:
            if not isinstance(q, dict) or not isinstance(q.get("q", ""), str):
                raise ValueError(f"widget {wid} : chaque requête est un objet {{q, display}}")
            if "display" in q and q["display"] not in DISPLAYS:
                raise ValueError(f"widget {wid} : display attendu {'|'.join(DISPLAYS)}")
        options = w.get("options", {})
        if not isinstance(options, dict):
            raise ValueError(f"widget {wid} : options doit être un objet")
        _validate_options(wid, wtype, options, queries)
        if wtype == "group":
            if depth > 0:
                raise ValueError(f"widget {wid} : un groupe ne contient pas d'autre groupe")
            w = {**w, "widgets": validate_widgets(w.get("widgets", []), depth + 1, seen)}
        elif "widgets" in w:
            raise ValueError(f"widget {wid} : seul un widget group contient des widgets")
        out.append(w)
    return out


def validate_template_vars(tvars) -> list:
    """Variables de modèle ; `multi: true` permet plusieurs valeurs (`clé IN (…)` dans la requête)."""
    if not isinstance(tvars, list) or len(tvars) > 20:
        raise ValueError("template_vars doit être une liste (20 au plus)")
    for v in tvars:
        if not isinstance(v, dict) or not _VAR_RE.match(str(v.get("name", ""))):
            raise ValueError("chaque variable a un nom [a-z0-9_]")
        if not TAG_KEY_RE.match(str(v.get("tag", ""))):
            raise ValueError(f"variable {v.get('name')} : tag invalide")
        default = v.get("default", "*")
        if isinstance(default, list):
            if not v.get("multi") or len(default) > 100 or not all(isinstance(x, str) for x in default):
                raise ValueError(f"variable {v.get('name')} : une liste de valeurs par défaut demande multi: true")
        elif not isinstance(default, str):
            raise ValueError(f"variable {v.get('name')} : default doit être une chaîne")
        if "multi" in v and not isinstance(v["multi"], bool):
            raise ValueError(f"variable {v.get('name')} : multi doit être un booléen")
        if "defaults" in v and (not isinstance(v["defaults"], list) or len(v["defaults"]) > 100
                                or not all(isinstance(x, str) for x in v["defaults"])):
            raise ValueError(f"variable {v.get('name')} : defaults doit être une liste de chaînes")
        if "values" in v and (not isinstance(v["values"], list) or len(v["values"]) > 500
                              or not all(isinstance(x, str) for x in v["values"])):
            raise ValueError(f"variable {v.get('name')} : values doit être une liste de chaînes")
    return tvars


def _validated(data: dict, partial: bool = False) -> dict:
    try:
        out = {}
        if "title" in data or not partial:
            title = data.get("title")
            if not isinstance(title, str) or not title.strip() or len(title) > 200:
                raise ValueError("title est obligatoire (200 caractères au plus)")
            out["title"] = title.strip()
        if "description" in data or not partial:
            desc = data.get("description", "") or ""
            if not isinstance(desc, str) or len(desc) > 5000:
                raise ValueError("description doit être une chaîne")
            out["description"] = desc
        if "widgets" in data or not partial:
            out["widgets"] = validate_widgets(data.get("widgets", []))
        if "template_vars" in data or not partial:
            out["template_vars"] = validate_template_vars(data.get("template_vars", []))
        if "folder" in data or not partial:
            folder = data.get("folder", "") or ""
            if not isinstance(folder, str) or len(folder) > 200:
                raise ValueError("folder : texte de 200 caractères au plus")
            out["folder"] = folder.strip()
        if "refresh_s" in data or not partial:
            refresh = data.get("refresh_s")
            if refresh in (None, 0):
                out["refresh_s"] = None
            else:
                out["refresh_s"] = _int(refresh, 10, 86400, "refresh_s")
        return out
    except ValueError as e:
        raise bad_request(str(e), "invalid_dashboard")


async def ensure_defaults(conn, org_id: int, user_id: int | None) -> None:
    """Crée les tableaux d'office au premier GET d'une organisation vide, une seule fois.

    Le drapeau dans orgs.settings empêche de les recréer si l'organisation les supprime ensuite.
    """
    org = await fetch_one(conn, "SELECT settings ? 'default_dashboards' AS done FROM orgs WHERE id = %s", (org_id,))
    if org is None or org["done"]:
        return
    await conn.execute("SELECT pg_advisory_xact_lock(7268002, (%s)::int)", (org_id % 2_000_000_000,))
    org = await fetch_one(conn, "SELECT settings ? 'default_dashboards' AS done FROM orgs WHERE id = %s", (org_id,))
    if org["done"]:
        return
    count = await fetch_one(conn, "SELECT count(*) AS n FROM dashboards WHERE org_id = %s", (org_id,))
    if count["n"] == 0:
        for d in DEFAULTS:
            await conn.execute("""
                INSERT INTO dashboards (org_id, title, description, widgets, template_vars, created_by)
                VALUES (%s, %s, %s, %s::jsonb, %s::jsonb, %s)
            """, (org_id, d["title"], d["description"], jdump(d["widgets"]), jdump(d["template_vars"]), user_id))
    await conn.execute("UPDATE orgs SET settings = settings || '{\"default_dashboards\": true}'::jsonb WHERE id = %s",
                       (org_id,))


async def _insert(conn, org_id: int, user_id: int | None, d: dict) -> dict:
    return await fetch_one(conn, f"""
        INSERT INTO dashboards (org_id, title, description, widgets, template_vars, folder, refresh_s, created_by)
        VALUES (%s, %s, %s, %s::jsonb, %s::jsonb, %s, %s, %s) RETURNING {FIELDS}
    """, (org_id, d["title"], d["description"], jdump(d["widgets"]), jdump(d["template_vars"]), d.get("folder", ""),
          d.get("refresh_s"), user_id))


async def _load(conn, org_id: int, dashboard_id: int) -> dict:
    row = await fetch_one(conn, f"SELECT {FIELDS} FROM dashboards WHERE org_id = %s AND id = %s", (org_id, dashboard_id))
    if row is None:
        raise not_found("tableau introuvable")
    return row


@router.get("/dashboards")
async def list_dashboards(p: Principal = Depends(viewer)):
    async with rt().db.writer() as conn:
        await ensure_defaults(conn, p.org_id, p.user_id)
        rows = await fetch_all(conn, f"SELECT {FIELDS} FROM dashboards WHERE org_id = %s ORDER BY folder, title, id",
                               (p.org_id,))
    return ok(rows)


@router.post("/dashboards")
async def create_dashboard(request: Request, data: dict = Body(...), p: Principal = Depends(member)):
    d = _validated(data)
    async with rt().db.writer() as conn:
        row = await _insert(conn, p.org_id, p.user_id, d)
        await audit.record(conn, org_id=p.org_id, principal=p, action="create", object_type="dashboard",
                           object_id=row["id"], summary=f"tableau « {d['title']} » créé", after=d, request=request)
    return ok(row, 201)


@router.post("/dashboards/import")
async def import_dashboard(request: Request, data: dict = Body(...), p: Principal = Depends(member)):
    source = data.get("dashboard") if isinstance(data.get("dashboard"), dict) else data
    d = _validated({k: source[k] for k in EXPORT_KEYS if k in source})
    async with rt().db.writer() as conn:
        row = await _insert(conn, p.org_id, p.user_id, d)
        await audit.record(conn, org_id=p.org_id, principal=p, action="import", object_type="dashboard",
                           object_id=row["id"], summary=f"tableau « {d['title']} » importé", after=d, request=request)
    return ok(row, 201)


@router.get("/dashboards/{dashboard_id}")
async def get_dashboard(dashboard_id: int, p: Principal = Depends(viewer)):
    async with rt().db.reader() as conn:
        return ok(await _load(conn, p.org_id, dashboard_id))


@router.get("/dashboards/{dashboard_id}/export")
async def export_dashboard(dashboard_id: int, p: Principal = Depends(viewer)):
    async with rt().db.reader() as conn:
        row = await _load(conn, p.org_id, dashboard_id)
    return ok({"format": EXPORT_FORMAT, "exported_at": datetime.now(timezone.utc),
               **{k: row[k] for k in EXPORT_KEYS}})


@router.put("/dashboards/{dashboard_id}")
async def update_dashboard(dashboard_id: int, request: Request, data: dict = Body(...), p: Principal = Depends(member)):
    d = _validated(data, partial=True)
    if not d:
        raise bad_request("rien à modifier")
    sets, params = [], []
    for col in ("title", "description", "widgets", "template_vars", "folder", "refresh_s"):
        if col in d:
            jsonb = col in ("widgets", "template_vars")
            sets.append(f"{col} = %s" + ("::jsonb" if jsonb else ""))
            params.append(jdump(d[col]) if jsonb else d[col])
    async with rt().db.writer() as conn:
        before = await _load(conn, p.org_id, dashboard_id)
        row = await fetch_one(conn, f"UPDATE dashboards SET {', '.join(sets)}, updated_at = now() "
                                    f"WHERE org_id = %s AND id = %s RETURNING {FIELDS}",
                              params + [p.org_id, dashboard_id])
        if row is None:
            raise not_found("tableau introuvable")
        await audit.record(conn, org_id=p.org_id, principal=p, action="update", object_type="dashboard",
                           object_id=dashboard_id, summary=f"tableau « {row['title']} » modifié",
                           before={k: before[k] for k in d}, after=d, request=request)
    return ok(row)


@router.post("/dashboards/{dashboard_id}/clone")
async def clone_dashboard(dashboard_id: int, request: Request, p: Principal = Depends(member)):
    async with rt().db.writer() as conn:
        src = await _load(conn, p.org_id, dashboard_id)
        d = {k: src[k] for k in EXPORT_KEYS}
        d["title"] = (src["title"] + " (copie)")[:200]
        row = await _insert(conn, p.org_id, p.user_id, d)
        await audit.record(conn, org_id=p.org_id, principal=p, action="clone", object_type="dashboard",
                           object_id=row["id"], summary=f"tableau « {src['title']} » cloné",
                           after={"from": dashboard_id, "title": d["title"]}, request=request)
    return ok(row, 201)


@router.delete("/dashboards/{dashboard_id}")
async def delete_dashboard(dashboard_id: int, request: Request, p: Principal = Depends(member)):
    async with rt().db.writer() as conn:
        row = await fetch_one(conn, "DELETE FROM dashboards WHERE org_id = %s AND id = %s RETURNING id, title",
                              (p.org_id, dashboard_id))
        if row is None:
            raise not_found("tableau introuvable")
        await audit.record(conn, org_id=p.org_id, principal=p, action="delete", object_type="dashboard",
                           object_id=dashboard_id, summary=f"tableau « {row['title']} » supprimé", before=row,
                           request=request)
    return done()


@router.post("/dashboards/{dashboard_id}/share")
async def share_dashboard(dashboard_id: int, request: Request, p: Principal = Depends(admin)):
    async with rt().db.writer() as conn:
        current = await _load(conn, p.org_id, dashboard_id)
        token = current["share_token"]
        if not token:
            token = secrets.token_urlsafe(24)
            await conn.execute("UPDATE dashboards SET share_token = %s, updated_at = now() WHERE org_id = %s AND id = %s",
                               (token, p.org_id, dashboard_id))
            await audit.record(conn, org_id=p.org_id, principal=p, action="share", object_type="dashboard",
                               object_id=dashboard_id, summary=f"tableau « {current['title']} » partagé en lecture",
                               request=request)
    url = f"{rt().settings.public_url}/share/?t={token}"
    return ok({"share_token": token, "url": url})


@router.delete("/dashboards/{dashboard_id}/share")
async def unshare_dashboard(dashboard_id: int, request: Request, p: Principal = Depends(admin)):
    async with rt().db.writer() as conn:
        current = await _load(conn, p.org_id, dashboard_id)
        await conn.execute("UPDATE dashboards SET share_token = NULL, updated_at = now() WHERE org_id = %s AND id = %s",
                           (p.org_id, dashboard_id))
        if current["share_token"]:
            await audit.record(conn, org_id=p.org_id, principal=p, action="unshare", object_type="dashboard",
                               object_id=dashboard_id, summary=f"partage du tableau « {current['title']} » retiré",
                               request=request)
    return done()
