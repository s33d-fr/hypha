---
id: agent-docker
path: /en/docs/agent-docker/
title: Run the agent in a Docker container
nav: Docker
section: Install the agent
order: 12
updated: 2026-11-19
description: Run hypha-agent in a Docker container that watches the host and its containers; volumes, privileges and log files.
lead: The agent image watches the host that runs it, its processes, its network and its containers.
---
## Run

```bash
docker run -d --name hypha-agent --restart unless-stopped --network host --pid host \
  --cap-add SYS_PTRACE --security-opt apparmor=unconfined \
  -v /proc:/host/proc:ro -v /sys:/host/sys:ro -v /:/host/root:ro \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  -v hypha-agent:/var/lib/hypha \
  -e HYPHA_API_KEY=hph_your_key -e HYPHA_URL=https://hypha.s33d.fr \
  registre-vm.s33d.fr/hypha/agent-docker:0.4.21
```

The **Docker** tab of **Settings › Install the agent** gives the command with the current published version.

| Option | Why |
|---|---|
| `--network host`, `--pid host` | see the host's network and processes, not the container's |
| `--cap-add SYS_PTRACE`, `apparmor=unconfined` | link each connection to its process |
| `/proc`, `/sys`, `/` read-only | host system and disk metrics |
| `docker.sock` read-only | container list and metrics |
| `hypha-agent` volume | agent identity and send buffer, kept across restarts |

## Follow log files

The image does not read the host's systemd journal: for that, install the agent on the host ([Linux](/en/docs/agent-linux/)). To follow files, mount them, then mount a configuration that declares them:

```bash
-v /var/log/nginx:/host/log/nginx:ro \
-v /etc/hypha/hypha.yaml:/etc/hypha/hypha.yaml:ro
```

```yaml
logs:
  enabled: true
  journald: false
  files:
    - {path: /host/log/nginx/*.log, service: nginx, source: nginx}
```

`HYPHA_API_KEY` and `HYPHA_URL` passed with `-e` take precedence over the file.

## What changes in a container

- Updates come with the image (`docker pull` of a new version), never from the agent itself.
- Remote actions on the host's services are not available: the image has no `systemctl`.
- Private synthetic tests, SNMP, inventory and integrations work as on a host; their settings are mounted in `/etc/hypha/conf.d/`.

## OpenTelemetry and StatsD relay

With `--network host`, the host's applications reach the agent's OTLP relay on `127.0.0.1:4318` and StatsD on `127.0.0.1:8125/udp`. Enable `otlp.enabled: true` in the mounted configuration. See [Traces](/en/docs/traces/).
