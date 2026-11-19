"""Worker HYPHA : un seul actif sur le parc, par verrou consultatif PostgreSQL.

Le verrou est tenu par une connexion dédiée ; si elle tombe, le verrou part avec elle et toutes
les tâches s'arrêtent aussitôt, pour qu'un autre nœud reprenne sans double exécution. Le worker
n'exécute aucun DDL : partitions et agrégats passent par les fonctions SECURITY DEFINER du schéma.
"""
import asyncio
import logging
import signal
import time
from contextlib import nullcontext

import psycopg
from psycopg_pool import PoolTimeout

from . import __version__, plans, telemetry
from .actions import service as actions_service
from .api import audit_routes
from .billing import long_retention, metering
from .billing import service as billing
from .config import Settings, get_settings
from .db import Database, fetch_all, fetch_one
from .ingest import error_tracking
from .ingest.usage import UPSERT as USAGE_UPSERT, Usage
from .logsetup import setup_logging
from .monitors.notify import Notifier
from .monitors.runner import evaluate_all
from .reports import service as reports_service
from .synthetics import runner as synth

log = logging.getLogger("hypha.worker")

LOCK_ID = 7268001
RETRY_S = 30
DB_ERRORS = (psycopg.OperationalError, PoolTimeout)


class LockLost(Exception):
    pass


# Tests lancés depuis S33D (§ 52) : en SaaS, la formule borne l'intervalle (Découverte 5 min, Pro et
# Entreprise 1 min) et le nombre (Découverte : les 10 premiers) ; au-delà des 50 premiers tests d'un
# espace payant, chaque exécution se facture. L'auto-hébergé garde l'intervalle de chaque test.
DUE_SQL = """
    SELECT x.id, x.org_id, x.kind, x.target, x.config, x.steps, x.org_settings, x.plan, x.rang
      FROM (SELECT t.id, t.org_id, t.kind, t.target, t.config, t.steps, t.interval_s, t.last_run,
                   o.settings AS org_settings, o.plan,
                   row_number() OVER (PARTITION BY t.org_id ORDER BY t.id) AS rang
              FROM synthetic_tests t JOIN orgs o ON o.id = t.org_id
             WHERE t.enabled AND 'server' = ANY(t.locations)) x
     WHERE (NOT %s OR x.plan <> 'decouverte' OR x.rang <= %s)
       AND (x.last_run IS NULL OR x.last_run <= now() - make_interval(secs => CASE
              WHEN NOT %s THEN x.interval_s
              WHEN x.plan = 'decouverte' THEN greatest(x.interval_s, %s)
              WHEN x.plan IN ('pro', 'entreprise') THEN greatest(x.interval_s, %s)
              ELSE x.interval_s END))
     ORDER BY x.last_run NULLS FIRST LIMIT 200
"""


class Synthetics:
    """Lance les tests échus, chacun dans sa tâche, au plus 10 à la fois."""

    def __init__(self, db: Database, settings: Settings, concurrency: int = 10, usage: Usage | None = None):
        self.db = db
        self.settings = settings
        self.sem = asyncio.Semaphore(concurrency)
        self.running: set[int] = set()
        self.tasks: set[asyncio.Task] = set()
        self.usage = usage

    async def tick(self) -> None:
        free, paid = plans.LIMITS[plans.DECOUVERTE], plans.LIMITS[plans.PRO]
        saas = self.settings.saas
        async with self.db.writer() as conn:
            rows = await fetch_all(conn, DUE_SQL, (saas, free.public_tests, saas, free.public_interval_min_s,
                                                   paid.public_interval_min_s))
            due = [r for r in rows if r["id"] not in self.running]
            if due:
                await conn.execute("UPDATE synthetic_tests SET last_run = now() WHERE id = ANY(%s::bigint[])",
                                   ([r["id"] for r in due],))
        if saas and self.usage is not None:
            for r in due:
                billable = r["plan"] in plans.PAID and r["rang"] > (paid.public_tests_included or 0)
                self.usage.add(r["org_id"], synthetic_runs=1, synthetic_billable_runs=1 if billable else 0)
        for test in due:
            self.running.add(test["id"])
            task = asyncio.create_task(self._run(test))
            self.tasks.add(task)
            task.add_done_callback(self.tasks.discard)

    async def _run(self, test: dict) -> None:
        # Une trace par test exécuté (le relevé des tests échus, toutes les 5 s, n'en ouvre pas).
        with telemetry.task_span("worker.synthetic", **{"hypha.org_id": test["org_id"], "hypha.test_id": test["id"],
                                                        "hypha.test_kind": test["kind"]}):
            try:
                dnsbl = None
                if synth.uses_dnsbl(test):
                    # Base injoignable : le test ne part pas, plutôt que de relire les listes noires.
                    async with self.db.reader() as conn:
                        dnsbl = await synth.dnsbl_cache(conn, test["org_id"])
                async with self.sem:
                    result = await synth.execute(self.settings, test, test["org_settings"], dnsbl)
                telemetry.set_attributes({"hypha.test_ok": result.ok})
                async with self.db.writer() as conn:
                    await synth.record(conn, test["org_id"], test["id"], self.settings.synthetics_location, result)
            except DB_ERRORS as e:
                telemetry.mark_error(e)
                log.warning("résultat du test %s non enregistré : base indisponible (%s)", test["id"], e)
            except Exception as e:
                telemetry.mark_error(e)
                log.exception("test synthétique %s en erreur", test["id"])
            finally:
                self.running.discard(test["id"])

    async def close(self) -> None:
        for t in list(self.tasks):
            t.cancel()
        await asyncio.gather(*self.tasks, return_exceptions=True)


async def _safe(name: str, fn, span: str | None = None) -> None:
    """Exécute une tâche sans jamais lever ; `span` ouvre une trace « hypha.<span> » (si traces actives)."""
    t0 = time.monotonic()
    with telemetry.task_span(span) if span else nullcontext():
        try:
            result = await fn()
            if result is not None:
                log.debug("%s : %s (%.1f s)", name, result, time.monotonic() - t0)
        except DB_ERRORS as e:
            telemetry.mark_error(e)
            log.warning("%s : base indisponible (%s)", name, str(e).splitlines()[0] if str(e) else type(e).__name__)
        except asyncio.CancelledError:
            raise
        except Exception as e:
            telemetry.mark_error(e)
            log.exception("%s : erreur", name)


async def every(period: float, name: str, fn, first_delay: float = 0.0, span: str | None = None) -> None:
    await asyncio.sleep(first_delay)
    while True:
        t0 = time.monotonic()
        await _safe(name, fn, span)
        await asyncio.sleep(max(1.0, period - (time.monotonic() - t0)))


async def aligned(period: int, offset: int, name: str, fn, span: str | None = None) -> None:
    """Au démarrage, puis à chaque période calée sur l'horloge (ex. chaque heure à HH:08)."""
    await _safe(name, fn, span)
    while True:
        now = time.time()
        nxt = (now - offset) // period * period + period + offset
        await asyncio.sleep(max(1.0, nxt - now))
        await _safe(name, fn, span)


async def call(db: Database, sql: str):
    async with db.writer() as conn:
        row = await fetch_one(conn, sql)
    return next(iter(row.values())) if row else None


async def keep_lock(conn: psycopg.AsyncConnection) -> None:
    while True:
        await asyncio.sleep(15)
        try:
            await asyncio.wait_for(conn.execute("SELECT 1"), 10)
        except Exception as e:
            raise LockLost(f"connexion du verrou perdue : {e}")


async def acquire_lock(settings: Settings) -> psycopg.AsyncConnection:
    announced = False
    while True:
        try:
            conn = await psycopg.AsyncConnection.connect(settings.db_write, autocommit=True,
                                                          application_name="hypha-worker-lock")
            cur = await conn.execute("SELECT pg_try_advisory_lock(%s)", (LOCK_ID,))
            if (await cur.fetchone())[0]:
                return conn
            await conn.close()
            if not announced:
                log.info("un autre worker est actif ; nouvel essai toutes les %s s", RETRY_S)
                announced = True
        except psycopg.OperationalError as e:
            log.warning("base indisponible, worker en attente : %s", str(e).splitlines()[0] if str(e) else e)
        await asyncio.sleep(RETRY_S)


async def run_active(settings: Settings, lock_conn: psycopg.AsyncConnection) -> None:
    db = Database(settings, "hypha-worker", statement_timeout="300s")
    await db.open()
    notifier = Notifier(settings)
    usage = Usage()
    synthetics = Synthetics(db, settings, usage=usage)
    tasks = [
        asyncio.create_task(keep_lock(lock_conn), name="verrou"),
        asyncio.create_task(notifier.run(), name="notifications"),
        asyncio.create_task(aligned(3600, 120, "partitions", lambda: call(db, "SELECT hypha.maintain_partitions()"),
                                    "worker.partitions"),
                            name="partitions"),
        asyncio.create_task(every(60, "moniteurs", lambda: evaluate_all(db, notifier, settings), 10,
                                  "worker.monitors"), name="moniteurs"),
        asyncio.create_task(every(5, "synthétiques", synthetics.tick, 5), name="synthétiques"),
        asyncio.create_task(every(300, "rollup_5m", lambda: call(db, "SELECT hypha.rollup_5m()"), 30,
                                  "worker.rollup_5m"), name="rollup_5m"),
        asyncio.create_task(aligned(3600, 480, "rollup_1h", lambda: call(db, "SELECT hypha.rollup_1h()"),
                                    "worker.rollup_1h"),
                            name="rollup_1h"),
        asyncio.create_task(aligned(3600, 900, "sessions", lambda: purge_sessions(db), "worker.sessions"),
                            name="sessions"),
        # Exploitation (contrat §§ 22, 24, 28) : rapports échus, purge de l'audit, actions expirées.
        asyncio.create_task(every(300, "rapports", lambda: reports_service.tick(db, settings), 60,
                                  "worker.reports"), name="rapports"),
        asyncio.create_task(aligned(3600, 1500, "audit", lambda: audit_routes.purge(db), "worker.audit"),
                            name="audit"),
        asyncio.create_task(every(60, "actions", lambda: actions_service.expire(db), 20, "worker.actions"),
                            name="actions"),
        # Suivi des erreurs (contrat § 25) : comptes horaires gardés 92 jours.
        asyncio.create_task(aligned(86400, 3 * 3600 + 600, "erreurs", lambda: error_tracking.purge_hits(db),
                                    "worker.error_hits"), name="erreurs"),
    ]
    if settings.saas:
        # Formules et facturation (§ 52) : exécutions synthétiques comptées, hôtes vus dans l'heure,
        # cumul de la période envoyé à Stripe, conservation longue copiée la nuit, purges.
        tasks += [
            asyncio.create_task(every(60, "consommation", lambda: flush_usage(db, usage), 60), name="consommation"),
            asyncio.create_task(aligned(300, 30, "hôtes à l'heure", lambda: hourly_hosts(db), "worker.usage_hourly"),
                                name="hôtes à l'heure"),
            asyncio.create_task(aligned(3600, 600, "facturation", lambda: billing.sync_all(db, settings),
                                        "worker.billing"), name="facturation"),
            asyncio.create_task(aligned(3600, 1200, "conservation longue", lambda: long_retention.run(db),
                                        "worker.long_retention"), name="conservation longue"),
            asyncio.create_task(aligned(86400, 4 * 3600 + 900, "purges facturation", lambda: purge_billing(db),
                                        "worker.billing_purge"), name="purges facturation"),
        ]
    try:
        done, _ = await asyncio.wait(tasks, return_when=asyncio.FIRST_COMPLETED)
        for t in done:
            if not t.cancelled() and t.exception():
                log.warning("arrêt des tâches : %s", t.exception())
    finally:
        for t in tasks:
            t.cancel()
        await asyncio.gather(*tasks, return_exceptions=True)
        await synthetics.close()
        await db.close()


async def flush_usage(db: Database, usage: Usage):
    if not usage.pending:
        return None
    pending = usage.take()
    try:
        async with db.writer() as conn:
            await conn.execute(USAGE_UPSERT, Usage.params(pending))
    except Exception:
        usage.restore(pending)
        raise
    return f"{len(pending)} ligne(s)"


async def hourly_hosts(db: Database):
    async with db.writer() as conn:
        return await metering.refresh_hourly(conn)


async def purge_billing(db: Database):
    """Petites tables seulement : comptes horaires gardés 400 jours, événements Stripe 180 jours,
    hôtes refusés 30 jours."""
    async with db.writer() as conn:
        a = await conn.execute("DELETE FROM usage_hourly WHERE hour < now() - interval '400 days'")
        b = await conn.execute("DELETE FROM stripe_events WHERE received_at < now() - interval '180 days'")
        c = await conn.execute("DELETE FROM org_refused_hosts WHERE last_at < now() - interval '30 days'")
    return f"{a.rowcount} + {b.rowcount} + {c.rowcount} ligne(s)"


async def purge_sessions(db: Database):
    async with db.writer() as conn:
        cur = await conn.execute("DELETE FROM sessions WHERE expires_at < now()")
        return cur.rowcount


async def main() -> None:
    settings = get_settings()
    setup_logging(settings.log_level)
    telemetry.setup(settings, "hypha-worker")     # avant toute création de pool (curseur tracé)
    if not settings.db_write:
        raise SystemExit("HYPHA_DB_WRITE absent")
    log.info("hypha-worker %s démarré", __version__)
    while True:
        lock_conn = await acquire_lock(settings)
        log.info("verrou %s obtenu : ce worker est l'actif", LOCK_ID)
        try:
            await run_active(settings, lock_conn)
        finally:
            try:
                await lock_conn.close()
            except Exception:
                pass
        log.warning("worker redevenu passif ; reprise de l'attente du verrou")
        await asyncio.sleep(5)


def run() -> None:
    async def runner():
        task = asyncio.current_task()
        loop = asyncio.get_running_loop()
        for sig in (signal.SIGTERM, signal.SIGINT):
            try:
                loop.add_signal_handler(sig, task.cancel)
            except (NotImplementedError, RuntimeError):
                pass                      # Windows : Ctrl+C lève KeyboardInterrupt, suffisant
        try:
            await main()
        except asyncio.CancelledError:
            log.info("hypha-worker arrêté")

    try:
        asyncio.run(runner())
    except KeyboardInterrupt:
        pass
    finally:
        telemetry.shutdown()
