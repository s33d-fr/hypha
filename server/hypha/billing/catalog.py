"""Catalogue Stripe de HYPHA : prix retrouvés par `lookup_key` (préfixe `hypha_`), compteurs, taxe.

Aucun identifiant Stripe n'est écrit dans le code : un compte neuf (bascule sur la SARL, compte
dédié) n'a besoin que du script de catalogue (SCRIPTS/STRIPE/hypha_catalogue.py) et de la clé.
Le compte est PARTAGÉ avec d'autres sites : tout ce qui est créé pour HYPHA porte
`metadata.offre = "hypha"`, et le webhook ne traite que ces objets.
"""
import time

from .stripe_api import Stripe

OFFRE = "hypha"

# Clé interne → lookup_key du prix.
LOOKUP = {
    "host": "hypha_pro_hote_mensuel",          # 2 € par hôte et par mois, à l'heure (compteur)
    "host_year": "hypha_pro_hote_annuel",      # 18 € par hôte engagé et par an (licence annuelle)
    "device": "hypha_equipement_mensuel",      # 0,50 € par équipement ou ressource cloud et par mois
    "gb_over": "hypha_go_au_dela",             # 0,10 € par Go de journaux ou de traces au-delà de l'inclus
    "retention_30": "hypha_conservation_30j",  # 0,05 € par Go conservé 30 jours
    "retention_90": "hypha_conservation_90j",  # 0,15 € par Go conservé 90 jours
    "synthetic": "hypha_synthetiques_executions",  # 1 € les 10 000 exécutions au-delà des 50 tests
    "sms": "hypha_sms",                        # 0,10 € le SMS
    "entreprise": "hypha_entreprise_mensuel",  # 250 € par mois
}

# Compteurs (Billing Meters, agrégation « last » : la valeur envoyée est le cumul de la période).
METERS = {
    "host": "hypha_hotes",
    "device": "hypha_equipements",
    "gb_over": "hypha_go_au_dela",
    "retention_30": "hypha_conservation_30j_go",
    "retention_90": "hypha_conservation_90j_go",
    "synthetic": "hypha_executions_synthetiques",
    "sms": "hypha_sms_envoyes",
}
USAGE_ITEMS = ("host", "device", "gb_over", "retention_30", "retention_90", "synthetic", "sms")
COUPON_ASSOCIATION = "hypha-association-50"
TAX_RATE_LOOKUP = "hypha_tva_fr_20"            # metadata.hypha_cle du taux de TVA française 20 %
PORTAL_LOOKUP = "hypha_portail"                # metadata.hypha_cle de la configuration du portail client

REVERSE_CHARGE_FOOTER = ("Autoliquidation : TVA due par le preneur (article 196 de la directive 2006/112/CE). "
                         "Reverse charge.")
SELLER_FOOTER = "S33D SARL · SIREN 131 908 329 · RCS Douai · TVA FR16131908329 · hypha.s33d.fr"

EU_COUNTRIES = {"AT", "BE", "BG", "CY", "CZ", "DE", "DK", "EE", "ES", "FI", "FR", "GR", "HR", "HU", "IE", "IT",
                "LT", "LU", "LV", "MT", "NL", "PL", "PT", "RO", "SE", "SI", "SK"}


class Catalog:
    TTL_S = 3600

    def __init__(self):
        self.loaded_at = 0.0
        self.prices: dict[str, dict] = {}       # clé interne → prix Stripe
        self.price_ids: set[str] = set()
        self.tax_rate_id: str | None = None
        self.portal_id: str | None = None

    def fresh(self) -> bool:
        return bool(self.prices) and time.monotonic() - self.loaded_at < self.TTL_S

    async def load(self, stripe: Stripe, force: bool = False) -> "Catalog":
        if self.fresh() and not force:
            return self
        keys = list(LOOKUP.values())
        found = await stripe.get("/v1/prices", {"lookup_keys": keys, "active": True, "limit": 100})
        by_lookup = {p.get("lookup_key"): p for p in found.get("data") or []
                     if (p.get("metadata") or {}).get("offre") == OFFRE}
        missing = [k for k, lk in LOOKUP.items() if lk not in by_lookup]
        if missing:
            from .stripe_api import StripeError
            raise StripeError(0, "catalog_incomplete", "prix HYPHA absents du compte Stripe : "
                              + ", ".join(LOOKUP[k] for k in missing) + " (lancer hypha_catalogue.py)")
        self.prices = {k: by_lookup[lk] for k, lk in LOOKUP.items()}
        self.price_ids = {p["id"] for p in self.prices.values()}
        rates = await stripe.list_all("/v1/tax_rates", {"active": True}, limit=500)
        self.tax_rate_id = next((r["id"] for r in rates
                                 if (r.get("metadata") or {}).get("hypha_cle") == TAX_RATE_LOOKUP), None)
        confs = await stripe.list_all("/v1/billing_portal/configurations", {"active": True}, limit=200)
        self.portal_id = next((c["id"] for c in confs
                               if (c.get("metadata") or {}).get("hypha_cle") == PORTAL_LOOKUP), None)
        self.loaded_at = time.monotonic()
        return self

    def price_id(self, key: str) -> str:
        return self.prices[key]["id"]

    def key_of(self, price_id: str | None) -> str | None:
        for k, p in self.prices.items():
            if p["id"] == price_id:
                return k
        return None


CATALOG = Catalog()


def is_hypha_price(price) -> bool:
    """Prix du catalogue HYPHA : objet développé (lookup_key, métadonnées) ou identifiant connu."""
    if isinstance(price, dict):
        if str(price.get("lookup_key") or "").startswith("hypha_"):
            return True
        if (price.get("metadata") or {}).get("offre") == OFFRE:
            return True
        price = price.get("id")
    return bool(price) and price in CATALOG.price_ids
