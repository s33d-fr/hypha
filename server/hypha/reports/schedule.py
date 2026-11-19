"""Échéances d'un rapport programmé (contrat § 22), sans accès à la base.

`schedule` = {"every":"day|week|month","weekday":1..7 (lundi = 1),"day":1..31,"hour":0..23,
"minute":0..59,"tz":"Europe/Paris"} ; un jour du mois au-delà de la fin du mois tombe le dernier jour.
Un rapport part quand l'échéance la plus récente est passée, postérieure à sa création, et que
`last_sent_at` lui est antérieur.
"""
import calendar
from datetime import date, datetime, time, timedelta, timezone

from ..monitors.downtime import zone

EVERY = ("day", "week", "month")
REPORT_KINDS = ("summary", "dashboard", "slo")


def _int(s: dict, key: str, lo: int, hi: int, default: int) -> int:
    v = s.get(key, default)
    if isinstance(v, bool) or not isinstance(v, int) or not lo <= v <= hi:
        raise ValueError(f"schedule.{key} doit être un entier entre {lo} et {hi}")
    return v


def normalize_schedule(s) -> dict:
    if not isinstance(s, dict):
        raise ValueError("schedule doit être un objet {every, weekday, day, hour, tz}")
    every = s.get("every", "week")
    if every not in EVERY:
        raise ValueError(f"schedule.every attendu : {'|'.join(EVERY)}")
    out = {"every": every, "hour": _int(s, "hour", 0, 23, 8), "minute": _int(s, "minute", 0, 59, 0),
           "tz": s.get("tz") or "Europe/Paris"}
    zone(out["tz"])
    if every == "week":
        out["weekday"] = _int(s, "weekday", 1, 7, 1)
    if every == "month":
        out["day"] = _int(s, "day", 1, 31, 1)
    return out


def _matches(s: dict, d: date) -> bool:
    if s["every"] == "day":
        return True
    if s["every"] == "week":
        return d.isoweekday() == s.get("weekday", 1)
    return d.day == min(s.get("day", 1), calendar.monthrange(d.year, d.month)[1])


def _at(s: dict, d: date, tz) -> datetime:
    return datetime.combine(d, time(s.get("hour", 8), s.get("minute", 0)), tzinfo=tz).astimezone(timezone.utc)


def last_due(s: dict, now: datetime) -> datetime:
    """Échéance la plus récente, à `now` ou avant."""
    tz = zone(s.get("tz"))
    d = now.astimezone(tz).date()
    for _ in range(40):
        if _matches(s, d) and _at(s, d, tz) <= now:
            return _at(s, d, tz)
        d -= timedelta(days=1)
    raise ValueError("échéance introuvable")


def next_due(s: dict, now: datetime) -> datetime:
    tz = zone(s.get("tz"))
    d = now.astimezone(tz).date()
    for _ in range(40):
        if _matches(s, d) and _at(s, d, tz) > now:
            return _at(s, d, tz)
        d += timedelta(days=1)
    raise ValueError("échéance introuvable")


def is_due(s: dict, now: datetime, last_sent: datetime | None, created_at: datetime) -> bool:
    due = last_due(s, now)
    return due >= created_at and (last_sent is None or last_sent < due)
