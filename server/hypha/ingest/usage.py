"""Consommation par organisation (contrat § 30) : compteurs en mémoire, écrits une fois par minute.

Chaque processus d'API compte ce qu'il écrit (lignes et octets de journaux, points de métriques,
spans et leurs octets, événements RUM, contrôles), ce qu'une limite de formule a refusé (§ 52) et ce
qui se facture à l'acte (explications par l'IA, exécutions synthétiques, SMS), par organisation et
par jour UTC, puis ajoute ses comptes à `usage_daily` en une requête. Un processus arrêté avant
l'écriture perd au plus une minute.
"""
import time
from datetime import datetime, timezone

FIELDS = ("log_lines", "log_bytes", "metric_points", "spans", "rum_events", "checks", "span_bytes",
          "synthetic_runs", "synthetic_billable_runs", "explanations", "sms", "refused_log_lines",
          "refused_spans")
FLUSH_EVERY_S = 60.0

UPSERT = (
    f"INSERT INTO usage_daily (org_id, day, {', '.join(FIELDS)}) "
    f"SELECT * FROM unnest(%s::bigint[], %s::date[], {', '.join(['%s::bigint[]'] * len(FIELDS))}) ORDER BY 1, 2 "
    "ON CONFLICT (org_id, day) DO UPDATE SET "
    + ", ".join(f"{f} = usage_daily.{f} + EXCLUDED.{f}" for f in FIELDS)
)


class Usage:
    def __init__(self):
        self.pending: dict[tuple, list[int]] = {}
        self.flushed_at = time.monotonic()

    def add(self, org_id: int, **counts: int) -> None:
        if not any(counts.values()):
            return
        day = datetime.now(timezone.utc).date()
        entry = self.pending.get((org_id, day))
        if entry is None:
            entry = self.pending[(org_id, day)] = [0] * len(FIELDS)
        for i, name in enumerate(FIELDS):
            n = counts.get(name)
            if n:
                entry[i] += int(n)

    def pending_count(self, org_id: int, day, name: str) -> int:
        """Compte pas encore écrit en base pour (organisation, jour, champ)."""
        entry = self.pending.get((org_id, day))
        return entry[FIELDS.index(name)] if entry else 0

    def due(self, now: float | None = None) -> bool:
        now = time.monotonic() if now is None else now
        return bool(self.pending) and now - self.flushed_at >= FLUSH_EVERY_S

    def take(self) -> dict:
        pending, self.pending = self.pending, {}
        self.flushed_at = time.monotonic()
        return pending

    def restore(self, pending: dict) -> None:
        for k, values in pending.items():
            entry = self.pending.setdefault(k, [0] * len(FIELDS))
            for i, v in enumerate(values):
                entry[i] += v

    @staticmethod
    def params(pending: dict) -> tuple:
        items = sorted(pending.items())
        cols = [[k[0] for k, _ in items], [k[1] for k, _ in items]]
        for i in range(len(FIELDS)):
            cols.append([v[i] for _, v in items])
        return tuple(cols)
