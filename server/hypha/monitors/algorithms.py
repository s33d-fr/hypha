"""Algorithmes des moniteurs avancés (contrat § 19), sans accès à la base.

Une série est une liste [(ts_ms, valeur)] triée par temps, sans valeur absente. Les écarts
robustes passent par la MAD (médiane des écarts absolus à la médiane) × 1,4826, comparable à un
écart-type pour une loi normale. Une échelle nulle (série plate) est relevée à 1 % du niveau de
la série : sans ce plancher, le moindre frémissement d'une courbe plate serait une anomalie.
"""
import math
from statistics import median as _median

MAD_SCALE = 1.4826
SEASONS = {"hourly": 3600, "daily": 86400, "weekly": 604800}
# Créneaux de référence de l'algorithme `agile` : la dernière journée pour une saison horaire, les
# 7 derniers jours pour une saison quotidienne, les 3 dernières semaines pour une saison hebdomadaire.
AGILE_SEASONS = {"hourly": 24, "daily": 7, "weekly": 3}
BASIC_LOOKBACK_S = 7200
MIN_REFS = 3
MAX_THEIL_SEN_POINTS = 150


def median(xs):
    return _median(xs) if xs else None


def mad(xs, center=None) -> float:
    if not xs:
        return 0.0
    c = median(xs) if center is None else center
    return median([abs(x - c) for x in xs])


def robust_scale(center: float, spread: float) -> float:
    """Échelle d'écart, jamais nulle : au moins 1 % du niveau, au moins 1e-9."""
    return max(spread, abs(center or 0.0) * 0.01, 1e-9)


def outside(v: float, lo: float, hi: float, direction: str) -> bool:
    if direction == "above":
        return v > hi
    if direction == "below":
        return v < lo
    return v < lo or v > hi


# ── anomalies ─────────────────────────────────────────────────────────────────

def enough_history(algorithm: str, history: list, window_start_ms: int, season_s: int) -> bool:
    """« Au moins une saison » : la référence couvre la saison (la moitié des 2 h pour `basic`)."""
    if not history:
        return False
    need_s = BASIC_LOOKBACK_S / 2 if algorithm == "basic" else season_s
    return history[0][0] <= window_start_ms - need_s * 1000


def _basic(points: list, window: list, k: float) -> list:
    out = []
    for ts, v in window:
        refs = [x for t, x in points if ts - BASIC_LOOKBACK_S * 1000 <= t < ts]
        if len(refs) < 5:
            continue
        mean = sum(refs) / len(refs)
        std = math.sqrt(sum((x - mean) ** 2 for x in refs) / len(refs))
        s = robust_scale(mean, std)
        out.append((ts, v, mean - k * s, mean + k * s))
    return out


def _agile(points: list, window: list, k: float, season_s: int, n_seasons: int) -> list:
    index = dict(points)
    out = []
    for ts, v in window:
        refs = [index[ts - i * season_s * 1000] for i in range(1, n_seasons + 1)
                if ts - i * season_s * 1000 in index]
        if len(refs) < MIN_REFS:
            continue
        med = median(refs)
        s = robust_scale(med, MAD_SCALE * mad(refs, med))
        out.append((ts, v, med - k * s, med + k * s))
    return out


def _trailing_median(points: list, ts: int, span_ms: int):
    vals = [x for t, x in points if ts - span_ms < t <= ts]
    return median(vals)


def decompose(history: list, season_s: int, step_s: int) -> dict | None:
    """Tendance (médiane glissante sur une saison), saison (médiane par créneau), échelle des résidus."""
    if len(history) < 10:
        return None
    span = season_s * 1000
    # Tendance calculée sur 24 ancres par saison puis interpolée : une médiane par point coûterait
    # des secondes de calcul par groupe sur 7 jours à 5 min.
    first, last = history[0][0], history[-1][0]
    anchor_step = max(step_s * 1000, span // 24)
    anchors = []
    t = first
    while t <= last:
        m = _trailing_median(history, t, span)
        if m is not None:
            anchors.append((t, m))
        t += anchor_step
    if not anchors or anchors[-1][0] != last:
        anchors.append((last, _trailing_median(history, last, span)))

    def trend(ts: int) -> float:
        if ts <= anchors[0][0]:
            return anchors[0][1]
        for (t0, v0), (t1, v1) in zip(anchors, anchors[1:]):
            if t0 <= ts <= t1:
                return v0 + (v1 - v0) * (ts - t0) / (t1 - t0) if t1 > t0 else v1
        return anchors[-1][1]

    slots = max(1, season_s // step_s)
    phase = {}
    detrended = []
    for ts, v in history:
        d = v - trend(ts)
        p = (ts // (step_s * 1000)) % slots
        phase.setdefault(p, []).append(d)
        detrended.append((ts, p, d))
    seasonal = {p: median(ds) for p, ds in phase.items()}
    residuals = [d - seasonal.get(p, 0.0) for _, p, d in detrended]
    center = median([v for _, v in history])
    return {"seasonal": seasonal, "slots": slots, "step_s": step_s, "season_s": season_s,
            "scale": robust_scale(center, MAD_SCALE * mad(residuals))}


def _robust(points: list, window: list, k: float, model: dict) -> list:
    out = []
    span = model["season_s"] * 1000
    for ts, v in window:
        trend = _trailing_median(points, ts, span)
        if trend is None:
            continue
        p = (ts // (model["step_s"] * 1000)) % model["slots"]
        expected = trend + model["seasonal"].get(p, 0.0)
        out.append((ts, v, expected - k * model["scale"], expected + k * model["scale"]))
    return out


def anomaly_bounds(algorithm: str, history: list, window: list, k: float, season: str, step_s: int,
                   model: dict | None = None) -> list:
    """Bornes de chaque point de la fenêtre : [(ts, valeur, basse, haute)]."""
    points = history + window
    season_s = SEASONS[season]
    if algorithm == "basic":
        return _basic(points, window, k)
    if algorithm == "agile":
        return _agile(points, window, k, season_s, AGILE_SEASONS[season])
    model = model or decompose(history, season_s, step_s)
    return _robust(points, window, k, model) if model else []


def anomaly_share(bounds: list, direction: str) -> float | None:
    """Part (%) des points de la fenêtre hors bornes ; None sans point évaluable."""
    if not bounds:
        return None
    n = sum(1 for _, v, lo, hi in bounds if outside(v, lo, hi, direction))
    return 100.0 * n / len(bounds)


# ── prévision ─────────────────────────────────────────────────────────────────

def _thin(points: list, n: int) -> list:
    if len(points) <= n:
        return points
    step = (len(points) - 1) / (n - 1)
    return [points[round(i * step)] for i in range(n)]


def theil_sen(xs: list[float], ys: list[float]) -> tuple[float, float]:
    """Pente (médiane des pentes deux à deux) et ordonnée (médiane des y − pente·x)."""
    slopes = [(ys[j] - ys[i]) / (xs[j] - xs[i])
              for i in range(len(xs)) for j in range(i + 1, len(xs)) if xs[j] != xs[i]]
    b = median(slopes) if slopes else 0.0
    a = median([y - b * x for x, y in zip(xs, ys)])
    return b, a


def seasonal_slope(points: list, bin_s: int = 3600, period_s: int = 86400) -> float | None:
    """Pente médiane des paires d'un même créneau (heure du jour) séparées d'au moins une demi-période."""
    by_slot: dict[int, list] = {}
    for ts, v in points:
        x = ts / 1000
        by_slot.setdefault(int(x % period_s) // bin_s, []).append((x, v))
    slopes = [(v2 - v1) / (x2 - x1) for pts in by_slot.values() for i, (x1, v1) in enumerate(pts)
              for x2, v2 in pts[i + 1:] if x2 - x1 >= period_s / 2]
    return median(slopes) if len(slopes) >= 3 else None


def breach(value: float, threshold: float, comparator: str) -> bool:
    return {">": value > threshold, ">=": value >= threshold,
            "<": value < threshold, "<=": value <= threshold}[comparator]


def fit_forecast(points: list, algorithm: str) -> dict | None:
    """Modèle de prévision ; None si les données sont trop maigres (12 points sur 1 h, ou 1 jour en saisonnier)."""
    if len(points) < 12:
        return None
    span_s = (points[-1][0] - points[0][0]) / 1000
    if span_s < 3600 or (algorithm == "seasonal" and span_s < 86400):
        return None
    thin = _thin(points, MAX_THEIL_SEN_POINTS)
    t0 = points[-1][0] / 1000
    xs = [ts / 1000 - t0 for ts, _ in thin]
    ys = [v for _, v in thin]
    b, a = theil_sen(xs, ys)
    if algorithm == "seasonal":
        # Theil-Sen saisonnier : pentes entre points du même créneau horaire, d'un jour à l'autre ;
        # la saison s'annule dans chaque paire et ne fausse plus la tendance.
        sb = seasonal_slope(thin)
        if sb is not None:
            b = sb
            a = median([y - b * x for x, y in zip(xs, ys)])
    residuals = [(ts / 1000, v - (a + b * (ts / 1000 - t0))) for ts, v in points]
    seasonal, bin_s = {}, 3600
    if algorithm == "seasonal":
        by_slot: dict[int, list] = {}
        for x, r in residuals:
            by_slot.setdefault(int(x % 86400) // bin_s, []).append(r)
        seasonal = {s: median(rs) for s, rs in by_slot.items()}
        residuals = [(x, r - seasonal.get(int(x % 86400) // bin_s, 0.0)) for x, r in residuals]
    rs = [r for _, r in residuals]
    return {"t0": t0, "slope": b, "intercept": a, "seasonal": seasonal, "bin_s": bin_s,
            "sigma": MAD_SCALE * mad(rs), "last": points[-1][1]}


def predict(model: dict, t_s: float) -> float:
    v = model["intercept"] + model["slope"] * (t_s - model["t0"])
    if model["seasonal"]:
        v += model["seasonal"].get(int(t_s % 86400) // model["bin_s"], 0.0)
    return v


def time_to_breach(model: dict, threshold: float, comparator: str, horizon_s: float, now_s: float) -> float:
    """Secondes avant que la prévision franchisse le seuil ; 0 si déjà franchi ; −1 si jamais.

    Au-delà de l'horizon, la tendance linéaire seule donne l'échéance (rendue même si elle dépasse
    l'horizon : la valeur du moniteur dit « dans 9 jours »).
    """
    if breach(model["last"], threshold, comparator):
        return 0.0
    step = max(60.0, horizon_s / 200)
    t = step
    while t <= horizon_s:
        if breach(predict(model, now_s + t), threshold, comparator):
            return t
        t += step
    b = model["slope"]
    if b == 0:
        return -1.0
    level = model["intercept"] + b * (now_s - model["t0"])
    rising = comparator in (">", ">=")
    if (rising and b > 0) or (not rising and b < 0):
        # Rien dans l'horizon : l'échéance est au-delà, même si la saison l'a repoussée.
        return max((threshold - level) / b, horizon_s + step)
    return -1.0


def forecast_points(model: dict, now_s: float, horizon_s: float, n: int = 100) -> list:
    """[[ts_ms, valeur, basse, haute]] de maintenant à l'horizon (bande à ± 2 écarts robustes)."""
    band = 2 * model["sigma"]
    step = max(60.0, horizon_s / n)
    out, t = [], step
    while t <= horizon_s:
        v = predict(model, now_s + t)
        out.append([int((now_s + t) * 1000), v, v - band, v + band])
        t += step
    return out


def forecast_state(eta_critical: float, eta_warning: float | None, horizon_s: float) -> str:
    if 0 <= eta_critical <= horizon_s:
        return "ALERT"
    if eta_warning is not None and 0 <= eta_warning <= horizon_s:
        return "WARN"
    return "OK"


# ── groupes aberrants ─────────────────────────────────────────────────────────

def outliers_mad(series: dict[str, dict[int, float]], tolerance: float) -> dict[str, float]:
    """Part (%) du temps où chaque groupe s'écarte de la médiane des groupes de plus de
    `tolerance` écarts robustes. Un instant ne compte qu'avec au moins 3 groupes présents."""
    stamps: dict[int, dict[str, float]] = {}
    for g, pts in series.items():
        for ts, v in pts.items():
            stamps.setdefault(ts, {})[g] = v
    out_n = {g: 0 for g in series}
    seen = {g: 0 for g in series}
    for values in stamps.values():
        if len(values) < 3:
            continue
        vals = list(values.values())
        med = median(vals)
        scale = robust_scale(med, MAD_SCALE * mad(vals, med))
        for g, v in values.items():
            seen[g] += 1
            if abs(v - med) > tolerance * scale:
                out_n[g] += 1
    return {g: 100.0 * out_n[g] / seen[g] for g in series if seen[g]}


def _distance(a: dict[int, float], b: dict[int, float]) -> float:
    common = a.keys() & b.keys()
    if not common:
        return math.inf
    return sum(abs(a[t] - b[t]) for t in common) / len(common)


def outliers_dbscan(series: dict[str, dict[int, float]], tolerance: float) -> dict[str, float]:
    """DBSCAN sur les séries entières : distance = écart absolu moyen aux instants communs,
    ε = tolerance × distance médiane à la série médiane, voisinage minimal = la moitié des groupes.
    Les groupes hors de la plus grande grappe valent 100, les autres 0."""
    groups = [g for g, pts in series.items() if pts]
    if len(groups) < 3:
        return {g: 0.0 for g in groups}
    stamps: dict[int, list] = {}
    for g in groups:
        for ts, v in series[g].items():
            stamps.setdefault(ts, []).append(v)
    med_series = {ts: median(vs) for ts, vs in stamps.items() if len(vs) >= 2}
    to_median = [_distance(series[g], med_series) for g in groups]
    finite = [d for d in to_median if math.isfinite(d)]
    level = median([abs(v) for vs in stamps.values() for v in vs]) or 0.0
    eps = tolerance * robust_scale(level, median(finite) if finite else 0.0)
    min_pts = max(2, math.ceil(len(groups) / 2))
    neighbors = {g: [h for h in groups if h != g and _distance(series[g], series[h]) <= eps] for g in groups}
    cluster: dict[str, int] = {}
    cid = 0
    for g in groups:
        if g in cluster or len(neighbors[g]) + 1 < min_pts:
            continue
        cid += 1
        queue = [g]
        cluster[g] = cid
        while queue:
            cur = queue.pop()
            if len(neighbors[cur]) + 1 < min_pts:
                continue
            for h in neighbors[cur]:
                if h not in cluster:
                    cluster[h] = cid
                    queue.append(h)
    if not cluster:
        return {g: 0.0 for g in groups}
    sizes: dict[int, int] = {}
    for c in cluster.values():
        sizes[c] = sizes.get(c, 0) + 1
    main = max(sizes, key=lambda c: (sizes[c], -c))
    return {g: 0.0 if cluster.get(g) == main else 100.0 for g in groups}


# ── variation ─────────────────────────────────────────────────────────────────

def change_value(current: float | None, previous: float | None, mode: str) -> float | None:
    """Variation de la fenêtre courante par rapport à la fenêtre décalée : absolue ou en %."""
    if current is None or previous is None:
        return None
    if mode == "abs":
        return current - previous
    if previous == 0:
        return 0.0 if current == 0 else None
    return (current - previous) / abs(previous) * 100.0
