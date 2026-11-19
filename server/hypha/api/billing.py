"""Formule et facturation d'un espace (contrat § 52).

- `GET /api/v1/billing` (viewer) : formule, limites et leur consommation, hôtes refusés ; pour
  `admin` et plus : abonnement, estimation de la période en cours (et, en Découverte, ce que
  coûterait la formule Pro avec la consommation du mois).
- `POST /api/v1/billing/checkout` · `/portal` · `/cancel` · `/resume` · `PUT /billing/plan` ·
  `PUT /billing/retention` : propriétaire de l'espace seulement.
- `POST /api/stripe/webhook` : public, signé par Stripe (secret propre à HYPHA).
"""
import logging
from datetime import datetime, timezone

from fastapi import APIRouter, Body, Depends, Request
from fastapi.responses import JSONResponse

from .. import audit, plans, quotas
from ..auth.principal import Principal, owner, viewer
from ..billing import metering
from ..billing import service as B
from ..billing.stripe_api import SignatureError, StripeError, verify_webhook
from ..db import fetch_all, fetch_one
from ..errors import ApiError
from ..ingest.extras import state_of
from ..runtime import rt
from .common import ok

log = logging.getLogger("hypha.billing")
router = APIRouter()
public_router = APIRouter()
UTC = timezone.utc
MAX_WEBHOOK_BODY = 1 << 20

SUB_FIELDS = ("status", "wanted_plan", "billing_interval", "committed_hosts", "period_start", "period_end",
              "commit_period_end", "cancel_at", "retention_days", "discount", "company", "billing_email", "country",
              "vat_id", "vat_status", "tax_mode", "last_sync_at", "last_error")


def _prices() -> dict:
    return {k: float(v) for k, v in plans.PRICES.items()}


async def overview(conn, p: Principal) -> dict:
    s = rt().settings
    plan = await plans.get_plan(p.org_id, conn)
    out = {"mode": s.mode, "plan": plan.code, "limits": plans.describe(plan), "prices": _prices(),
           "billing": {"enabled": s.billing_enabled, "open": s.billing_open},
           "association_discount": float(plans.ASSOCIATION_DISCOUNT), "vat_rate": float(plans.VAT_RATE)}
    if not s.saas:
        return out
    usage = state_of(rt()).usage
    hosts = await fetch_one(conn, """
        SELECT count(*) FILTER (WHERE kind = 'agent') AS hosts, count(*) FILTER (WHERE kind <> 'agent') AS devices
          FROM hosts WHERE org_id = %s AND last_seen > now() - interval '1 hour'
    """, (p.org_id,)) or {}
    tests = await fetch_one(conn, """
        SELECT count(*) FILTER (WHERE 'server' = ANY(locations)) AS public_tests,
               count(*) FILTER (WHERE NOT ('server' = ANY(locations))) AS private_tests,
               coalesce(min(interval_s) FILTER (WHERE 'server' = ANY(locations)), 0) AS min_public_interval_s
          FROM synthetic_tests WHERE org_id = %s AND enabled
    """, (p.org_id,)) or {}
    pages = await fetch_one(conn, "SELECT count(*) AS n FROM status_pages WHERE org_id = %s", (p.org_id,)) or {}
    today = await fetch_one(conn, """
        SELECT refused_log_lines, refused_spans FROM usage_daily WHERE org_id = %s AND day = (now() AT TIME ZONE 'UTC')::date
    """, (p.org_id,)) or {}
    refused = await fetch_all(conn, """
        SELECT hostname, kind, first_at, last_at, attempts FROM org_refused_hosts
         WHERE org_id = %s AND last_at > now() - interval '1 day' ORDER BY last_at DESC LIMIT 50
    """, (p.org_id,))
    out["now"] = {
        "hosts": hosts.get("hosts", 0), "devices": hosts.get("devices", 0),
        "public_tests": tests.get("public_tests", 0), "private_tests": tests.get("private_tests", 0),
        "min_public_interval_s": tests.get("min_public_interval_s", 0), "status_pages": pages.get("n", 0),
        "ingest_bytes_today": await quotas.bytes_today(conn, p.org_id, usage),
        "explanations_month": await quotas.explanations_this_month(conn, p.org_id, usage),
        "refused_log_lines_today": today.get("refused_log_lines", 0) or 0,
        "refused_spans_today": today.get("refused_spans", 0) or 0,
    }
    out["refused_hosts"] = refused
    out["alerts"] = _alerts(plan, out["now"], refused)
    if not p.at_least("admin"):
        return out
    row = await B.billing_row(conn, p.org_id) or {}
    now = datetime.now(UTC)
    out["subscription"] = {k: row.get(k) for k in SUB_FIELDS} if row else None
    if out["subscription"] is not None:
        out["subscription"]["has_customer"] = bool(row.get("stripe_customer_id"))
        out["subscription"]["has_commitment"] = bool(row.get("commit_subscription_id"))
    if plan.paid:
        period = metering.Period(row["period_start"], row["period_end"]) \
            if row.get("period_start") and row.get("period_end") and row["period_start"] <= now < row["period_end"] \
            else metering.month_of(now)
        committed = int(row.get("committed_hosts") or 0) if row.get("commit_subscription_id") else 0
        q = await metering.quantities(conn, p.org_id, period, now, committed)
        out["period"] = q["detail"]
        out["estimate"] = metering.estimate(q, plan.code, row.get("discount"), row.get("tax_mode") or "fr20",
                                            committed, row.get("billing_interval") or "month")
    elif plan.code == plans.DECOUVERTE:
        period = metering.month_of(now)
        q = await metering.quantities(conn, p.org_id, period, now, 0)
        out["period"] = q["detail"]
        # Ce que coûterait Pro ce mois-ci avec la même consommation (mêmes règles que la facture).
        out["pro_estimate"] = metering.estimate(q, plans.PRO, None, "fr20")
    return out


def _alerts(plan: plans.OrgPlan, now: dict, refused: list) -> list[dict]:
    """Limites atteintes, montrées en bandeau : rien ne doit être refusé en silence."""
    lim = plan.limits
    out = []
    recent = [r for r in refused if (datetime.now(UTC) - r["last_at"]).total_seconds() < 3600]
    hosts_refused = [r["hostname"] for r in recent if r["kind"] == "agent"]
    devices_refused = [r["hostname"] for r in recent if r["kind"] != "agent"]
    if hosts_refused:
        out.append({"quota": "hosts", "limit": lim.hosts, "names": hosts_refused[:10], "count": len(hosts_refused)})
    if devices_refused:
        out.append({"quota": "devices", "limit": lim.devices, "names": devices_refused[:10], "count": len(devices_refused)})
    if lim.ingest_bytes_per_day and now["ingest_bytes_today"] >= lim.ingest_bytes_per_day:
        out.append({"quota": "ingest_bytes_per_day", "limit": lim.ingest_bytes_per_day,
                    "refused_log_lines": now["refused_log_lines_today"], "refused_spans": now["refused_spans_today"]})
    elif lim.ingest_bytes_per_day and now["ingest_bytes_today"] >= 0.8 * lim.ingest_bytes_per_day:
        out.append({"quota": "ingest_bytes_per_day", "limit": lim.ingest_bytes_per_day, "level": "warn",
                    "used": now["ingest_bytes_today"]})
    if lim.explanations_per_month and now["explanations_month"] >= lim.explanations_per_month:
        out.append({"quota": "explanations_per_month", "limit": lim.explanations_per_month})
    return out


@router.get("/billing")
async def get_billing(p: Principal = Depends(viewer)):
    async with rt().db.reader() as conn:
        return ok(await overview(conn, p))


def _saas():
    if not rt().settings.saas:
        raise ApiError(404, "not_found", "formules et facturation : service hébergé seulement (HYPHA_MODE=saas)")


async def _org(conn, org_id: int) -> dict:
    org = await fetch_one(conn, "SELECT id, slug, name, plan FROM orgs WHERE id = %s", (org_id,))
    if org is None:
        raise ApiError(404, "not_found", "espace introuvable")
    if org.get("plan") == plans.INTERNE:
        raise ApiError(409, "internal_org", "l'espace interne de S33D n'a ni formule ni facturation")
    return org


def _stripe_failure(e: StripeError) -> ApiError:
    log.warning("appel Stripe en échec : %s", e)
    return ApiError(502, "stripe_error", f"Stripe a refusé l'opération : {e.message}")


@router.post("/billing/checkout")
async def checkout(request: Request, data: dict = Body(...), p: Principal = Depends(owner)):
    _saas()
    async with rt().db.writer() as conn:
        org = await _org(conn, p.org_id)
        try:
            res = await B.start_checkout(conn, org, p.email, data)
        except StripeError as e:
            raise _stripe_failure(e)
        await audit.record(conn, org_id=p.org_id, principal=p, action="checkout", object_type="billing",
                           object_id=p.org_id, summary=f"souscription {data.get('plan')} ({data.get('interval', 'month')}) "
                                                       f"ouverte chez Stripe", request=request)
    return ok(res)


@router.post("/billing/portal")
async def portal(data: dict | None = Body(None), p: Principal = Depends(owner)):
    _saas()
    async with rt().db.reader() as conn:
        await _org(conn, p.org_id)
        try:
            url = await B.portal_url(conn, p.org_id, (data or {}).get("lang") or "fr")
        except StripeError as e:
            raise _stripe_failure(e)
    return ok({"url": url})


async def _change(request: Request, p: Principal, action: str, summary: str, fn):
    _saas()
    async with rt().db.writer() as conn:
        await _org(conn, p.org_id)
        try:
            res = await fn(conn)
        except StripeError as e:
            raise _stripe_failure(e)
        await audit.record(conn, org_id=p.org_id, principal=p, action=action, object_type="billing",
                           object_id=p.org_id, summary=summary, after=res, request=request)
    return ok(res)


@router.post("/billing/cancel")
async def cancel(request: Request, p: Principal = Depends(owner)):
    return await _change(request, p, "cancel", "retour à Découverte demandé en fin de période",
                         lambda conn: B.cancel(conn, p.org_id))


@router.post("/billing/resume")
async def resume(request: Request, p: Principal = Depends(owner)):
    return await _change(request, p, "resume", "résiliation annulée", lambda conn: B.resume(conn, p.org_id))


@router.put("/billing/plan")
async def change_plan(request: Request, data: dict = Body(...), p: Principal = Depends(owner)):
    plan = data.get("plan")
    return await _change(request, p, "update", f"formule {plan}", lambda conn: B.change_plan(conn, p.org_id, plan))


@router.put("/billing/retention")
async def set_retention(request: Request, data: dict = Body(...), p: Principal = Depends(owner)):
    try:
        days = int(data.get("days"))
    except (TypeError, ValueError):
        days = 0
    return await _change(request, p, "update", f"conservation des journaux et traces : {days} jours",
                         lambda conn: B.set_retention(conn, p.org_id, days))


# ── webhook Stripe (public, signé) ─────────────────────────────────────────────

@public_router.post("/api/stripe/webhook")
async def stripe_webhook(request: Request):
    s = rt().settings
    if not s.saas or not s.stripe_webhook_secret:
        raise ApiError(503, "billing_unavailable", "webhook Stripe non configuré sur ce serveur")
    length = request.headers.get("content-length", "")
    if length.isdigit() and int(length) > MAX_WEBHOOK_BODY:
        raise ApiError(413, "payload_too_large", "corps trop gros")
    payload = await request.body()
    if len(payload) > MAX_WEBHOOK_BODY:
        raise ApiError(413, "payload_too_large", "corps trop gros")
    try:
        event = verify_webhook(payload, request.headers.get("stripe-signature"), s.stripe_webhook_secret)
    except SignatureError as e:
        log.warning("webhook Stripe refusé : %s", e)
        raise ApiError(400, "invalid_signature", str(e))
    eid, etype = str(event.get("id") or ""), str(event.get("type") or "")
    if not eid.startswith("evt_"):
        raise ApiError(400, "invalid_event", "événement sans identifiant")
    async with rt().db.writer() as conn:
        fresh = await fetch_one(conn, "INSERT INTO stripe_events (id, type) VALUES (%s, %s) "
                                      "ON CONFLICT (id) DO NOTHING RETURNING id", (eid, etype[:100]))
        if fresh is None:
            return JSONResponse({"received": True, "duplicate": True})
        try:
            outcome, org_id = await B.handle_event(conn, event)
        except StripeError as e:
            # Transaction annulée (l'événement n'est pas marqué reçu) : Stripe le renverra.
            log.warning("webhook %s (%s) : Stripe indisponible, renvoi attendu : %s", eid, etype, e)
            raise ApiError(503, "stripe_unavailable", "Stripe injoignable, renvoyez plus tard")
        await conn.execute("UPDATE stripe_events SET outcome = %s, org_id = %s WHERE id = %s",
                           (outcome[:300], org_id, eid))
    if org_id is not None:
        log.info("webhook Stripe %s (%s), espace %s : %s", eid, etype, org_id, outcome)
    return JSONResponse({"received": True})
