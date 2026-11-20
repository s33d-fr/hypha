---
id: docs
path: /en/docs/
title: HYPHA documentation
nav: Overview
section: Get started
order: 1
updated: 2026-11-20
description: Documentation for HYPHA, the monitoring platform by S33D: install the agent, connect integrations, follow metrics, logs and traces, alert and repair.
lead: HYPHA monitors servers, applications, networks and clouds. It tells you the cause of an outage in your language, then proposes the repair, which you approve in one click.
---
## Where to start

<div class="cards">
<a class="card" href="/en/docs/getting-started/"><b>Get started</b><span>Create an account, install the first agent, receive the first alert.</span></a>
<a class="card" href="/en/docs/videos/"><b>Videos</b><span>Seven short demos recorded in the real interface.</span></a>
<a class="card" href="/en/docs/agent-linux/"><b>Install the agent</b><span>Linux, Windows, Docker and Kubernetes.</span></a>
<a class="card" href="/en/docs/integrations/"><b>Integrations</b><span>Databases, mail, web servers, SNMP, clouds and hypervisors.</span></a>
<a class="card" href="/en/docs/monitors/"><b>Monitors</b><span>Thresholds, anomalies, forecasts and more than 90 ready-made templates.</span></a>
<a class="card" href="/en/docs/explain-and-repair/"><b>Explain and repair</b><span>The cause of an outage, its evidence, then the action approved in one click.</span></a>
<a class="card" href="/en/docs/api/"><b>API</b><span>Keys, data intake, OpenTelemetry, automation.</span></a>
<a class="card" href="/en/docs/self-hosting/"><b>Self-hosting</b><span>The same software on your server, your data stays with you.</span></a>
</div>

## What HYPHA does

- **Infrastructure**: Linux and Windows hosts, processes, network, containers, hardware and software inventory, certificates.
- **Logs**: collection, full-text search, facets, patterns, exclusion filters, masking of sensitive data.
- **Traces**: OpenTelemetry, service map, error tracking, real user monitoring (RUM).
- **Network and clouds**: SNMP devices, AWS, Azure, Google Cloud, OVHcloud, Scaleway, vSphere, Proxmox, Hyper-V, KVM.
- **Alerts**: monitors, Problems view, twelve channel types including push notifications to your phone, downtimes, escalations, incidents, SLOs, status pages.
- **Explain and repair**: S33D's AI describes the probable cause with its evidence; a named action is approved in one click and the agent runs it.

## Two ways to use it

| | Hosted service | Self-hosted |
|---|---|---|
| Where the data lives | on S33D's own infrastructure, in France | on your server |
| Plans | Découverte (free), Pro, Entreprise | Libre (free) |
| Getting started | an S33D account, then the agent | `docker compose up -d`, then the agent |

Plans and prices are on the [Pricing](/en/pricing/) page. The server is released under the AGPL v3 licence, the agent under the Apache 2.0 licence.

## HYPHA vocabulary

- **Workspace**: your organisation in HYPHA. Members, keys, monitors and data belong to a workspace.
- **Agent**: the `hypha-agent` program, installed on each machine. It sends metrics, logs and checks, and queries the devices and APIs you give it.
- **Host**: a monitored machine. An SNMP device or a cloud resource also appears as a host.
- **Monitor**: a rule that evaluates data and changes state (OK, warning, alert, no data).
- **Group**: one line of a monitor, for example `host:web-02` for a monitor grouped by host.
