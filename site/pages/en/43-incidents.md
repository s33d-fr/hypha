---
id: incidents
path: /en/docs/incidents/
title: Incidents and postmortems
nav: Incidents
section: Alert and act
order: 43
updated: 2026-11-19
description: Declare and follow an incident in HYPHA, keep its timeline, inform the public, then have S33D's AI draft the postmortem.
lead: Declare, follow, resolve, learn. The timeline fills itself and the AI writes the first draft of the postmortem.
---
## Declare an incident

- From the Problems view: **Open an incident** on an alert. The title, severity (taken from the priority) and link to the monitor are filled in, and the timeline starts with the group's history.
- From the **Incidents** page: title, severity from 1 to 5, commander, customer impact.

Each incident receives a number within the workspace.

## Follow

- **Status** and **severity** can be changed; each change goes into the timeline.
- **Timeline**: team notes, public updates, and, while the incident is open, every state change of the linked monitors.
- **Links** to monitors, hosts, services.
- **Charts** of the linked monitors over the incident's duration.
- **Notifications**: creation and status changes go to the workspace's incident channels.

## Inform the public

Link the incident to one or more [status pages](/en/docs/status-pages/): its title, status and public updates appear there, without any internal data.

## The postmortem

**Postmortem** tab, **Draft with AI** button. HYPHA gathers the timeline, the linked alerts and the error logs of the hosts concerned during the incident, then S33D's model writes in Markdown:

- summary, impact, timeline;
- root cause;
- what worked, what did not;
- actions, as a table: action, owner, due date.

The text stays editable. The drafting is recorded in the timeline. The number of AI drafts depends on the plan: see [pricing](/en/pricing/).
