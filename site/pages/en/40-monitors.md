---
id: monitors
path: /en/docs/monitors/
title: Monitors and templates
nav: Monitors
section: Alert and act
order: 40
updated: 2026-11-19
description: HYPHA monitor types (metric, host, check, logs, APM, synthetic, anomaly, forecast, outlier, composite, change, event, process, errors, SLO), their thresholds, options and the template catalogue.
lead: A monitor evaluates data every minute and changes state. Start from a catalogue template, there are more than 80, or write your own with a live preview.
---
{{video:5-moniteurs}}

## States

| State | Meaning |
|---|---|
| OK | below thresholds |
| Warning | `warning` threshold crossed |
| Alert | `critical` threshold crossed |
| No data | no data over the window (`no_data_s`) |

A grouped monitor (`by {host}`) has one state per group. Each state change is recorded, notifies the monitor's channels and creates an event.

## Types

| Type | What it evaluates |
|---|---|
| `metric` | a metric query over a window (average, max, min, sum, last value) |
| `host` | a host whose agent has sent nothing for the set delay |
| `service_check` | the worst recent state of a check (`systemd.unit`, `windows.service`, `tls.cert.expiry`…) |
| `log` | the number of lines of a search over a window, grouped by field or attribute |
| `apm` | error rate, p95 latency or throughput of a service |
| `synthetic` | N consecutive failures of a synthetic test |
| `anomaly` | the share of points outside the usual bounds (rolling average, same slots of the last 7 days, or seasonal decomposition) |
| `forecast` | the time before a curve crosses a threshold, by robust regression (for example, disk full within 48 h) |
| `outlier` | the group that deviates from the others (median absolute deviation) |
| `composite` | a logical expression over other monitors: `12 && (13 \|\| !14)` |
| `change` | the change of a query compared with the same window earlier |
| `event` | the number of events of a search (for example, SNMP traps) |
| `process` | the number of processes with a given name on each host |
| `error_tracking` | new or regressed issues of the Errors page |
| `slo` | the remaining error budget or burn rate of an SLO |

The form shows a live preview: the groups, their state, the chart, and depending on the type the anomaly bounds or the forecast.

## Thresholds and options

- `critical` and `warning`, with an optional recovery threshold (`critical_recovery`, `warning_recovery`) to avoid flapping around a value.
- **Priority** from P1 (critical) to P5 (information).
- **Consecutive evaluations** before alerting, and before returning to OK.
- **Evaluation delay** for late data.
- **Host dependency** (on by default): when a host stops answering, the other alerts of that host are held and do not notify. A monitor can also depend on other monitors.
- **Flapping**: beyond 5 changes in 30 minutes, a single "unstable" notification, then silence until things calm down.
- **Renotification** while the alert lasts, and **escalations** to other channels after a delay without acknowledgement.
- **Mute** until a date, or indefinitely.

## The message

The notification message accepts variables and conditional blocks:

```text
{{#is_alert}}{{name}} on {{host}}: {{value}} (threshold {{threshold}}).{{/is_alert}}
{{#is_warning}}{{host}} is close to the threshold: {{value}}.{{/is_warning}}
{{#is_recovery}}{{host}}: back to normal after {{duration}}.{{/is_recovery}}
```

Variables: `{{name}}`, `{{group}}`, `{{host}}`, `{{value}}`, `{{last_value}}`, `{{state}}`, `{{priority}}`, `{{threshold}}`, `{{duration}}`, `{{url}}`, `{{tags.<key>}}`, and `{{eta}}` for a forecast. Blocks: `is_alert`, `is_warning`, `is_recovery`, `is_nodata`. The mention `@canal-<id>` adds a channel for that message.

## The template catalogue

**Monitors › New monitor › From a template**: more than 80 templates by family, each with its variables (targeted tags, thresholds, service). Template names and descriptions, and the monitors they create, are written in French for now; any monitor can be renamed.

| Family | Examples |
|---|---|
| System | CPU or memory saturated, disk I/O wait, disk almost full, disk full within 48 h, inodes, swap, abnormal load, host not reporting, reboot required |
| Services | systemd unit failed, Windows service stopped |
| Security | certificate close to expiry, pending security updates, SSH brute-force attack |
| Applications | HTTP 5xx errors, p95 latency, new application error |
| Databases, Message queues, Search | PostgreSQL, MySQL, Redis, Memcached, MongoDB, RabbitMQ, OpenSearch |
| Network | interface down, device unreachable |
| Mail | Postfix queue |
| Synthetics | synthetic test failing |
| Kubernetes, Cloud, Virtualization | clusters, nodes, AWS, Azure, Google Cloud, OVHcloud, Scaleway, vSphere, Proxmox, Hyper-V, KVM |

A new workspace receives six catalogue monitors, linked to an email channel to the account's address: host not reporting, disk almost full, memory saturated, systemd unit failed, Windows service stopped, certificate close to expiry.
