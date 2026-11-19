---
id: explain
path: /en/docs/explain-and-repair/
title: Outage explanation and one-click repair
nav: Explain and repair
section: Alert and act
order: 46
updated: 2026-11-19
description: How HYPHA explains an outage with its evidence, in your language, with S33D's AI model, then proposes a named action that you approve in one click and the agent runs.
lead: HYPHA tells you what is happening, the probable cause and its evidence, the fix. When the fix is a known action, it proposes it: a human approves, the agent runs it.
---
{{video:4-alerte-expliquee}}

## Explain

The **Explain** button sits on an alert (Problems view, overview, monitors), on a host page, on a log line's details, on a trace and on an application error.

HYPHA then gathers, within the workspace and for the object concerned:

- the host: system, cores, memory, tags, last contact;
- the monitor: query, thresholds, state, value and its ten latest changes;
- the host's non-OK checks;
- its logs from the last 30 minutes, errors first, repeated messages grouped;
- its events from the last 24 hours;
- its metrics from the last 30 minutes, summarised (CPU, memory, load, disks, swap, network);
- its most demanding processes;
- for a trace, the error spans and the critical path.

The model answers in four parts: **What is happening**, **Probable cause** (with its evidence: a log line, a measured value), **Fix** (commands for the host's system), **Preventing it**. It must not make anything up; if the evidence is not enough, it says so and states what to check. The panel also shows the model used, the duration and the context read.

### Where the model runs

- **Hosted service**: on S33D's GPU servers, in France. Data sent to the model goes to no third party.
- **Self-hosting**: on your own Ollama-style model server (`HYPHA_LLM_URL`). Without a model server, the button does not appear.

Limit: 60 explanations per hour per workspace; the monthly number depends on the plan (see [pricing](/en/pricing/)).

## Repair in one click

HYPHA reads the **Fix** part again. If it finds an action it knows (`systemctl restart|start|stop <unit>`, `Restart-Service <service>`, `service <name> restart`), it proposes it as a button, for example "Restart shop-worker.service on web-01", on two conditions: the host's agent accepts that action, and the unit or service exists on the host.

1. A click opens a confirmation: action, host, reason.
2. If you are an administrator, the action is created and approved in the same step. Otherwise, it waits for an administrator's approval in **Settings › Actions**.
3. The agent receives it through its control channel, checks that it is allowed, runs it, then returns the exit code and the output.
4. Follow-up is live: pending, sent, running, succeeded or failed. The agent sends the unit's state right away.

An unapproved action expires after one hour; an approved action that the agent has not picked up expires after 10 minutes. Everything goes to the [audit log](/en/docs/accounts-and-roles/#audit-log).

## What the agent accepts

Actions are **off by default**. On each host, you decide:

```yaml
actions:
  enabled: true
  allow: [restart_service, start_service]        # allowed actions
  services: ["nginx*", "shop-*"]                 # targeted units or services
  deny_services: ["sshd*", "ssh*", "hypha-agent*", "systemd-*"]
  scripts:
    clear-cache: /etc/hypha/scripts/clear-cache.sh
  timeout: 120s
```

- No free command: only named actions (`restart_service`, `start_service`, `stop_service`, `run_script`, `update_agent`).
- An action outside the list, a forbidden service or an expired action are refused by the agent, with the reason.
- The agent's own service is never touched.
- A script is designated by its name; its path comes from the agent's configuration, never from the server. It runs without arguments, its output is capped at 64 KiB.
- In a container, actions on the host's services are not available.

An action can also be proposed by hand, through the proposal form (action, unit or script, reason) or the [API](/en/docs/api/). History and pending actions are in **Settings › Actions**.
