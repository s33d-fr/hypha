---
id: integrations
path: /en/docs/integrations/
title: System, web and mail integrations
nav: System, web and mail
section: Integrations
order: 20
updated: 2026-11-20
description: HYPHA agent integrations: nginx, Apache, HAProxy, IIS, BIND, Postfix, Dovecot, rspamd, Exchange, OpenVPN, fail2ban, SSH, Wazuh, Suricata, certificates, SMART, sensors, clock, updates, systemd timers, VirtualBox, Btrfs, NFS.
lead: The agent recognises installed software and collects its metrics, with Datadog-compatible names. Most integrations turn themselves on.
---
## How it works

- `enabled: auto`: the integration turns on after a first successful read. Software absent: state "absent", no check, new attempt every 10 minutes.
- `enabled: true` forces the integration, `enabled: false` turns it off.
- Each integration publishes a `<name>.can_connect` check and never breaks the agent if the tool is missing.
- The state of each one is shown in **Settings › Agents**.

Settings go under `integrations.<name>` in [the configuration](/en/docs/configuration/). Databases have their own page: [Databases](/en/docs/databases/).

## Web servers and load balancers

| Integration | Source | Main metrics | Activation |
|---|---|---|---|
| `nginx` | `stub_status` page | `nginx.requests`, `nginx.connections.active`, `.reading`, `.writing`, `.waiting` | `enabled: true` and `url` |
| `apache` | `mod_status` (`?auto`) | `apache.net.hits`, `.request_per_s`, `.bytes_per_s`, `apache.performance.busy_workers`, `.idle_workers`, `apache.conns_total` | auto |
| `haproxy` | admin socket (`show stat`) or CSV page | `haproxy.frontend.*`, `haproxy.backend.*`, `haproxy.server.*`, `haproxy.backend_up`, `haproxy.server.up` | auto |
| `iis` (Windows) | performance counters | `iis.requests_per_s`, `iis.current_connections` | auto |

For nginx, open the status page to the loopback only:

```nginx
location /nginx_status { stub_status; allow 127.0.0.1; deny all; }
```

```yaml
integrations:
  nginx: {enabled: true, url: http://127.0.0.1/nginx_status}
  apache: {enabled: auto, url: "http://127.0.0.1/server-status?auto"}
  haproxy: {enabled: auto, socket: /run/haproxy/admin.sock}   # or url: http://127.0.0.1:8404/stats;csv
```

nginx, Apache and HAProxy access logs are parsed on arrival: method, path, HTTP code, duration, client address. See [Logs](/en/docs/logs/#parsing-on-arrival).

PHP-FPM, Varnish, Squid, Lighttpd, Gunicorn and Supervisor, as well as PowerDNS, OpenLDAP, Keycloak, Nexus, Harbor, ZooKeeper and Ceph, have their dedicated probes: see [Applications and services](/en/docs/applications-and-services/).

Jenkins, GitHub (and GitHub Enterprise Cloud), the Zot registry and Odoo: see [CI, forges and business apps](/en/docs/ci-forges-and-business-apps/).

## DNS

`bind` reads the JSON statistics channel (`statistics-channels { inet 127.0.0.1 port 8053; };`), otherwise `rndc status`. Metrics: `bind.queries` by type, `bind.responses.nxdomain`, `bind.responses.servfail`, `bind.zone.serial` per zone, `bind.recursive_clients`.

Pi-hole, AdGuard Home and Unbound, as well as Caddy, MinIO, Mosquitto, Proxmox Backup Server, Borg and restic, have their own page: [Network, storage and backups](/en/docs/network-storage-backups/).

## Mail

| Integration | Source | Metrics and checks |
|---|---|---|
| `postfix` | `postqueue -j` and delivery logs | `postfix.queue.size` per queue (active, deferred, hold, incoming), `postfix.delivery.sent`, `.deferred`, `.bounced` |
| `dovecot` | `doveadm stats dump`, `doveadm who` | `dovecot.auth.success`, `.failures`, `dovecot.connections` |
| `rspamd` | local `/stat` page | `rspamd.messages.scanned`, `.learned`, `.spam`, `.ham`, `rspamd.actions` per action, `rspamd.connections` |
| `exchange` (Windows) | performance counters | `exchange.queue.<state>` per transport depot, `exchange.database.mounted` (and check), `exchange.imap.connections`; checks for poison and suspended messages |

To check mail end to end (SMTP banner, IMAP, send then fetch, SPF, DKIM, DMARC, blocklists), see [mail synthetic tests](/en/docs/synthetics/#mail).

## Security and access

- `openvpn`: status files (server: connected clients; client: tunnel throughput): `openvpn.clients.connected`, `openvpn.client.bytes_in`, `.bytes_out`. A file frozen for 10 minutes turns `openvpn.can_connect` critical.
- `fail2ban`: `fail2ban-client status`, read every 5 minutes: `fail2ban.banned`, `fail2ban.failed` per jail.
- Authentication logs: `sshd`, `sudo`, `su` and `fail2ban` lines are analysed on arrival (`evt.name`, `evt.outcome`, `usr.name`, `network.client.ip`). The "SSH brute-force attack" template relies on them.
- `ssh`: availability and banner of SSH servers, without authentication (the agent reads the banner, sends its own and closes; the server logs a connection closed before authentication). One `ssh.can_connect` check per target: critical if the port does not answer or does not speak SSH, warning if the banner does not match `expect_banner` or announces protocol 1; metric `ssh.response_time`. Inactive until targets are given:

```yaml
integrations:
  ssh:
    targets:
      - {name: bastion, address: "bastion.example:22", expect_banner: "^SSH-2.0-OpenSSH_9"}
      - {address: 192.0.2.10}          # port 22 by default
```

- `wazuh`: REST API of the Wazuh manager (port 55000), with an API account holding the `readonly` role (`url`, `username`, `password`, `insecure: true` for its self-signed certificate). Manager daemons (`wazuh.manager.daemons`: critical if analysisd, remoted, wazuh-db, modulesd… is stopped), agents by connection status (`wazuh.agents`, `status` tag), events received, processed and dropped, alerts written, analysisd and remoted queue usage, remoted sessions and bytes.
- `suricata`: the `eve.json` file (default `/var/log/suricata/eve.json`), followed as it grows: latest `stats` record (captured and dropped packets, drop share `suricata.capture.drop_rate`, decoding, flows, TCP, detection, flows per application protocol, memory) and alerts counted by severity and action (`suricata.alerts`). `suricata.can_read` check: critical if the latest `stats` record is older than `stale_after` (5 min; Suricata stopped, or `stats` missing from the `eve-log` types). On the first pass, only the last 4 MiB of the file are read.

## Certificates

`x509` watches the certificates installed on the host: `tls.cert.days_left` and the `tls.cert.expiry` check (warning under 21 days, critical under 7 days), read every hour.

- Paths read by default: `/etc/letsencrypt/live/*/cert.pem`, `/etc/ssl/*/fullchain.pem`.
- Discovery (`discover`, on by default): the certificates declared by Apache, nginx, Postfix, Dovecot and HAProxy.
- Windows: the computer store (`stores: [My]`), without certificates replaced by a newer one or expired for more than 30 days.

The **Certificates** page of the interface gathers every certificate collected by agents and synthetic tests, sorted by expiry.

## Hardware and system

| Integration | Source | Metrics and checks |
|---|---|---|
| `smart` | `smartctl`, every 30 min, without waking a sleeping disk | `smart.health`, `.temperature`, `.reallocated_sectors`, `.power_on_hours`, `.wear_level` |
| `hwmon` | `/sys/class/hwmon` | `system.temperature`, `system.fan.rpm` |
| `ntp` | `chronyc`, `timedatectl` or `ntpq`; `w32tm` on Windows | `ntp.offset`, `ntp.in_sync` check |
| `updates` | package manager cache (no `apt update`), read every hour | `system.updates.pending`, `.security`, `system.reboot_required`; Windows: `windows.updates.pending` check |
| `systemd_timers` | `systemctl` | `systemd.timer` check: critical if the last run failed, warning if an active timer has not run for two periods |
| `virtualbox` | accounts' `VirtualBox.xml` and processes | `virtualbox.vms.running`, `.total`, `virtualbox.vm.running` check for autostart VMs |
| `btrfs` | `/sys/fs/btrfs` (error counters: kernel 5.14 and later, otherwise `btrfs device stats`) | per mounted filesystem: `btrfs.allocated`, `.used`, `.usage` per type (`usage_type`) and profile (`replication_type`), `btrfs.unallocated` and `.unallocated.pct`, `btrfs.device.errors`; `btrfs.health` check: critical if a device is missing, warning if error counters are not zero |
| `nfs` | client: `/proc/1/mountstats`; server: `/proc/net/rpc/nfsd` | client, per mount: operations, bytes read and written, average round-trip and execution times, retransmissions, timeouts, usage; `nfs.mount` check (critical if `statfs` does not answer within `timeout`, 5 s: hung mount). Server: nfsd threads, RPC calls and bad calls, bytes, operations per version, exports; `nfs.server` check |

An NFS server in a container keeps its statistics in its own network namespace: `integrations.nfs.server_stats: /proc/<container pid>/net/rpc/nfsd`. `nfs.mounts` restricts the followed mounts (patterns).

## Processes, ports and pages

Without any specific integration, the agent can also check:

- that a process is present (`checks.processes`): `process.up` check and `process.count` metric;
- an HTTP page (`checks.http`) or a TCP port (`checks.tcp`), from the host's network.

## Your own metrics

The agent listens for StatsD in DogStatsD format on `127.0.0.1:8125/udp`, aggregates over 15 seconds and sends everything as regular metrics:

```bash
echo "shop.orders:1|c|#env:prod" | nc -u -w0 127.0.0.1 8125
```

For an application that already publishes Prometheus metrics, Nagios or Centreon plugins, or logs sent over syslog, see [Generic collection](/en/docs/generic-collection/).

Counters (`c`) become sums, gauges (`g`) values, timers (`ms`, `h`) distributions (`.avg`, `.max`, `.p50`, `.p95`, `.p99`, `.count`). For OpenTelemetry metrics, see [Traces](/en/docs/traces/).
