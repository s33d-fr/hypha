---
id: self-hosted-apps
path: /en/docs/self-hosted-apps/
title: Self-hosted apps: Nextcloud, Jellyfin, Syncthing, Immich, Forgejo, Matrix, Home Assistant, Mastodon
nav: Self-hosted apps
section: Integrations
order: 29
updated: 2026-11-21
description: Monitor the most popular self-hosted applications with HYPHA: Nextcloud, Jellyfin, Syncthing, Immich, Gitea and Forgejo, Matrix Synapse, Home Assistant and Mastodon, through their API or their Prometheus metrics.
lead: Eight applications people host themselves, read through their native interface: health, users, job queues, storage, synchronisation, with monitor templates and a ready-made dashboard for each.
---
## At a glance

Each application is read the most direct way it offers: its API when its Prometheus metrics miss the essentials (Nextcloud, Jellyfin, Syncthing, Immich), otherwise a profile of the [OpenMetrics reader](/en/docs/generic-collection/). Everything was tested against the real software running in a container in a S33D test environment (last column).

| Application | Collection | Activation | Namespace | Tested on |
|---|---|---|---|---|
| Nextcloud | probe: `status.php` and serverinfo API | once `url` is set | `nextcloud.*` | Nextcloud 35.0.3 (serverinfo 7.0.0) |
| Jellyfin | probe: `/health` and server API | auto on `127.0.0.1:8096` | `jellyfin.*` | Jellyfin 12.4.0 |
| Syncthing | probe: REST API | auto on `127.0.0.1:8384` | `syncthing.*` | Syncthing 2.1.7 (two linked devices) |
| Immich | probe: server API | once `url` is set | `immich.*` | Immich 3.4.2 |
| Gitea and Forgejo | `gitea` or `forgejo` profile (`/metrics`) | to declare | `gitea.*` | Gitea 28.4.0, Forgejo 13.0.5 |
| Matrix Synapse | `synapse` profile (`/_synapse/metrics`) | to declare | `synapse.*` | Synapse 1.165.0 |
| Home Assistant | `homeassistant` profile (`/api/prometheus`) | to declare | `homeassistant.*` | Home Assistant 2026.11.2 |
| Mastodon | `mastodon` and `mastodon_streaming` profiles | to declare | `mastodon.*`, `mastodon_streaming.*` | Mastodon 4.7.6 (web, Sidekiq, streaming) |

The probes only read: no write, no job started, no call that would make the application contact an outside service. Keys are best put in a file only the agent can read (`api_key_file`, `token_file`), read again at each collection: a changed key is picked up without restarting the agent. `hypha-agent check-config` shows them masked.

```yaml
integrations:
  nextcloud: {url: "https://cloud.example", token_file: /etc/hypha/nextcloud.token}
  jellyfin: {api_key_file: /etc/hypha/jellyfin.key}          # default url: http://127.0.0.1:8096
  syncthing: {api_key_file: /etc/hypha/syncthing.key}        # default url: http://127.0.0.1:8384
  immich: {url: "http://127.0.0.1:2283", api_key_file: /etc/hypha/immich.key}
  openmetrics:
    - profile: forgejo                                       # or gitea
      bearer_token_file: /etc/hypha/forgejo-metrics.token
    - profile: synapse
    - profile: homeassistant
      bearer_token_file: /etc/hypha/homeassistant.token
    - {profile: mastodon, name: mastodon-web, url: "http://127.0.0.1:9394/metrics"}
    - {profile: mastodon, name: mastodon-sidekiq, url: "http://127.0.0.1:9395/metrics"}
    - profile: mastodon_streaming
```

The four probes accept `instances` (several Nextcloud or Syncthing on one machine, each with its `name`, tag `instance_name`).

## Nextcloud

The probe reads `status.php` without an account (installed, maintenance mode, database upgrade pending, version), then the API of the **serverinfo** app (shipped and enabled by default). The simplest is a token of serverinfo's own, which only gives access to these statistics:

```sh
sudo -u www-data php occ config:app:set serverinfo token --value "$(openssl rand -hex 24)"
```

The same token goes into `token_file` (header `NC-Token`). Otherwise, an administrator account and an app password (`username`, `password`).

- Checks: `nextcloud.can_connect`, `nextcloud.status` (critical in maintenance mode, when the database waits for `occ upgrade` or when installation is unfinished; serverinfo is then not queried).
- `nextcloud.maintenance`, `nextcloud.needs_db_upgrade`, `nextcloud.info` (tag `version`).
- `nextcloud.users`, `nextcloud.users.disabled`, `nextcloud.users.active` (tag `period`: `5m`, `1h`, `24h`, `7d`, `30d`), `nextcloud.files`, `nextcloud.storages`.
- `nextcloud.shares`, `nextcloud.shares.by_type` (`user`, `group`, `link`, `mail`, `room`, `federated_sent`, `federated_received`), `nextcloud.shares.link_no_password`.
- `nextcloud.storage.free` (free space of the data folder), `nextcloud.database.size` (tag `db_type`).
- PHP: `nextcloud.php.memory_limit`, `nextcloud.php.opcache.hit_rate`, `.opcache.memory.used`, `.memory.free`, `.memory.wasted`, `nextcloud.php.opcache.full`, `nextcloud.php.opcache.oom_restarts`, `nextcloud.php.apcu.hit_rate`, `nextcloud.php.apcu.memory.available`.

File and storage counts are those serverinfo keeps up to date through its background job (`occ serverinfo:update-storage-statistics` refreshes them). App updates are not read: the probe keeps serverinfo's defaults (`skipApps`, `skipUpdate`) so that Nextcloud is never made to query the app store.

Nextcloud 35 (seen in the test) also publishes `/metrics` in OpenMetrics format (active users, sessions, background jobs), limited to the loopback by default (`openmetrics_allowed_clients` in `config.php`): the generic reader can read it, the probe covers the essentials.

## Jellyfin

Without a key, the probe reads `/health` (`Healthy`, `Degraded`, `Unhealthy`) and the public version. With an API key (**Dashboard › API Keys**), it also reads sessions, libraries and scheduled tasks. Jellyfin 12.4 only accepts the key in the `Authorization: MediaBrowser Token="…"` header (`X-Emby-Token` and `?api_key=` are refused): that is what the agent sends.

- Checks: `jellyfin.can_connect`, `jellyfin.health` (warning if `Degraded`, critical if `Unhealthy`), `jellyfin.scheduled_tasks` (warning when the last run of a task failed, with its name and message).
- `jellyfin.healthy`, `jellyfin.info` (`version`), `jellyfin.pending_restart`, `jellyfin.update_available`.
- Sessions active in the last 16 minutes: `jellyfin.sessions.active`, `.sessions.playing`, `.sessions.paused`, `.sessions.transcoding`; `jellyfin.streams` (tag `play_method`: `directplay`, `directstream`, `transcode`).
- `jellyfin.library.items` (tag `type`: `movie`, `series`, `episode`, `song`, `album`, `artist`, `book`…), `jellyfin.library.items_total`.
- `jellyfin.scheduled_tasks.failed`, `jellyfin.scheduled_tasks.running`.

Jellyfin's `/metrics` (`EnableMetrics` in `system.xml`) only publishes .NET runtime and HTTP request metrics: the generic reader can read it as well.

## Syncthing

Without a key, the probe reads `/rest/noauth/health`. With the GUI key (**Actions › Settings › GUI › API Key**, header `X-API-Key`), it reads devices, connections, folders and errors every minute. As `/rest/db/status` is costly for Syncthing, the probe makes one request per folder per minute, and stops at 100 folders.

- Checks: `syncthing.can_connect`; `syncthing.folder` per folder (tags `folder`, `folder_label`, `folder_type`): critical on error (missing `.stfolder` marker, permissions, disk), warning when files cannot be received or the change watcher fails, OK when paused; `syncthing.device` per remote device (tags `device`, `device_id` on 7 characters): OK connected or paused, warning disconnected, with the date it was last seen.
- Folders: `syncthing.folder.need_files`, `.need_bytes`, `.global_files`, `.global_bytes`, `.local_files`, `.local_bytes`, `.completion` (%), `.pull_errors`, `.error`; `syncthing.folders` (tag `state`: `idle`, `scanning`, `syncing`, `error`, `paused`…).
- Devices: `syncthing.device.connected`, `syncthing.device.last_seen_age` (s, 0 while connected), `syncthing.device.transfer.in` and `.transfer.out` (bytes); `syncthing.devices` (tag `state`: `connected`, `disconnected`, `paused`).
- `syncthing.transfer.in`, `syncthing.transfer.out`, `syncthing.uptime`, `syncthing.goroutines`, `syncthing.memory.alloc`, `syncthing.memory.sys`, `syncthing.system.errors`, `syncthing.listeners.errors`, `syncthing.discovery.errors`, `syncthing.info` (`version`).

Syncthing 2.1 also publishes `/metrics` (key required): folder states and volumes as codes, without errors or missing devices; the REST probe gives them in plain words.

## Immich

Without a key, the probe reads `/api/server/ping` and the version. With an API key (**Account Settings › API Keys**) holding the `server.storage`, `server.statistics` and `job.read` permissions (and nothing else), it reads the library disk, the number of photos and videos, and the job queues.

- Checks: `immich.can_connect`, `immich.queues` (warning when a queue is paused or holds failed jobs, with their names).
- `immich.storage.size`, `.storage.used`, `.storage.available` (bytes), `immich.storage.usage` (%).
- `immich.assets` and `immich.usage.by_type` (tag `type`: `photo`, `video`), `immich.usage`, `immich.users`; per account (tag `user`): `immich.user.usage`, `immich.user.assets`, and with a quota `immich.user.quota`, `immich.user.quota_usage` (%).
- `immich.jobs` (tags `queue`: `thumbnail_generation`, `metadata_extraction`, `video_conversion`… and `state`: `active`, `waiting`, `delayed`, `failed`, `paused`), `immich.queue.paused`, `immich.info` (`version`).

Immich's Prometheus metrics (`IMMICH_TELEMETRY_INCLUDE=all`, ports 8081 and 8082) mostly give internal durations and queue activity, without the disk or the queue state: the generic reader can read them as well. Pitfall seen on Immich 3.4.2: with the default listen address (`::`), both ports answer "400 Bad Request" to every request; setting `IMMICH_HOST=0.0.0.0` makes them readable.

## Gitea and Forgejo

In `app.ini`: `[metrics] ENABLED = true` (or `GITEA__metrics__ENABLED=true`, `FORGEJO__metrics__ENABLED=true` in a container). With `[metrics] TOKEN = …`, the token goes into `bearer_token_file`. Forgejo publishes the same `gitea_*` families as Gitea: the `gitea` and `forgejo` profiles are identical and give the same `gitea.*` names, hence the same dashboards and monitors (`instance_name` tells them apart).

- `gitea.repositories`, `gitea.users` (tag `state`: `active`, `inactive` on Gitea), `gitea.organizations`, `gitea.teams`, `gitea.issues`, `gitea.issues.open`, `gitea.issues.closed`, `gitea.milestones`, `gitea.labels`, `gitea.comments`, `gitea.releases`, `gitea.mirrors`, `gitea.stars`, `gitea.watches`, `gitea.follows`, `gitea.webhooks`, `gitea.hook_tasks`, `gitea.public_keys`, `gitea.oauth_apps`, `gitea.login_sources`, `gitea.projects`, `gitea.projects.boards`, `gitea.attachments`, `gitea.accesses`, `gitea.update_tasks`.
- `gitea.build_info` (tag `version`), `gitea.process.resident_memory`, `gitea.process.cpu_seconds`, `gitea.process.open_fds`, `gitea.go.goroutines`.

These are database counts (gauges): the forge's health is followed through `openmetrics.can_connect` and an HTTP synthetic test on the web interface.

## Matrix Synapse

In `homeserver.yaml`: `enable_metrics: true` and a dedicated listener, bound to the loopback.

```yaml
listeners:
  - port: 9000
    type: metrics
    bind_addresses: ['127.0.0.1']
```

- `synapse.http.requests` (tags `method`, `servlet`), `synapse.http.responses` (`code`), `synapse.http.response_time` (s, `.p50`, `.p95`, `.p99`, `.count`), `synapse.http.in_flight`.
- `synapse.events.persisted`, `synapse.event_processing.lag` (ms, tag `name`: `federation_sender`, `appservice_sender`…).
- Federation: `synapse.federation.pending_pdus`, `.pending_edus`, `.pending_destinations`, `.sent_transactions`, `.received_pdus`, `.staged_pdus`, `.oldest_staged_pdu`, `.known_servers`.
- Users: `synapse.users.mau`, `.mau_max`, `.daily_active`, `.non_deactivated`, `.registrations`, `.logins`.
- `synapse.db.schedule_time` (wait for a database connection), `synapse.push.processed`, `synapse.push.failed`, `synapse.rate_limit.rejected`, `synapse.rate_limit.slept`, `synapse.pushers`, `synapse.notifier.rooms`, `.users`, `.listeners`, `synapse.threadpool.working`, `synapse.background_update.status`, `synapse.background_processes.in_flight`, `synapse.scheduler.running_tasks`, `synapse.appservice.failed_transactions`, `synapse.build_info` (`version`), `synapse.process.resident_memory`, `.cpu_seconds`, `.open_fds`.

Synapse's public health is followed as well by an HTTP synthetic test on `/health` (answer `OK`) of the client listener.

## Home Assistant

In `configuration.yaml`: `prometheus:` (the native Prometheus integration). Its `/api/prometheus` route requires a long-lived access token (**Profile › Security › Long-lived access tokens**), set in `bearer_token_file`.

- Per entity (tags `entity`, `domain`, `friendly_name`): `homeassistant.entity.available` (0 = unavailable), `homeassistant.entity.state_changes`.
- Sensors: `homeassistant.sensor.temperature` (°C), `.sensor.humidity` (%), `.sensor.battery` (%), `.sensor.power` (W), `.sensor.energy` (kWh), `.sensor.co2` (ppm); `homeassistant.binary_sensor.state`, `homeassistant.switch.state`, `homeassistant.light.brightness`, `homeassistant.cover.position`, `homeassistant.lock.state`, `homeassistant.input_boolean.state`, `homeassistant.device_tracker.state`, `homeassistant.alarm.state` (tag `state`).
- Thermostats: `homeassistant.climate.current_temperature`, `.climate.target_temperature`, `.climate.action` (tag `action`).
- `homeassistant.update.available` (1 = an update is waiting), `homeassistant.process.resident_memory`, `.cpu_seconds`, `.open_fds`.

A home with several hundred entities goes past the 2,000-series cap of a target (at least two series per entity): raise `max_series` in the entry, or leave out domains in the configuration of Home Assistant's Prometheus integration (`filter`). Other families (`homeassistant_sensor_unit_*`, update times) can be added through `metrics` and `rename`.

## Mastodon

Mastodon environment variables (seen on 4.7.6): `MASTODON_PROMETHEUS_EXPORTER_ENABLED=true`, and either `MASTODON_PROMETHEUS_EXPORTER_LOCAL=true` (web and Sidekiq each serve their own `/metrics` on `MASTODON_PROMETHEUS_EXPORTER_PORT`, 9394 by default: one port per service when they run on the same machine), or the `bin/prometheus_exporter` process that gathers them. `MASTODON_PROMETHEUS_EXPORTER_SIDEKIQ_DETAILED_METRICS=true` adds the Sidekiq queues (size, latency), `MASTODON_PROMETHEUS_EXPORTER_WEB_DETAILED_METRICS=true` the HTTP requests. The streaming server publishes `/metrics` on its port (4000) without any setting.

- Puma: `mastodon.puma.workers`, `.booted_workers`, `.running_threads`, `.busy_threads`, `.thread_pool_capacity`, `.max_threads`, `.request_backlog`.
- HTTP (detailed): `mastodon.http.requests` (tag `status`), `mastodon.http.request_duration`, `.sql_duration`, `.redis_duration` (summaries in s: `.avg`, `.p50`, `.p90`, `.p99`), `mastodon.http.queue_duration` (with the reverse proxy's `X-Request-Start` header).
- Sidekiq: `mastodon.sidekiq.jobs`, `.failed_jobs`, `.dead_jobs`, `.restarted_jobs` (tag `queue`), `mastodon.sidekiq.job_duration`, `mastodon.sidekiq.queue.backlog` and `.queue.latency` (s, per `queue`), `mastodon.sidekiq.process.busy`, `.process.concurrency`, `mastodon.sidekiq.stats.enqueued`, `.retry`, `.dead`, `.scheduled`, `.failed`, `.processed`, `.processes`, `.workers`.
- `mastodon.db_pool.busy`, `.size`, `.waiting`, `.dead`, `mastodon.process.rss` (tag `type`), `mastodon.ruby.heap_live_slots`, `mastodon.ruby.major_gc`, `mastodon.collector.working`.
- Streaming: `mastodon_streaming.clients` and `.messages_sent` (tag `type`: `websocket`, `eventsource`), `.channels`, `.redis.subscriptions`, `.redis.messages_received`, `.pg_pool.total`, `.pg_pool.idle`, `.pg_pool.waiting`, `.eventloop.lag`, `.eventloop.lag_p99`, `.process.resident_memory`.

Puma and Sidekiq queue metrics are gathered by Mastodon at intervals: right after a restart, they are missing for a short while. Public health is followed as well by an HTTP synthetic test on `/health` (answer `OK`) and `/api/v1/streaming/health`.

## Monitor templates

In **Monitors › New › From a template**, family **Applications auto-hébergées** (self-hosted apps):

- **Nextcloud**: unreachable, out of service (maintenance, upgrade pending), free space (GiB), OPcache full.
- **Jellyfin**: unreachable, unhealthy, failed scheduled tasks, simultaneous transcodes.
- **Syncthing**: unreachable, folder at fault, device disconnected, device missing for N hours, folder behind for an hour.
- **Immich**: unreachable, blocked job queues (paused, failures), library disk, waiting jobs.
- **Gitea and Forgejo**, **Synapse**, **Home Assistant**, **Mastodon**: target unreachable (by `instance_name`); event processing lag, federation queue and slow requests for Synapse; unavailable entity, low battery and update available in Home Assistant; Sidekiq queues behind, jobs to retry and requests waiting in Puma for Mastodon.

## Ready-made dashboards

**Dashboards › Catalogue**, family **Applications auto-hébergées**: Nextcloud, Jellyfin, Syncthing, Immich, Gitea and Forgejo, Matrix Synapse, Home Assistant, Mastodon (with streaming), installed in one click then editable like any dashboard.

## What was tested

Each application ran in a container in the S33D test environment, with test accounts and data created for the occasion. The agent first read it in dry-run mode, then the 0.11 agent of the test host sent its metrics to HYPHA: every check and every metric of the ready-made dashboards was seen there, except `synapse.push.processed` and `synapse.push.failed` (no push gateway in the test).

Faults provoked, seen, then back to normal: Nextcloud in maintenance mode (`nextcloud.status` critical); Jellyfin, Home Assistant and Mastodon's Sidekiq stopped (critical); second Syncthing stopped (`syncthing.can_connect` critical, and on the first one, device disconnected as a warning with its last connection date); Syncthing folder without its `.stfolder` marker (critical); Immich queue paused (warning).

Still to be seen on a real installation: Jellyfin transcoding, Synapse pushes and federation between two Matrix servers, a Home Assistant home with several hundred entities (the test had 127), federated Mastodon under load, Immich quotas; Jellyfin failed scheduled tasks and Immich failed jobs are only tested on real responses altered in the tests.
