"""Statuts et disponibilité d'une page de statut (contrat § 21), sans accès à la base.

Statut d'un composant : moniteur OK → operational, WARN → degraded, ALERT → partial_outage
(major_outage en priorité 1), maintenance active → maintenance ; synthétique : dernier résultat ;
SLO : breached → partial_outage, warning → degraded ; hôte : sans nouvelles → major_outage.
Disponibilité quotidienne en jours UTC, reconstruite comme les SLO de type moniteur (§ 13) : part du
jour hors ALERT. Un jour sans aucune couverture (avant la création du moniteur) vaut null.
Les pourcentages sont tronqués (pas arrondis) à deux décimales : 99,999 % s'affiche 99,99 %,
jamais 100 % alors qu'il y a eu une coupure.
"""
import math
from datetime import date, datetime, timedelta, timezone

from ..slo.compute import covered

ORDER = ("operational", "maintenance", "degraded", "partial_outage", "major_outage")
RANK = {s: i for i, s in enumerate(ORDER)}
DAYS = 90
DAY_S = 86400


def floor2(x: float) -> float:
    return math.floor(x * 100 + 1e-9) / 100


def worst_status(statuses) -> str:
    statuses = [s for s in statuses if s]
    return max(statuses, key=lambda s: RANK[s]) if statuses else "operational"


def overall(statuses) -> str:
    return worst_status(statuses)


def monitor_status(state: str | None, priority: int | None, in_maintenance: bool) -> str:
    if in_maintenance:
        return "maintenance"
    if state == "ALERT":
        return "major_outage" if priority == 1 else "partial_outage"
    if state == "WARN":
        return "degraded"
    return "operational"


def synthetic_status(last_ok: bool | None, in_maintenance: bool) -> str:
    if in_maintenance:
        return "maintenance"
    return "partial_outage" if last_ok is False else "operational"


def slo_status(state: str | None, in_maintenance: bool) -> str:
    if in_maintenance:
        return "maintenance"
    return {"breached": "partial_outage", "warning": "degraded"}.get(state or "", "operational")


def host_status(up: bool | None, in_maintenance: bool) -> str:
    if in_maintenance:
        return "maintenance"
    return "major_outage" if up is False else "operational"


def utc_days(now: datetime, n: int = DAYS) -> list[date]:
    """Les n derniers jours UTC, du plus ancien à aujourd'hui."""
    today = now.astimezone(timezone.utc).date()
    return [today - timedelta(days=i) for i in range(n - 1, -1, -1)]


def _day_bounds(d: date) -> tuple[float, float]:
    start = datetime(d.year, d.month, d.day, tzinfo=timezone.utc).timestamp()
    return start, start + DAY_S


def daily_from_intervals(bad: list, warn: list, start_s: float, now_s: float, days: list[date],
                         priority_one: bool = False) -> list[dict]:
    """Une entrée par jour : disponibilité (part hors ALERT) et pire statut du jour."""
    out = []
    for d in days:
        a, b = _day_bounds(d)
        a, b = max(a, start_s), min(b, now_s)
        if b <= a:
            out.append({"date": d.isoformat(), "uptime": None, "worst": None})
            continue
        bad_s, warn_s = covered(bad, a, b), covered(warn, a, b)
        worst = ("major_outage" if priority_one else "partial_outage") if bad_s > 0 else \
            "degraded" if warn_s > 0 else "operational"
        out.append({"date": d.isoformat(), "uptime": floor2(100.0 * (1 - bad_s / (b - a))), "worst": worst})
    return out


def uptime_from_intervals(bad: list, start_s: float, now_s: float) -> float | None:
    if now_s <= start_s:
        return None
    return floor2(100.0 * (1 - covered(bad, start_s, now_s) / (now_s - start_s)))


def daily_from_counts(counts: dict[date, tuple[float, float]], days: list[date], mode: str = "synthetic",
                      target: float | None = None) -> list[dict]:
    """Jours tirés de comptes (bons, total) : résultats synthétiques ou SLO de type métrique."""
    out = []
    for d in days:
        good, total = counts.get(d, (0.0, 0.0))
        if total <= 0:
            out.append({"date": d.isoformat(), "uptime": None, "worst": None})
            continue
        up = floor2(100.0 * good / total)
        if mode == "slo":
            worst = "partial_outage" if target is not None and up < target else "operational"
        else:
            worst = "operational" if good >= total else "degraded" if up >= 99 else "partial_outage"
        out.append({"date": d.isoformat(), "uptime": up, "worst": worst})
    return out


def uptime_from_counts(counts: dict[date, tuple[float, float]]) -> float | None:
    good = sum(g for g, _ in counts.values())
    total = sum(t for _, t in counts.values())
    return floor2(100.0 * good / total) if total > 0 else None
