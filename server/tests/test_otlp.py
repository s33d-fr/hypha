import base64
import json

import pytest

from hypha.ingest import otlp
from hypha.ingest.body import Window

NOW_MS = 1_800_000_000_000
NOW_NS = NOW_MS * 1_000_000
WIN = Window.around(NOW_MS)
TRACE_HEX = "5b8efff798038103d269b633813fc60c"
SPAN_HEX = "eee19b7ec3c1b174"
PARENT_HEX = "eee19b7ec3c1b173"


def _kv(key, **value):
    return {"key": key, "value": value}


TRACES_JSON = {"resourceSpans": [{
    "resource": {"attributes": [_kv("service.name", stringValue="api"), _kv("host.name", stringValue="web-01")]},
    "scopeSpans": [{"spans": [
        {"traceId": TRACE_HEX, "spanId": SPAN_HEX, "parentSpanId": "", "name": "GET /users/:id", "kind": 2,
         "startTimeUnixNano": str(NOW_NS), "endTimeUnixNano": str(NOW_NS + 25_000_000),
         "attributes": [_kv("http.route", stringValue="/users/:id"), _kv("http.status_code", intValue="500")],
         "status": {"code": 2, "message": "boom"},
         "events": [{"timeUnixNano": str(NOW_NS + 1_000_000), "name": "exception",
                     "attributes": [_kv("exception.type", stringValue="ValueError")]}]},
        {"traceId": TRACE_HEX, "spanId": PARENT_HEX, "parentSpanId": SPAN_HEX, "name": "SELECT",
         "kind": "SPAN_KIND_CLIENT", "startTimeUnixNano": str(NOW_NS), "endTimeUnixNano": str(NOW_NS + 5_000_000),
         "attributes": [_kv("db.statement", stringValue="SELECT * FROM users WHERE id = $1")]},
        {"traceId": "", "spanId": SPAN_HEX, "startTimeUnixNano": str(NOW_NS)},
        {"traceId": TRACE_HEX, "spanId": SPAN_HEX, "startTimeUnixNano": str(NOW_NS - 30 * 86400 * 10 ** 9)},
    ]}],
}]}


def test_traces_json():
    spans, dropped = otlp.convert_traces(TRACES_JSON, WIN)
    assert dropped == 2
    root, child = spans
    assert root["trace_id"] == TRACE_HEX and root["span_id"] == SPAN_HEX and root["parent_span_id"] is None
    assert root["service"] == "api" and root["hostname"] == "web-01"
    assert root["kind"] == "server" and root["status"] == "error" and root["status_message"] == "boom"
    assert root["resource"] == "/users/:id" and root["duration_ns"] == 25_000_000
    assert root["attributes"]["http.status_code"] == 500
    assert root["events"][0]["name"] == "exception" and root["events"][0]["ts"] == NOW_MS + 1
    assert child["kind"] == "client" and child["parent_span_id"] == SPAN_HEX
    assert child["resource"].startswith("SELECT * FROM users")


def test_traces_protobuf_roundtrip():
    from google.protobuf import json_format
    from opentelemetry.proto.collector.trace.v1.trace_service_pb2 import ExportTraceServiceRequest
    # Le JSON OTLP porte des identifiants hexadécimaux ; en protobuf ce sont des octets.
    doc = json.loads(json.dumps(TRACES_JSON))
    for sp in doc["resourceSpans"][0]["scopeSpans"][0]["spans"]:
        for k in ("traceId", "spanId", "parentSpanId"):
            if sp.get(k):
                sp[k] = base64.b64encode(bytes.fromhex(sp[k])).decode()
            if isinstance(sp.get("kind"), str):
                sp["kind"] = 3
    msg = json_format.ParseDict(doc, ExportTraceServiceRequest())
    decoded = otlp.decode("traces", msg.SerializeToString(), protobuf=True)
    spans, dropped = otlp.convert_traces(decoded, WIN)
    assert dropped == 2
    assert spans[0]["trace_id"] == TRACE_HEX and spans[0]["span_id"] == SPAN_HEX
    assert spans[0]["kind"] == "server" and spans[0]["status"] == "error"
    assert spans[1]["parent_span_id"] == SPAN_HEX and spans[1]["kind"] == "client"


def test_encode_response():
    assert otlp.encode_response("traces", protobuf=False, rejected=0) == b"{}"
    body = json.loads(otlp.encode_response("logs", protobuf=False, rejected=3))
    assert body["partialSuccess"]["rejectedLogRecords"] == 3
    from opentelemetry.proto.collector.metrics.v1.metrics_service_pb2 import ExportMetricsServiceResponse
    resp = ExportMetricsServiceResponse()
    resp.ParseFromString(otlp.encode_response("metrics", protobuf=True, rejected=2))
    assert resp.partial_success.rejected_data_points == 2


def test_decode_errors():
    with pytest.raises(otlp.OtlpError):
        otlp.decode("traces", b"\xff\xff\xff", protobuf=True)
    with pytest.raises(otlp.OtlpError):
        otlp.decode("traces", b"[1]", protobuf=False)


def test_norm_id():
    assert otlp.norm_id(TRACE_HEX.upper(), 16) == TRACE_HEX
    assert otlp.norm_id(base64.b64encode(bytes.fromhex(SPAN_HEX)).decode(), 8) == SPAN_HEX
    assert otlp.norm_id("0" * 32, 16) is None
    assert otlp.norm_id("xyz", 16) is None


def _metrics(metric):
    return {"resourceMetrics": [{
        "resource": {"attributes": [_kv("service.name", stringValue="api"), _kv("host.name", stringValue="web-01")]},
        "scopeMetrics": [{"metrics": [metric]}]}]}


def test_gauge_and_delta_sum():
    gauge = {"name": "queue.depth", "unit": "1", "gauge": {"dataPoints": [
        {"timeUnixNano": str(NOW_NS), "asInt": "42", "attributes": [_kv("Queue Name", stringValue="jobs")]}]}}
    out, dropped = otlp.convert_metrics(_metrics(gauge), WIN, otlp.CumulativeState())
    assert dropped == 0 and len(out) == 1
    p = out[0]
    assert p.name == "queue.depth" and p.type == "gauge" and p.value == 42.0 and p.unit is None
    assert p.tags == {"queue_name": "jobs", "service": "api", "host": "web-01"} and p.hostname == "web-01"
    delta = {"name": "jobs.done", "sum": {"aggregationTemporality": 1, "isMonotonic": True, "dataPoints": [
        {"timeUnixNano": str(NOW_NS), "asDouble": 5}]}}
    out, _ = otlp.convert_metrics(_metrics(delta), WIN, otlp.CumulativeState())
    assert out[0].type == "count" and out[0].value == 5


def test_cumulative_sum_becomes_rate():
    state = otlp.CumulativeState()

    def point(t_s, value):
        return _metrics({"name": "http.requests", "unit": "{request}", "sum": {
            "aggregationTemporality": 2, "isMonotonic": True,
            "dataPoints": [{"timeUnixNano": str(NOW_NS + t_s * 10 ** 9), "asInt": str(value)}]}})

    out, _ = otlp.convert_metrics(point(0, 100), WIN, state)
    assert out == []                                   # premier point : amorce
    out, _ = otlp.convert_metrics(point(10, 150), WIN, state)
    assert len(out) == 1 and out[0].type == "rate" and out[0].value == pytest.approx(5.0)
    out, _ = otlp.convert_metrics(point(20, 10), WIN, state)
    assert out == []                                   # remise à zéro du compteur
    out, _ = otlp.convert_metrics(point(30, 30), WIN, state)
    assert out[0].value == pytest.approx(2.0)


def test_histogram_delta_quantiles():
    hist = {"name": "http.server.duration", "unit": "ms", "histogram": {"aggregationTemporality": 1, "dataPoints": [
        {"timeUnixNano": str(NOW_NS), "count": "100", "sum": 2500.0,
         "explicitBounds": [10, 20, 50, 100], "bucketCounts": ["10", "40", "40", "10", "0"]}]}}
    out, _ = otlp.convert_metrics(_metrics(hist), WIN, otlp.CumulativeState())
    by = {p.name: p for p in out}
    assert by["http.server.duration.count"].value == 100 and by["http.server.duration.count"].type == "count"
    assert by["http.server.duration.sum"].value == 2500
    assert by["http.server.duration.avg"].value == 25
    assert by["http.server.duration.avg"].unit == "millisecond"
    # p50 : 50e valeur, fin du deuxième seau (10-20) ; p95 : milieu du quatrième (50-100).
    assert by["http.server.duration.p50"].value == pytest.approx(20.0)
    assert by["http.server.duration.p95"].value == pytest.approx(75.0)


def test_histogram_cumulative_uses_delta():
    state = otlp.CumulativeState()

    def point(t_s, counts):
        return _metrics({"name": "lat", "histogram": {"aggregationTemporality": 2, "dataPoints": [
            {"timeUnixNano": str(NOW_NS + t_s * 10 ** 9), "count": str(sum(counts)), "sum": float(sum(counts)),
             "explicitBounds": [1, 2], "bucketCounts": [str(c) for c in counts]}]}})

    assert otlp.convert_metrics(point(0, [10, 0, 0]), WIN, state)[0] == []
    out, _ = otlp.convert_metrics(point(10, [10, 0, 10]), WIN, state)
    by = {p.name: p.value for p in out}
    assert by["lat.count"] == 10                       # seulement les 10 nouvelles mesures
    assert by["lat.p50"] == pytest.approx(2.0)         # toutes au-delà de 2 (dernier seau, sans max connu)


def test_histogram_quantile_edges():
    assert otlp.histogram_quantile(0.5, [], [0]) is None
    assert otlp.histogram_quantile(0.5, [10], [4, 0], vmin=2) == pytest.approx(6.0)
    assert otlp.histogram_quantile(0.99, [10], [0, 4], vmax=30) == pytest.approx(29.8)
    assert otlp.exponential_quantile(0.5, 0, 0, 0, [0, 4]) == pytest.approx(3.0)


def test_logs_conversion():
    doc = {"resourceLogs": [{
        "resource": {"attributes": [_kv("service.name", stringValue="api"),
                                    _kv("deployment.environment", stringValue="prod")]},
        "scopeLogs": [{"logRecords": [
            {"timeUnixNano": str(NOW_NS), "severityNumber": 17, "body": {"stringValue": "échec"},
             "attributes": [_kv("user.id", intValue="7")], "traceId": TRACE_HEX, "spanId": SPAN_HEX},
            {"observedTimeUnixNano": str(NOW_NS), "severityText": "WARN",
             "body": {"kvlistValue": {"values": [_kv("a", intValue="1")]}}},
            {"timeUnixNano": str(NOW_NS - 30 * 86400 * 10 ** 9), "body": {"stringValue": "vieux"}},
        ]}]}]}
    logs, dropped = otlp.convert_logs(doc, WIN, NOW_MS)
    assert dropped == 1
    a, b = logs
    assert a["status"] == "error" and a["message"] == "échec" and a["trace_id"] == TRACE_HEX
    assert a["attributes"] == {"user.id": 7} and a["tags"] == {"env": "prod"} and a["source"] == "otlp"
    assert b["status"] == "warning" and json.loads(b["message"]) == {"a": 1}
