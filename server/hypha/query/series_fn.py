"""Fonctions de séries de `POST /metrics/query` (contrat § 30), sans accès à la base.

Une série est une liste de valeurs alignées sur la grille de la requête (None = seau sans donnée).
Les fonctions rendent une nouvelle liste de même longueur ; elles ignorent les None plutôt que de
les propager, sauf mention contraire.

    per_second(x)   valeur ÷ intervalle (compte par seau → débit par seconde)
    rate(x)         (v[i] − v[i−1]) ÷ intervalle, None si négatif (compteur remis à zéro)
    derivative(x)   (v[i] − v[i−1]) ÷ intervalle, signé
"""
import math
from statistics import median as _median

MAX_THEIL_SEN = 400              # points gardés (régulièrement) pour la pente de Theil-Sen
MAD_SCALE = 1.4826               # MAD → écart-type d'une loi normale


def _finite(v) -> bool:
    return v is not None and isinstance(v, (int, float)) and math.isfinite(v)


def map_values(values: list, fn) -> list:
    out = []
    for v in values:
        if v is None:
            out.append(None)
            continue
        try:
            r = fn(v)
        except (ValueError, ZeroDivisionError, OverflowError):
            r = None
        out.append(r if _finite(r) else None)
    return out


def log_base(values: list, base: float) -> list:
    fn = math.log2 if base == 2 else math.log10 if base == 10 else (lambda v: math.log(v, base))
    return map_values(values, lambda v: fn(v) if v > 0 else None)


def cumsum(values: list) -> list:
    total, out = 0.0, []
    for v in values:
        if v is None:
            out.append(None)
        else:
            total += v
            out.append(total)
    return out


def per_second(values: list, step_s: float) -> list:
    return map_values(values, lambda v: v / step_s)


def derivative(values: list, step_s: float, non_negative: bool = False) -> list:
    out, prev = [], None
    for v in values:
        if v is None or prev is None:
            out.append(None)
        else:
            d = (v - prev) / step_s
            out.append(None if non_negative and d < 0 else d)
        if v is not None:
            prev = v
    return out


def moving_avg(values: list, n: int) -> list:
    """Moyenne des `n` derniers seaux (fenêtre glissante, seaux vides ignorés)."""
    out, window = [], []
    for v in values:
        window.append(v)
        if len(window) > n:
            window.pop(0)
        known = [x for x in window if x is not None]
        out.append(sum(known) / len(known) if known and v is not None else None)
    return out


def rolling_median(values: list, n: int) -> list:
    out, window = [], []
    for v in values:
        window.append(v)
        if len(window) > n:
            window.pop(0)
        known = [x for x in window if x is not None]
        out.append(_median(known) if known and v is not None else None)
    return out


def ewma(values: list, n: int) -> list:
    """Moyenne mobile exponentielle, α = 2 / (n + 1)."""
    alpha = 2.0 / (n + 1)
    out, acc = [], None
    for v in values:
        if v is None:
            out.append(None)
            continue
        acc = v if acc is None else alpha * v + (1 - alpha) * acc
        out.append(acc)
    return out


def clamp(values: list, lo: float | None = None, hi: float | None = None) -> list:
    def f(v):
        if lo is not None and v < lo:
            return lo
        if hi is not None and v > hi:
            return hi
        return v
    return map_values(values, f)


def fill(values: list, mode: str) -> list:
    if mode == "null":
        return list(values)
    if mode == "zero":
        return [0.0 if v is None else v for v in values]
    if mode == "last":
        out, last = [], None
        for v in values:
            last = v if v is not None else last
            out.append(last)
        return out
    if mode == "linear":
        out = list(values)
        known = [i for i, v in enumerate(values) if v is not None]
        for a, b in zip(known, known[1:]):
            if b - a > 1:
                va, vb = values[a], values[b]
                for i in range(a + 1, b):
                    out[i] = va + (vb - va) * (i - a) / (b - a)
        return out
    raise ValueError(mode)


def summarize(values: list, how: str) -> float | None:
    known = [v for v in values if v is not None]
    if not known:
        return None
    if how in ("mean", "avg"):
        return sum(known) / len(known)
    if how == "max":
        return max(known)
    if how == "min":
        return min(known)
    if how in ("sum", "area"):
        return sum(known)
    if how == "last":
        return known[-1]
    raise ValueError(how)


def top_indices(series_values: list[list], n: int, how: str, direction: str) -> list[int]:
    """Indices des `n` séries les mieux classées ; une série vide passe toujours en dernier."""
    scored = [(summarize(v, how), i) for i, v in enumerate(series_values)]
    present = [(s, i) for s, i in scored if s is not None]
    present.sort(key=lambda x: (x[0], x[1]) if direction == "asc" else (-x[0], x[1]))
    return [i for _, i in present[:n]]


# ── prévision ──────────────────────────────────────────────────────────────────

def theil_sen(xs: list[float], ys: list[float]) -> tuple[float, float]:
    """Droite robuste : pente = médiane des pentes deux à deux, ordonnée = médiane de y − pente·x."""
    n = len(xs)
    if n == 0:
        raise ValueError("aucun point")
    if n == 1:
        return 0.0, ys[0]
    if n > MAX_THEIL_SEN:
        idx = [round(i * (n - 1) / (MAX_THEIL_SEN - 1)) for i in range(MAX_THEIL_SEN)]
        xs, ys = [xs[i] for i in idx], [ys[i] for i in idx]
        n = len(xs)
    slopes = [(ys[j] - ys[i]) / (xs[j] - xs[i]) for i in range(n) for j in range(i + 1, n) if xs[j] != xs[i]]
    slope = _median(slopes) if slopes else 0.0
    intercept = _median([y - slope * x for x, y in zip(xs, ys)])
    return slope, intercept


def mad(values: list[float]) -> float:
    if not values:
        return 0.0
    m = _median(values)
    return _median([abs(v - m) for v in values])


def forecast(times_ms: list[int], values: list, horizon_s: int, step_s: int, algorithm: str = "linear",
             max_points: int = 300) -> tuple[list, list]:
    """Points futurs [[ts, v]] et bornes [[ts, lo, hi]] après le dernier seau de la grille."""
    pts = [(t, v) for t, v in zip(times_ms, values) if v is not None]
    if len(pts) < 3 or not times_ms:
        return [], []
    x0 = pts[0][0]
    xs = [(t - x0) / 1000.0 for t, _ in pts]
    ys = [v for _, v in pts]
    slope, intercept = theil_sen(xs, ys)
    residuals = [y - (slope * x + intercept) for x, y in zip(xs, ys)]
    season: dict[int, float] = {}
    slot_s = 0
    if algorithm == "seasonal":
        span_s = xs[-1] - xs[0]
        if span_s >= 2 * 86400:
            slot_s = max(step_s, 3600)
            groups: dict[int, list] = {}
            for (t, _), r in zip(pts, residuals):
                groups.setdefault((t // 1000 % 86400) // slot_s, []).append(r)
            season = {k: _median(v) for k, v in groups.items()}
            residuals = [r - season.get((t // 1000 % 86400) // slot_s, 0.0) for (t, _), r in zip(pts, residuals)]
    spread = MAD_SCALE * mad(residuals)
    width = 1.96 * spread
    step = max(step_s, math.ceil(horizon_s / max(1, max_points)))
    last = times_ms[-1]
    future, bounds = [], []
    t = last + step * 1000
    while t <= last + horizon_s * 1000:
        x = (t - x0) / 1000.0
        v = slope * x + intercept
        if slot_s:
            v += season.get((t // 1000 % 86400) // slot_s, 0.0)
        future.append([t, v])
        bounds.append([t, v - width, v + width])
        t += step * 1000
    return future, bounds


# ── anomalies et valeurs aberrantes ────────────────────────────────────────────

def anomaly_bounds_basic(values: list, bounds: float, window: int) -> list:
    """[lo, hi] par seau : moyenne glissante ± bounds × écart-type des `window` seaux précédents."""
    out = []
    history: list = []
    for v in values:
        known = [x for x in history if x is not None]
        if len(known) >= max(3, window // 4):
            mean = sum(known) / len(known)
            std = math.sqrt(sum((x - mean) ** 2 for x in known) / len(known))
            out.append((mean - bounds * std, mean + bounds * std))
        else:
            out.append(None)
        history.append(v)
        if len(history) > window:
            history.pop(0)
    return out


def anomaly_bounds_agile(history: list[list], bounds: float, min_days: int = 3) -> list:
    """[lo, hi] par seau : médiane des mêmes créneaux des jours précédents ± bounds × MAD normalisé.

    `history` : une liste de valeurs par jour décalé (−1 j … −7 j), alignées sur la grille."""
    if not history:
        return []
    n = len(history[0])
    out = []
    for i in range(n):
        same = [h[i] for h in history if i < len(h) and h[i] is not None]
        if len(same) < min_days:
            out.append(None)
            continue
        m = _median(same)
        spread = MAD_SCALE * mad(same)
        if spread == 0:
            spread = abs(m) * 0.01 or 1e-9
        out.append((m - bounds * spread, m + bounds * spread))
    return out


def outlier_scores(series_values: list[list], tolerance: float) -> list[float]:
    """Part (0 à 1) des seaux où chaque série s'écarte de la médiane des autres de plus de
    `tolerance` × MAD normalisé (méthode MAD de Datadog)."""
    if not series_values:
        return []
    n = max(len(v) for v in series_values)
    outside = [0] * len(series_values)
    counted = [0] * len(series_values)
    for i in range(n):
        column = [(k, v[i]) for k, v in enumerate(series_values) if i < len(v) and v[i] is not None]
        if len(column) < 3:
            continue
        vals = [x for _, x in column]
        m = _median(vals)
        spread = MAD_SCALE * mad(vals)
        if spread == 0:
            spread = abs(m) * 0.01 or 1e-9
        for k, x in column:
            counted[k] += 1
            if abs(x - m) > tolerance * spread:
                outside[k] += 1
    return [outside[k] / counted[k] if counted[k] else 0.0 for k in range(len(series_values))]
