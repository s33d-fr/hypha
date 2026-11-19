"""Conservation par formule, à la lecture (contrat § 52).

Les partitions journalières de télémétrie sont communes à tous les espaces : purger les lignes d'un
espace Découverte au bout de 3 jours demanderait des DELETE dans des partitions de plusieurs Go, puis
leur VACUUM, sur un primaire qui attend déjà son disque. On borne donc la LECTURE à la conservation
de la formule (Découverte : journaux et traces 3 jours, mesures 30 jours dont 1 jour en pleine
résolution) ; l'effacement physique suit la purge commune des partitions (16 jours au plus pour les
journaux, les traces et les mesures brutes, 92 jours pour le pas de 5 min). Coût : nul.

Conservation longue (Pro, option 30 ou 90 jours) : les jours clos sont copiés la nuit dans
`logs_long` / `spans_long` (worker, `billing/long_retention.py`) ; une plage qui remonte au-delà de
14 jours se lit alors dans la vue `logs_etendus` / `spans_etendus`.

L'espace interne de S33D et l'auto-hébergé ne sont pas bornés ici (purge des partitions seule).
"""
from .plans import DECOUVERTE, ENTREPRISE, PRO, OrgPlan, get_plan
from .util import now_ms

DAY_MS = 86_400_000
LONG_SWITCH_DAYS = 14          # au-delà, la plage peut toucher des jours déjà passés dans *_long
BOUNDED = (DECOUVERTE, PRO, ENTREPRISE)


def bounds(plan: OrgPlan, kind: str, start: int, end: int, now: int | None = None) -> tuple[int, int, str]:
    """(début, fin, relation) d'une lecture de `logs` ou `spans` pour cet espace."""
    now = now_ms() if now is None else now
    if plan.code not in BOUNDED:
        return start, end, kind
    floor = now - plan.retention_days * DAY_MS
    if start < floor:
        start = floor
    if start >= end:
        start = end - 1           # plage entièrement hors conservation : lecture vide, pas d'erreur
    relation = kind
    if plan.retention_days > 15 and start < now - LONG_SWITCH_DAYS * DAY_MS:
        relation = kind + "_etendus"
    return start, end, relation


async def window(org_id: int, kind: str, start: int, end: int, conn=None) -> tuple[int, int, str]:
    return bounds(await get_plan(org_id, conn), kind, start, end)


def metrics_bounds(plan: OrgPlan, start: int, end: int, now: int | None = None) -> tuple[int, int, int | None]:
    """(début, fin, conservation du brut en secondes ou None) d'une requête de métriques."""
    now = now_ms() if now is None else now
    lim = plan.limits
    if plan.code not in BOUNDED or lim.metrics_days is None:
        return start, end, None
    floor = now - lim.metrics_days * DAY_MS
    if start < floor:
        start = floor
    if start >= end:
        start = end - 1
    return start, end, lim.metrics_raw_days * 86400


def visible_since(plan: OrgPlan, kind: str, now: int | None = None) -> int | None:
    """Premier instant lisible (ms) pour l'interface, ou None si seule la purge commune s'applique."""
    now = now_ms() if now is None else now
    if plan.code not in BOUNDED:
        return None
    if kind in ("logs", "spans"):
        return now - plan.retention_days * DAY_MS
    if plan.limits.metrics_days is not None:
        return now - plan.limits.metrics_days * DAY_MS
    return None
