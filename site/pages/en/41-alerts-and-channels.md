---
id: alerts
path: /en/docs/alerts-and-channels/
title: Alerts, Problems view and notification channels
nav: Alerts and channels
section: Alert and act
order: 41
updated: 2026-11-21
description: The HYPHA Problems view, acknowledgement, priorities, escalations, and the thirteen notification channels: email, push notifications, webhook, Slack, Teams, Telegram, Discord, Google Chat, Mattermost, ntfy, PagerDuty, Opsgenie, GLPI.
lead: A single list of what is wrong, sorted by priority, and notifications that reach the place where the team works.
---
## The Problems view

At the top of the sidebar, **Problems** lists every monitor group of the workspace that is not OK, sorted by priority then age. A count of unacknowledged problems stays visible in the sidebar.

For each line: priority, state, monitor, group, host, value, running duration, acknowledgement, last notification. Actions:

- **Acknowledge**, with a comment: renotifications and escalations stop; a return to OK clears the acknowledgement. Multiple selection acknowledges in bulk.
- **Explain**: S33D's AI describes the probable cause (see [Explain and repair](/en/docs/explain-and-repair/)).
- **Put in downtime** (see [Downtimes](/en/docs/downtimes/)).
- **Open an incident**, pre-filled with the monitor, the group and their history (see [Incidents](/en/docs/incidents/)).

Filters: priority, state, acknowledged or not, held by a downtime or a dependency, free text.

## Priorities

| Priority | Colour | Suggested use |
|---|---|---|
| P1 | red | service down for customers |
| P2 | orange | outage getting worse or hitting a feature |
| P3 | yellow | degradation to handle within the day |
| P4 | blue | to schedule |
| P5 | grey | information |

## Notification channels

**Settings › Notification channels** (administrator role). Each channel has a **Send a test** button.

| Channel | Settings |
|---|---|
| Email | one or more addresses; plain HTML message with the state, value, threshold, link to the monitor and to the explanation, and the host's latest log lines |
| Push notifications | all members of the workspace or selected members; notification on the phones and computers they enabled, with the link to the problem and its explanation, and an **Acknowledge** button (see [Mobile app](/en/docs/mobile-app/)) |
| Webhook | address and headers; JSON body `{monitor, group, state, value, message, url}` |
| Slack, Microsoft Teams, Google Chat, Discord, Mattermost | incoming webhook address |
| Telegram | bot token and chat identifier |
| ntfy | server, topic, optional token |
| PagerDuty | routing key (Events API v2): trigger then resolve |
| Opsgenie | API key and region (EU or US): create then close |
| GLPI | REST API address, application token and a user's API token: ticket opened on alert, solved on recovery (see below) |

Each send has 10 seconds to succeed, and two retries. A channel's secrets (tokens, keys, headers) are never shown again.

SMS notifications, planned for the Pro and Entreprise plans (€0.10 excl. VAT each), are not open yet.

### GLPI channel

An alert opens a ticket in [GLPI](https://glpi-project.org) through its REST API (`apirest.php`); the recovery solves it.

- **Alert** (ALERT, WARN, no data): a ticket is opened, titled with the monitor and group followed by a stable reference (`[HYPHA-1a2b3c4d5e]`); its urgency follows the monitor priority (P1 very high to P5 very low). While that ticket is not solved, reminders, escalations and worsening add a **follow-up** to it instead of opening another ticket.
- **Recovery**: a “recovered” follow-up, then a **solution**: the ticket becomes “Solved” (closing stays with GLPI and its approval rule).
- **Send a test**: a ticket opened then solved right away.

Settings: the address (`https://your-glpi/apirest.php`, or the GLPI address, completed automatically), the **application token** (Setup › General › API: enable the REST API, then an API client with its token) and a **user's API token** (Preferences › Remote access keys), preferably a service account with the Technician profile (allowed to create tickets and add follow-ups and solutions). Optional: type (incident or request), entity, ITIL category, request source (GLPI numeric IDs). The API is reached with tokens only: no login or password. A session is opened then closed for each send. A GLPI on a private network is only reached when the workspace allows private targets (self-hosting).

Tested against GLPI 12.0.2 (official image, MariaDB 11.4): ticket opened with “very high” urgency for a P1 monitor, follow-up added by the reminder, follow-up and solution on recovery (status “Solved”), channel test, rejected token (the `ERROR_GLPI_LOGIN_USER_TOKEN` error shown as is).

## Escalations

In a monitor's options: if a group stays non-OK and unacknowledged after a delay (for example 15 minutes), HYPHA notifies other channels, with their own message. Several steps can follow each other.

## Scheduled reports

**Settings › Reports** sends by email, on the chosen schedule:

- a **summary**: host availability, alerts by priority, noisiest monitors, problem duration, SLOs, incidents, hosts that appeared or disappeared, pending security updates, certificates to renew within 30 days;
- the **SLO status**;
- a **dashboard** rendered as values and small charts.

Each report has a preview and a send-now button.
