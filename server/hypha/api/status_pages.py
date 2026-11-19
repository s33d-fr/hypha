"""Pages de statut (contrat § 21) : gestion par les administrateurs, lecture publique sans authentification."""
import re

from fastapi import APIRouter, Body, Depends, Request
from fastapi.responses import JSONResponse

from .. import audit, quotas
from ..auth.principal import Principal, admin
from ..db import fetch_all, fetch_one
from ..errors import ApiError, bad_request, not_found
from ..runtime import rt
from ..status import service as S
from ..util import TTLCache, jdump
from .common import HyphaJSON, done, ok

router = APIRouter()
public_router = APIRouter()

FIELDS = "id, slug, title, description, components, public, created_at, updated_at"
SLUG_RE = re.compile(r"^[a-z0-9][a-z0-9-]{1,62}$")
KINDS = {"monitor": "monitors", "synthetic": "synthetic_tests", "slo": "slos", "host": "hosts"}
CACHE_S = 30
_cache = TTLCache(CACHE_S, max_items=10_000)


def _present(row: dict) -> dict:
    return {**row, "public_url": f"{rt().settings.public_url}/status/?p={row['slug']}"}


async def _validated(conn, org_id: int, data: dict, current: dict | None = None) -> dict:
    merged = {**(current or {"description": "", "components": [], "public": False}),
              **{k: v for k, v in data.items() if k in ("slug", "title", "description", "components", "public")}}
    slug = merged.get("slug")
    if not isinstance(slug, str) or not SLUG_RE.match(slug):
        raise bad_request("slug : 2 à 63 caractères a-z, 0-9 et tiret, sans tiret en tête", "invalid_status_page")
    title = merged.get("title")
    if not isinstance(title, str) or not title.strip() or len(title) > 200:
        raise bad_request("title est obligatoire (200 caractères au plus)", "invalid_status_page")
    desc = merged.get("description") or ""
    if not isinstance(desc, str) or len(desc) > 5000:
        raise bad_request("description : texte de 5 000 caractères au plus", "invalid_status_page")
    if not isinstance(merged.get("public"), bool):
        raise bad_request("public doit être un booléen", "invalid_status_page")
    comps = merged.get("components") or []
    if not isinstance(comps, list) or len(comps) > 100:
        raise bad_request("components : liste de 100 composants au plus", "invalid_status_page")
    out_comps, wanted = [], {k: set() for k in KINDS}
    for c in comps:
        if not isinstance(c, dict):
            raise bad_request("chaque composant est un objet {name, group, kind, ref_id}", "invalid_status_page")
        name, group, kind, ref = c.get("name"), c.get("group") or "", c.get("kind"), c.get("ref_id")
        if not isinstance(name, str) or not name.strip() or len(name) > 200:
            raise bad_request("composant : name est obligatoire (200 caractères au plus)", "invalid_status_page")
        if not isinstance(group, str) or len(group) > 100:
            raise bad_request("composant : group (texte, 100 caractères au plus)", "invalid_status_page")
        if kind not in KINDS:
            raise bad_request(f"composant : kind attendu {'|'.join(KINDS)}", "invalid_status_page")
        if isinstance(ref, bool) or not isinstance(ref, int):
            raise bad_request("composant : ref_id (entier) est obligatoire", "invalid_status_page")
        wanted[kind].add(ref)
        out_comps.append({"name": name.strip(), "group": group.strip(), "kind": kind, "ref_id": ref})
    for kind, refs in wanted.items():
        if refs:
            rows = await fetch_all(conn, f"SELECT id FROM {KINDS[kind]} WHERE org_id = %s AND id = ANY(%s::bigint[])",
                                   (org_id, sorted(refs)))
            missing = refs - {r["id"] for r in rows}
            if missing:
                raise bad_request(f"composants inconnus ({kind}) : {sorted(missing)}", "invalid_status_page")
    return {"slug": slug, "title": title.strip(), "description": desc, "components": out_comps,
            "public": merged["public"]}


async def _slug_free(conn, slug: str, page_id: int | None) -> None:
    row = await fetch_one(conn, "SELECT id FROM status_pages WHERE slug = %s", (slug,))
    if row and row["id"] != page_id:
        raise ApiError(409, "slug_taken", "ce slug est déjà pris")


async def _load(conn, org_id: int, page_id: int) -> dict:
    row = await fetch_one(conn, f"SELECT {FIELDS} FROM status_pages WHERE org_id = %s AND id = %s", (org_id, page_id))
    if row is None:
        raise not_found("page de statut introuvable")
    return row


@router.get("/status-pages")
async def list_pages(p: Principal = Depends(admin)):
    async with rt().db.reader() as conn:
        rows = await fetch_all(conn, f"SELECT {FIELDS} FROM status_pages WHERE org_id = %s ORDER BY title, id",
                               (p.org_id,))
    return ok([_present(r) for r in rows])


@router.post("/status-pages")
async def create_page(request: Request, data: dict = Body(...), p: Principal = Depends(admin)):
    async with rt().db.writer() as conn:
        page = await _validated(conn, p.org_id, data)
        await quotas.check_status_page(conn, p.org_id)      # limite de formule (§ 52)
        await _slug_free(conn, page["slug"], None)
        row = await fetch_one(conn, f"""
            INSERT INTO status_pages (org_id, slug, title, description, components, public)
            VALUES (%s, %s, %s, %s, %s::jsonb, %s) RETURNING {FIELDS}
        """, (p.org_id, page["slug"], page["title"], page["description"], jdump(page["components"]), page["public"]))
        await audit.record(conn, org_id=p.org_id, principal=p, action="create", object_type="status_page",
                           object_id=row["id"], summary=f"page de statut « {page['title']} » ({page['slug']})",
                           after=page, request=request)
    _cache.pop(page["slug"])
    return ok(_present(row), 201)


@router.get("/status-pages/{page_id}")
async def get_page(page_id: int, p: Principal = Depends(admin)):
    async with rt().db.reader() as conn:
        return ok(_present(await _load(conn, p.org_id, page_id)))


@router.put("/status-pages/{page_id}")
async def update_page(page_id: int, request: Request, data: dict = Body(...), p: Principal = Depends(admin)):
    async with rt().db.writer() as conn:
        current = await _load(conn, p.org_id, page_id)
        page = await _validated(conn, p.org_id, data, {k: current[k] for k in
                                                       ("slug", "title", "description", "components", "public")})
        await _slug_free(conn, page["slug"], page_id)
        row = await fetch_one(conn, f"""
            UPDATE status_pages SET slug = %s, title = %s, description = %s, components = %s::jsonb, public = %s,
                   updated_at = now()
             WHERE org_id = %s AND id = %s RETURNING {FIELDS}
        """, (page["slug"], page["title"], page["description"], jdump(page["components"]), page["public"],
              p.org_id, page_id))
        await audit.record(conn, org_id=p.org_id, principal=p, action="update", object_type="status_page",
                           object_id=page_id, summary=f"page de statut « {page['title']} » modifiée",
                           before={k: current[k] for k in ("slug", "title", "description", "components", "public")},
                           after=page, request=request)
    _cache.pop(current["slug"])
    _cache.pop(page["slug"])
    return ok(_present(row))


@router.delete("/status-pages/{page_id}")
async def delete_page(page_id: int, request: Request, p: Principal = Depends(admin)):
    async with rt().db.writer() as conn:
        row = await fetch_one(conn, "DELETE FROM status_pages WHERE org_id = %s AND id = %s RETURNING id, slug, title",
                              (p.org_id, page_id))
        if row is None:
            raise not_found("page de statut introuvable")
        await audit.record(conn, org_id=p.org_id, principal=p, action="delete", object_type="status_page",
                           object_id=page_id, summary=f"page de statut « {row['title']} » supprimée", before=row,
                           request=request)
    _cache.pop(row["slug"])
    return done()


@public_router.get("/api/public/status/{slug}")
async def public_status(slug: str):
    """Sans authentification ; 404 si la page n'existe pas ou n'est pas publique. Cache de 30 s par nœud."""
    if not SLUG_RE.match(slug):
        raise not_found("page de statut introuvable")
    cached = _cache.get(slug)
    if cached is None:
        async with rt().db.reader() as conn:
            page = await fetch_one(conn, "SELECT id, org_id, title, description, components FROM status_pages "
                                         "WHERE slug = %s AND public", (slug,))
            cached = await S.build(conn, page) if page else False
        _cache.set(slug, cached)
    if cached is False:
        return JSONResponse({"error": "not_found", "detail": "page de statut introuvable"}, status_code=404,
                            headers={"Cache-Control": f"public, max-age={CACHE_S}"})
    return HyphaJSON(cached, headers={"Cache-Control": f"public, max-age={CACHE_S}"})
