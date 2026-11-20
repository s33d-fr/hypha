package omprofiles

// Profils de la vague 2 des intégrations (0.6.0) : logiciels qui publient leurs mesures au format
// Prometheus, et logiciels Java lus par l'exportateur JMX Prometheus (javaagent officiel, règles
// livrées dans la documentation). Les noms Prometheus retenus ont été relevés sur les vrais
// logiciels, en conteneur (versions dans la documentation) ; les profils des logiciels propres à
// Kubernetes non éprouvés le disent dans leur description.
//
// Unités : les résumés de Consul et Vault (go-metrics) sont en millisecondes ; les latences de
// CockroachDB en nanosecondes ; celles d'Envoy et de Kong en millisecondes ; le reste en secondes
// et en octets, comme l'indique le suffixe du nom Prometheus.

func init() {
	// HashiCorp Consul 1.x/2.x : /v1/agent/metrics?format=prometheus, avec
	// telemetry { prometheus_retention_time = "60s" } dans la conf de l'agent Consul (sinon la
	// réponse est vide). Avec les ACL : jeton agent:read (bearer_token_file).
	register(Profile{
		Name: "consul", Description: "HashiCorp Consul (agent ou serveur)", Port: 8500,
		Path: "/v1/agent/metrics?format=prometheus", Namespace: "consul",
		Metrics: []string{
			"consul_server_isLeader", "consul_autopilot_healthy", "consul_autopilot_failure_tolerance",
			"consul_members_servers", "consul_members_clients", "consul_state_nodes", "consul_state_services",
			"consul_state_service_instances", "consul_state_kv_entries", "consul_raft_apply", "consul_raft_commitTime",
			"consul_raft_leader_lastContact", "consul_raft_state_candidate", "consul_raft_state_leader",
			"consul_rpc_request", "consul_rpc_request_error", "consul_rpc_queries_blocking", "consul_api_http",
			"consul_kvs_apply", "consul_catalog_register", "consul_runtime_alloc_bytes", "consul_runtime_num_goroutines",
			"consul_agent_tls_cert_expiry", "consul_mesh_active_root_ca_expiry",
		},
		Rename: map[string]string{
			"consul_server_isLeader":             "server.is_leader",
			"consul_autopilot_healthy":           "autopilot.healthy",
			"consul_autopilot_failure_tolerance": "autopilot.failure_tolerance",
			"consul_members_servers":             "members.servers",
			"consul_members_clients":             "members.clients",
			"consul_state_nodes":                 "catalog.nodes",
			"consul_state_services":              "catalog.services",
			"consul_state_service_instances":     "catalog.service_instances",
			"consul_state_kv_entries":            "kv.entries",
			"consul_raft_apply":                  "raft.apply",
			"consul_raft_commitTime":             "raft.commit_time",
			"consul_raft_leader_lastContact":     "raft.leader.last_contact",
			"consul_raft_state_candidate":        "raft.state.candidate",
			"consul_raft_state_leader":           "raft.state.leader",
			"consul_rpc_request":                 "rpc.request",
			"consul_rpc_request_error":           "rpc.request_error",
			"consul_rpc_queries_blocking":        "rpc.queries_blocking",
			"consul_api_http":                    "http.request",
			"consul_kvs_apply":                   "kvs.apply",
			"consul_catalog_register":            "catalog.register",
			"consul_runtime_alloc_bytes":         "runtime.alloc",
			"consul_runtime_num_goroutines":      "runtime.goroutines",
			"consul_agent_tls_cert_expiry":       "agent.tls_cert_expiry",
			"consul_mesh_active_root_ca_expiry":  "mesh.root_ca_expiry",
		},
		Labels: []string{"method", "path"},
	})

	// HashiCorp Vault 1.x/2.x : /v1/sys/metrics?format=prometheus, avec telemetry
	// { prometheus_retention_time = "60s", disable_hostname = true } ; lecture sans jeton si le
	// listener porte telemetry { unauthenticated_metrics_access = true }, sinon jeton dont la
	// politique lit sys/metrics (bearer_token_file). TLS par défaut, comme un Vault de production.
	register(Profile{
		Name: "vault", Description: "HashiCorp Vault", Scheme: "https", Port: 8200,
		Path: "/v1/sys/metrics?format=prometheus", Namespace: "vault",
		Metrics: []string{
			"vault_core_unsealed", "vault_core_active", "vault_core_performance_standby", "vault_core_in_flight_requests",
			"vault_core_handle_request", "vault_core_handle_login_request", "vault_core_check_token",
			"vault_core_leadership_lost", "vault_core_leadership_setup_failed", "vault_expire_num_leases",
			"vault_expire_num_irrevocable_leases", "vault_token_count", "vault_token_count_by_ttl",
			"vault_identity_entity_count", "vault_barrier_get", "vault_barrier_put", "vault_audit_log_request_failure",
			"vault_audit_log_response_failure", "vault_raft_leader_lastContact", "vault_raft_apply", "vault_cache_hit",
			"vault_cache_miss", "vault_runtime_alloc_bytes", "vault_runtime_sys_bytes", "vault_runtime_num_goroutines",
		},
		Rename: map[string]string{
			"vault_core_unsealed":                 "core.unsealed",
			"vault_core_active":                   "core.active",
			"vault_core_performance_standby":      "core.performance_standby",
			"vault_core_in_flight_requests":       "core.in_flight_requests",
			"vault_core_handle_request":           "core.handle_request",
			"vault_core_handle_login_request":     "core.handle_login_request",
			"vault_core_check_token":              "core.check_token",
			"vault_core_leadership_lost":          "core.leadership_lost",
			"vault_core_leadership_setup_failed":  "core.leadership_setup_failed",
			"vault_expire_num_leases":             "expire.num_leases",
			"vault_expire_num_irrevocable_leases": "expire.num_irrevocable_leases",
			"vault_token_count":                   "token.count",
			"vault_token_count_by_ttl":            "token.count_by_ttl",
			"vault_identity_entity_count":         "identity.entity_count",
			"vault_barrier_get":                   "barrier.get",
			"vault_barrier_put":                   "barrier.put",
			"vault_audit_log_request_failure":     "audit.log_request_failure",
			"vault_audit_log_response_failure":    "audit.log_response_failure",
			"vault_raft_leader_lastContact":       "raft.leader.last_contact",
			"vault_raft_apply":                    "raft.apply",
			"vault_cache_hit":                     "cache.hit",
			"vault_cache_miss":                    "cache.miss",
			"vault_runtime_alloc_bytes":           "runtime.alloc",
			"vault_runtime_sys_bytes":             "runtime.sys",
			"vault_runtime_num_goroutines":        "runtime.goroutines",
		},
	})

	// etcd 3.5/3.6 : port client 2379 (ou --listen-metrics-urls, 2381 chez kubeadm).
	register(Profile{
		Name: "etcd", Description: "etcd (magasin clé-valeur, plan de contrôle Kubernetes)", Port: 2379,
		Namespace: "etcd",
		Metrics: []string{
			"etcd_server_has_leader", "etcd_server_is_leader", "etcd_server_leader_changes_seen_total",
			"etcd_server_proposals_failed_total", "etcd_server_proposals_pending", "etcd_server_proposals_committed_total",
			"etcd_server_proposals_applied_total", "etcd_server_heartbeat_send_failures_total", "etcd_server_slow_apply_total",
			"etcd_server_slow_read_indexes_total", "etcd_server_quota_backend_bytes", "etcd_mvcc_db_total_size_in_bytes",
			"etcd_mvcc_db_total_size_in_use_in_bytes", "etcd_debugging_mvcc_keys_total", "etcd_mvcc_put_total",
			"etcd_mvcc_range_total", "etcd_mvcc_txn_total", "etcd_mvcc_delete_total", "etcd_disk_wal_fsync_duration_seconds",
			"etcd_disk_backend_commit_duration_seconds", "etcd_network_peer_round_trip_time_seconds",
			"etcd_network_peer_sent_failures_total", "etcd_network_client_grpc_received_bytes_total",
			"etcd_network_client_grpc_sent_bytes_total", "grpc_server_handled_total", "os_fd_used", "os_fd_limit",
			"process_resident_memory_bytes",
		},
		Rename: map[string]string{
			"etcd_server_has_leader":                        "server.has_leader",
			"etcd_server_is_leader":                         "server.is_leader",
			"etcd_server_leader_changes_seen_total":         "server.leader_changes",
			"etcd_server_proposals_failed_total":            "server.proposals.failed",
			"etcd_server_proposals_pending":                 "server.proposals.pending",
			"etcd_server_proposals_committed_total":         "server.proposals.committed",
			"etcd_server_proposals_applied_total":           "server.proposals.applied",
			"etcd_server_heartbeat_send_failures_total":     "server.heartbeat_failures",
			"etcd_server_slow_apply_total":                  "server.slow_apply",
			"etcd_server_slow_read_indexes_total":           "server.slow_read_indexes",
			"etcd_server_quota_backend_bytes":               "server.quota_backend",
			"etcd_mvcc_db_total_size_in_bytes":              "db.size",
			"etcd_mvcc_db_total_size_in_use_in_bytes":       "db.size_in_use",
			"etcd_debugging_mvcc_keys_total":                "mvcc.keys",
			"etcd_mvcc_put_total":                           "mvcc.put",
			"etcd_mvcc_range_total":                         "mvcc.range",
			"etcd_mvcc_txn_total":                           "mvcc.txn",
			"etcd_mvcc_delete_total":                        "mvcc.delete",
			"etcd_disk_wal_fsync_duration_seconds":          "disk.wal_fsync",
			"etcd_disk_backend_commit_duration_seconds":     "disk.backend_commit",
			"etcd_network_peer_round_trip_time_seconds":     "network.peer_round_trip",
			"etcd_network_peer_sent_failures_total":         "network.peer_sent_failures",
			"etcd_network_client_grpc_received_bytes_total": "network.client.received",
			"etcd_network_client_grpc_sent_bytes_total":     "network.client.sent",
			"grpc_server_handled_total":                     "grpc.handled",
			"os_fd_used":                                    "fd.used",
			"os_fd_limit":                                   "fd.limit",
			"process_resident_memory_bytes":                 "process.resident_memory",
		},
		Labels: []string{"grpc_code", "To"},
	})

	// CoreDNS 1.x (greffon prometheus, :9153) : requêtes et réponses par code, durées, cache,
	// relais (coredns_proxy_* depuis 1.11, coredns_forward_* avant), paniques, rechargements.
	register(Profile{
		Name: "coredns", Description: "CoreDNS", Port: 9153, Namespace: "coredns",
		Metrics: []string{
			"coredns_dns_requests_total", "coredns_dns_responses_total", "coredns_dns_request_duration_seconds",
			"coredns_cache_entries", "coredns_cache_hits_total", "coredns_cache_misses_total", "coredns_cache_requests_total",
			"coredns_proxy_request_duration_seconds", "coredns_proxy_healthcheck_failures_total",
			"coredns_forward_requests_total", "coredns_forward_responses_total", "coredns_forward_healthcheck_failures_total",
			"coredns_forward_healthcheck_broken_total", "coredns_forward_max_concurrent_rejects_total",
			"coredns_panics_total", "coredns_reload_failed_total", "coredns_hosts_entries",
			"process_resident_memory_bytes", "process_cpu_seconds_total",
		},
		Rename: map[string]string{
			"coredns_dns_requests_total":                   "requests",
			"coredns_dns_responses_total":                  "responses",
			"coredns_dns_request_duration_seconds":         "request_duration",
			"coredns_cache_entries":                        "cache.entries",
			"coredns_cache_hits_total":                     "cache.hits",
			"coredns_cache_misses_total":                   "cache.misses",
			"coredns_cache_requests_total":                 "cache.requests",
			"coredns_proxy_request_duration_seconds":       "proxy.request_duration",
			"coredns_proxy_healthcheck_failures_total":     "proxy.healthcheck_failures",
			"coredns_forward_requests_total":               "forward.requests",
			"coredns_forward_responses_total":              "forward.responses",
			"coredns_forward_healthcheck_failures_total":   "forward.healthcheck_failures",
			"coredns_forward_healthcheck_broken_total":     "forward.healthcheck_broken",
			"coredns_forward_max_concurrent_rejects_total": "forward.max_concurrent_rejects",
			"coredns_panics_total":                         "panics",
			"coredns_reload_failed_total":                  "reload.failed",
			"coredns_hosts_entries":                        "hosts.entries",
			"process_resident_memory_bytes":                "process.resident_memory",
			"process_cpu_seconds_total":                    "process.cpu_seconds",
		},
		Labels: []string{"server", "zone", "type", "rcode", "to", "proxy_name"},
	})

	// Traefik 2.x/3.x : --metrics.prometheus=true (point d'entrée « traefik », :8080, par défaut ;
	// ou --metrics.prometheus.entryPoint). Étiquettes des routeurs et services avec
	// addRoutersLabels / addServicesLabels.
	register(Profile{
		Name: "traefik", Description: "Traefik Proxy", Port: 8080, Namespace: "traefik",
		Metrics: []string{
			"traefik_entrypoint_requests_total", "traefik_entrypoint_request_duration_seconds",
			"traefik_entrypoint_requests_bytes_total", "traefik_entrypoint_responses_bytes_total", "traefik_open_connections",
			"traefik_entrypoint_open_connections", "traefik_service_requests_total", "traefik_service_request_duration_seconds",
			"traefik_service_server_up", "traefik_service_retries_total", "traefik_router_requests_total",
			"traefik_config_reloads_total", "traefik_config_reloads_failure_total", "traefik_config_last_reload_success",
			"traefik_config_last_reload_failure", "traefik_tls_certs_not_after",
		},
		Rename: map[string]string{
			"traefik_entrypoint_requests_total":           "entrypoint.requests",
			"traefik_entrypoint_request_duration_seconds": "entrypoint.request_duration",
			"traefik_entrypoint_requests_bytes_total":     "entrypoint.requests_bytes",
			"traefik_entrypoint_responses_bytes_total":    "entrypoint.responses_bytes",
			"traefik_open_connections":                    "open_connections",
			"traefik_entrypoint_open_connections":         "entrypoint.open_connections",
			"traefik_service_requests_total":              "service.requests",
			"traefik_service_request_duration_seconds":    "service.request_duration",
			"traefik_service_server_up":                   "service.server_up",
			"traefik_service_retries_total":               "service.retries",
			"traefik_router_requests_total":               "router.requests",
			"traefik_config_reloads_total":                "config.reloads",
			"traefik_config_reloads_failure_total":        "config.reload_failures",
			"traefik_config_last_reload_success":          "config.last_reload_success",
			"traefik_config_last_reload_failure":          "config.last_reload_failure",
			"traefik_tls_certs_not_after":                 "tls.certs_not_after",
		},
		Labels: []string{"code", "entrypoint", "service", "router", "url", "cn"},
	})

	// Envoy 1.x : interface d'administration (:9901), /stats/prometheus. Durées en millisecondes.
	// Envoy publie à la fois envoy_cluster_upstream_rq (par code) et envoy_cluster_upstream_rq_total.
	register(Profile{
		Name: "envoy", Description: "Envoy Proxy", Port: 9901, Path: "/stats/prometheus", Namespace: "envoy",
		Metrics: []string{
			"envoy_server_live", "envoy_server_state", "envoy_server_uptime", "envoy_server_memory_allocated",
			"envoy_server_memory_heap_size", "envoy_server_total_connections", "envoy_server_days_until_first_cert_expiring",
			"envoy_cluster_upstream_cx_active", "envoy_cluster_upstream_cx_connect_fail", "envoy_cluster_upstream_cx_overflow",
			"envoy_cluster_upstream_rq_total", "envoy_cluster_upstream_rq_xx", "envoy_cluster_upstream_rq_time",
			"envoy_cluster_upstream_rq_timeout", "envoy_cluster_upstream_rq_retry", "envoy_cluster_upstream_rq_pending_overflow",
			"envoy_cluster_membership_healthy", "envoy_cluster_membership_total",
			"envoy_cluster_outlier_detection_ejections_active", "envoy_http_downstream_rq_total",
			"envoy_http_downstream_rq_xx", "envoy_http_downstream_rq_time", "envoy_http_downstream_cx_active",
			"envoy_listener_downstream_cx_active",
		},
		Rename: map[string]string{
			"envoy_server_live":                                "server.live",
			"envoy_server_state":                               "server.state",
			"envoy_server_uptime":                              "server.uptime",
			"envoy_server_memory_allocated":                    "server.memory.allocated",
			"envoy_server_memory_heap_size":                    "server.memory.heap_size",
			"envoy_server_total_connections":                   "server.total_connections",
			"envoy_server_days_until_first_cert_expiring":      "server.days_until_first_cert_expiring",
			"envoy_cluster_upstream_cx_active":                 "cluster.upstream.cx_active",
			"envoy_cluster_upstream_cx_connect_fail":           "cluster.upstream.cx_connect_fail",
			"envoy_cluster_upstream_cx_overflow":               "cluster.upstream.cx_overflow",
			"envoy_cluster_upstream_rq_total":                  "cluster.upstream.rq_total",
			"envoy_cluster_upstream_rq_xx":                     "cluster.upstream.rq_xx",
			"envoy_cluster_upstream_rq_time":                   "cluster.upstream.rq_time",
			"envoy_cluster_upstream_rq_timeout":                "cluster.upstream.rq_timeout",
			"envoy_cluster_upstream_rq_retry":                  "cluster.upstream.rq_retry",
			"envoy_cluster_upstream_rq_pending_overflow":       "cluster.upstream.rq_pending_overflow",
			"envoy_cluster_membership_healthy":                 "cluster.membership.healthy",
			"envoy_cluster_membership_total":                   "cluster.membership.total",
			"envoy_cluster_outlier_detection_ejections_active": "cluster.outlier_detection.ejections_active",
			"envoy_http_downstream_rq_total":                   "http.downstream.rq_total",
			"envoy_http_downstream_rq_xx":                      "http.downstream.rq_xx",
			"envoy_http_downstream_rq_time":                    "http.downstream.rq_time",
			"envoy_http_downstream_cx_active":                  "http.downstream.cx_active",
			"envoy_listener_downstream_cx_active":              "listener.downstream.cx_active",
		},
		Labels: []string{"envoy_cluster_name", "envoy_response_code_class", "envoy_http_conn_manager_prefix",
			"envoy_listener_address"},
	})

	// Kong Gateway 3.x : greffon prometheus activé, lu sur le status listener
	// (status_listen = 0.0.0.0:8100) ou l'API d'administration (:8001). Latences en millisecondes.
	register(Profile{
		Name: "kong", Description: "Kong Gateway", Port: 8100, Namespace: "kong",
		Metrics: []string{
			"kong_http_requests_total", "kong_request_latency_ms", "kong_upstream_latency_ms", "kong_kong_latency_ms",
			"kong_bandwidth_bytes", "kong_nginx_connections_total", "kong_datastore_reachable",
			"kong_upstream_target_health", "kong_memory_lua_shared_dict_bytes", "kong_memory_lua_shared_dict_total_bytes",
			"kong_memory_workers_lua_vms_bytes", "kong_nginx_metric_errors_total", "kong_nginx_timers",
			"kong_db_entities_total", "kong_control_plane_connected",
		},
		Rename: map[string]string{
			"kong_http_requests_total":                "http.requests",
			"kong_request_latency_ms":                 "request_latency",
			"kong_upstream_latency_ms":                "upstream_latency",
			"kong_kong_latency_ms":                    "kong_latency",
			"kong_bandwidth_bytes":                    "bandwidth",
			"kong_nginx_connections_total":            "nginx.connections",
			"kong_datastore_reachable":                "datastore.reachable",
			"kong_upstream_target_health":             "upstream.target_health",
			"kong_memory_lua_shared_dict_bytes":       "memory.shared_dict.used",
			"kong_memory_lua_shared_dict_total_bytes": "memory.shared_dict.total",
			"kong_memory_workers_lua_vms_bytes":       "memory.workers_lua_vms",
			"kong_nginx_metric_errors_total":          "nginx.metric_errors",
			"kong_nginx_timers":                       "nginx.timers",
			"kong_db_entities_total":                  "db.entities",
			"kong_control_plane_connected":            "control_plane.connected",
		},
		Labels: []string{"service", "route", "code", "direction", "state", "upstream", "target"},
	})

	// CockroachDB 23.x à 26.x : /_status/vars sur le port HTTP (8080, en HTTPS hors --insecure ;
	// lecture sans authentification). Latences en nanosecondes.
	register(Profile{
		Name: "cockroachdb", Description: "CockroachDB", Scheme: "https", Port: 8080, Path: "/_status/vars",
		Namespace: "cockroachdb",
		Metrics: []string{
			"liveness_livenodes", "liveness_heartbeatfailures", "ranges", "ranges_unavailable", "ranges_underreplicated",
			"ranges_overreplicated", "replicas", "replicas_leaders", "replicas_leaseholders", "capacity",
			"capacity_available", "capacity_used", "livebytes", "sql_conns", "sql_new_conns", "sql_select_count",
			"sql_insert_count", "sql_update_count", "sql_delete_count", "sql_query_count", "sql_failure_count",
			"sql_txn_begin_count", "sql_txn_commit_count", "sql_txn_abort_count", "sql_txn_rollback_count",
			"sql_service_latency", "sql_exec_latency", "sql_txn_latency", "txn_commits", "txn_aborts",
			"sys_cpu_combined_percent_normalized", "sys_rss", "sys_goroutines", "clock_offset_meannanos",
			"security_certificate_expiration_ca", "security_certificate_expiration_node", "requests_slow_latch",
			"requests_slow_lease", "requests_slow_raft", "rocksdb_read_amplification", "storage_l0_sublevels",
			"admission_io_overload", "sys_host_disk_read_bytes", "sys_host_disk_write_bytes", "sql_mem_root_current",
		},
		Rename: map[string]string{
			"liveness_livenodes":                   "liveness.live_nodes",
			"liveness_heartbeatfailures":           "liveness.heartbeat_failures",
			"ranges":                               "ranges.total",
			"ranges_unavailable":                   "ranges.unavailable",
			"ranges_underreplicated":               "ranges.underreplicated",
			"ranges_overreplicated":                "ranges.overreplicated",
			"replicas":                             "replicas.total",
			"replicas_leaders":                     "replicas.leaders",
			"replicas_leaseholders":                "replicas.leaseholders",
			"capacity":                             "capacity.total",
			"capacity_available":                   "capacity.available",
			"capacity_used":                        "capacity.used",
			"livebytes":                            "live_bytes",
			"sql_conns":                            "sql.conns",
			"sql_new_conns":                        "sql.new_conns",
			"sql_select_count":                     "sql.select",
			"sql_insert_count":                     "sql.insert",
			"sql_update_count":                     "sql.update",
			"sql_delete_count":                     "sql.delete",
			"sql_query_count":                      "sql.query",
			"sql_failure_count":                    "sql.failure",
			"sql_txn_begin_count":                  "sql.txn.begin",
			"sql_txn_commit_count":                 "sql.txn.commit",
			"sql_txn_abort_count":                  "sql.txn.abort",
			"sql_txn_rollback_count":               "sql.txn.rollback",
			"sql_service_latency":                  "sql.service_latency",
			"sql_exec_latency":                     "sql.exec_latency",
			"sql_txn_latency":                      "sql.txn_latency",
			"txn_commits":                          "txn.commits",
			"txn_aborts":                           "txn.aborts",
			"sys_cpu_combined_percent_normalized":  "sys.cpu.combined_normalized",
			"sys_rss":                              "sys.rss",
			"sys_goroutines":                       "sys.goroutines",
			"clock_offset_meannanos":               "clock_offset.mean_nanos",
			"security_certificate_expiration_ca":   "security.certificate_expiration.ca",
			"security_certificate_expiration_node": "security.certificate_expiration.node",
			"requests_slow_latch":                  "requests.slow.latch",
			"requests_slow_lease":                  "requests.slow.lease",
			"requests_slow_raft":                   "requests.slow.raft",
			"rocksdb_read_amplification":           "storage.read_amplification",
			"storage_l0_sublevels":                 "storage.l0_sublevels",
			"admission_io_overload":                "admission.io_overload",
			"sys_host_disk_read_bytes":             "sys.host.disk.read",
			"sys_host_disk_write_bytes":            "sys.host.disk.write",
			"sql_mem_root_current":                 "sql.mem.root_current",
		},
		Labels: []string{"node_id", "store"},
	})

	// n8n 1.x/2.x : N8N_METRICS=true ; exécutions des flux avec
	// N8N_METRICS_INCLUDE_MESSAGE_EVENT_BUS_METRICS=true (et N8N_METRICS_INCLUDE_WORKFLOW_ID_LABEL
	// pour les avoir flux par flux) ; files d'attente en mode queue.
	register(Profile{
		Name: "n8n", Description: "n8n (automatisation de flux)", Port: 5678, Namespace: "n8n",
		Metrics: []string{
			"n8n_active_workflow_count", "n8n_workflow_started_total", "n8n_workflow_success_total",
			"n8n_workflow_failed_total", "n8n_workflow_execution_duration_seconds", "n8n_scaling_mode_queue_jobs_waiting",
			"n8n_scaling_mode_queue_jobs_active", "n8n_scaling_mode_queue_jobs_completed",
			"n8n_scaling_mode_queue_jobs_failed", "n8n_instance_role_leader", "n8n_last_activity",
			"n8n_nodejs_eventloop_lag_p99_seconds", "n8n_nodejs_heap_size_used_bytes", "n8n_nodejs_heap_size_total_bytes",
			"n8n_process_resident_memory_bytes", "n8n_process_cpu_seconds_total", "n8n_process_open_fds",
		},
		Rename: map[string]string{
			"n8n_active_workflow_count":               "workflows.active",
			"n8n_workflow_started_total":              "workflow.started",
			"n8n_workflow_success_total":              "workflow.success",
			"n8n_workflow_failed_total":               "workflow.failed",
			"n8n_workflow_execution_duration_seconds": "workflow.execution_duration",
			"n8n_scaling_mode_queue_jobs_waiting":     "queue.jobs.waiting",
			"n8n_scaling_mode_queue_jobs_active":      "queue.jobs.active",
			"n8n_scaling_mode_queue_jobs_completed":   "queue.jobs.completed",
			"n8n_scaling_mode_queue_jobs_failed":      "queue.jobs.failed",
			"n8n_instance_role_leader":                "instance.leader",
			"n8n_last_activity":                       "last_activity",
			"n8n_nodejs_eventloop_lag_p99_seconds":    "nodejs.eventloop_lag_p99",
			"n8n_nodejs_heap_size_used_bytes":         "nodejs.heap_used",
			"n8n_nodejs_heap_size_total_bytes":        "nodejs.heap_total",
			"n8n_process_resident_memory_bytes":       "process.resident_memory",
			"n8n_process_cpu_seconds_total":           "process.cpu_seconds",
			"n8n_process_open_fds":                    "process.open_fds",
		},
		Labels: []string{"status", "workflow_id"},
	})

	// Temporal Server 1.x : global.metrics.prometheus.listenAddress (9090 dans la charte Helm,
	// 8000 dans les exemples Docker). Demandes et erreurs par service, persistance, arriéré des
	// files de tâches, issue des flux. Durées en secondes.
	register(Profile{
		Name: "temporal", Description: "Temporal Server", Port: 9090, Namespace: "temporal",
		Metrics: []string{
			"service_requests", "service_error_with_type", "service_latency", "service_pending_requests",
			"persistence_requests", "persistence_error_with_type", "persistence_latency", "approximate_backlog_count",
			"approximate_backlog_age_seconds", "poll_timeouts", "workflow_success", "workflow_failed", "workflow_timeout",
			"workflow_terminate", "workflow_cancel", "workflow_continued_as_new", "workflow_schedule_to_close_latency",
			"numshards_gauge", "restarts", "num_goroutines", "memory_heap", "service_grpc_conn_active",
		},
		Rename: map[string]string{
			"service_requests":                   "service.requests",
			"service_error_with_type":            "service.errors",
			"service_latency":                    "service.latency",
			"service_pending_requests":           "service.pending_requests",
			"persistence_requests":               "persistence.requests",
			"persistence_error_with_type":        "persistence.errors",
			"persistence_latency":                "persistence.latency",
			"approximate_backlog_count":          "task_queue.backlog",
			"approximate_backlog_age_seconds":    "task_queue.backlog_age",
			"poll_timeouts":                      "task_queue.poll_timeouts",
			"workflow_success":                   "workflow.success",
			"workflow_failed":                    "workflow.failed",
			"workflow_timeout":                   "workflow.timeout",
			"workflow_terminate":                 "workflow.terminate",
			"workflow_cancel":                    "workflow.cancel",
			"workflow_continued_as_new":          "workflow.continued_as_new",
			"workflow_schedule_to_close_latency": "workflow.schedule_to_close_latency",
			"numshards_gauge":                    "history.shards",
			"restarts":                           "restarts",
			"num_goroutines":                     "goroutines",
			"memory_heap":                        "memory.heap",
			"service_grpc_conn_active":           "grpc.conn_active",
		},
		Labels: []string{"service_name", "namespace", "taskqueue", "task_type", "error_type", "workflowType"},
	})

	// Celery 5.x par Flower 2.x (/metrics, :5555) : événements des tâches par type, durée, tâches en
	// cours et préchargées, ouvriers en ligne. Les ouvriers doivent émettre leurs événements (-E).
	register(Profile{
		Name: "celery", Description: "Celery (lu par Flower)", Port: 5555, Namespace: "celery",
		Metrics: []string{
			"flower_events_total", "flower_task_runtime_seconds", "flower_task_prefetch_time_seconds",
			"flower_worker_online", "flower_worker_number_of_currently_executing_tasks", "flower_worker_prefetched_tasks",
		},
		Rename: map[string]string{
			"flower_events_total":                               "events",
			"flower_task_runtime_seconds":                       "task.runtime",
			"flower_task_prefetch_time_seconds":                 "task.prefetch_time",
			"flower_worker_online":                              "worker.online",
			"flower_worker_number_of_currently_executing_tasks": "worker.executing_tasks",
			"flower_worker_prefetched_tasks":                    "worker.prefetched_tasks",
		},
		Labels: []string{"task", "type", "worker"},
	})

	// ── Java par l'exportateur JMX Prometheus (javaagent 1.x, règles de la documentation) ──
	// Chaque profil reprend aussi les mesures de la JVM publiées par le javaagent (tas, GC,
	// fils, processeur, descripteurs) et l'état du relevé JMX lui-même.

	// Apache Tomcat 9 à 11 : -javaagent:jmx_prometheus_javaagent.jar=9404:tomcat.yml.
	register(Profile{
		Name: "tomcat", Description: "Apache Tomcat (exportateur JMX)", Port: 9404, Namespace: "tomcat",
		Metrics: []string{
			"tomcat_requestprocessor_requestcount_total", "tomcat_requestprocessor_errorcount_total",
			"tomcat_requestprocessor_bytesreceived_total", "tomcat_requestprocessor_bytessent_total",
			"tomcat_requestprocessor_processingtime_total", "tomcat_requestprocessor_maxtime",
			"tomcat_threadpool_currentthreadcount", "tomcat_threadpool_currentthreadsbusy", "tomcat_threadpool_maxthreads",
			"tomcat_threadpool_connectioncount", "tomcat_threadpool_maxconnections", "tomcat_session_activesessions",
			"tomcat_session_maxactivesessions", "tomcat_session_rejectedsessions_total", "tomcat_session_expiredsessions_total",
			"tomcat_session_sessioncounter_total",
			"jvm_memory_used_bytes", "jvm_memory_committed_bytes", "jvm_memory_max_bytes", "jvm_gc_collection_seconds",
			"jvm_threads_current", "jvm_threads_deadlocked", "jvm_classes_currently_loaded", "process_cpu_seconds_total",
			"process_open_fds", "process_max_fds", "process_resident_memory_bytes", "jmx_scrape_error",
			"jmx_scrape_duration_seconds",
		},
		Rename: map[string]string{
			"tomcat_requestprocessor_requestcount_total":   "requests",
			"tomcat_requestprocessor_errorcount_total":     "errors",
			"tomcat_requestprocessor_bytesreceived_total":  "bytes_received",
			"tomcat_requestprocessor_bytessent_total":      "bytes_sent",
			"tomcat_requestprocessor_processingtime_total": "processing_time",
			"tomcat_requestprocessor_maxtime":              "max_time",
			"tomcat_threadpool_currentthreadcount":         "threads.current",
			"tomcat_threadpool_currentthreadsbusy":         "threads.busy",
			"tomcat_threadpool_maxthreads":                 "threads.max",
			"tomcat_threadpool_connectioncount":            "connections.current",
			"tomcat_threadpool_maxconnections":             "connections.max",
			"tomcat_session_activesessions":                "sessions.active",
			"tomcat_session_maxactivesessions":             "sessions.max_active",
			"tomcat_session_rejectedsessions_total":        "sessions.rejected",
			"tomcat_session_expiredsessions_total":         "sessions.expired",
			"tomcat_session_sessioncounter_total":          "sessions.created",
			"jvm_memory_used_bytes":                        "jvm.memory.used",
			"jvm_memory_committed_bytes":                   "jvm.memory.committed",
			"jvm_memory_max_bytes":                         "jvm.memory.max",
			"jvm_gc_collection_seconds":                    "jvm.gc.collection",
			"jvm_threads_current":                          "jvm.threads.current",
			"jvm_threads_deadlocked":                       "jvm.threads.deadlocked",
			"jvm_classes_currently_loaded":                 "jvm.classes.loaded",
			"process_cpu_seconds_total":                    "process.cpu_seconds",
			"process_open_fds":                             "process.open_fds",
			"process_max_fds":                              "process.max_fds",
			"process_resident_memory_bytes":                "process.resident_memory",
			"jmx_scrape_error":                             "jmx.scrape_error",
			"jmx_scrape_duration_seconds":                  "jmx.scrape_duration",
		},
		Labels: []string{"connector", "context", "area"},
	})

	// Apache Kafka 3.x/4.x (KRaft ou ZooKeeper) : KAFKA_OPTS=-javaagent:…=9405:kafka.yml. Débits
	// du courtier (tous sujets), partitions sous-répliquées ou hors ligne, contrôleur actif,
	// temps des requêtes (moyenne et 99e centile, en ms), inactivité des fils réseau et de
	// traitement.
	register(Profile{
		Name: "kafka", Description: "Apache Kafka (courtier, exportateur JMX)", Port: 9405, Namespace: "kafka",
		Metrics: []string{
			"kafka_server_brokertopicmetrics_messagesinpersec_total", "kafka_server_brokertopicmetrics_bytesinpersec_total",
			"kafka_server_brokertopicmetrics_bytesoutpersec_total", "kafka_server_brokertopicmetrics_bytesrejectedpersec_total",
			"kafka_server_brokertopicmetrics_failedproducerequestspersec_total",
			"kafka_server_brokertopicmetrics_failedfetchrequestspersec_total",
			"kafka_server_brokertopicmetrics_totalproducerequestspersec_total",
			"kafka_server_brokertopicmetrics_totalfetchrequestspersec_total",
			"kafka_server_replicamanager_underreplicatedpartitions", "kafka_server_replicamanager_underminisrpartitioncount",
			"kafka_server_replicamanager_offlinereplicacount", "kafka_server_replicamanager_partitioncount",
			"kafka_server_replicamanager_leadercount", "kafka_server_replicamanager_isrshrinkspersec_total",
			"kafka_server_replicamanager_isrexpandspersec_total", "kafka_server_requesthandler_avg_idle_percent",
			"kafka_controller_activecontrollercount", "kafka_controller_offlinepartitionscount",
			"kafka_controller_globaltopiccount", "kafka_controller_globalpartitioncount",
			"kafka_controller_preferredreplicaimbalancecount", "kafka_controller_activebrokercount",
			"kafka_controller_fencedbrokercount", "kafka_controller_stats_uncleanleaderelectionspersec_total",
			"kafka_network_request_totaltimems_mean", "kafka_network_request_totaltimems_99thpercentile",
			"kafka_network_requests_total", "kafka_network_processor_avg_idle_percent", "kafka_log_flushes_total",
			"kafka_log_flush_time_ms_p99",
			"jvm_memory_used_bytes", "jvm_memory_committed_bytes", "jvm_memory_max_bytes", "jvm_gc_collection_seconds",
			"jvm_threads_current", "jvm_threads_deadlocked", "jvm_classes_currently_loaded", "process_cpu_seconds_total",
			"process_open_fds", "process_max_fds", "process_resident_memory_bytes", "jmx_scrape_error",
			"jmx_scrape_duration_seconds",
		},
		Rename: map[string]string{
			"kafka_server_brokertopicmetrics_messagesinpersec_total":            "broker.messages_in",
			"kafka_server_brokertopicmetrics_bytesinpersec_total":               "broker.bytes_in",
			"kafka_server_brokertopicmetrics_bytesoutpersec_total":              "broker.bytes_out",
			"kafka_server_brokertopicmetrics_bytesrejectedpersec_total":         "broker.bytes_rejected",
			"kafka_server_brokertopicmetrics_failedproducerequestspersec_total": "broker.produce.failed",
			"kafka_server_brokertopicmetrics_failedfetchrequestspersec_total":   "broker.fetch.failed",
			"kafka_server_brokertopicmetrics_totalproducerequestspersec_total":  "broker.produce.requests",
			"kafka_server_brokertopicmetrics_totalfetchrequestspersec_total":    "broker.fetch.requests",
			"kafka_server_replicamanager_underreplicatedpartitions":             "replication.under_replicated_partitions",
			"kafka_server_replicamanager_underminisrpartitioncount":             "replication.under_min_isr_partitions",
			"kafka_server_replicamanager_offlinereplicacount":                   "replication.offline_replicas",
			"kafka_server_replicamanager_partitioncount":                        "replication.partitions",
			"kafka_server_replicamanager_leadercount":                           "replication.leaders",
			"kafka_server_replicamanager_isrshrinkspersec_total":                "replication.isr_shrinks",
			"kafka_server_replicamanager_isrexpandspersec_total":                "replication.isr_expands",
			"kafka_server_requesthandler_avg_idle_percent":                      "request_handler.avg_idle",
			"kafka_controller_activecontrollercount":                            "controller.active",
			"kafka_controller_offlinepartitionscount":                           "controller.offline_partitions",
			"kafka_controller_globaltopiccount":                                 "controller.topics",
			"kafka_controller_globalpartitioncount":                             "controller.partitions",
			"kafka_controller_preferredreplicaimbalancecount":                   "controller.preferred_replica_imbalance",
			"kafka_controller_activebrokercount":                                "controller.active_brokers",
			"kafka_controller_fencedbrokercount":                                "controller.fenced_brokers",
			"kafka_controller_stats_uncleanleaderelectionspersec_total":         "controller.unclean_leader_elections",
			"kafka_network_request_totaltimems_mean":                            "request.total_time.mean",
			"kafka_network_request_totaltimems_99thpercentile":                  "request.total_time.p99",
			"kafka_network_requests_total":                                      "request.count",
			"kafka_network_processor_avg_idle_percent":                          "network.processor_avg_idle",
			"kafka_log_flushes_total":                                           "log.flushes",
			"kafka_log_flush_time_ms_p99":                                       "log.flush_time.p99",
			"jvm_memory_used_bytes":                                             "jvm.memory.used",
			"jvm_memory_committed_bytes":                                        "jvm.memory.committed",
			"jvm_memory_max_bytes":                                              "jvm.memory.max",
			"jvm_gc_collection_seconds":                                         "jvm.gc.collection",
			"jvm_threads_current":                                               "jvm.threads.current",
			"jvm_threads_deadlocked":                                            "jvm.threads.deadlocked",
			"jvm_classes_currently_loaded":                                      "jvm.classes.loaded",
			"process_cpu_seconds_total":                                         "process.cpu_seconds",
			"process_open_fds":                                                  "process.open_fds",
			"process_max_fds":                                                   "process.max_fds",
			"process_resident_memory_bytes":                                     "process.resident_memory",
			"jmx_scrape_error":                                                  "jmx.scrape_error",
			"jmx_scrape_duration_seconds":                                       "jmx.scrape_duration",
		},
		Labels: []string{"request", "area"},
	})

	// Apache ActiveMQ Classic 5.x/6.x : ACTIVEMQ_OPTS=… -javaagent:…=9406:activemq.yml. Mémoire et
	// stockage du courtier (% de leurs limites : à 100 %, les producteurs sont bloqués), files et
	// sujets (taille, consommateurs, débits), sans les destinations d'avis.
	register(Profile{
		Name: "activemq", Description: "Apache ActiveMQ Classic (exportateur JMX)", Port: 9406, Namespace: "activemq",
		Metrics: []string{
			"activemq_broker_memorypercentusage", "activemq_broker_storepercentusage", "activemq_broker_temppercentusage",
			"activemq_broker_currentconnectionscount", "activemq_broker_totalconsumercount",
			"activemq_broker_totalproducercount", "activemq_broker_totalmessagecount", "activemq_broker_totalenqueuecount_total",
			"activemq_broker_totaldequeuecount_total", "activemq_destination_queuesize", "activemq_destination_consumercount",
			"activemq_destination_producercount", "activemq_destination_inflightcount",
			"activemq_destination_memorypercentusage", "activemq_destination_averageenqueuetime",
			"activemq_destination_enqueuecount_total", "activemq_destination_dequeuecount_total",
			"activemq_destination_expiredcount_total", "activemq_destination_dispatchcount_total",
			"jvm_memory_used_bytes", "jvm_memory_committed_bytes", "jvm_memory_max_bytes", "jvm_gc_collection_seconds",
			"jvm_threads_current", "jvm_threads_deadlocked", "jvm_classes_currently_loaded", "process_cpu_seconds_total",
			"process_open_fds", "process_max_fds", "process_resident_memory_bytes", "jmx_scrape_error",
			"jmx_scrape_duration_seconds",
		},
		Rename: map[string]string{
			"activemq_broker_memorypercentusage":       "broker.memory_usage",
			"activemq_broker_storepercentusage":        "broker.store_usage",
			"activemq_broker_temppercentusage":         "broker.temp_usage",
			"activemq_broker_currentconnectionscount":  "broker.connections",
			"activemq_broker_totalconsumercount":       "broker.consumers",
			"activemq_broker_totalproducercount":       "broker.producers",
			"activemq_broker_totalmessagecount":        "broker.messages",
			"activemq_broker_totalenqueuecount_total":  "broker.enqueued",
			"activemq_broker_totaldequeuecount_total":  "broker.dequeued",
			"activemq_destination_queuesize":           "destination.size",
			"activemq_destination_consumercount":       "destination.consumers",
			"activemq_destination_producercount":       "destination.producers",
			"activemq_destination_inflightcount":       "destination.in_flight",
			"activemq_destination_memorypercentusage":  "destination.memory_usage",
			"activemq_destination_averageenqueuetime":  "destination.average_enqueue_time",
			"activemq_destination_enqueuecount_total":  "destination.enqueued",
			"activemq_destination_dequeuecount_total":  "destination.dequeued",
			"activemq_destination_expiredcount_total":  "destination.expired",
			"activemq_destination_dispatchcount_total": "destination.dispatched",
			"jvm_memory_used_bytes":                    "jvm.memory.used",
			"jvm_memory_committed_bytes":               "jvm.memory.committed",
			"jvm_memory_max_bytes":                     "jvm.memory.max",
			"jvm_gc_collection_seconds":                "jvm.gc.collection",
			"jvm_threads_current":                      "jvm.threads.current",
			"jvm_threads_deadlocked":                   "jvm.threads.deadlocked",
			"jvm_classes_currently_loaded":             "jvm.classes.loaded",
			"process_cpu_seconds_total":                "process.cpu_seconds",
			"process_open_fds":                         "process.open_fds",
			"process_max_fds":                          "process.max_fds",
			"process_resident_memory_bytes":            "process.resident_memory",
			"jmx_scrape_error":                         "jmx.scrape_error",
			"jmx_scrape_duration_seconds":              "jmx.scrape_duration",
		},
		Labels: []string{"broker", "destination_type", "destination", "area"},
	})

	// Apache Cassandra 4.x/5.x : JVM_EXTRA_OPTS=-javaagent:…=9407:cassandra.yml. Requêtes clientes
	// par portée (lecture, écriture…), latences en microsecondes, délais dépassés, indisponibles,
	// charge, compactions en attente, files des pools de fils, messages abandonnés, clients CQL.
	register(Profile{
		Name: "cassandra", Description: "Apache Cassandra (exportateur JMX)", Port: 9407, Namespace: "cassandra",
		Metrics: []string{
			"cassandra_clientrequest_requests_total", "cassandra_clientrequest_latency_mean",
			"cassandra_clientrequest_latency_99thpercentile", "cassandra_clientrequest_timeouts_total",
			"cassandra_clientrequest_unavailables_total", "cassandra_clientrequest_failures_total", "cassandra_storage_load",
			"cassandra_storage_exceptions_total", "cassandra_storage_totalhints_total", "cassandra_storage_totalhintsinprogress",
			"cassandra_compaction_pendingtasks", "cassandra_compaction_completedtasks_total",
			"cassandra_compaction_bytescompacted_total", "cassandra_threadpools_activetasks",
			"cassandra_threadpools_pendingtasks", "cassandra_threadpools_totalblockedtasks_total",
			"cassandra_droppedmessage_dropped_total", "cassandra_client_connectednativeclients", "cassandra_cache_hitrate",
			"cassandra_cache_size", "cassandra_cache_capacity", "cassandra_commitlog_pendingtasks",
			"cassandra_commitlog_totalcommitlogsize",
			"jvm_memory_used_bytes", "jvm_memory_committed_bytes", "jvm_memory_max_bytes", "jvm_gc_collection_seconds",
			"jvm_threads_current", "jvm_threads_deadlocked", "jvm_classes_currently_loaded", "process_cpu_seconds_total",
			"process_open_fds", "process_max_fds", "process_resident_memory_bytes", "jmx_scrape_error",
			"jmx_scrape_duration_seconds",
		},
		Rename: map[string]string{
			"cassandra_clientrequest_requests_total":         "client_request.count",
			"cassandra_clientrequest_latency_mean":           "client_request.latency.mean",
			"cassandra_clientrequest_latency_99thpercentile": "client_request.latency.p99",
			"cassandra_clientrequest_timeouts_total":         "client_request.timeouts",
			"cassandra_clientrequest_unavailables_total":     "client_request.unavailables",
			"cassandra_clientrequest_failures_total":         "client_request.failures",
			"cassandra_storage_load":                         "storage.load",
			"cassandra_storage_exceptions_total":             "storage.exceptions",
			"cassandra_storage_totalhints_total":             "storage.hints",
			"cassandra_storage_totalhintsinprogress":         "storage.hints_in_progress",
			"cassandra_compaction_pendingtasks":              "compaction.pending_tasks",
			"cassandra_compaction_completedtasks_total":      "compaction.completed_tasks",
			"cassandra_compaction_bytescompacted_total":      "compaction.bytes_compacted",
			"cassandra_threadpools_activetasks":              "thread_pools.active_tasks",
			"cassandra_threadpools_pendingtasks":             "thread_pools.pending_tasks",
			"cassandra_threadpools_totalblockedtasks_total":  "thread_pools.blocked_tasks",
			"cassandra_droppedmessage_dropped_total":         "dropped_messages",
			"cassandra_client_connectednativeclients":        "client.connected_native_clients",
			"cassandra_cache_hitrate":                        "cache.hit_rate",
			"cassandra_cache_size":                           "cache.size",
			"cassandra_cache_capacity":                       "cache.capacity",
			"cassandra_commitlog_pendingtasks":               "commitlog.pending_tasks",
			"cassandra_commitlog_totalcommitlogsize":         "commitlog.size",
			"jvm_memory_used_bytes":                          "jvm.memory.used",
			"jvm_memory_committed_bytes":                     "jvm.memory.committed",
			"jvm_memory_max_bytes":                           "jvm.memory.max",
			"jvm_gc_collection_seconds":                      "jvm.gc.collection",
			"jvm_threads_current":                            "jvm.threads.current",
			"jvm_threads_deadlocked":                         "jvm.threads.deadlocked",
			"jvm_classes_currently_loaded":                   "jvm.classes.loaded",
			"process_cpu_seconds_total":                      "process.cpu_seconds",
			"process_open_fds":                               "process.open_fds",
			"process_max_fds":                                "process.max_fds",
			"process_resident_memory_bytes":                  "process.resident_memory",
			"jmx_scrape_error":                               "jmx.scrape_error",
			"jmx_scrape_duration_seconds":                    "jmx.scrape_duration",
		},
		Labels: []string{"scope", "pool", "cache", "area"},
	})

	// Apache Solr 9.x et 10.x : mesures natives au format Prometheus
	// (/solr/admin/metrics?wt=prometheus), sans exportateur. Solr 10 publie des noms
	// OpenTelemetry (solr_core_*, jvm_*), Solr 9 des noms solr_metrics_* : les deux jeux sont
	// retenus (préfixe metrics. pour Solr 9).
	register(Profile{
		Name: "solr", Description: "Apache Solr (mesures natives)", Port: 8983,
		Path: "/solr/admin/metrics?wt=prometheus", Namespace: "solr",
		Metrics: []string{
			"solr_core_requests_total", "solr_core_requests_errors_total", "solr_core_requests_times_milliseconds",
			"solr_node_requests_errors_total", "solr_core_index_size_megabytes", "solr_core_indexsearcher_index_num_docs",
			"solr_core_indexsearcher_cache_lookups_total", "solr_core_update_docs_pending_commit", "solr_core_segments",
			"solr_cores_loaded", "solr_disk_space_megabytes", "jvm_memory_used_bytes", "jvm_memory_limit_bytes",
			"jvm_gc_duration_seconds", "jvm_thread_count", "jvm_cpu_recent_utilization",
			"solr_metrics_core_requests_total", "solr_metrics_core_request_time_ms", "solr_metrics_core_index_size_bytes",
			"solr_metrics_core_searcher_documents", "solr_metrics_core_update_handler", "solr_metrics_jetty_response_total",
			"solr_metrics_node_cores",
		},
		Rename: map[string]string{
			"solr_core_requests_total":                    "core.requests",
			"solr_core_requests_errors_total":             "core.request_errors",
			"solr_core_requests_times_milliseconds":       "core.request_time",
			"solr_node_requests_errors_total":             "node.request_errors",
			"solr_core_index_size_megabytes":              "core.index_size_mb",
			"solr_core_indexsearcher_index_num_docs":      "core.num_docs",
			"solr_core_indexsearcher_cache_lookups_total": "core.cache.lookups",
			"solr_core_update_docs_pending_commit":        "core.update.docs_pending",
			"solr_core_segments":                          "core.segments",
			"solr_cores_loaded":                           "cores",
			"solr_disk_space_megabytes":                   "disk_space_mb",
			"jvm_memory_used_bytes":                       "jvm.memory.used",
			"jvm_memory_limit_bytes":                      "jvm.memory.limit",
			"jvm_gc_duration_seconds":                     "jvm.gc.duration",
			"jvm_thread_count":                            "jvm.threads",
			"jvm_cpu_recent_utilization":                  "jvm.cpu.utilization",
			"solr_metrics_core_requests_total":            "metrics.core.requests",
			"solr_metrics_core_request_time_ms":           "metrics.core.request_time",
			"solr_metrics_core_index_size_bytes":          "metrics.core.index_size",
			"solr_metrics_core_searcher_documents":        "metrics.core.searcher_documents",
			"solr_metrics_core_update_handler":            "metrics.core.update_handler",
			"solr_metrics_jetty_response_total":           "metrics.jetty.responses",
			"solr_metrics_node_cores":                     "metrics.node.cores",
		},
		Labels: []string{"core", "category", "type", "result", "status", "item", "jvm_memory_type"},
	})

	// WildFly 27 et suivants : point /metrics natif de l'interface d'administration (9990,
	// sous-système metrics), sans exportateur ; statistiques d'Undertow et des sources de données
	// avec -Dwildfly.statistics-enabled=true. (L'exportateur JMX en javaagent se heurte au
	// LogManager de JBoss sur WildFly 42.)
	register(Profile{
		Name: "wildfly", Description: "WildFly (mesures natives)", Port: 9990, Namespace: "wildfly",
		Metrics: []string{
			"wildfly_undertow_request_count_total", "wildfly_undertow_error_count_total",
			"wildfly_undertow_max_processing_time_seconds", "wildfly_datasources_pool_active_count",
			"wildfly_datasources_pool_available_count", "wildfly_datasources_pool_in_use_count",
			"wildfly_datasources_pool_idle_count", "wildfly_datasources_pool_max_used_count",
			"wildfly_datasources_pool_wait_count", "wildfly_datasources_pool_timed_out",
			"wildfly_datasources_pool_average_get_time", "wildfly_transactions_number_of_transactions_total",
			"wildfly_transactions_number_of_committed_transactions_total",
			"wildfly_transactions_number_of_aborted_transactions_total",
			"wildfly_transactions_number_of_timed_out_transactions_total",
			"wildfly_transactions_number_of_application_rollbacks_total",
			"wildfly_transactions_number_of_resource_rollbacks_total", "wildfly_transactions_number_of_inflight_transactions",
			"wildfly_request_controller_active_requests", "wildfly_ee_hung_thread_count", "base_memory_usedHeap_bytes",
			"base_memory_maxHeap_bytes", "base_memory_usedNonHeap_bytes", "base_thread_count", "base_gc_total",
			"base_cpu_processCpuLoad", "base_jvm_uptime_seconds",
		},
		Rename: map[string]string{
			"wildfly_undertow_request_count_total":                        "undertow.requests",
			"wildfly_undertow_error_count_total":                          "undertow.errors",
			"wildfly_undertow_max_processing_time_seconds":                "undertow.max_processing_time",
			"wildfly_datasources_pool_active_count":                       "datasource.pool.active",
			"wildfly_datasources_pool_available_count":                    "datasource.pool.available",
			"wildfly_datasources_pool_in_use_count":                       "datasource.pool.in_use",
			"wildfly_datasources_pool_idle_count":                         "datasource.pool.idle",
			"wildfly_datasources_pool_max_used_count":                     "datasource.pool.max_used",
			"wildfly_datasources_pool_wait_count":                         "datasource.pool.wait_count",
			"wildfly_datasources_pool_timed_out":                          "datasource.pool.timed_out",
			"wildfly_datasources_pool_average_get_time":                   "datasource.pool.average_get_time",
			"wildfly_transactions_number_of_transactions_total":           "transactions.started",
			"wildfly_transactions_number_of_committed_transactions_total": "transactions.committed",
			"wildfly_transactions_number_of_aborted_transactions_total":   "transactions.aborted",
			"wildfly_transactions_number_of_timed_out_transactions_total": "transactions.timed_out",
			"wildfly_transactions_number_of_application_rollbacks_total":  "transactions.application_rollbacks",
			"wildfly_transactions_number_of_resource_rollbacks_total":     "transactions.resource_rollbacks",
			"wildfly_transactions_number_of_inflight_transactions":        "transactions.inflight",
			"wildfly_request_controller_active_requests":                  "request_controller.active_requests",
			"wildfly_ee_hung_thread_count":                                "ee.hung_threads",
			"base_memory_usedHeap_bytes":                                  "jvm.memory.heap_used",
			"base_memory_maxHeap_bytes":                                   "jvm.memory.heap_max",
			"base_memory_usedNonHeap_bytes":                               "jvm.memory.nonheap_used",
			"base_thread_count":                                           "jvm.threads",
			"base_gc_total":                                               "jvm.gc.collections",
			"base_cpu_processCpuLoad":                                     "process.cpu_load",
			"base_jvm_uptime_seconds":                                     "jvm.uptime",
		},
		Labels: []string{"data_source", "http_listener", "https_listener"},
	})

	// Apache Pulsar 3.x à 5.x : courtier, /metrics/ sur le port HTTP (8080). Agrégats du courtier
	// et par espace de noms Pulsar (l'étiquette topic est écartée : un sujet de plus ne fait pas
	// une série de plus) ; latence de publication en millisecondes.
	register(Profile{
		Name: "pulsar", Description: "Apache Pulsar (courtier)", Port: 8080, Path: "/metrics/", Namespace: "pulsar",
		Metrics: []string{
			"pulsar_broker_topics_count", "pulsar_broker_subscriptions_count", "pulsar_broker_producers_count",
			"pulsar_broker_consumers_count", "pulsar_broker_rate_in", "pulsar_broker_rate_out", "pulsar_broker_throughput_in",
			"pulsar_broker_throughput_out", "pulsar_broker_msg_backlog", "pulsar_broker_storage_size",
			"pulsar_broker_storage_write_rate", "pulsar_broker_storage_read_rate", "pulsar_broker_publish_latency",
			"pulsar_broker_lookup_failures", "pulsar_topics_count", "pulsar_msg_backlog", "pulsar_rate_in", "pulsar_rate_out",
			"pulsar_storage_size", "pulsar_active_connections", "jvm_memory_bytes_used", "jvm_memory_bytes_max",
			"jvm_gc_collection_seconds", "process_resident_memory_bytes", "process_cpu_seconds",
		},
		Rename: map[string]string{
			"pulsar_broker_topics_count":        "broker.topics",
			"pulsar_broker_subscriptions_count": "broker.subscriptions",
			"pulsar_broker_producers_count":     "broker.producers",
			"pulsar_broker_consumers_count":     "broker.consumers",
			"pulsar_broker_rate_in":             "broker.rate_in",
			"pulsar_broker_rate_out":            "broker.rate_out",
			"pulsar_broker_throughput_in":       "broker.throughput_in",
			"pulsar_broker_throughput_out":      "broker.throughput_out",
			"pulsar_broker_msg_backlog":         "broker.msg_backlog",
			"pulsar_broker_storage_size":        "broker.storage_size",
			"pulsar_broker_storage_write_rate":  "broker.storage.write_rate",
			"pulsar_broker_storage_read_rate":   "broker.storage.read_rate",
			"pulsar_broker_publish_latency":     "broker.publish_latency",
			"pulsar_broker_lookup_failures":     "broker.lookup_failures",
			"pulsar_topics_count":               "namespace.topics",
			"pulsar_msg_backlog":                "namespace.msg_backlog",
			"pulsar_rate_in":                    "namespace.rate_in",
			"pulsar_rate_out":                   "namespace.rate_out",
			"pulsar_storage_size":               "namespace.storage_size",
			"pulsar_active_connections":         "connections.active",
			"jvm_memory_bytes_used":             "jvm.memory.used",
			"jvm_memory_bytes_max":              "jvm.memory.max",
			"jvm_gc_collection_seconds":         "jvm.gc.collection",
			"process_resident_memory_bytes":     "process.resident_memory",
			"process_cpu_seconds":               "process.cpu_seconds",
		},
		Labels: []string{"namespace", "area"},
	})

	// ScyllaDB 5.x à 2026.x : /metrics sur le port 9180. Séries additionnées sur tous les cœurs
	// (étiquette shard écartée) ; latences du coordinateur en microsecondes.
	register(Profile{
		Name: "scylla", Description: "ScyllaDB", Port: 9180, Namespace: "scylla",
		Metrics: []string{
			"scylla_reactor_utilization", "scylla_node_operation_mode", "scylla_storage_proxy_coordinator_read_latency",
			"scylla_storage_proxy_coordinator_write_latency", "scylla_storage_proxy_coordinator_foreground_reads",
			"scylla_storage_proxy_coordinator_foreground_writes", "scylla_transport_requests_served",
			"scylla_transport_current_connections", "scylla_transport_cql_errors_total", "scylla_cql_reads", "scylla_cql_inserts",
			"scylla_cql_updates", "scylla_cql_deletes", "scylla_cache_row_hits", "scylla_cache_row_misses",
			"scylla_cache_bytes_used", "scylla_compaction_manager_compactions", "scylla_compaction_manager_pending_compactions",
			"scylla_memory_allocated_memory", "scylla_memory_free_memory", "scylla_memory_total_memory",
			"scylla_database_total_reads", "scylla_database_total_writes", "scylla_database_total_reads_failed",
			"scylla_database_total_writes_failed", "scylla_database_total_writes_timedout",
			"scylla_database_requests_blocked_memory_current", "scylla_gossip_live",
		},
		Rename: map[string]string{
			"scylla_reactor_utilization":                         "reactor.utilization",
			"scylla_node_operation_mode":                         "node.operation_mode",
			"scylla_storage_proxy_coordinator_read_latency":      "coordinator.read_latency",
			"scylla_storage_proxy_coordinator_write_latency":     "coordinator.write_latency",
			"scylla_storage_proxy_coordinator_foreground_reads":  "coordinator.foreground_reads",
			"scylla_storage_proxy_coordinator_foreground_writes": "coordinator.foreground_writes",
			"scylla_transport_requests_served":                   "cql.requests_served",
			"scylla_transport_current_connections":               "cql.connections",
			"scylla_transport_cql_errors_total":                  "cql.errors",
			"scylla_cql_reads":                                   "cql.reads",
			"scylla_cql_inserts":                                 "cql.inserts",
			"scylla_cql_updates":                                 "cql.updates",
			"scylla_cql_deletes":                                 "cql.deletes",
			"scylla_cache_row_hits":                              "cache.row_hits",
			"scylla_cache_row_misses":                            "cache.row_misses",
			"scylla_cache_bytes_used":                            "cache.bytes_used",
			"scylla_compaction_manager_compactions":              "compaction.running",
			"scylla_compaction_manager_pending_compactions":      "compaction.pending",
			"scylla_memory_allocated_memory":                     "memory.allocated",
			"scylla_memory_free_memory":                          "memory.free",
			"scylla_memory_total_memory":                         "memory.total",
			"scylla_database_total_reads":                        "database.reads",
			"scylla_database_total_writes":                       "database.writes",
			"scylla_database_total_reads_failed":                 "database.reads_failed",
			"scylla_database_total_writes_failed":                "database.writes_failed",
			"scylla_database_total_writes_timedout":              "database.writes_timedout",
			"scylla_database_requests_blocked_memory_current":    "database.requests_blocked_memory",
			"scylla_gossip_live":                                 "gossip.live",
		},
		Labels: []string{"type"},
	})

	// SonarQube 9.3 et suivants (Community Build 26.x éprouvé) : /api/monitoring/metrics, lu
	// avec l'en-tête X-Sonar-Passcode (sonar.web.systemPasscode) ou le jeton d'un administrateur
	// (bearer_token_file). État du web, du moteur de calcul et d'Elasticsearch, tâches en
	// attente, disque d'Elasticsearch, latence de la base, durée des requêtes de l'API.
	register(Profile{
		Name: "sonarqube", Description: "SonarQube (mesures natives)", Port: 9000, Path: "/api/monitoring/metrics",
		Namespace: "sonarqube",
		Metrics: []string{
			"sonarqube_health_web_status", "sonarqube_health_compute_engine_status", "sonarqube_health_elasticsearch_status",
			"sonarqube_compute_engine_pending_tasks_total", "sonarqube_compute_engine_tasks_running_duration_seconds",
			"sonarqube_compute_engine_system_tasks_running_duration_seconds", "sonarqube_elasticsearch_disk_usage_percent",
			"sonarqube_elasticsearch_disk_space_free_bytes", "sonarqube_elasticsearch_read_only_indices_total",
			"sonarqube_db_latency_seconds", "sonarqube_web_api_v1_request_duration_seconds",
			"sonarqube_web_api_v2_request_duration_seconds", "sonarqube_web_uptime_minutes",
			"sonarqube_number_of_connected_sonarlint_clients", "sonarqube_license_days_before_expiration_total",
		},
		Rename: map[string]string{
			"sonarqube_health_web_status":                                    "health.web",
			"sonarqube_health_compute_engine_status":                         "health.compute_engine",
			"sonarqube_health_elasticsearch_status":                          "health.elasticsearch",
			"sonarqube_compute_engine_pending_tasks_total":                   "compute_engine.pending_tasks",
			"sonarqube_compute_engine_tasks_running_duration_seconds":        "compute_engine.task_duration",
			"sonarqube_compute_engine_system_tasks_running_duration_seconds": "compute_engine.system_task_duration",
			"sonarqube_elasticsearch_disk_usage_percent":                     "elasticsearch.disk_usage",
			"sonarqube_elasticsearch_disk_space_free_bytes":                  "elasticsearch.disk_free",
			"sonarqube_elasticsearch_read_only_indices_total":                "elasticsearch.read_only_indices",
			"sonarqube_db_latency_seconds":                                   "db.latency",
			"sonarqube_web_api_v1_request_duration_seconds":                  "web.api_v1.request_duration",
			"sonarqube_web_api_v2_request_duration_seconds":                  "web.api_v2.request_duration",
			"sonarqube_web_uptime_minutes":                                   "web.uptime_minutes",
			"sonarqube_number_of_connected_sonarlint_clients":                "sonarlint.clients",
			"sonarqube_license_days_before_expiration_total":                 "license.days_before_expiration",
		},
		Labels: []string{"task_type"},
	})

	// GitLab 16 à 19 (Omnibus) : Rails et Puma sur /-/metrics, lu sur Puma lui-même
	// (127.0.0.1:8080) ou derrière nginx ; l'adresse de l'agent doit figurer dans
	// gitlab_rails['monitoring_whitelist']. Requêtes par classe de code, durées (requêtes,
	// transactions, SQL, attente dans la file de Puma), réserve de connexions à la base, Redis,
	// indicateurs SLI des requêtes Rails, Puma et mémoire de Ruby.
	register(Profile{
		Name: "gitlab", Description: "GitLab (Rails et Puma)", Port: 8080, Path: "/-/metrics", Namespace: "gitlab",
		Metrics: []string{
			"http_requests_total", "http_request_duration_seconds", "gitlab_transaction_duration_seconds",
			"gitlab_sql_duration_seconds", "gitlab_rails_queue_duration_seconds", "gitlab_database_connection_pool_busy",
			"gitlab_database_connection_pool_size", "gitlab_database_connection_pool_waiting",
			"gitlab_database_connection_pool_dead", "gitlab_redis_client_requests_total",
			"gitlab_redis_client_exceptions_total", "gitlab_redis_client_requests_duration_seconds",
			"gitlab_sli_rails_request_total", "gitlab_sli_rails_request_error_total", "gitlab_sli_rails_request_apdex_total",
			"gitlab_sli_rails_request_apdex_success_total", "puma_running", "puma_queued_connections", "puma_workers",
			"puma_running_workers", "ruby_process_resident_memory_bytes", "ruby_gc_duration_seconds",
		},
		Rename: map[string]string{
			"http_requests_total":                           "http.requests",
			"http_request_duration_seconds":                 "http.request_duration",
			"gitlab_transaction_duration_seconds":           "transaction.duration",
			"gitlab_sql_duration_seconds":                   "sql.duration",
			"gitlab_rails_queue_duration_seconds":           "rails.queue_duration",
			"gitlab_database_connection_pool_busy":          "db.pool.busy",
			"gitlab_database_connection_pool_size":          "db.pool.size",
			"gitlab_database_connection_pool_waiting":       "db.pool.waiting",
			"gitlab_database_connection_pool_dead":          "db.pool.dead",
			"gitlab_redis_client_requests_total":            "redis.requests",
			"gitlab_redis_client_exceptions_total":          "redis.exceptions",
			"gitlab_redis_client_requests_duration_seconds": "redis.request_duration",
			"gitlab_sli_rails_request_total":                "sli.rails.requests",
			"gitlab_sli_rails_request_error_total":          "sli.rails.errors",
			"gitlab_sli_rails_request_apdex_total":          "sli.rails.apdex_total",
			"gitlab_sli_rails_request_apdex_success_total":  "sli.rails.apdex_success",
			"puma_running":                       "puma.running",
			"puma_queued_connections":            "puma.queued_connections",
			"puma_workers":                       "puma.workers",
			"puma_running_workers":               "puma.running_workers",
			"ruby_process_resident_memory_bytes": "ruby.resident_memory",
			"ruby_gc_duration_seconds":           "ruby.gc.duration",
		},
		Labels: []string{"method", "status", "storage", "db_config_name"},
	})

	// GitLab : exportateur de Sidekiq (sidekiq['metrics_enabled'], 127.0.0.1:8082/metrics).
	// Tâches terminées par issue (done, fail) et leur durée, attente en file, tâches en cours.
	register(Profile{
		Name: "gitlab_sidekiq", Description: "GitLab (Sidekiq)", Port: 8082, Namespace: "gitlab_sidekiq",
		Metrics: []string{
			"sidekiq_jobs_completion_seconds", "sidekiq_jobs_queue_duration_seconds", "sidekiq_running_jobs",
			"sidekiq_concurrency", "sidekiq_enqueued_jobs_total",
		},
		Rename: map[string]string{
			"sidekiq_jobs_completion_seconds":     "jobs.completion",
			"sidekiq_jobs_queue_duration_seconds": "jobs.queue_duration",
			"sidekiq_running_jobs":                "jobs.running",
			"sidekiq_concurrency":                 "concurrency",
			"sidekiq_enqueued_jobs_total":         "jobs.enqueued",
		},
		Labels: []string{"job_status", "queue"},
	})

	// GitLab : Gitaly (stockage Git, prometheus_listen_addr, 127.0.0.1:9236). Appels gRPC par code,
	// git pack-objects et limitation de concurrence (en cours, en file), commandes git en cours.
	register(Profile{
		Name: "gitaly", Description: "GitLab (Gitaly)", Port: 9236, Namespace: "gitaly",
		Metrics: []string{
			"grpc_server_handled_total", "gitaly_pack_objects_in_progress", "gitaly_pack_objects_queued",
			"gitaly_concurrency_limiting_in_progress", "gitaly_concurrency_limiting_queued", "gitaly_commands_running",
			"gitaly_command_cpu_seconds_total", "gitaly_catfile_processes", "process_resident_memory_bytes",
		},
		Rename: map[string]string{
			"grpc_server_handled_total":               "grpc.handled",
			"gitaly_pack_objects_in_progress":         "pack_objects.in_progress",
			"gitaly_pack_objects_queued":              "pack_objects.queued",
			"gitaly_concurrency_limiting_in_progress": "concurrency_limiting.in_progress",
			"gitaly_concurrency_limiting_queued":      "concurrency_limiting.queued",
			"gitaly_commands_running":                 "commands.running",
			"gitaly_command_cpu_seconds_total":        "command.cpu_seconds",
			"gitaly_catfile_processes":                "catfile.processes",
			"process_resident_memory_bytes":           "process.resident_memory",
		},
		Labels: []string{"grpc_code", "mode"},
	})

	// ── Logiciels propres à Kubernetes (l'agent de la grappe lit le service de chacun) ──

	// cert-manager 1.x : contrôleur, :9402/metrics (prometheus.enabled de la charte). État Ready
	// de chaque certificat et émetteur (condition True, False, Unknown), échéance et date de
	// renouvellement prévue (horodatages Unix), synchronisations des contrôleurs.
	register(Profile{
		Name: "cert_manager", Description: "cert-manager (Kubernetes)", Port: 9402, Namespace: "cert_manager",
		Metrics: []string{
			"certmanager_certificate_ready_status", "certmanager_certificate_expiration_timestamp_seconds",
			"certmanager_certificate_renewal_timestamp_seconds", "certmanager_clusterissuer_ready_status",
			"certmanager_issuer_ready_status", "certmanager_controller_sync_call_count", "certmanager_clock_time_seconds_gauge",
		},
		Rename: map[string]string{
			"certmanager_certificate_ready_status":                 "certificate.ready",
			"certmanager_certificate_expiration_timestamp_seconds": "certificate.expiration_timestamp",
			"certmanager_certificate_renewal_timestamp_seconds":    "certificate.renewal_timestamp",
			"certmanager_clusterissuer_ready_status":               "clusterissuer.ready",
			"certmanager_issuer_ready_status":                      "issuer.ready",
			"certmanager_controller_sync_call_count":               "controller.sync_calls",
			"certmanager_clock_time_seconds_gauge":                 "clock_time",
		},
		Labels: []string{"condition", "name", "namespace", "issuer_name", "controller"},
	})

	// Velero 1.x : serveur, :8085/metrics. Sauvegardes tentées, réussies, en échec, partielles ou
	// refusées à la validation, état de la dernière par planification (1 réussie, 0 en échec),
	// emplacement de stockage disponible (1) ou non (0), restaurations.
	register(Profile{
		Name: "velero", Description: "Velero (sauvegardes Kubernetes)", Port: 8085, Namespace: "velero",
		Metrics: []string{
			"velero_backup_attempt_total", "velero_backup_success_total", "velero_backup_failure_total",
			"velero_backup_partial_failure_total", "velero_backup_validation_failure_total", "velero_backup_last_status",
			"velero_backup_last_successful_timestamp", "velero_backup_items_errors", "velero_backup_total",
			"velero_backup_location_status_gauge", "velero_restore_success_total", "velero_restore_failed_total",
			"velero_restore_partial_failure_total",
		},
		Rename: map[string]string{
			"velero_backup_attempt_total":             "backup.attempts",
			"velero_backup_success_total":             "backup.success",
			"velero_backup_failure_total":             "backup.failure",
			"velero_backup_partial_failure_total":     "backup.partial_failure",
			"velero_backup_validation_failure_total":  "backup.validation_failure",
			"velero_backup_last_status":               "backup.last_status",
			"velero_backup_last_successful_timestamp": "backup.last_successful_timestamp",
			"velero_backup_items_errors":              "backup.items_errors",
			"velero_backup_total":                     "backups",
			"velero_backup_location_status_gauge":     "backup_location.available",
			"velero_restore_success_total":            "restore.success",
			"velero_restore_failed_total":             "restore.failed",
			"velero_restore_partial_failure_total":    "restore.partial_failure",
		},
		Labels: []string{"schedule", "backup_location_name"},
	})

	// Argo CD 2.x/3.x : contrôleur d'applications, :8082/metrics. Santé et synchronisation de
	// chaque application (argocd_app_info vaut 1, ses étiquettes portent l'état), synchronisations
	// par issue, réconciliations, connexion aux grappes.
	register(Profile{
		Name: "argocd", Description: "Argo CD (contrôleur d'applications)", Port: 8082, Namespace: "argocd",
		Metrics: []string{
			"argocd_app_info", "argocd_app_sync_total", "argocd_app_reconcile", "argocd_app_orphaned_resources_count",
			"argocd_cluster_connection_status", "argocd_cluster_api_resource_objects", "argocd_cluster_cache_age_seconds",
			"argocd_kubectl_exec_pending", "argocd_redis_request_total",
		},
		Rename: map[string]string{
			"argocd_app_info":                     "app.info",
			"argocd_app_sync_total":               "app.syncs",
			"argocd_app_reconcile":                "app.reconcile",
			"argocd_app_orphaned_resources_count": "app.orphaned_resources",
			"argocd_cluster_connection_status":    "cluster.connection_status",
			"argocd_cluster_api_resource_objects": "cluster.api_resource_objects",
			"argocd_cluster_cache_age_seconds":    "cluster.cache_age",
			"argocd_kubectl_exec_pending":         "kubectl.exec_pending",
			"argocd_redis_request_total":          "redis.requests",
		},
		Labels: []string{"name", "health_status", "sync_status", "phase", "server", "failed"},
	})

	// Argo CD : serveur de dépôts, :8084/metrics. Requêtes git par type et leur durée, demandes
	// en attente.
	register(Profile{
		Name: "argocd_repo_server", Description: "Argo CD (serveur de dépôts)", Port: 8084, Namespace: "argocd_repo_server",
		Metrics: []string{
			"argocd_git_request_total", "argocd_git_request_duration_seconds", "argocd_repo_pending_request_total",
			"argocd_redis_request_total",
		},
		Rename: map[string]string{
			"argocd_git_request_total":            "git.requests",
			"argocd_git_request_duration_seconds": "git.request_duration",
			"argocd_repo_pending_request_total":   "pending_requests",
			"argocd_redis_request_total":          "redis.requests",
		},
		Labels: []string{"request_type", "failed"},
	})

	// Linkerd 2.x : mandataire d'un pod maillé (:4191/metrics), ou Prometheus de linkerd-viz
	// (/federate) pour toute la maille. Requêtes et réponses par sens et par classement, latence
	// des réponses (ms), connexions TCP, certificat d'identité du mandataire.
	register(Profile{
		Name: "linkerd", Description: "Linkerd (mandataire)", Port: 4191, Namespace: "linkerd",
		Metrics: []string{
			"request_total", "response_total", "response_latency_ms", "tcp_open_connections", "tcp_open_total",
			"tcp_close_total", "inbound_http_authz_allow_total", "inbound_http_authz_deny_total",
			"control_identity_cert_expiration_timestamp_seconds", "control_identity_cert_refreshes",
			"process_resident_memory_bytes",
		},
		Rename: map[string]string{
			"request_total":                  "requests",
			"response_total":                 "responses",
			"response_latency_ms":            "response_latency",
			"tcp_open_connections":           "tcp.open_connections",
			"tcp_open_total":                 "tcp.opens",
			"tcp_close_total":                "tcp.closes",
			"inbound_http_authz_allow_total": "inbound.authz_allowed",
			"inbound_http_authz_deny_total":  "inbound.authz_denied",
			"control_identity_cert_expiration_timestamp_seconds": "identity.cert_expiration_timestamp",
			"control_identity_cert_refreshes":                    "identity.cert_refreshes",
			"process_resident_memory_bytes":                      "process.resident_memory",
		},
		Labels: []string{"direction", "classification", "status_code", "result", "namespace", "deployment", "dst_deployment"},
	})

	// KEDA 2.x : opérateur, :8080/metrics (prometheus.operator.enabled de la charte). Erreurs des
	// déclencheurs et des ScaledObject, déclencheurs actifs et leur valeur, latence de la boucle
	// de mise à l'échelle, objets en pause.
	register(Profile{
		Name: "keda", Description: "KEDA (mise à l'échelle Kubernetes)", Port: 8080, Namespace: "keda",
		Metrics: []string{
			"keda_scaler_detail_errors_total", "keda_scaled_object_errors_total", "keda_scaler_active",
			"keda_scaler_metrics_value", "keda_scaler_metrics_latency_seconds", "keda_internal_scale_loop_latency_seconds",
			"keda_scaled_object_paused", "keda_resource_registered_total", "keda_trigger_registered_total",
		},
		Rename: map[string]string{
			"keda_scaler_detail_errors_total":          "scaler.errors",
			"keda_scaled_object_errors_total":          "scaled_object.errors",
			"keda_scaler_active":                       "scaler.active",
			"keda_scaler_metrics_value":                "scaler.metrics_value",
			"keda_scaler_metrics_latency_seconds":      "scaler.metrics_latency",
			"keda_internal_scale_loop_latency_seconds": "scale_loop_latency",
			"keda_scaled_object_paused":                "scaled_object.paused",
			"keda_resource_registered_total":           "resources.registered",
			"keda_trigger_registered_total":            "triggers.registered",
		},
		Labels: []string{"namespace", "scaledObject", "scaler", "type"},
	})
}
