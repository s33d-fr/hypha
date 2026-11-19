import pytest

from hypha.monitors import evaluate as ev
from hypha.monitors.validate import normalize_monitor
from hypha.slo import compute as C


def ch(ts, group, frm, to):
    return C.Change(ts, group, frm, to)


def bad(intervals):
    return sum(b - a for a, b in C.merge(intervals))


# ── reconstruction des intervalles d'état ──────────────────────────────────────

def test_intervals_from_changes():
    inside = [ch(100, "*", "OK", "ALERT"), ch(300, "*", "ALERT", "OK"), ch(700, "*", "OK", "ALERT")]
    iv = C.alert_intervals([], inside, {"*": "ALERT"}, 0, 1000, 100)
    assert sorted(iv) == [(100, 300), (700, 1000)]
    assert C.sli_from_bad(bad(iv), 1000) == 50.0


def test_state_at_window_start():
    # Dernier changement antérieur à la fenêtre : le groupe y entre en ALERT.
    iv = C.alert_intervals([ch(-50, "*", "OK", "ALERT")], [ch(200, "*", "ALERT", "OK")], {"*": "OK"}, 0, 1000, 100)
    assert iv == [(0, 200)]
    # Sans changement antérieur : l'état de départ du premier changement de la fenêtre.
    assert C.alert_intervals([], [ch(400, "g", "ALERT", "OK")], {"g": "OK"}, 0, 1000, 100) == [(0, 400)]
    # Aucun changement : l'état courant vaut pour toute la fenêtre.
    assert C.alert_intervals([], [], {"g": "ALERT"}, 0, 1000, 100) == [(0, 1000)]
    assert C.alert_intervals([], [], {"g": "OK"}, 0, 1000, 100) == []


def test_worst_group_without_double_counting():
    inside = [ch(100, "a", "OK", "ALERT"), ch(300, "b", "OK", "ALERT"), ch(400, "a", "ALERT", "OK"),
              ch(600, "b", "ALERT", "OK")]
    iv = C.alert_intervals([], inside, {"a": "OK", "b": "OK"}, 0, 1000, 100)
    assert C.merge(iv) == [(100, 600)] and bad(iv) == 500


def test_warn_and_nodata_count_as_good():
    inside = [ch(100, "*", "OK", "WARN"), ch(200, "*", "WARN", "NODATA"), ch(300, "*", "NODATA", "ALERT")]
    assert C.alert_intervals([], inside, {"*": "ALERT"}, 0, 1000, 100) == [(300, 1000)]


def test_forgotten_and_inconsistent_groups():
    # Groupe oublié par le worker (plus d'état courant) : sorti d'alerte no_data_s après son dernier changement.
    assert C.alert_intervals([], [ch(500, "h", "OK", "ALERT")], {}, 0, 1000, 100) == [(500, 600)]
    assert C.alert_intervals([ch(-1000, "h", "OK", "ALERT")], [], {}, 0, 1000, 100) == []
    # État courant contraire au dernier changement (requête modifiée) : l'alerte s'arrête au changement.
    assert C.alert_intervals([], [ch(500, "h", "OK", "ALERT")], {"h": "OK"}, 0, 1000, 100) == []


def test_monitor_created_mid_window():
    merged = C.merge([(600, 700)])
    sli, sli_1h, sli_6h, history = C.monitor_figures(merged, 500, 1000, 100)
    assert sli == pytest.approx(80.0)                  # 100 s d'alerte sur 500 s d'existence
    assert sli_1h == pytest.approx(80.0) and sli_6h == pytest.approx(80.0)
    assert history == [[600_000, 100.0], [700_000, 50.0], [800_000, 66.6667], [900_000, 75.0], [1_000_000, 80.0]]


def test_burn_windows_use_recent_time_only():
    end = 30 * 86400
    merged = C.merge([(end - 1800, end)])              # alerte sur la dernière demi-heure
    sli, sli_1h, sli_6h, _ = C.monitor_figures(merged, 0, end, 86400)
    assert sli_1h == pytest.approx(50.0) and sli_6h == pytest.approx(100 * (1 - 1800 / 21600))
    assert sli == pytest.approx(100 * (1 - 1800 / end))


# ── budget, vitesses, état ─────────────────────────────────────────────────────

def test_error_budget_and_burn_rate():
    assert C.error_budget(99.95, 99.9) == pytest.approx(50.0)
    assert C.error_budget(100.0, 99.9) == pytest.approx(100.0)
    assert C.error_budget(99.0, 99.9) == -100.0       # borné en bas
    assert C.burn_rate(99.5, 99.9) == pytest.approx(5.0)
    assert C.burn_rate(100.0, 99.9) == 0.0


def test_state():
    assert C.slo_state(99.97, 99.9, 99.95) == "ok"
    assert C.slo_state(99.92, 99.9, 99.95) == "warning"
    assert C.slo_state(99.8, 99.9, 99.95) == "breached"
    assert C.slo_state(99.92, 99.9, None) == "ok"
    s = C.summarize(99.95, 99.5, 99.8, 99.9, None)
    assert s == {"sli": 99.95, "target": 99.9, "state": "ok", "error_budget_remaining": 50.0,
                 "burn_rate_1h": 5.0, "burn_rate_6h": 2.0}


def test_ratio_and_empty_totals():
    assert C.ratio_sli(0, 0) == 100.0                  # total = 0 : 100
    assert C.ratio_sli(999, 1000) == pytest.approx(99.9)
    assert C.ratio_sli(1200, 1000) == 100.0
    assert C.sli_from_bad(0, 0) == 100.0               # moniteur créé à l'instant
    history = C.ratio_history({0: 90.0, 3_600_000: 100.0}, {0: 100.0, 3_600_000: 100.0}, 0, 7_200_000, 3600)
    assert history == [[3_600_000, 90.0], [7_200_000, 95.0]]
    assert C.ratio_history({}, {}, 0, 7_200_000, 3600) == []


def test_history_times():
    assert C.history_times(0, 250, 100) == [100, 200, 250]
    assert C.history_times(0, 200, 100) == [100, 200]
    assert C.history_times(10, 10, 100) == []


# ── moniteur de type slo ───────────────────────────────────────────────────────

def test_slo_monitor_validation_and_state():
    m = normalize_monitor({"name": "budget API", "type": "slo", "query": {"slo_id": 3, "on": "budget"},
                           "thresholds": {"critical": 10, "warning": 25}})
    assert m["query"] == {"slo_id": 3, "on": "budget", "window": "1h", "comparator": "<"}
    assert ev.threshold_state(None, 5.0, m["thresholds"], "<") == ev.ALERT
    assert ev.threshold_state(None, 20.0, m["thresholds"], "<") == ev.WARN
    m = normalize_monitor({"name": "vitesse", "type": "slo", "query": {"slo_id": 3, "on": "burn_rate", "window": "6h"},
                           "thresholds": {"critical": 14.4, "warning": 6}})
    assert m["query"]["comparator"] == ">"
    for bad_query, thresholds in (({"slo_id": 3, "on": "x"}, {"critical": 1}),
                                  ({"slo_id": 3, "on": "burn_rate", "window": "2h"}, {"critical": 1}),
                                  ({"on": "budget"}, {"critical": 1}),
                                  ({"slo_id": 3, "on": "budget"}, {}),
                                  ({"slo_id": 3, "on": "budget"}, {"critical": 30, "warning": 10})):
        with pytest.raises(ValueError):
            normalize_monitor({"name": "x", "type": "slo", "query": bad_query, "thresholds": thresholds})
