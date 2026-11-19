"""Lecture des résultats d'un test par emplacement (contrat § 29), pour le moniteur `synthetic`.

Le moniteur reçoit les derniers résultats (du plus récent au plus ancien) et décide :
un emplacement est « en échec » quand ses `failures` derniers résultats sont tous en échec ;
le moniteur passe en ALERT quand au moins `locations_min_failing` emplacements le sont (1 par
défaut : le comportement d'avant la 0.4.0 pour un test au seul emplacement `server`).

    rows = SELECT location, ok, ts FROM synthetic_results WHERE … ORDER BY ts DESC LIMIT 500
    verdict = synthetic_verdict(rows, failures=q.get("failures", 1),
                                min_failing=q.get("locations_min_failing", 1))
    measured["*"] = None if verdict is None else ("ALERT" if verdict.alert else "OK", float(verdict.failing))
"""
from dataclasses import dataclass, field


@dataclass
class Verdict:
    alert: bool
    failing: int                                        # emplacements en échec
    locations: dict = field(default_factory=dict)       # emplacement → échecs consécutifs


def consecutive_failures_by_location(rows: list[dict]) -> dict[str, int]:
    """Échecs consécutifs les plus récents, par emplacement (lignes triées du plus récent au plus ancien)."""
    out: dict[str, int] = {}
    closed: set[str] = set()
    for r in rows:
        loc = r.get("location") or "server"
        if loc in closed:
            continue
        if r.get("ok"):
            closed.add(loc)
            out.setdefault(loc, 0)
        else:
            out[loc] = out.get(loc, 0) + 1
    return out


def synthetic_verdict(rows: list[dict], failures: int = 1, min_failing: int = 1) -> Verdict | None:
    """None sans aucun résultat (le moniteur passe alors en NODATA selon ses réglages)."""
    if not rows:
        return None
    by_loc = consecutive_failures_by_location(rows)
    failing = sum(1 for n in by_loc.values() if n >= max(1, int(failures)))
    return Verdict(failing >= max(1, int(min_failing)), failing, by_loc)
