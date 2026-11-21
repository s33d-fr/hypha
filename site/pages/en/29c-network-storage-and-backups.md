---
id: network-storage-backups
path: /en/docs/network-storage-backups/
title: "Self-hosted network, storage and backups: Pi-hole, AdGuard Home, Unbound, Caddy, MinIO, Mosquitto, Proxmox Backup Server, Borg, restic"
nav: Network, storage and backups
section: Integrations
order: 29
updated: 2026-11-21
description: Monitor self-hosted network and data services with HYPHA - Pi-hole and AdGuard Home (DNS filtering), Unbound, Caddy, MinIO, the Mosquitto MQTT broker, Proxmox Backup Server, Borg and restic (age of the last backup).
lead: DNS queries and blocking, upstream servers, object storage capacity, MQTT clients, and above all the age and state of the last backup of every group or repository, read-only.
---
## At a glance

| Software | Collection | Activation | Namespace | Tested on |
|---|---|---|---|---|
| Pi-hole | Pi-hole 6 REST API (`/api`) | auto if `/etc/pihole` exists, or `password` set | `pihole.*` | Pi-hole 6.6 (FTL 6.9) |
| AdGuard Home | `/control` API (web interface account) | as soon as `username` and `password` are set | `adguard.*` | AdGuard Home 1.0.0 |
| Unbound | `unbound-control stats_noreset` | auto if `/etc/unbound/unbound.conf` exists | `unbound.*` | Unbound 1.26.2 (Debian 13) |
| Caddy | admin API (`:2019`) + OpenMetrics profile `caddy` | probe auto on `127.0.0.1:2019`; profile to declare | `caddy.*` | Caddy 2.11.9 |
| MinIO | OpenMetrics profiles `minio` and `minio_bucket` | to declare | `minio.*` | MinIO RELEASE.2025-10-15T17-29-55Z |
| Mosquitto | `$SYS` topics over MQTT 3.1.1 | auto if `/etc/mosquitto` exists, or `username` set | `mosquitto.*` | Mosquitto 2.1.4 |
| Proxmox Backup Server | API (`:8007`), token with the Audit role | as soon as the token is set | `proxmox_backup.*` | PBS 4.2.14 |
| Borg | `borg list --json` (and `borg info`) | as soon as a repository is declared | `borg.*` | Borg 1.4.4 (server 1.4.0) |
| restic | `restic snapshots --json` (and `restic stats`) | as soon as a repository is declared | `restic.*` | restic 0.18.1, rest-server |

Each piece of software was run in an S33D test environment, in a container, with real data: real DNS queries, real backups, then provoked failures (interrupted backup, corrupted chunk, stopped upstream). The probes accept **instances** (`instances:`, one named entry per extra server) and **fixed tags** (`tags:`) added to all their metrics and checks. Nothing is ever written to this software.

## Pi-hole

Pi-hole 6 serves a REST API on its web server (ports 80 and 443 by default). Create an **app password** (Settings › Web interface / API › Configure app password): it skips two-factor authentication and can be removed without touching the interface password.

```yaml
integrations:
  pihole:
    url: http://127.0.0.1           # or https://127.0.0.1 with insecure: true (self-signed certificate)
    password: "app password"
```

The agent opens a session on the first collection, keeps it while Pi-hole accepts it and closes it when it stops: Pi-hole limits the number of open sessions. A Pi-hole without a password is read without `password`.

| Metric | Meaning |
|---|---|
| `pihole.queries.total`, `.blocked`, `.cached`, `.forwarded`, `.unique_domains` | over the last 24 hours (FTL sliding window) |
| `pihole.queries.percent_blocked` | share of blocked queries (%) |
| `pihole.queries.frequency` | queries per second (recent FTL average) |
| `pihole.queries.by_status` (`status`), `pihole.queries.by_reply` (`reply`) | queries by handling (gravity, cache, forwarded…) and by reply |
| `pihole.clients.active`, `pihole.clients.total` | clients |
| `pihole.gravity.domains_blocked`, `pihole.gravity.age` | listed domains, seconds since the last list update |
| `pihole.blocking.enabled` | 1 when blocking is on |
| `pihole.messages`, `pihole.messages.by_type` | FTL warnings (Tools › Diagnosis) |
| `pihole.updates_available` | components (core, web, FTL) for which Pi-hole reports a newer version |

Checks: `pihole.can_connect` and `pihole.blocking` (warning: blocking disabled, with the remaining time; critical: blocking failed).

## AdGuard Home

The agent reads `/control/status`, `/control/stats` and `/control/filtering/status` with the web interface account, over basic authentication. It never calls `/control/version.json` (which queries AdGuard's servers) nor any write route.

```yaml
integrations:
  adguard:
    url: http://127.0.0.1:3000
    username: admin
    password: "…"
```

| Metric | Meaning |
|---|---|
| `adguard.queries`, `.queries.blocked`, `.queries.percent_blocked` | over the AdGuard Home statistics window (24 h by default) |
| `adguard.queries.safebrowsing`, `.safesearch`, `.parental` | answers replaced by safe browsing, safe search, parental control |
| `adguard.processing_time` | average processing time (ms) |
| `adguard.upstream.responses`, `adguard.upstream.avg_time` (`upstream`) | answers and average time (ms) of each upstream server |
| `adguard.filter.rules`, `adguard.filter.age` (`filter`) | rules and age (s) of each enabled list |
| `adguard.filters.enabled`, `.rules`, `.empty`, `.oldest_update_age` | enabled lists, total rules, empty or never downloaded lists, oldest update |
| `adguard.running`, `adguard.protection_enabled`, `adguard.filtering.enabled` | 1/0 |

Checks: `adguard.can_connect` and `adguard.protection` (warning: protection disabled; critical: DNS server stopped).

## Unbound

Enable the control channel (the Debian package already does, on a Unix socket), and preferably extended statistics:

```text
server:
    extended-statistics: yes
remote-control:
    control-enable: yes
    control-interface: /run/unbound.ctl
```

```yaml
integrations:
  unbound:
    command: unbound-control                   # "docker exec unbound unbound-control" in a container
    # config_file: /etc/unbound/unbound.conf   # passed with -c
```

The agent runs `unbound-control stats_noreset` every 30 seconds, without a shell: counters are not reset. Counters give a per-interval delta; if another tool reads `stats` (which resets), a single interval is lost.

- Counters: `unbound.num.queries`, `.cachehits`, `.cachemiss`, `.prefetch`, `.expired`, `.recursivereplies`, `unbound.num.answer.rcode` (`rcode`), `unbound.num.query.type` (`query_type`), `unbound.num.answer.secure`, `.bogus`, `unbound.num.rrset.bogus`, `unbound.requestlist.exceeded`, `.overwritten`, `unbound.unwanted.queries`, `.replies`, `unbound.num.query.tcp`, `.tls`, `.https`, `.ipv6`.
- Gauges: `unbound.cache.hit_rate` (%), `unbound.requestlist.avg`, `.max`, `.current`, `unbound.recursion.time.avg`, `.median` (s), `unbound.time.up`, `unbound.mem.cache.rrset`, `.message`, `unbound.msg.cache.count`, `unbound.rrset.cache.count`, `unbound.infra.cache.count`, `unbound.key.cache.count`.

Check `unbound.can_connect`: critical when `unbound-control` fails (Unbound stopped, control channel disabled, unreadable keys).

## Caddy

Caddy serves its Prometheus metrics on its admin API (`/metrics`, port 2019). HTTP server metrics need the global `metrics` option:

```text
{
	metrics
}
```

```yaml
integrations:
  openmetrics:
    - profile: caddy                 # http://127.0.0.1:2019/metrics
  caddy:
    url: http://127.0.0.1:2019       # admin API probe (auto)
```

Profile `caddy`: `caddy.http.requests`, `caddy.http.requests_in_flight`, `caddy.http.request_errors` (by `server` and `handler`), `caddy.http.request.duration` (histogram: `.count` by `code` = responses per code, `.p95`…), `caddy.http.response.duration`, `caddy.http.request.size`, `caddy.http.response.size`, `caddy.reverse_proxy.upstream.healthy` (1/0 per `upstream`), `caddy.config.last_reload_successful`, `caddy.config.last_reload_success_timestamp`, `caddy.process.resident_memory`, `caddy.go.goroutines`.

The `caddy` probe only reads (GET) `/config/apps/http/servers` (`caddy.servers`, `caddy.routes`) and `/reverse_proxy/upstreams` (`caddy.upstream.requests`: requests in progress, `caddy.upstream.fails`: failures counted by passive health checks, `caddy.upstreams`, `caddy.upstreams.failing`). Check `caddy.can_connect`. The admin API must never be open to the network.

## MinIO

MinIO serves its metrics on `/minio/v2/metrics/cluster` (cluster) and `/minio/v2/metrics/bucket` (per bucket). Create MinIO's Prometheus token, then store the `bearer_token` value in a file only the agent can read:

```text
mc admin prometheus generate myalias cluster
```

```yaml
integrations:
  openmetrics:
    - profile: minio                 # http://127.0.0.1:9000/minio/v2/metrics/cluster
      bearer_token_file: /etc/hypha/minio.token
    - profile: minio_bucket          # /minio/v2/metrics/bucket, same token
      bearer_token_file: /etc/hypha/minio.token
```

Without a token MinIO answers 403, unless `MINIO_PROMETHEUS_AUTH_TYPE=public`. In a cluster, one agent is enough (the cluster endpoint aggregates every node; `server` tag).

- Health: `minio.cluster.health.status` (1 = healthy), `minio.cluster.nodes.online`, `.offline`, `minio.cluster.drives.online`, `.offline`, `.total`, `minio.cluster.erasure_set.healing_drives`, `.online_drives`, `.status`, `.write_quorum` (`pool`, `set`).
- Capacity: `minio.cluster.capacity.usable.free`, `.total`, `minio.cluster.capacity.raw.free`, `.total` (bytes).
- Usage (after the first MinIO scanner pass): `minio.cluster.usage.objects`, `.bytes`, `.versions`, `minio.cluster.buckets`; per bucket: `minio.bucket.usage.bytes`, `.objects`, `minio.bucket.requests`, `minio.bucket.traffic.received`, `.sent` (`bucket`).
- S3 API: `minio.s3.requests` (`api`), `minio.s3.requests.errors`, `.4xx_errors`, `.5xx_errors`, `.inflight`, `.waiting`, `minio.s3.requests.rejected.auth`, `.invalid`, `.timestamp`, `.header`, `minio.s3.traffic.received`, `.sent`.
- Nodes: `minio.node.uptime`, `minio.node.process.resident_memory`, `minio.node.fd.open`, `.limit`.

MinIO no longer publishes images or binaries of its free edition: the tested version was built from its official sources.

## Mosquitto

The agent connects to the broker over MQTT 3.1.1, subscribes to `$SYS/broker/#` at QoS 0, reads the retained values, then disconnects: it never publishes. Give it an account that only reads `$SYS`:

```text
# mosquitto_passwd -b /etc/mosquitto/passwd hypha "…"
# ACL (acl_file):
user hypha
topic read $SYS/#
```

```yaml
integrations:
  mosquitto:
    url: mqtt://127.0.0.1:1883       # mqtts://host:8883 over TLS (ca_file, insecure)
    username: hypha
    password: "…"
```

- Gauges: `mosquitto.clients.connected`, `.disconnected`, `.total`, `.expired`, `mosquitto.subscriptions`, `mosquitto.retained_messages`, `mosquitto.messages.stored`, `mosquitto.store.messages.count`, `.bytes`, `mosquitto.heap.current`, `.maximum`, `mosquitto.connections.sockets`, `mosquitto.uptime`, `mosquitto.load.connections.1min`, `mosquitto.load.messages.received.1min`, `.sent.1min`.
- Counters (per-interval delta): `mosquitto.messages.received`, `.sent`, `mosquitto.publish.messages.received`, `.sent`, `.dropped`, `mosquitto.publish.bytes.received`, `.sent`, `mosquitto.bytes.received`, `.sent`.

Check `mosquitto.can_connect`: account refused (CONNACK 4 or 5), subscription refused by the ACL, or no `$SYS` topic received (`sys_interval 0`).

## Proxmox Backup Server

Create a user and an API token with the **Audit** role, on path `/`, for the user AND for the token (a token's rights are the intersection of both):

```text
proxmox-backup-manager user create hypha@pbs
proxmox-backup-manager acl update / Audit --auth-id hypha@pbs
proxmox-backup-manager user generate-token hypha@pbs readonly
proxmox-backup-manager acl update / Audit --auth-id 'hypha@pbs!readonly'
proxmox-backup-manager cert info | grep Fingerprint
```

```yaml
integrations:
  proxmox_backup:
    url: https://127.0.0.1:8007
    token_id: "hypha@pbs!readonly"
    token_secret: "…"                # value returned by generate-token
    fingerprint: "af:c8:fd:…"        # SHA-256 fingerprint of the certificate (or ca_file, or insecure: true)
    # interval: 1m
    # max_groups: 500                # backup groups followed per datastore
```

The subscription is never read. On every collection:

- Datastores: `proxmox_backup.datastore.total`, `.used`, `.avail`, `.usage` (%), `.days_until_full` (PBS estimate), `.chunks`, `.bad_chunks`, `.gc_pending`, `.dedup_factor`, `.groups`, `.last_backup.age` (`datastore`).
- Backup groups (VM, container, host; all namespaces): `proxmox_backup.group.last_backup.age` (seconds since the last backup) and `proxmox_backup.group.snapshots`, tags `datastore`, `backup_type`, `backup_id`, `pbs_namespace`.
- Verifications (every 15 min): `proxmox_backup.datastore.snapshots`, `.snapshots.verify_failed`, `.snapshots.unverified`.
- Tasks failed since the previous collection: `proxmox_backup.tasks.failed` (`pbs_task`) and one event per task.
- Server: `proxmox_backup.node.cpu`, `.memory.used`, `.memory.total`, `.root.used`, `.root.total`, `.uptime`, `.load.1`.

Checks: `proxmox_backup.can_connect`, `proxmox_backup.datastore` (critical: datastore in error), `proxmox_backup.job` (last run of every verify, prune, sync and garbage collection job; critical on failure), `proxmox_backup.backup` (last backup task of every group over 7 days, re-read every 5 min: critical on failure, cleared as soon as the next one succeeds).

## Borg and restic

The agent runs the tool installed on the machine, without a shell and **without a lock**: `borg --bypass-lock`, `restic --no-lock`. A backup, prune or check in progress is never hindered or delayed by monitoring. Every repository is re-read every 15 minutes.

```yaml
integrations:
  borg:
    repositories:
      - name: server
        repository: ssh://borg@backup.example/./repo
        passphrase_file: /etc/hypha/borg-server.pass       # root, mode 600 or 400
        rsh: "ssh -i /root/.ssh/borg -o BatchMode=yes"
  restic:
    repositories:
      - name: nas
        repository_file: /etc/hypha/restic-nas.repo        # address with credentials (rest:, s3:…)
        password_file: /etc/hypha/restic-nas.pass
      - name: local
        repository: /srv/restic
        password_file: /etc/hypha/restic-local.pass
    # stats: true      # repository size (borg info, restic stats), re-read every 6 h
    # interval: 15m
    # timeout: 2m
```

- The passphrase never goes on the command line nor in a visible environment variable: Borg gets it on its standard input (`BORG_PASSPHRASE_FD=0`), restic reads its `--password-file` itself. A passphrase, password or address file open to the group or to everyone, or not owned by root, is refused.
- `env:` passes variables without secrets (for instance `AWS_SHARED_CREDENTIALS_FILE` pointing to a root-only file); a password inside a repository address is refused (use `repository_file`).
- Under systemd, the tool runs in its own bounded unit (512 MiB, half a core, low I/O priority, limited to `timeout`): a large repository does not weigh on the agent. Borg keeps its working directory in `/var/lib/hypha/borg`.
- restic retries for several minutes against an unreachable server: the collection stops at `timeout` and the check turns critical.

| Metric | Meaning |
|---|---|
| `borg.last_archive.age` | seconds since the start of the last complete archive |
| `borg.archives`, `borg.checkpoints` | complete archives, checkpoints (`.checkpoint`) |
| `borg.repository.last_modified.age` | seconds since the last write to the repository |
| `borg.repository.unique_csize`, `.unique_size`, `.total_size` | with `stats: true`: stored size (deduplicated and compressed), deduplicated, total |
| `restic.last_snapshot.age` | seconds since the last snapshot |
| `restic.snapshots`, `restic.host.last_snapshot.age` (`restic_host`) | snapshots, age of the last one per machine |
| `restic.last_snapshot.files`, `.bytes`, `.added`, `.duration` | last snapshot (restic 0.17 or later) |
| `restic.repository.size`, `.uncompressed_size`, `.compression_ratio` | with `stats: true` |

Tag `repository` (the repository `name`). Checks: `borg.can_connect` and `restic.can_connect` (repository readable, otherwise critical: server unreachable, passphrase refused, badly protected file), `borg.backup` (warning: the last backup stopped halfway, a checkpoint newer than the last complete archive; critical: no archive) and `restic.backup` (critical: no snapshot).

## Monitor templates

- **DNS**: Pi-hole unreachable, Pi-hole blocking disabled, old Pi-hole block lists, Pi-hole warnings, AdGuard Home unreachable, AdGuard Home protection disabled, old or empty filter lists, Unbound unreachable, Unbound SERVFAIL answers, queries dropped by Unbound, invalid DNSSEC answers.
- **Web**: Caddy admin API unreachable, Caddy upstream down, Caddy configuration reload failed, Caddy 5xx responses.
- **Storage**: unhealthy MinIO cluster, MinIO drives or nodes offline, healing drives, low MinIO free space, S3 API 5xx errors.
- **Message queues**: Mosquitto unreachable, dropped MQTT messages, missing MQTT clients.
- **Backups**: Proxmox Backup Server unreachable, datastore unavailable or nearly full, failed PBS backup, failed PBS job, snapshots failing verification, unreadable Borg or restic repository, interrupted last Borg backup, restic repository without snapshots, and **last backup too old** for PBS (per group), Borg and restic (per repository), thresholds in hours (48 h critical, 26 h warning by default).

## Ready-made dashboards

**Dashboards › Catalogue**: Pi-hole, AdGuard Home and Unbound (**DNS** family), Caddy (**Web**), MinIO (**Storage**), Mosquitto (**Message queues**), Proxmox Backup Server, Borg and restic (**Backups**).

## Not tested

- Multi-node MinIO cluster (tested on one node and one drive) and drive healing.
- Proxmox Backup Server: sync between two servers, tapes, removable datastores and namespaces (read through the API, without a real test).
- Pi-hole 6 over HTTPS and two-factor authentication; AdGuard Home behind a reverse proxy.
- Mosquitto over TLS (`mqtts://`); other MQTT brokers (EMQX, HiveMQ: their `$SYS` topics differ).
- Borg 2 (new format and commands); restic repositories on S3 or SFTP.
