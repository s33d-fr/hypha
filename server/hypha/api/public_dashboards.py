"""Tableaux partagés en lecture publique (contrat § 30), sans authentification.

`GET /api/public/dashboards/{token}` rend le tableau (sans auteur ni identifiants internes) ;
`POST /api/public/dashboards/{token}/query` prend le corps de `/metrics/query`, mais chaque requête
et chaque formule doit être celle d'un widget du tableau, variables de modèle à leur valeur par
défaut (comparaison sur la forme analysée, pas sur le texte) : le lien ne lit rien d'autre.
Débit borné par adresse.
"""
import re

from fastapi import APIRouter, Request

from ..db import fetch_one
from ..errors import ApiError, QueryError, bad_request, not_found
from ..query import formulas as FX
from ..runtime import rt
from ..util import RateLimiter, TTLCache
from .common import ok
from .metrics import QueryBody, run_query

router = APIRouter()

_TOKEN_RE = re.compile(r"^[A-Za-z0-9_\-]{16,64}$")
_limiter = RateLimiter(120)
_cache = TTLCache(30)
PUBLIC_FIELDS = ("title", "description", "widgets", "template_vars", "folder", "refresh_s")


def _client(request: Request) -> str:
    fwd = request.headers.get("x-forwarded-for", "").split(",")[0].strip()
    return fwd or (request.client.host if request.client else "?")


def _tag_filter(tag: str, value: str) -> str:
    """Même écriture que `tagFilter` de l'interface : guillemets si la valeur porte , { } ou un blanc."""
    if re.fullmatch(r"[^,{}\s]+", value):
        return f"{tag}:{value}"
    return f'{tag}:"' + value.replace('"', '\\"') + '"'


def substitute(q: str, tvars: list) -> str:
    """Variables à leur valeur par défaut, exactement comme l'interface (`applyVars` de widgets.js et
    share.js) : `$host` → `host:x`, rien si `*` ; une variable multiple n'est gardée que si elle n'a
    qu'une valeur par défaut (sinon elle vaut `*`)."""
    out = str(q or "")
    for v in tvars or []:
        name = v.get("name")
        if not name:
            continue
        value = v.get("default") or "*"
        if v.get("multi") and isinstance(v.get("defaults"), list) and v["defaults"]:
            value = v["defaults"]
        if isinstance(value, list):
            value = value[0] if len(value) == 1 else "*"
        tag = v.get("tag") or name
        repl = "\x00" if value in ("", "*") else _tag_filter(tag, str(value))
        out = re.sub(r"\$" + re.escape(name) + r"(?![\w.])", lambda m: repl, out)

    def braces(m):
        parts = [x.strip() for x in m.group(1).split(",")]
        parts = [x for x in parts if x and x != "\x00"]
        return "{" + (",".join(parts) if parts else "*") + "}"
    out = re.sub(r"\{([^}]*)\}", braces, out)
    return out.replace("\x00", "")


def _widgets(widgets: list):
    for w in widgets or []:
        yield w
        if w.get("type") == "group":
            yield from _widgets(w.get("widgets") or [])


def allowed(dashboard: dict) -> tuple[set, set]:
    """Formes canoniques des requêtes et des formules que le tableau partagé peut demander."""
    queries, formulas = set(), set()
    tvars = dashboard.get("template_vars") or []
    for w in _widgets(dashboard.get("widgets") or []):
        for q in w.get("queries") or []:
            for text in {q.get("q", ""), substitute(q.get("q", ""), tvars)}:
                try:
                    queries.add(FX.canonical(FX.parse_query(text)))
                except QueryError:
                    continue
        for f in (w.get("options") or {}).get("formulas") or []:
            try:
                formulas.add(FX.canonical(FX.parse_formula(f.get("formula", ""))))
            except QueryError:
                continue
    return queries, formulas


async def _dashboard(token: str, request: Request) -> dict:
    if not _TOKEN_RE.match(token or ""):
        raise not_found("tableau partagé introuvable")
    if not _limiter.allow(_client(request)):
        raise ApiError(429, "rate_limited", "trop de requêtes")
    row = _cache.get(token)
    if row is None:
        async with rt().db.reader() as conn:
            row = await fetch_one(conn, "SELECT org_id, title, description, widgets, template_vars, folder, refresh_s "
                                        "FROM dashboards WHERE share_token = %s", (token,))
        _cache.set(token, row or False)
    if not row:
        raise not_found("tableau partagé introuvable")
    return row


@router.get("/api/public/dashboards/{token}")
async def public_dashboard(token: str, request: Request):
    row = await _dashboard(token, request)
    return ok({k: row[k] for k in PUBLIC_FIELDS})


@router.post("/api/public/dashboards/{token}/query")
async def public_query(token: str, body: QueryBody, request: Request):
    row = await _dashboard(token, request)
    queries, formulas = allowed(row)
    for item in body.queries:
        try:
            shape = FX.canonical(FX.parse_query(item.q))
        except QueryError as e:
            raise bad_request(f"requête « {item.id} » : {e}", "invalid_query")
        if shape not in queries:
            raise ApiError(403, "query_not_allowed", f"requête « {item.id} » absente des widgets de ce tableau")
    for f in body.formulas:
        try:
            shape = FX.canonical(FX.parse_formula(f.formula))
        except QueryError as e:
            raise bad_request(f"formule « {f.id} » : {e}", "invalid_query")
        if shape not in formulas:
            raise ApiError(403, "query_not_allowed", f"formule « {f.id} » absente des widgets de ce tableau")
    async with rt().db.reader() as conn:
        return ok(await run_query(conn, row["org_id"], body))
