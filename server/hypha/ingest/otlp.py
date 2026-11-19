"""Conversion OTLP/HTTP (protobuf ou JSON) vers les lignes HYPHA (contrat § 3.9). Aucun accès à la base.

Le protobuf est d'abord rendu sous la forme JSON d'OTLP (camelCase), puis un seul chemin convertit.
Deux écarts entre les formes sont recollés ici : en JSON OTLP les identifiants de trace sont en
hexadécimal, en protobuf (MessageToDict) ils sortent en base64 ; les entiers 64 bits peuvent
arriver en chaîne dans les deux cas.
"""
import base64
import binascii
import json
import math
import re
import time
from dataclasses import dataclass

from ..util import METRIC_NAME_RE, clean_tags, clean_text, tags_hash
from .body import Window, normalize_log_status

KINDS = {1: "internal", 2: "server", 3: "client", 4: "producer", 5: "consumer"}
_HEX_RE = re.compile(r"^[0-9a-f]+$")
RESOURCE_KEYS = ("http.route", "url.path", "db.statement")
MAX_ATTRS = 128

_UNITS = {"ms": "millisecond", "s": "second", "us": "microsecond", "ns": "nanosecond",
          "By": "byte", "By/s": "byte_per_second", "%": "percent", "1": None,
          "{request}": "count", "{requests}": "count"}


class OtlpError(ValueError):
    pass


# ── décodage ───────────────────────────────────────────────────────────────────

def _request_class(signal: str):
    if signal == "traces":
        from opentelemetry.proto.collector.trace.v1 import trace_service_pb2 as m
        return m.ExportTraceServiceRequest, m.ExportTraceServiceResponse
    if signal == "metrics":
        from opentelemetry.proto.collector.metrics.v1 import metrics_service_pb2 as m
        return m.ExportMetricsServiceRequest, m.ExportMetricsServiceResponse
    if signal == "logs":
        from opentelemetry.proto.collector.logs.v1 import logs_service_pb2 as m
        return m.ExportLogsServiceRequest, m.ExportLogsServiceResponse
    raise ValueError(signal)


def decode(signal: str, data: bytes, protobuf: bool) -> dict:
    if protobuf:
        from google.protobuf import json_format
        from google.protobuf.message import DecodeError
        req_cls, _ = _request_class(signal)
        msg = req_cls()
        try:
            msg.ParseFromString(data)
        except DecodeError as e:
            raise OtlpError(f"protobuf OTLP illisible : {e}")
        return json_format.MessageToDict(msg, use_integers_for_enums=True)
    try:
        doc = json.loads(data)
    except (ValueError, UnicodeDecodeError) as e:
        raise OtlpError(f"JSON OTLP invalide : {e}")
    if not isinstance(doc, dict):
        raise OtlpError("le corps OTLP doit être un objet")
    return doc


def encode_response(signal: str, protobuf: bool, rejected: int) -> bytes:
    field = {"traces": "rejected_spans", "metrics": "rejected_data_points", "logs": "rejected_log_records"}[signal]
    if protobuf:
        _, resp_cls = _request_class(signal)
        resp = resp_cls()
        if rejected:
            setattr(resp.partial_success, field, rejected)
            resp.partial_success.error_message = "données hors fenêtre de temps ou invalides"
        return resp.SerializeToString()
    if not rejected:
        return b"{}"
    camel = re.sub(r"_([a-z])", lambda m: m.group(1).upper(), field)
    return json.dumps({"partialSuccess": {camel: rejected,
                                          "errorMessage": "données hors fenêtre de temps ou invalides"}}).encode()


# ── valeurs OTLP ───────────────────────────────────────────────────────────────

def to_int(value, default: int = 0) -> int:
    try:
        return int(value)
    except (TypeError, ValueError):
        return default


def to_float(value) -> float | None:
    try:
        f = float(value)
    except (TypeError, ValueError):
        return None
    return f if math.isfinite(f) else None


def any_value(v):
    if not isinstance(v, dict):
        return None
    if "stringValue" in v:
        return v["stringValue"]
    if "boolValue" in v:
        return bool(v["boolValue"])
    if "intValue" in v:
        return to_int(v["intValue"])
    if "doubleValue" in v:
        return to_float(v["doubleValue"])
    if "arrayValue" in v:
        return [any_value(x) for x in (v["arrayValue"] or {}).get("values", [])[:100]]
    if "kvlistValue" in v:
        return attributes((v["kvlistValue"] or {}).get("values"))
    if "bytesValue" in v:
        return v["bytesValue"]
    return None


def attributes(items) -> dict:
    out = {}
    for kv in (items or [])[:MAX_ATTRS]:
        if isinstance(kv, dict) and isinstance(kv.get("key"), str):
            out[kv["key"]] = any_value(kv.get("value"))
    return out


def norm_id(value, nbytes: int) -> str | None:
    """Identifiant de trace (16 octets) ou de span (8 octets) en hexadécimal minuscule."""
    if not value or not isinstance(value, str):
        return None
    s = value.strip()
    low = s.lower()
    if len(low) == 2 * nbytes and _HEX_RE.match(low):
        return None if set(low) == {"0"} else low
    try:
        raw = base64.b64decode(s, validate=True)
    except (binascii.Error, ValueError):
        return None
    if len(raw) != nbytes or raw == b"\x00" * nbytes:
        return None
    return raw.hex()


def _resource(container: dict) -> tuple[dict, str, str | None]:
    rattrs = attributes((container.get("resource") or {}).get("attributes"))
    service = clean_text(str(rattrs.get("service.name") or "unknown_service"), 200)
    hostname = rattrs.get("host.name")
    hostname = clean_text(str(hostname), 255) if hostname else None
    return rattrs, service, hostname


def _scopes(container: dict, new: str, old: str) -> list:
    return (container.get(new) or []) + (container.get(old) or [])


# ── traces ─────────────────────────────────────────────────────────────────────

def span_kind(value) -> str | None:
    if isinstance(value, str):
        if value.isdigit():
            return KINDS.get(int(value))
        v = value.upper().removeprefix("SPAN_KIND_").lower()
        return v if v in KINDS.values() else None
    return KINDS.get(value) if isinstance(value, int) else None


def span_status(status) -> str:
    code = (status or {}).get("code") if isinstance(status, dict) else None
    if code in (2, "2", "STATUS_CODE_ERROR"):
        return "error"
    if code in (1, "1", "STATUS_CODE_OK"):
        return "ok"
    return "unset"


def convert_traces(doc: dict, window: Window) -> tuple[list[dict], int]:
    spans, dropped = [], 0
    for rs in doc.get("resourceSpans") or []:
        rattrs, service, hostname = _resource(rs)
        for ss in _scopes(rs, "scopeSpans", "instrumentationLibrarySpans"):
            for sp in ss.get("spans") or []:
                trace_id = norm_id(sp.get("traceId"), 16)
                span_id = norm_id(sp.get("spanId"), 8)
                start = to_int(sp.get("startTimeUnixNano"))
                end = to_int(sp.get("endTimeUnixNano"))
                ts_ms = start // 1_000_000
                if not trace_id or not span_id or not start or not window.ok(ts_ms):
                    dropped += 1
                    continue
                attrs = attributes(sp.get("attributes"))
                resource = next((attrs[k] for k in RESOURCE_KEYS if attrs.get(k)), None)
                status = sp.get("status") or {}
                events = [{"ts": to_int(e.get("timeUnixNano")) // 1_000_000,
                           "name": e.get("name"),
                           "attributes": attributes(e.get("attributes"))}
                          for e in (sp.get("events") or [])[:100] if isinstance(e, dict)]
                spans.append({
                    "ts_ms": ts_ms,
                    "trace_id": trace_id,
                    "span_id": span_id,
                    "parent_span_id": norm_id(sp.get("parentSpanId"), 8),
                    "service": service,
                    "name": clean_text(str(sp.get("name") or "span"), 300),
                    "resource": clean_text(str(resource), 200) if resource is not None else None,
                    "kind": span_kind(sp.get("kind")),
                    "duration_ns": max(0, end - start),
                    "status": span_status(status),
                    "status_message": clean_text(status.get("message"), 2000) if status.get("message") else None,
                    "hostname": hostname,
                    "attributes": attrs,
                    "resource_attributes": rattrs,
                    "events": events,
                })
    return spans, dropped


# ── journaux ───────────────────────────────────────────────────────────────────

def severity_status(number, text) -> str:
    n = to_int(number)
    if 1 <= n <= 8:
        return "debug"
    if 9 <= n <= 12:
        return "info"
    if 13 <= n <= 16:
        return "warning"
    if 17 <= n <= 20:
        return "error"
    if 21 <= n <= 24:
        return "critical"
    return normalize_log_status(text)


def convert_logs(doc: dict, window: Window, now_ms: int) -> tuple[list[dict], int]:
    logs, dropped = [], 0
    for rl in doc.get("resourceLogs") or []:
        rattrs, service, hostname = _resource(rl)
        env = rattrs.get("deployment.environment.name") or rattrs.get("deployment.environment")
        for sl in _scopes(rl, "scopeLogs", "instrumentationLibraryLogs"):
            for lr in sl.get("logRecords") or []:
                t = to_int(lr.get("timeUnixNano")) or to_int(lr.get("observedTimeUnixNano"))
                ts_ms = t // 1_000_000 if t else now_ms
                if not window.ok(ts_ms):
                    dropped += 1
                    continue
                body = any_value(lr.get("body"))
                if body is None:
                    message = ""
                elif isinstance(body, str):
                    message = body
                else:
                    message = json.dumps(body, ensure_ascii=False, default=str)
                tags = {"env": str(env)} if env else {}
                logs.append({
                    "ts_ms": ts_ms,
                    "message": message,
                    "status": severity_status(lr.get("severityNumber"), lr.get("severityText")),
                    "service": service,
                    "source": "otlp",
                    "attributes": attributes(lr.get("attributes")),
                    "tags": clean_tags(tags),
                    "trace_id": norm_id(lr.get("traceId"), 16),
                    "span_id": norm_id(lr.get("spanId"), 8),
                    "hostname": hostname,
                })
    return logs, dropped


# ── métriques ──────────────────────────────────────────────────────────────────

@dataclass
class MetricOut:
    name: str
    type: str
    unit: str | None
    tags: dict
    hostname: str | None
    ts_ms: int
    value: float


class CumulativeState:
    """Dernière valeur cumulée vue par série : sert à convertir un cumul en débit ou en delta.

    Chaque nœud d'API garde sa propre mémoire. Si deux points successifs d'une série arrivent sur
    deux nœuds différents, le débit est calculé entre des points plus espacés : il reste juste,
    puisqu'un cumul divisé par son intervalle donne le débit moyen sur cet intervalle.
    """

    def __init__(self, max_items: int = 200_000, ttl_s: float = 6 * 3600):
        self.max_items = max_items
        self.ttl_s = ttl_s
        self._data: dict = {}

    def swap(self, key, t_ns: int, payload):
        """Mémorise (t_ns, payload) et rend la valeur précédente, ou None."""
        prev = self._data.get(key)
        if prev is not None and time.monotonic() - prev[2] > self.ttl_s:
            prev = None
        if prev is not None and t_ns <= prev[0]:
            return None              # point rejoué ou en désordre : on garde l'état le plus récent
        if len(self._data) >= self.max_items:
            self._data.clear()
        self._data[key] = (t_ns, payload, time.monotonic())
        return None if prev is None else (prev[0], prev[1])


def metric_unit(unit) -> str | None:
    if not unit or not isinstance(unit, str):
        return None
    return _UNITS.get(unit, clean_text(unit, 50))


def _metric_name(name) -> str | None:
    if not isinstance(name, str):
        return None
    name = name.strip()
    return name if METRIC_NAME_RE.match(name) else None


def _point_tags(dp: dict, service: str, hostname: str | None) -> dict:
    raw = attributes(dp.get("attributes"))
    tags = clean_tags(raw, normalize_keys=True)
    tags.setdefault("service", service)
    if hostname:
        tags.setdefault("host", hostname)
    return tags


def _temporality(value) -> int:
    if value in (1, "1", "AGGREGATION_TEMPORALITY_DELTA"):
        return 1
    if value in (2, "2", "AGGREGATION_TEMPORALITY_CUMULATIVE"):
        return 2
    return 0


def _number(dp: dict) -> float | None:
    if "asDouble" in dp:
        return to_float(dp["asDouble"])
    if "asInt" in dp:
        return to_float(dp["asInt"])
    return None


def histogram_quantile(q: float, bounds: list[float], counts: list[float],
                       vmin: float | None = None, vmax: float | None = None) -> float | None:
    """Quantile approché d'un histogramme à bornes explicites (interpolation linéaire dans le seau).

    Seau i : ]bounds[i-1], bounds[i]] ; le premier part de -inf, le dernier va à +inf. Les bords
    infinis sont remplacés par min/max quand l'émetteur les fournit, sinon par la borne voisine.
    """
    total = sum(counts)
    if total <= 0 or len(counts) != len(bounds) + 1:
        return None
    target = q * total
    cum = 0.0
    for i, c in enumerate(counts):
        if c <= 0:
            continue
        if cum + c >= target:
            if not bounds:
                lo = vmin if vmin is not None else 0.0
                hi = vmax if vmax is not None else lo
            elif i == 0:
                hi = bounds[0]
                lo = min(vmin, hi) if vmin is not None else (0.0 if hi > 0 else hi)
            elif i == len(bounds):
                lo = bounds[-1]
                hi = vmax if vmax is not None and vmax > lo else lo
            else:
                lo, hi = bounds[i - 1], bounds[i]
                if vmin is not None and vmin > lo:
                    lo = min(vmin, hi)
                if vmax is not None and vmax < hi:
                    hi = max(vmax, lo)
            return lo + (hi - lo) * (target - cum) / c
        cum += c
    return bounds[-1] if bounds else None


def exponential_quantile(q: float, scale: int, zero_count: float, offset: int,
                         counts: list[float]) -> float | None:
    """Quantile approché d'un histogramme exponentiel (seaux positifs seulement)."""
    total = zero_count + sum(counts)
    if total <= 0:
        return None
    target = q * total
    if target <= zero_count:
        return 0.0
    cum = zero_count
    base = 2.0 ** (2.0 ** -scale)
    for k, c in enumerate(counts):
        if c <= 0:
            continue
        if cum + c >= target:
            idx = offset + k
            lo, hi = base ** idx, base ** (idx + 1)
            return lo + (hi - lo) * (target - cum) / c
        cum += c
    return None


QUANTILES = (("p50", 0.5), ("p95", 0.95), ("p99", 0.99))


def convert_metrics(doc: dict, window: Window, state: CumulativeState) -> tuple[list[MetricOut], int]:
    out: list[MetricOut] = []
    dropped = 0
    for rm in doc.get("resourceMetrics") or []:
        _, service, hostname = _resource(rm)
        for sm in _scopes(rm, "scopeMetrics", "instrumentationLibraryMetrics"):
            for m in sm.get("metrics") or []:
                name = _metric_name(m.get("name"))
                unit = metric_unit(m.get("unit"))
                if name is None:
                    for kind in ("gauge", "sum", "histogram", "exponentialHistogram", "summary"):
                        dropped += len((m.get(kind) or {}).get("dataPoints") or [])
                    continue
                dropped += _convert_metric(m, name, unit, service, hostname, window, state, out)
    return out, dropped


def _convert_metric(m, name, unit, service, hostname, window, state, out) -> int:
    dropped = 0

    def emit(n, mtype, u, tags, ts_ms, value):
        if value is None or not math.isfinite(value) or not METRIC_NAME_RE.match(n):
            return
        out.append(MetricOut(n, mtype, u, tags, hostname, ts_ms, value))

    def series_key(suffix, tags):
        return (name + suffix, tags_hash(tags), hostname)

    if "gauge" in m:
        for dp in (m["gauge"] or {}).get("dataPoints") or []:
            t = to_int(dp.get("timeUnixNano"))
            if not t or not window.ok(t // 1_000_000):
                dropped += 1
                continue
            emit(name, "gauge", unit, _point_tags(dp, service, hostname), t // 1_000_000, _number(dp))

    elif "sum" in m:
        s = m["sum"] or {}
        temporality = _temporality(s.get("aggregationTemporality"))
        monotonic = bool(s.get("isMonotonic"))
        for dp in s.get("dataPoints") or []:
            t = to_int(dp.get("timeUnixNano"))
            value = _number(dp)
            if not t or value is None or not window.ok(t // 1_000_000):
                dropped += 1
                continue
            tags = _point_tags(dp, service, hostname)
            ts_ms = t // 1_000_000
            if temporality == 2 and monotonic:
                prev = state.swap(series_key("", tags), t, value)
                if prev is None:
                    continue             # premier point vu : il amorce le calcul du débit
                pt, pv = prev
                if value < pv or t <= pt:
                    continue             # compteur remis à zéro : on repart du nouveau cumul
                rate_unit = (unit + "_per_second") if unit and not unit.endswith("_per_second") else unit
                emit(name, "rate", rate_unit, tags, ts_ms, (value - pv) / ((t - pt) / 1e9))
            elif temporality == 1:
                emit(name, "count", unit, tags, ts_ms, value)
            else:
                # Somme non monotone (UpDownCounter) : c'est une valeur instantanée.
                emit(name, "gauge", unit, tags, ts_ms, value)

    elif "histogram" in m:
        h = m["histogram"] or {}
        temporality = _temporality(h.get("aggregationTemporality"))
        for dp in h.get("dataPoints") or []:
            t = to_int(dp.get("timeUnixNano"))
            if not t or not window.ok(t // 1_000_000):
                dropped += 1
                continue
            tags = _point_tags(dp, service, hostname)
            ts_ms = t // 1_000_000
            count = to_float(dp.get("count")) or 0.0
            total = to_float(dp.get("sum"))
            bounds = [b for b in (to_float(x) for x in dp.get("explicitBounds") or []) if b is not None]
            counts = [to_float(x) or 0.0 for x in dp.get("bucketCounts") or []]
            vmin, vmax = to_float(dp.get("min")), to_float(dp.get("max"))
            if temporality == 2:
                prev = state.swap(series_key("#hist", tags), t, (count, total, tuple(bounds), counts))
                if prev is None:
                    continue
                _, (pcount, ptotal, pbounds, pcounts) = prev
                if count < pcount or tuple(bounds) != pbounds or len(counts) != len(pcounts):
                    continue
                count -= pcount
                total = None if total is None or ptotal is None else total - ptotal
                counts = [c - p for c, p in zip(counts, pcounts)]
                vmin = vmax = None       # min/max d'un cumul ne bornent pas l'intervalle
            emit(name + ".count", "count", None, tags, ts_ms, count)
            if total is not None:
                emit(name + ".sum", "count", unit, tags, ts_ms, total)
                if count > 0:
                    emit(name + ".avg", "gauge", unit, tags, ts_ms, total / count)
            if count > 0 and counts:
                for suffix, q in QUANTILES:
                    emit(f"{name}.{suffix}", "gauge", unit, tags, ts_ms,
                         histogram_quantile(q, bounds, counts, vmin, vmax))

    elif "exponentialHistogram" in m:
        h = m["exponentialHistogram"] or {}
        temporality = _temporality(h.get("aggregationTemporality"))
        for dp in h.get("dataPoints") or []:
            t = to_int(dp.get("timeUnixNano"))
            if not t or not window.ok(t // 1_000_000):
                dropped += 1
                continue
            tags = _point_tags(dp, service, hostname)
            ts_ms = t // 1_000_000
            count = to_float(dp.get("count")) or 0.0
            total = to_float(dp.get("sum"))
            if temporality == 2:
                prev = state.swap(series_key("#exp", tags), t, (count, total))
                if prev is None:
                    continue
                _, (pcount, ptotal) = prev
                if count < pcount:
                    continue
                count -= pcount
                total = None if total is None or ptotal is None else total - ptotal
                emit(name + ".count", "count", None, tags, ts_ms, count)
                if total is not None and count > 0:
                    emit(name + ".sum", "count", unit, tags, ts_ms, total)
                    emit(name + ".avg", "gauge", unit, tags, ts_ms, total / count)
                continue
            emit(name + ".count", "count", None, tags, ts_ms, count)
            if total is not None:
                emit(name + ".sum", "count", unit, tags, ts_ms, total)
                if count > 0:
                    emit(name + ".avg", "gauge", unit, tags, ts_ms, total / count)
            pos = dp.get("positive") or {}
            counts = [to_float(x) or 0.0 for x in pos.get("bucketCounts") or []]
            if count > 0 and counts:
                for suffix, q in QUANTILES:
                    emit(f"{name}.{suffix}", "gauge", unit, tags, ts_ms,
                         exponential_quantile(q, to_int(dp.get("scale")), to_float(dp.get("zeroCount")) or 0.0,
                                              to_int(pos.get("offset")), counts))

    elif "summary" in m:
        for dp in (m["summary"] or {}).get("dataPoints") or []:
            t = to_int(dp.get("timeUnixNano"))
            if not t or not window.ok(t // 1_000_000):
                dropped += 1
                continue
            tags = _point_tags(dp, service, hostname)
            ts_ms = t // 1_000_000
            for qv in dp.get("quantileValues") or []:
                q = to_float(qv.get("quantile"))
                for suffix, target in QUANTILES:
                    if q is not None and abs(q - target) < 1e-9:
                        emit(f"{name}.{suffix}", "gauge", unit, tags, ts_ms, to_float(qv.get("value")))
    return dropped
