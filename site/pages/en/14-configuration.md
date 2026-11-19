---
id: configuration
path: /en/docs/configuration/
title: Agent configuration reference
nav: Agent configuration
section: Install the agent
order: 14
updated: 2026-11-19
description: Every key of hypha.yaml and conf.d: key, server, tags, logs, processes, network, checks, integrations, SNMP, actions, updates.
lead: One main file, additions in conf.d, and a command that checks it all before a restart.
---
## Where the configuration lives

| System | Main file | Additions |
|---|---|---|
| Linux | `/etc/hypha/hypha.yaml` (600) | `/etc/hypha/conf.d/*.yaml` |
| Windows | `C:\ProgramData\Hypha\hypha.yaml` | `C:\ProgramData\Hypha\conf.d\*.yaml` |
| Docker | `/etc/hypha/hypha.yaml` in the container | mounted `/etc/hypha/conf.d/` |

Reading rules:

- `conf.d/*.yaml` files are read after the main file, in alphabetical order, and merged key by key: an object is merged, a list replaces the list, a missing key keeps its value.
- The `HYPHA_API_KEY` and `HYPHA_URL` variables take precedence over the files.
- An unreadable or invalid file stops the agent: never a half-read configuration. An unknown key is only a warning.

Before every restart:

```bash
sudo hypha-agent check-config
```

The command shows the merged configuration, secrets masked, and names the source file of each error.

## General keys

| Key | Default | Purpose |
|---|---|---|
| `api_key` | (required) | ingestion key, `hph_` followed by 40 characters |
| `url` | `https://hypha.s33d.fr` | intake server; your own address when self-hosting |
| `hostname` | system name | host name in HYPHA |
| `tags` | `{}` | tags added to everything the agent sends; keys `a-z 0-9 _ . - /` |
| `interval` | `15s` | metric and check interval (1 s minimum) |
| `tls_skip_verify` | `false` | only disable certificate checks for a self-hosted server with an internal certificate |
| `proxy` | (empty) | HTTP proxy; empty = `HTTPS_PROXY` and `NO_PROXY` variables |
| `buffer.dir`, `buffer.max_mb` | `/var/lib/hypha/buffer`, `256` | disk buffer for failed sends; the oldest go beyond the limit |

## Logs

```yaml
logs:
  enabled: true
  journald: true                       # Linux: systemd journal
  windows_eventlog: [System, Application]
  files:
    - {path: /var/log/nginx/*.log, service: nginx, source: nginx}
    - {path: /srv/app/logs/*.json, service: shop}
  exclude_patterns: ["GET /health"]    # regular expressions, lines never sent
```

A JSON line is decoded: `message` or `msg` becomes the message, `level` or `severity` the status, the rest goes into attributes. A message is truncated at 32 KiB. To reduce volume on the server side, see also [exclusion filters](/en/docs/logs/#exclusion-filters-and-sampling).

## Processes, network, containers

```yaml
processes:
  enabled: true
  include: ["^java", "gunicorn"]   # always collected, on top of the top 30 by CPU and by memory
network:
  enabled: true
  # exclude_interfaces: [lo, "docker*", "veth*", "br-*", "virbr*", "cali*", "flannel*", "cni*"]
containers:
  enabled: auto                     # auto = if /var/run/docker.sock exists
```

## Checks

```yaml
checks:
  systemd_units: [nginx.service]    # followed; a failed unit is ALWAYS reported
  windows_services: [W3SVC]         # followed; a stopped automatic service is reported
  windows_services_exclude: ["^Veeam"]
  windows_auto_services: auto       # auto, always, never
  http:
    - {name: site, url: "https://example.com/", expect_status: 200, timeout: 5s}
  tcp:
    - {name: db, address: "192.0.2.5:5432", timeout: 3s}
  processes:
    - {name: nginx}
    - {name: gunicorn, match: contains, min: 2}
    - {name: "^php-fpm[0-9.]*$", match: regex, min: 1, max: 1}
```

HTTP and TCP checks start from the host: they see internal addresses. For scheduled tests with history and assertions, see [synthetic tests](/en/docs/synthetics/).

## Integrations

Each integration turns itself on when its software is detected (`enabled: auto`), or is forced (`true`) or turned off (`false`). Its state shows in **Settings › Agents**: ok, absent, disabled, or the error met. Each one is described on the [Integrations](/en/docs/integrations/), [Databases](/en/docs/databases/), [SNMP](/en/docs/snmp/), [Clouds](/en/docs/cloud/), [Virtualization](/en/docs/virtualization/) and [Kubernetes](/en/docs/kubernetes/) pages.

### Several instances

To read several targets of one integration (ovhcloud, mysql, redis, memcached, rabbitmq, mongodb, opensearch, kubernetes, libvirt, proxmox, aws, scaleway, azure, gcp), give an `instances` list. Each entry has a `name` (lowercase letters, digits, `-` and `_`), starts from the integration's defaults, and adds up across `conf.d` files. Its data carries the `instance_name` tag.

```yaml
integrations:
  redis:
    instances:
      - {name: sessions, address: "127.0.0.1:6379"}
      - {name: queue, address: "127.0.0.1:6380", password: "…"}
```

## Application metrics and traces

```yaml
statsd:
  enabled: true
  listen: 127.0.0.1:8125     # DogStatsD format: name:value|g|c|ms|h|s|#tag:value
otlp:
  enabled: false
  listen: 127.0.0.1:4318     # OTLP/HTTP: the agent relays to HYPHA, adding the key
```

Listening outside the loopback triggers a warning: it opens the port to the network.

## Control channel, updates, actions

```yaml
control:
  enabled: true
  interval: 30s            # the agent polls the server; no port open on the host
auto_update: false         # follow the target version set in Settings › Agents
actions:
  enabled: false
  allow: [restart_service, start_service, stop_service]   # also: run_script, update_agent
  services: ["*"]
  deny_services: ["sshd*", "ssh*", "hypha-agent*", "systemd-*"]
  scripts:
    clear-cache: /etc/hypha/scripts/clear-cache.sh
  timeout: 120s
synthetics:
  enabled: false           # private synthetic test location
  max_concurrent: 4
inventory:
  enabled: true
  interval: 6h
  packages: true
```

Actions are off by default. The agent's own service is never touched, whatever the list. A named script must have an absolute path, belong to root and be writable neither by the group nor by others; it runs without arguments and its output is capped at 64 KiB. See [Explain and repair](/en/docs/explain-and-repair/) and [Fleet and updates](/en/docs/fleet/).

## SNMP

```yaml
snmp:
  enabled: true
  interval: 60s
  devices:
    - {name: rack-1-switch, address: 192.0.2.10, version: 2c, community: readonly, tags: {role: switch}}
  traps: {enabled: false, listen: "0.0.0.0:162", community: public}
```

Versions, profiles and SNMPv3 settings are on the [SNMP](/en/docs/snmp/) page. SNMP secrets stay in the agent's configuration: they never go to the server.
