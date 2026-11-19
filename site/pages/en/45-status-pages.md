---
id: status-pages
path: /en/docs/status-pages/
title: Public status pages
nav: Status pages
section: Alert and act
order: 45
updated: 2026-11-19
description: Publish a status page with HYPHA: components drawn from monitors, synthetic tests and SLOs, 90-day availability, incidents and downtimes.
lead: A public page that keeps itself up to date, without ever showing a machine name or an internal value.
---
{{video:6-tableaux-et-statut}}

## Create a page

**Settings › Status pages** (administrator role):

1. a title, a description and a short address (`slug`);
2. components, chosen among monitors, synthetic tests, SLOs and hosts, arranged in groups;
3. publication.

The page lives at `https://hypha.s33d.fr/status/?p=<slug>`. It follows the visitor's light or dark theme.

## What it shows

- An overall banner: operational, degraded, partial outage, major outage, maintenance.
- Each component and its bar for the last 90 days; hovering a day gives its availability.
- Ongoing incidents, then the 90-day history, with their public updates.
- Active downtimes and those of the next 7 days.

## How the state is computed

| Source | Rule |
|---|---|
| Monitor | OK: operational · warning: degraded · alert: partial outage, or major outage at priority P1 |
| Active downtime | maintenance |
| Synthetic test | its last result |
| SLO | objective missed: partial outage |

Daily availability is computed like a monitor SLO. The page is cached for 30 seconds. It exposes no host name, value or query: only the component names you chose.

The number of pages depends on the plan: see [pricing](/en/pricing/).
