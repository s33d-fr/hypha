---
id: faq
kind: page
path: /en/faq/
title: HYPHA frequently asked questions
updated: 2026-11-19
description: Answers to common questions about HYPHA: price, plans, data hosting, agent, security, AI, self-hosting, cancellation.
lead: Short answers to the questions we are asked most. The documentation has the details.
---
## The product

### What is HYPHA?

HYPHA is S33D's monitoring platform: server metrics, logs, traces, network, clouds, alerts and status pages, in one place. It tells you the probable cause of an outage with its evidence, in your language, then proposes the repair, which you approve in one click.

### What can it monitor?

Linux and Windows servers, Docker containers, Kubernetes clusters, databases (PostgreSQL, MySQL, MariaDB, Redis, MongoDB, SQL Server…), web and mail servers, switches, NAS, printers and UPS over SNMP, AWS, Azure, Google Cloud, OVHcloud and Scaleway accounts, vSphere, Proxmox, Hyper-V and KVM hypervisors, sites and APIs through synthetic tests, and your applications through OpenTelemetry.

### Does HYPHA replace our current tools?

HYPHA covers infrastructure monitoring, logs, traces, synthetic tests, alerts, incidents and status pages. Its metric names follow Datadog conventions, and it receives OpenTelemetry and StatsD: your dashboards and instrumented applications find their way quickly.

### In which languages?

The interface, the documentation and outage explanations exist in French and English.

## Prices and plans

### Is HYPHA free?

Yes, in two ways: the **Libre** plan (the software on your server, without limits) and the **Découverte** plan (hosted by S33D, 5 hosts, no credit card). The **Pro** plan costs €2 excl. VAT per host per month, or €1.50 on a yearly plan. See [pricing](/en/pricing/).

### How are hosts counted?

On actual usage, by the hour, over the month, not at peak. A server running ten days out of thirty counts as one third of a host-month. An SNMP device or a cloud resource read by a connector costs €0.50 excl. VAT per month on Pro.

### What happens when I reach a Découverte limit?

What goes beyond is refused and the interface tells you: a sixth host is not stored, logs beyond 500 MB are refused until midnight UTC, explanations come back on the 1st of the month. Nothing is ever billed without moving to a paid plan.

### Is there a commitment?

Not on the monthly plan: you stop when you want, the subscription runs until the end of the current month. The yearly plan commits a number of hosts for a year, paid in advance, in exchange for a 25% discount.

### Are prices with or without VAT?

Without. French VAT at 20% is added; a business in another European Union country with a valid VAT number is under reverse charge.

### Are there discounts?

Yes: −50% on Pro for non-profits, education and research, with proof. Local authorities pay the Pro price and can pay by French public administrative mandate.

### How do I pay?

By card, on Stripe's secure page. S33D never sees your bank details. The Entreprise plan accepts purchase orders and French public administrative mandates. Online subscription to paid plans opens soon.

## Data and security

### Where is my data hosted?

On S33D's own infrastructure, in France. AI explanations are written on S33D's servers: nothing goes to an AI provider. When self-hosting, nothing leaves your server.

### How long is my data kept?

It depends on the plan: on Découverte, logs and traces 3 days, metrics 30 days; on Pro, logs and traces 15 days (30 or 90 days as an option), metrics up to 15 months at one-hour resolution. See the [privacy policy](/en/privacy/).

### Is the agent safe?

It opens no port: it sends its data over HTTPS and polls the server itself. Its updates are signed and verified. Connectors use read-only accounts. Repairs are off by default and, when on, only pass named actions that you allow. See [Security](/en/docs/security/).

### Can we avoid sending sensitive data?

Yes. The `secrets` masking rule removes tokens, passwords and keys from logs by default, before they are written. Other rules (cards, IBANs, emails, phone numbers, IP addresses, social security numbers) turn on in one click. Exclusion filters drop whole lines, and the agent can be told never to send some lines.

### Do we have to give administrator access to our clouds?

No. Each connector asks for the smallest read permission: Reader role at Azure, `compute.viewer` at Google, a few `Describe` actions at AWS, `GET` requests at OVHcloud.

## AI and repair

### How does outage explanation work?

On an alert, a host, a log line or a trace, HYPHA gathers logs, metrics, checks, events and processes, then S33D's model describes what is happening, the probable cause with its evidence, the fix and how to prevent it. Allow 5 to 30 seconds.

### Does the AI act on my servers by itself?

No. It proposes a named action (for example, restart a service). A human approves it in one click, and the agent only runs it if the host's configuration allows it.

## Installation

### How do I install the agent?

One command on Linux or Windows, given in **Settings › Install the agent**. There are also `.deb` and `.rpm` packages and a Docker image. See [Getting started](/en/docs/getting-started/).

### Does the agent slow my servers down?

It aims for less than one percent of a core and less than 60 MB of memory, runs at low priority, and systemd caps it in case of runaway.

### Can we host HYPHA ourselves?

Yes, with Docker Compose: that is the Libre plan, free and without limits. See [Self-hosting](/en/docs/self-hosting/).

## Account

### How do I stop?

Go back to Découverte from **Settings › Plan and billing**: the subscription ends at the end of the period. To delete your account and your data, write to [contact@s33d.fr](mailto:contact@s33d.fr).

### Who do I contact?

S33D, at [contact@s33d.fr](mailto:contact@s33d.fr).
