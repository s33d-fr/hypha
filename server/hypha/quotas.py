"""Limites de formule appliquées (contrat § 52) : à l'ingestion et dans l'API, jamais en silence.

Un refus rend HTTP 402 `{"error":"plan_limit","detail":…,"quota":…,"limit":…,"plan":…}` :
- l'agent abandonne le corps sans le renvoyer (pas de tampon qui déborderait à minuit) et journalise
  le détail une fois par minute ; un client OTLP fait de même (402 n'est pas « à réessayer ») ;
- l'interface montre le détail traduit et propose la formule Pro ;
- les hôtes refusés sont inscrits dans `org_refused_hosts`, les lignes et spans refusés comptés dans
  `usage_daily` : la page Formule les montre.

Hôte « actif » : vu dans la dernière heure. Quand une limite d'hôtes est atteinte, les hôtes les
plus anciens de l'espace (plus petit identifiant) gardent leur place, les autres sont refusés ; un
hôte refusé n'actualise pas `last_seen`, il ne prend donc la place de personne.
"""
import logging
from datetime import datetime, timezone

from .db import fetch_one
from .errors import ApiError
from .plans import MB, OrgPlan, get_plan
from .util import TTLCache

log = logging.getLogger("hypha.quotas")

PLAN_NAMES = {"decouverte": "Découverte", "pro": "Pro", "entreprise": "Entreprise", "interne": "interne"}
UPGRADE = "passez à la formule Pro (Réglages › Formule et facturation)"

_hosts = TTLCache(60)        # (org, agent_id) → admis
_bytes_db = TTLCache(30)     # (org, jour) → octets de journaux et de traces déjà écrits en base
_month_db = TTLCache(30)     # (org, mois) → explications déjà comptées en base


class PlanLimit(ApiError):
    def __init__(self, quota: str, detail: str, limit, plan: str | None):
        super().__init__(402, "plan_limit", detail, quota=quota, limit=limit, plan=plan)


def _name(plan: OrgPlan) -> str:
    return PLAN_NAMES.get(plan.code or "", plan.code or "")


# ── hôtes, équipements et ressources cloud ─────────────────────────────────────

ADMIT_SQL = """
    WITH me AS (SELECT id FROM hosts WHERE org_id = %s AND agent_id = %s::uuid)
    SELECT count(*) AS n FROM hosts h
     WHERE h.org_id = %s AND h.kind = ANY(%s::text[]) AND h.last_seen > now() - interval '1 hour'
       AND h.agent_id <> %s::uuid
       AND (NOT EXISTS (SELECT 1 FROM me) OR h.id < (SELECT id FROM me))
"""

REFUSED_SQL = """
    INSERT INTO org_refused_hosts (org_id, agent_id, hostname, kind) VALUES (%s, %s::uuid, %s, %s)
    ON CONFLICT (org_id, agent_id) DO UPDATE
       SET hostname = EXCLUDED.hostname, kind = EXCLUDED.kind, last_at = now(),
           attempts = org_refused_hosts.attempts + 1
"""


async def admit_host(conn, org_id: int, agent_id: str, hostname: str, kind: str) -> None:
    plan = await get_plan(org_id, conn)
    cap = plan.limits.hosts if kind == "agent" else plan.limits.devices
    if cap is None:
        return
    key = (org_id, agent_id)
    allowed = _hosts.get(key)
    if allowed is None:
        kinds = ["agent"] if kind == "agent" else ["network_device", "cloud_resource"]
        row = await fetch_one(conn, ADMIT_SQL, (org_id, agent_id, org_id, kinds, agent_id))
        allowed = (row or {}).get("n", 0) < cap
        _hosts.set(key, allowed)
        if not allowed:
            await _record_refused(org_id, agent_id, hostname, kind)
    if not allowed:
        if kind == "agent":
            raise PlanLimit("hosts", f"Formule {_name(plan)} : {cap} hôtes au plus ; « {hostname} » est refusé "
                                     f"tant que {cap} autres hôtes envoient des données. Retirez l'agent d'un "
                                     f"hôte ou {UPGRADE}.", cap, plan.code)
        raise PlanLimit("devices", f"Formule {_name(plan)} : {cap} équipements réseau et ressources cloud au plus ; "
                                   f"« {hostname} » est refusé. Retirez-en un de la configuration de l'agent ou "
                                   f"{UPGRADE}.", cap, plan.code)


async def _record_refused(org_id: int, agent_id: str, hostname: str, kind: str) -> None:
    """Hors de la transaction de la requête (elle sera annulée par le refus)."""
    from .runtime import rt
    try:
        async with rt().db.writer(timeout=3) as c:
            await c.execute(REFUSED_SQL, (org_id, agent_id, (hostname or "?")[:255], kind))
    except Exception as e:
        log.warning("hôte refusé non inscrit (org %s, %s) : %s", org_id, hostname, e)


def forget_hosts(org_id: int) -> None:
    for key in [k for k in list(_hosts._data) if k[0] == org_id]:
        _hosts.pop(key)


# ── journaux et traces : volume quotidien (Découverte) ─────────────────────────

def _today():
    return datetime.now(timezone.utc).date()


async def bytes_today(conn, org_id: int, usage) -> int:
    """Octets de journaux et de traces reçus aujourd'hui (UTC) : base (30 s) + ce processus."""
    day = _today()
    key = (org_id, day)
    stored = _bytes_db.get(key)
    if stored is None:
        row = await fetch_one(conn, "SELECT log_bytes + span_bytes AS b FROM usage_daily "
                                    "WHERE org_id = %s AND day = %s", (org_id, day))
        stored = int((row or {}).get("b") or 0)
        _bytes_db.set(key, stored)
    return stored + usage.pending_count(org_id, day, "log_bytes") + usage.pending_count(org_id, day, "span_bytes")


async def admit_ingest(conn, org_id: int, what: str, count: int, usage) -> None:
    """Refuse un lot de journaux (`logs`) ou de spans (`spans`) quand le volume du jour est atteint."""
    plan = await get_plan(org_id, conn)
    cap = plan.limits.ingest_bytes_per_day
    if cap is None:
        return
    used = await bytes_today(conn, org_id, usage)
    if used < cap:
        return
    usage.add(org_id, **({"refused_log_lines": count} if what == "logs" else {"refused_spans": count}))
    raise PlanLimit("ingest_bytes_per_day",
                    f"Formule {_name(plan)} : {cap // MB} Mo de journaux et de traces par jour, atteints "
                    f"({used // MB} Mo reçus aujourd'hui) ; envois refusés jusqu'à minuit UTC. Ajoutez des "
                    f"filtres d'exclusion (Réglages › Journaux) ou {UPGRADE}.", cap, plan.code)


# ── tests synthétiques publics ─────────────────────────────────────────────────

async def check_synthetic(conn, org_id: int, test: dict, test_id: int | None = None) -> None:
    """Test lancé depuis S33D (`server` dans ses emplacements) : intervalle minimal et nombre de tests.
    Les tests privés (lancés par les agents du client) ne sont pas limités."""
    if "server" not in (test.get("locations") or []):
        return
    plan = await get_plan(org_id, conn)
    lim = plan.limits
    if plan.code in (None, "interne"):
        return
    if int(test.get("interval_s") or 0) < lim.public_interval_min_s:
        raise PlanLimit("public_interval_min_s",
                        f"Formule {_name(plan)} : un test lancé depuis S33D tourne au plus toutes les "
                        f"{lim.public_interval_min_s // 60} min (interval_s ≥ {lim.public_interval_min_s}). "
                        f"Un test lancé par vos agents n'a pas cette limite.", lim.public_interval_min_s, plan.code)
    if lim.public_tests is not None and test.get("enabled", True):
        row = await fetch_one(conn, """
            SELECT count(*) AS n FROM synthetic_tests
             WHERE org_id = %s AND enabled AND 'server' = ANY(locations) AND (%s::bigint IS NULL OR id <> %s)
        """, (org_id, test_id, test_id))
        if (row or {}).get("n", 0) >= lim.public_tests:
            raise PlanLimit("public_tests",
                            f"Formule {_name(plan)} : {lim.public_tests} tests lancés depuis S33D au plus. "
                            f"Désactivez-en un, faites-le lancer par un de vos agents (illimité), ou {UPGRADE}.",
                            lim.public_tests, plan.code)


# ── pages de statut ────────────────────────────────────────────────────────────

async def check_status_page(conn, org_id: int) -> None:
    plan = await get_plan(org_id, conn)
    cap = plan.limits.status_pages
    if cap is None:
        return
    row = await fetch_one(conn, "SELECT count(*) AS n FROM status_pages WHERE org_id = %s", (org_id,))
    if (row or {}).get("n", 0) >= cap:
        raise PlanLimit("status_pages", f"Formule {_name(plan)} : {cap} page de statut au plus ; {UPGRADE}.",
                        cap, plan.code)


# ── explications et post-mortem par l'IA ───────────────────────────────────────

async def explanations_this_month(conn, org_id: int, usage) -> int:
    now = datetime.now(timezone.utc)
    first = now.date().replace(day=1)
    key = (org_id, first)
    stored = _month_db.get(key)
    if stored is None:
        row = await fetch_one(conn, "SELECT coalesce(sum(explanations), 0)::bigint AS n FROM usage_daily "
                                    "WHERE org_id = %s AND day >= %s", (org_id, first))
        stored = int((row or {}).get("n") or 0)
        _month_db.set(key, stored)
    pending = sum(v[usage_index("explanations")] for (o, d), v in usage.pending.items() if o == org_id and d >= first)
    return stored + pending


def usage_index(name: str) -> int:
    from .ingest.usage import FIELDS
    return FIELDS.index(name)


async def check_explanation(conn, org_id: int, usage) -> None:
    plan = await get_plan(org_id, conn)
    cap = plan.limits.explanations_per_month
    if cap is None:
        return
    used = await explanations_this_month(conn, org_id, usage)
    if used >= cap:
        raise PlanLimit("explanations_per_month",
                        f"Formule {_name(plan)} : {cap} explications ou post-mortem par l'IA de S33D et par mois, "
                        f"atteints ; de nouveau disponibles le 1er du mois prochain, ou {UPGRADE}.", cap, plan.code)
