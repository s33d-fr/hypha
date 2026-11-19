---
id: agent-linux
path: /en/docs/agent-linux/
title: Install the agent on Linux
nav: Linux
section: Install the agent
order: 10
updated: 2026-11-19
description: Install hypha-agent on Linux in one command, or from .deb and .rpm packages; check, update and uninstall it.
lead: A single binary with no dependency, for amd64 and arm64. It runs as a systemd service and uses less than one percent of a core.
---
## Requirements

- A distribution with systemd, on amd64 or arm64 (Debian, Ubuntu, Red Hat, Rocky, Alma, Fedora, 64-bit Raspberry Pi OS…).
- Outbound HTTPS access to `hypha.s33d.fr`, directly or through a proxy. The agent opens no port on the host.
- An ingestion key: **Settings › API keys**, or the **Create a key and insert it** button on the **Install the agent** page.

## Install in one command

```bash
curl -fsSL https://hypha.s33d.fr/install/install.sh | sudo HYPHA_API_KEY=hph_your_key bash
```

The script downloads the binary for your architecture, checks its SHA-256 sum, writes `/etc/hypha/hypha.yaml` (mode 600), installs the `hypha-agent.service` unit and starts it. It asks no question and can be run again: the same version does not restart the service.

Accepted variables:

| Variable | Purpose |
|---|---|
| `HYPHA_API_KEY` | ingestion key, required on first installation |
| `HYPHA_URL` | target server, `https://hypha.s33d.fr` by default; your own address when self-hosting |
| `HYPHA_TAGS` | tags added to everything the agent sends: `env:prod,role:web` or `{"env":"prod"}` |
| `HYPHA_HOSTNAME` | host name in HYPHA (system name by default) |
| `HYPHA_FORCE_CONF` | `1` rewrites `api_key` and `url` in an existing configuration |

`HYPHA_TAGS` and `HYPHA_HOSTNAME` only apply when the file is created: an existing configuration is never rewritten, except `api_key` and `url` with `HYPHA_FORCE_CONF=1`.

## Install from a package

`.deb` and `.rpm` packages are published next to the binary, under [hypha.s33d.fr/install/](https://hypha.s33d.fr/install/SHA256SUMS):

```bash
# Debian, Ubuntu (replace 0.4.21 with the version in version.txt)
curl -fsSLO https://hypha.s33d.fr/install/hypha-agent_0.4.21_amd64.deb
sudo apt install ./hypha-agent_0.4.21_amd64.deb

# Red Hat, Rocky, Alma, Fedora
sudo dnf install https://hypha.s33d.fr/install/hypha-agent-0.4.21-1.x86_64.rpm
```

The package installs the sample configuration. Set `api_key` in `/etc/hypha/hypha.yaml`, then:

```bash
sudo systemctl start hypha-agent
```

An agent installed from a package is updated by the package manager; the install script then refuses to replace it.

## Check

```bash
sudo hypha-agent status          # collector state and last send
sudo hypha-agent check-config    # merged configuration, secrets masked, errors with their file
systemctl status hypha-agent
journalctl -u hypha-agent -n 50
```

The host appears in **Infrastructure** within a minute. **Settings › Agents** shows its version, the state of each collector and the last error.

## What the agent collects by default

- System metrics every 15 seconds: CPU, memory, swap, disks, I/O, network, load, processes.
- The systemd journal (`journalctl`), resumed after the last successful send.
- A snapshot of processes and network connections every minute.
- Docker containers if `/var/run/docker.sock` exists.
- Hardware and software inventory at start, then every 6 hours.
- Failed systemd units and late timers, pending updates, clock, certificates, sensors and SMART disks when the tools are present.
- Detected software: PostgreSQL, MySQL, Redis, nginx, HAProxy, Postfix and the other [integrations](/en/docs/integrations/).

Everything is set in `/etc/hypha/hypha.yaml` and `/etc/hypha/conf.d/*.yaml`: see the [configuration reference](/en/docs/configuration/).

## Privileges and footprint

- The agent runs as `root` to read every log and each process's `/proc`.
- The systemd unit hardens it: `/usr`, `/boot` and `/etc` read-only for it, `/home` read-only, private `/tmp`, no privilege escalation; it only writes under `/var/lib/hypha`.
- Low priority (`Nice=10`, idle I/O class) and caps in case of runaway: 25% of one core, 256 MB of memory.
- If the server is unreachable, sends wait in a disk buffer (256 MB by default), then go out again.

## Update

- Script: run the install command again; only a new version restarts the service.
- Automatic: with `auto_update: true`, the agent follows the target version set in **Settings › Agents** and checks the signature of every binary. See [Fleet and updates](/en/docs/fleet/).
- Package: `apt upgrade` or `dnf upgrade` after downloading the new package.

## Uninstall

```bash
curl -fsSL https://hypha.s33d.fr/install/install.sh | sudo bash -s -- --uninstall
```

The configuration (`/etc/hypha`) and state (`/var/lib/hypha`) are kept. To erase everything: `sudo rm -rf /etc/hypha /var/lib/hypha`. An agent installed from a package is removed with `apt remove hypha-agent` or `dnf remove hypha-agent`.
