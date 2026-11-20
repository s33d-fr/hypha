---
id: prometheus-software
path: /en/docs/prometheus-software/
title: Software exposing Prometheus: Consul, Vault, Traefik, Kafka, GitLab, Argo CD…
nav: Prometheus software
section: Integrations
order: 27
updated: 2026-11-20
description: Monitor with HYPHA the software that publishes Prometheus metrics (Consul, Vault, etcd, CoreDNS, Traefik, Envoy, Kong, CockroachDB, ScyllaDB, Pulsar, Celery, n8n, Temporal, GitLab, SonarQube, Solr, WildFly, cert-manager, Velero, Argo CD, Linkerd, KEDA) and Java software through the Prometheus JMX exporter (Tomcat, Kafka, ActiveMQ, Cassandra).
lead: Twenty-nine ready-made profiles for the agent's OpenMetrics reader: one configuration line per product, clean metric names, monitor templates and a catalog dashboard for each.
---
## At a glance

Each product is read by the agent's [OpenMetrics reader](/en/docs/generic-collection/) with a **profile** that sets the port, the path, the families kept, their HYPHA names and the labels kept. The profiles were written against the expositions of the real products, run in containers in an S33D test environment (versions in the last column).

| Product | Profile | Default address | Namespace | Tested on |
|---|---|---|---|---|
| HashiCorp Consul | `consul` | `http://127.0.0.1:8500/v1/agent/metrics?format=prometheus` | `consul.*` | Consul 2.1.0 |
| HashiCorp Vault | `vault` | `https://127.0.0.1:8200/v1/sys/metrics?format=prometheus` | `vault.*` | Vault 2.2.2 |
| etcd | `etcd` | `http://127.0.0.1:2379/metrics` | `etcd.*` | etcd 3.6.5 |
| CoreDNS | `coredns` | `http://127.0.0.1:9153/metrics` | `coredns.*` | CoreDNS 1.12.4 |
| Traefik Proxy | `traefik` | `http://127.0.0.1:8080/metrics` | `traefik.*` | Traefik 3.7.19 |
| Envoy Proxy | `envoy` | `http://127.0.0.1:9901/stats/prometheus` | `envoy.*` | Envoy 1.36.13 |
| Kong Gateway | `kong` | `http://127.0.0.1:8100/metrics` | `kong.*` | Kong 3.9.4 |
| CockroachDB | `cockroachdb` | `https://127.0.0.1:8080/_status/vars` | `cockroachdb.*` | CockroachDB 26.4.1 |
| ScyllaDB | `scylla` | `http://127.0.0.1:9180/metrics` | `scylla.*` | ScyllaDB 2026.3.6 |
| Apache Pulsar | `pulsar` | `http://127.0.0.1:8080/metrics/` | `pulsar.*` | Pulsar 5.0.1 (standalone) |
| Celery (through Flower) | `celery` | `http://127.0.0.1:5555/metrics` | `celery.*` | Celery 5.7.0, Flower 2.2.0 |
| n8n | `n8n` | `http://127.0.0.1:5678/metrics` | `n8n.*` | n8n 2.48.6 |
| Temporal | `temporal` | `http://127.0.0.1:9090/metrics` | `temporal.*` | Temporal 1.32.2 |
| GitLab (Rails, Puma) | `gitlab` | `http://127.0.0.1:8080/-/metrics` | `gitlab.*` | GitLab CE 19.6.1 |
| GitLab (Sidekiq) | `gitlab_sidekiq` | `http://127.0.0.1:8082/metrics` | `gitlab_sidekiq.*` | GitLab CE 19.6.1 |
| GitLab (Gitaly) | `gitaly` | `http://127.0.0.1:9236/metrics` | `gitaly.*` | GitLab CE 19.6.1 |
| SonarQube | `sonarqube` | `http://127.0.0.1:9000/api/monitoring/metrics` | `sonarqube.*` | Community Build 26.11 |
| Apache Solr | `solr` | `http://127.0.0.1:8983/solr/admin/metrics?wt=prometheus` | `solr.*` | Solr 10.1.0 and 9.11.0 |
| WildFly | `wildfly` | `http://127.0.0.1:9990/metrics` | `wildfly.*` | WildFly 42.0.0 |
| Apache Tomcat | `tomcat` | `http://127.0.0.1:9404/metrics` (JMX exporter) | `tomcat.*` | Tomcat 11.0.29 |
| Apache Kafka | `kafka` | `http://127.0.0.1:9405/metrics` (JMX exporter) | `kafka.*` | Kafka 4.3.1 (KRaft) |
| Apache ActiveMQ Classic | `activemq` | `http://127.0.0.1:9406/metrics` (JMX exporter) | `activemq.*` | ActiveMQ 6.2.0 |
| Apache Cassandra | `cassandra` | `http://127.0.0.1:9407/metrics` (JMX exporter) | `cassandra.*` | Cassandra 5.0.9 |
| cert-manager | `cert_manager` | `http://127.0.0.1:9402/metrics` | `cert_manager.*` | cert-manager 1.21.3 (k3s 1.35) |
| Velero | `velero` | `http://127.0.0.1:8085/metrics` | `velero.*` | Velero 1.18.2 (k3s 1.35) |
| Argo CD (controller) | `argocd` | `http://127.0.0.1:8082/metrics` | `argocd.*` | Argo CD 3.6.2 (k3s 1.35) |
| Argo CD (repositories) | `argocd_repo_server` | `http://127.0.0.1:8084/metrics` | `argocd_repo_server.*` | Argo CD 3.6.2 (k3s 1.35) |
| Linkerd (proxy) | `linkerd` | `http://127.0.0.1:4191/metrics` | `linkerd.*` | Linkerd edge-26.11.2 (k3s 1.35) |
| KEDA | `keda` | `http://127.0.0.1:8080/metrics` | `keda.*` | KEDA 2.21.1 (k3s 1.35) |

"Tested" means the product ran in a container, the agent read it and produced the names described here. A few families documented by the vendor but absent during the test (they only appear after their event) are kept and pointed out below.

## Declaring a product

One entry per product in `integrations.openmetrics`:

```yaml
integrations:
  openmetrics:
    - profile: consul                       # default address from the table above
    - profile: traefik
      url: http://127.0.0.1:8082            # no path: the profile's path is added
    - profile: vault
      url: https://vault.lan:8200           # ?format=prometheus is added too
      ca_file: /etc/hypha/vault-ca.pem
      bearer_token_file: /etc/hypha/vault-metrics.token
    - profile: kafka
      name: kafka-1                         # two brokers on one machine: one name each
    - profile: kafka
      name: kafka-2
      url: http://127.0.0.1:9415
```

- `instance_name` is the profile name, or `name`; every series also carries `endpoint`.
- The `openmetrics.can_connect` check goes critical when the target stops answering; the **Software unreachable (OpenMetrics profile)** template watches it for a given `instance_name`.
- Counters become per-second rates, histograms and summaries give `.count`, `.sum`, `.avg` and their percentiles (`.p50`, `.p95`, `.p99`…).
- The entry's `metrics`, `rename`, `labels` and `tags` extend the profile; `max_series` (2,000) caps the series of a target.

## Infrastructure

### HashiCorp Consul

In the Consul agent configuration: `telemetry { prometheus_retention_time = "60s" disable_hostname = true }` (without a retention time the response is empty). With ACLs, an `agent:read` token (`bearer_token_file`).

- `consul.server.is_leader`, `consul.autopilot.healthy`, `consul.autopilot.failure_tolerance`, `consul.members.servers`, `consul.members.clients`.
- Catalog: `consul.catalog.nodes`, `.catalog.services`, `.catalog.service_instances`, `consul.kv.entries`.
- Raft: `consul.raft.apply` (per s), `consul.raft.commit_time` and `consul.raft.leader.last_contact` (summaries in ms: `.p50`, `.p90`, `.p99`), `consul.raft.state.candidate` and `.state.leader` (elections).
- `consul.rpc.request`, `consul.rpc.request_error`, `consul.http.request` (ms, labels `method` and `path`), `consul.kvs.apply`, `consul.runtime.alloc`, `consul.runtime.goroutines`, `consul.agent.tls_cert_expiry`.

Consul does not publish the status of its health checks in Prometheus format.

### HashiCorp Vault

In the Vault configuration: `telemetry { prometheus_retention_time = "60s" disable_hostname = true }`. No token needed if the listener has `telemetry { unauthenticated_metrics_access = true }`, otherwise a token whose policy reads `sys/metrics` (`bearer_token_file`). The profile targets HTTPS, like a production Vault.

- `vault.core.unsealed` (0 = sealed), `vault.core.active`, `vault.core.performance_standby`, `vault.core.in_flight_requests`.
- Durations in ms (`.count`, `.p50`, `.p90`, `.p99`): `vault.core.handle_request`, `.core.handle_login_request`, `.core.check_token`, `vault.barrier.get`, `vault.barrier.put`.
- `vault.expire.num_leases`, `vault.expire.num_irrevocable_leases`, `vault.token.count_by_ttl`, `vault.identity.entity_count`, `vault.runtime.alloc`, `vault.runtime.goroutines`.
- Kept without being seen during the test (dev mode): `vault.audit.log_request_failure`, `vault.audit.log_response_failure` (with an audit device), `vault.raft.leader.last_contact` (integrated storage).

### etcd

Client port 2379, or the `--listen-metrics-urls` address (`http://127.0.0.1:2381` with kubeadm).

- `etcd.server.has_leader`, `etcd.server.is_leader`, `etcd.server.leader_changes`, `etcd.server.proposals.failed`, `.proposals.pending`, `.proposals.committed`, `.proposals.applied`, `etcd.server.slow_apply`, `etcd.server.heartbeat_failures`.
- Database: `etcd.db.size`, `etcd.db.size_in_use`, `etcd.server.quota_backend`, `etcd.mvcc.keys`, `etcd.mvcc.put`, `.range`, `.txn`, `.delete`.
- Disk (histograms, s): `etcd.disk.wal_fsync.p99`, `etcd.disk.backend_commit.p99`. Network: `etcd.network.peer_round_trip`, `etcd.network.peer_sent_failures` (label `to`, multi-member clusters), `etcd.grpc.handled` by `grpc_code`.

## Network

### CoreDNS

`prometheus :9153` plugin in the Corefile.

- `coredns.requests` (labels `server`, `zone`, `type`), `coredns.responses` (`rcode`), `coredns.request_duration` (s, `.p95`).
- Cache: `coredns.cache.entries`, `.cache.hits`, `.cache.misses`. Forwarding: `coredns.proxy.request_duration` (CoreDNS ≥ 1.11) or `coredns.forward.*` (earlier versions).
- `coredns.panics`, `coredns.reload.failed`, `coredns.process.resident_memory`.

### Traefik Proxy

`--metrics.prometheus=true`; the `traefik` entry point (8080) serves the metrics, or the one set by `--metrics.prometheus.entryPoint`. Router and service labels with `addRoutersLabels` and `addServicesLabels`.

- `traefik.entrypoint.requests` (`code`, `entrypoint`), `traefik.entrypoint.request_duration` (s), `.requests_bytes`, `.responses_bytes`, `traefik.open_connections`.
- `traefik.service.requests`, `traefik.service.request_duration`, `traefik.service.server_up` (`service`, `url`; 0 = failing health check), `traefik.router.requests`.
- `traefik.config.reloads`, `traefik.config.last_reload_success`, `traefik.tls.certs_not_after` (timestamp, label `cn`).

### Envoy Proxy

Admin interface (`admin`, 9901), path `/stats/prometheus`. Durations in milliseconds.

- `envoy.server.live`, `envoy.server.state`, `envoy.server.uptime`, `envoy.server.memory.allocated`, `envoy.server.total_connections`, `envoy.server.days_until_first_cert_expiring`.
- Upstream (`envoy_cluster_name`): `envoy.cluster.upstream.rq_total`, `.rq_xx` (`envoy_response_code_class`: 2, 4, 5…), `.rq_time.p99`, `.cx_active`, `.cx_connect_fail`, `.rq_timeout`, `.rq_retry`, `envoy.cluster.membership.healthy` and `.total`.
- Downstream (`envoy_http_conn_manager_prefix`): `envoy.http.downstream.rq_total`, `.rq_xx`, `.rq_time`, `.cx_active`; `envoy.listener.downstream.cx_active`.

### Kong Gateway

`prometheus` plugin enabled (globally or per service; `status_code_metrics`, `latency_metrics`, `bandwidth_metrics`, `upstream_health_metrics` set to `true`), read on the status listener (`status_listen = 0.0.0.0:8100`) or the Admin API. Latencies in milliseconds.

- `kong.http.requests` (`service`, `route`, `code`), `kong.request_latency`, `kong.upstream_latency`, `kong.kong_latency` (`.p95`), `kong.bandwidth` (`direction`).
- `kong.datastore.reachable`, `kong.upstream.target_health` (`upstream`, `target`, `state`), `kong.nginx.connections` (`state`), `kong.memory.shared_dict.used` and `.total`.

## Databases

### CockroachDB

`/_status/vars` on the HTTP port (8080), over HTTPS unless `--insecure` (`ca_file` pointing at the cluster CA), no authentication. The exposition is large (≈ 1.8 MB): `interval: 30s` is enough. Latencies in nanoseconds.

- `cockroachdb.liveness.live_nodes`, `cockroachdb.ranges.unavailable`, `.ranges.underreplicated`, `.ranges.overreplicated`, `cockroachdb.replicas.*`, `cockroachdb.capacity.total`, `.available`, `.used`.
- SQL: `cockroachdb.sql.conns`, `.select`, `.insert`, `.update`, `.delete`, `.failure`, `.txn.commit`, `.txn.abort`, `cockroachdb.sql.service_latency.p99`, `.exec_latency`, `.txn_latency`.
- `cockroachdb.sys.cpu.combined_normalized`, `cockroachdb.sys.rss`, `cockroachdb.clock_offset.mean_nanos`, `cockroachdb.requests.slow.*`, `cockroachdb.storage.l0_sublevels`, `cockroachdb.admission.io_overload`. Labels `node_id` and `store`.

### ScyllaDB

`/metrics` on port 9180. Series are summed over all cores (the `shard` label is dropped); coordinator latencies in microseconds.

- `scylla.reactor.utilization` (%), `scylla.node.operation_mode` (3 = normal), `scylla.gossip.live`.
- `scylla.coordinator.read_latency` and `.write_latency` (`.p99`), `scylla.cql.requests_served`, `.connections`, `.reads`, `.inserts`, `.errors` (`type`).
- `scylla.database.reads`, `.writes`, `.reads_failed`, `.writes_failed`, `.writes_timedout`, `scylla.cache.row_hits`, `.row_misses`, `scylla.compaction.running`, `.pending`, `scylla.memory.allocated`, `.free`.

## Message queues

### Apache Pulsar

The broker's `/metrics/` (HTTP port 8080). Broker and per-Pulsar-namespace aggregates: the `topic` label is dropped, so one more topic does not mean one more series.

- `pulsar.broker.topics`, `.subscriptions`, `.producers`, `.consumers`, `.rate_in`, `.rate_out` (messages/s), `.throughput_in`, `.throughput_out` (bytes/s), `.msg_backlog`, `.storage_size`, `.publish_latency` (ms, `.p99`).
- Per namespace (`namespace`): `pulsar.namespace.topics`, `.msg_backlog`, `.rate_in`, `.rate_out`, `.storage_size`. JVM: `pulsar.jvm.memory.used` (`area`), `pulsar.jvm.gc.collection`.

### Celery (through Flower)

Flower publishes the workers' metrics on `/metrics` (5555). Workers must send their events (`celery worker -E`, or `worker_send_task_events = True`).

- `celery.worker.online` (`worker`), `celery.worker.executing_tasks`, `celery.worker.prefetched_tasks`.
- `celery.events` by `type` (`task-received`, `task-started`, `task-succeeded`, `task-failed`…) and `task`, `celery.task.runtime` (s, `.p95`), `celery.task.prefetch_time`.

## Applications

### n8n

`N8N_METRICS=true`. Workflow executions with `N8N_METRICS_INCLUDE_MESSAGE_EVENT_BUS_METRICS=true`, per workflow with `N8N_METRICS_INCLUDE_WORKFLOW_ID_LABEL=true`.

- `n8n.workflows.active`, `n8n.workflow.started`, `.success`, `.failed` (per s), `n8n.workflow.execution_duration` (`status`, `.p95`).
- Queue mode (`EXECUTIONS_MODE=queue`): `n8n.queue.jobs.waiting`, `.active`, `.completed`, `.failed` (kept, not tested: the test ran in regular mode).
- `n8n.nodejs.eventloop_lag_p99`, `n8n.nodejs.heap_used`, `n8n.process.resident_memory`, `n8n.process.cpu_seconds`.

### Temporal

The server's `global.metrics.prometheus.listenAddress` (9090 in the Helm chart, 8000 in the Docker examples: adjust `url`). Durations in seconds.

- `temporal.service.requests` and `.errors` (`service_name`, `namespace`, `error_type`), `temporal.service.latency` (`.p95`), `temporal.service.pending_requests`.
- `temporal.persistence.requests`, `.errors`, `.latency`.
- Task queues (`namespace`, `taskqueue`, `task_type`): `temporal.task_queue.backlog`, `.backlog_age` (s), `.poll_timeouts`.
- Workflow outcomes (`workflowtype`): `temporal.workflow.success`, `.failed`, `.timeout`, `.terminate`, `.cancel`, `.continued_as_new`, `.schedule_to_close_latency`. Only `terminate` was seen during the test; the others appear with their event.

### GitLab

Three profiles for an Omnibus installation, read by the agent on the GitLab machine:

- `gitlab`: Rails and Puma, `/-/metrics` on Puma (`127.0.0.1:8080`) or behind nginx; the agent's address must be in `gitlab_rails['monitoring_whitelist']` (127.0.0.0/8 by default).
- `gitlab_sidekiq`: `sidekiq['metrics_enabled'] = true` (`127.0.0.1:8082`).
- `gitaly`: `gitaly['configuration'][:prometheus_listen_addr]` (`127.0.0.1:9236`).

Metrics:

- `gitlab.http.requests` (`method`, `status`: 2xx… 5xx), `gitlab.http.request_duration`, `gitlab.transaction.duration`, `gitlab.sql.duration`, `gitlab.rails.queue_duration` (s).
- `gitlab.db.pool.busy`, `.waiting`, `.size`, `.dead` (`db_config_name`), `gitlab.redis.requests`, `.exceptions` (`storage`), `gitlab.sli.rails.requests`, `.errors`, `.apdex_total`, `.apdex_success`.
- `gitlab.puma.running`, `.queued_connections`, `.workers`, `gitlab.ruby.resident_memory`.
- `gitlab_sidekiq.jobs.completion` (`job_status`: `done`, `fail`), `gitlab_sidekiq.jobs.queue_duration`, `.jobs.running`, `.concurrency`.
- `gitaly.grpc.handled` (`grpc_code`), `gitaly.pack_objects.in_progress`, `.queued`, `gitaly.concurrency_limiting.queued`, `gitaly.commands.running`.

The test ran with reduced settings (Puma in single mode, 5 Sidekiq threads, bundled Prometheus off); the profile applies to the default configuration as well.

### SonarQube

`/api/monitoring/metrics` (SonarQube 9.3 and later), read with the system passcode header (`sonar.web.systemPasscode`) or an administrator token:

```yaml
    - profile: sonarqube
      headers:
        X-Sonar-Passcode: "the passcode"   # configuration file in mode 600
```

- `sonarqube.health.web`, `.health.compute_engine`, `.health.elasticsearch` (1 = up).
- `sonarqube.compute_engine.pending_tasks`, `.compute_engine.task_duration`, `sonarqube.elasticsearch.disk_usage` (%), `.disk_free`, `.read_only_indices`, `sonarqube.db.latency` (s), `sonarqube.web.api_v1.request_duration`, `.api_v2.request_duration`, `sonarqube.web.uptime_minutes`.

## Java

### The Prometheus JMX exporter

Tomcat, Kafka, ActiveMQ and Cassandra do not publish Prometheus metrics: their JMX MBeans are read by the official **Prometheus JMX exporter**, loaded as a javaagent in the JVM. Download `jmx_prometheus_javaagent-1.7.0.jar` from the releases of the `prometheus/jmx_exporter` project on GitHub (the SHA-256 sum is published next to it; Maven Central stops at 1.0.1), put the product's rules file (below) next to it, then add to the JVM:

```text
-javaagent:/opt/jmx/jmx_prometheus_javaagent-1.7.0.jar=127.0.0.1:9404:/opt/jmx/tomcat.yml
```

`127.0.0.1:` before the port keeps the exporter on the loopback. Each profile also keeps the JVM metrics published by the exporter: `<namespace>.jvm.memory.used`, `.memory.committed`, `.memory.max` (`area`: `heap`, `nonheap`), `<namespace>.jvm.gc.collection` (`.count`, `.sum`), `.jvm.threads.current`, `.jvm.threads.deadlocked`, `.jvm.classes.loaded`, `<namespace>.process.cpu_seconds`, `.process.open_fds`, `<namespace>.jmx.scrape_error` (1 = unreadable MBeans), `.jmx.scrape_duration`.

The rules restrict the MBeans read (`includeObjectNames`): a full scrape of Kafka or Cassandra is costly for the JVM and yields thousands of series.

### Apache Tomcat

In `bin/setenv.sh`: `CATALINA_OPTS="$CATALINA_OPTS -javaagent:/opt/jmx/jmx_prometheus_javaagent-1.7.0.jar=127.0.0.1:9404:/opt/jmx/tomcat.yml"`.

```yaml
# HYPHA rules for Apache Tomcat (Prometheus JMX exporter, javaagent 1.x).
lowercaseOutputName: true
lowercaseOutputLabelNames: true
includeObjectNames:
  - "Catalina:type=GlobalRequestProcessor,*"
  - "Catalina:type=ThreadPool,*"
  - "Catalina:type=Manager,*"
rules:
  - pattern: 'Catalina<type=GlobalRequestProcessor, name="?([^",>]+)"?><>(requestCount|errorCount|bytesReceived|bytesSent|processingTime):'
    name: tomcat_requestprocessor_$2_total
    labels:
      connector: "$1"
    type: COUNTER
  - pattern: 'Catalina<type=GlobalRequestProcessor, name="?([^",>]+)"?><>maxTime:'
    name: tomcat_requestprocessor_maxtime
    labels:
      connector: "$1"
    type: GAUGE
  - pattern: 'Catalina<type=ThreadPool, name="?([^",>]+)"?><>(currentThreadCount|currentThreadsBusy|maxThreads|connectionCount|maxConnections):'
    name: tomcat_threadpool_$2
    labels:
      connector: "$1"
    type: GAUGE
  - pattern: 'Catalina<type=Manager, host=([^,>]+), context=([^,>]+)><>(activeSessions|maxActiveSessions):'
    name: tomcat_session_$3
    labels:
      vhost: "$1"
      context: "$2"
    type: GAUGE
  - pattern: 'Catalina<type=Manager, host=([^,>]+), context=([^,>]+)><>(rejectedSessions|expiredSessions|sessionCounter):'
    name: tomcat_session_$3_total
    labels:
      vhost: "$1"
      context: "$2"
    type: COUNTER
```

- Per connector (`connector`): `tomcat.requests`, `tomcat.errors`, `tomcat.bytes_received`, `.bytes_sent`, `tomcat.processing_time` (ms per s), `tomcat.max_time`, `tomcat.threads.current`, `.busy`, `.max`, `tomcat.connections.current`, `.max`.
- Per application (`context`): `tomcat.sessions.active`, `.max_active`, `.created`, `.expired`, `.rejected`.

### Apache Kafka

`KAFKA_OPTS="-javaagent:/opt/jmx/jmx_prometheus_javaagent-1.7.0.jar=127.0.0.1:9405:/opt/jmx/kafka.yml"` before starting the broker. Kafka's tools (`kafka-*.sh`) also read `KAFKA_OPTS`: run them with an empty `KAFKA_OPTS=`, otherwise they grab the exporter's port.

```yaml
# HYPHA rules for Apache Kafka 3.x/4.x (KRaft or ZooKeeper), Prometheus JMX exporter 1.x.
lowercaseOutputName: true
lowercaseOutputLabelNames: true
includeObjectNames:
  - "kafka.server:type=BrokerTopicMetrics,*"
  - "kafka.server:type=ReplicaManager,*"
  - "kafka.server:type=KafkaRequestHandlerPool,*"
  - "kafka.controller:type=KafkaController,*"
  - "kafka.controller:type=ControllerStats,*"
  - "kafka.network:type=RequestMetrics,name=TotalTimeMs,*"
  - "kafka.network:type=RequestMetrics,name=RequestsPerSec,*"
  - "kafka.network:type=SocketServer,*"
  - "kafka.log:type=LogFlushStats,*"
rules:
  - pattern: 'kafka.server<type=BrokerTopicMetrics, name=(MessagesInPerSec|BytesInPerSec|BytesOutPerSec|BytesRejectedPerSec|FailedProduceRequestsPerSec|FailedFetchRequestsPerSec|TotalProduceRequestsPerSec|TotalFetchRequestsPerSec)><>Count:'
    name: kafka_server_brokertopicmetrics_$1_total
    type: COUNTER
  - pattern: 'kafka.server<type=ReplicaManager, name=(UnderReplicatedPartitions|UnderMinIsrPartitionCount|AtMinIsrPartitionCount|OfflineReplicaCount|PartitionCount|LeaderCount)><>Value:'
    name: kafka_server_replicamanager_$1
    type: GAUGE
  - pattern: 'kafka.server<type=ReplicaManager, name=(IsrShrinksPerSec|IsrExpandsPerSec)><>Count:'
    name: kafka_server_replicamanager_$1_total
    type: COUNTER
  - pattern: 'kafka.server<type=KafkaRequestHandlerPool, name=RequestHandlerAvgIdlePercent><>OneMinuteRate:'
    name: kafka_server_requesthandler_avg_idle_percent
    type: GAUGE
  - pattern: 'kafka.controller<type=KafkaController, name=(ActiveControllerCount|OfflinePartitionsCount|GlobalTopicCount|GlobalPartitionCount|PreferredReplicaImbalanceCount|ActiveBrokerCount|FencedBrokerCount)><>Value:'
    name: kafka_controller_$1
    type: GAUGE
  - pattern: 'kafka.controller<type=ControllerStats, name=(UncleanLeaderElectionsPerSec|LeaderElectionRateAndTimeMs)><>Count:'
    name: kafka_controller_stats_$1_total
    type: COUNTER
  - pattern: 'kafka.network<type=RequestMetrics, name=TotalTimeMs, request=(Produce|FetchConsumer|FetchFollower|Metadata|OffsetCommit)><>(Mean|99thPercentile):'
    name: kafka_network_request_totaltimems_$2
    labels:
      request: "$1"
    type: GAUGE
  - pattern: 'kafka.network<type=RequestMetrics, name=RequestsPerSec, request=(Produce|FetchConsumer|FetchFollower|Metadata|OffsetCommit), version=(\d+)><>Count:'
    name: kafka_network_requests_total
    labels:
      request: "$1"
      version: "$2"
    type: COUNTER
  - pattern: 'kafka.network<type=SocketServer, name=NetworkProcessorAvgIdlePercent><>Value:'
    name: kafka_network_processor_avg_idle_percent
    type: GAUGE
  - pattern: 'kafka.log<type=LogFlushStats, name=LogFlushRateAndTimeMs><>Count:'
    name: kafka_log_flushes_total
    type: COUNTER
  - pattern: 'kafka.log<type=LogFlushStats, name=LogFlushRateAndTimeMs><>99thPercentile:'
    name: kafka_log_flush_time_ms_p99
    type: GAUGE
```

- Broker (all topics): `kafka.broker.messages_in`, `.bytes_in`, `.bytes_out`, `.bytes_rejected`, `.produce.requests`, `.produce.failed`, `.fetch.requests`, `.fetch.failed`.
- Replication: `kafka.replication.under_replicated_partitions`, `.under_min_isr_partitions`, `.offline_replicas`, `.partitions`, `.leaders`, `.isr_shrinks`, `.isr_expands`.
- Controller: `kafka.controller.active` (1 on the active controller), `.offline_partitions`, `.topics`, `.partitions`, `.active_brokers`, `.fenced_brokers`, `.unclean_leader_elections`.
- Requests (`request`: Produce, FetchConsumer, FetchFollower, Metadata, OffsetCommit): `kafka.request.count`, `kafka.request.total_time.mean` and `.p99` (ms). Idleness: `kafka.network.processor_avg_idle`, `kafka.request_handler.avg_idle` (fraction, 1 = idle). `kafka.log.flushes`, `kafka.log.flush_time.p99`.

Consumer lag is read at the consumers, not in the broker.

### Apache ActiveMQ Classic

`ACTIVEMQ_OPTS` (in `bin/setenv` or the environment): the value replaces the script's, so keep the memory and login settings, for example `-Xms64M -Xmx1G -Djava.util.logging.config.file=logging.properties -Djava.security.auth.login.config=$ACTIVEMQ_CONF/login.config -javaagent:/opt/jmx/jmx_prometheus_javaagent-1.7.0.jar=127.0.0.1:9406:/opt/jmx/activemq.yml`. The `activemq producer`, `consumer`… commands also read `ACTIVEMQ_OPTS`: give them a value without the javaagent.

```yaml
# HYPHA rules for Apache ActiveMQ Classic 5.x/6.x (Prometheus JMX exporter 1.x). The internal
# advisory destinations (ActiveMQ.Advisory.*) are left out.
lowercaseOutputName: true
lowercaseOutputLabelNames: true
includeObjectNames:
  - "org.apache.activemq:type=Broker,brokerName=*"
  - "org.apache.activemq:type=Broker,brokerName=*,destinationType=*,destinationName=*"
rules:
  - pattern: 'org.apache.activemq<type=Broker, brokerName=([^,>]+), destinationType=(Queue|Topic), destinationName=((?!ActiveMQ\.Advisory)[^,>]+)><>(QueueSize|ConsumerCount|ProducerCount|InFlightCount|MemoryPercentUsage|AverageEnqueueTime):'
    name: activemq_destination_$4
    labels:
      broker: "$1"
      destination_type: "$2"
      destination: "$3"
    type: GAUGE
  - pattern: 'org.apache.activemq<type=Broker, brokerName=([^,>]+), destinationType=(Queue|Topic), destinationName=((?!ActiveMQ\.Advisory)[^,>]+)><>(EnqueueCount|DequeueCount|ExpiredCount|DispatchCount):'
    name: activemq_destination_$4_total
    labels:
      broker: "$1"
      destination_type: "$2"
      destination: "$3"
    type: COUNTER
  - pattern: 'org.apache.activemq<type=Broker, brokerName=([^,>]+)><>(MemoryPercentUsage|StorePercentUsage|TempPercentUsage|CurrentConnectionsCount|TotalConsumerCount|TotalProducerCount|TotalMessageCount):'
    name: activemq_broker_$2
    labels:
      broker: "$1"
    type: GAUGE
  - pattern: 'org.apache.activemq<type=Broker, brokerName=([^,>]+)><>(TotalEnqueueCount|TotalDequeueCount):'
    name: activemq_broker_$2_total
    labels:
      broker: "$1"
    type: COUNTER
```

- Broker (`broker`): `activemq.broker.memory_usage`, `.store_usage`, `.temp_usage` (% of the limits; at 100 % producers are blocked), `.connections`, `.consumers`, `.producers`, `.messages`, `.enqueued`, `.dequeued`.
- Queues and topics (`destination`, `destination_type`), without the `ActiveMQ.Advisory.*` advisory destinations: `activemq.destination.size`, `.consumers`, `.producers`, `.in_flight`, `.memory_usage`, `.average_enqueue_time`, `.enqueued`, `.dequeued`, `.dispatched`, `.expired`.

### Apache Cassandra

`JVM_EXTRA_OPTS="-javaagent:/opt/jmx/jmx_prometheus_javaagent-1.7.0.jar=127.0.0.1:9407:/opt/jmx/cassandra.yml"` (read by `cassandra-env.sh`). Client request latencies in microseconds.

```yaml
# HYPHA rules for Apache Cassandra 4.x/5.x (Prometheus JMX exporter 1.x). Client request
# latencies in microseconds (Cassandra timers).
lowercaseOutputName: true
lowercaseOutputLabelNames: true
includeObjectNames:
  - "org.apache.cassandra.metrics:type=ClientRequest,*"
  - "org.apache.cassandra.metrics:type=Storage,*"
  - "org.apache.cassandra.metrics:type=Compaction,*"
  - "org.apache.cassandra.metrics:type=ThreadPools,*"
  - "org.apache.cassandra.metrics:type=DroppedMessage,*"
  - "org.apache.cassandra.metrics:type=Client,*"
  - "org.apache.cassandra.metrics:type=Cache,*"
  - "org.apache.cassandra.metrics:type=CommitLog,*"
rules:
  - pattern: 'org.apache.cassandra.metrics<type=ClientRequest, scope=(Read|Write|CASRead|CASWrite|RangeSlice), name=Latency><>Count:'
    name: cassandra_clientrequest_requests_total
    labels:
      scope: "$1"
    type: COUNTER
  - pattern: 'org.apache.cassandra.metrics<type=ClientRequest, scope=(Read|Write|CASRead|CASWrite|RangeSlice), name=Latency><>(Mean|99thPercentile):'
    name: cassandra_clientrequest_latency_$2
    labels:
      scope: "$1"
    type: GAUGE
  - pattern: 'org.apache.cassandra.metrics<type=ClientRequest, scope=(Read|Write|CASRead|CASWrite|RangeSlice), name=(Timeouts|Unavailables|Failures)><>Count:'
    name: cassandra_clientrequest_$2_total
    labels:
      scope: "$1"
    type: COUNTER
  - pattern: 'org.apache.cassandra.metrics<type=Storage, name=(Load|TotalHintsInProgress)><>Count:'
    name: cassandra_storage_$1
    type: GAUGE
  - pattern: 'org.apache.cassandra.metrics<type=Storage, name=(Exceptions|TotalHints)><>Count:'
    name: cassandra_storage_$1_total
    type: COUNTER
  - pattern: 'org.apache.cassandra.metrics<type=Compaction, name=(PendingTasks)><>Value:'
    name: cassandra_compaction_$1
    type: GAUGE
  - pattern: 'org.apache.cassandra.metrics<type=Compaction, name=(CompletedTasks)><>Value:'
    name: cassandra_compaction_$1_total
    type: COUNTER
  - pattern: 'org.apache.cassandra.metrics<type=Compaction, name=(BytesCompacted)><>Count:'
    name: cassandra_compaction_$1_total
    type: COUNTER
  - pattern: 'org.apache.cassandra.metrics<type=ThreadPools, path=(\w+), scope=(\w+), name=(ActiveTasks|PendingTasks)><>Value:'
    name: cassandra_threadpools_$3
    labels:
      path: "$1"
      pool: "$2"
    type: GAUGE
  - pattern: 'org.apache.cassandra.metrics<type=ThreadPools, path=(\w+), scope=(\w+), name=(TotalBlockedTasks)><>Count:'
    name: cassandra_threadpools_$3_total
    labels:
      path: "$1"
      pool: "$2"
    type: COUNTER
  - pattern: 'org.apache.cassandra.metrics<type=DroppedMessage, scope=(\w+), name=Dropped><>Count:'
    name: cassandra_droppedmessage_dropped_total
    labels:
      message_type: "$1"
    type: COUNTER
  - pattern: 'org.apache.cassandra.metrics<type=Client, name=(connectedNativeClients)><>Value:'
    name: cassandra_client_$1
    type: GAUGE
  - pattern: 'org.apache.cassandra.metrics<type=Cache, scope=(KeyCache|RowCache|CounterCache), name=(HitRate|Size|Capacity)><>Value:'
    name: cassandra_cache_$2
    labels:
      cache: "$1"
    type: GAUGE
  - pattern: 'org.apache.cassandra.metrics<type=CommitLog, name=(PendingTasks|TotalCommitLogSize)><>Value:'
    name: cassandra_commitlog_$1
    type: GAUGE
```

- Client requests (`scope`: Read, Write, CASRead, CASWrite, RangeSlice): `cassandra.client_request.count`, `.latency.mean`, `.latency.p99`, `.timeouts`, `.unavailables`, `.failures`.
- `cassandra.storage.load`, `.exceptions`, `.hints`, `cassandra.compaction.pending_tasks`, `.completed_tasks`, `.bytes_compacted`, `cassandra.thread_pools.pending_tasks`, `.active_tasks`, `.blocked_tasks` (`pool`), `cassandra.dropped_messages`, `cassandra.client.connected_native_clients`, `cassandra.cache.hit_rate` (`cache`), `cassandra.commitlog.pending_tasks`.

### Apache Solr (native metrics)

Solr 9 and 10 publish their metrics in Prometheus format on `/solr/admin/metrics?wt=prometheus`, without an exporter (Solr 10 has no JMX MBeans left to read). Solr 10 uses OpenTelemetry names, Solr 9 `solr_metrics_*` names: both sets are kept.

- Solr 10 (`core`, `category`): `solr.core.requests`, `solr.core.request_errors`, `solr.core.request_time` (ms, `.p95`), `solr.core.num_docs`, `solr.core.index_size_mb`, `solr.core.segments`, `solr.core.cache.lookups` (`result`: hit, miss), `solr.core.update.docs_pending`, `solr.cores`, `solr.disk_space_mb`, `solr.jvm.memory.used` (`jvm_memory_type`), `solr.jvm.gc.duration`, `solr.jvm.threads`.
- Solr 9: `solr.metrics.core.requests` (`type`: requests, errors, timeouts…), `solr.metrics.core.request_time` (ms, summary), `solr.metrics.core.index_size`, `solr.metrics.core.searcher_documents`, `solr.metrics.jetty.responses` (`status`), `solr.metrics.node.cores`.

### WildFly (native metrics)

WildFly publishes its metrics on `/metrics` of the management interface (9990, `metrics` subsystem), without an exporter. Undertow, datasource and transaction statistics with `-Dwildfly.statistics-enabled=true` (or `statistics-enabled` per subsystem). The JMX exporter as a javaagent does not start on WildFly 42 (conflict with the JBoss LogManager): the native endpoint is the way to go.

- Undertow (`http_listener`, `https_listener`): `wildfly.undertow.requests`, `.errors`, `.max_processing_time` (s).
- Datasources (`data_source`): `wildfly.datasource.pool.active`, `.available`, `.in_use`, `.idle`, `.max_used`, `.wait_count`, `.timed_out`, `.average_get_time` (ms).
- Transactions: `wildfly.transactions.started`, `.committed`, `.aborted`, `.timed_out`, `.application_rollbacks`, `.resource_rollbacks`, `.inflight`.
- `wildfly.jvm.memory.heap_used`, `.heap_max`, `.nonheap_used`, `wildfly.jvm.threads`, `wildfly.jvm.gc.collections`, `wildfly.process.cpu_load`, `wildfly.ee.hung_threads`.

## Kubernetes

These products run inside the cluster: declare them in the agent that already reads the cluster (see [Kubernetes](/en/docs/kubernetes/)), with the address of their service, for example:

```yaml
integrations:
  openmetrics:
    - profile: cert_manager
      url: http://cert-manager.cert-manager.svc:9402
    - profile: velero
      url: http://velero.velero.svc:8085
    - profile: argocd
      url: http://argocd-metrics.argocd.svc:8082
    - profile: argocd_repo_server
      url: http://argocd-repo-server.argocd.svc:8084
    - profile: keda
      url: http://keda-operator.keda.svc:8080
```

They were tested in a throwaway k3s 1.35 cluster (k3d), read through port forwarding.

### cert-manager

Helm chart with `prometheus.enabled=true` (service on 9402).

- `cert_manager.certificate.ready` (`condition`: True, False, Unknown; `namespace`, `name`, `issuer_name`), `cert_manager.clusterissuer.ready`, `cert_manager.issuer.ready`.
- `cert_manager.certificate.expiration_timestamp` and `.renewal_timestamp`: Unix timestamps (seconds), `cert_manager.clock_time` (cert-manager's clock), `cert_manager.controller.sync_calls` (`controller`).

The expiry is published as a timestamp: HYPHA shows it as is, and the **cert-manager certificate not ready** template alerts when issuance or renewal fails.

### Velero

Velero server, `/metrics` on 8085 (`metrics.enabled` in the chart).

- `velero.backup.attempts`, `.success`, `.failure`, `.partial_failure`, `.validation_failure` (per s), `velero.backup.last_status` (`schedule`; 1 = succeeded, 0 = failed), `velero.backups`, `velero.backup.items_errors`.
- `velero.backup_location.available` (`backup_location_name`; 0 = storage unreachable), `velero.restore.success`, `.failed`, `.partial_failure`.
- `velero.backup.last_successful_timestamp` (timestamp of the last successful backup): kept, not seen during the test, whose backup failed for lack of storage.

### Argo CD

- `argocd`: application controller, 8082 (`argocd-metrics` service). `argocd.app.info` is 1 per application and its labels carry the state: `name`, `health_status` (Healthy, Progressing, Degraded, Missing…), `sync_status` (Synced, OutOfSync, Unknown). `argocd.app.syncs` (`phase`), `argocd.app.reconcile` (s), `argocd.app.orphaned_resources`, `argocd.cluster.connection_status`, `.api_resource_objects`, `.cache_age`.
- `argocd_repo_server`: repository server, 8084. `argocd_repo_server.git.requests` (`request_type`: fetch, ls-remote), `.git.request_duration`, `.pending_requests`.

### Linkerd

Each meshed pod's proxy publishes its metrics on `:4191/metrics`. For the whole mesh, rather point at the `linkerd-viz` Prometheus (`/federate?match[]={job="linkerd-proxy"}`), which returns the same names with the `namespace` and `deployment` labels (not tested: the test read one proxy).

- `linkerd.requests` (`direction`: inbound, outbound), `linkerd.responses` (`classification`: success, failure; `status_code`), `linkerd.response_latency` (ms, `.p95`).
- `linkerd.tcp.open_connections`, `.opens`, `.closes`, `linkerd.inbound.authz_allowed` and `.authz_denied`, `linkerd.identity.cert_expiration_timestamp`, `linkerd.identity.cert_refreshes` (`result`).

### KEDA

KEDA operator, `/metrics` on 8080 (`prometheus.operator.enabled=true` in the chart).

- `keda.scaler.errors` (`namespace`, `scaledobject`, `scaler`), `keda.scaled_object.errors`, `keda.scaler.active`, `keda.scaler.metrics_value`, `keda.scaler.metrics_latency` (s), `keda.scale_loop_latency` (s), `keda.scaled_object.paused`, `keda.resources.registered`, `keda.triggers.registered`.

## Monitor templates

In **Monitors › New › From a template**:

- **Generic collection**: software unreachable (by `instance_name`), JMX scrape failing.
- **Infrastructure**: unhealthy Consul cluster, sealed Vault, etcd without a leader, etcd disk too slow.
- **Network**: CoreDNS SERVFAIL responses, Traefik upstream server down, Envoy upstream 5xx errors, Kong cannot reach its database, unhealthy Kong upstream target.
- **Databases**: unavailable or under-replicated CockroachDB ranges, Cassandra timeouts and unavailable replicas, Cassandra compactions lagging, ScyllaDB saturated, failing ScyllaDB writes.
- **Message queues**: under-replicated or offline Kafka partitions, ActiveMQ broker memory or store saturated, ActiveMQ queue backing up, Pulsar backlog, Celery worker offline, failing Celery tasks.
- **Applications**: aging Temporal task queue, Temporal persistence errors, failing n8n workflows, GitLab 5xx errors, requests waiting in Puma, failing Sidekiq jobs, SonarQube component down, SonarQube Elasticsearch disk.
- **Web**: Tomcat errors, WildFly errors and timed-out transactions; **Search**: Solr request errors.
- **Kubernetes**: cert-manager certificate not ready, last Velero backup failed, Velero storage unreachable, Argo CD application degraded or out of sync, failing responses in the Linkerd mesh, KEDA scalers in error.

## Ready dashboards

**Dashboards › Catalog**: one dashboard per product (Consul, Vault, etcd, CoreDNS, Traefik, Envoy, Kong, CockroachDB, n8n, Temporal, Celery, Tomcat, Kafka, ActiveMQ, Cassandra, Solr, WildFly, Pulsar, ScyllaDB, SonarQube, GitLab, cert-manager, Velero, Argo CD, Linkerd, KEDA), installed in one click, then editable like any dashboard.
