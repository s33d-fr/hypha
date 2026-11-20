---
id: getting-started
path: /en/docs/getting-started/
title: Getting started with HYPHA
nav: Get started
section: Get started
order: 2
updated: 2026-11-19
description: Create a HYPHA account, install the first agent and receive the first alert, in ten minutes.
lead: One account, one agent, and your first data arrives within a minute. Allow ten minutes.
---
## 1. Create an account

1. Open [hypha.s33d.fr](https://hypha.s33d.fr) and choose **Sign in**.
2. Sign in with your S33D account, or create one: email address, or a Google, Microsoft, Apple, Facebook or GitHub account.
3. On first sign-in, HYPHA creates your **workspace** on the Découverte plan, free and without a credit card.

A new workspace comes with:

- an email notification channel to your account's address;
- six monitors linked to that channel: host not reporting, disk almost full, memory saturated, systemd unit failed, Windows service stopped, certificate close to expiry (their names are in French for now; rename them at will).

You are warned of a common outage without setting anything up.

> Must your data stay on your premises? Install the same software on your server: see [Self-hosting](/en/docs/self-hosting/).

## 2. Install the agent

Open **Settings › Install the agent**. The page gives the command for your system and can create the ingestion key for you (button **Create a key and insert it**).

On Linux:

```bash
curl -fsSL https://hypha.s33d.fr/install/install.sh | sudo HYPHA_API_KEY=hph_your_key bash
```

On Windows, in PowerShell run as administrator:

```powershell
$env:HYPHA_API_KEY='hph_your_key'; iwr -useb https://hypha.s33d.fr/install/install.ps1 | iex
```

Details per system: [Linux](/en/docs/agent-linux/), [Windows](/en/docs/agent-windows/), [Docker](/en/docs/agent-docker/), [Kubernetes](/en/docs/kubernetes/).

## 3. See your data

Within a minute, the host appears in **Infrastructure**. The overview shows its metrics; the host page shows its processes, logs, checks and inventory.

As long as no agent is connected, the overview shows three steps to guide you.

## 4. Receive an alert

The six default monitors already watch the host. To go further:

- **Monitors › New monitor › From a template** offers more than 90 templates: CPU, memory, disk full within 48 h, certificates, databases, clouds, Kubernetes;
- **Settings › Notification channels** adds Slack, Teams, Telegram, Discord, PagerDuty, a webhook and other channels;
- each channel's **Send a test** button checks that it reaches its destination.

## 5. Invite your team

**Settings › Members** invites a person by email address, with a role: read-only, member, administrator or owner. The invitation becomes active at their first sign-in. See [Accounts and roles](/en/docs/accounts-and-roles/).

## Next steps

- Connect the host's software: [Integrations](/en/docs/integrations/) and [Databases](/en/docs/databases/).
- Monitor network devices: [SNMP](/en/docs/snmp/).
- Read an AWS, Azure, Google Cloud, OVHcloud or Scaleway account: [Clouds](/en/docs/cloud/).
- Understand an outage and repair it: [Explain and repair](/en/docs/explain-and-repair/).
