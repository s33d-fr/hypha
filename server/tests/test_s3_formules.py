"""Formules et facturation (contrat § 52), sans base ni réseau : limites, conservation, consommation,
montants, client Stripe (encodage, signature du webhook) et filtre des objets HYPHA du compte partagé."""
import asyncio
import dataclasses
import hashlib
import hmac
import json
import time
from datetime import datetime, timedelta, timezone

import pytest

import test_routes_nodb as T
from hypha import plans, quotas, retention
from hypha.billing import catalog, metering
from hypha.billing import service as B
from hypha.billing.stripe_api import SignatureError, encode, verify_webhook
from hypha.config import get_settings
from hypha.runtime import Runtime, set_runtime

UTC = timezone.utc
SECRET = "whsec_essai_hypha"


@pytest.fixture(autouse=True)
def _caches():
    plans._cache.clear()
    quotas._hosts.clear()
    quotas._bytes_db.clear()
    quotas._month_db.clear()
    yield
    plans._cache.clear()
    quotas._hosts.clear()


def responder(plan="decouverte", n_hosts=0, bytes_today=0, billing_row=None, retention_days=15):
    base = T.respond

    def respond(sql, params):
        s = " ".join(sql.split())
        if "FROM orgs o LEFT JOIN org_billing b" in s:
            return [{"plan": plan, "retention_days": retention_days, "status": "active"}]
        if s.startswith("WITH me AS (SELECT id FROM hosts"):
            return [{"n": n_hosts}]
        if "SELECT log_bytes + span_bytes AS b FROM usage_daily" in s:
            return [{"b": bytes_today}]
        if "FROM org_billing WHERE stripe_customer_id" in s:
            return [billing_row] if billing_row else []
        if s.startswith("INSERT INTO stripe_events"):
            return [{"id": params[0]}]
        if s.startswith("SELECT count(*) AS n FROM synthetic_tests"):
            return [{"n": 3}]
        return base(sql, params)
    return respond


@pytest.fixture()
def client(monkeypatch):
    get_settings.cache_clear()
    settings = dataclasses.replace(get_settings(), stripe_secret_key="sk_test_factice",
                                   stripe_webhook_secret=SECRET)
    db = T.FakeDatabase()
    set_runtime(Runtime(settings=settings, db=db))
    c = T.TestClient(T.create_app(), raise_server_exceptions=True)
    c.db = db
    yield c
    set_runtime(None)


# ── outils ─────────────────────────────────────────────────────────────────────

def test_encode_nested_params():
    out = encode({"line_items": [{"price": "price_1", "quantity": 2}], "subscription_data": {
        "metadata": {"offre": "hypha"}, "default_tax_rates": None}, "flag": True, "empty": []})
    assert ("line_items[0][price]", "price_1") in out
    assert ("line_items[0][quantity]", "2") in out
    assert ("subscription_data[metadata][offre]", "hypha") in out
    assert ("flag", "true") in out and ("empty", "") in out
    assert not any(k.startswith("subscription_data[default_tax_rates]") for k, _ in out)


def _signed(body: bytes, secret=SECRET, t=None):
    t = int(time.time()) if t is None else t
    sig = hmac.new(secret.encode(), f"{t}.".encode() + body, hashlib.sha256).hexdigest()
    return f"t={t},v1={sig}"


def test_verify_webhook():
    body = json.dumps({"id": "evt_1", "type": "x"}).encode()
    assert verify_webhook(body, _signed(body), SECRET)["id"] == "evt_1"
    with pytest.raises(SignatureError):
        verify_webhook(body, _signed(body, "whsec_autre"), SECRET)
    with pytest.raises(SignatureError):
        verify_webhook(body, _signed(body, t=int(time.time()) - 3600), SECRET)
    with pytest.raises(SignatureError):
        verify_webhook(body, None, SECRET)


def test_decimal_text():
    assert B.decimal_text(12.345) == "12.345"
    assert B.decimal_text(1234567.891) == "1234567.891"
    assert B.decimal_text(0.0) == "0" and B.decimal_text(7) == "7"


# ── formules, conservation ─────────────────────────────────────────────────────

def test_plan_from_row():
    d = plans.from_row(5, {"plan": "decouverte", "retention_days": 90})
    assert d.limits.hosts == 5 and d.retention_days == 3 and not d.paid
    p = plans.from_row(5, {"plan": "pro", "retention_days": 90})
    assert p.paid and p.retention_days == 90 and p.limits.hosts is None
    assert plans.from_row(1, {"plan": "interne"}).limits == plans.UNLIMITED
    assert plans.from_row(1, None).code == plans.INTERNE


def test_retention_bounds():
    now = 1_800_000_000_000
    day = retention.DAY_MS
    d = plans.from_row(5, {"plan": "decouverte"})
    s, e, rel = retention.bounds(d, "logs", now - 10 * day, now, now)
    assert s == now - 3 * day and rel == "logs"
    s, e, rel = retention.bounds(d, "logs", now - 20 * day, now - 10 * day, now)
    assert s == e - 1                                   # entièrement hors conservation : lecture vide
    p90 = plans.from_row(5, {"plan": "pro", "retention_days": 90})
    assert retention.bounds(p90, "spans", now - 40 * day, now, now)[2] == "spans_etendus"
    assert retention.bounds(p90, "spans", now - 2 * day, now, now)[2] == "spans"
    i = plans.from_row(1, {"plan": "interne"})
    assert retention.bounds(i, "logs", now - 40 * day, now, now) == (now - 40 * day, now, "logs")
    s, e, raw = retention.metrics_bounds(d, now - 90 * day, now, now)
    assert s == now - 30 * day and raw == 86400
    assert retention.metrics_bounds(i, now - 90 * day, now, now)[2] is None


# ── consommation et montants ───────────────────────────────────────────────────

class _Conn:
    def __init__(self, hours, daily):
        self.hours, self.daily = hours, daily

    async def execute(self, sql, params=None):
        rows = self.hours if "FROM usage_hourly" in sql else [self.daily]

        class Cur:
            async def fetchall(self_inner):
                return rows

            async def fetchone(self_inner):
                return rows[0] if rows else None
        return Cur()


def test_quantities_and_estimate():
    period = metering.month_of(datetime(2026, 11, 19, 12, tzinfo=UTC))
    assert period.start == datetime(2026, 11, 1, tzinfo=UTC) and period.hours == 720
    hours = [{"hosts": 20, "devices": 4}] * 720                     # 20 hôtes, tout le mois
    daily = {"log_bytes": 250 * plans.GB, "span_bytes": 50 * plans.GB, "bytes_30": 0, "bytes_90": 0,
             "synthetic_billable_runs": 20000, "sms": 3}
    q = asyncio.run(metering.quantities(_Conn(hours, daily), 9, period, period.end))
    assert q["host"] == 20 and q["device"] == 4
    assert q["gb_over"] == 50.0                                     # 250 − 200 Go de journaux, traces sous 100 Go
    est = metering.estimate(q, plans.PRO, None, "fr20")
    amounts = {x["key"]: x["amount"] for x in est["lines"]}
    assert amounts["host"] == 40.0 and amounts["device"] == 2.0 and amounts["gb_over"] == 5.0
    assert amounts["synthetic"] == 2.0 and amounts["sms"] == 0.3
    assert est["total_ht"] == 49.3 and est["vat"] == 9.86 and est["total_ttc"] == 59.16
    assert metering.estimate(q, plans.PRO, "association", "reverse")["lines"][0]["amount"] == 20.0
    assert metering.estimate(q, plans.ENTREPRISE, None, "reverse")["total_ht"] == 299.3
    # Annuel, 15 hôtes engagés : seules 5 hôtes-mois au-delà de l'engagement passent à l'usage.
    q2 = asyncio.run(metering.quantities(_Conn(hours, daily), 9, period, period.end, committed_hosts=15))
    assert q2["host"] == 5
    half = asyncio.run(metering.quantities(_Conn([{"hosts": 1, "devices": 0}] * 360, daily), 9, period, period.end))
    assert half["host"] == 0.5                                      # proratisé à l'heure, pas au pic


def test_tax_mode():
    assert B.tax_mode_for("FR", "verified") == "fr20"
    assert B.tax_mode_for("BE", "verified") == "reverse"
    assert B.tax_mode_for("BE", "pending") == "fr20"
    assert B.tax_mode_for("US", "verified") == "fr20"


# ── limites appliquées ─────────────────────────────────────────────────────────

def test_decouverte_refuses_sixth_host(client, monkeypatch):
    monkeypatch.setattr(T, "respond", responder(plan="decouverte", n_hosts=5))
    r = T.post_gz(client, "/api/v1/intake/metrics", {"agent": T.AGENT, "series": []})
    assert r.status_code == 402, r.text
    body = r.json()
    assert body["error"] == "plan_limit" and body["quota"] == "hosts" and body["limit"] == 5
    assert "5 hôtes au plus" in body["detail"]
    assert any("INSERT INTO org_refused_hosts" in e[1] for e in client.db.log if e[0] == "SQL")


def test_pro_admits_any_host(client, monkeypatch):
    monkeypatch.setattr(T, "respond", responder(plan="pro", n_hosts=500))
    r = T.post_gz(client, "/api/v1/intake/metrics", {"agent": T.AGENT, "series": []})
    assert r.status_code == 202, r.text


def test_decouverte_daily_volume(client, monkeypatch):
    monkeypatch.setattr(T, "respond", responder(plan="decouverte", n_hosts=0, bytes_today=600 * plans.MB))
    ts = int(T.NOW.timestamp() * 1000)
    r = T.post_gz(client, "/api/v1/intake/logs", {"agent": T.AGENT, "logs": [{"ts": ts, "message": "x"}]})
    assert r.status_code == 402 and r.json()["quota"] == "ingest_bytes_per_day", r.text
    assert "minuit UTC" in r.json()["detail"]


def test_decouverte_public_test_interval(client, monkeypatch):
    monkeypatch.setattr(T, "respond", responder(plan="decouverte"))
    r = client.post("/api/v1/synthetics", headers=T.UI, json={
        "name": "site", "kind": "http", "target": "https://example.org/", "interval_s": 60, "locations": ["server"]})
    assert r.status_code == 402 and r.json()["quota"] == "public_interval_min_s", r.text


def test_me_reports_plan(client, monkeypatch):
    monkeypatch.setattr(T, "respond", responder(plan="pro"))
    me = client.get("/api/v1/me", headers=T.UI).json()
    assert me["plan"] == "pro" and me["features"]["billing"] is True and me["features"]["billing_open"] is False


def test_checkout_closed(client, monkeypatch):
    monkeypatch.setattr(T, "respond", responder(plan="decouverte"))
    r = client.post("/api/v1/billing/checkout", headers=T.UI, json={"plan": "pro", "country": "FR"})
    assert r.status_code in (409, 503), r.text           # org 1 du banc : interne (409) ou fermé (503)


# ── webhook : compte partagé, seuls les objets HYPHA ───────────────────────────

def _post_event(client, event, secret=SECRET):
    body = json.dumps(event).encode()
    return client.post("/api/stripe/webhook", content=body,
                       headers={"Stripe-Signature": _signed(body, secret), "Content-Type": "application/json"})


@pytest.fixture()
def no_stripe(monkeypatch):
    async def fake_load(stripe, force=False):
        catalog.CATALOG.prices = {k: {"id": f"price_{k}", "lookup_key": lk} for k, lk in catalog.LOOKUP.items()}
        catalog.CATALOG.price_ids = {p["id"] for p in catalog.CATALOG.prices.values()}
        return catalog.CATALOG
    monkeypatch.setattr(catalog.CATALOG, "load", fake_load)

    async def no_call(*a, **k):
        raise AssertionError("appel Stripe inattendu")
    monkeypatch.setattr(B.Stripe, "request", no_call)


def test_webhook_rejects_bad_signature(client):
    r = _post_event(client, {"id": "evt_x", "type": "invoice.paid"}, secret="whsec_faux")
    assert r.status_code == 400 and r.json()["error"] == "invalid_signature"


def test_webhook_ignores_other_sites(client, monkeypatch, no_stripe):
    monkeypatch.setattr(T, "respond", responder())
    event = {"id": "evt_poker", "type": "customer.subscription.updated", "data": {"object": {
        "id": "sub_poker", "customer": "cus_poker", "status": "active", "metadata": {"site": "poker"},
        "items": {"data": [{"price": {"id": "price_poker", "lookup_key": "poker_mensuel"}}]}}}}
    r = _post_event(client, event)
    assert r.status_code == 200, r.text
    sqls = [e[1] for e in client.db.log if e[0] == "SQL"]
    assert not any("UPDATE orgs SET plan" in s for s in sqls)
    assert any("UPDATE stripe_events SET outcome" in s for s in sqls)


def test_webhook_hypha_subscription_sets_plan(client, monkeypatch, no_stripe):
    row = {"org_id": 7, "stripe_customer_id": "cus_h", "usage_subscription_id": None, "commit_subscription_id": None,
           "committed_hosts": 0, "status": "none", "wanted_plan": "pro", "tax_mode": "fr20", "discount": None}
    monkeypatch.setattr(T, "respond", responder(billing_row=row))
    start = int(datetime(2026, 12, 1, tzinfo=UTC).timestamp())
    event = {"id": "evt_h", "type": "customer.subscription.updated", "data": {"object": {
        "id": "sub_h", "customer": "cus_h", "status": "active",
        "metadata": {"offre": "hypha", "hypha_org_id": "7", "hypha_plan": "pro", "hypha_role": "usage"},
        "items": {"data": [{"price": {"id": "price_host", "lookup_key": "hypha_pro_hote_mensuel"},
                            "current_period_start": start, "current_period_end": start + 31 * 86400}]}}}}
    r = _post_event(client, event)
    assert r.status_code == 200, r.text
    plan_updates = [e for e in client.db.log if e[0] == "SQL" and "UPDATE orgs SET plan" in e[1]]
    assert plan_updates and plan_updates[0][2][0] == "pro" and plan_updates[0][2][1] == 7
    # Même client annoncé pour un autre espace : jamais rattaché.
    client.db.log.clear()
    event["id"] = "evt_h2"
    event["data"]["object"]["metadata"]["hypha_org_id"] = "8"
    assert _post_event(client, event).status_code == 200
    assert not any("UPDATE orgs SET plan" in e[1] for e in client.db.log if e[0] == "SQL")


def test_next_month_anchor():
    assert metering.next_month_start(datetime(2026, 12, 31, 23, 59, tzinfo=UTC)) == datetime(2027, 1, 1, tzinfo=UTC)
    assert metering.next_month_start(datetime(2026, 11, 1, 0, 0, tzinfo=UTC)) == datetime(2026, 12, 1, tzinfo=UTC)
    assert metering.month_of(datetime(2027, 2, 10, tzinfo=UTC)).hours == 28 * 24
    assert timedelta(hours=metering.month_of(datetime(2026, 12, 5, tzinfo=UTC)).hours) == timedelta(days=31)


# ── conservation longue (copie de nuit, purge) ─────────────────────────────────

class _LongDb:
    def __init__(self, entitled, copied_until, tracked):
        self.entitled, self.copied_until, self.tracked, self.log = entitled, copied_until, tracked, []

    def writer(self):
        db = self

        class Conn:
            async def execute(self, sql, params=None):
                db.log.append((" ".join(sql.split()), params))
                s = " ".join(sql.split())
                rows = []
                if "FROM org_billing b JOIN orgs o" in s:
                    rows = [{"org_id": k, "retention_days": v} for k, v in db.entitled.items()]
                elif "FROM long_retention_state" in s:
                    rows = [{"parent": p, "copied_until": db.copied_until} for p in ("logs", "spans")]
                elif "FROM long_retention_orgs" in s and s.startswith("SELECT"):
                    rows = db.tracked

                class Cur:
                    async def fetchall(self_inner):
                        return rows
                return Cur()

        from contextlib import asynccontextmanager

        @asynccontextmanager
        async def ctx():
            yield Conn()
        return ctx()


def test_long_retention_copies_closed_day_at_night():
    from hypha.billing import long_retention as LR
    now = datetime(2026, 12, 20, 2, 0, tzinfo=UTC)
    day = datetime(2026, 12, 5, tzinfo=UTC)                  # clos depuis le 20/12 00:00, purgé le 21/12
    db = _LongDb({7: 30}, day, [])
    out = asyncio.run(LR.run(db, now))
    inserts = [p for s, p in db.log if s.startswith("INSERT INTO logs_long")]
    assert len(inserts) == 24 and inserts[0] == (7, day, day + timedelta(hours=1))
    assert len([1 for s, _ in db.log if s.startswith("INSERT INTO spans_long")]) == 24
    assert any(s.startswith("UPDATE long_retention_state") and p[0] == day + timedelta(days=1) for s, p in db.log)
    assert "logs 2026-12-05" in out
    # En journée, loin de la purge : rien ne bouge.
    db2 = _LongDb({7: 30}, day, [])
    asyncio.run(LR.run(db2, datetime(2026, 12, 20, 12, 0, tzinfo=UTC)))
    assert not any(s.startswith("INSERT") for s, _ in db2.log)


def test_long_retention_purges_when_option_stops():
    from datetime import date
    from hypha.billing import long_retention as LR
    now = datetime(2026, 12, 20, 2, 0, tzinfo=UTC)
    tracked = [{"org_id": 8, "retention_days": 90, "first_day": date(2026, 9, 1), "last_day": date(2026, 12, 4)}]
    db = _LongDb({}, now, tracked)                          # plus d'option : tout part cette nuit
    asyncio.run(LR.run(db, now))
    deletes = [p for s, p in db.log if s.startswith("DELETE FROM logs_long")]
    assert deletes[0][1] == datetime(2026, 9, 1, tzinfo=UTC) and deletes[-1][2] == datetime(2026, 12, 5, tzinfo=UTC)
    assert len(deletes) == 95                                 # 95 jours, une tranche par jour
    assert any(s.startswith("DELETE FROM long_retention_orgs") for s, _ in db.log)
