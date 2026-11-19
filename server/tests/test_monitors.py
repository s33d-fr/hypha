from datetime import datetime, timedelta, timezone

import pytest

from hypha.monitors import evaluate as ev
from hypha.monitors.validate import normalize_monitor

T0 = datetime(2026, 11, 18, 12, 0, tzinfo=timezone.utc)
TH = {"critical": 90, "warning": 80, "critical_recovery": 85, "warning_recovery": 75}


def test_synthetic_window_covers_two_runs():
    assert ev.synthetic_window(600, 300) == 660
    assert ev.synthetic_window(600, 21600) == 43260        # DNS de messagerie, toutes les 6 h
    assert ev.synthetic_window(3600, 60) == 3600           # la valeur posée reste un plancher
    assert ev.synthetic_window(600, None) == 600           # test supprimé : fenêtre du moniteur


def test_threshold_state_with_hysteresis():
    s = ev.threshold_state
    assert s(None, 50, TH, ">") == ev.OK
    assert s(None, 82, TH, ">") == ev.WARN
    assert s(None, 95, TH, ">") == ev.ALERT
    assert s(ev.ALERT, 87, TH, ">") == ev.ALERT       # pas encore sous critical_recovery
    assert s(ev.ALERT, 85, TH, ">") == ev.WARN        # sous le retour critique, encore au-dessus de warning_recovery
    assert s(ev.WARN, 77, TH, ">") == ev.WARN
    assert s(ev.WARN, 75, TH, ">") == ev.OK
    assert s(ev.OK, 87, TH, ">") == ev.WARN           # 87 ne franchit pas critical sans alerte préalable
    low = {"critical": 10, "critical_recovery": 20}
    assert s(None, 5, low, "<") == ev.ALERT
    assert s(ev.ALERT, 15, low, "<") == ev.ALERT
    assert s(ev.ALERT, 20, low, "<") == ev.OK
    assert s(None, 90, {"critical": 90}, ">=") == ev.ALERT


def test_check_status_mapping():
    assert ev.check_status_state(0) == ev.OK
    assert ev.check_status_state(1) == ev.WARN
    assert ev.check_status_state(2) == ev.ALERT
    assert ev.check_status_state(3) == ev.WARN
    assert ev.worst_check_status([0, 3, 1]) == 1
    assert ev.worst_check_status([3, 2]) == 2


def test_consecutive_and_worst():
    assert ev.consecutive_failures([False, False, True, False]) == 2
    assert ev.consecutive_failures([True, False]) == 0
    assert ev.worst([ev.OK, ev.NODATA, ev.WARN]) == ev.WARN
    assert ev.worst([]) == ev.NODATA


def test_render():
    msg = ev.render("{{name}} {{ state }} sur {{group}} = {{value}}", {"name": "CPU", "state": "ALERT",
                                                                         "group": "host:web-01", "value": "95"})
    assert msg == "CPU ALERT sur host:web-01 = 95"


def prev(state, minutes_ago=1, value=1.0, notified=None):
    t = T0 - timedelta(minutes=minutes_ago)
    return ev.PrevState(state, value, t, t, notified)


def test_plan_new_group_ok_is_silent():
    p = ev.plan({}, {"host:a": (ev.OK, 10.0)}, T0, 600, False, 0, False)
    assert p.transitions == [] and len(p.upserts) == 1


def test_plan_new_group_alert_notifies():
    p = ev.plan({}, {"host:a": (ev.ALERT, 95.0)}, T0, 600, False, 0, False)
    assert p.transitions == [("host:a", ev.OK, ev.ALERT, 95.0, True)]
    group, state, value, since, last_eval, last_notified = p.upserts[0]
    assert since == T0 and last_notified == T0


def test_plan_transition_and_recovery():
    p = ev.plan({"*": prev(ev.ALERT)}, {"*": (ev.OK, 1.0)}, T0, 600, False, 0, False)
    assert p.transitions == [("*", ev.ALERT, ev.OK, 1.0, True)]


def test_plan_muted_records_but_does_not_notify():
    p = ev.plan({"*": prev(ev.OK, notified=None)}, {"*": (ev.ALERT, 99.0)}, T0, 600, False, 0, True)
    assert p.transitions == [("*", ev.OK, ev.ALERT, 99.0, False)]
    assert p.upserts[0][5] is None


def test_plan_steady_state_keeps_since():
    old = prev(ev.ALERT, minutes_ago=30)
    p = ev.plan({"*": old}, {"*": (ev.ALERT, 97.0)}, T0, 600, False, 0, False)
    assert p.transitions == [] and p.renotify == []
    assert p.upserts[0][3] == old.since and p.upserts[0][4] == T0


def test_plan_renotify():
    old = prev(ev.ALERT, minutes_ago=30, notified=T0 - timedelta(minutes=30))
    p = ev.plan({"*": old}, {"*": (ev.ALERT, 97.0)}, T0, 600, False, 900, False)
    assert p.renotify == [("*", ev.ALERT, 97.0)] and p.upserts[0][5] == T0
    p = ev.plan({"*": old}, {"*": (ev.ALERT, 97.0)}, T0, 600, False, 3600, False)
    assert p.renotify == []
    p = ev.plan({"*": old}, {"*": (ev.ALERT, 97.0)}, T0, 600, False, 900, True)
    assert p.renotify == []


def test_plan_no_data():
    recent = prev(ev.ALERT, minutes_ago=5)
    stale = prev(ev.ALERT, minutes_ago=15)
    # Muet depuis moins de no_data_s : rien ne bouge.
    p = ev.plan({"host:a": recent}, {}, T0, 600, True, 0, False)
    assert p.upserts == [] and p.transitions == [] and p.deletes == []
    # Muet depuis plus longtemps, notify_no_data : NODATA notifié, last_eval conservé.
    p = ev.plan({"host:a": stale}, {"host:a": None}, T0, 600, True, 0, False)
    assert p.transitions == [("host:a", ev.ALERT, ev.NODATA, 1.0, True)]
    assert p.upserts[0][4] == stale.last_eval
    # Sans notify_no_data : le groupe disparu est oublié sans bruit.
    p = ev.plan({"host:a": stale}, {}, T0, 600, False, 0, False)
    assert p.deletes == ["host:a"] and p.transitions == []
    # Déjà NODATA : on n'y revient pas à chaque passage.
    p = ev.plan({"host:a": prev(ev.NODATA, minutes_ago=60)}, {}, T0, 600, True, 0, False)
    assert p.upserts == [] and p.transitions == []
    # Retour des données après NODATA.
    p = ev.plan({"host:a": prev(ev.NODATA, minutes_ago=60)}, {"host:a": (ev.OK, 3.0)}, T0, 600, True, 0, False)
    assert p.transitions == [("host:a", ev.NODATA, ev.OK, 3.0, True)]


def test_recovered_groups():
    stuck = {"host:dns-01,unit:certbot.service": prev(ev.WARN, minutes_ago=5),
             "host:edge-01,unit:logrotate.service": prev(ev.ALERT, minutes_ago=5),
             "host:web-01,unit:nginx.service": prev(ev.ALERT, minutes_ago=5),
             "host:web-02,unit:x.service": prev(ev.OK, minutes_ago=5)}
    measured = {"host:edge-01,unit:logrotate.service": (ev.ALERT, 2.0)}
    # dns-01 émet encore sans citer l'unité : rétablie. edge-01 la cite encore. web-01 est muet.
    # web-02 est déjà OK.
    got = ev.recovered_groups(stuck, measured, {"dns-01", "edge-01", "web-02"})
    assert got == ["host:dns-01,unit:certbot.service"]
    assert ev.recovered_groups(stuck, measured, set()) == []


BASE = {"name": "CPU", "type": "metric",
        "query": {"q": "avg:system.cpu.usage{*} by {host}", "window_s": 300, "aggregation": "avg", "comparator": ">"},
        "thresholds": {"critical": 90, "warning": 80, "critical_recovery": 85}}


def test_normalize_monitor_defaults():
    m = normalize_monitor(BASE)
    assert m["no_data_s"] == 600 and m["renotify_s"] == 0 and m["enabled"] and m["channel_ids"] == []
    assert m["thresholds"] == {"critical": 90.0, "warning": 80.0, "critical_recovery": 85.0}
    m = normalize_monitor({"name": "Hôtes", "type": "host", "query": {"tags": {"env": "prod"}}})
    assert m["query"] == {"tags": {"env": "prod"}} and m["thresholds"] == {}
    m = normalize_monitor({"name": "Units", "type": "service_check", "query": {"check": "systemd.unit"}})
    assert m["query"]["by"] == ["host"] and m["query"]["window_s"] == 300
    m = normalize_monitor({"name": "Services", "type": "service_check",
                           "query": {"check": "windows.service", "by": ["host", "windows_service"]}})
    assert m["query"]["by"] == ["host", "windows_service"]
    m = normalize_monitor({"name": "Erreurs", "type": "log", "query": {"q": "status:error service:nginx"},
                           "thresholds": {"critical": 10}})
    assert m["query"]["comparator"] == ">" and m["query"]["by"] == []
    m = normalize_monitor({"name": "Site", "type": "synthetic", "query": {"test_id": 12, "failures": 2}})
    assert m["query"] == {"test_id": 12, "failures": 2}
    m = normalize_monitor({"name": "API", "type": "apm", "query": {"service": "api", "metric": "error_rate"},
                           "thresholds": {"critical": 0.05}})
    assert m["query"]["window_s"] == 300


@pytest.mark.parametrize("patch", [
    {"type": "nope"},
    {"name": ""},
    {"query": {"q": "median:x{*}"}},
    {"query": dict(BASE["query"], comparator="=")},
    {"query": dict(BASE["query"], window_s=10)},
    {"thresholds": {}},
    {"thresholds": {"critical": 90, "warning": 95}},
    {"thresholds": {"critical": 90, "critical_recovery": 95}},
    {"thresholds": {"critical": "90"}},
    {"thresholds": {"critical": 90, "bogus": 1}},
    {"renotify_s": 10},
    {"channel_ids": ["1"]},
])
def test_normalize_monitor_errors(patch):
    with pytest.raises(ValueError):
        normalize_monitor({**BASE, **patch})


def test_thresholds_below_comparator():
    m = normalize_monitor({**BASE, "query": dict(BASE["query"], comparator="<"),
                           "thresholds": {"critical": 10, "warning": 20, "critical_recovery": 15}})
    assert m["thresholds"]["critical"] == 10
    with pytest.raises(ValueError):
        normalize_monitor({**BASE, "query": dict(BASE["query"], comparator="<"),
                           "thresholds": {"critical": 10, "warning": 5}})
