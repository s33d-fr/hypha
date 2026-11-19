"""Consommation facturable d'un espace (contrat § 52) : quantités de la période et estimation.

- Hôtes : comptés à l'heure (`usage_hourly`, hôtes vus dans chaque heure UTC), proratisés sur la
  période : un hôte présent tout le mois vaut 1 hôte-mois (2 € HT), 360 h sur 720 en valent 0,5.
  En annuel, seules les heures au-delà du nombre d'hôtes engagé se facturent (au tarif mensuel).
- Équipements réseau et ressources cloud : de même, 0,50 € par équipement-mois.
- Journaux et traces : octets reçus par jour (`usage_daily`) ; inclus 10 Go de journaux et 5 Go de
  traces par hôte-mois (base : hôtes réels, au moins les hôtes engagés en annuel), au-delà 0,10 €/Go.
- Conservation longue : Go reçus les jours où l'option 30 ou 90 jours était active.
- Synthétiques : exécutions des tests publics au-delà des 50 premiers (par ordre de création).
- SMS : à l'unité.
Les périodes sont des mois civils UTC (abonnements calés sur le 1er à 00:00 UTC) ; le premier mois
commence à la souscription (journée de souscription exclue pour les octets, à l'avantage du client).
"""
from dataclasses import dataclass
from datetime import date, datetime, timedelta, timezone
from decimal import ROUND_HALF_UP, Decimal

from ..db import fetch_all, fetch_one
from ..plans import ASSOCIATION_DISCOUNT, ENTREPRISE, GB, LOGS_GB_PER_HOST, PRICES, TRACES_GB_PER_HOST, VAT_RATE

UTC = timezone.utc
CENT = Decimal("0.01")
Q3 = Decimal("0.001")


@dataclass(frozen=True)
class Period:
    start: datetime
    end: datetime                 # exclue

    @property
    def hours(self) -> float:
        return max(1.0, (self.end - self.start).total_seconds() / 3600)


def month_of(now: datetime) -> Period:
    start = now.astimezone(UTC).replace(day=1, hour=0, minute=0, second=0, microsecond=0)
    end = (start + timedelta(days=32)).replace(day=1)
    return Period(start, end)


def next_month_start(now: datetime) -> datetime:
    return month_of(now).end


def _first_full_day(dt: datetime) -> date:
    d = dt.astimezone(UTC)
    return d.date() if (d.hour, d.minute, d.second, d.microsecond) == (0, 0, 0, 0) else d.date() + timedelta(days=1)


async def refresh_hourly(conn, now: datetime | None = None) -> int:
    """Hôtes et équipements vus dans l'heure en cours (et la précédente juste après le changement
    d'heure). GREATEST : le compte d'une heure ne fait que monter au fil des passages."""
    now = (now or datetime.now(UTC)).astimezone(UTC)
    hour = now.replace(minute=0, second=0, microsecond=0)
    hours = [hour] + ([hour - timedelta(hours=1)] if now.minute < 5 else [])
    n = 0
    for h in hours:
        cur = await conn.execute("""
            INSERT INTO usage_hourly (org_id, hour, hosts, devices)
            SELECT org_id, %s, count(*) FILTER (WHERE kind = 'agent'), count(*) FILTER (WHERE kind <> 'agent')
              FROM hosts WHERE last_seen >= %s AND first_seen < %s
             GROUP BY org_id
            ON CONFLICT (org_id, hour) DO UPDATE
               SET hosts = GREATEST(usage_hourly.hosts, EXCLUDED.hosts),
                   devices = GREATEST(usage_hourly.devices, EXCLUDED.devices)
        """, (h, h, h + timedelta(hours=1)))
        n += cur.rowcount or 0
    return n


async def quantities(conn, org_id: int, period: Period, upto: datetime, committed_hosts: int = 0) -> dict:
    """Quantités facturables de `period`, arrêtées à `upto` (fin de période pour la facture)."""
    stop = min(upto, period.end)
    hours = await fetch_all(conn, """
        SELECT hosts, devices FROM usage_hourly WHERE org_id = %s AND hour >= %s AND hour < %s
    """, (org_id, period.start.replace(minute=0, second=0, microsecond=0), stop))
    host_hours = sum(r["hosts"] for r in hours)
    over_hours = sum(max(0, r["hosts"] - committed_hosts) for r in hours)
    device_hours = sum(r["devices"] for r in hours)
    elapsed_h = max(0.0, (stop - period.start).total_seconds() / 3600)
    day_from, day_to = _first_full_day(period.start), stop.astimezone(UTC).date()
    if stop.astimezone(UTC) > datetime.combine(day_to, datetime.min.time(), UTC):
        day_to += timedelta(days=1)           # jour en cours compris
    d = await fetch_one(conn, """
        SELECT coalesce(sum(log_bytes), 0)::bigint AS log_bytes, coalesce(sum(span_bytes), 0)::bigint AS span_bytes,
               coalesce(sum(log_lines), 0)::bigint AS log_lines, coalesce(sum(spans), 0)::bigint AS spans,
               coalesce(sum(synthetic_runs), 0)::bigint AS synthetic_runs,
               coalesce(sum(synthetic_billable_runs), 0)::bigint AS synthetic_billable_runs,
               coalesce(sum(sms), 0)::bigint AS sms, coalesce(sum(explanations), 0)::bigint AS explanations,
               coalesce(sum(log_bytes + span_bytes) FILTER (WHERE retention_days = 30), 0)::bigint AS bytes_30,
               coalesce(sum(log_bytes + span_bytes) FILTER (WHERE retention_days = 90), 0)::bigint AS bytes_90,
               coalesce(sum(refused_log_lines), 0)::bigint AS refused_log_lines,
               coalesce(sum(refused_spans), 0)::bigint AS refused_spans
          FROM usage_daily WHERE org_id = %s AND day >= %s AND day < %s
    """, (org_id, day_from, day_to)) or {}
    H = period.hours
    basis = max(host_hours, committed_hosts * elapsed_h) / H          # hôtes-mois pour les volumes inclus
    logs_gb = (d.get("log_bytes") or 0) / GB
    traces_gb = (d.get("span_bytes") or 0) / GB
    over_gb = max(0.0, logs_gb - LOGS_GB_PER_HOST * basis) + max(0.0, traces_gb - TRACES_GB_PER_HOST * basis)
    return {
        "host": round((over_hours if committed_hosts else host_hours) / H, 3),
        "device": round(device_hours / H, 3),
        "gb_over": round(over_gb, 3),
        "retention_30": round((d.get("bytes_30") or 0) / GB, 3),
        "retention_90": round((d.get("bytes_90") or 0) / GB, 3),
        "synthetic": int(d.get("synthetic_billable_runs") or 0),
        "sms": int(d.get("sms") or 0),
        "detail": {
            "period_start": period.start, "period_end": period.end, "upto": stop,
            "host_hours": host_hours, "host_months": round(host_hours / H, 3), "device_hours": device_hours,
            "committed_hosts": committed_hosts, "included_basis_host_months": round(basis, 3),
            "log_bytes": int(d.get("log_bytes") or 0), "span_bytes": int(d.get("span_bytes") or 0),
            "logs_included_gb": round(LOGS_GB_PER_HOST * basis, 3),
            "traces_included_gb": round(TRACES_GB_PER_HOST * basis, 3),
            "log_lines": int(d.get("log_lines") or 0), "spans": int(d.get("spans") or 0),
            "synthetic_runs": int(d.get("synthetic_runs") or 0), "explanations": int(d.get("explanations") or 0),
            "refused_log_lines": int(d.get("refused_log_lines") or 0),
            "refused_spans": int(d.get("refused_spans") or 0),
        },
    }


UNIT_PRICE = {"host": PRICES["host_month"], "device": PRICES["device_month"], "gb_over": PRICES["gb_over"],
              "retention_30": PRICES["retention_30"], "retention_90": PRICES["retention_90"],
              "synthetic": PRICES["synthetic_run"], "sms": PRICES["sms"]}


def estimate(q: dict, plan: str, discount: str | None, tax_mode: str, committed_hosts: int = 0,
             interval: str = "month") -> dict:
    """Montant HT, TVA et TTC de la période en cours, poste par poste (estimation : Stripe facture)."""
    lines = []
    for key in ("host", "device", "gb_over", "retention_30", "retention_90", "synthetic", "sms"):
        qty = Decimal(str(q[key]))
        unit = UNIT_PRICE[key]
        if key == "host" and discount == "association":
            unit = unit * (1 - ASSOCIATION_DISCOUNT)
        amount = (qty * unit).quantize(CENT, ROUND_HALF_UP)
        lines.append({"key": key, "quantity": float(qty), "unit_price": float(unit), "amount": float(amount)})
    if plan == ENTREPRISE:
        lines.append({"key": "entreprise", "quantity": 1, "unit_price": float(PRICES["entreprise_month"]),
                      "amount": float(PRICES["entreprise_month"])})
    total = sum(Decimal(str(x["amount"])) for x in lines).quantize(CENT)
    vat = (total * VAT_RATE).quantize(CENT, ROUND_HALF_UP) if tax_mode == "fr20" else Decimal("0.00")
    out = {"lines": lines, "total_ht": float(total), "vat": float(vat), "total_ttc": float(total + vat),
           "tax_mode": tax_mode, "currency": "eur"}
    if interval == "year" and committed_hosts:
        unit = PRICES["host_year"] * ((1 - ASSOCIATION_DISCOUNT) if discount == "association" else 1)
        out["annual_commitment"] = {"hosts": committed_hosts, "unit_price": float(unit),
                                    "amount": float((unit * committed_hosts).quantize(CENT))}
    return out
