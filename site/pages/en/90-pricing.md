---
id: pricing
kind: page
path: /en/pricing/
title: HYPHA pricing
updated: 2026-11-19
description: HYPHA pricing: Libre and Découverte free, Pro at €2 excl. VAT per host per month (€1.50 on a yearly plan), Entreprise. No commitment, hosted in France.
lead: The software is free and open. Self-hosted, it costs nothing and has no limits. Hosted by S33D in France, it is free to start, then billed on actual usage.
---
<div class="plans">
<div class="plan"><h3>Libre</h3><p class="price">€0</p><p class="unit">on your server</p><ul><li>Unlimited hosts, devices and volumes</li><li>Retention set by you</li><li>Explanations with your own AI model</li><li>Agent Apache 2.0, server AGPL v3</li></ul><a class="btn ghost" href="/en/docs/self-hosting/">Install</a></div>
<div class="plan"><h3>Découverte</h3><p class="price">€0</p><p class="unit">no credit card</p><ul><li>5 hosts, 10 network devices or cloud resources</li><li>Logs and traces: 500 MB per day, kept 3 days</li><li>Metrics: 1 day at full resolution, 30 days at 5-minute resolution</li><li>10 public synthetic tests, every 5 min at most</li><li>30 AI explanations per month, 1 status page</li></ul><a class="btn ghost" href="/">Create an account</a></div>
<div class="plan star"><h3>Pro</h3><p class="price">€2 excl. VAT</p><p class="unit">per host per month, no commitment · €1.50 on a yearly plan</p><ul><li>Hosts at actual usage, counted by the hour</li><li>10 GB of logs and 5 GB of traces included per host per month, kept 15 days</li><li>Metrics: 15 days at full resolution, 92 days at 5 minutes, 15 months at one hour</li><li>50 public synthetic tests, every minute</li><li>Unlimited AI explanations and status pages</li></ul><a class="btn" href="/">Get started</a></div>
<div class="plan"><h3>Entreprise</h3><p class="price">Pro + €250 excl. VAT</p><p class="unit">per month</p><ul><li>Everything in Pro</li><li>Support in French with guaranteed response times</li><li>GDPR data processing agreement (DPA)</li><li>Audit log kept 1 year, dedicated database possible</li><li>Purchase order or French public administrative mandate</li><li>SAML SSO (in preparation)</li></ul><a class="btn ghost" href="mailto:contact@s33d.fr?subject=HYPHA%20Entreprise">Write to us</a></div>
</div>

Prices exclude VAT. French VAT at 20% is added (€2 excl. VAT = €2.40 incl. VAT); reverse charge for a business in another European Union country with a valid VAT number.

> Online subscription to the Pro and Entreprise plans opens soon. Libre and Découverte are available now.

## Every plan includes

Outage explanations in French and English, one-click repair, users, alerts (email, webhooks, Slack, Teams and the other channels) and an unlimited number of synthetic tests run by your own agents, SNMP, cloud connectors, mail monitoring, dashboards and status pages.

## The details

| | Libre | Découverte | Pro | Entreprise |
|---|---|---|---|---|
| Hosting | on your premises (Docker Compose) | S33D, in France | S33D, in France | S33D, in France |
| Price excl. VAT | €0 | €0, no credit card | €2 per host per month, no commitment or minimum; €1.50 per host per month on a yearly plan (€18 per host per year, −25%) | Pro + €250 per month |
| Hosts (agents) | unlimited | 5 | usage-based | usage-based |
| Network devices (SNMP) and cloud resources read by a connector | unlimited | 10 | €0.50 each per month | €0.50 each per month |
| Metrics | set by you | 1 day at full resolution, 30 days at 5 minutes | 15 days at full resolution, 92 days at 5 minutes, 15 months at one hour | as Pro |
| Logs and traces | set by you | 500 MB per day (logs and traces together), kept 3 days | included: 10 GB of logs and 5 GB of traces per host per month, pooled across the workspace, kept 15 days; beyond, €0.10 per GB | as Pro |
| Extended retention of logs and traces | set by you | no | 30 days: +€0.05 per GB; 90 days: +€0.15 per GB | as Pro |
| Public synthetic tests (run from S33D) | unlimited | 10 tests, interval of at least 5 min | 50 tests per workspace, interval of at least 1 min; beyond, €1 per 10,000 runs | as Pro |
| Private synthetic tests (run by your agents) | unlimited | unlimited | unlimited | unlimited |
| AI explanations and postmortems (S33D model) | your model | 30 per month | unlimited, fair use | unlimited |
| SMS notifications | no | no | €0.10 each (coming) | €0.10 each (coming) |
| Users, alerts, one-click repair | unlimited | unlimited | unlimited | unlimited |
| Status pages | unlimited | 1 | unlimited | unlimited |
| Extras | | | | SAML SSO (in preparation), support in French with guaranteed response times, GDPR data processing agreement (DPA), audit log kept 1 year, dedicated database possible, payment by purchase order or French public administrative mandate |

## Discounts

- **Non-profits, education, research**: −50% on Pro, i.e. €1 excl. VAT per host per month, €0.75 on a yearly plan. On request to [contact@s33d.fr](mailto:contact@s33d.fr), with proof.
- **Local authorities**: Pro price; payment by French public administrative mandate possible.

## How the invoice is computed

- **Actual usage**: a host is counted by the hour over the month, not at peak. A server present ten days out of thirty counts as one third of a host-month.
- **Monthly, no commitment**: the period runs from the 1st to the last day of the month; usage is billed at month end.
- **Yearly**: a committed number of hosts, paid in advance for the year; hosts beyond the commitment are billed at the monthly price.
- **Nothing is billed silently**: on Découverte, a reached limit refuses what goes beyond and tells you. Nothing is billed without moving to a paid plan.
- **Cancellation**: at any time; the subscription ends at the end of the current period (or of the yearly commitment), then the workspace returns to Découverte.

Example: 20 hosts present the whole month, under 200 GB of logs per month and 10 tests every minute, on Pro: €40 excl. VAT per month (€48 incl. VAT), or €30 excl. VAT per month on a yearly plan.

The full rules are in the [terms of sale](/en/terms/); how billing works is in the [documentation](/en/docs/plans-and-billing/). Questions: the [FAQ](/en/faq/).
