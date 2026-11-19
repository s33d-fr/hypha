---
id: accounts
path: /en/docs/accounts-and-roles/
title: Accounts, roles, keys and audit log
nav: Accounts and roles
section: Administer
order: 50
updated: 2026-11-19
description: Manage the members of a HYPHA workspace, their roles, ingestion and application keys, and the audit log.
lead: Four roles, two kinds of keys, and a record of every change.
---
## Workspaces and sign-in

- On hypha.s33d.fr, you sign in with an S33D account (single sign-on). One account can belong to several workspaces; the selector at the top of the screen switches between them.
- When self-hosting, accounts are local (login and password), and an OpenID Connect sign-in can be added.
- A session lasts 7 days.

## Roles

| Role | Can |
|---|---|
| Read-only (`viewer`) | see everything, request an explanation |
| Member (`member`) | also: monitors, dashboards, downtimes, incidents, views, synthetic tests, SLOs, reports, propose an action |
| Administrator (`admin`) | also: channels, keys, members, log masking, fleet, action approval, status pages, public dashboard sharing |
| Owner (`owner`) | everything; only an owner can appoint another owner |

## Invite

**Settings › Members**: an email address and a role. The invitation becomes active when the person signs in with that address. An administrator can change a role or remove a member at any time.

## Keys

**Settings › API keys**:

| Key | Used to | Header |
|---|---|---|
| Ingestion | send data: agents, OpenTelemetry | `X-Hypha-Api-Key` (or `Authorization: Bearer` for OTLP) |
| Application | call the interface API from a script | `X-Hypha-App-Key` |

- A key starts with `hph_`, followed by 40 characters. It is shown only once, at creation: HYPHA only keeps its fingerprint.
- An application key acts with the role of the person who created it.
- An ingestion key does not open the interface API.
- A key is revoked in one click; agents using it are refused at once. To replace it, create the new one, install it (`HYPHA_FORCE_CONF=1` on reinstall), then revoke the old one.

## Audit log

**Settings › Audit** (administrator): every write made through the interface or the API goes in: creation, change, deletion, acknowledgement, downtime, approval or rejection of an action, test send, key rotation, sign-in, sign-out. For each: date, author, object, summary, state before and after, IP address. Filters by period, author, object type, action and text.

Entries are kept for 13 months.

## Usage

**Settings › Usage** shows, day by day, log lines and their volume, metric points, spans, RUM events and checks received, as well as the number of hosts, devices and series, and the chattiest services. See also [Plans and billing](/en/docs/plans-and-billing/).

## Profile

**Settings › Profile**: your account, your workspace, its hosting, and your language (French or English) and theme (dark or light) preferences, stored in the browser.
