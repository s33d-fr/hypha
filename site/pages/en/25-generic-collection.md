---
id: generic-collection
path: /en/docs/generic-collection/
title: Generic collection: Prometheus, Nagios plugins and syslog
nav: Prometheus, Nagios, syslog
section: Integrations
order: 25
updated: 2026-11-20
description: The HYPHA agent's OpenMetrics and Prometheus reader and its profiles, running Nagios, Monitoring Plugins and Centreon plugins, receiving syslog from network devices.
lead: Three engines to monitor what has no dedicated integration: any software that publishes its metrics in Prometheus format, the thousands of existing Nagios and Centreon plugins, and logs sent over syslog.
---
## OpenMetrics and Prometheus reader

The agent reads `/metrics` pages in the Prometheus exposition format (text 0.0.4) or OpenMetrics 1.0 (text). One entry per target, under `integrations.openmetrics`:

```yaml
integrations:
  openmetrics:
    - profile: node_exporter              # http://127.0.0.1:9100/metrics by default
    - name: myapp
      url: https://app.example:8443/metrics
      interval: 30s                       # 30 s by default, at least 10 s
      timeout: 10s
      namespace: myapp                    # prefix of the HYPHA metrics
      metrics: [http_requests_total, "job_*", "re:queue_(depth|age)_.*"]
      exclude_metrics: ["go_*"]
      rename: {http_requests_total: http.requests}
      labels: [code, method]              # labels kept as tags; "*" = all
      tags: {team: payments}
      bearer_token_file: /etc/hypha/myapp.token   # read again at every scrape
      ca_file: /etc/hypha/internal-ca.pem # also cert_file, key_file, insecure
      max_series: 2000
```

Lists from several `conf.d` files add up: one file per application. Other headers go in `headers`; `username` and `password` give basic authentication. Header values and passwords are never shown by `hypha-agent check-config`.

### How metrics are translated

| Prometheus type | What HYPHA receives |
|---|---|
| gauge, unknown | the value as is |
| counter | a per-second rate (`byte_per_second` for `_bytes`), nothing on the first read or after a reset |
| histogram | `<name>.count` and `<name>.sum` (rates), `<name>.avg`, percentiles `<name>.p50`, `.p95`, `.p99` estimated like `histogram_quantile`; buckets with `histogram_buckets: true` |
| summary | `<name>.count`, `<name>.sum`, `<name>.avg` and every published quantile (`0.5` → `p50`, `0.999` → `p99_9`) |

HYPHA name: `<namespace>.<name>` (a counter loses its `_total` suffix), or `<namespace>.<rename>`. Prometheus labels outside `labels` are dropped, and series that only differ by them are **added up** (for example `node_cpu_seconds_total` without the `cpu` label gives the time of all cores). Every series carries `instance_name` and `endpoint`.

### Cardinality guard

Beyond `max_series` series for a target (2,000 by default), the agent keeps the first ones in alphabetical order, drops the rest, says so in the agent status and log, and publishes `openmetrics.series.dropped`. The "OpenMetrics series cap reached" monitor template warns when it lasts.

### Built-in profiles

A profile brings the port, path, selected metrics, their names and the useful labels. What the entry adds (`metrics`, `rename`, `labels`, `tags`) completes the profile.

| Profile | Port | Metrics (namespace) |
|---|---|---|
| `node_exporter` | 9100 | `node.load.1`, `node.cpu.seconds` by mode, `node.memory.*`, `node.filesystem.*` by mount point, `node.network.*` and `node.disk.*` by device, `node.procs.*`, `node.filefd.*`, `node.hwmon.temperature`, `node.time.offset` |
| `prometheus` | 9090 | `prometheus.tsdb.head_series`, `.tsdb.samples_appended`, `.http.requests` by code, `.http.request_duration.p95`, `.target.interval_length.p99`, `.rule.evaluation_failures`, `.notifications.dropped`, `.config.last_reload_successful`, `.process.resident_memory` |

Twenty-nine more profiles (Consul, Vault, etcd, Traefik, Kafka through the JMX exporter, GitLab, Argo CD…) are described in [Software exposing Prometheus](/en/docs/prometheus-software/).

### Agent check and metrics

- `openmetrics.can_connect`: critical when the target does not answer, answers with an error or returns an unreadable exposition (the protobuf format is not read).
- `openmetrics.series`, `openmetrics.series.dropped`, `openmetrics.scrape.duration` per target.

## Nagios, Monitoring Plugins and Centreon plugins

The agent runs Nagios-format plugins and turns them into a check and metrics:

```yaml
integrations:
  nagios:
    - name: root-disk
      command: check_disk -w 20% -c 10% -p /
      interval: 60s                        # at least 10 s
      timeout: 30s                         # at most the interval
    - name: local-http
      command: [check_http, -H, 127.0.0.1, -u, /health]
      user: nagios                         # account running the plugin (Linux)
    - name: centreon-load
      command: [/usr/bin/perl, /usr/lib/centreon/plugins/centreon_plugins.pl,
                --plugin=os::linux::local::plugin, --mode=load]
      tags: {role: web}
```

- Exit code 0, 1, 2, 3: `nagios.check` check OK, WARNING, CRITICAL, UNKNOWN, tagged `nagios_check` (the entry name), with the plugin's text. Any other code counts as UNKNOWN, a timeout as CRITICAL.
- Performance data (`'label'=value[unit];warn;crit;min;max`): `nagios.perfdata` metric tagged `perf_label`, plus `nagios.perfdata.warn`, `.crit`, `.min`, `.max` when the plugin gives them. Converted units: `s`, `ms`, `us`, `%`, `B`, `KB`, `MB`, `GB`, `TB`, `c`. `perfdata: false` turns them off.
- `nagios.duration`: duration of each run.

### Security

- Commands come **only** from the host's configuration files. The HYPHA server can neither send nor change them.
- On Linux, the agent refuses to run anything if a file declaring plugins, or the executable itself, is not owned by root or is writable by group or others (an UNKNOWN check says so).
- Direct execution, no shell: `$`, `*`, `|` or `;` stay ordinary characters. A bare plugin name is looked up in `/usr/lib/nagios/plugins`, `/usr/lib64/nagios/plugins`, `/usr/lib/monitoring-plugins`, `/usr/local/nagios/libexec`, `/usr/lib/centreon/plugins`, never in `PATH`.
- Reduced environment (system `PATH`, `LANG=C`), without the agent's variables; output capped at 64 KiB; on timeout, the plugin and its children are stopped; at most four plugins at once.
- On Windows: absolute path required; protect the agent's configuration folder with its permissions (administrators only).

Usual packages: `monitoring-plugins` (Debian, Ubuntu), `nagios-plugins-all` (EPEL); Centreon plugins: the open `centreon-plugins` repository (Perl, with `libjson-xs-perl`).

## Syslog reception

The agent can receive logs from your devices (firewalls, switches, routers, NAS, printers) and servers. **Off by default.**

```yaml
syslog:
  enabled: true
  udp: 0.0.0.0:514                 # empty = no UDP
  tcp: 0.0.0.0:514                 # empty = no TCP
  allowed_sources: [192.0.2.0/24]  # empty = any source
  max_connections: 64
  rate_limit: 2000                 # lines per second, beyond that dropped and counted
  parsers:
    - {match: 192.0.2.10, parser: kv}
  tags: {site: hq}
```

- Formats: RFC 3164 (BSD) and RFC 5424, rsyslog's ISO 8601 timestamps; over TCP, length-prefixed frames (octet counting, RFC 6587) or newline-terminated ones. A line that matches no format keeps all its text.
- The agent opens no firewall: allow the port yourself, and only from your devices. Port 80 is refused.
- Each line becomes a HYPHA log: source `syslog`, service = application name (otherwise `syslog`), status from the severity (0 `emergency` … 7 `debug`), `syslog_host` tag = header hostname, otherwise the sender's address. Attributes: `syslog.facility`, `syslog.severity`, `syslog.hostname`, `syslog.appname`, `syslog.procid`, `syslog.msgid`, `syslog.timestamp`, `syslog.structured_data`, `network.client.ip`. The time kept is the reception time (a device clock can be wrong).
- Parsing models: `parsers` designates, by address, network or hostname, the model the server applies on arrival (attribute `syslog.parser`). Shipped: `kv`, which splits `key=value` pairs (`action=deny srcip=203.0.113.9` becomes `@action`, `@srcip`). The usual parsers (sshd, sudo, HTTP access) apply as well.
- Metrics: `syslog.messages` by `syslog_host` (0 for a sender seen in the last 24 hours that went quiet), `syslog.dropped` by reason, `syslog.connections`; check `syslog.can_receive`.

Received logs are searched like the others: `source:syslog @syslog.hostname:fw01`, `source:syslog status:error`.

Models for firewalls and routers (Fortinet, Stormshield, pfSense, OPNsense, Palo Alto, Cisco ASA, SonicWall, WatchGuard, Check Point, MikroTik, Ubiquiti): see [Network and security devices (syslog)](/en/docs/syslog-devices/).

## Monitor templates and dashboards

**Collecte générique** family of the monitor catalogue: OpenMetrics target unreachable, series cap reached, Nagios plugin alerting, syslog reception failing, silent syslog device, dropped syslog lines, severe errors received by syslog.

The [dashboard catalogue](/en/docs/metrics/#ready-made-dashboard-catalogue) offers "Cibles OpenMetrics et Prometheus", "node_exporter (Prometheus)", "Serveur Prometheus", "Plugins Nagios et Centreon" and "Journaux reçus par syslog".
