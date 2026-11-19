---
id: downtimes
path: /en/docs/downtimes/
title: Scheduled downtimes
nav: Downtimes
section: Alert and act
order: 42
updated: 2026-11-19
description: Put monitors, hosts or tags in downtime in HYPHA, once or on a schedule, without losing history.
lead: During a downtime, HYPHA keeps evaluating and recording, but notifies no one.
---
## What a downtime does

- Covered monitors are evaluated and their states recorded; no notification goes out.
- The groups concerned carry the "downtime" mark in the Problems view.
- At the end, a group still non-OK notifies normally, once.
- An event marks the start and the end.
- On a status page, a component in downtime shows it.

## Scope

- **Monitors**: a list, or all of them.
- **Tags**: the group or the host must carry all the given tags (`env:preprod`, `role:db`).
- **Groups**: specific groups (`host:web-02`).

## Duration and recurrence

- Once: now for a duration (1 h, 4 h, 1 day, or custom), or between two dates.
- Every day, every week (chosen days) or every month (day of the month), at a given time, for a duration, in the chosen time zone, until an optional end date.

## Where to set it

- **Downtimes** page: list (active ones first), creation with a preview of the monitors affected.
- A host page: **Put in downtime** (1 h, 4 h, 1 day, custom).
- A monitor page, and a line of the Problems view.
- Through the API: `POST /api/v1/downtimes` with `{"duration_s": 3600, …}` starts right away (see [API](/en/docs/api/)).
