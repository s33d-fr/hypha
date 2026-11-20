---
id: logs
path: /en/docs/logs/
title: Logs
nav: Logs
section: Observe
order: 31
updated: 2026-11-19
description: Collect, search and control logs in HYPHA: search syntax, facets, patterns, live tail, exclusion filters, parsing on arrival, masking, log-based metrics.
lead: Every log of the fleet in one place, searched in one line, parsed and masked on arrival, with control over their volume.
---
## Where logs come from

- **Linux**: the systemd journal and the files declared in `logs.files`.
- **Windows**: event logs (System and Application by default; other channels and filters: see [Windows](/en/docs/windows/)).
- **Applications**: OpenTelemetry (`/v1/logs`), directly or through the agent's relay.
- A JSON line is decoded by the agent: `message` or `msg`, `level` or `severity`, the rest as attributes.

See [log configuration](/en/docs/configuration/#logs).

{{video:2-journaux}}

## Search

| Syntax | Effect |
|---|---|
| `timeout connection` | lines containing both words (implicit AND, case-insensitive) |
| `"exact phrase"` | the phrase as is |
| `service:nginx`, `host:web-01`, `status:error`, `source:journald` | field equality; `service:shop*` for a prefix |
| `@http.status_code:502` | attribute value |
| `-service:sshd` | exclusion |
| `status:error OR status:critical` | either one |

Statuses: `emergency`, `alert`, `critical`, `error`, `warning`, `notice`, `info`, `debug`.

The page shows the histogram by status and the facets: status, service, host, source, and, when present, HTTP code and method. Clicking a value adds it to the query.

- **Live**: new lines arrive every 2 seconds.
- **Context**: the lines of the same host and service around a line.
- **Patterns**: similar messages grouped into templates (numbers, identifiers, addresses, paths replaced), counted, with their share and statuses. A click filters on the pattern.
- **Views**: a search can be saved and found again in the Views menu.
- **Explain**: from a line's details, S33D's AI describes what is happening (see [Explain and repair](/en/docs/explain-and-repair/)).

## Parsing on arrival

HYPHA parses common formats into attributes, without overwriting an existing attribute:

- uvicorn, nginx and Apache access logs (combined format), HAProxy (httplog): `http.method`, `http.url_path`, `http.status_code`, `http.useragent`, `network.client.ip`, `network.bytes_written`, `duration`, `haproxy.frontend`, `haproxy.backend`, `haproxy.server`;
- `sshd`, `sudo`, `su`, `fail2ban`: `evt.name`, `evt.outcome`, `usr.name`, `network.client.ip`, `sudo.command`, `fail2ban.jail`.

An info line with an HTTP code of 500 or more becomes an error, 400 or more a warning. A more severe status already set is never lowered.

## Exclusion filters and sampling

Access logs and health probes often make up most of the volume. **Settings › Logs** shows volume by service and by host, and sets filters applied on arrival, before writing:

- **Exclude**: lines matching the query are dropped.
- **Sample**: a share of the lines is kept (for example 10%), drawn in a stable way.
- The first matching filter decides; reorder by dragging.
- Before saving, the preview gives the lines per hour concerned, their share of the total and five examples.
- Errors and more severe lines are never dropped by sampling, only by an explicit exclusion on the status.

From the Logs page, **Exclude these lines** pre-fills a filter with the current query. Dropped lines do not count in your usage.

## Masking sensitive data

Applied on arrival, before writing, to the message and text attributes. Masked data becomes `[masqué:<rule>]`.

| Rule | What it masks | State in a new workspace |
|---|---|---|
| `secrets` | Bearer tokens, `password=…`, AWS, GitHub and Slack keys, HYPHA keys, JWTs, PEM private keys | on |
| `credit_card` | card numbers (Luhn check) | offered |
| `iban` | IBANs | offered |
| `email` | email addresses | offered |
| `phone_fr` | French phone numbers | offered |
| `ipv4_public` | public IPv4 addresses | offered |
| `fr_nir` | French social security numbers | offered |

An administrator turns rules on and tests a sample text in **Settings › Logs**.

## Log-based metrics

A log query can feed a metric: a count of lines, or the distribution of an attribute (`.avg`, `.p50`, `.p95`, `.p99`, `.max`), by group. These metrics are plotted and alerted on like any other and remain after the logs' retention ends.

## Application errors

An error line carrying a stack trace (Python, Java, Go, Node) or `error.kind` and `error.stack` attributes feeds the **Errors** page, with errors from traces and browsers. See [Traces](/en/docs/traces/#error-tracking).

## Retention

Retention depends on the plan: see [pricing](/en/pricing/). When self-hosting, you set it.
