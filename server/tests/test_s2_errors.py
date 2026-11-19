"""§ 25 : extraction des erreurs, piles normalisées, empreintes, regroupement par lot."""
import json
from datetime import datetime, timedelta, timezone

from hypha.ingest import body as B
from hypha.ingest import error_tracking as ET
from hypha.ingest import extras

from conftest import placeholders

NOW = datetime(2026, 11, 18, 10, 30, tzinfo=timezone.utc)
TS = int(NOW.timestamp() * 1000)

PY = """Traceback (most recent call last):
  File "/opt/app/server.py", line 120, in handle
    return view(request)
  File "/opt/app/views.py", line 42, in view
    total = int(value)
ValueError: invalid literal for int() with base 10: 'abc'"""

PY_OTHER_LINES = PY.replace("line 120", "line 999").replace("line 42", "line 7").replace("'abc'", "'xyz'")

JAVA = """java.lang.IllegalStateException: pool épuisé (12 connexions)
\tat com.s33d.db.Pool.acquire(Pool.java:88)
\tat com.s33d.api.Handler.handle(Handler.java:31)
\tat java.base/java.lang.Thread.run(Thread.java:1583)"""

GO = """panic: runtime error: index out of range [3] with length 2

goroutine 17 [running]:
main.parse(0xc000012345, 0x2)
\t/app/main.go:42 +0x1d
main.main()
\t/app/main.go:10 +0x25"""

NODE = """TypeError: Cannot read properties of undefined (reading 'id')
    at getUser (/srv/app/users.js:12:18)
    at Layer.handle [as handle_request] (/srv/app/node_modules/express/lib/router/layer.js:95:5)"""


def log(message, status="error", attributes=None, service="api"):
    return B.log_row(1, 10, "web-01", TS, message, status, service, "journald", attributes or {}, {}, "a" * 32, None)


def test_stack_kinds_and_frames():
    assert ET.stack_kind(PY) == "python" and ET.stack_kind(JAVA) == "java"
    assert ET.stack_kind(GO) == "go" and ET.stack_kind(NODE) == "node"
    assert ET.stack_kind("erreur simple sans pile") is None
    assert ET.frames(PY) == ["/opt/app/views.py:view", "/opt/app/server.py:handle"]
    assert ET.frames(JAVA)[0] == "com.s33d.db.Pool.acquire(Pool.java)"
    assert ET.frames(GO) == ["main.parse /app/main.go", "main.main /app/main.go"]
    assert ET.frames(NODE)[0] == "getUser /srv/app/users.js"
    assert ET.error_type_message(PY, "python") == ("ValueError", "invalid literal for int() with base 10: 'abc'")
    assert ET.error_type_message(JAVA, "java")[0] == "java.lang.IllegalStateException"
    assert ET.error_type_message(GO, "go") == ("panic", "runtime error: index out of range [3] with length 2")
    assert ET.error_type_message(NODE, "node")[0] == "TypeError"


def test_fingerprint_ignores_lines_and_values():
    a = ET.from_log_row(log(PY), 0)
    b = ET.from_log_row(log(PY_OTHER_LINES), 1)
    assert a.fingerprint == b.fingerprint and a.error_type == "ValueError" and a.origin == "log"
    c = ET.from_log_row(log(PY, service="worker"), 2)
    assert c.fingerprint != a.fingerprint
    # Sans pile : le gabarit du message (les nombres et identifiants ne comptent pas).
    x = ET.from_log_row(log("db down", attributes={"error": {"kind": "Timeout", "message": "délai 30 s"}}), 0)
    y = ET.from_log_row(log("db down", attributes={"error": {"kind": "Timeout", "message": "délai 45 s"}}), 0)
    assert x.fingerprint == y.fingerprint and x.error_type == "Timeout" and x.message == "délai 30 s"


def test_log_rows_that_are_not_errors():
    assert ET.from_log_row(log(PY, status="warning"), 0) is None
    assert ET.from_log_row(log("connexion refusée"), 0) is None
    assert ET.from_log_row(log("x", attributes={"error": "chaîne"}), 0) is None
    flat = ET.from_log_row(log("x", attributes={"error.stack": JAVA}), 0)
    assert flat.error_type == "java.lang.IllegalStateException" and flat.stack == JAVA


def test_tag_log_row():
    r = ET.tag_log_row(log("x", attributes={"error": {"kind": "T"}}), "abc", 77)
    assert len(r) == len(B.LOG_COLUMNS) + 1 and r[-1] == 77
    assert json.loads(r[B.LOG_COLUMNS.index("attributes")]) == {"error": {"kind": "T", "fingerprint": "abc"}}
    r = ET.tag_log_row(log("x", attributes={"error": "chaîne"}), "abc")
    assert json.loads(r[B.LOG_COLUMNS.index("attributes")]) == {"error": "chaîne", "error.fingerprint": "abc"}


def test_spans_and_rum():
    span = {"status": "error", "service": "api", "name": "GET /", "trace_id": "b" * 32, "status_message": "boom",
            "events": [{"name": "exception", "attributes": {"exception.type": "KeyError",
                                                            "exception.message": "'id'", "exception.stacktrace": PY}}],
            "attributes": {}, "ts_ms": TS}
    o = ET.from_span(span, NOW, 10)
    assert o.origin == "trace" and o.error_type == "KeyError" and o.trace_id == "b" * 32 and o.host_id == 10
    assert ET.from_span({**span, "status": "ok"}, NOW, None) is None
    assert ET.from_span({**span, "events": [{"name": "log"}]}, NOW, None) is None
    occ = extras.span_occurrences([span], [10])
    assert len(occ) == 1 and span["attributes"]["error.fingerprint"] == o.fingerprint
    r = ET.from_rum("Uncaught TypeError: x is undefined", {"stack": NODE}, NOW, "boutique")
    assert r.origin == "rum" and r.service == "boutique" and r.error_type == "TypeError"
    rows = [(1, 5, NOW, "s", "/", "error", "boom", None, "{}", "UA", "FR"),
            (1, 5, NOW, "s", "/", "view", None, 120.0, "{}", "UA", "FR")]
    out, occs = extras.rum_occurrences(rows, "boutique")
    assert len(occs) == 1 and json.loads(out[0][8])["error.fingerprint"] == occs[0].fingerprint
    assert out[1] is rows[1]


def test_aggregate_and_upsert_sql():
    a = ET.from_log_row(log(PY), 0)
    later = ET.Occurrence(**{**a.__dict__, "ts": NOW + timedelta(hours=1), "index": 1, "host_id": 11})
    deltas = ET.aggregate([a, later], {0: 100, 1: 101})
    assert len(deltas) == 1
    d = deltas[0]
    assert d.count == 2 and d.first_seen == NOW and d.last_seen == NOW + timedelta(hours=1)
    assert d.log_id == 101 and d.host_id == 11 and len(d.hours) == 2
    assert placeholders(ET.UPSERT_ISSUES) == 13 and placeholders(ET.UPSERT_HITS) == 3
    assert "status = CASE WHEN i.status = 'resolved'" in " ".join(ET.UPSERT_ISSUES.split())


def test_upsert_runs_against_a_fake_connection():
    import asyncio

    calls = []

    class Cur:
        def __init__(self, rows):
            self.rows = rows

        async def fetchall(self):
            return self.rows

    class Conn:
        async def execute(self, sql, params=None):
            assert placeholders(sql) == len(params)
            calls.append((sql, params))
            if "INSERT INTO error_issues" in sql:
                return Cur([{"id": 5, "fingerprint": params[1][0]}])
            return Cur([])

    deltas = ET.aggregate([ET.from_log_row(log(PY), 0)])
    assert asyncio.run(ET.upsert(Conn(), 1, deltas)) == 1
    assert "INSERT INTO error_issue_hits" in calls[1][0] and calls[1][1][0] == [5]
