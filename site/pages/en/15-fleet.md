---
id: fleet
path: /en/docs/fleet/
title: Agent fleet and signed updates
nav: Fleet and updates
section: Install the agent
order: 15
updated: 2026-11-19
description: Follow the version and state of every agent, update a whole fleet from the interface, with signed and verified binaries.
lead: Choose a target version in the interface, and each agent updates itself after checking the binary's signature.
---
## The Agents page

**Settings › Agents** lists every agent: host, version (coloured when behind), system, last contact, state of each collector and integration, capabilities (actions, private synthetic tests, automatic update, SNMP devices) and last error.

## The control channel

The agent polls the server every 30 seconds (`control.interval`). It reads its target version, approved actions and the synthetic tests assigned to it. The server never connects to the agent: no port is opened on your machines.

## Update a fleet

1. Set `auto_update: true` in the configuration of the agents concerned.
2. In **Settings › Agents**, select one agent, several, or the whole fleet of a system, then choose the target version.
3. Follow the state live: download, verification, installation, then the new version at the next contact.

On every update, the agent:

1. downloads `SHA256SUMS` and its signature `SHA256SUMS.sig` from `<url>/install/`;
2. checks the **ed25519** signature with the public key built into the agent;
3. downloads the binary for its platform and checks its SHA-256 sum;
4. runs the new binary to check its version and the current configuration;
5. installs it, then restarts: on Linux through systemd, on Windows through a detached process.

A missing or wrong signature stops everything: the agent keeps its binary and reports the failure. A failure is retried an hour later, three times at most. The agent never goes back to an older version unless you choose it explicitly.

On Linux, `/usr` stays read-only for the agent: it prepares the verified binary under `/var/lib/hypha`, and the next start of the unit checks the signature again and installs it.

In a container, the agent never updates itself: change the image version.

## Self-hosting

The self-hosting kit serves the signed binaries and their signature under `/install/` on your server: your agents update from your premises, without reaching S33D.
