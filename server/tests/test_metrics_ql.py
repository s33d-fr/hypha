import json
from datetime import timedelta

import pytest

from hypha.errors import QueryError
from hypha.query import metrics_ql as mql
from hypha.util import ms_to_dt

from conftest import placeholders

DAY = 86400_000
NOW = 1_800_000_000_000


def test_parse_full_query():
    q = mql.parse("avg:system.cpu.user{host:web-01,env:prod} by {host}")
    assert q.space_agg == "avg" and q.metric == "system.cpu.user"
    assert [(f.key, f.value, f.negate, f.prefix) for f in q.filters] == [("host", "web-01", False, False),
                                                                          ("env", "prod", False, False)]
    assert q.group_by == ["host"] and q.rollup_fn is None


def test_parse_variants():
    q = mql.parse("max:system.disk.in_use{!mount:/boot,device:sd*} by {host,mount}.rollup(max, 300)")
    assert q.space_agg == "max" and q.group_by == ["host", "mount"]
    assert q.filters[0].negate and q.filters[0].value == "/boot"
    assert q.filters[1].prefix and q.filters[1].value == "sd"
    assert (q.rollup_fn, q.rollup_s) == ("max", 300)
    q = mql.parse("sum:container.mem.usage{*}.rollup(last) by {container_name}")
    assert q.filters == [] and q.rollup_fn == "last" and q.rollup_s is None and q.group_by == ["container_name"]
    q = mql.parse("system.load.1")
    assert q.space_agg == "avg" and q.filters == [] and q.group_by == []
    q = mql.parse('count:app.x{image:"ghcr.io/zot:v2, latest",}')
    assert q.filters[0].value == "ghcr.io/zot:v2, latest" and q.space_agg == "count"
    q = mql.parse("avg:system.cpu.usage{}")
    assert q.filters == []


@pytest.mark.parametrize("bad", [
    "", "median:system.cpu.user{*}", "avg:system.cpu.user{Host:web-01}", "avg:system cpu{*}",
    "avg:x{host}", "avg:x{host:a*b}", "avg:x{*} by host", "avg:x{*}.rollup(p99, 60)", "avg:x{*}.rollup(avg, 0)",
    "avg:x{$host}", 'avg:x{host:"web-01}', "avg:x{*}.rollup(avg,60).rollup(max,60)", "avg:x{host:}",
])
def test_parse_errors(bad):
    with pytest.raises(QueryError):
        mql.parse(bad)


def test_template_vars():
    assert mql.apply_template_vars("avg:x{$host} by {host}", {"host": "*"}) == "avg:x{} by {host}"
    assert mql.apply_template_vars("avg:x{$host,env:prod}", {"host": "host:web-01"}) == "avg:x{host:web-01,env:prod}"
    assert mql.parse(mql.apply_template_vars("avg:x{state:open,$host}", {"host": "*"})).filters[0].key == "state"


def test_series_sql_is_parameterized():
    q = mql.parse("avg:system.cpu.user{host:web-01,!env:dev,device:sd*} by {host}")
    sql, params = mql.series_sql(q, 7)
    assert placeholders(sql) == len(params)
    assert "web-01" not in sql and "sd" not in sql.replace("FROM metric_series", "")
    assert params[:2] == [7, "system.cpu.user"]
    assert json.loads(params[2]) == {"host": "web-01"}
    assert "NOT COALESCE(tags @> %s::jsonb, false)" in sql
    assert params[4:6] == ["device", "sd%"]


def test_series_sql_escapes_like():
    q = mql.parse("avg:x{path:/var/lib_50%*}")
    _, params = mql.series_sql(q, 1)
    assert params[3] == "/var/lib\\_50\\%%"


def test_choose_source():
    assert mql.choose_source(NOW - DAY, NOW, NOW) is mql.RAW
    assert mql.choose_source(NOW - 2 * DAY, NOW, NOW) is mql.RAW
    assert mql.choose_source(NOW - 3 * DAY, NOW, NOW) is mql.R5M
    assert mql.choose_source(NOW - 60 * DAY, NOW, NOW) is mql.R5M
    assert mql.choose_source(NOW - 61 * DAY, NOW, NOW) is mql.R1H
    # Une heure d'il y a trois semaines : le brut est purgé, on lit les 5 minutes.
    assert mql.choose_source(NOW - 21 * DAY, NOW - 21 * DAY + 3600_000, NOW) is mql.R5M
    assert mql.choose_source(NOW - 100 * DAY, NOW - 99 * DAY, NOW) is mql.R1H


def test_choose_interval_respects_max_points():
    assert mql.choose_interval(NOW - 3600_000, NOW, 300, mql.RAW) == 15
    assert mql.choose_interval(NOW - DAY, NOW, 300, mql.RAW) == 300
    assert mql.choose_interval(NOW - 7 * DAY, NOW, 300, mql.R5M) == 3600
    assert mql.choose_interval(NOW - 3600_000, NOW, 300, mql.R5M) == 300
    for span in (3600_000, DAY, 7 * DAY, 30 * DAY, 400 * DAY):
        for source in (mql.RAW, mql.R5M, mql.R1H):
            step = mql.choose_interval(NOW - span, NOW, 300, source)
            assert span / 1000 / step <= 300 and step >= source.min_step
    assert mql.choose_interval(NOW - 3600_000, NOW, 300, mql.RAW, rollup_s=600) == 600
    assert mql.choose_interval(NOW - DAY, NOW, 300, mql.RAW, rollup_s=60) == 300


def test_points_sql_whitelists_and_params():
    sql, params = mql.points_sql(mql.R5M, "avg", "sum", [1, 2], [0, 0], 300, ms_to_dt(NOW - DAY), ms_to_dt(NOW))
    assert placeholders(sql) == len(params)
    assert "FROM metric_rollup_5m" in sql and "sum(sum) / NULLIF(sum(cnt), 0)" in sql and "sum(v)" in sql
    assert params[2] == timedelta(seconds=300)
    sql, _ = mql.points_sql(mql.RAW, "last", "count", [1], [0], 15, ms_to_dt(NOW - 3600_000), ms_to_dt(NOW))
    assert "array_agg(value ORDER BY ts DESC)" in sql and "count(v)" in sql and "FROM metric_points" in sql
    with pytest.raises(KeyError):
        mql.points_sql(mql.RAW, "avg); DROP TABLE x; --", "sum", [1], [0], 15, ms_to_dt(NOW), ms_to_dt(NOW))


def test_group_series():
    rows = [{"id": 1, "tags": {"host": "a", "device": "sda"}, "unit": None},
            {"id": 2, "tags": {"host": "a", "device": "sdb"}, "unit": "percent"},
            {"id": 3, "tags": {"host": "b"}, "unit": "percent"}]
    groups, ids, idx, units = mql.group_series(rows, ["host"])
    assert groups == [{"host": "a"}, {"host": "b"}] and ids == [1, 2, 3] and idx == [0, 0, 1]
    assert units == ["percent", "percent"]
    groups, _, idx, _ = mql.group_series(rows, [])
    assert groups == [{}] and idx == [0, 0, 0]
