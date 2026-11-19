"""Formules de HYPHA (docs/OFFRE.md, contrat § 52) : limites d'un espace, prix, lecture en base.

Libre = auto-hébergé (`HYPHA_MODE=selfhosted`) : aucune formule n'est lue, rien n'est limité ni
facturé. En SaaS, chaque espace (`orgs.plan`) porte l'une des formules suivantes :
- `interne` : l'espace de S33D, ni quota ni facturation ;
- `decouverte` : gratuit, sans carte bancaire, limité ;
- `pro` : à l'usage, facturé par Stripe ; `entreprise` : Pro + forfait mensuel.

La formule est lue en base (orgs + org_billing) et gardée 30 s par processus : un changement venu
du webhook Stripe s'applique sur tous les nœuds en moins d'une minute.
"""
from dataclasses import dataclass
from decimal import Decimal

from .util import TTLCache

INTERNE, DECOUVERTE, PRO, ENTREPRISE = "interne", "decouverte", "pro", "entreprise"
PLANS = (INTERNE, DECOUVERTE, PRO, ENTREPRISE)
PAID = (PRO, ENTREPRISE)
RETENTION_CHOICES = (15, 30, 90)

MB = 1000 * 1000                  # Mo et Go décimaux, comme sur une facture
GB = 1000 * MB


@dataclass(frozen=True)
class Limits:
    hosts: int | None = None                    # hôtes (agents) actifs
    devices: int | None = None                  # équipements réseau et ressources cloud actifs
    ingest_bytes_per_day: int | None = None     # journaux et traces ensemble, par jour UTC
    public_tests: int | None = None             # tests synthétiques publics (plafond)
    public_tests_included: int | None = None    # au-delà : exécutions facturées
    public_interval_min_s: int = 30             # intervalle minimal d'un test public
    explanations_per_month: int | None = None   # explications et post-mortem par l'IA de S33D
    status_pages: int | None = None
    logs_days: int = 15                         # journaux et traces visibles
    metrics_raw_days: int = 15                  # mesures en pleine résolution
    metrics_days: int | None = None             # mesures au total (None : 15 mois au pas d'une heure)
    sms: bool = True
    long_retention: bool = False                # option de conservation 30 / 90 jours


LIMITS = {
    INTERNE: Limits(),
    DECOUVERTE: Limits(hosts=5, devices=10, ingest_bytes_per_day=500 * MB, public_tests=10,
                       public_interval_min_s=300, explanations_per_month=30, status_pages=1, logs_days=3,
                       metrics_raw_days=1, metrics_days=30, sms=False),
    PRO: Limits(public_tests_included=50, public_interval_min_s=60, long_retention=True),
    ENTREPRISE: Limits(public_tests_included=50, public_interval_min_s=60, long_retention=True),
}
UNLIMITED = Limits()

# Pro : volumes inclus par hôte et par mois, mis en commun sur l'espace.
LOGS_GB_PER_HOST = 10
TRACES_GB_PER_HOST = 5

# Prix hors taxes en euros (docs/OFFRE.md). Stripe porte les mêmes montants (lookup_key hypha_*).
PRICES = {
    "host_month": Decimal("2.00"),              # par hôte et par mois, à l'heure
    "host_year": Decimal("18.00"),              # par hôte engagé et par an (1,50 € par mois)
    "device_month": Decimal("0.50"),            # par équipement ou ressource cloud et par mois
    "gb_over": Decimal("0.10"),                 # par Go de journaux ou de traces au-delà de l'inclus
    "retention_30": Decimal("0.05"),            # par Go reçu avec la conservation 30 jours
    "retention_90": Decimal("0.15"),            # par Go reçu avec la conservation 90 jours
    "synthetic_run": Decimal("0.0001"),         # 1 € les 10 000 exécutions au-delà des 50 tests
    "sms": Decimal("0.10"),
    "entreprise_month": Decimal("250.00"),
}
ASSOCIATION_DISCOUNT = Decimal("0.5")           # −50 % sur les hôtes (associations, enseignement, recherche)
VAT_RATE = Decimal("0.20")


@dataclass(frozen=True)
class OrgPlan:
    org_id: int
    code: str | None              # None = auto-hébergé (formule Libre)
    limits: Limits
    retention_days: int           # journaux et traces visibles pour CET espace (option comprise)
    billing_status: str = "none"

    @property
    def paid(self) -> bool:
        return self.code in PAID

    @property
    def limited(self) -> bool:
        return self.code == DECOUVERTE


SELFHOSTED_PLAN = OrgPlan(0, None, UNLIMITED, 15)

_cache = TTLCache(30)

PLAN_SQL = """
    SELECT o.plan, b.retention_days, b.status
      FROM orgs o LEFT JOIN org_billing b ON b.org_id = o.id
     WHERE o.id = %s
"""


def from_row(org_id: int, row: dict | None) -> OrgPlan:
    """Formule d'un espace d'après sa ligne ; un espace introuvable n'est pas limité (jamais en prod)."""
    code = (row or {}).get("plan") or INTERNE
    if code not in LIMITS:
        code = DECOUVERTE
    limits = LIMITS[code]
    retention = limits.logs_days
    if code in PAID and (row or {}).get("retention_days") in RETENTION_CHOICES:
        retention = int(row["retention_days"])
    return OrgPlan(org_id, code, limits, retention, (row or {}).get("status") or "none")


async def get_plan(org_id: int, conn=None) -> OrgPlan:
    from .runtime import rt
    runtime = rt()
    if not runtime.settings.saas:
        return SELFHOSTED_PLAN
    cached = _cache.get(org_id)
    if cached is not None:
        return cached
    from .db import fetch_one
    if conn is not None:
        row = await fetch_one(conn, PLAN_SQL, (org_id,))
    else:
        async with runtime.db.reader() as c:
            row = await fetch_one(c, PLAN_SQL, (org_id,))
    plan = from_row(org_id, row)
    _cache.set(org_id, plan)
    return plan


def forget(org_id: int) -> None:
    _cache.pop(org_id)


def describe(plan: OrgPlan) -> dict:
    """Limites rendues à l'interface (None = sans limite)."""
    lim = plan.limits
    return {
        "hosts": lim.hosts, "devices": lim.devices, "ingest_bytes_per_day": lim.ingest_bytes_per_day,
        "public_tests": lim.public_tests, "public_tests_included": lim.public_tests_included,
        "public_interval_min_s": lim.public_interval_min_s, "explanations_per_month": lim.explanations_per_month,
        "status_pages": lim.status_pages, "logs_days": plan.retention_days, "metrics_raw_days": lim.metrics_raw_days,
        "metrics_days": lim.metrics_days, "sms": lim.sms, "long_retention": lim.long_retention,
        "logs_gb_per_host": LOGS_GB_PER_HOST if plan.paid else None,
        "traces_gb_per_host": TRACES_GB_PER_HOST if plan.paid else None,
    }
