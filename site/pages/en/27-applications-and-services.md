---
id: applications
path: /en/docs/applications-and-services/
title: "Applications and services: PHP-FPM, Varnish, Squid, Gunicorn, PowerDNS, OpenLDAP, Keycloak, Nexus, Harbor, Ceph…"
nav: Applications and services
section: Integrations
order: 27
updated: 2026-11-20
description: Dedicated HYPHA agent probes for PHP-FPM, Gunicorn, Varnish, Squid, Lighttpd, Supervisor, Fluentd, PowerDNS (authoritative server and Recursor), OpenLDAP, Keycloak, Sonatype Nexus, Harbor, ZooKeeper and Ceph: settings, permissions, metrics and checks.
lead: Each piece of software is read through its native interface (status page, admin socket, statistics command, REST API), read-only.
---
## Principle

These probes follow the rules of the other integrations ([System, web and mail](/en/docs/integrations/)): in `auto` mode, a probe whose software answers at its usual address turns itself on; those that need an account, a key or an address with no default only start once given one. Once the software has been seen, its `<name>.can_connect` check turns critical if it stops answering. Cumulative counters become per-interval differences. Several targets of one probe are declared with `instances` ([Configuration](/en/docs/configuration/)).

Each probe has its [monitor templates](/en/docs/monitors/) and its ready-made dashboard (**Dashboards › Catalogue**).

## PHP-FPM

The agent speaks FastCGI directly to the pool socket and reads its status page: no page to open in the web server. Without settings, every `/run/php/php*-fpm.sock` or `/run/php-fpm/*.sock` socket is read, one pool per socket. In each pool's configuration:

```ini
pm.status_path = /status
ping.path = /ping
```

```yaml
integrations:
  phpfpm:
    address: 127.0.0.1:9000     # or /run/php/php8.3-fpm.sock; or url: http://127.0.0.1/status
```

Metrics (`pool` tag): active, idle and total processes, busy share (`php_fpm.processes.usage`), listen queue (`php_fpm.listen_queue.size`), accepted and slow requests, `pm.max_children` cap reached (`php_fpm.processes.max_reached`). Checks `php_fpm.can_connect` and `php_fpm.can_ping`. A pool found without a status page stays "inactive" (`hypha-agent status` says so), without alerting.

## Gunicorn

The agent recognises Gunicorn masters in `/proc` (title `gunicorn: master [name]` when setproctitle is installed, otherwise `--name` or the WSGI module of the command line) and counts their workers, busy or idle (`gunicorn.worker_processes`, `app` and `state` tags). Check `gunicorn.is_running`: critical if an application already seen (or listed in `apps`) has no master or no worker left.

Requests come from Gunicorn's native StatsD, received by the agent:

```bash
gunicorn --statsd-host 127.0.0.1:8125 app:app
```

Gunicorn's StatsD metrics: `gunicorn.requests`, `gunicorn.request.duration`, `gunicorn.request.status.<code>`, `gunicorn.workers`, `gunicorn.log.error`.

```yaml
integrations:
  gunicorn:
    apps: [shop, "api.main:app"]   # optional: expected applications
```

## Varnish

`varnishstat -j` (Varnish 6.5 and later forms, and earlier ones), and backend health through `varnishadm backend.list -j`. For a Varnish in a container or a named instance:

```yaml
integrations:
  varnish:
    command: "docker exec varnish varnishstat"   # or "varnishstat -n name"
```

Metrics: client requests, cache hits, passes and misses, hit rate (`varnish.cache_hit_rate`), objects, expiries and evictions (`varnish.n_lru_nuked`), dropped sessions, worker threads, connections, requests and failures per backend, used and free storage. One `varnish.backend_healthy` check per backend (critical when it fails its probes). Without access to the `varnishadm` secret, backend health is not read and the rest goes on.

## Squid

Cache manager pages served by Squid on its own port (`http://127.0.0.1:3128/squid-internal-mgr/counters` and `info`); the default configuration (`http_access allow localhost manager`) is enough from the machine itself. A `cachemgr_passwd` is given as `password`.

Metrics: client requests, hits, errors, KiB exchanged with clients and servers, hit rate (`squid.cachemgr.client_http.hit_rate`), aborted requests, clients, used and maximum file descriptors, storage.

## Lighttpd

`mod_status` page (`status.status-url = "/server-status"`), read as `?auto`. Since the host's port 80 usually belongs to Apache or nginx, the address is given:

```yaml
integrations:
  lighttpd:
    url: http://127.0.0.1:8080/server-status
```

Metrics: requests and bytes (totals, per second), active and idle connections, uptime.

## Supervisor

Supervisor's XML-RPC API, through the `[unix_http_server]` Unix socket (found on its own at `/run/supervisor.sock`, `/var/run/supervisor.sock`…) or the `[inet_http_server]` (`url: http://127.0.0.1:9001/RPC2`, `username`, `password`). A socket under `/tmp` is invisible to the agent (`PrivateTmp` of its systemd unit): put it under `/run`.

One `supervisord.process.status` check per program: RUNNING OK, STARTING UNKNOWN, STOPPED warning, FATAL, EXITED and BACKOFF critical, with the reason given by Supervisor. Metrics: programs per state, uptime of each.

## Fluentd

Fluentd's `monitor_agent` plugin:

```
<source>
  @type monitor_agent
  bind 127.0.0.1
  port 24220
</source>
```

Per plugin (`plugin_id`, `type`, `plugin_category` tags): queued chunks and bytes, free buffer space (`fluentd.buffer_available_buffer_space_ratios`), retries, emitted and written records, rollbacks, slow flushes. Name each plugin (`@id`): without it, its identifier changes at every Fluentd restart.

## PowerDNS

API of the built-in web server, with the API key (`webserver=yes`, `api=yes`, `api-key=…`). Two probes: `powerdns` (authoritative server, port 8081) and `powerdns_recursor` (port 8082).

```yaml
integrations:
  powerdns:
    url: http://127.0.0.1:8081
    api_key: "…"
  powerdns_recursor:
    url: http://127.0.0.1:8082
    api_key: "…"
```

Authoritative server: UDP and TCP queries, answers, latency, packet and query caches (hit rate), SERVFAIL, timeouts, corrupt packets, queue, signatures. Recursor: questions, outgoing queries, answers by latency bucket, caches and hit rate, SERVFAIL, NXDOMAIN, timeouts, concurrent queries, latency. Checks `powerdns.authoritative.security_status` and `powerdns.recursor.security_status`: warning when PowerDNS recommends an upgrade, critical when the version is vulnerable.

## OpenLDAP

slapd's `cn=Monitor` database: connections, initiated and completed operations per type, bytes and entries sent, threads (`openldap.threads`, `status` tag), read and write waiters, uptime. The `monitor` database must be declared in slapd with read access for the agent's account:

```ldif
dn: cn=module{0},cn=config
changetype: modify
add: olcModuleLoad
olcModuleLoad: back_monitor

dn: olcDatabase={2}monitor,cn=config
changetype: add
objectClass: olcDatabaseConfig
olcDatabase: {2}monitor
olcAccess: {0}to * by dn.exact="gidNumber=0+uidNumber=0,cn=peercred,cn=external,cn=auth" read by * none
```

```yaml
integrations:
  openldap:
    url: "ldapi://%2Frun%2Fslapd%2Fldapi"   # SASL EXTERNAL: root identity of the socket
    # or: url: ldaps://ldap.example:636, bind_dn: "cn=hypha,dc=example,dc=com", password: "…"
```

## Keycloak

Health on the management port (`KC_HEALTH_ENABLED=true`, `/health/ready`, database included) and sessions through the admin API. For sessions, a confidential client of the `master` realm with "Service accounts roles", whose service account holds `view-realm` and `view-users` of the `<realm>-realm` client of each realm read:

```yaml
integrations:
  keycloak:
    url: http://127.0.0.1:9000          # management port
    admin_url: http://127.0.0.1:8080
    client_id: hypha
    client_secret: "…"
    # realms: [customers]               # empty: all the account can see
```

Metrics: `keycloak.health.ready`, active and offline sessions per realm and per client, users per realm. Checks `keycloak.can_connect` (DOWN: critical, with the failing checks) and `keycloak.admin_api`. Keycloak's JVM and HTTP metrics are only published in the Prometheus format (`/metrics`): read them with the [OpenMetrics reader](/en/docs/generic-collection/).

## Sonatype Nexus

REST API: availability (`/service/rest/v1/status`) and writability (`nexus.writable`, critical when read-only) without an account; with an account whose role holds `nx-metrics-all` and `nx-blobstores-read`: system health checks (`nexus.health`, warning with the names of failing checks), JVM heap, threads, HTTP requests and responses per class, latency, size and free space of blob stores.

```yaml
integrations:
  nexus:
    url: http://127.0.0.1:8081
    username: hypha
    password: "…"
```

## Harbor

API v2.0: health of each component (`harbor.component`, no account needed) and of the whole (`harbor.status`), version and read-only flag; with an administrator account: projects, repositories, consumed storage, registry disk (`harbor.disk.usage`).

```yaml
integrations:
  harbor:
    url: https://harbor.example
    username: admin
    password: "…"
```

## ZooKeeper

Four-letter commands `mntr` and `ruok` on `127.0.0.1:2181`. Since ZooKeeper 3.5 they must be listed in `4lw.commands.whitelist=mntr,ruok,srvr` (`ZOO_4LW_COMMANDS_WHITELIST` in the official Docker image). Metrics: latencies, outstanding requests, connections, znodes, watches, ephemerals, data size, file descriptors, followers and synced followers (on the leader), packets, role (`zookeeper.server_state`). Checks `zookeeper.can_connect` and `zookeeper.ruok`.

## Ceph

`ceph status` and `ceph df` as JSON, on a cluster node with the `ceph` tool and a readable keyring; in auto mode, only when `/etc/ceph/ceph.conf` exists. A read-only client is enough (`ceph auth get-or-create client.hypha mon 'allow r' mgr 'allow r'`):

```yaml
integrations:
  ceph:
    command: "ceph --id hypha"          # or "docker exec ceph ceph", "cephadm shell -- ceph"
```

Metrics (`ceph_fsid` tag): monitors and quorum, OSDs up and in, placement groups per state and active+clean share, capacity and usage, throughput and operations, usage and available space of each pool. Check `ceph.overall_status`: HEALTH_WARN as warning, HEALTH_ERR as critical, with the unmuted health checks. One agent per cluster is enough.
