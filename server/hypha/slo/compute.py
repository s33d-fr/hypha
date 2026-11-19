"""Calcul des SLO, sans accès à la base (contrat § 13).

Type `monitor` : le SLI est la part du temps où le moniteur n'était pas en ALERT, son état global
étant le pire de ses groupes (NODATA et WARN comptent comme bons). Les intervalles d'ALERT de
chaque groupe se reconstruisent depuis ses changements d'état (`alert_events`) ; l'état au début
de la fenêtre est celui du dernier changement antérieur, à défaut l'état de départ du premier
changement de la fenêtre, à défaut l'état courant (`monitor_states`). Le temps avant la création
du moniteur est exclu.

Type `metric` : SLI = Σ good / Σ total sur la fenêtre, 100 si total = 0.

Budget restant = (SLI − cible) / (100 − cible) × 100, borné en bas à −100.
Vitesse de consommation sur une fenêtre courte = (100 − SLI de cette fenêtre) / (100 − cible).
"""
from dataclasses import dataclass

TIMEFRAME_S = {"7d": 7 * 86400, "30d": 30 * 86400, "90d": 90 * 86400}
HISTORY_STEP_S = {"7d": 3600, "30d": 86400, "90d": 86400}     # un point par heure (7 j) ou par jour
ALERT = "ALERT"


@dataclass(frozen=True)
class Change:
    ts: float                   # secondes Unix
    group: str
    from_state: str | None
    to_state: str


def alert_intervals(before: list[Change], inside: list[Change], current: dict[str, str],
                    start: float, end: float, no_data_s: float,
                    bad: frozenset = frozenset({ALERT})) -> list[tuple[float, float]]:
    """Intervalles [a, b) où un groupe était en ALERT (ou dans un des états de `bad`), bornés à [start, end].

    `before` : le dernier changement de chaque groupe antérieur à `start` ; `inside` : les
    changements de [start, end], dans l'ordre. Un groupe en ALERT qui n'a plus d'état courant
    (oublié par le worker faute de données) est réputé sorti d'alerte `no_data_s` après son
    dernier changement : sa disparition n'est pas datée.
    """
    last_before = {c.group: c for c in before}
    by_group: dict[str, list[Change]] = {}
    for c in sorted(inside, key=lambda c: c.ts):
        by_group.setdefault(c.group, []).append(c)
    out: list[tuple[float, float]] = []
    for group in set(last_before) | set(by_group) | set(current):
        changes = by_group.get(group, [])
        if group in last_before:
            state = last_before[group].to_state
        elif changes:
            state = changes[0].from_state or "OK"
        else:
            state = current.get(group, "OK")
        t = start
        for c in changes:
            if state in bad and c.ts > t:
                out.append((t, c.ts))
            state, t = c.to_state, max(t, c.ts)
        if state in bad:
            if group in current:
                tail = end if current[group] in bad else t
            else:
                last_ts = changes[-1].ts if changes else last_before[group].ts if group in last_before else start
                tail = min(end, last_ts + no_data_s)
            if tail > t:
                out.append((t, tail))
    return [(max(a, start), min(b, end)) for a, b in out if min(b, end) > max(a, start)]


def merge(intervals: list[tuple[float, float]]) -> list[tuple[float, float]]:
    """Union d'intervalles : le pire état de tous les groupes, sans compter deux fois un recouvrement."""
    out: list[list[float]] = []
    for a, b in sorted(intervals):
        if out and a <= out[-1][1]:
            out[-1][1] = max(out[-1][1], b)
        else:
            out.append([a, b])
    return [(a, b) for a, b in out]


def covered(merged: list[tuple[float, float]], a: float, b: float) -> float:
    """Durée des intervalles (déjà fusionnés) comprise dans [a, b]."""
    return sum(max(0.0, min(y, b) - max(x, a)) for x, y in merged)


def sli_from_bad(bad_s: float, total_s: float) -> float:
    if total_s <= 0:
        return 100.0
    return max(0.0, min(100.0, 100.0 * (1.0 - bad_s / total_s)))


def ratio_sli(good: float, total: float) -> float:
    if total <= 0:
        return 100.0
    return max(0.0, min(100.0, 100.0 * good / total))


def error_budget(sli: float, target: float) -> float:
    return max(-100.0, (sli - target) / (100.0 - target) * 100.0)


def burn_rate(sli: float, target: float) -> float:
    return (100.0 - sli) / (100.0 - target)


def slo_state(sli: float, target: float, warning: float | None) -> str:
    if sli < target:
        return "breached"
    if warning is not None and sli < warning:
        return "warning"
    return "ok"


def summarize(sli: float, sli_1h: float, sli_6h: float, target: float, warning: float | None) -> dict:
    return {"sli": round(sli, 4), "target": round(target, 6), "state": slo_state(sli, target, warning),
            "error_budget_remaining": round(error_budget(sli, target), 2),
            "burn_rate_1h": round(burn_rate(sli_1h, target), 3), "burn_rate_6h": round(burn_rate(sli_6h, target), 3)}


def history_times(start: float, end: float, step: float) -> list[float]:
    """Instants des points d'historique : chaque pas après le début de la fenêtre, et maintenant."""
    times, t = [], start + step
    while t < end:
        times.append(t)
        t += step
    if end > start:
        times.append(end)
    return times


def monitor_figures(merged: list[tuple[float, float]], start: float, end: float, step: float):
    """(SLI fenêtre, SLI 1 h, SLI 6 h, historique [[ts_ms, SLI cumulé], …]) d'un SLO de moniteur."""
    def sli_between(a: float, b: float) -> float:
        a = max(a, start)
        return sli_from_bad(covered(merged, a, b), b - a)

    history = [[int(t * 1000), round(sli_between(start, t), 4)] for t in history_times(start, end, step)]
    return sli_between(start, end), sli_between(end - 3600, end), sli_between(end - 6 * 3600, end), history


def ratio_history(good: dict[int, float], total: dict[int, float], start_ms: int, end_ms: int,
                  step_s: int) -> list[list]:
    """SLI cumulé (Σ good / Σ total depuis le début) à la fin de chaque seau, et maintenant."""
    history, g_sum, t_sum = [], 0.0, 0.0
    for bucket in sorted(set(good) | set(total)):
        g_sum += good.get(bucket, 0.0)
        t_sum += total.get(bucket, 0.0)
        point = min(bucket + step_s * 1000, end_ms)
        if point > start_ms:
            history.append([point, round(ratio_sli(g_sum, t_sum), 4)])
    return history
