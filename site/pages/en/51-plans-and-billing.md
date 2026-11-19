---
id: billing
path: /en/docs/plans-and-billing/
title: Plans, limits and billing
nav: Plans and billing
section: Administer
order: 51
updated: 2026-11-19
description: Choose and change a HYPHA plan, understand the Découverte limits, Pro usage-based billing, the yearly commitment, VAT, invoices and the return to Découverte.
lead: The Settings › Plan and billing page shows your plan, your limits, your usage and the estimate for the period. Prices are on the Pricing page.
---
{{video:7-formule-et-consommation}}

## Plans at a glance

| Plan | For | Price excl. VAT |
|---|---|---|
| Libre | self-hosting, no limit | €0 |
| Découverte | hosted service, to try or for a small fleet | €0, no credit card |
| Pro | hosted service, usage-based | €2 per host per month, €1.50 on a yearly plan |
| Entreprise | Pro with guaranteed support and a GDPR contract | Pro + €250 per month |

Each plan is detailed on the [Pricing](/en/pricing/) page, and the contractual rules in the [terms of sale](/en/terms/).

## The Plan and billing page

**Settings › Plan and billing** shows:

- your plan, the subscription state, the current period, the VAT applied, the chosen retention;
- **Limits and usage**: hosts and devices active in the last hour, logs and traces received today, tests run from S33D, status pages, this month's explanations;
- **hosts refused** by a limit in the last 24 hours;
- the **estimate for the current period**, computed like the invoice, and on Découverte, what Pro would cost this month with your usage.

Only the workspace **owner** chooses the plan and manages billing.

## When a Découverte limit is reached

HYPHA never charges silently: a reached limit refuses what goes beyond, reports it and bills nothing.

| Limit | What happens |
|---|---|
| 5 active hosts | hosts already present keep their place; one more host is refused and its data is not stored. A host silent for an hour frees its place. |
| 10 devices or cloud resources | same rule |
| 500 MB of logs and traces per day | sends are refused until midnight UTC; refused lines are counted |
| 10 tests run from S33D, every 5 minutes at most | creating one more test is refused; a test run by your agents is not limited |
| 30 AI explanations per month | available again on the 1st of the month |
| 1 status page | creating one more page is refused |

A banner in the interface and the message returned to the agent say which limit is reached. The agent then drops the refused send without storing it, so as not to fill its buffer.

Retention on Découverte: logs and traces visible for 3 days, metrics 1 day at full resolution and 30 days at 5-minute resolution.

## Moving to Pro

1. **Settings › Plan and billing › Choose Pro**.
2. Choose monthly billing (no commitment) or yearly billing (a number of committed hosts, paid in advance).
3. Enter the billing email, the country, and for a business the company name and EU VAT number.
4. Payment happens on Stripe's secure page. The plan turns on as soon as Stripe confirms the payment.

> Online subscription to paid plans opens soon. Libre and Découverte are available now.

## How Pro is billed

- **Hosts at actual usage**, counted by the hour, not at peak: a host present half the month counts as half a host-month.
- **Network devices and cloud resources**: €0.50 excl. VAT each per month, counted by the hour as well.
- **Logs and traces**: 10 GB of logs and 5 GB of traces included per host per month, pooled across the workspace; beyond, €0.10 excl. VAT per GB received.
- **Extended retention** (option): 30 days (+€0.05 excl. VAT per GB received) or 90 days (+€0.15 excl. VAT per GB received), for logs and traces received from the day of the choice.
- **Tests run from S33D**: 50 tests included; beyond, €1 excl. VAT per 10,000 runs.
- The monthly period runs from the 1st to the last day of the month. Usage is sent to Stripe every hour; the month's invoice takes the total.
- **Yearly**: committed hosts are paid in advance (€18 excl. VAT per host per year); hours beyond the commitment follow the monthly price.

## VAT and invoices

- Prices exclude VAT. French VAT at 20%.
- A business in another European Union country with a valid VAT number (checked by Stripe with VIES): reverse charge, stated at the bottom of the invoice.
- Invoices and payment method: the **Payment and invoices** button, which opens Stripe's portal.
- HYPHA never sees your bank details.

## Discounts

- Non-profits, education, research: −50% on Pro hosts, on request to S33D with proof; S33D applies the discount to your workspace.
- Local authorities: Pro price, payment by French public administrative mandate possible.

## Change or stop

- **Switch to Entreprise**: the €250 excl. VAT monthly fee is added, prorated for the current month. **Back to Pro** removes it with a prorated credit.
- **Back to Découverte**: the subscription ends at the end of the current period (or of the yearly commitment). Usage of the period is still billed, then the Découverte limits apply.
