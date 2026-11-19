"""§ 30 : formules et fonctions de /metrics/query (analyseur sûr, séries alignées, prévision, anomalies)."""
import asyncio

import pytest

from hypha.errors import QueryError
from hypha.query import compose
from hypha.query import formulas as FX
from hypha.query import metrics_ql as mql
from hypha.query import series_fn as F
from hypha.util import bucket_floor

from conftest import placeholders

NOW = 1_800_000_000_000
HOUR = 3600_000


# ── analyse ────────────────────────────────────────────────────────────────────

def test_parse_query_with_functions():
    n = FX.parse_query("avg:system.cpu.usage{host:web-01} by {host}")
    assert FX.is_plain(n) and n.mq.metric == "system.cpu.usage"
    n = FX.parse_query("timeshift(avg:system.cpu.usage{host:web-01,env:prod}, -604800)")
    assert isinstance(n, FX.Call) and n.name == "timeshift" and n.args[1].value == -604800
    assert n.args[0].mq.filters[1].key == "env"
    n = FX.parse_query("top(per_second(sum:logs.x{*} by {service}.rollup(sum, 60)), 5, 'max', 'desc')")
    assert n.name == "top" and n.args[0].name == "per_second" and n.args[0].args[0].mq.rollup_s == 60
    assert [a.value for a in n.args[1:]] == [5, "max", "desc"]
    n = FX.parse_query("top(avg:x{*} by {host}, 3)")
    assert [a.value for a in n.args[1:]] == [3, "mean", "desc"]
    n = FX.parse_query("anomalies(avg:x{*}, 'agile')")
    assert n.args[2].value == 2.0
    assert FX.leaf_metric(FX.parse_query("abs(ewma(avg:y{*}, 4))")).metric == "y"


@pytest.mark.parametrize("bad", [
    "abs(avg:x{*}) + 1", "abs(avg:x{*}", "fill(avg:x{*}, 'souvent')", "moving_avg(avg:x{*})",
    "moving_avg(avg:x{*}, 2.5)", "moving_avg(avg:x{*}, 0)", "timeshift(avg:x{*}, 'hier')", "top(avg:x{*}, 500)",
    "forecast(avg:x{*}, 'linear', 10)", "outliers(avg:x{*}, 'dbscan')", "abs()", "median:x", "",
])
def test_parse_query_errors(bad):
    with pytest.raises(QueryError):
        FX.parse_query(bad)


def test_parse_formula():
    n = FX.parse_formula("a / b * 100")
    assert isinstance(n, FX.BinOp) and n.op == "*" and n.left.op == "/" and n.right.value == 100.0
    assert FX.refs(n) == {"a", "b"}
    n = FX.parse_formula("moving_avg(a / b, 5) - -2")
    assert n.left.name == "moving_avg" and isinstance(n.right, FX.Neg)
    n = FX.parse_formula("(a + b) / 2")
    assert n.op == "/" and n.left.op == "+"
    for bad in ("a +", "a $ b", "'x'", "foo(a)", "a b", "", "(a", "abs()", "abs(1)", "1e400 * a", "a" * 3000):
        with pytest.raises(QueryError):
            FX.parse_formula(bad)
    with pytest.raises(QueryError):
        FX.parse_formula(" + ".join(["a"] * 120))


def test_canonical_ignores_spacing_and_filter_order():
    a = FX.canonical(FX.parse_query("timeshift( avg:x{b:2,a:1} by {host} , -3600 )"))
    b = FX.canonical(FX.parse_query("timeshift(avg:x{a:1,b:2} by {host}, -3600)"))
    assert a == b
    assert FX.canonical(FX.parse_formula("a/b")) == FX.canonical(FX.parse_formula(" a / b "))
    assert a != FX.canonical(FX.parse_query("avg:x{a:1,b:2} by {host}"))


def test_metrics_in_filter():
    q = mql.parse("avg:system.cpu.usage{host IN (web-01, \"web 02\"),env:prod,!dc NOT IN (a)} by {host}")
    f = q.filters[0]
    assert f.values == ["web-01", "web 02"] and not f.negate
    assert q.filters[2].values == ["a"] and q.filters[2].negate is False      # ! et NOT s'annulent
    q2 = mql.parse("avg:x{host NOT IN (a,b)}")
    assert q2.filters[0].negate and q2.filters[0].values == ["a", "b"]
    sql, params = mql.series_sql(q, 1)
    assert placeholders(sql) == len(params)
    assert "(tags @> %s::jsonb OR tags @> %s::jsonb)" in sql
    with pytest.raises(QueryError):
        mql.parse("avg:x{host IN ()}")


# ── fonctions de séries ────────────────────────────────────────────────────────

def test_series_functions():
    v = [1.0, None, 3.0, 6.0]
    assert F.cumsum(v) == [1.0, None, 4.0, 10.0]
    assert F.per_second([30.0, None], 15) == [2.0, None]
    assert F.derivative([1.0, 3.0, None, 2.0], 2) == [None, 1.0, None, -0.5]
    assert F.derivative([5.0, 3.0, 4.0], 1, non_negative=True) == [None, None, 1.0]
    assert F.moving_avg([1.0, 2.0, 3.0, None, 5.0], 2) == [1.0, 1.5, 2.5, None, 5.0]
    assert F.rolling_median([1.0, 9.0, 2.0], 3) == [1.0, 5.0, 2.0]
    assert F.ewma([10.0, None, 20.0], 3) == [10.0, None, 15.0]
    assert F.fill([None, 1.0, None, None, 4.0, None], "linear") == [None, 1.0, 2.0, 3.0, 4.0, None]
    assert F.fill([None, 1.0, None], "last") == [None, 1.0, 1.0]
    assert F.fill([None, 1.0], "zero") == [0.0, 1.0]
    assert F.clamp([-1.0, 5.0, 11.0], lo=0, hi=10) == [0.0, 5.0, 10.0]
    assert F.log_base([8.0, 0.0, -1.0], 2) == [3.0, None, None]
    assert F.top_indices([[1.0], [5.0], [None], [3.0]], 2, "max", "desc") == [1, 3]
    assert F.top_indices([[1.0], [5.0], [3.0]], 1, "mean", "asc") == [0]


def test_theil_sen_is_robust_and_forecast_extends():
    xs = list(range(20))
    ys = [2.0 * x + 1 for x in xs]
    ys[5] = 500.0                                                    # valeur aberrante
    slope, intercept = F.theil_sen(xs, ys)
    assert slope == pytest.approx(2.0) and intercept == pytest.approx(1.0)
    times = [NOW + i * 60_000 for i in range(30)]
    values = [10.0 + i for i in range(30)]
    future, bounds = F.forecast(times, values, 600, 60)
    assert future[0][0] == times[-1] + 60_000 and future[-1][0] == times[-1] + 600_000
    assert future[0][1] == pytest.approx(40.0) and bounds[0][1] <= future[0][1] <= bounds[0][2]
    assert F.forecast(times[:2], values[:2], 600, 60) == ([], [])
    day = 86400_000
    times = [NOW + i * HOUR for i in range(24 * 4)]
    values = [100.0 + (50.0 if (t // 1000 % 86400) // 3600 in (12, 13) else 0.0) for t in times]
    fut, _ = F.forecast(times, values, 2 * 86400, 3600, "seasonal")
    noon = [v for t, v in fut if (t // 1000 % 86400) // 3600 == 12]
    night = [v for t, v in fut if (t // 1000 % 86400) // 3600 == 3]
    assert noon and night and min(noon) > max(night) + 30
    assert day > 0


def test_anomaly_bounds_and_outliers():
    values = [10.0, 11.0, 9.0, 10.0, 10.5, 9.5, 10.0, 50.0]
    b = F.anomaly_bounds_basic(values, 2.0, 6)
    assert b[0] is None and b[-1] is not None and not b[-1][0] <= 50.0 <= b[-1][1]
    history = [[10.0, 20.0], [11.0, 21.0], [9.0, 19.0], [10.0, None]]
    agile = F.anomaly_bounds_agile(history, 2.0)
    assert agile[0][0] < 10.0 < agile[0][1] and agile[1][0] < 20.0 < agile[1][1]
    assert F.anomaly_bounds_agile([[1.0], [None]], 2.0) == [None]
    scores = F.outlier_scores([[1.0] * 10, [1.1] * 10, [0.9] * 10, [9.0] * 10], 3.0)
    assert scores[3] == 1.0 and scores[0] == 0.0


def test_binop_alignment():
    S = FX.Series
    a = [S({"host": "web-01"}, [10.0, 20.0], "byte"), S({"host": "web-02"}, [5.0, None], "byte")]
    b = [S({"host": "web-01"}, [2.0, 0.0], "byte"), S({"host": "web-03"}, [1.0, 1.0], "byte")]
    out = FX.binop("/", a, b)
    assert len(out) == 1 and out[0].group == {"host": "web-01"} and out[0].values == [5.0, None]
    assert out[0].unit is None
    total = [S({}, [100.0, 200.0])]
    out = FX.binop("/", a, total)
    assert [s.values for s in out] == [[0.1, 0.1], [0.05, None]]
    out = FX.binop("*", a, 100.0)
    assert out[0].values == [1000.0, 2000.0] and out[0].unit == "byte"
    assert FX.binop("+", a, a)[0].unit == "byte"
    disks = [S({"host": "web-01", "device": "sda"}, [1.0, 1.0]), S({"host": "web-01", "device": "sdb"}, [2.0, 2.0])]
    out = FX.binop("+", disks, a)
    assert sorted(s.group["device"] for s in out) == ["sda", "sdb"] and out[0].values[0] in (11.0, 12.0)
    assert FX.binop("+", [S({"service": "x"}, [1.0])], [S({"host": "y"}, [1.0])]) == []
    assert FX.negate(a)[0].values == [-10.0, -20.0]


# ── exécution complète (lecture de la base simulée) ────────────────────────────

class FakeExec:
    def __init__(self):
        self.calls = []

    async def __call__(self, conn, org_id, mq, from_ms, to_ms, source, step_s):
        self.calls.append((mq.metric, from_ms, to_ms, step_s))
        first = bucket_floor(from_ms, step_s)
        buckets = list(range(first, to_ms, step_s * 1000))

        def series(group, fn, unit="percent"):
            return {"group": group if mq.group_by else {}, "unit": unit, "points": [[b, fn(b)] for b in buckets]}
        if mq.metric == "m.a":
            return [series({"host": "web-01"}, lambda b: 10.0), series({"host": "web-02"}, lambda b: 20.0)] \
                if mq.group_by else [series({}, lambda b: 15.0)]
        if mq.metric == "m.b":
            return [series({"host": "web-01"}, lambda b: 5.0), series({"host": "web-02"}, lambda b: 4.0)]
        if mq.metric == "m.clock":
            return [series({}, lambda b: float(b // 1000 % 86400))]
        if mq.metric == "m.line":
            return [series({}, lambda b: (b - NOW) / 60_000)]
        return []


@pytest.fixture()
def fake(monkeypatch):
    f = FakeExec()
    monkeypatch.setattr(compose.metrics_exec, "execute", f)
    return f


def run(queries, formulas=(), start=NOW - HOUR, end=NOW, max_points=60):
    return asyncio.run(compose.run(None, 1, start, end, max_points, NOW, list(queries), list(formulas)))


def test_plain_queries_keep_their_format(fake):
    out = run([("a", "avg:m.a{*} by {host}")])
    assert out["interval_s"] == 60 and [s["group"] for s in out["series"]] == [{"host": "web-01"}, {"host": "web-02"}]
    assert set(out["series"][0]) == {"query", "metric", "group", "unit", "points"}
    out = run([("a", "avg:m.a{*} by {host}.rollup(avg, 300)")])
    assert out["series"][0]["interval_s"] == 300


def test_formula_ratio(fake):
    out = run([("a", "avg:m.a{*} by {host}"), ("b", "avg:m.b{*} by {host}")], [("f", "a / b * 100", "taux")])
    f = [s for s in out["series"] if s["query"] == "f"]
    assert {tuple(s["group"].items()): s["points"][0][1] for s in f} == {(("host", "web-01"),): 200.0,
                                                                         (("host", "web-02"),): 500.0}
    assert f[0]["alias"] == "taux" and f[0]["formula"] == "a / b * 100" and f[0]["metric"] == "taux"
    assert len(fake.calls) == 2                                        # a et b lus une seule fois


def test_timeshift_realigns(fake):
    out = run([("a", "timeshift(avg:m.clock{*}, -3600)")])
    pts = out["series"][0]["points"]
    assert fake.calls[0][1] == NOW - 2 * HOUR and fake.calls[0][2] == NOW - HOUR
    # La valeur au seau t est celle relevée une heure plus tôt.
    t, v = pts[5]
    assert v == float((t - HOUR) // 1000 % 86400)


def test_forecast_and_anomalies_and_outliers(fake):
    out = run([("a", "forecast(avg:m.line{*}, 'linear', 600)")])
    past = [s for s in out["series"] if not s.get("future")]
    future = [s for s in out["series"] if s.get("future")]
    assert len(past) == 1 and len(future) == 1
    assert future[0]["points"][0][0] > past[0]["points"][-1][0] and len(future[0]["bounds"][0]) == 3
    assert future[0]["points"][-1][1] == pytest.approx((future[0]["points"][-1][0] - NOW) / 60_000, abs=1e-6)
    out = run([("a", "anomalies(avg:m.clock{*}, 'basic', 2)")])
    assert "bounds" in out["series"][0] and len(out["series"][0]["bounds"]) == len(out["series"][0]["points"])
    fake.calls.clear()
    out = run([("a", "anomalies(avg:m.clock{*}, 'agile', 3)")])
    assert len(fake.calls) == 8                                        # la plage et les 7 jours précédents
    assert out["series"][0]["bounds"][10][1] is not None
    out = run([("a", "outliers(avg:m.a{*} by {host}, 'mad', 3)")])
    assert all("outlier" in s for s in out["series"])
    out = run([("a", "top(avg:m.a{*} by {host}, 1, 'max', 'desc')")])
    assert [s["group"] for s in out["series"]] == [{"host": "web-02"}]


def test_compose_errors(fake):
    for queries, formulas in (([("a", "avg:m.a{*}")], [("f", "a / z", None)]),
                              ([("a", "avg:m.a{*}")], [("a", "a * 2", None)]),
                              ([("a", "avg:m.a{*}")], [("f", "2 * 3", None)]),
                              ([("a", "median:m.a{*}")], [])):
        with pytest.raises(QueryError):
            run(queries, formulas)
