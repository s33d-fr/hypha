package omprofiles

// Vague 8 des intégrations (réseau, stockage et sauvegardes auto-hébergés) : Caddy et MinIO publient
// leurs mesures au format Prometheus. Pi-hole, AdGuard Home, Unbound, Mosquitto, Proxmox Backup
// Server, Borg et restic ont leurs sondes dédiées (paquet integrations, vague8.go).

func init() {
	// Caddy 2 : mesures sur le point d'administration (port 2019, /metrics, toujours servi) ; celles
	// des serveurs HTTP n'existent qu'avec l'option globale « metrics » du Caddyfile (ou
	// apps.http.metrics en JSON). Requêtes et requêtes en cours par serveur et gestionnaire ; durées,
	// tailles et nombre de réponses par code (histogrammes : .count = réponses, par code) ; erreurs
	// des gestionnaires ; santé de chaque serveur amont du mandataire (1/0) ; dernier rechargement de
	// la configuration. Éprouvé sur Caddy 2.11.9. L'étiquette method est écartée (additionnée).
	register(Profile{
		Name: "caddy", Description: "Caddy (serveur web et mandataire)", Port: 2019,
		Namespace: "caddy",
		Metrics: []string{
			"caddy_http_requests_total", "caddy_http_requests_in_flight", "caddy_http_request_errors_total",
			"caddy_http_request_duration_seconds", "caddy_http_response_duration_seconds",
			"caddy_http_request_size_bytes", "caddy_http_response_size_bytes",
			"caddy_reverse_proxy_upstreams_healthy",
			"caddy_config_last_reload_successful", "caddy_config_last_reload_success_timestamp_seconds",
			"process_resident_memory_bytes", "process_cpu_seconds_total", "process_open_fds", "go_goroutines",
		},
		Rename: map[string]string{
			"caddy_http_requests_total":                         "http.requests",
			"caddy_http_requests_in_flight":                     "http.requests_in_flight",
			"caddy_http_request_errors_total":                   "http.request_errors",
			"caddy_http_request_duration_seconds":               "http.request.duration",
			"caddy_http_response_duration_seconds":              "http.response.duration",
			"caddy_http_request_size_bytes":                     "http.request.size",
			"caddy_http_response_size_bytes":                    "http.response.size",
			"caddy_reverse_proxy_upstreams_healthy":             "reverse_proxy.upstream.healthy",
			"caddy_config_last_reload_successful":               "config.last_reload_successful",
			"caddy_config_last_reload_success_timestamp_seconds": "config.last_reload_success_timestamp",
			"process_resident_memory_bytes":                     "process.resident_memory",
			"process_cpu_seconds_total":                         "process.cpu_seconds",
			"process_open_fds":                                  "process.open_fds",
			"go_goroutines":                                     "go.goroutines",
		},
		Labels: []string{"server", "handler", "code", "upstream"},
	})

	// MinIO : mesures de la grappe (/minio/v2/metrics/cluster, port 9000), lues avec le jeton
	// Prometheus de MinIO (« mc admin prometheus generate <alias> », en bearer_token_file) ou sans
	// jeton si MINIO_PROMETHEUS_AUTH_TYPE=public. Capacité brute et utile, nœuds et disques en ligne
	// ou hors ligne, santé de la grappe et de chaque jeu d'effacement (disques en guérison, quorum),
	// usage (objets, octets, seaux ; publié après le premier passage du scanner), requêtes S3 par API
	// et leurs erreurs, trafic, requêtes refusées, processus des nœuds. Éprouvé sur MinIO
	// RELEASE.2025-10-15T17-29-55Z (construit depuis les sources : plus d'image publique).
	register(Profile{
		Name: "minio", Description: "MinIO (stockage objet S3), grappe", Port: 9000, Path: "/minio/v2/metrics/cluster",
		Namespace: "minio",
		Metrics: []string{
			"minio_cluster_capacity_raw_free_bytes", "minio_cluster_capacity_raw_total_bytes",
			"minio_cluster_capacity_usable_free_bytes", "minio_cluster_capacity_usable_total_bytes",
			"minio_cluster_drive_offline_total", "minio_cluster_drive_online_total", "minio_cluster_drive_total",
			"minio_cluster_nodes_offline_total", "minio_cluster_nodes_online_total",
			"minio_cluster_health_status", "minio_cluster_write_quorum",
			"minio_cluster_health_erasure_set_healing_drives", "minio_cluster_health_erasure_set_online_drives",
			"minio_cluster_health_erasure_set_status", "minio_cluster_health_erasure_set_write_quorum",
			"minio_cluster_health_erasure_set_read_quorum",
			"minio_cluster_usage_object_total", "minio_cluster_usage_total_bytes", "minio_cluster_usage_version_total",
			"minio_cluster_usage_deletemarker_total", "minio_cluster_bucket_total",
			"minio_s3_requests_total", "minio_s3_requests_errors_total", "minio_s3_requests_4xx_errors_total",
			"minio_s3_requests_5xx_errors_total", "minio_s3_requests_inflight_total", "minio_s3_requests_waiting_total",
			"minio_s3_requests_rejected_auth_total", "minio_s3_requests_rejected_invalid_total",
			"minio_s3_requests_rejected_timestamp_total", "minio_s3_requests_rejected_header_total",
			"minio_s3_traffic_received_bytes", "minio_s3_traffic_sent_bytes",
			"minio_heal_objects_errors_total", "minio_heal_objects_heal_total",
			"minio_node_process_uptime_seconds", "minio_node_process_resident_memory_bytes",
			"minio_node_file_descriptor_open_total", "minio_node_file_descriptor_limit_total",
			"minio_notify_events_errors_total", "minio_audit_failed_messages",
		},
		Rename: map[string]string{
			"minio_cluster_capacity_raw_free_bytes":            "cluster.capacity.raw.free",
			"minio_cluster_capacity_raw_total_bytes":           "cluster.capacity.raw.total",
			"minio_cluster_capacity_usable_free_bytes":         "cluster.capacity.usable.free",
			"minio_cluster_capacity_usable_total_bytes":        "cluster.capacity.usable.total",
			"minio_cluster_drive_offline_total":                "cluster.drives.offline",
			"minio_cluster_drive_online_total":                 "cluster.drives.online",
			"minio_cluster_drive_total":                        "cluster.drives.total",
			"minio_cluster_nodes_offline_total":                "cluster.nodes.offline",
			"minio_cluster_nodes_online_total":                 "cluster.nodes.online",
			"minio_cluster_health_status":                      "cluster.health.status",
			"minio_cluster_write_quorum":                       "cluster.write_quorum",
			"minio_cluster_health_erasure_set_healing_drives":  "cluster.erasure_set.healing_drives",
			"minio_cluster_health_erasure_set_online_drives":   "cluster.erasure_set.online_drives",
			"minio_cluster_health_erasure_set_status":          "cluster.erasure_set.status",
			"minio_cluster_health_erasure_set_write_quorum":    "cluster.erasure_set.write_quorum",
			"minio_cluster_health_erasure_set_read_quorum":     "cluster.erasure_set.read_quorum",
			"minio_cluster_usage_object_total":                 "cluster.usage.objects",
			"minio_cluster_usage_total_bytes":                  "cluster.usage.bytes",
			"minio_cluster_usage_version_total":                "cluster.usage.versions",
			"minio_cluster_usage_deletemarker_total":           "cluster.usage.delete_markers",
			"minio_cluster_bucket_total":                       "cluster.buckets",
			"minio_s3_requests_total":                          "s3.requests",
			"minio_s3_requests_errors_total":                   "s3.requests.errors",
			"minio_s3_requests_4xx_errors_total":               "s3.requests.4xx_errors",
			"minio_s3_requests_5xx_errors_total":               "s3.requests.5xx_errors",
			"minio_s3_requests_inflight_total":                 "s3.requests.inflight",
			"minio_s3_requests_waiting_total":                  "s3.requests.waiting",
			"minio_s3_requests_rejected_auth_total":            "s3.requests.rejected.auth",
			"minio_s3_requests_rejected_invalid_total":         "s3.requests.rejected.invalid",
			"minio_s3_requests_rejected_timestamp_total":       "s3.requests.rejected.timestamp",
			"minio_s3_requests_rejected_header_total":          "s3.requests.rejected.header",
			"minio_s3_traffic_received_bytes":                  "s3.traffic.received",
			"minio_s3_traffic_sent_bytes":                      "s3.traffic.sent",
			"minio_heal_objects_errors_total":                  "heal.objects.errors",
			"minio_heal_objects_heal_total":                    "heal.objects.healed",
			"minio_node_process_uptime_seconds":                "node.uptime",
			"minio_node_process_resident_memory_bytes":         "node.process.resident_memory",
			"minio_node_file_descriptor_open_total":            "node.fd.open",
			"minio_node_file_descriptor_limit_total":           "node.fd.limit",
			"minio_notify_events_errors_total":                 "notify.events.errors",
			"minio_audit_failed_messages":                      "audit.failed_messages",
		},
		Labels: []string{"server", "api", "pool", "set"},
	})

	// MinIO, par seau (/minio/v2/metrics/bucket, même jeton) : octets, objets et versions de chaque
	// seau (après le passage du scanner), requêtes et erreurs 4xx par seau, trafic. Un seau = une
	// série par mesure : sur des milliers de seaux, garder le plafond max_series.
	register(Profile{
		Name: "minio_bucket", Description: "MinIO (stockage objet S3), par seau", Port: 9000, Path: "/minio/v2/metrics/bucket",
		Namespace: "minio",
		Metrics: []string{
			"minio_bucket_usage_total_bytes", "minio_bucket_usage_object_total", "minio_bucket_usage_version_total",
			"minio_bucket_requests_total", "minio_bucket_requests_4xx_errors_total", "minio_bucket_requests_5xx_errors_total",
			"minio_bucket_requests_inflight_total", "minio_bucket_traffic_received_bytes", "minio_bucket_traffic_sent_bytes",
			"minio_bucket_quota_total_bytes",
		},
		Rename: map[string]string{
			"minio_bucket_usage_total_bytes":         "bucket.usage.bytes",
			"minio_bucket_usage_object_total":        "bucket.usage.objects",
			"minio_bucket_usage_version_total":       "bucket.usage.versions",
			"minio_bucket_requests_total":            "bucket.requests",
			"minio_bucket_requests_4xx_errors_total": "bucket.requests.4xx_errors",
			"minio_bucket_requests_5xx_errors_total": "bucket.requests.5xx_errors",
			"minio_bucket_requests_inflight_total":   "bucket.requests.inflight",
			"minio_bucket_traffic_received_bytes":    "bucket.traffic.received",
			"minio_bucket_traffic_sent_bytes":        "bucket.traffic.sent",
			"minio_bucket_quota_total_bytes":         "bucket.quota",
		},
		Labels: []string{"bucket", "server"},
	})
}
