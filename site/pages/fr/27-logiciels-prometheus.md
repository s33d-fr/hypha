---
id: prometheus-software
path: /docs/logiciels-prometheus/
title: Logiciels exposant Prometheus : Consul, Vault, Traefik, Kafka, GitLab, Argo CD…
nav: Logiciels Prometheus
section: Intégrations
order: 27
updated: 2026-11-20
description: Superviser avec HYPHA les logiciels qui publient leurs mesures au format Prometheus (Consul, Vault, etcd, CoreDNS, Traefik, Envoy, Kong, CockroachDB, ScyllaDB, Pulsar, Celery, n8n, Temporal, GitLab, SonarQube, Solr, WildFly, cert-manager, Velero, Argo CD, Linkerd, KEDA) et les logiciels Java par l'exportateur JMX Prometheus (Tomcat, Kafka, ActiveMQ, Cassandra).
lead: Vingt-neuf profils prêts pour le lecteur OpenMetrics de l'agent : une ligne de conf par logiciel, des noms de mesures propres, des modèles de moniteurs et un tableau au catalogue pour chacun.
---
## En un coup d'œil

Chaque logiciel est lu par le [lecteur OpenMetrics](/docs/collecte-generique/) de l'agent, avec un **profil** qui donne le port, le chemin, les familles retenues, leurs noms HYPHA et les étiquettes gardées. Les profils ont été écrits sur les expositions des vrais logiciels, lancés en conteneur dans un environnement d'essai de S33D (versions dans la dernière colonne).

| Logiciel | Profil | Adresse par défaut | Espace de noms | Éprouvé sur |
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
| Celery (par Flower) | `celery` | `http://127.0.0.1:5555/metrics` | `celery.*` | Celery 5.7.0, Flower 2.2.0 |
| n8n | `n8n` | `http://127.0.0.1:5678/metrics` | `n8n.*` | n8n 2.48.6 |
| Temporal | `temporal` | `http://127.0.0.1:9090/metrics` | `temporal.*` | Temporal 1.32.2 |
| GitLab (Rails, Puma) | `gitlab` | `http://127.0.0.1:8080/-/metrics` | `gitlab.*` | GitLab CE 19.6.1 |
| GitLab (Sidekiq) | `gitlab_sidekiq` | `http://127.0.0.1:8082/metrics` | `gitlab_sidekiq.*` | GitLab CE 19.6.1 |
| GitLab (Gitaly) | `gitaly` | `http://127.0.0.1:9236/metrics` | `gitaly.*` | GitLab CE 19.6.1 |
| SonarQube | `sonarqube` | `http://127.0.0.1:9000/api/monitoring/metrics` | `sonarqube.*` | Community Build 26.11 |
| Apache Solr | `solr` | `http://127.0.0.1:8983/solr/admin/metrics?wt=prometheus` | `solr.*` | Solr 10.1.0 et 9.11.0 |
| WildFly | `wildfly` | `http://127.0.0.1:9990/metrics` | `wildfly.*` | WildFly 42.0.0 |
| Apache Tomcat | `tomcat` | `http://127.0.0.1:9404/metrics` (exportateur JMX) | `tomcat.*` | Tomcat 11.0.29 |
| Apache Kafka | `kafka` | `http://127.0.0.1:9405/metrics` (exportateur JMX) | `kafka.*` | Kafka 4.3.1 (KRaft) |
| Apache ActiveMQ Classic | `activemq` | `http://127.0.0.1:9406/metrics` (exportateur JMX) | `activemq.*` | ActiveMQ 6.2.0 |
| Apache Cassandra | `cassandra` | `http://127.0.0.1:9407/metrics` (exportateur JMX) | `cassandra.*` | Cassandra 5.0.9 |
| cert-manager | `cert_manager` | `http://127.0.0.1:9402/metrics` | `cert_manager.*` | cert-manager 1.21.3 (k3s 1.35) |
| Velero | `velero` | `http://127.0.0.1:8085/metrics` | `velero.*` | Velero 1.18.2 (k3s 1.35) |
| Argo CD (contrôleur) | `argocd` | `http://127.0.0.1:8082/metrics` | `argocd.*` | Argo CD 3.6.2 (k3s 1.35) |
| Argo CD (dépôts) | `argocd_repo_server` | `http://127.0.0.1:8084/metrics` | `argocd_repo_server.*` | Argo CD 3.6.2 (k3s 1.35) |
| Linkerd (mandataire) | `linkerd` | `http://127.0.0.1:4191/metrics` | `linkerd.*` | Linkerd edge-26.11.2 (k3s 1.35) |
| KEDA | `keda` | `http://127.0.0.1:8080/metrics` | `keda.*` | KEDA 2.21.1 (k3s 1.35) |

« Éprouvé » veut dire : le logiciel a tourné en conteneur, l'agent l'a lu et a produit les noms décrits ici. Quelques familles documentées par l'éditeur mais absentes pendant l'essai (elles n'apparaissent qu'après l'événement) sont retenues et signalées plus bas.

## Déclarer un logiciel

Une entrée par logiciel dans `integrations.openmetrics` :

```yaml
integrations:
  openmetrics:
    - profile: consul                       # adresse par défaut du tableau ci-dessus
    - profile: traefik
      url: http://127.0.0.1:8082            # sans chemin : celui du profil est ajouté
    - profile: vault
      url: https://vault.lan:8200           # ?format=prometheus est ajouté aussi
      ca_file: /etc/hypha/vault-ca.pem
      bearer_token_file: /etc/hypha/vault-metrics.token
    - profile: kafka
      name: kafka-1                         # deux courtiers sur la même machine : un name chacun
    - profile: kafka
      name: kafka-2
      url: http://127.0.0.1:9415
```

- `instance_name` vaut le nom du profil, ou `name` ; chaque série porte aussi `endpoint`.
- Le contrôle `openmetrics.can_connect` passe en critique quand la cible ne répond plus ; le modèle **Logiciel injoignable (profil OpenMetrics)** le surveille pour un `instance_name` donné.
- Les compteurs deviennent des débits par seconde, les histogrammes et résumés donnent `.count`, `.sum`, `.avg` et leurs centiles (`.p50`, `.p95`, `.p99`…).
- `metrics`, `rename`, `labels` et `tags` de l'entrée complètent le profil ; `max_series` (2 000) borne le nombre de séries d'une cible.

## Infrastructure

### HashiCorp Consul

Dans la conf de l'agent Consul : `telemetry { prometheus_retention_time = "60s" disable_hostname = true }` (sans durée de rétention, la réponse est vide). Avec les ACL, un jeton `agent:read` (`bearer_token_file`).

- `consul.server.is_leader`, `consul.autopilot.healthy`, `consul.autopilot.failure_tolerance`, `consul.members.servers`, `consul.members.clients`.
- Catalogue : `consul.catalog.nodes`, `.catalog.services`, `.catalog.service_instances`, `consul.kv.entries`.
- Raft : `consul.raft.apply` (par s), `consul.raft.commit_time` et `consul.raft.leader.last_contact` (résumés en ms : `.p50`, `.p90`, `.p99`), `consul.raft.state.candidate` et `.state.leader` (élections).
- `consul.rpc.request`, `consul.rpc.request_error`, `consul.http.request` (ms, étiquettes `method` et `path`), `consul.kvs.apply`, `consul.runtime.alloc`, `consul.runtime.goroutines`, `consul.agent.tls_cert_expiry`.

L'état des contrôles de santé de Consul n'est pas publié au format Prometheus.

### HashiCorp Vault

Dans la conf de Vault : `telemetry { prometheus_retention_time = "60s" disable_hostname = true }`. Lecture sans jeton si le listener porte `telemetry { unauthenticated_metrics_access = true }`, sinon un jeton dont la politique lit `sys/metrics` (`bearer_token_file`). Le profil vise HTTPS, comme un Vault de production.

- `vault.core.unsealed` (0 = scellé), `vault.core.active`, `vault.core.performance_standby`, `vault.core.in_flight_requests`.
- Durées en ms (`.count`, `.p50`, `.p90`, `.p99`) : `vault.core.handle_request`, `.core.handle_login_request`, `.core.check_token`, `vault.barrier.get`, `vault.barrier.put`.
- `vault.expire.num_leases`, `vault.expire.num_irrevocable_leases`, `vault.token.count_by_ttl`, `vault.identity.entity_count`, `vault.runtime.alloc`, `vault.runtime.goroutines`.
- Retenues sans avoir été vues pendant l'essai (mode dev) : `vault.audit.log_request_failure`, `vault.audit.log_response_failure` (avec un périphérique d'audit), `vault.raft.leader.last_contact` (stockage intégré).

### etcd

Port client 2379, ou l'adresse de `--listen-metrics-urls` (`http://127.0.0.1:2381` chez kubeadm).

- `etcd.server.has_leader`, `etcd.server.is_leader`, `etcd.server.leader_changes`, `etcd.server.proposals.failed`, `.proposals.pending`, `.proposals.committed`, `.proposals.applied`, `etcd.server.slow_apply`, `etcd.server.heartbeat_failures`.
- Base : `etcd.db.size`, `etcd.db.size_in_use`, `etcd.server.quota_backend`, `etcd.mvcc.keys`, `etcd.mvcc.put`, `.range`, `.txn`, `.delete`.
- Disque (histogrammes, s) : `etcd.disk.wal_fsync.p99`, `etcd.disk.backend_commit.p99`. Réseau : `etcd.network.peer_round_trip`, `etcd.network.peer_sent_failures` (étiquette `to`, grappe à plusieurs membres), `etcd.grpc.handled` par `grpc_code`.

## Réseau

### CoreDNS

Greffon `prometheus :9153` dans le Corefile.

- `coredns.requests` (étiquettes `server`, `zone`, `type`), `coredns.responses` (`rcode`), `coredns.request_duration` (s, `.p95`).
- Cache : `coredns.cache.entries`, `.cache.hits`, `.cache.misses`. Relais : `coredns.proxy.request_duration` (CoreDNS ≥ 1.11) ou `coredns.forward.*` (versions antérieures).
- `coredns.panics`, `coredns.reload.failed`, `coredns.process.resident_memory`.

### Traefik Proxy

`--metrics.prometheus=true` ; le point d'entrée `traefik` (8080) sert les mesures, ou celui de `--metrics.prometheus.entryPoint`. Étiquettes des routeurs et des services avec `addRoutersLabels` et `addServicesLabels`.

- `traefik.entrypoint.requests` (`code`, `entrypoint`), `traefik.entrypoint.request_duration` (s), `.requests_bytes`, `.responses_bytes`, `traefik.open_connections`.
- `traefik.service.requests`, `traefik.service.request_duration`, `traefik.service.server_up` (`service`, `url` ; 0 = contrôle de santé en échec), `traefik.router.requests`.
- `traefik.config.reloads`, `traefik.config.last_reload_success`, `traefik.tls.certs_not_after` (horodatage, étiquette `cn`).

### Envoy Proxy

Interface d'administration (`admin`, 9901), chemin `/stats/prometheus`. Durées en millisecondes.

- `envoy.server.live`, `envoy.server.state`, `envoy.server.uptime`, `envoy.server.memory.allocated`, `envoy.server.total_connections`, `envoy.server.days_until_first_cert_expiring`.
- Amont (`envoy_cluster_name`) : `envoy.cluster.upstream.rq_total`, `.rq_xx` (`envoy_response_code_class` : 2, 4, 5…), `.rq_time.p99`, `.cx_active`, `.cx_connect_fail`, `.rq_timeout`, `.rq_retry`, `envoy.cluster.membership.healthy` et `.total`.
- Aval (`envoy_http_conn_manager_prefix`) : `envoy.http.downstream.rq_total`, `.rq_xx`, `.rq_time`, `.cx_active` ; `envoy.listener.downstream.cx_active`.

### Kong Gateway

Greffon `prometheus` activé (global ou par service ; `status_code_metrics`, `latency_metrics`, `bandwidth_metrics`, `upstream_health_metrics` à `true`), lu sur le status listener (`status_listen = 0.0.0.0:8100`) ou l'API d'administration. Latences en millisecondes.

- `kong.http.requests` (`service`, `route`, `code`), `kong.request_latency`, `kong.upstream_latency`, `kong.kong_latency` (`.p95`), `kong.bandwidth` (`direction`).
- `kong.datastore.reachable`, `kong.upstream.target_health` (`upstream`, `target`, `state`), `kong.nginx.connections` (`state`), `kong.memory.shared_dict.used` et `.total`.

## Bases de données

### CockroachDB

`/_status/vars` sur le port HTTP (8080), en HTTPS hors `--insecure` (`ca_file` vers la CA de la grappe), sans authentification. L'exposition est grosse (≈ 1,8 Mo) : `interval: 30s` suffit. Latences en nanosecondes.

- `cockroachdb.liveness.live_nodes`, `cockroachdb.ranges.unavailable`, `.ranges.underreplicated`, `.ranges.overreplicated`, `cockroachdb.replicas.*`, `cockroachdb.capacity.total`, `.available`, `.used`.
- SQL : `cockroachdb.sql.conns`, `.select`, `.insert`, `.update`, `.delete`, `.failure`, `.txn.commit`, `.txn.abort`, `cockroachdb.sql.service_latency.p99`, `.exec_latency`, `.txn_latency`.
- `cockroachdb.sys.cpu.combined_normalized`, `cockroachdb.sys.rss`, `cockroachdb.clock_offset.mean_nanos`, `cockroachdb.requests.slow.*`, `cockroachdb.storage.l0_sublevels`, `cockroachdb.admission.io_overload`. Étiquettes `node_id` et `store`.

### ScyllaDB

`/metrics` sur le port 9180. Les séries sont additionnées sur tous les cœurs (étiquette `shard` écartée) ; latences du coordinateur en microsecondes.

- `scylla.reactor.utilization` (%), `scylla.node.operation_mode` (3 = normal), `scylla.gossip.live`.
- `scylla.coordinator.read_latency` et `.write_latency` (`.p99`), `scylla.cql.requests_served`, `.connections`, `.reads`, `.inserts`, `.errors` (`type`).
- `scylla.database.reads`, `.writes`, `.reads_failed`, `.writes_failed`, `.writes_timedout`, `scylla.cache.row_hits`, `.row_misses`, `scylla.compaction.running`, `.pending`, `scylla.memory.allocated`, `.free`.

## Files de messages

### Apache Pulsar

`/metrics/` du courtier (port HTTP 8080). Agrégats du courtier et par espace de noms Pulsar : l'étiquette `topic` est écartée, un sujet de plus ne fait pas une série de plus.

- `pulsar.broker.topics`, `.subscriptions`, `.producers`, `.consumers`, `.rate_in`, `.rate_out` (messages/s), `.throughput_in`, `.throughput_out` (octets/s), `.msg_backlog`, `.storage_size`, `.publish_latency` (ms, `.p99`).
- Par espace de noms (`namespace`) : `pulsar.namespace.topics`, `.msg_backlog`, `.rate_in`, `.rate_out`, `.storage_size`. JVM : `pulsar.jvm.memory.used` (`area`), `pulsar.jvm.gc.collection`.

### Celery (par Flower)

Flower publie les mesures des ouvriers sur `/metrics` (5555). Les ouvriers doivent émettre leurs événements (`celery worker -E`, ou `worker_send_task_events = True`).

- `celery.worker.online` (`worker`), `celery.worker.executing_tasks`, `celery.worker.prefetched_tasks`.
- `celery.events` par `type` (`task-received`, `task-started`, `task-succeeded`, `task-failed`…) et `task`, `celery.task.runtime` (s, `.p95`), `celery.task.prefetch_time`.

## Applications

### n8n

`N8N_METRICS=true`. Exécutions des flux avec `N8N_METRICS_INCLUDE_MESSAGE_EVENT_BUS_METRICS=true`, flux par flux avec `N8N_METRICS_INCLUDE_WORKFLOW_ID_LABEL=true`.

- `n8n.workflows.active`, `n8n.workflow.started`, `.success`, `.failed` (par s), `n8n.workflow.execution_duration` (`status`, `.p95`).
- Mode file d'attente (`EXECUTIONS_MODE=queue`) : `n8n.queue.jobs.waiting`, `.active`, `.completed`, `.failed` (retenues, non éprouvées : l'essai tournait en mode normal).
- `n8n.nodejs.eventloop_lag_p99`, `n8n.nodejs.heap_used`, `n8n.process.resident_memory`, `n8n.process.cpu_seconds`.

### Temporal

`global.metrics.prometheus.listenAddress` du serveur (9090 dans la charte Helm, 8000 dans les exemples Docker : `url` à adapter). Durées en secondes.

- `temporal.service.requests` et `.errors` (`service_name`, `namespace`, `error_type`), `temporal.service.latency` (`.p95`), `temporal.service.pending_requests`.
- `temporal.persistence.requests`, `.errors`, `.latency`.
- Files de tâches (`namespace`, `taskqueue`, `task_type`) : `temporal.task_queue.backlog`, `.backlog_age` (s), `.poll_timeouts`.
- Issue des flux (`workflowtype`) : `temporal.workflow.success`, `.failed`, `.timeout`, `.terminate`, `.cancel`, `.continued_as_new`, `.schedule_to_close_latency`. Seul `terminate` a été vu pendant l'essai, les autres apparaissent avec leur événement.

### GitLab

Trois profils pour une installation Omnibus, lus par l'agent de la machine GitLab :

- `gitlab` : Rails et Puma, `/-/metrics` sur Puma (`127.0.0.1:8080`) ou derrière nginx ; l'adresse de l'agent doit figurer dans `gitlab_rails['monitoring_whitelist']` (127.0.0.0/8 par défaut).
- `gitlab_sidekiq` : `sidekiq['metrics_enabled'] = true` (`127.0.0.1:8082`).
- `gitaly` : `gitaly['configuration'][:prometheus_listen_addr]` (`127.0.0.1:9236`).

Mesures :

- `gitlab.http.requests` (`method`, `status` : 2xx… 5xx), `gitlab.http.request_duration`, `gitlab.transaction.duration`, `gitlab.sql.duration`, `gitlab.rails.queue_duration` (s).
- `gitlab.db.pool.busy`, `.waiting`, `.size`, `.dead` (`db_config_name`), `gitlab.redis.requests`, `.exceptions` (`storage`), `gitlab.sli.rails.requests`, `.errors`, `.apdex_total`, `.apdex_success`.
- `gitlab.puma.running`, `.queued_connections`, `.workers`, `gitlab.ruby.resident_memory`.
- `gitlab_sidekiq.jobs.completion` (`job_status` : `done`, `fail`), `gitlab_sidekiq.jobs.queue_duration`, `.jobs.running`, `.concurrency`.
- `gitaly.grpc.handled` (`grpc_code`), `gitaly.pack_objects.in_progress`, `.queued`, `gitaly.concurrency_limiting.queued`, `gitaly.commands.running`.

L'essai a tourné en réglages réduits (Puma en mode simple, Sidekiq à 5 fils, Prometheus intégré coupé), le profil vaut aussi pour la conf par défaut.

### SonarQube

`/api/monitoring/metrics` (SonarQube 9.3 et suivants), lu avec l'en-tête du code d'accès système (`sonar.web.systemPasscode`) ou le jeton d'un administrateur :

```yaml
    - profile: sonarqube
      headers:
        X-Sonar-Passcode: "le code d'accès"   # fichier de conf en 600
```

- `sonarqube.health.web`, `.health.compute_engine`, `.health.elasticsearch` (1 = en marche).
- `sonarqube.compute_engine.pending_tasks`, `.compute_engine.task_duration`, `sonarqube.elasticsearch.disk_usage` (%), `.disk_free`, `.read_only_indices`, `sonarqube.db.latency` (s), `sonarqube.web.api_v1.request_duration`, `.api_v2.request_duration`, `sonarqube.web.uptime_minutes`.

## Java

### L'exportateur JMX Prometheus

Tomcat, Kafka, ActiveMQ et Cassandra ne publient pas de mesures Prometheus : leurs MBeans JMX sont lus par l'**exportateur JMX Prometheus** officiel, chargé comme javaagent dans la JVM. Téléchargez `jmx_prometheus_javaagent-1.7.0.jar` depuis les publications du projet `prometheus/jmx_exporter` sur GitHub (la somme SHA-256 est publiée à côté ; Maven Central s'arrête à la 1.0.1), posez à côté le fichier de règles du logiciel (ci-dessous), puis ajoutez à la JVM :

```text
-javaagent:/opt/jmx/jmx_prometheus_javaagent-1.7.0.jar=127.0.0.1:9404:/opt/jmx/tomcat.yml
```

`127.0.0.1:` devant le port garde l'exportateur sur la boucle locale. Chaque profil reprend aussi les mesures de la JVM publiées par l'exportateur : `<espace>.jvm.memory.used`, `.memory.committed`, `.memory.max` (`area` : `heap`, `nonheap`), `<espace>.jvm.gc.collection` (`.count`, `.sum`), `.jvm.threads.current`, `.jvm.threads.deadlocked`, `.jvm.classes.loaded`, `<espace>.process.cpu_seconds`, `.process.open_fds`, `<espace>.jmx.scrape_error` (1 = MBeans illisibles), `.jmx.scrape_duration`.

Les règles restreignent les MBeans lus (`includeObjectNames`) : un relevé complet d'un Kafka ou d'un Cassandra coûte cher à la JVM et produit des milliers de séries.

### Apache Tomcat

Dans `bin/setenv.sh` : `CATALINA_OPTS="$CATALINA_OPTS -javaagent:/opt/jmx/jmx_prometheus_javaagent-1.7.0.jar=127.0.0.1:9404:/opt/jmx/tomcat.yml"`.

```yaml
# Règles HYPHA pour Apache Tomcat (exportateur JMX Prometheus, javaagent 1.x).
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

- Par connecteur (`connector`) : `tomcat.requests`, `tomcat.errors`, `tomcat.bytes_received`, `.bytes_sent`, `tomcat.processing_time` (ms par s), `tomcat.max_time`, `tomcat.threads.current`, `.busy`, `.max`, `tomcat.connections.current`, `.max`.
- Par application (`context`) : `tomcat.sessions.active`, `.max_active`, `.created`, `.expired`, `.rejected`.

### Apache Kafka

`KAFKA_OPTS="-javaagent:/opt/jmx/jmx_prometheus_javaagent-1.7.0.jar=127.0.0.1:9405:/opt/jmx/kafka.yml"` avant le lancement du courtier. Les outils de Kafka (`kafka-*.sh`) lisent aussi `KAFKA_OPTS` : lancez-les avec `KAFKA_OPTS=` vide, sinon ils prennent le port de l'exportateur.

```yaml
# Règles HYPHA pour Apache Kafka 3.x/4.x (KRaft ou ZooKeeper), exportateur JMX Prometheus 1.x.
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

- Courtier (tous sujets) : `kafka.broker.messages_in`, `.bytes_in`, `.bytes_out`, `.bytes_rejected`, `.produce.requests`, `.produce.failed`, `.fetch.requests`, `.fetch.failed`.
- Réplication : `kafka.replication.under_replicated_partitions`, `.under_min_isr_partitions`, `.offline_replicas`, `.partitions`, `.leaders`, `.isr_shrinks`, `.isr_expands`.
- Contrôleur : `kafka.controller.active` (1 sur le contrôleur actif), `.offline_partitions`, `.topics`, `.partitions`, `.active_brokers`, `.fenced_brokers`, `.unclean_leader_elections`.
- Requêtes (`request` : Produce, FetchConsumer, FetchFollower, Metadata, OffsetCommit) : `kafka.request.count`, `kafka.request.total_time.mean` et `.p99` (ms). Inactivité : `kafka.network.processor_avg_idle`, `kafka.request_handler.avg_idle` (fraction, 1 = inactif). `kafka.log.flushes`, `kafka.log.flush_time.p99`.

Le retard des consommateurs se lit chez les consommateurs, pas dans le courtier.

### Apache ActiveMQ Classic

`ACTIVEMQ_OPTS` (dans `bin/setenv` ou l'environnement) : la valeur remplace celle du script, gardez donc la mémoire et la conf de connexion, par exemple `-Xms64M -Xmx1G -Djava.util.logging.config.file=logging.properties -Djava.security.auth.login.config=$ACTIVEMQ_CONF/login.config -javaagent:/opt/jmx/jmx_prometheus_javaagent-1.7.0.jar=127.0.0.1:9406:/opt/jmx/activemq.yml`. Les commandes `activemq producer`, `consumer`… lisent aussi `ACTIVEMQ_OPTS` : donnez-leur une valeur sans javaagent.

```yaml
# Règles HYPHA pour Apache ActiveMQ Classic 5.x/6.x (exportateur JMX Prometheus 1.x). Les
# destinations d'avis internes (ActiveMQ.Advisory.*) sont écartées.
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

- Courtier (`broker`) : `activemq.broker.memory_usage`, `.store_usage`, `.temp_usage` (% des limites ; à 100 %, les producteurs sont bloqués), `.connections`, `.consumers`, `.producers`, `.messages`, `.enqueued`, `.dequeued`.
- Files et sujets (`destination`, `destination_type`), sans les destinations d'avis `ActiveMQ.Advisory.*` : `activemq.destination.size`, `.consumers`, `.producers`, `.in_flight`, `.memory_usage`, `.average_enqueue_time`, `.enqueued`, `.dequeued`, `.dispatched`, `.expired`.

### Apache Cassandra

`JVM_EXTRA_OPTS="-javaagent:/opt/jmx/jmx_prometheus_javaagent-1.7.0.jar=127.0.0.1:9407:/opt/jmx/cassandra.yml"` (lu par `cassandra-env.sh`). Latences des requêtes clientes en microsecondes.

```yaml
# Règles HYPHA pour Apache Cassandra 4.x/5.x (exportateur JMX Prometheus 1.x). Latences des
# requêtes clientes en microsecondes (minuteries de Cassandra).
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

- Requêtes clientes (`scope` : Read, Write, CASRead, CASWrite, RangeSlice) : `cassandra.client_request.count`, `.latency.mean`, `.latency.p99`, `.timeouts`, `.unavailables`, `.failures`.
- `cassandra.storage.load`, `.exceptions`, `.hints`, `cassandra.compaction.pending_tasks`, `.completed_tasks`, `.bytes_compacted`, `cassandra.thread_pools.pending_tasks`, `.active_tasks`, `.blocked_tasks` (`pool`), `cassandra.dropped_messages`, `cassandra.client.connected_native_clients`, `cassandra.cache.hit_rate` (`cache`), `cassandra.commitlog.pending_tasks`.

### Apache Solr (mesures natives)

Solr 9 et 10 publient leurs mesures au format Prometheus sur `/solr/admin/metrics?wt=prometheus`, sans exportateur (Solr 10 n'a plus de MBeans JMX à lire). Solr 10 donne des noms OpenTelemetry, Solr 9 des noms `solr_metrics_*` : les deux jeux sont retenus.

- Solr 10 (`core`, `category`) : `solr.core.requests`, `solr.core.request_errors`, `solr.core.request_time` (ms, `.p95`), `solr.core.num_docs`, `solr.core.index_size_mb`, `solr.core.segments`, `solr.core.cache.lookups` (`result` : hit, miss), `solr.core.update.docs_pending`, `solr.cores`, `solr.disk_space_mb`, `solr.jvm.memory.used` (`jvm_memory_type`), `solr.jvm.gc.duration`, `solr.jvm.threads`.
- Solr 9 : `solr.metrics.core.requests` (`type` : requests, errors, timeouts…), `solr.metrics.core.request_time` (ms, résumé), `solr.metrics.core.index_size`, `solr.metrics.core.searcher_documents`, `solr.metrics.jetty.responses` (`status`), `solr.metrics.node.cores`.

### WildFly (mesures natives)

WildFly publie ses mesures sur `/metrics` de l'interface d'administration (9990, sous-système `metrics`), sans exportateur. Statistiques d'Undertow, des sources de données et des transactions avec `-Dwildfly.statistics-enabled=true` (ou `statistics-enabled` par sous-système). L'exportateur JMX en javaagent ne démarre pas sur WildFly 42 (conflit avec le LogManager de JBoss) : le point natif est la voie à suivre.

- Undertow (`http_listener`, `https_listener`) : `wildfly.undertow.requests`, `.errors`, `.max_processing_time` (s).
- Sources de données (`data_source`) : `wildfly.datasource.pool.active`, `.available`, `.in_use`, `.idle`, `.max_used`, `.wait_count`, `.timed_out`, `.average_get_time` (ms).
- Transactions : `wildfly.transactions.started`, `.committed`, `.aborted`, `.timed_out`, `.application_rollbacks`, `.resource_rollbacks`, `.inflight`.
- `wildfly.jvm.memory.heap_used`, `.heap_max`, `.nonheap_used`, `wildfly.jvm.threads`, `wildfly.jvm.gc.collections`, `wildfly.process.cpu_load`, `wildfly.ee.hung_threads`.

## Kubernetes

Ces logiciels tournent dans la grappe : déclarez-les dans l'agent qui lit déjà la grappe (voir [Kubernetes](/docs/kubernetes/)), avec l'adresse de leur service, par exemple :

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

Ils ont été éprouvés dans une grappe k3s 1.35 jetable (k3d), lue par redirection de ports.

### cert-manager

Charte Helm avec `prometheus.enabled=true` (service sur 9402).

- `cert_manager.certificate.ready` (`condition` : True, False, Unknown ; `namespace`, `name`, `issuer_name`), `cert_manager.clusterissuer.ready`, `cert_manager.issuer.ready`.
- `cert_manager.certificate.expiration_timestamp` et `.renewal_timestamp` : horodatages Unix (secondes), `cert_manager.clock_time` (l'heure de cert-manager), `cert_manager.controller.sync_calls` (`controller`).

L'échéance est publiée en horodatage : HYPHA la montre telle quelle, le modèle **Certificat cert-manager non prêt** alerte sur l'échec d'émission ou de renouvellement.

### Velero

Serveur Velero, `/metrics` sur 8085 (`metrics.enabled` de la charte).

- `velero.backup.attempts`, `.success`, `.failure`, `.partial_failure`, `.validation_failure` (par s), `velero.backup.last_status` (`schedule` ; 1 = réussie, 0 = en échec), `velero.backups`, `velero.backup.items_errors`.
- `velero.backup_location.available` (`backup_location_name` ; 0 = stockage injoignable), `velero.restore.success`, `.failed`, `.partial_failure`.
- `velero.backup.last_successful_timestamp` (horodatage de la dernière sauvegarde réussie) : retenue, non vue pendant l'essai, dont la sauvegarde a échoué faute de stockage.

### Argo CD

- `argocd` : contrôleur d'applications, 8082 (service `argocd-metrics`). `argocd.app.info` vaut 1 par application, ses étiquettes portent l'état : `name`, `health_status` (Healthy, Progressing, Degraded, Missing…), `sync_status` (Synced, OutOfSync, Unknown). `argocd.app.syncs` (`phase`), `argocd.app.reconcile` (s), `argocd.app.orphaned_resources`, `argocd.cluster.connection_status`, `.api_resource_objects`, `.cache_age`.
- `argocd_repo_server` : serveur de dépôts, 8084. `argocd_repo_server.git.requests` (`request_type` : fetch, ls-remote), `.git.request_duration`, `.pending_requests`.

### Linkerd

Chaque mandataire d'un pod maillé publie ses mesures sur `:4191/metrics`. Pour toute la maille, visez plutôt le Prometheus de `linkerd-viz` (`/federate?match[]={job="linkerd-proxy"}`), qui rend les mêmes noms avec les étiquettes `namespace` et `deployment` (non éprouvé : l'essai lisait un mandataire).

- `linkerd.requests` (`direction` : inbound, outbound), `linkerd.responses` (`classification` : success, failure ; `status_code`), `linkerd.response_latency` (ms, `.p95`).
- `linkerd.tcp.open_connections`, `.opens`, `.closes`, `linkerd.inbound.authz_allowed` et `.authz_denied`, `linkerd.identity.cert_expiration_timestamp`, `linkerd.identity.cert_refreshes` (`result`).

### KEDA

Opérateur KEDA, `/metrics` sur 8080 (`prometheus.operator.enabled=true` de la charte).

- `keda.scaler.errors` (`namespace`, `scaledobject`, `scaler`), `keda.scaled_object.errors`, `keda.scaler.active`, `keda.scaler.metrics_value`, `keda.scaler.metrics_latency` (s), `keda.scale_loop_latency` (s), `keda.scaled_object.paused`, `keda.resources.registered`, `keda.triggers.registered`.

## Modèles de moniteurs

Dans **Moniteurs › Nouveau › Depuis un modèle** :

- **Collecte générique** : logiciel injoignable (par `instance_name`), relevé JMX en échec.
- **Infrastructure** : grappe Consul en mauvaise santé, Vault scellé, etcd sans meneur, disque d'etcd trop lent.
- **Réseau** : réponses SERVFAIL de CoreDNS, serveur amont Traefik en panne, erreurs 5xx des amonts d'Envoy, Kong ne joint plus sa base, cible amont Kong en mauvaise santé.
- **Bases de données** : plages CockroachDB indisponibles ou sous-répliquées, délais dépassés et répliques insuffisantes dans Cassandra, compactions Cassandra en retard, ScyllaDB saturé, écritures ScyllaDB en échec.
- **Files de messages** : partitions Kafka sous-répliquées ou hors ligne, mémoire ou stockage du courtier ActiveMQ saturés, file ActiveMQ qui s'accumule, arriéré Pulsar, ouvrier Celery hors ligne, tâches Celery en échec.
- **Applications** : file de tâches Temporal qui vieillit, erreurs de persistance Temporal, flux n8n en échec, erreurs 5xx de GitLab, requêtes en attente dans Puma, tâches Sidekiq en échec, composant SonarQube arrêté, disque d'Elasticsearch de SonarQube.
- **Web** : erreurs Tomcat, erreurs et transactions expirées de WildFly ; **Recherche** : erreurs des requêtes Solr.
- **Kubernetes** : certificat cert-manager non prêt, dernière sauvegarde Velero en échec, stockage Velero injoignable, application Argo CD dégradée ou désynchronisée, réponses en échec dans la maille Linkerd, déclencheurs KEDA en erreur.

## Tableaux prêts

**Tableaux de bord › Catalogue** : un tableau par logiciel (Consul, Vault, etcd, CoreDNS, Traefik, Envoy, Kong, CockroachDB, n8n, Temporal, Celery, Tomcat, Kafka, ActiveMQ, Cassandra, Solr, WildFly, Pulsar, ScyllaDB, SonarQube, GitLab, cert-manager, Velero, Argo CD, Linkerd, KEDA), installé d'un clic puis modifiable comme un tableau ordinaire.
