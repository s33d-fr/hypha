"""Maintenances planifiées (contrat § 16) : validation, occurrences, portée. Sans accès à la base.

Une maintenance simple couvre [start_ts, end_ts) (end_ts absent = sans fin). Une maintenance
récurrente (`daily`, `weekly` jours 1 = lundi … 7, `monthly` jour du mois) ouvre chaque jour retenu
une occurrence qui commence à `start` (« HH:MM ») dans le fuseau `tz` et dure `duration_s` ; elle
n'est active que dans l'intersection de l'occurrence, de [start_ts, end_ts) et de l'avant `until`.
Un `day_of_month` au-delà de la fin du mois (31 en avril) tombe le dernier jour du mois.
Heure qui n'existe pas (passage à l'heure d'été) : zoneinfo la décale d'une heure, comme l'horloge.
"""
import calendar
import re
from dataclasses import dataclass, field
from datetime import date, datetime, time, timedelta, timezone
from zoneinfo import ZoneInfo, ZoneInfoNotFoundError

from ..util import clean_tags, ms_to_dt, parse_group_key

UTC = timezone.utc
RECURRENCE_TYPES = ("daily", "weekly", "monthly")
_HHMM_RE = re.compile(r"^([01]\d|2[0-3]):([0-5]\d)$")
MAX_DURATION_S = 31 * 86400
SEARCH_DAYS = 400              # horizon de recherche de la prochaine occurrence


def zone(name: str | None) -> ZoneInfo:
    try:
        return ZoneInfo(name or "UTC")
    except (ZoneInfoNotFoundError, ValueError):
        raise ValueError(f"fuseau inconnu : {name}")


# ── validation ──────────────────────────────────────────────────────────────────

def _int(v, what: str, lo: int, hi: int) -> int:
    if isinstance(v, bool) or not isinstance(v, int) or not lo <= v <= hi:
        raise ValueError(f"{what} doit être un entier entre {lo} et {hi}")
    return v


def _ms(v, what: str) -> int:
    if isinstance(v, bool) or not isinstance(v, (int, float)) or v <= 0:
        raise ValueError(f"{what} doit être un horodatage en millisecondes")
    return int(v)


def normalize_scope(scope) -> dict:
    if scope is None:
        scope = {}
    if not isinstance(scope, dict):
        raise ValueError("scope doit être un objet {monitor_ids, tags, groups}")
    unknown = set(scope) - {"monitor_ids", "tags", "groups"}
    if unknown:
        raise ValueError(f"scope : clé inconnue {sorted(unknown)[0]} (attendu monitor_ids, tags, groups)")
    ids = scope.get("monitor_ids") or []
    if not isinstance(ids, list) or len(ids) > 500 or not all(isinstance(i, int) and not isinstance(i, bool) for i in ids):
        raise ValueError("scope.monitor_ids doit être une liste d'entiers (500 au plus)")
    tags = scope.get("tags") or {}
    if not isinstance(tags, dict) or len(clean_tags(tags)) != len(tags):
        raise ValueError("scope.tags doit être un objet {clé: valeur texte}")
    groups = scope.get("groups") or []
    if not isinstance(groups, list) or len(groups) > 500 or not all(
            isinstance(g, str) and 0 < len(g) <= 1000 for g in groups):
        raise ValueError("scope.groups doit être une liste de clés de groupe (500 au plus)")
    return {"monitor_ids": sorted(set(ids)), "tags": clean_tags(tags), "groups": list(dict.fromkeys(groups))}


def normalize_recurrence(rec) -> dict | None:
    if rec is None:
        return None
    if not isinstance(rec, dict):
        raise ValueError("recurrence doit être un objet ou null")
    kind = rec.get("type")
    if kind not in RECURRENCE_TYPES:
        raise ValueError(f"recurrence.type attendu : {'|'.join(RECURRENCE_TYPES)}")
    start = rec.get("start")
    if not isinstance(start, str) or not _HHMM_RE.match(start):
        raise ValueError("recurrence.start attendu au format HH:MM")
    out = {"type": kind, "start": start,
           "duration_s": _int(rec.get("duration_s"), "recurrence.duration_s", 60, MAX_DURATION_S),
           "tz": rec.get("tz") or "UTC"}
    zone(out["tz"])
    if kind == "weekly":
        days = rec.get("days")
        if not isinstance(days, list) or not days or not all(
                isinstance(d, int) and not isinstance(d, bool) and 1 <= d <= 7 for d in days):
            raise ValueError("recurrence.days : liste de jours 1 (lundi) à 7 (dimanche)")
        out["days"] = sorted(set(days))
    if kind == "monthly":
        out["day_of_month"] = _int(rec.get("day_of_month"), "recurrence.day_of_month", 1, 31)
    if rec.get("until") is not None:
        out["until"] = _ms(rec["until"], "recurrence.until")
    return out


def normalize_downtime(data: dict, now_ms: int, current: dict | None = None) -> dict:
    """Définition complète et vérifiée ; `current` (forme normalisée) sert de base à une modification.

    `duration_s` sans `end_ts` fixe la fin à début + durée ; sans `start_ts`, le début est maintenant.
    """
    if not isinstance(data, dict):
        raise ValueError("le corps doit être un objet JSON")
    base = dict(current or {})
    merged = {**base, **{k: v for k, v in data.items() if k in
                         ("name", "message", "scope", "start_ts", "end_ts", "recurrence", "enabled")}}
    name = merged.get("name")
    if not isinstance(name, str) or not name.strip() or len(name) > 200:
        raise ValueError("name est obligatoire (200 caractères au plus)")
    message = merged.get("message") or ""
    if not isinstance(message, str) or len(message) > 2000:
        raise ValueError("message doit être une chaîne (2 000 caractères au plus)")
    start = merged.get("start_ts")
    start = now_ms if start is None else _ms(start, "start_ts")
    end = merged.get("end_ts")
    if "duration_s" in data and data["duration_s"] is not None and "end_ts" not in data:
        end = start + 1000 * _int(data["duration_s"], "duration_s", 60, 366 * 86400)
    elif end is not None:
        end = _ms(end, "end_ts")
    if end is not None and end <= start:
        raise ValueError("end_ts doit suivre start_ts")
    enabled = merged.get("enabled", True)
    if not isinstance(enabled, bool):
        raise ValueError("enabled doit être un booléen")
    return {"name": name.strip(), "message": message, "scope": normalize_scope(merged.get("scope")),
            "start_ts": start, "end_ts": end, "recurrence": normalize_recurrence(merged.get("recurrence")),
            "enabled": enabled}


# ── occurrences ─────────────────────────────────────────────────────────────────

@dataclass
class Window:
    start: datetime
    end: datetime | None          # None = sans fin


def _as_dt(v) -> datetime | None:
    if v is None:
        return None
    if isinstance(v, datetime):
        return v if v.tzinfo else v.replace(tzinfo=UTC)
    return ms_to_dt(v)


def _day_matches(rec: dict, d: date) -> bool:
    if rec["type"] == "daily":
        return True
    if rec["type"] == "weekly":
        return d.isoweekday() in rec["days"]
    last = calendar.monthrange(d.year, d.month)[1]
    return d.day == min(rec["day_of_month"], last)


def _occurrence(rec: dict, d: date, tz: ZoneInfo) -> tuple[datetime, datetime]:
    hh, mm = (int(x) for x in rec["start"].split(":"))
    local = datetime.combine(d, time(hh, mm), tzinfo=tz)
    start = local.astimezone(UTC)
    return start, start + timedelta(seconds=rec["duration_s"])


def _bounds(dt: dict) -> tuple[datetime, datetime | None]:
    start = _as_dt(dt["start_ts"])
    end = _as_dt(dt.get("end_ts"))
    rec = dt.get("recurrence")
    if rec and rec.get("until") is not None:
        until = ms_to_dt(rec["until"])
        end = until if end is None else min(end, until)
    return start, end


def _clip(a: datetime, b: datetime, lo: datetime, hi: datetime | None) -> Window | None:
    s = max(a, lo)
    e = b if hi is None else min(b, hi)
    return Window(s, e) if s < e else None


def occurrences(dt: dict, frm: datetime, to: datetime) -> list[Window]:
    """Fenêtres effectives d'une maintenance qui recoupent [frm, to), dans l'ordre."""
    lo, hi = _bounds(dt)
    rec = dt.get("recurrence")
    if not rec:
        w = Window(lo, hi)
        if (hi is None or hi > frm) and lo < to and (hi is None or lo < hi):
            return [w]
        return []
    tz = zone(rec.get("tz"))
    first = (max(frm, lo) - timedelta(seconds=rec["duration_s"]) - timedelta(days=1)).astimezone(tz).date()
    last = to.astimezone(tz).date() + timedelta(days=1)
    out = []
    d = first
    while d <= last:
        if _day_matches(rec, d):
            a, b = _occurrence(rec, d, tz)
            w = _clip(a, b, lo, hi)
            if w and w.start < to and (w.end is None or w.end > frm):
                out.append(w)
        d += timedelta(days=1)
    return out


def active_window(dt: dict, now: datetime) -> Window | None:
    """Fenêtre en cours à `now` (maintenance active), ou None."""
    if not dt.get("enabled", True):
        return None
    for w in occurrences(dt, now, now + timedelta(microseconds=1)):
        if w.start <= now and (w.end is None or now < w.end):
            return w
    return None


def next_window(dt: dict, now: datetime) -> Window | None:
    """Fenêtre en cours, sinon la prochaine à venir (dans les 400 jours), sinon None."""
    if not dt.get("enabled", True):
        return None
    current = active_window(dt, now)
    if current:
        return current
    lo, hi = _bounds(dt)
    if hi is not None and hi <= now:
        return None
    rec = dt.get("recurrence")
    if not rec:
        return Window(lo, hi) if lo > now else None
    step = 31 if rec["type"] == "monthly" else 8
    frm = now
    horizon = now + timedelta(days=SEARCH_DAYS)
    while frm < horizon:
        to = frm + timedelta(days=step)
        for w in occurrences(dt, frm, to):
            if w.start > now:
                return w
        frm = to
    return None


def last_ended(dt: dict, now: datetime, lookback: timedelta) -> Window | None:
    """Dernière fenêtre terminée dans [now − lookback, now], ou None."""
    ended = [w for w in occurrences(dt, now - lookback, now)
             if w.end is not None and now - lookback <= w.end <= now]
    return ended[-1] if ended else None


# ── portée ──────────────────────────────────────────────────────────────────────

@dataclass
class Active:
    """Maintenance active pendant une évaluation."""
    id: int
    name: str
    scope: dict
    end: datetime | None
    host_tags_needed: bool = field(init=False)

    def __post_init__(self):
        self.host_tags_needed = any(k != "host" for k in (self.scope.get("tags") or {}))


def covers(scope: dict, monitor_id: int, group: str, host_tags: dict[str, dict] | None = None) -> bool:
    """La maintenance couvre-t-elle ce groupe de ce moniteur ?

    `monitor_ids` vide = tous les moniteurs. `tags` : le groupe, ou l'hôte du groupe, porte toutes
    ces étiquettes (`host` se compare à `host:x` du groupe). `groups` : clés exactes. Sans `tags`
    ni `groups`, tous les groupes des moniteurs visés.
    """
    ids = scope.get("monitor_ids") or []
    if ids and monitor_id not in ids:
        return False
    groups = scope.get("groups") or []
    tags = scope.get("tags") or {}
    if groups and group in groups:
        return True
    if tags:
        have = parse_group_key(group)
        host = have.get("host")
        if host and host_tags and host in host_tags:
            have = {**(host_tags.get(host) or {}), **have}
        return all(have.get(k) == v for k, v in tags.items())
    return not groups


def covers_host(scope: dict, hostname: str, host_tags: dict | None = None) -> bool:
    """Pour une page de statut : la maintenance vise-t-elle cet hôte (quel que soit le moniteur) ?"""
    if scope.get("monitor_ids"):
        return False
    groups = scope.get("groups") or []
    if any(parse_group_key(g).get("host") == hostname for g in groups):
        return True
    tags = scope.get("tags") or {}
    if tags:
        have = {**(host_tags or {}), "host": hostname}
        return all(have.get(k) == v for k, v in tags.items())
    return not groups


def suppressed_by(active: list[Active], monitor_id: int, group: str,
                  host_tags: dict[str, dict] | None = None) -> Active | None:
    for a in active:
        if covers(a.scope, monitor_id, group, host_tags):
            return a
    return None
