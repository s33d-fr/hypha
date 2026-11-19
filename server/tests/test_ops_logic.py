"""Logique pure de la 0.4.0 (contrat §§ 16 à 22, 28) : maintenances, gabarits, composites, anomalies,
prévisions, groupes aberrants, règles d'alerte du § 17, gestes reconnus, disponibilité, rapports."""
import math
from datetime import date, datetime, timedelta, timezone

import pytest

from hypha.actions import propose as P
from hypha.monitors import algorithms as A
from hypha.monitors import composite, template, templates
from hypha.monitors import downtime as D
from hypha.monitors import evaluate as ev
from hypha.monitors.validate import normalize_monitor, normalize_options
from hypha.reports import render as R
from hypha.reports import schedule as SCH
from hypha.status import compute as SC

UTC = timezone.utc


def ms(dt: datetime) -> int:
    return int(dt.timestamp() * 1000)


# ── maintenances ───────────────────────────────────────────────────────────────

def _dt(rec=None, start=datetime(2026, 1, 1, tzinfo=UTC), end=None, scope=None):
    return {"start_ts": start, "end_ts": end, "recurrence": rec, "enabled": True, "scope": scope or {}}


def test_daily_recurrence_follows_timezone_and_dst():
    rec = D.normalize_recurrence({"type": "daily", "start": "02:00", "duration_s": 3600, "tz": "Europe/Paris"})
    dt = _dt(rec)
    # Hiver (UTC+1) : 02:00 à Paris = 01:00 UTC.
    assert D.active_window(dt, datetime(2026, 11, 18, 1, 30, tzinfo=UTC))
    assert not D.active_window(dt, datetime(2026, 11, 18, 0, 30, tzinfo=UTC))
    assert not D.active_window(dt, datetime(2026, 11, 18, 2, 0, tzinfo=UTC))
    # Été (UTC+2) : 02:00 à Paris = 00:00 UTC.
    w = D.active_window(dt, datetime(2026, 7, 1, 0, 30, tzinfo=UTC))
    assert w and w.start == datetime(2026, 7, 1, 0, 0, tzinfo=UTC) and w.end == datetime(2026, 7, 1, 1, 0, tzinfo=UTC)


def test_weekly_monthly_and_bounds():
    assert date(2026, 11, 16).isoweekday() == 1
    weekly = _dt(D.normalize_recurrence({"type": "weekly", "days": [1, 3], "start": "22:00", "duration_s": 7200,
                                         "tz": "UTC"}))
    assert D.active_window(weekly, datetime(2026, 11, 16, 23, 0, tzinfo=UTC))       # lundi
    assert not D.active_window(weekly, datetime(2026, 11, 17, 23, 0, tzinfo=UTC))   # mardi
    # Occurrence du mercredi 22:00 qui déborde sur jeudi 00:00.
    assert D.active_window(weekly, datetime(2026, 11, 18, 23, 59, tzinfo=UTC))
    nxt = D.next_window(weekly, datetime(2026, 11, 19, 12, 0, tzinfo=UTC))
    assert nxt.start == datetime(2026, 11, 23, 22, 0, tzinfo=UTC)
    monthly = _dt(D.normalize_recurrence({"type": "monthly", "day_of_month": 31, "start": "03:00", "duration_s": 600,
                                          "tz": "UTC"}))
    # Novembre n'a que 30 jours : l'occurrence tombe le 30.
    assert D.active_window(monthly, datetime(2026, 11, 30, 3, 5, tzinfo=UTC))
    assert not D.active_window(monthly, datetime(2026, 11, 29, 3, 5, tzinfo=UTC))
    until = ms(datetime(2026, 11, 20, tzinfo=UTC))
    bounded = _dt(D.normalize_recurrence({"type": "daily", "start": "10:00", "duration_s": 3600, "tz": "UTC",
                                          "until": until}), start=datetime(2026, 11, 18, 10, 30, tzinfo=UTC))
    assert not D.active_window(bounded, datetime(2026, 11, 18, 10, 15, tzinfo=UTC))   # avant start_ts
    w = D.active_window(bounded, datetime(2026, 11, 18, 10, 45, tzinfo=UTC))
    assert w.start == datetime(2026, 11, 18, 10, 30, tzinfo=UTC)
    assert D.active_window(bounded, datetime(2026, 11, 19, 10, 15, tzinfo=UTC))
    assert not D.active_window(bounded, datetime(2026, 11, 21, 10, 15, tzinfo=UTC))   # après until
    assert D.next_window(bounded, datetime(2026, 11, 21, tzinfo=UTC)) is None
    ended = D.last_ended(bounded, datetime(2026, 11, 19, 11, 30, tzinfo=UTC), timedelta(days=1))
    assert ended.end == datetime(2026, 11, 19, 11, 0, tzinfo=UTC)


def test_one_shot_downtime_and_shortcut():
    now = ms(datetime(2026, 11, 18, 12, 0, tzinfo=UTC))
    d = D.normalize_downtime({"name": "Patch", "duration_s": 3600, "scope": {"tags": {"host": "web-01"}}}, now)
    assert d["start_ts"] == now and d["end_ts"] == now + 3600_000 and d["recurrence"] is None
    row = {**d, "start_ts": datetime(2026, 11, 18, 12, 0, tzinfo=UTC), "end_ts": datetime(2026, 11, 18, 13, 0, tzinfo=UTC)}
    assert D.active_window(row, datetime(2026, 11, 18, 12, 30, tzinfo=UTC))
    assert not D.active_window(row, datetime(2026, 11, 18, 13, 0, tzinfo=UTC))
    assert D.next_window(row, datetime(2026, 11, 18, 11, 0, tzinfo=UTC)).start == row["start_ts"]
    assert not D.active_window({**row, "enabled": False}, datetime(2026, 11, 18, 12, 30, tzinfo=UTC))
    for bad in ({"name": ""}, {"name": "x", "start_ts": now, "end_ts": now - 1},
                {"name": "x", "recurrence": {"type": "weekly", "start": "02:00", "duration_s": 60}},
                {"name": "x", "recurrence": {"type": "daily", "start": "25:00", "duration_s": 60}},
                {"name": "x", "recurrence": {"type": "daily", "start": "02:00", "duration_s": 60, "tz": "Mars/Olympus"}},
                {"name": "x", "scope": {"hosts": ["a"]}}, {"name": "x", "scope": {"monitor_ids": ["1"]}}):
        with pytest.raises(ValueError):
            D.normalize_downtime(bad, now)


def test_downtime_scope():
    assert D.covers({}, 4, "host:web-01")
    assert D.covers({"monitor_ids": [4]}, 4, "*") and not D.covers({"monitor_ids": [5]}, 4, "*")
    scope = {"tags": {"host": "web-01"}}
    assert D.covers(scope, 4, "host:web-01,unit:nginx.service") and not D.covers(scope, 4, "host:web-02")
    assert not D.covers(scope, 4, "*")
    env = {"tags": {"env": "prod"}}
    assert D.covers(env, 4, "host:web-01", {"web-01": {"env": "prod"}})
    assert not D.covers(env, 4, "host:web-02", {"web-02": {"env": "dev"}})
    assert D.covers({"groups": ["host:a,unit:x"]}, 1, "host:a,unit:x") and not D.covers({"groups": ["host:a"]}, 1, "host:b")
    assert D.covers_host({"tags": {"host": "web-01"}}, "web-01") and not D.covers_host({"monitor_ids": [1]}, "web-01")
    active = [D.Active(1, "m", {"monitor_ids": [9]}, None), D.Active(2, "n", scope, None)]
    assert D.suppressed_by(active, 4, "host:web-01").id == 2 and D.suppressed_by(active, 4, "host:x") is None


# ── gabarits ───────────────────────────────────────────────────────────────────

def test_template_variables_blocks_and_mentions():
    ctx = {"name": "CPU", "group": "host:web-01", "host": "web-01", "value": "95", "state": "ALERT", "priority": "P1",
           "threshold": "90", "url": "https://h/m/1", "last_value": "40", "duration": "5 min", "eta": "",
           "tags": {"env": "prod"}, **template.flags_for("ALERT", "OK")}
    text = ("{{#is_alert}}ALERTE {{name}} sur {{ host }} : {{value}} > {{threshold}} @canal-7{{/is_alert}}"
            "{{#is_recovery}}rétabli{{/is_recovery}}{{^is_recovery}} [{{priority}} {{tags.env}}{{tags.absent}}]"
            "{{/is_recovery}} {{inconnue}}")
    out = template.render(text, ctx)
    assert out == "ALERTE CPU sur web-01 : 95 > 90 @canal-7 [P1 prod] {{inconnue}}"
    assert template.mentions(out) == [7] and "@canal" not in template.strip_mentions(out)
    rec = template.render("{{#is_recovery}}OK après {{duration}}{{#is_alert}}jamais{{/is_alert}}{{/is_recovery}}",
                          {"duration": "3 h", **template.flags_for("OK", "ALERT")})
    assert rec == "OK après 3 h"
    assert template.mentions("@canal-3 et @canal-3, mail@canal-4") == [3]
    assert template.human_duration(42) == "42 s" and template.human_duration(3 * 3600 + 300) == "3 h 5 min"
    assert template.human_duration(2 * 86400 + 4 * 3600 + 60) == "2 j 4 h"
    assert template.eta_text(-1) == "jamais" and template.eta_text(0) == "déjà franchi"
    assert template.eta_text(90000) == "dans 1 j 1 h"
    assert ev.render("{{name}} {{ state }}", {"name": "CPU", "state": "ALERT"}) == "CPU ALERT"


# ── composites ─────────────────────────────────────────────────────────────────

def test_composite_expression():
    node = composite.parse("12 && (13 || !14)")
    assert composite.ids(node) == {12, 13, 14}
    assert composite.evaluate(node, {12: True, 13: False, 14: False})
    assert not composite.evaluate(node, {12: True, 13: False, 14: True})
    # && lie plus fort que || : 1 || 2 && !3 = 1 || (2 && !3).
    n = composite.parse("1 || 2 && !3")
    assert composite.evaluate(n, {1: True, 3: True}) and not composite.evaluate(n, {2: True, 3: True})
    for bad in ("", "1 &&", "(1", "1 2", "1 & 2", "a || 1", "1)", "!"):
        with pytest.raises(ValueError):
            composite.parse(bad)
    assert composite.find_cycle({1: {2}, 2: {3}, 3: set()}, 1) is None
    assert composite.find_cycle({1: {2}, 2: {1}}, 1) == [1, 2, 1]
    assert composite.find_cycle({0: {5}, 5: {6}, 6: {5}}, 0) == [5, 6, 5]


# ── anomalies, prévisions, aberrants ───────────────────────────────────────────

STEP = 300_000
T_END = ms(datetime(2026, 11, 18, 12, 0, tzinfo=UTC))


def _seasonal(t_ms: int) -> float:
    hour = (t_ms / 3_600_000) % 24
    return 50 + 20 * math.sin(2 * math.pi * hour / 24)


def test_anomaly_basic_agile_robust():
    w_start = T_END - 900_000
    # basic : 2 h presque plates, puis un saut.
    hist = [(t, 10.0 + (0.1 if (t // 60_000) % 2 else -0.1)) for t in range(w_start - 7_200_000, w_start, 60_000)]
    calm = [(t, 10.0) for t in range(w_start, T_END, 60_000)]
    spike = [(t, 30.0) for t in range(w_start, T_END, 60_000)]
    assert A.enough_history("basic", hist, w_start, 86400)
    assert A.anomaly_share(A.anomaly_bounds("basic", hist, calm, 2.0, "daily", 60), "both") == 0.0
    assert A.anomaly_share(A.anomaly_bounds("basic", hist, spike, 2.0, "daily", 60), "both") == 100.0
    assert A.anomaly_share(A.anomaly_bounds("basic", hist, spike, 2.0, "daily", 60), "below") == 0.0
    # agile et robust : saison quotidienne sur 8 jours au pas de 5 min.
    start = T_END - 8 * 86_400_000
    hist = [(t, _seasonal(t)) for t in range(start, w_start, STEP)]
    normal = [(t, _seasonal(t)) for t in range(w_start, T_END, STEP)]
    odd = [(t, _seasonal(t) + 40) for t in range(w_start, T_END, STEP)]
    for alg in ("agile", "robust"):
        assert A.anomaly_share(A.anomaly_bounds(alg, hist, normal, 3.0, "daily", 300), "both") == 0.0, alg
        assert A.anomaly_share(A.anomaly_bounds(alg, hist, odd, 3.0, "daily", 300), "both") == 100.0, alg
    assert not A.enough_history("agile", hist[-100:], w_start, 86400)
    assert A.anomaly_share([], "both") is None


def test_forecast_linear_and_seasonal():
    now_s = T_END / 1000
    # Disque : +0,25 point par heure depuis 7 jours, 82 % maintenant → 95 % dans 52 h.
    pts = [(t, 82.0 - 0.25 * (T_END - t) / 3_600_000) for t in range(T_END - 7 * 86_400_000, T_END + 1, 3_600_000)]
    pts[10] = (pts[10][0], 400.0)          # point aberrant : Theil-Sen l'ignore
    model = A.fit_forecast(pts, "linear")
    assert abs(model["slope"] * 3600 - 0.25) < 1e-6
    eta = A.time_to_breach(model, 95.0, ">", 172800, now_s)
    assert 52 * 3600 - 1800 <= eta <= 52 * 3600 + 1800
    assert A.forecast_state(eta, None, 172800) == "OK"
    assert A.forecast_state(A.time_to_breach(model, 90.0, ">", 172800, now_s), None, 172800) == "ALERT"
    assert A.time_to_breach(model, 80.0, ">", 172800, now_s) == 0.0
    assert A.time_to_breach(model, 10.0, "<", 172800, now_s) == -1.0
    fc = A.forecast_points(model, now_s, 172800)
    assert len(fc) == 100 and all(lo <= v <= hi for _, v, lo, hi in fc)
    assert A.fit_forecast(pts[:5], "linear") is None
    # Saisonnier : la saison quotidienne est retirée des résidus.
    spts = [(t, _seasonal(t) + 0.1 * (t - T_END) / 3_600_000) for t in range(T_END - 3 * 86_400_000, T_END + 1, 3_600_000)]
    sm = A.fit_forecast(spts, "seasonal")
    lm = A.fit_forecast(spts, "linear")
    assert sm["sigma"] < lm["sigma"] / 5
    assert A.fit_forecast(spts[-20:], "seasonal") is None


def test_outliers_mad_and_dbscan():
    series = {f"host:w{i}": {t: 1.0 + 0.01 * i for t in range(0, 600_000, 60_000)} for i in range(5)}
    series["host:bad"] = {t: 9.0 for t in range(0, 600_000, 60_000)}
    shares = A.outliers_mad(series, 3.0)
    assert shares["host:bad"] == 100.0 and all(shares[f"host:w{i}"] == 0.0 for i in range(5))
    db = A.outliers_dbscan(series, 3.0)
    assert db["host:bad"] == 100.0 and all(db[f"host:w{i}"] == 0.0 for i in range(5))
    assert A.outliers_mad({"a": {0: 1.0}, "b": {0: 5.0}}, 3.0) == {}
    assert A.outliers_dbscan({"a": {0: 1.0}, "b": {0: 2.0}}, 3.0) == {"a": 0.0, "b": 0.0}


def test_change_value():
    assert A.change_value(150.0, 100.0, "pct") == 50.0
    assert A.change_value(150.0, 100.0, "abs") == 50.0
    assert A.change_value(5.0, 0.0, "pct") is None and A.change_value(0.0, 0.0, "pct") == 0.0
    assert A.change_value(None, 1.0, "abs") is None
    assert A.theil_sen([0, 1, 2, 3, 4, 5], [0, 2, 4, 6, 8, 100])[0] == 2.0


# ── règles d'alerte du § 17 ────────────────────────────────────────────────────

T0 = datetime(2026, 11, 18, 12, 0, tzinfo=UTC)


def st(state, minutes_ago=1, **kw):
    t = T0 - timedelta(minutes=minutes_ago)
    return ev.PrevState(state, 1.0, t, t, kw.pop("notified", None), **kw)


def test_require_and_recovery_consecutive():
    opts = ev.Options.of({"require_consecutive": 3, "recovery_consecutive": 2})
    streaks = {}
    prev = {"*": st(ev.OK)}
    for i in range(2):
        p = ev.plan(prev, {"*": (ev.ALERT, 95.0)}, T0, 600, False, 0, False, opts=opts, streaks=streaks)
        assert p.transitions == [] and p.upserts[0][1] == ev.OK
        streaks = p.streaks
    assert streaks == {"*": (ev.ALERT, 2)}
    p = ev.plan(prev, {"*": (ev.ALERT, 95.0)}, T0, 600, False, 0, False, opts=opts, streaks=streaks)
    assert p.transitions == [("*", ev.OK, ev.ALERT, 95.0, True)] and p.streaks == {}
    # Une mesure OK au milieu remet le compteur à zéro.
    p = ev.plan(prev, {"*": (ev.OK, 1.0)}, T0, 600, False, 0, False, opts=opts, streaks={"*": (ev.ALERT, 2)})
    assert p.streaks == {}
    prev = {"*": st(ev.ALERT)}
    p = ev.plan(prev, {"*": (ev.OK, 1.0)}, T0, 600, False, 0, False, opts=opts)
    assert p.transitions == [] and p.streaks == {"*": (ev.OK, 1)}
    p = ev.plan(prev, {"*": (ev.OK, 1.0)}, T0, 600, False, 0, False, opts=opts, streaks=p.streaks)
    assert p.transitions == [("*", ev.ALERT, ev.OK, 1.0, True)]


def test_downtime_suppression_and_end_notice():
    p = ev.plan({"host:a": st(ev.OK)}, {"host:a": (ev.ALERT, 9.0)}, T0, 600, False, 0, False,
                suppress={"host:a": ev.DOWNTIME})
    assert p.transitions == [("host:a", ev.OK, ev.ALERT, 9.0, False)]
    assert p.flags["host:a"][2] == ev.DOWNTIME and p.suppressed == {"host:a": ev.DOWNTIME}
    # La maintenance finit, le groupe est toujours en ALERT : une notification, une seule.
    old = st(ev.ALERT, 30, suppressed=ev.DOWNTIME)
    p = ev.plan({"host:a": old}, {"host:a": (ev.ALERT, 9.0)}, T0, 600, False, 900, False)
    assert p.notices == [("host:a", ev.ALERT, 9.0, "suppression_end")] and p.renotify == []
    assert p.flags["host:a"][2] is None and p.upserts[0][5] == T0
    # Fin de maintenance sur un groupe revenu à OK : rien.
    p = ev.plan({"host:a": st(ev.OK, 30, suppressed=ev.DOWNTIME)}, {"host:a": (ev.OK, 1.0)}, T0, 600, False, 0, False)
    assert p.notices == []
    # Dépendance : même traitement.
    p = ev.plan({"host:a": st(ev.OK)}, {"host:a": (ev.ALERT, 9.0)}, T0, 600, False, 0, False,
                suppress={"host:a": ev.DEPENDENCY})
    assert p.transitions[0][4] is False and p.flags["host:a"][2] == ev.DEPENDENCY


def test_flapping():
    changes = [T0 - timedelta(minutes=m) for m in (25, 20, 15, 10)]
    p = ev.plan({"*": st(ev.OK)}, {"*": (ev.ALERT, 9.0)}, T0, 600, False, 0, False,
                recent_changes={"*": changes})
    assert p.transitions == [("*", ev.OK, ev.ALERT, 9.0, False)]
    assert p.notices == [("*", ev.ALERT, 9.0, "flapping")] and p.flags["*"][0] is True
    assert p.flags["*"][2] == ev.FLAPPING
    # Toujours instable : rien ne part.
    p = ev.plan({"*": st(ev.ALERT, flapping=True)}, {"*": (ev.OK, 1.0)}, T0, 600, False, 0, False,
                recent_changes={"*": changes})
    assert p.transitions[0][4] is False and p.notices == [] and p.flags["*"][0] is True
    # Plus aucun changement sur la fenêtre : stable, une notification de l'état courant.
    p = ev.plan({"*": st(ev.ALERT, 40, flapping=True)}, {"*": (ev.ALERT, 9.0)}, T0, 600, False, 0, False)
    assert p.notices == [("*", ev.ALERT, 9.0, "flapping_end")] and p.flags["*"][0] is False
    # Détection coupée.
    p = ev.plan({"*": st(ev.OK)}, {"*": (ev.ALERT, 9.0)}, T0, 600, False, 0, False,
                opts=ev.Options.of({"flapping": False}), recent_changes={"*": changes * 3})
    assert p.transitions[0][4] is True and p.notices == []


def test_ack_and_escalations():
    esc = [{"after_s": 900, "channel_ids": [5], "message": ""}, {"after_s": 3600, "channel_ids": [6], "message": "x"}]
    opts = ev.Options.of({"escalations": esc})
    old = st(ev.ALERT, 20, notified=T0 - timedelta(minutes=20))
    p = ev.plan({"*": old}, {"*": (ev.ALERT, 9.0)}, T0, 600, False, 600, False, opts=opts)
    assert [(e[3], e[4]["channel_ids"]) for e in p.escalations] == [(1, [5])] and p.flags["*"][1] == 1
    assert p.renotify == [("*", ev.ALERT, 9.0)]
    # Marche déjà atteinte : pas de doublon ; la 2e attend 1 h depuis le passage hors OK.
    old = st(ev.ALERT, 50, escalation_step=1)
    p = ev.plan({"*": old}, {"*": (ev.ALERT, 9.0)}, T0, 600, False, 0, False, opts=opts)
    assert p.escalations == []
    p = ev.plan({"*": old}, {"*": (ev.ALERT, 9.0)}, T0, 600, False, 0, False, opts=opts,
                out_since={"*": T0 - timedelta(minutes=70)})
    assert [e[3] for e in p.escalations] == [2] and p.flags["*"][1] == 2
    # Acquitté : ni relance ni escalade ; un retour à OK efface l'acquittement et la marche.
    acked = st(ev.ALERT, 120, acked=True, notified=T0 - timedelta(hours=2))
    p = ev.plan({"*": acked}, {"*": (ev.ALERT, 9.0)}, T0, 600, False, 600, False, opts=opts)
    assert p.escalations == [] and p.renotify == []
    p = ev.plan({"*": st(ev.ALERT, acked=True, escalation_step=2)}, {"*": (ev.OK, 1.0)}, T0, 600, False, 0, False,
                opts=opts)
    assert p.flags["*"] == (False, 0, None, True)


def test_options_and_new_types_validation():
    o = normalize_options(None)
    assert o == {"depends_on_host": True, "depends_on": [], "flapping": {"changes": 5, "window_s": 1800},
                 "escalations": [], "require_consecutive": 1, "recovery_consecutive": 1, "evaluation_delay_s": 0}
    o = normalize_options({"flapping": False, "escalations": [{"after_s": 1800, "channel_ids": [2, 2]},
                                                              {"after_s": 600, "channel_ids": [1], "message": "m"}]})
    assert o["flapping"] is False and [e["after_s"] for e in o["escalations"]] == [600, 1800]
    for bad in ({"nope": 1}, {"escalations": [{"after_s": 600, "channel_ids": []}]}, {"require_consecutive": 0},
                {"escalations": [{"after_s": 600, "channel_ids": [1]}, {"after_s": 600, "channel_ids": [2]}]},
                {"flapping": {"changes": 1}}, {"depends_on": ["3"]}):
        with pytest.raises(ValueError):
            normalize_options(bad)
    m = normalize_monitor({"name": "a", "type": "anomaly", "query": {"q": "avg:system.load.norm.1{*} by {host}"}})
    assert m["thresholds"] == {"critical": 50.0} and m["query"]["algorithm"] == "basic" and m["priority"] is None
    m = normalize_monitor({"name": "c", "type": "composite", "query": {"expression": "3  &&  !4"}, "priority": 1})
    assert m["query"] == {"expression": "3 && !4", "warn_as_true": False, "ids": [3, 4]} and m["priority"] == 1
    m = normalize_monitor({"name": "l", "type": "log", "query": {"q": "sshd", "by": ["@network.client.ip", "host"]},
                           "thresholds": {"critical": 20}})
    assert m["query"]["by"] == ["@network.client.ip", "host"]
    m = normalize_monitor({"name": "p", "type": "process", "query": {"name": "nginx", "tags": {"role": "web"}}})
    assert m["query"] == {"name": "nginx", "match": "exact", "tags": {"role": "web"}, "by": ["host"], "min": 1,
                          "max": None}
    m = normalize_monitor({"name": "e", "type": "error_tracking", "query": {"service": "api"}, "thresholds": {"critical": 0}})
    assert m["query"]["new_only"] is True
    for bad in ({"type": "forecast", "query": {"q": "max:system.disk.in_use{*}"}},
                {"type": "outlier", "query": {"q": "avg:system.load.norm.1{*}"}},
                {"type": "composite", "query": {"expression": "1 &&"}},
                {"type": "process", "query": {"name": "(", "match": "regex"}},
                {"type": "process", "query": {"name": "x", "min": 3, "max": 1}},
                {"type": "anomaly", "query": {"q": "avg:x{*}", "algorithm": "magic"}},
                {"type": "anomaly", "query": {"q": "avg:x{*}"}, "thresholds": {"critical": 150}},
                {"type": "event", "query": {"q": "@attr:x"}, "thresholds": {"critical": 1}},
                {"type": "change", "query": {"q": "sum:x{*}", "mode": "ratio"}, "thresholds": {"critical": 1}},
                {"type": "forecast", "query": {"q": "max:x{*}", "algorithm": "seasonal", "history_s": 7200},
                 "thresholds": {"critical": 1}},
                {"type": "log", "query": {"q": "", "by": ["@bad path"]}, "thresholds": {"critical": 1}},
                {"type": "metric", "query": {"q": "avg:x{*}"}, "thresholds": {"critical": 1}, "priority": 6}):
        with pytest.raises(ValueError):
            normalize_monitor({"name": "x", **bad})


def test_template_catalogue_builds_valid_monitors():
    ids = {t["id"] for t in templates.catalogue()}
    for wanted in ("cpu_high", "memory_high", "disk_forecast", "inodes_high", "swap_high", "load_anomaly", "host_down",
                   "systemd_unit", "windows_service", "snmp_interface_down", "snmp_unreachable", "cert_expiry",
                   "synthetic_failed", "security_updates", "reboot_required", "ssh_bruteforce", "http_5xx",
                   "latency_p95", "pg_replication_lag", "postfix_queue", "new_error", "disk_full", "ovh_server_state",
                   "ovh_ip_spam", "ovh_ip_antihack", "ovh_ip_mitigation", "ovh_service_renewal", "ovh_connector",
                   "vsphere_host_status", "vsphere_datastore_status", "vsphere_datastore_full", "vsphere_vm_status",
                   "vsphere_snapshot_old", "vsphere_connector", "mysql_down", "mysql_replication",
                   "mysql_replication_lag", "mysql_connections", "redis_down", "redis_replication",
                   "redis_persistence", "redis_memory", "memcached_down", "rabbitmq_down", "rabbitmq_node_alarm",
                   "rabbitmq_queue_backlog", "mongodb_down", "mongodb_replication", "mongodb_replication_lag",
                   "opensearch_down", "opensearch_cluster_health", "opensearch_heap", "opensearch_disk",
                   "opensearch_rejections", "kube_connector", "kube_node", "kube_workload", "kube_restarts",
                   "kube_unschedulable", "libvirt_domain", "libvirt_pool", "libvirt_pool_full", "proxmox_connector",
                   "proxmox_quorum", "proxmox_node", "proxmox_storage", "proxmox_storage_full", "proxmox_guest_ha",
                   "proxmox_backup", "hyperv_vm", "hyperv_replication", "hyperv_checkpoint_old", "aws_connector",
                   "aws_ec2_status", "aws_ebs_volume", "aws_cloudwatch_alarm", "aws_ec2_cpu", "scaleway_connector",
                   "scaleway_server", "scaleway_metal", "azure_connector", "azure_vm_health", "azure_alerts",
                   "gcp_connector", "gcp_instance"):
        assert wanted in ids
    given = {"synthetic_failed": {"test_id": 12}, "http_5xx": {"service": "api"}, "latency_p95": {"service": "api"}}
    for t in templates.TEMPLATES:
        m = normalize_monitor(templates.build(t["id"], given.get(t["id"], {})))
        assert m["name"] and m["type"] in ("metric", "forecast", "anomaly", "host", "service_check", "synthetic",
                                           "log", "apm", "error_tracking")
    disk = normalize_monitor(templates.build("disk_forecast", {"scope": "env:prod", "horizon_h": 24}))
    assert disk["query"]["q"] == "max:system.disk.in_use{env:prod} by {host,mount}" and disk["query"]["horizon_s"] == 86400
    ssh = normalize_monitor(templates.build("ssh_bruteforce", {}))
    assert ssh["query"]["by"] == ["@network.client.ip"] and ssh["thresholds"]["critical"] == 20.0
    assert normalize_monitor(templates.build("postfix_queue", {}))["query"]["q"] == \
        "max:postfix.queue.size{queue:deferred} by {host}"
    for tid, bad in (("nope", {}), ("cpu_high", {"scope": "x y"}), ("cpu_high", {"critical": "90"}),
                     ("synthetic_failed", {}), ("cpu_high", {"extra": 1}), ("host_down", {"tag": "a,b"})):
        with pytest.raises(ValueError):
            templates.build(tid, bad)


# ── gestes de réparation ───────────────────────────────────────────────────────

ANSWER_FR = """## Ce qui se passe
nginx est arrêté.
## Cause probable
`systemctl status nginx` montre « failed ».
## Correctif
```bash
sudo systemctl restart nginx
sudo systemctl --no-pager start php8.3-fpm.service && journalctl -u nginx
service cron restart
```
## Éviter la récidive
Surveiller avec `systemctl stop truc`.
"""


def test_fix_section_and_gestures():
    fix = P.fix_section(ANSWER_FR)
    assert "restart nginx" in fix and "truc" not in fix
    found = P.find_gestures(fix)
    assert [(g["kind"], g["params"]) for g in found] == [
        ("restart_service", {"unit": "nginx.service"}), ("start_service", {"unit": "php8.3-fpm.service"}),
        ("restart_service", {"unit": "cron.service"})]
    en = "## What is happening\nx\n## Probable cause\ny\n## Fix\nRun `Restart-Service -Name W3SVC -Force` then " \
         "`Stop-Service \"Print Spooler\"`.\n## Preventing recurrence\nz"
    gestures = P.find_gestures(P.fix_section(en))
    assert [(g["kind"], g["os"], g["params"]) for g in gestures] == [
        ("restart_service", "windows", {"service": "W3SVC"}), ("stop_service", "windows", {"service": "Print Spooler"})]
    # Langue inconnue : la 3e partie d'une réponse en quatre.
    other = "## A\n.\n## B\n.\n## C\nsystemctl restart redis-server\n## D\n."
    assert P.find_gestures(P.fix_section(other))[0]["params"] == {"unit": "redis-server.service"}
    assert P.find_gestures("systemctl restart $(cat x); rm -rf /") == []
    assert P.label("restart_service", {"unit": "nginx.service"}, "web-01", "fr") == "Redémarrer nginx.service sur web-01"
    assert P.label("stop_service", {"service": "W3SVC"}, "win-01", "en") == "Stop W3SVC on win-01"


def test_action_params_and_capabilities():
    assert P.validate_params("restart_service", {"unit": "nginx"}, "linux") == {"unit": "nginx.service"}
    assert P.validate_params("restart_service", {"service": "W3SVC"}, "windows") == {"service": "W3SVC"}
    assert P.validate_params("run_script", {"script": "vider-cache-nginx"}, "linux") == {"script": "vider-cache-nginx"}
    assert P.validate_params("update_agent", {"version": "0.4.0"}, "linux") == {"version": "0.4.0"}
    for kind, params in (("restart_service", {"unit": "a;b"}), ("restart_service", {"unit": "a", "x": 1}),
                         ("run_script", {"script": "/etc/passwd"}), ("update_agent", {"version": "latest"}),
                         ("format_disk", {}), ("restart_service", "nginx")):
        with pytest.raises(ValueError):
            P.validate_params(kind, params, "linux")
    caps = {"actions": {"enabled": True, "kinds": ["restart_service", "run_script"], "scripts": ["vider-cache"]}}
    assert P.capable(caps, "restart_service", {"unit": "x.service"}) == (True, "")
    assert not P.capable(caps, "stop_service", {"unit": "x.service"})[0]
    assert not P.capable(caps, "run_script", {"script": "autre"})[0]
    assert not P.capable({"actions": {"enabled": False, "kinds": ["restart_service"]}}, "restart_service", {})[0]
    assert not P.capable({}, "restart_service", {})[0]


# ── pages de statut ────────────────────────────────────────────────────────────

def test_status_computation():
    assert SC.monitor_status("ALERT", 1, False) == "major_outage"
    assert SC.monitor_status("ALERT", 2, False) == "partial_outage"
    assert SC.monitor_status("WARN", None, False) == "degraded"
    assert SC.monitor_status("NODATA", None, False) == "operational"
    assert SC.monitor_status("ALERT", 1, True) == "maintenance"
    assert SC.slo_status("breached", False) == "partial_outage" and SC.synthetic_status(False, False) == "partial_outage"
    assert SC.overall(["operational", "maintenance", "degraded"]) == "degraded"
    assert SC.overall([]) == "operational" and SC.overall(["maintenance", "operational"]) == "maintenance"
    now = datetime(2026, 11, 18, 12, 0, tzinfo=UTC)
    days = SC.utc_days(now)
    assert len(days) == 90 and days[-1] == date(2026, 11, 18)
    day = datetime(2026, 11, 17, tzinfo=UTC).timestamp()
    bad = [(day + 3600, day + 7200)]
    warn = [(day + 10 * 3600, day + 11 * 3600)]
    start = datetime(2026, 11, 16, 6, 0, tzinfo=UTC).timestamp()
    out = SC.daily_from_intervals(bad, warn, start, now.timestamp(), days)
    by = {d["date"]: d for d in out}
    assert by["2026-11-15"] == {"date": "2026-11-15", "uptime": None, "worst": None}
    assert by["2026-11-16"] == {"date": "2026-11-16", "uptime": 100.0, "worst": "operational"}
    assert by["2026-11-17"]["uptime"] == 95.83 and by["2026-11-17"]["worst"] == "partial_outage"
    assert SC.daily_from_intervals(bad, [], start, now.timestamp(), days, True)[-2]["worst"] == "major_outage"
    assert SC.uptime_from_intervals([(0, 1)], 0, 100000) == 99.99      # tronqué, jamais arrondi à 100
    counts = {date(2026, 11, 17): (99.0, 100.0), date(2026, 11, 18): (10.0, 10.0)}
    sd = SC.daily_from_counts(counts, days)
    assert sd[-2] == {"date": "2026-11-17", "uptime": 99.0, "worst": "degraded"} and sd[-1]["worst"] == "operational"
    assert SC.daily_from_counts(counts, days, "slo", 99.5)[-2]["worst"] == "partial_outage"
    assert SC.uptime_from_counts(counts) == 99.09 and SC.uptime_from_counts({}) is None


# ── notifications ──────────────────────────────────────────────────────────────

def test_notification_payloads():
    from hypha.monitors import notify as N
    mon = {"id": 3, "name": "CPU <b>", "type": "metric", "priority": 1}
    n = N.Notice({"id": 1, "kind": "pagerduty", "config": {}}, mon, "host:web-01,device:sda", "ALERT", "OK", 95.0,
                 "message <i>", "https://h/monitors/3", threshold=90.0, explain_url="https://h/problems?explain=3",
                 logs=["10:00:00 [error] nginx boom"])
    assert n.subject == "[HYPHA] [ALERT] [P1] CPU <b> (host:web-01,device:sda)"
    ev = N.pagerduty_event(n, "k" * 32)
    assert ev["event_action"] == "trigger" and ev["dedup_key"] == "3:host:web-01,device:sda"
    assert ev["payload"]["severity"] == "critical" and ev["payload"]["source"] == "web-01"
    rec = N.Notice(n.channel, mon, n.group, "OK", "ALERT", 1.0, "rétabli", n.url)
    assert N.pagerduty_event(rec, "k" * 32) == {"routing_key": "k" * 32, "event_action": "resolve",
                                                "dedup_key": "3:host:web-01,device:sda"}
    og = N.opsgenie_create(n)
    assert og["priority"] == "P1" and og["alias"] == "3:host:web-01,device:sda" and len(og["message"]) <= 130
    assert N.ntfy_payload(n, "ops")["priority"] == 5 and N.ntfy_payload(rec, "ops")["tags"] == ["white_check_mark"]
    assert N.discord_payload(n)["embeds"][0]["color"] == 0xE01E5A
    html = N.alert_html(n)
    assert "&lt;b&gt;" in html and "&lt;i&gt;" in html and "<i>" not in html and "#E01E5A" in html
    assert "Expliquer avec l'IA" in html and "nginx boom" in html and "P1 critique" in html
    text = N.text_body(n)
    assert "Seuil : 90.0" in text and "Explication : https://h/problems?explain=3" in text
    inc = N.Notice(n.channel, {"id": 7, "name": "INC-12 x", "type": "incident", "priority": 2}, "*", "ALERT", None,
                   None, "Incident déclaré", "https://h/incidents/7", kind="incident", subject_override="[HYPHA] INC-12",
                   dedup_key="incident:7")
    assert "État" not in N.text_body(inc) and "Priorité" not in N.alert_html(inc) and inc.key == "incident:7"
    assert N.Notice(n.channel, mon, "*", "WARN", "OK", 1.0, "m", "u", True).subject.startswith("[relance] ")
    assert N.Notice(n.channel, mon, "*", "WARN", "OK", 1.0, "m", "u", kind="escalation").subject.startswith("[escalade] ")


# ── rapports ───────────────────────────────────────────────────────────────────

def test_report_schedule():
    s = SCH.normalize_schedule({"every": "week", "weekday": 1, "hour": 8, "tz": "Europe/Paris"})
    now = datetime(2026, 11, 18, 12, 0, tzinfo=UTC)                     # mercredi
    assert SCH.last_due(s, now) == datetime(2026, 11, 16, 7, 0, tzinfo=UTC)
    assert SCH.next_due(s, now) == datetime(2026, 11, 23, 7, 0, tzinfo=UTC)
    created = datetime(2026, 11, 10, tzinfo=UTC)
    assert SCH.is_due(s, now, None, created)
    assert SCH.is_due(s, now, datetime(2026, 11, 9, tzinfo=UTC), created)
    assert not SCH.is_due(s, now, datetime(2026, 11, 16, 7, 1, tzinfo=UTC), created)
    assert not SCH.is_due(s, now, None, datetime(2026, 11, 17, tzinfo=UTC))       # créé après l'échéance
    daily = SCH.normalize_schedule({"every": "day", "hour": 13, "tz": "UTC"})
    assert SCH.last_due(daily, now) == datetime(2026, 11, 17, 13, 0, tzinfo=UTC)
    monthly = SCH.normalize_schedule({"every": "month", "day": 31, "hour": 6, "tz": "UTC"})
    assert SCH.last_due(monthly, now) == datetime(2026, 10, 31, 6, 0, tzinfo=UTC)
    assert SCH.next_due(monthly, now) == datetime(2026, 11, 30, 6, 0, tzinfo=UTC)
    for bad in ({"every": "year"}, {"every": "week", "weekday": 8}, {"hour": 24}, {"tz": "Nulle/Part"}):
        with pytest.raises(ValueError):
            SCH.normalize_schedule(bad)


def test_report_render():
    svg = R.sparkline([1.0, 2.0, None, 3.0, 4.0, 2.0])
    assert svg.startswith("<svg") and svg.count("<polyline") == 2
    assert "<polyline" not in R.sparkline([None, 1.0])
    html = R.page("Rapport <x>", "sous-titre", [R.section("S", R.table(["a"], [["<b>"]]))], "https://h")
    assert "&lt;b&gt;" in html and "<b>" not in html and "Rapport &lt;x&gt;" in html
    assert R.duration(59) == "59 s" and R.duration(7260) == "2 h 01" and R.duration(90000) == "1 j 1 h"
    assert R.fmt(None) == "—" and R.fmt(0.5) == "0.5" and R.fmt(12345678.0) == "1.23e+07"
