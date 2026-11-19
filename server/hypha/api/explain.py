"""`POST /api/v1/explain` : explication d'une alerte, d'un hôte, d'une ligne de journal ou d'une trace."""
import logging

from fastapi import APIRouter, Body, Depends

from .. import quotas
from ..actions.service import proposals as action_proposals
from ..auth.principal import Principal, viewer
from ..errors import ApiError, bad_request
from ..explain.collect import collect
from ..explain.context import assemble
from ..explain.llm import HourlyQuota, LlmUnavailable, chat
from ..explain.prompt import build_messages
from ..explain.request import parse_request
from ..ingest.extras import state_of
from ..runtime import rt
from .common import ok

log = logging.getLogger("hypha.explain")
router = APIRouter()

QUOTA_PER_HOUR = 60
_quota = HourlyQuota(QUOTA_PER_HOUR)


@router.post("/explain")
async def explain(data: dict = Body(...), p: Principal = Depends(viewer)):
    settings = rt().settings
    if not settings.explain_enabled:
        raise ApiError(503, "llm_unavailable", "explication des pannes non configurée sur ce serveur")
    try:
        req = parse_request(data)
    except ValueError as e:
        raise bad_request(str(e))
    usage = state_of(rt()).usage
    async with rt().db.reader() as conn:
        ctx = await collect(conn, p.org_id, req)
        await quotas.check_explanation(conn, p.org_id, usage)     # formule Découverte : 30 par mois (§ 52)
    # Le quota se compte après la vérification de l'objet : un 404 ne consomme rien.
    if not _quota.allow(p.org_id):
        raise ApiError(429, "rate_limited", f"{QUOTA_PER_HOUR} explications par heure au plus pour l'organisation")
    text = assemble(ctx)
    try:
        explanation, duration_ms = await chat(settings.llm_url, settings.llm_model,
                                              build_messages(req.kind, text, req.lang), settings.llm_timeout_s)
    except LlmUnavailable as e:
        log.warning("explication %s (org %s) impossible : %s", req.kind, p.org_id, e)
        raise ApiError(503, "llm_unavailable", f"le modèle ne répond pas ({e})")
    usage.add(p.org_id, explanations=1)
    log.info("explication %s (org %s) : %s ms, contexte de %s caractères", req.kind, p.org_id, duration_ms, len(text))
    return ok({"explanation": explanation, "model": settings.llm_model, "duration_ms": duration_ms,
               "context": ctx.summary,
               "proposed_actions": await _proposals(p.org_id, explanation, req, ctx.summary.get("host"))})


async def _proposals(org_id: int, explanation: str, req, hostname: str | None) -> list[dict]:
    """Gestes du « Correctif » que l'agent de l'hôte sait faire (§ 28) ; jamais bloquant pour l'explication."""
    try:
        async with rt().db.reader() as conn:
            return await action_proposals(conn, org_id, explanation, req.lang, hostname,
                                          getattr(req, "host_id", None))
    except Exception as e:
        log.warning("propositions d'actions non calculées (org %s) : %s", org_id, e)
        return []
