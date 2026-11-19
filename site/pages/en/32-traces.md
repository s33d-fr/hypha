---
id: traces
path: /en/docs/traces/
title: Traces, OpenTelemetry, errors and RUM
nav: Traces and OpenTelemetry
section: Observe
order: 32
updated: 2026-11-19
description: Send OpenTelemetry traces, metrics and logs to HYPHA; APM, service map, error tracking and real user monitoring (RUM).
lead: HYPHA speaks OpenTelemetry. Your applications send their traces, directly or through the host's agent, and HYPHA derives services, latencies, errors and a dependency map.
---
## Send OpenTelemetry data

HYPHA receives OTLP over HTTP, as protobuf or JSON:

| Signal | Address |
|---|---|
| Traces | `https://hypha.s33d.fr/v1/traces` |
| Metrics | `https://hypha.s33d.fr/v1/metrics` |
| Logs | `https://hypha.s33d.fr/v1/logs` |

The ingestion key goes in the `X-Hypha-Api-Key` header, or `Authorization: Bearer <key>`.

### Through the agent (recommended)

Turn on the agent's relay: applications target the loopback, the agent adds the key and retries if the server is unreachable.

```yaml
otlp:
  enabled: true
  listen: 127.0.0.1:4318
```

```bash
export OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318
export OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
export OTEL_SERVICE_NAME=shop-api
export OTEL_RESOURCE_ATTRIBUTES=deployment.environment=prod,service.version=2.14.0
```

### Directly

```bash
export OTEL_EXPORTER_OTLP_ENDPOINT=https://hypha.s33d.fr
export OTEL_EXPORTER_OTLP_HEADERS="X-Hypha-Api-Key=hph_your_key"
```

### What HYPHA keeps

- `service.name` gives the service; `host.name` attaches the trace to the host of the same name.
- A span's resource comes from `http.route`, otherwise `url.path`, otherwise `db.statement` (200 characters).
- A span with the OTLP "error" status is an error.
- OTLP metrics: gauges and sums become points (a cumulative sum becomes a rate); a histogram gives `.count`, `.sum`, `.avg` and approximate quantiles `.p50`, `.p95`, `.p99`.

## APM

{{video:3-traces}}

- **Services**: throughput, error rate, p50, p95 and p99 latency of each service.
- **Service page**: operations, resources, charts, overlaid deployments, linked logs and monitors.
- **Traces**: filter by service, operation, status and minimum duration; a trace is shown as a waterfall with its spans, attributes, events and linked logs.
- **Service map**: dependencies drawn from spans (a service calling another, a database, an external service), coloured by state.
- **Service catalogue**: team, tier, links (runbook, repository, documentation, chat), monitors, SLOs and errors of each service. The runbook link appears in the notifications of its monitors.

Monitor templates: HTTP 5xx errors of a service, p95 latency, new application error.

## Error tracking

The **Errors** page groups similar errors into issues:

- error spans carrying an `exception` event (`exception.type`, `exception.message`, `exception.stacktrace`);
- error log lines carrying a stack trace (Python, Java, Go, Node) or `error.kind` and `error.stack`;
- JavaScript errors reported by RUM.

An issue's fingerprint takes into account the origin, the service, the type and the first five frames of the stack, without line numbers or values. Each issue shows its stack, its histogram and its latest occurrences with the matching trace or line. A resolved issue that comes back reopens and is marked "regressed". Buttons **Resolve**, **Ignore** and **Explain**.

## RUM: real user monitoring

Create an application in the **RUM** page, then add its script to your pages:

```html
<script src="https://hypha.s33d.fr/rum/hypha-rum.js" data-token="client_token" data-service="shop-web" defer></script>
```

Optional attributes: `data-env`, `data-version`, `data-sample` (share of sessions followed, 0 to 1), `data-full-url` (`true` to keep the full address, query included).

The dependency-free script collects page views and their load time, the LCP, CLS and INP web vitals, JavaScript errors and resources slower than one second. It sets no cookie: the session lives in the tab (`sessionStorage`) and expires after 30 minutes of inactivity. The RUM page shows views, sessions, errors, the 75th percentile of web vitals and the slowest pages.

The client token only opens the sending of RUM events: it can appear in a public page.
