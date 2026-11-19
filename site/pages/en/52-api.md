---
id: api
path: /en/docs/api/
title: HYPHA API
nav: API
section: Administer
order: 52
updated: 2026-11-19
description: The HYPHA REST API: key authentication, data intake, OpenTelemetry, deployment events, monitors, downtimes, metrics, logs, actions.
lead: Everything the interface does goes through a JSON API. An application key is enough to automate it.
---
## Authentication

| Use | Key | Header |
|---|---|---|
| Read and write like the interface | application | `X-Hypha-App-Key: hph_…` |
| Send data | ingestion | `X-Hypha-Api-Key: hph_…` (OTLP: also `Authorization: Bearer hph_…`) |

Keys are created in **Settings › API keys** (see [Accounts and roles](/en/docs/accounts-and-roles/#keys)). An application key acts with its author's role. Base address: `https://hypha.s33d.fr/api/v1` (when self-hosting, your own address).

## Conventions

- JSON bodies and responses; timestamps in Unix milliseconds (`ts`, `from`, `to`). Without `from` or `to`, the last hour.
- Error: an HTTP status and `{"error": "short_code", "detail": "readable sentence"}`.
- An empty list returns `[]`; an object outside the workspace returns 404 `not_found`.
- Pagination: `limit` (100 by default, 1,000 at most) and `cursor` when the volume requires it.
- A reached plan limit returns 402 `plan_limit` with the detail.

```bash
curl -s -H "X-Hypha-App-Key: $HYPHA_APP_KEY" https://hypha.s33d.fr/api/v1/hosts
```

## Read

| Route | Returns |
|---|---|
| `GET /hosts` | hosts, their state and their values over the last 2 minutes (`?q=`, `?tag=key:value`) |
| `GET /hosts/{id}` | a host, its agent, its latest checks |
| `GET /problems` | non-OK groups, sorted by priority |
| `GET /monitors`, `GET /monitors/{id}/history` | monitors and a monitor's history |
| `POST /metrics/query` | series of one or more queries, with formulas |
| `GET /logs/search`, `/logs/facets`, `/logs/histogram`, `/logs/patterns` | log search |
| `GET /apm/services`, `/apm/traces`, `/apm/traces/{trace_id}` | services, traces, trace details |
| `GET /inventory`, `/inventory/packages?name=openssl&version_lt=3.5.2` | inventory and fleet-wide package search |
| `GET /certificates` | collected certificates, sorted by expiry |
| `GET /usage` | usage per day |

Metric query example:

```bash
curl -s -H "X-Hypha-App-Key: $HYPHA_APP_KEY" -H "Content-Type: application/json" \
  https://hypha.s33d.fr/api/v1/metrics/query \
  -d '{"queries":[{"id":"a","q":"avg:system.cpu.usage{env:prod} by {host}"}],"max_points":300}'
```

## Write

| Route | Effect |
|---|---|
| `POST /monitors`, `PUT /monitors/{id}`, `DELETE /monitors/{id}` | monitors |
| `POST /monitors/from-template` | monitor from a template: `{"template": "disk_forecast", "vars": {…}}` |
| `POST /monitors/{id}/mute` | mute until `until` (ms) or indefinitely (`null`) |
| `POST /monitors/{id}/groups/ack` | acknowledge a group, with a comment |
| `POST /downtimes` | downtime; `{"duration_s": 3600, "scope": {…}}` starts right away |
| `POST /incidents`, `POST /incidents/from-alert` | incidents |
| `POST /synthetics`, `POST /synthetics/{id}/run` | synthetic tests, immediate run |
| `POST /dashboards`, `POST /dashboards/import` | dashboards |
| `POST /actions`, `POST /actions/{id}/approve` | propose and approve a remote action |
| `POST /explain` | AI explanation: `{"kind": "alert", "monitor_id": 6, "group": "host:web-02", "lang": "en"}`; `kind` can also be `host`, `log`, `trace` or `error` |

## Events

An event appears on the timeline, overlays charts and can trigger a monitor. Example, at the end of a deployment:

```bash
curl -s -H "X-Hypha-App-Key: $HYPHA_APP_KEY" -H "Content-Type: application/json" \
  https://hypha.s33d.fr/api/v1/events \
  -d '{"title":"Deployed shop-api 2.15.0","source":"deploy","type":"deploy","alert_type":"success",
       "tags":{"service":"shop-api","version":"2.15.0"}}'
```

APM charts and the service page overlay `source:deploy` events.

## Send data

| Route | Content |
|---|---|
| `POST /api/v1/intake/metrics`, `/logs`, `/checks`, `/events`, `/host`… | agent protocol (JSON body, gzip accepted, 10 MB at most) |
| `POST /v1/traces`, `/v1/metrics`, `/v1/logs` | OpenTelemetry (OTLP/HTTP, protobuf or JSON) |
| `POST /api/v1/intake/rum?token=…` | browser (RUM script) |

For your own applications, prefer OpenTelemetry or StatsD through the agent: see [Traces](/en/docs/traces/) and [Integrations](/en/docs/integrations/#your-own-metrics). The server accepts timestamps from 14 days in the past to 10 minutes in the future.

Intake responses: `202 {"status":"ok","accepted":N,"dropped":M}`; 401 unknown key; 403 revoked or application key; 413 body too large; 429 rate; 402 plan limit.

## Public pages

- `GET /api/public/status/{slug}`: the state of a published status page, without authentication.
- `GET /api/public/dashboards/{token}`: a dashboard shared read-only.
