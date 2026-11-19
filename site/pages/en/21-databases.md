---
id: databases
path: /en/docs/databases/
title: Databases, caches, queues and search engines
nav: Databases
section: Integrations
order: 21
updated: 2026-11-19
description: PostgreSQL and its most expensive queries (DBM), MySQL and MariaDB, Redis and Valkey, Memcached, MongoDB, RabbitMQ, OpenSearch and Elasticsearch, SQL Server: settings, permissions and metrics.
lead: Each integration reads the software with a read-only monitoring account, or through the local socket when possible.
---
## PostgreSQL

On by default if the socket `/var/run/postgresql/.s.PGSQL.5432` exists. The agent connects through that socket as the system user `postgres` (`peer` authentication, no password), in a read-only session.

```yaml
integrations:
  postgres:
    enabled: auto
    # or a monitoring account holding the pg_monitor role:
    # dsn: postgres://hypha:password@127.0.0.1:5432/postgres?sslmode=disable
    dbm: auto
```

Metrics: connections and their share of the maximum, committed and rolled-back transactions, rows read and written, database sizes, cache hit ratio, locks, waiting queries, longest query, idle-in-transaction sessions, background writer, primary or replica role, replication lag (`postgresql.replication_delay`, `postgresql.replication.wal_lag_bytes`).

### Most expensive queries (DBM)

If the `pg_stat_statements` extension exists in the `postgres` database, the agent sends the 50 most expensive queries (total time) every 5 minutes, with the normalised text PostgreSQL returns. The **Databases** page ranks them by total time, calls or mean time, with the cache hit ratio and each query's trend.

```sql
-- postgresql.conf: shared_preload_libraries = 'pg_stat_statements' (restart needed if missing)
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
```

The agent checks for the extension again once an hour: after creating it, restart `hypha-agent` to see it at once.

## MySQL and MariaDB

Without settings, the agent connects as `root` through the local socket `/run/mysqld/mysqld.sock` (socket authentication of Debian and Ubuntu packages). Otherwise, a monitoring account:

```sql
CREATE USER 'hypha'@'127.0.0.1' IDENTIFIED BY '…';
GRANT PROCESS, REPLICATION CLIENT ON *.* TO 'hypha'@'127.0.0.1';
```

```yaml
integrations:
  mysql:
    dsn: "hypha:password@tcp(127.0.0.1:3306)/"
```

Metrics: connections (current, maximum, share used, refused, aborted), queries and slow queries, commands by type, running threads, on-disk temporary tables, InnoDB buffer pool, row lock waits, database sizes, replication lag and state (`mysql.replication`).

## Redis and Valkey

The `INFO` command, on `127.0.0.1:6379` by default, or a Unix socket.

```yaml
integrations:
  redis:
    address: 127.0.0.1:6379
    # username: hypha   # Redis 6+ ACL
    # password: "…"
```

Metrics: connected and blocked clients, used memory, limit, fragmentation, cache hit rate, keys, expired and evicted keys, commands, network throughput, replication and replicas, persistence to disk (`redis.persistence`).

## Memcached

The `stats` command on `127.0.0.1:11211`: bytes and limit, get hit rate, evictions, connections and refused connections, items, threads.

## MongoDB

A connection URI, `mongodb://127.0.0.1:27017` by default. With authentication on, an account with the `clusterMonitor` role:

```yaml
integrations:
  mongodb:
    uri: "mongodb://hypha:password@127.0.0.1:27017/?authSource=admin"
```

Metrics: current and available connections, operations, read and write queues, WiredTiger cache, resident memory, database sizes, replica set state and lag.

## RabbitMQ

The management API (`http://127.0.0.1:15672`), with an account holding the `monitoring` tag (without an account: `guest`, which RabbitMQ only accepts from the machine itself).

```yaml
integrations:
  rabbitmq:
    url: http://127.0.0.1:15672
    username: hypha
    password: "…"
```

Metrics: connections, channels, consumers, queues, ready and unacknowledged messages, publish, deliver and ack rates, node alarms (memory, disk, descriptors) through the `rabbitmq.node` check, messages per queue.

## OpenSearch and Elasticsearch

The REST API of the local node: the agent finds the address port 9200 listens on, or uses `url`.

```yaml
integrations:
  opensearch:
    url: https://127.0.0.1:9200
    username: hypha
    password: "…"
    insecure: false     # true only for a self-signed certificate
```

Metrics: cluster health (`opensearch.cluster_health`), nodes, active, initialising and unassigned shards, pending tasks, JVM heap and garbage collection, disk usage, documents, indexing and search (rate, latency), rejected writes and searches, tripped circuit breakers.

## SQL Server

On Windows, through performance counters, when SQL Server is present: `mssql.buffer_cache_hit_ratio`, `mssql.batch_requests_per_s`, `mssql.user_connections`.

## Monitor templates

The **Databases**, **Message queues** and **Search** families of the catalogue: unreachable, replication stopped or lagging, connections almost saturated, Redis memory almost full, Redis persistence failing, growing RabbitMQ queue, RabbitMQ node alarm, degraded OpenSearch cluster, saturated JVM heap, disk at watermark, rejected writes.
