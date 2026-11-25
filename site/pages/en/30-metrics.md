---
id: metrics
path: /en/docs/metrics/
title: Metrics and dashboards
nav: Metrics and dashboards
section: Observe
order: 30
updated: 2026-11-20
description: The HYPHA metric query language, its formulas and functions, the explorer, dashboards, their widgets and public read-only sharing.
lead: A short query, formulas, functions, and a grid of widgets the whole team shares.
---
## The query language

```text
<aggregation>:<metric>{<filters>} by {<keys>}
```

- **Aggregations** across series: `avg`, `sum`, `min`, `max`, `count`.
- **Filters**: `key:value`, `!key:value` to exclude, `key:prefix*`, `*` for everything.
- **Grouping**: `by {host}`, `by {host,mount}`.
- **Time aggregation** (optional): `.rollup(avg|sum|min|max|last, <seconds>)`.

Examples:

```text
avg:system.cpu.usage{env:prod} by {host}
max:system.disk.in_use{role:db} by {host,mount}
sum:nginx.requests{*}.rollup(sum, 300)
```

HYPHA reads raw points up to two days, 5-minute aggregates up to 60 days, then hourly aggregates.

### Formulas and functions

Each query has an identifier (`a`, `b`…); a formula combines them: `a / b * 100`. Functions, on a query or a formula:

| Family | Functions |
|---|---|
| Arithmetic | `abs`, `log2`, `log10`, `cumsum`, `per_second`, `rate`, `derivative` |
| Smoothing | `moving_avg(x, n)`, `ewma(x, n)`, `median(x, n)` |
| Comparison | `timeshift(x, -86400)` (same curve one day earlier) |
| Selection | `top(x, 5, 'mean', 'desc')`, `clamp_min`, `clamp_max`, `fill(x, 'zero')` |
| Analysis | `forecast(x, 'linear', horizon)`, `anomalies(x, 'agile', 2)`, `outliers(x, 'mad', 3)` |

## The explorer

The **Metrics** page finds a metric, suggests its tags, plots the query and the formula, and saves the view. Each metric can carry a unit and a description (catalogue provided for `system.*`, `snmp.*`, `postgresql.*`, `haproxy.*`, `nginx.*`).

## Dashboards

Two dashboards come built in: **Vue système** (system) and **Vue réseau** (network), created with French titles you can rename. Yours are laid out on a 12-column grid.

Widgets: time series, query value, top list, log stream, monitor summary, note, host map, check status, heatmap, distribution, table, change, scatter plot, pie chart, event timeline, monitor graph with its thresholds, monitor value, service map, network map, problems, SLO list, image, embedded frame (same origin), group.

Dashboard features:

- template variables, with multiple values (`$host`, `$env`);
- event overlays, for example deployments (`source:deploy`);
- comparison with the previous period, logarithmic axis, coloured thresholds;
- cloning, folders, automatic refresh, full-screen TV mode;
- JSON export and import.

### Ready-made dashboard catalogue

**Dashboards › Catalog**: about thirty ready-made dashboards, sorted by family, installed in one click into the workspace; an installed dashboard becomes a regular one (editable, clonable, exportable), and installing twice gives two dashboards. Their queries target the metrics the agent publishes: a dashboard stays empty until the integration is active. Dashboard titles are in French for now; any dashboard can be renamed.

| Family | Dashboards |
|---|---|
| System | system view, network view, system in detail, hardware (SMART, sensors), Windows (IIS, SQL Server) |
| Databases, search, queues | PostgreSQL, MySQL and MariaDB, Redis and Valkey, Memcached, MongoDB, OpenSearch and Elasticsearch, RabbitMQ |
| Web, mail, network, security | nginx and Apache, HAProxy, mail (Postfix, Dovecot, rspamd, Exchange), BIND DNS, SNMP devices, certificates and access |
| Containers, virtualization, cloud | Docker, Kubernetes, KVM and VirtualBox, Proxmox VE, Hyper-V, VMware vSphere, clouds (AWS, Azure, Google Cloud, Scaleway, OVHcloud) |
| Generic collection | OpenMetrics targets, node_exporter, Prometheus server, plugins, logs received by syslog (see [Generic collection](/en/docs/generic-collection/)) |

API: `GET /api/v1/dashboards/catalogue`, `POST /api/v1/dashboards/catalogue/{id}`.

### Public read-only sharing

An administrator can publish a dashboard through a secret link (`/share/?t=…`). The link only runs the queries of the dashboard's widgets, read-only. It can be withdrawn at any time.

## Send your metrics

- StatsD: see [Integrations](/en/docs/integrations/#your-own-metrics).
- `/metrics` pages in Prometheus or OpenMetrics format: see [Generic collection](/en/docs/generic-collection/).
- OpenTelemetry: OTLP metrics on `/v1/metrics`, see [Traces](/en/docs/traces/).
- Deployment events: see [API](/en/docs/api/#events).
