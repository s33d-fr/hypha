---
id: slos
path: /en/docs/slos/
title: Service level objectives (SLOs)
nav: SLOs
section: Alert and act
order: 44
updated: 2026-11-19
description: Define SLOs in HYPHA, from a monitor or from two metric queries, and follow the error budget and burn rate.
lead: A numeric objective, an error budget, and an alert when it melts too fast.
---
## Two ways to measure

- **From a monitor**: the share of time it was OK. A warning or no data count as OK, an alert as bad. Time before the monitor was created is not counted.
- **From two queries**: the ratio of "good" to "total", for example requests without error over all requests. With no request over the window, the SLI is 100%.

Settings: target (for example 99.9%), optional warning threshold, window of 7, 30 or 90 days.

## What HYPHA computes

- **SLI**: the value measured over the window.
- **Remaining error budget**: (SLI − target) ÷ (100 − target), as a percentage.
- **Burn rate** over 1 hour and 6 hours: 1 means the budget runs out exactly at the end of the window.
- **State**: good, warning, objective missed.

An SLO page plots the cumulative SLI with the target line. The `slo` dashboard widget and the "SLO" report reuse these values.

## Alerting

An `slo` monitor alerts when the remaining budget falls below a threshold, or when the burn rate exceeds a threshold over 1 h or 6 h. An SLO can also be a component of a [status page](/en/docs/status-pages/): a missed objective shows there as "partial outage".
