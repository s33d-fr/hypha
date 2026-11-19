"""Facturation Stripe de HYPHA (contrat § 52) : souscription, portail, webhook, report de l'usage.

Abonnements d'un espace payant :
- « usage » (mensuel, calé sur le 1er du mois à 00:00 UTC) : sept postes à l'usage (compteurs à
  agrégation `last` : HYPHA envoie le cumul de la période, chaque heure puis à la création de la
  facture) et, en Entreprise, le forfait de 250 € ;
- « commit » (annuel, facultatif) : hôtes engagés à 18 € par an, payés d'avance ; les heures au-delà
  de l'engagement passent par le poste « hôtes » de l'abonnement à l'usage, au tarif mensuel.

TVA : prix HT, taux français 20 % (Tax Rate) ; client professionnel d'un autre pays de l'UE dont le
numéro est vérifié par Stripe (VIES) : autoliquidation (`tax_exempt=reverse`, mention en pied de
facture). Stripe Tax n'est pas utilisé.

Compte Stripe partagé : le webhook ne traite que les objets HYPHA (`metadata.offre = "hypha"` ou un
prix du catalogue `hypha_*`), puis les rattache à un espace seulement si le client Stripe est bien
celui de cet espace (`org_billing.stripe_customer_id`).
"""
import logging
import re
import uuid
from datetime import datetime, timezone

from .. import plans, quotas
from ..db import fetch_all, fetch_one
from ..errors import ApiError, bad_request
from ..runtime import rt
from ..util import jdump
from . import metering
from .catalog import (CATALOG, COUPON_ASSOCIATION, EU_COUNTRIES, METERS, OFFRE, REVERSE_CHARGE_FOOTER,
                      SELLER_FOOTER, USAGE_ITEMS, is_hypha_price)
from .stripe_api import Stripe, StripeError

log = logging.getLogger("hypha.billing")
UTC = timezone.utc

LIVE = ("active", "trialing", "past_due", "unpaid", "incomplete")
PAYING = ("active", "trialing", "past_due")
COUNTRY_RE = re.compile(r"^[A-Z]{2}$")
VAT_RE = re.compile(r"^[A-Z]{2}[0-9A-Z+*.]{2,13}$")
EMAIL_RE = re.compile(r"^[^@\s]{1,64}@[^@\s]{1,255}\.[^@\s]{2,}$")


def client() -> Stripe:
    s = rt().settings
    if not s.billing_enabled:
        raise ApiError(503, "billing_unavailable", "facturation non configurée sur ce serveur")
    return Stripe(s.stripe_secret_key)


def _ts(v) -> datetime | None:
    return datetime.fromtimestamp(int(v), UTC) if v else None


async def billing_row(conn, org_id: int) -> dict | None:
    return await fetch_one(conn, "SELECT * FROM org_billing WHERE org_id = %s", (org_id,))


async def ensure_row(conn, org_id: int) -> dict:
    await conn.execute("INSERT INTO org_billing (org_id) VALUES (%s) ON CONFLICT (org_id) DO NOTHING", (org_id,))
    return await billing_row(conn, org_id)


async def _update(conn, org_id: int, **fields) -> None:
    cols = ", ".join(f"{k} = %s" for k in fields)
    vals = [jdump(v) if isinstance(v, dict) else v for v in fields.values()]
    await conn.execute(f"UPDATE org_billing SET {cols}, updated_at = now() WHERE org_id = %s", vals + [org_id])


async def set_plan(conn, org_id: int, plan: str) -> None:
    await conn.execute("UPDATE orgs SET plan = %s WHERE id = %s AND plan <> 'interne' AND plan <> %s",
                       (plan, org_id, plan))
    plans.forget(org_id)
    quotas.forget_hosts(org_id)


# ── souscription (Checkout) ────────────────────────────────────────────────────

def _clean_input(data: dict) -> dict:
    plan = data.get("plan")
    if plan not in plans.PAID:
        raise bad_request("plan : pro ou entreprise")
    interval = data.get("interval", "month")
    if interval not in ("month", "year"):
        raise bad_request("interval : month ou year")
    committed = 0
    if interval == "year":
        try:
            committed = int(data.get("committed_hosts") or 0)
        except (TypeError, ValueError):
            committed = 0
        if not 1 <= committed <= 100000:
            raise bad_request("committed_hosts : nombre d'hôtes engagés pour l'année (1 à 100 000)")
    company = str(data.get("company") or "").strip()[:200]
    email = str(data.get("email") or "").strip()[:320]
    if email and not EMAIL_RE.match(email):
        raise bad_request("email : adresse de facturation invalide")
    country = str(data.get("country") or "").strip().upper()
    if not COUNTRY_RE.match(country):
        raise bad_request("country : code pays à deux lettres (FR, BE, DE…)")
    vat = re.sub(r"[\s.\-]", "", str(data.get("vat_id") or "")).upper()
    if vat and not VAT_RE.match(vat):
        raise bad_request("vat_id : numéro de TVA intracommunautaire invalide (ex. FR16131908329)")
    if vat and vat[:2] != ("EL" if country == "GR" else country):
        raise bad_request("vat_id : le numéro de TVA doit commencer par le code du pays choisi")
    lang = "en" if data.get("lang") == "en" else "fr"
    return {"plan": plan, "interval": interval, "committed": committed, "company": company, "email": email,
            "country": country, "vat": vat, "lang": lang}


async def _ensure_customer(stripe: Stripe, conn, org: dict, row: dict, x: dict, user_email: str | None) -> str:
    footer = SELLER_FOOTER
    params = {
        "name": x["company"] or org["name"], "email": x["email"] or user_email or None,
        "address": {"country": x["country"]}, "preferred_locales": [x["lang"]],
        "invoice_settings": {"footer": footer},
        "metadata": {"offre": OFFRE, "hypha_org_id": str(org["id"]), "hypha_org_slug": org["slug"]},
    }
    if row.get("stripe_customer_id"):
        await stripe.post(f"/v1/customers/{row['stripe_customer_id']}", params)
        return row["stripe_customer_id"]
    cus = await stripe.post("/v1/customers", params, idempotency_key=f"hypha-client-{org['id']}-{uuid.uuid4()}")
    await _update(conn, org["id"], stripe_customer_id=cus["id"])
    return cus["id"]


async def _vat_status(stripe: Stripe, customer: str, vat: str) -> str | None:
    """Numéro de TVA posé sur le client ; Stripe le vérifie auprès du VIES (gratuit, asynchrone)."""
    if not vat:
        return None
    ids = await stripe.get(f"/v1/customers/{customer}/tax_ids", {"limit": 100})
    tid = next((t for t in ids.get("data") or [] if t.get("value", "").upper() == vat), None)
    if tid is None:
        for t in ids.get("data") or []:
            if t.get("type") == "eu_vat":
                await stripe.delete(f"/v1/customers/{customer}/tax_ids/{t['id']}")
        tid = await stripe.post(f"/v1/customers/{customer}/tax_ids", {"type": "eu_vat", "value": vat})
    status = (tid.get("verification") or {}).get("status")
    if status == "pending":
        import asyncio
        for _ in range(3):
            await asyncio.sleep(1.5)
            tid = await stripe.get(f"/v1/customers/{customer}/tax_ids/{tid['id']}")
            status = (tid.get("verification") or {}).get("status")
            if status != "pending":
                break
    return status


def tax_mode_for(country: str | None, vat_status: str | None) -> str:
    """20 % sauf professionnel d'un AUTRE pays de l'UE au numéro vérifié : autoliquidation."""
    if country and country != "FR" and country in EU_COUNTRIES and vat_status == "verified":
        return "reverse"
    return "fr20"


async def apply_tax(stripe: Stripe, row: dict, tax_mode: str) -> None:
    """Pose le régime de TVA sur le client et sur ses abonnements HYPHA (factures à venir)."""
    cus = row["stripe_customer_id"]
    footer = SELLER_FOOTER + ("\n" + REVERSE_CHARGE_FOOTER if tax_mode == "reverse" else "")
    await stripe.post(f"/v1/customers/{cus}", {"tax_exempt": "reverse" if tax_mode == "reverse" else "none",
                                               "invoice_settings": {"footer": footer}})
    rates = [CATALOG.tax_rate_id] if tax_mode == "fr20" and CATALOG.tax_rate_id else []
    for sid in (row.get("usage_subscription_id"), row.get("commit_subscription_id")):
        if sid:
            await stripe.post(f"/v1/subscriptions/{sid}", {"default_tax_rates": rates})


def _usage_items(plan: str) -> list[dict]:
    items = [{"price": CATALOG.price_id(k)} for k in USAGE_ITEMS]
    if plan == plans.ENTREPRISE:
        items.append({"price": CATALOG.price_id("entreprise"), "quantity": 1})
    return items


def _meta(org_id: int, plan: str, role: str) -> dict:
    return {"offre": OFFRE, "hypha_org_id": str(org_id), "hypha_plan": plan, "hypha_role": role}


async def start_checkout(conn, org: dict, user_email: str | None, data: dict) -> dict:
    s = rt().settings
    stripe = client()
    if not s.billing_open:
        raise ApiError(503, "billing_closed", "Les abonnements Pro et Entreprise ouvrent très bientôt : la formule "
                                              "Découverte reste disponible en attendant.")
    x = _clean_input(data)
    row = await ensure_row(conn, org["id"])
    if x["interval"] == "month" and row.get("usage_subscription_id") and row.get("status") in LIVE:
        raise ApiError(409, "already_subscribed", "abonnement déjà en cours : changez de formule ou d'engagement "
                                                  "depuis la page Formule et facturation")
    if x["interval"] == "year" and row.get("commit_subscription_id"):
        raise ApiError(409, "already_committed", "engagement annuel déjà en cours : le nombre d'hôtes se change "
                                                 "depuis le portail de facturation")
    await CATALOG.load(stripe)
    customer = await _ensure_customer(stripe, conn, org, row, x, user_email)
    vat_status = await _vat_status(stripe, customer, x["vat"])
    tax_mode = tax_mode_for(x["country"], vat_status)
    row = dict(row, stripe_customer_id=customer)
    await apply_tax(stripe, dict(row, usage_subscription_id=None, commit_subscription_id=None), tax_mode)
    rates = [CATALOG.tax_rate_id] if tax_mode == "fr20" and CATALOG.tax_rate_id else None
    if x["interval"] == "month":
        role = "usage"
        line_items = _usage_items(x["plan"])
        sub = {"metadata": _meta(org["id"], x["plan"], role),
               "billing_cycle_anchor": int(metering.next_month_start(datetime.now(UTC)).timestamp()),
               "default_tax_rates": rates, "description": f"HYPHA {x['plan']} · espace {org['slug']}"}
    else:
        role = "commit"
        line_items = [{"price": CATALOG.price_id("host_year"), "quantity": x["committed"],
                       "adjustable_quantity": {"enabled": True, "minimum": 1, "maximum": 100000}}]
        sub = {"metadata": _meta(org["id"], x["plan"], role), "default_tax_rates": rates,
               "description": f"HYPHA {x['plan']}, engagement annuel · espace {org['slug']}"}
    params = {
        "mode": "subscription", "customer": customer, "line_items": line_items, "subscription_data": sub,
        "success_url": f"{s.public_url}/settings/billing?souscription=ok",
        "cancel_url": f"{s.public_url}/settings/billing?souscription=annulee",
        "locale": x["lang"], "billing_address_collection": "required",
        "customer_update": {"address": "auto", "name": "auto"},
        "client_reference_id": str(org["id"]), "metadata": _meta(org["id"], x["plan"], role),
    }
    if row.get("discount") == "association":
        params["discounts"] = [{"coupon": COUPON_ASSOCIATION}]
    session = await stripe.post("/v1/checkout/sessions", params)
    await _update(conn, org["id"], wanted_plan=x["plan"], company=x["company"] or None, billing_email=x["email"] or None,
                  country=x["country"], vat_id=x["vat"] or None, vat_status=vat_status, tax_mode=tax_mode,
                  **({"committed_hosts": x["committed"]} if role == "commit" else {}))
    return {"url": session["url"], "tax_mode": tax_mode, "vat_status": vat_status}


async def portal_url(conn, org_id: int, lang: str) -> str:
    stripe = client()
    row = await billing_row(conn, org_id)
    if not row or not row.get("stripe_customer_id"):
        raise ApiError(409, "no_customer", "aucun abonnement : souscrivez d'abord une formule payante")
    await CATALOG.load(stripe)
    params = {"customer": row["stripe_customer_id"], "return_url": f"{rt().settings.public_url}/settings/billing",
              "locale": "en" if lang == "en" else "fr"}
    if CATALOG.portal_id:
        params["configuration"] = CATALOG.portal_id
    return (await stripe.post("/v1/billing_portal/sessions", params))["url"]


# ── changements en cours d'abonnement ──────────────────────────────────────────

async def _live_row(conn, org_id: int) -> dict:
    row = await billing_row(conn, org_id)
    if not row or not row.get("usage_subscription_id") or row.get("status") not in LIVE:
        raise ApiError(409, "no_subscription", "aucun abonnement payant en cours pour cet espace")
    return row


async def cancel(conn, org_id: int) -> dict:
    """Retour à Découverte en fin de période (fin de l'engagement annuel s'il y en a un)."""
    stripe = client()
    row = await _live_row(conn, org_id)
    if row.get("commit_subscription_id"):
        await stripe.post(f"/v1/subscriptions/{row['commit_subscription_id']}", {"cancel_at_period_end": True})
    end = row.get("commit_period_end")
    if row.get("commit_subscription_id") and end:
        sub = await stripe.post(f"/v1/subscriptions/{row['usage_subscription_id']}", {"cancel_at": int(end.timestamp())})
    else:
        sub = await stripe.post(f"/v1/subscriptions/{row['usage_subscription_id']}", {"cancel_at_period_end": True})
    cancel_at = _ts(sub.get("cancel_at")) or row.get("period_end")
    await _update(conn, org_id, cancel_at=cancel_at)
    return {"cancel_at": cancel_at}


async def resume(conn, org_id: int) -> dict:
    stripe = client()
    row = await _live_row(conn, org_id)
    await stripe.post(f"/v1/subscriptions/{row['usage_subscription_id']}", {"cancel_at": ""})
    await stripe.post(f"/v1/subscriptions/{row['usage_subscription_id']}", {"cancel_at_period_end": False})
    if row.get("commit_subscription_id"):
        await stripe.post(f"/v1/subscriptions/{row['commit_subscription_id']}", {"cancel_at_period_end": False})
    await _update(conn, org_id, cancel_at=None)
    return {"cancel_at": None}


async def change_plan(conn, org_id: int, plan: str) -> dict:
    """Pro ↔ Entreprise : ajoute ou retire le forfait mensuel (au prorata) sur l'abonnement à l'usage."""
    if plan not in plans.PAID:
        raise bad_request("plan : pro ou entreprise")
    stripe = client()
    await CATALOG.load(stripe)
    row = await _live_row(conn, org_id)
    sid = row["usage_subscription_id"]
    sub = await stripe.get(f"/v1/subscriptions/{sid}")
    ent = next((i for i in sub["items"]["data"] if i["price"]["id"] == CATALOG.price_id("entreprise")), None)
    if plan == plans.ENTREPRISE and ent is None:
        await stripe.post("/v1/subscription_items", {"subscription": sid, "price": CATALOG.price_id("entreprise"),
                                                     "quantity": 1, "proration_behavior": "create_prorations"})
    elif plan == plans.PRO and ent is not None:
        await stripe.delete(f"/v1/subscription_items/{ent['id']}", {"proration_behavior": "create_prorations"})
    await stripe.post(f"/v1/subscriptions/{sid}", {"metadata": {"hypha_plan": plan}})
    await _update(conn, org_id, wanted_plan=plan)
    await set_plan(conn, org_id, plan)
    return {"plan": plan}


async def set_retention(conn, org_id: int, days: int) -> dict:
    if days not in plans.RETENTION_CHOICES:
        raise bad_request("days : 15, 30 ou 90")
    plan = await plans.get_plan(org_id, conn)
    if not plan.paid and days != 15:
        raise quotas.PlanLimit("long_retention", "La conservation longue (30 ou 90 jours) est une option des "
                                                 "formules Pro et Entreprise.", None, plan.code)
    await ensure_row(conn, org_id)
    await _update(conn, org_id, retention_days=days)
    await snapshot_retention(conn, org_id, days)
    plans.forget(org_id)
    return {"retention_days": days}


async def snapshot_retention(conn, org_id: int, days: int) -> None:
    """Option de conservation du jour (UTC), lue pour facturer les Go reçus ce jour-là."""
    await conn.execute("""
        INSERT INTO usage_daily (org_id, day, retention_days) VALUES (%s, (now() AT TIME ZONE 'UTC')::date, %s)
        ON CONFLICT (org_id, day) DO UPDATE SET retention_days = EXCLUDED.retention_days
    """, (org_id, days))


async def set_discount(conn, org_id: int, discount: str | None) -> dict:
    """Remise association (−50 % sur les hôtes), posée par S33D après vérification (ligne de commande)."""
    row = await ensure_row(conn, org_id)
    await _update(conn, org_id, discount=discount)
    applied = []
    if rt().settings.billing_enabled:
        stripe = client()
        for sid in (row.get("usage_subscription_id"), row.get("commit_subscription_id")):
            if sid:
                await stripe.post(f"/v1/subscriptions/{sid}",
                                  {"discounts": [{"coupon": COUPON_ASSOCIATION}] if discount else []})
                applied.append(sid)
    return {"discount": discount, "subscriptions": applied}


# ── webhook ────────────────────────────────────────────────────────────────────

def _metadata(obj: dict) -> dict:
    return obj.get("metadata") or {}


def _sub_is_hypha(sub: dict) -> bool:
    if _metadata(sub).get("offre") == OFFRE:
        return True
    return any(is_hypha_price(i.get("price")) for i in ((sub.get("items") or {}).get("data") or []))


def _invoice_subscription(inv: dict) -> tuple[str | None, dict]:
    parent = (inv.get("parent") or {}).get("subscription_details") or {}
    sid = parent.get("subscription") or inv.get("subscription")
    meta = parent.get("metadata") or (inv.get("subscription_details") or {}).get("metadata") or {}
    return (sid if isinstance(sid, str) else (sid or {}).get("id")), meta


def _line_price(line: dict) -> str | None:
    pr = line.get("pricing") or {}
    pid = (pr.get("price_details") or {}).get("price") or line.get("price")
    return pid if isinstance(pid, str) else (pid or {}).get("id")


def _invoice_is_hypha(inv: dict) -> bool:
    _, meta = _invoice_subscription(inv)
    if meta.get("offre") == OFFRE or _metadata(inv).get("offre") == OFFRE:
        return True
    return any(_line_price(li) in CATALOG.price_ids for li in ((inv.get("lines") or {}).get("data") or []))


async def _row_for(conn, org_hint, customer: str | None) -> dict | None:
    """Espace d'un objet Stripe : le client doit être celui de l'espace annoncé (jamais un autre)."""
    if not customer:
        return None
    row = await fetch_one(conn, "SELECT * FROM org_billing WHERE stripe_customer_id = %s", (customer,))
    if row is None:
        return None
    if org_hint not in (None, "") and str(org_hint) != str(row["org_id"]):
        return None
    return row


def _period(sub: dict) -> tuple[datetime | None, datetime | None]:
    items = (sub.get("items") or {}).get("data") or []
    start = sub.get("current_period_start") or (items[0].get("current_period_start") if items else None)
    end = sub.get("current_period_end") or (items[0].get("current_period_end") if items else None)
    return _ts(start), _ts(end)


async def apply_subscription(conn, stripe: Stripe, sub: dict, deleted: bool = False) -> tuple[str, int | None]:
    meta = _metadata(sub)
    row = await _row_for(conn, meta.get("hypha_org_id"), sub.get("customer"))
    if row is None:
        return "client HYPHA inconnu : ignoré", None
    org_id = row["org_id"]
    items = (sub.get("items") or {}).get("data") or []
    year_item = next((i for i in items if (i.get("price") or {}).get("id") == CATALOG.prices.get("host_year", {}).get("id")
                      or (i.get("price") or {}).get("lookup_key") == "hypha_pro_hote_annuel"), None)
    role = meta.get("hypha_role") or ("commit" if year_item else "usage")
    status = sub.get("status") or "none"
    gone = deleted or status in ("canceled", "incomplete_expired")
    start, end = _period(sub)
    if role == "commit":
        if gone:
            if row.get("commit_subscription_id") in (None, sub["id"]):
                await _update(conn, org_id, commit_subscription_id=None, committed_hosts=0, billing_interval="month",
                              commit_period_end=None)
            return f"engagement annuel terminé ({status})", org_id
        await _update(conn, org_id, commit_subscription_id=sub["id"], billing_interval="year",
                      committed_hosts=int((year_item or {}).get("quantity") or row.get("committed_hosts") or 0),
                      commit_period_end=end)
        if status in PAYING and not row.get("usage_subscription_id"):
            await ensure_usage_subscription(conn, stripe, dict(row, commit_subscription_id=sub["id"]), sub,
                                            meta.get("hypha_plan") or row.get("wanted_plan") or plans.PRO)
        return f"engagement annuel {status}", org_id
    if gone:
        if row.get("usage_subscription_id") in (None, sub["id"]):
            await _update(conn, org_id, usage_subscription_id=None, status="canceled", cancel_at=None)
            await set_plan(conn, org_id, plans.DECOUVERTE)
        return f"abonnement terminé ({status}) : retour à Découverte", org_id
    ent_id = CATALOG.prices.get("entreprise", {}).get("id")
    plan = plans.ENTREPRISE if any((i.get("price") or {}).get("id") == ent_id or
                                   (i.get("price") or {}).get("lookup_key") == "hypha_entreprise_mensuel"
                                   for i in items) else plans.PRO
    cancel_at = _ts(sub.get("cancel_at")) or (end if sub.get("cancel_at_period_end") else None)
    await _update(conn, org_id, usage_subscription_id=sub["id"], status=status, period_start=start, period_end=end,
                  cancel_at=cancel_at)
    if status in PAYING:
        await set_plan(conn, org_id, plan)
    elif status == "unpaid":
        # Toutes les relances ont échoué : service limité à Découverte jusqu'au paiement.
        await set_plan(conn, org_id, plans.DECOUVERTE)
    return f"abonnement {status} : formule {plan if status in PAYING else 'inchangée'}", org_id


async def ensure_usage_subscription(conn, stripe: Stripe, row: dict, commit_sub: dict, plan: str) -> str | None:
    """Engagement annuel souscrit : abonnement mensuel à l'usage créé à côté, même moyen de paiement."""
    if row.get("usage_subscription_id"):
        return row["usage_subscription_id"]
    pm = commit_sub.get("default_payment_method")
    pm = pm if isinstance(pm, str) else (pm or {}).get("id")
    tax_mode = row.get("tax_mode") or "fr20"
    params = {
        "customer": commit_sub["customer"], "items": _usage_items(plan),
        "billing_cycle_anchor": int(metering.next_month_start(datetime.now(UTC)).timestamp()),
        "proration_behavior": "create_prorations", "payment_behavior": "allow_incomplete", "off_session": True,
        "metadata": _meta(row["org_id"], plan, "usage"),
        "default_tax_rates": [CATALOG.tax_rate_id] if tax_mode == "fr20" and CATALOG.tax_rate_id else None,
        "description": f"HYPHA {plan}, usage mensuel",
    }
    if pm:
        params["default_payment_method"] = pm
    if row.get("discount") == "association":
        params["discounts"] = [{"coupon": COUPON_ASSOCIATION}]
    sub = await stripe.post("/v1/subscriptions", params, idempotency_key=f"hypha-usage-{row['org_id']}-{commit_sub['id']}")
    start, end = _period(sub)
    await _update(conn, row["org_id"], usage_subscription_id=sub["id"], status=sub.get("status") or "none",
                  period_start=start, period_end=end)
    if sub.get("status") in PAYING:
        await set_plan(conn, row["org_id"], plan)
    return sub["id"]


async def handle_event(conn, event: dict) -> tuple[str, int | None]:
    """Traite un événement signé ; rend (ce qui a été fait, espace) pour le journal `stripe_events`."""
    stripe = client()
    await CATALOG.load(stripe)
    etype = event.get("type") or ""
    obj = (event.get("data") or {}).get("object") or {}
    if etype == "checkout.session.completed":
        if _metadata(obj).get("offre") != OFFRE:
            return "ignoré : pas HYPHA", None
        sid = obj.get("subscription")
        sid = sid if isinstance(sid, str) else (sid or {}).get("id")
        if not sid:
            return "session sans abonnement : ignorée", None
        sub = await stripe.get(f"/v1/subscriptions/{sid}")
        return await apply_subscription(conn, stripe, sub)
    if etype.startswith("customer.subscription."):
        if not _sub_is_hypha(obj):
            return "ignoré : pas HYPHA", None
        return await apply_subscription(conn, stripe, obj, deleted=etype.endswith(".deleted"))
    if etype.startswith("invoice."):
        if not _invoice_is_hypha(obj):
            return "ignoré : pas HYPHA", None
        sid, meta = _invoice_subscription(obj)
        row = await _row_for(conn, meta.get("hypha_org_id"), obj.get("customer"))
        if row is None:
            return "client HYPHA inconnu : ignoré", None
        if etype == "invoice.created" and sid and sid == row.get("usage_subscription_id"):
            period = _invoice_usage_period(obj)
            if period:
                await report_usage(conn, stripe, row, period, final=True)
                return f"usage définitif envoyé ({period.start:%Y-%m-%d} → {period.end:%Y-%m-%d})", row["org_id"]
            return "facture sans poste à l'usage", row["org_id"]
        if etype == "invoice.payment_failed":
            await _update(conn, row["org_id"], last_error=f"paiement refusé (facture {obj.get('number') or obj.get('id')})")
            return "paiement refusé", row["org_id"]
        if etype == "invoice.paid":
            await _update(conn, row["org_id"], last_error=None)
            return "facture payée", row["org_id"]
        return f"{etype} : rien à faire", row["org_id"]
    if etype.startswith("customer.tax_id."):
        cus = obj.get("customer")
        cus = cus if isinstance(cus, str) else (cus or {}).get("id")
        if not cus:
            return "ignoré : sans client", None
        customer = await stripe.get(f"/v1/customers/{cus}")
        if _metadata(customer).get("offre") != OFFRE:
            return "ignoré : pas HYPHA", None
        row = await _row_for(conn, _metadata(customer).get("hypha_org_id"), cus)
        if row is None:
            return "client HYPHA inconnu : ignoré", None
        status = (obj.get("verification") or {}).get("status") if etype != "customer.tax_id.deleted" else None
        mode = tax_mode_for(row.get("country"), status)
        await _update(conn, row["org_id"], vat_status=status, tax_mode=mode)
        await apply_tax(stripe, row, mode)
        return f"TVA : numéro {status or 'retiré'}, régime {mode}", row["org_id"]
    return "type non traité", None


def _invoice_usage_period(inv: dict) -> metering.Period | None:
    """Période des postes à l'usage d'une facture (celle qui vient de se clore)."""
    usage_ids = {CATALOG.prices[k]["id"] for k in USAGE_ITEMS if k in CATALOG.prices}
    spans = [li.get("period") or {} for li in ((inv.get("lines") or {}).get("data") or [])
             if _line_price(li) in usage_ids]
    starts = [p["start"] for p in spans if p.get("start")]
    ends = [p["end"] for p in spans if p.get("end")]
    if not starts or not ends:
        return None
    return metering.Period(_ts(min(starts)), _ts(max(ends)))


# ── report de l'usage (compteurs Stripe) ───────────────────────────────────────

def decimal_text(value) -> str:
    """Nombre en notation décimale simple (jamais 1e-05 ni 1.2e+06), comme l'attend Stripe."""
    from decimal import Decimal
    d = Decimal(str(value))
    text = format(d.quantize(Decimal("0.001")) if d != d.to_integral_value() else d.to_integral_value(), "f")
    return text.rstrip("0").rstrip(".") if "." in text else text

async def report_usage(conn, stripe: Stripe, row: dict, period: metering.Period, final: bool = False) -> dict:
    now = datetime.now(UTC)
    upto = period.end if final else now
    committed = int(row.get("committed_hosts") or 0) if row.get("commit_subscription_id") else 0
    q = await metering.quantities(conn, row["org_id"], period, upto, committed)
    stamp = int(min(now, period.end).timestamp()) - (1 if final or now >= period.end else 0)
    sent = {}
    for key in USAGE_ITEMS:
        value = q[key]
        await stripe.post("/v1/billing/meter_events", {
            "event_name": METERS[key], "timestamp": stamp,
            "identifier": f"hypha-{row['org_id']}-{key}-{int(period.start.timestamp())}-{stamp}",
            "payload": {"stripe_customer_id": row["stripe_customer_id"], "value": decimal_text(value)},
        })
        sent[key] = value
    report = {"period_start": period.start.isoformat(), "period_end": period.end.isoformat(), "final": final,
              "at": now.isoformat(), "values": sent}
    await _update(conn, row["org_id"], last_sync_at=now, last_report=report, last_error=None)
    return report


async def sync_all(db, settings) -> str:
    """Tâche horaire du worker : option de conservation du jour, puis cumul de la période à Stripe."""
    async with db.writer() as conn:
        paid = await fetch_all(conn, """
            SELECT b.*, o.id AS the_org, o.plan FROM orgs o LEFT JOIN org_billing b ON b.org_id = o.id
             WHERE o.plan IN ('pro', 'entreprise')
        """)
        for row in paid:
            row["org_id"] = row["the_org"]
            await snapshot_retention(conn, row["org_id"], int(row.get("retention_days") or 15))
    if not settings.billing_enabled:
        return f"{len(paid)} espace(s) payant(s), facturation non configurée"
    stripe = Stripe(settings.stripe_secret_key)
    await CATALOG.load(stripe)
    done, errors = 0, 0
    now = datetime.now(UTC)
    for row in paid:
        if not row.get("usage_subscription_id") or row.get("status") not in LIVE or not row.get("stripe_customer_id"):
            continue
        period = metering.Period(row["period_start"], row["period_end"])             if row.get("period_start") and row.get("period_end") and row["period_start"] <= now < row["period_end"]             else metering.month_of(now)
        try:
            async with db.writer() as conn:
                await report_usage(conn, stripe, row, period)
            done += 1
        except StripeError as e:
            errors += 1
            log.warning("usage de l'espace %s non envoyé à Stripe : %s", row["org_id"], e)
            async with db.writer() as conn:
                await _update(conn, row["org_id"], last_error=f"envoi de l'usage : {e.message}"[:500])
    return f"usage envoyé pour {done} espace(s), {errors} échec(s)"
