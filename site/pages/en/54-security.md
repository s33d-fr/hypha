---
id: security
path: /en/docs/security/
title: Security
nav: Security
section: Administer
order: 54
updated: 2026-11-19
description: HYPHA security measures: agent with no open port, signed updates, read-only connectors, allow-listed repairs, hashed keys, masking, audit, hosting in France.
lead: What HYPHA does to limit what an agent, a key or an account can do, and where your data goes.
---
## The agent

- **No open port** on the host: the agent sends its data over HTTPS and polls the server itself for instructions. StatsD and the OTLP relay only listen on the loopback; any other address triggers a warning.
- **Server certificate checked**; turning the check off (`tls_skip_verify`) triggers a warning.
- **Privileges**: on Linux, the agent runs as root to read every log and `/proc`. The systemd unit hardens it: `/usr`, `/boot` and `/etc` read-only for it, `/home` read-only, private `/tmp`, no privilege escalation, writes under `/var/lib/hypha` only, CPU and memory caps. On Windows, it runs as a LocalSystem service.
- **Configuration**: `hypha.yaml` in mode 600. Secrets (SNMP, databases, clouds) stay in the agent's configuration and never go to the server; `check-config` shows them masked.

## Signed updates

Every published binary is listed in `SHA256SUMS`, signed with **ed25519**. The agent checks the signature with the public key built into it, then the binary's sum, then runs it to check its version and the configuration, before installing it. A missing or wrong signature stops the update. See [Fleet and updates](/en/docs/fleet/).

Script installation checks the SHA-256 sum of the downloaded binary.

## Read-only connectors

Each connector asks for the smallest read permission the provider offers:

| Connector | Permission requested |
|---|---|
| AWS | `ec2:DescribeInstances`, `ec2:DescribeInstanceStatus`, `ec2:DescribeVolumes`, `cloudwatch:DescribeAlarms`, `sts:GetCallerIdentity` |
| Azure | Reader role on the subscription |
| Google Cloud | `roles/compute.viewer` |
| OVHcloud | "consumer" key limited to `GET` |
| Scaleway | `InstancesReadOnly`, `ElasticMetalReadOnly` |
| vSphere | ReadOnly role |
| Proxmox VE | token with the PVEAuditor role |
| Kubernetes | `get`, `list`, `watch` on nodes, pods, events, workloads; no secrets, logs or `exec` |
| Databases | `pg_monitor`, `PROCESS` and `REPLICATION CLIENT`, `clusterMonitor`, RabbitMQ `monitoring` tag; read-only sessions |
| SNMP | read-only v3 account, `authPriv` recommended |

## Allow-listed repairs

Remote actions are off by default. When on, they only pass named actions, on the services allowed by the host's configuration, with a deny list (SSH, the agent itself, systemd). No free command; a script is designated by its name, its path comes from the host. An action must be approved by an administrator and expires if no one picks it up. See [Explain and repair](/en/docs/explain-and-repair/).

## Accounts, keys and records

- Four roles (read-only, member, administrator, owner).
- Keys of 40 random characters; HYPHA only keeps their SHA-256 fingerprint and shows them only once. An ingestion key does not open the interface API.
- Notification channel secrets are never shown again.
- The [audit log](/en/docs/accounts-and-roles/#audit-log) keeps every write, its author and IP address for 13 months.

## Data

- Hosted service: on S33D's own infrastructure, in France. Encryption in transit (HTTPS).
- Explanations and postmortems are written by S33D's model, on its own servers: nothing goes to an AI provider.
- [Masking](/en/docs/logs/#masking-sensitive-data) removes secrets from logs before they are written, by default for the `secrets` rule.
- Synthetic tests and webhooks run from S33D only target public addresses: S33D's internal network is closed to them.
- Public status pages and shared dashboards only expose what you chose.
- Self-hosting: nothing leaves your server.

Personal data processing is described in the [privacy policy](/en/privacy/).

## Report a vulnerability

Write to [contact@s33d.fr](mailto:contact@s33d.fr) with the steps to reproduce it. Please give us time to fix it before any disclosure.
