"""Validation d'un moniteur (contrat §§ 7, 17, 19) : rend la définition complétée des défauts.

Les erreurs sont des ValueError dont le message est montré tel quel à l'utilisateur.
"""
import re

from ..errors import QueryError
from ..query import logs_ql, metrics_ql
from ..util import TAG_KEY_RE, clean_tags
from . import composite

TYPES = ("metric", "service_check", "log", "host", "synthetic", "apm", "slo",
         "anomaly", "forecast", "outlier", "composite", "change", "event", "process", "error_tracking")
SLO_ON = {"budget": "<", "burn_rate": ">"}      # budget sous le seuil, ou vitesse au-dessus
SLO_WINDOWS = ("1h", "6h")
COMPARATORS = (">", ">=", "<", "<=")
AGGREGATIONS = ("avg", "max", "min", "sum", "last")
APM_METRICS = ("error_rate", "p95_ms", "hits")
LOG_GROUP_COLUMNS = ("service", "host", "status", "source")
EVENT_GROUP_COLUMNS = ("host", "source", "type", "alert_type", "priority")
THRESHOLD_KEYS = ("critical", "warning", "critical_recovery", "warning_recovery")
# Types dont l'état se lit dans des seuils numériques (les autres ont un état propre).
NUMERIC_TYPES = ("metric", "log", "apm", "slo", "anomaly", "change", "event", "error_tracking")
ANOMALY_ALGORITHMS = ("basic", "agile", "robust")
SEASONALITIES = ("hourly", "daily", "weekly")
DIRECTIONS = ("both", "above", "below")
FORECAST_ALGORITHMS = ("linear", "seasonal")
OUTLIER_ALGORITHMS = ("mad", "dbscan")
CHANGE_MODES = ("pct", "abs")
PROCESS_MATCH = ("exact", "contains", "regex")
OPTION_KEYS = ("depends_on_host", "depends_on", "flapping", "escalations", "require_consecutive",
               "recovery_consecutive", "evaluation_delay_s")
DEFAULT_FLAPPING = {"changes": 5, "window_s": 1800}
DAY = 86400


def _int(q: dict, key: str, lo: int, hi: int, default: int) -> int:
    v = q.get(key, default)
    if not isinstance(v, int) or isinstance(v, bool) or not lo <= v <= hi:
        raise ValueError(f"{key} doit être un entier entre {lo} et {hi}")
    return v


def _num(q: dict, key: str, lo: float, hi: float, default: float) -> float:
    v = q.get(key, default)
    if isinstance(v, bool) or not isinstance(v, (int, float)) or not lo <= v <= hi:
        raise ValueError(f"{key} doit être un nombre entre {lo:g} et {hi:g}")
    return float(v)


def _choice(q: dict, key: str, choices: tuple, default: str) -> str:
    v = q.get(key, default)
    if v not in choices:
        raise ValueError(f"{key} attendu : {'|'.join(choices)}")
    return v


def _window(q: dict, default: int = 300) -> int:
    w = q.get("window_s", default)
    if not isinstance(w, int) or isinstance(w, bool) or not 60 <= w <= 86400:
        raise ValueError("window_s doit être un entier entre 60 et 86400")
    return w


def _comparator(q: dict) -> str:
    c = q.get("comparator", ">")
    if c not in COMPARATORS:
        raise ValueError(f"comparator attendu : {' '.join(COMPARATORS)}")
    return c


def _tags(value, what: str) -> dict:
    if value is None:
        return {}
    if not isinstance(value, dict):
        raise ValueError(f"{what} doit être un objet {{clé: valeur}}")
    tags = clean_tags(value)
    if len(tags) != len(value):
        raise ValueError(f"{what} : clés [a-z0-9_.-/] et valeurs texte seulement")
    return tags


def _metric_q(query: dict) -> metrics_ql.MetricQuery:
    try:
        return metrics_ql.parse(query.get("q"))
    except QueryError as e:
        raise ValueError(f"requête de métrique : {e}")


def _log_by(by) -> list[str]:
    """`service host status source` ou `@chemin.attribut` (contrat § 23), 5 clés au plus."""
    if not isinstance(by, list) or len(by) > 5:
        raise ValueError(f"by accepte 5 clés au plus parmi {', '.join(LOG_GROUP_COLUMNS)} ou @chemin.attribut")
    out = []
    for k in by:
        if not isinstance(k, str):
            raise ValueError("by : liste de chaînes")
        if k.startswith("@"):
            try:
                logs_ql.attribute_path(k[1:])
            except QueryError as e:
                raise ValueError(f"by : {e}")
        elif k not in LOG_GROUP_COLUMNS:
            raise ValueError(f"by accepte : {', '.join(LOG_GROUP_COLUMNS)} ou @chemin.attribut")
        if k not in out:
            out.append(k)
    return out


def normalize_query(mtype: str, query) -> dict:
    if not isinstance(query, dict):
        raise ValueError("query doit être un objet")
    if mtype == "metric":
        q = query.get("q")
        _metric_q(query)
        agg = _choice(query, "aggregation", AGGREGATIONS, "avg")
        return {"q": q, "window_s": _window(query), "aggregation": agg, "comparator": _comparator(query)}
    if mtype == "host":
        return {"tags": _tags(query.get("tags"), "tags")}
    if mtype == "service_check":
        check = query.get("check")
        if not isinstance(check, str) or not check.strip() or len(check) > 200:
            raise ValueError("check (nom du contrôle) est obligatoire")
        by = query.get("by", ["host"])
        if not isinstance(by, list) or not all(isinstance(k, str) and TAG_KEY_RE.match(k) for k in by) or len(by) > 10:
            raise ValueError("by doit être une liste de noms d'étiquettes")
        out = {"check": check.strip(), "tags": _tags(query.get("tags"), "tags"), "by": by}
        out["window_s"] = _window(query) if "window_s" in query else 300
        return out
    if mtype == "log":
        q = query.get("q", "")
        if not isinstance(q, str):
            raise ValueError("q doit être une chaîne")
        try:
            logs_ql.parse(q)
        except QueryError as e:
            raise ValueError(f"requête de journaux : {e}")
        return {"q": q, "window_s": _window(query), "comparator": _comparator(query),
                "by": _log_by(query.get("by", []))}
    if mtype == "synthetic":
        test_id = query.get("test_id")
        failures = query.get("failures", 1)
        if not isinstance(test_id, int) or isinstance(test_id, bool):
            raise ValueError("test_id (identifiant du test synthétique) est obligatoire")
        if not isinstance(failures, int) or isinstance(failures, bool) or not 1 <= failures <= 20:
            raise ValueError("failures doit être un entier entre 1 et 20")
        out = {"test_id": test_id, "failures": failures}
        if "locations_min_failing" in query:
            out["locations_min_failing"] = _int(query, "locations_min_failing", 1, 50, 1)
        return out
    if mtype == "apm":
        service = query.get("service")
        if not isinstance(service, str) or not service.strip():
            raise ValueError("service est obligatoire")
        metric = query.get("metric")
        if metric not in APM_METRICS:
            raise ValueError(f"metric attendue : {'|'.join(APM_METRICS)}")
        return {"service": service.strip(), "metric": metric, "window_s": _window(query),
                "comparator": _comparator(query)}
    if mtype == "slo":
        slo_id = query.get("slo_id")
        if not isinstance(slo_id, int) or isinstance(slo_id, bool):
            raise ValueError("slo_id (identifiant du SLO) est obligatoire")
        on = query.get("on", "budget")
        if on not in SLO_ON:
            raise ValueError(f"on attendu : {'|'.join(SLO_ON)}")
        window = query.get("window", "1h")
        if window not in SLO_WINDOWS:
            raise ValueError(f"window attendue : {'|'.join(SLO_WINDOWS)}")
        # Le sens de comparaison découle de « on » : il n'est pas laissé au choix.
        return {"slo_id": slo_id, "on": on, "window": window, "comparator": SLO_ON[on]}
    if mtype == "anomaly":
        _metric_q(query)
        return {"q": query["q"], "window_s": _window(query, 900),
                "algorithm": _choice(query, "algorithm", ANOMALY_ALGORITHMS, "basic"),
                "bounds": _num(query, "bounds", 0.5, 10.0, 2.0),
                "direction": _choice(query, "direction", DIRECTIONS, "both"),
                "seasonality": _choice(query, "seasonality", SEASONALITIES, "daily"),
                "comparator": ">="}
    if mtype == "forecast":
        _metric_q(query)
        history = _int(query, "history_s", 3600, 90 * DAY, 7 * DAY)
        horizon = _int(query, "horizon_s", 600, 90 * DAY, 2 * DAY)
        algorithm = _choice(query, "algorithm", FORECAST_ALGORITHMS, "linear")
        if algorithm == "seasonal" and history < DAY:
            raise ValueError("la prévision saisonnière demande history_s d'au moins 86400 (un jour)")
        return {"q": query["q"], "history_s": history, "horizon_s": horizon, "algorithm": algorithm,
                "comparator": _comparator(query)}
    if mtype == "outlier":
        mq = _metric_q(query)
        if not mq.group_by:
            raise ValueError("un moniteur outlier compare des groupes : ajoutez « by {host} » à la requête")
        return {"q": query["q"], "window_s": _window(query, 900),
                "algorithm": _choice(query, "algorithm", OUTLIER_ALGORITHMS, "mad"),
                "tolerance": _num(query, "tolerance", 0.5, 20.0, 3.0), "pct": _num(query, "pct", 0.0, 100.0, 20.0)}
    if mtype == "composite":
        expr = query.get("expression")
        try:
            node = composite.parse(expr)
        except ValueError as e:
            raise ValueError(f"expression : {e}")
        warn = query.get("warn_as_true", False)
        if not isinstance(warn, bool):
            raise ValueError("warn_as_true doit être un booléen")
        return {"expression": " ".join(expr.split()), "warn_as_true": warn, "ids": sorted(composite.ids(node))}
    if mtype == "change":
        _metric_q(query)
        return {"q": query["q"], "window_s": _window(query), "shift_s": _int(query, "shift_s", 60, 90 * DAY, 3600),
                "mode": _choice(query, "mode", CHANGE_MODES, "pct"),
                "aggregation": _choice(query, "aggregation", AGGREGATIONS, "avg"), "comparator": _comparator(query)}
    if mtype == "event":
        q = query.get("q", "")
        if not isinstance(q, str):
            raise ValueError("q doit être une chaîne")
        try:
            logs_ql.compile_query(q, logs_ql.EventsResolver())
        except QueryError as e:
            raise ValueError(f"requête d'événements : {e}")
        by = query.get("by", [])
        if not isinstance(by, list) or len(by) > 5 or not all(
                isinstance(k, str) and (k in EVENT_GROUP_COLUMNS or TAG_KEY_RE.match(k)) for k in by):
            raise ValueError(f"by : 5 clés au plus parmi {', '.join(EVENT_GROUP_COLUMNS)} ou des étiquettes")
        return {"q": q, "window_s": _window(query), "by": list(dict.fromkeys(by)), "comparator": _comparator(query)}
    if mtype == "process":
        name = query.get("name")
        if not isinstance(name, str) or not name.strip() or len(name) > 200:
            raise ValueError("name (nom du processus) est obligatoire")
        match = _choice(query, "match", PROCESS_MATCH, "exact")
        if match == "regex":
            try:
                re.compile(name)
            except re.error as e:
                raise ValueError(f"expression régulière invalide : {e}")
        by = query.get("by", ["host"])
        if by != ["host"]:
            raise ValueError("by vaut [\"host\"] : le compte se fait par hôte")
        lo = _int(query, "min", 0, 100000, 1)
        hi = query.get("max")
        if hi is not None:
            hi = _int(query, "max", 0, 100000, 0)
            if hi < lo:
                raise ValueError("max doit être supérieur ou égal à min")
        return {"name": name.strip(), "match": match, "tags": _tags(query.get("tags"), "tags"), "by": ["host"],
                "min": lo, "max": hi}
    if mtype == "error_tracking":
        service = query.get("service")
        if service is not None and (not isinstance(service, str) or not service.strip() or len(service) > 200):
            raise ValueError("service doit être un nom de service")
        new_only = query.get("new_only", True)
        if not isinstance(new_only, bool):
            raise ValueError("new_only doit être un booléen")
        return {"service": service.strip() if service else None, "new_only": new_only,
                "window_s": _window(query), "comparator": _comparator(query)}
    raise ValueError(f"type attendu : {'|'.join(TYPES)}")


def normalize_thresholds(mtype: str, thresholds, comparator: str | None) -> dict:
    if thresholds is None:
        thresholds = {}
    if not isinstance(thresholds, dict):
        raise ValueError("thresholds doit être un objet")
    out = {}
    for k, v in thresholds.items():
        if k not in THRESHOLD_KEYS:
            raise ValueError(f"seuil inconnu : {k} (attendu {', '.join(THRESHOLD_KEYS)})")
        if v is None:
            continue
        if isinstance(v, bool) or not isinstance(v, (int, float)):
            raise ValueError(f"le seuil {k} doit être un nombre")
        out[k] = float(v)
    if mtype == "anomaly":
        out.setdefault("critical", 50.0)       # part des points hors bornes
        if not all(0 <= v <= 100 for v in out.values()):
            raise ValueError("les seuils d'un moniteur anomaly sont des parts en % (0 à 100)")
    if mtype not in NUMERIC_TYPES and mtype != "forecast":
        return out
    if "critical" not in out:
        raise ValueError("thresholds.critical est obligatoire pour ce type")
    above = comparator in (">", ">=")

    def worse(a, b):           # a est-il « plus grave » que b ?
        return a > b if above else a < b

    if "warning" in out and not worse(out["critical"], out["warning"]):
        raise ValueError("le seuil warning doit être moins grave que critical")
    if "critical_recovery" in out and not worse(out["critical"], out["critical_recovery"]):
        raise ValueError("critical_recovery doit être en deçà de critical (sinon l'alerte ne se lève ou ne retombe jamais)")
    if "warning_recovery" in out:
        if "warning" not in out:
            raise ValueError("warning_recovery demande un seuil warning")
        if not worse(out["warning"], out["warning_recovery"]):
            raise ValueError("warning_recovery doit être en deçà de warning")
    return out


def _ids(value, what: str, limit: int) -> list[int]:
    if value is None:
        return []
    if not isinstance(value, list) or len(value) > limit or not all(
            isinstance(i, int) and not isinstance(i, bool) for i in value):
        raise ValueError(f"{what} doit être une liste d'entiers ({limit} au plus)")
    return sorted(set(value))


def normalize_options(options) -> dict:
    """Options du § 17, défauts compris."""
    if options is None:
        options = {}
    if not isinstance(options, dict):
        raise ValueError("options doit être un objet")
    unknown = set(options) - set(OPTION_KEYS)
    if unknown:
        raise ValueError(f"option inconnue : {sorted(unknown)[0]} (attendu {', '.join(OPTION_KEYS)})")
    dep_host = options.get("depends_on_host", True)
    if not isinstance(dep_host, bool):
        raise ValueError("options.depends_on_host doit être un booléen")
    flap = options.get("flapping", DEFAULT_FLAPPING)
    if flap is None or flap is False:
        flap = False
    elif isinstance(flap, dict):
        flap = {"changes": _int(flap, "changes", 2, 100, 5), "window_s": _int(flap, "window_s", 60, DAY, 1800)}
    else:
        raise ValueError("options.flapping : {changes, window_s} ou false")
    escalations = options.get("escalations") or []
    if not isinstance(escalations, list) or len(escalations) > 5:
        raise ValueError("options.escalations : liste de 5 marches au plus")
    steps = []
    for e in escalations:
        if not isinstance(e, dict):
            raise ValueError("chaque escalade est un objet {after_s, channel_ids, message}")
        ids = _ids(e.get("channel_ids"), "escalations.channel_ids", 20)
        if not ids:
            raise ValueError("chaque escalade vise au moins un canal (channel_ids)")
        message = e.get("message", "") or ""
        if not isinstance(message, str) or len(message) > 2000:
            raise ValueError("escalations.message : chaîne de 2 000 caractères au plus")
        steps.append({"after_s": _int(e, "after_s", 60, 30 * DAY, 900), "channel_ids": ids, "message": message})
    steps.sort(key=lambda e: e["after_s"])
    if len({e["after_s"] for e in steps}) != len(steps):
        raise ValueError("deux escalades ne peuvent pas avoir le même after_s")
    return {"depends_on_host": dep_host, "depends_on": _ids(options.get("depends_on"), "options.depends_on", 20),
            "flapping": flap, "escalations": steps,
            "require_consecutive": _int(options, "require_consecutive", 1, 20, 1),
            "recovery_consecutive": _int(options, "recovery_consecutive", 1, 20, 1),
            "evaluation_delay_s": _int(options, "evaluation_delay_s", 0, 3600, 0)}


def normalize_priority(value) -> int | None:
    if value is None:
        return None
    if isinstance(value, bool) or not isinstance(value, int) or not 1 <= value <= 5:
        raise ValueError("priority vaut 1 (critique) à 5 (info), ou null")
    return value


def normalize_monitor(data: dict) -> dict:
    """Définition complète et vérifiée d'un moniteur (hors existence des canaux et du test)."""
    name = data.get("name")
    if not isinstance(name, str) or not name.strip() or len(name) > 200:
        raise ValueError("name est obligatoire (200 caractères au plus)")
    mtype = data.get("type")
    if mtype not in TYPES:
        raise ValueError(f"type attendu : {'|'.join(TYPES)}")
    query = normalize_query(mtype, data.get("query"))
    thresholds = normalize_thresholds(mtype, data.get("thresholds"), query.get("comparator"))
    message = data.get("message", "") or ""
    if not isinstance(message, str) or len(message) > 10000:
        raise ValueError("message doit être une chaîne (10 000 caractères au plus)")
    channel_ids = data.get("channel_ids", []) or []
    if not isinstance(channel_ids, list) or not all(isinstance(c, int) and not isinstance(c, bool) for c in channel_ids):
        raise ValueError("channel_ids doit être une liste d'entiers")
    no_data_s = data.get("no_data_s", 600)
    renotify_s = data.get("renotify_s", 0)
    if not isinstance(no_data_s, int) or isinstance(no_data_s, bool) or not 60 <= no_data_s <= 7 * 86400:
        raise ValueError("no_data_s doit être un entier entre 60 et 604800")
    if not isinstance(renotify_s, int) or isinstance(renotify_s, bool) or not (renotify_s == 0 or 300 <= renotify_s <= 7 * 86400):
        raise ValueError("renotify_s vaut 0 (pas de relance) ou un entier entre 300 et 604800")
    return {
        "name": name.strip(), "type": mtype, "query": query, "thresholds": thresholds, "message": message,
        "channel_ids": sorted(set(channel_ids)), "notify_no_data": bool(data.get("notify_no_data", False)),
        "no_data_s": no_data_s, "renotify_s": renotify_s, "tags": _tags(data.get("tags"), "tags"),
        "enabled": bool(data.get("enabled", True)), "priority": normalize_priority(data.get("priority")),
        "options": normalize_options(data.get("options")),
    }
