package omprofiles

// Profils de la vague 7 des intégrations : applications auto-hébergées qui publient leurs mesures
// au format Prometheus. Les noms retenus ont été relevés sur les vrais logiciels, en conteneur
// (versions dans la documentation) : Gitea 28.4 et Forgejo 13.0 (mêmes noms gitea_*), Synapse
// 1.165, Home Assistant 2026.11, Mastodon 4.7 (exportateur prometheus_exporter du web et de
// Sidekiq, /metrics du serveur de streaming). Nextcloud, Jellyfin, Syncthing et Immich sont lus par
// leurs sondes dédiées (integrations/vague7_*.go).

func init() {
	// Gitea (et Forgejo) : [metrics] ENABLED = true dans app.ini (ou GITEA__metrics__ENABLED=true) ;
	// avec [metrics] TOKEN, jeton porteur (bearer_token_file). Compteurs de l'instance (jauges) :
	// dépôts, comptes, tickets ouverts et fermés, organisations, miroirs, tâches de crochets…
	register(Profile{
		Name: "gitea", Description: "Gitea (forge logicielle)", Port: 3000,
		Namespace: "gitea",
		Metrics: []string{
			"gitea_accesses", "gitea_attachments", "gitea_comments", "gitea_follows", "gitea_hooktasks", "gitea_issues",
			"gitea_issues_open", "gitea_issues_closed", "gitea_labels", "gitea_loginsources", "gitea_milestones", "gitea_mirrors",
			"gitea_oauths", "gitea_organizations", "gitea_projects", "gitea_projects_boards", "gitea_publickeys", "gitea_releases",
			"gitea_repositories", "gitea_stars", "gitea_teams", "gitea_updatetasks", "gitea_users", "gitea_watches", "gitea_webhooks",
			"gitea_build_info", "process_resident_memory_bytes", "process_cpu_seconds_total", "process_open_fds", "go_goroutines",
		},
		Rename: map[string]string{
			"gitea_accesses":                "accesses",
			"gitea_attachments":             "attachments",
			"gitea_comments":                "comments",
			"gitea_follows":                 "follows",
			"gitea_hooktasks":               "hook_tasks",
			"gitea_issues":                  "issues",
			"gitea_issues_open":             "issues.open",
			"gitea_issues_closed":           "issues.closed",
			"gitea_labels":                  "labels",
			"gitea_loginsources":            "login_sources",
			"gitea_milestones":              "milestones",
			"gitea_mirrors":                 "mirrors",
			"gitea_oauths":                  "oauth_apps",
			"gitea_organizations":           "organizations",
			"gitea_projects":                "projects",
			"gitea_projects_boards":         "projects.boards",
			"gitea_publickeys":              "public_keys",
			"gitea_releases":                "releases",
			"gitea_repositories":            "repositories",
			"gitea_stars":                   "stars",
			"gitea_teams":                   "teams",
			"gitea_updatetasks":             "update_tasks",
			"gitea_users":                   "users",
			"gitea_watches":                 "watches",
			"gitea_webhooks":                "webhooks",
			"gitea_build_info":              "build_info",
			"process_resident_memory_bytes": "process.resident_memory",
			"process_cpu_seconds_total":     "process.cpu_seconds",
			"process_open_fds":              "process.open_fds",
			"go_goroutines":                 "go.goroutines",
		},
		Labels: []string{"state", "version"},
	})
	// Forgejo publie les mêmes familles gitea_* (vérifié sur Forgejo 13.0.5) : même profil, mêmes
	// noms HYPHA (gitea.*), donc mêmes tableaux et moniteurs ; la version se lit sur gitea.build_info.
	if p, ok := registry["gitea"]; ok {
		p.Name, p.Description = "forgejo", "Forgejo (forge logicielle, noms gitea_* de Gitea)"
		register(p)
	}

	// Matrix Synapse : enable_metrics: true et un écouteur « type: metrics » (port 9000 dans la doc
	// de Synapse), chemin /_synapse/metrics. Requêtes et réponses par servlet, durées (histogramme),
	// événements écrits, retard du traitement des événements, file de la fédération, utilisateurs
	// actifs (MAU, jour), poussées, limitation de débit, mises à jour d'arrière-plan.
	register(Profile{
		Name: "synapse", Description: "Matrix Synapse (serveur de messagerie Matrix)", Port: 9000,
		Path: "/_synapse/metrics", Namespace: "synapse",
		Metrics: []string{
			"synapse_http_server_requests_received_total", "synapse_http_server_responses_total",
			"synapse_http_server_response_time_seconds", "synapse_http_server_in_flight_requests_count",
			"synapse_storage_events_persisted_events_total", "synapse_event_processing_lag",
			"synapse_federation_client_sent_transactions_total", "synapse_federation_server_received_pdus_total",
			"synapse_federation_transaction_queue_pending_pdus", "synapse_federation_transaction_queue_pending_edus",
			"synapse_federation_transaction_queue_pending_destinations", "synapse_federation_known_servers",
			"synapse_federation_server_oldest_inbound_pdu_in_staging", "synapse_federation_server_number_inbound_pdu_in_staging",
			"synapse_admin_mau_current", "synapse_admin_mau_max", "synapse_admin_daily_active_users",
			"synapse_non_deactivated_user_count", "synapse_user_registrations_total", "synapse_user_logins_total",
			"synapse_background_process_in_flight_count", "synapse_storage_schedule_time", "synapse_threadpool_working_threads",
			"synapse_threadpool_max_threads", "synapse_notifier_listeners", "synapse_notifier_rooms", "synapse_notifier_users",
			"synapse_pushers", "synapse_http_httppusher_http_pushes_processed_total", "synapse_http_httppusher_http_pushes_failed_total",
			"synapse_rate_limit_reject_total", "synapse_rate_limit_sleep_total", "synapse_background_update_status",
			"synapse_appservice_api_failed_transactions_total", "synapse_scheduler_running_tasks", "synapse_build_info",
			"process_resident_memory_bytes", "process_cpu_seconds_total", "process_open_fds",
		},
		Rename: map[string]string{
			"synapse_http_server_requests_received_total":               "http.requests",
			"synapse_http_server_responses_total":                       "http.responses",
			"synapse_http_server_response_time_seconds":                 "http.response_time",
			"synapse_http_server_in_flight_requests_count":              "http.in_flight",
			"synapse_storage_events_persisted_events_total":             "events.persisted",
			"synapse_event_processing_lag":                              "event_processing.lag",
			"synapse_federation_client_sent_transactions_total":         "federation.sent_transactions",
			"synapse_federation_server_received_pdus_total":             "federation.received_pdus",
			"synapse_federation_transaction_queue_pending_pdus":         "federation.pending_pdus",
			"synapse_federation_transaction_queue_pending_edus":         "federation.pending_edus",
			"synapse_federation_transaction_queue_pending_destinations": "federation.pending_destinations",
			"synapse_federation_known_servers":                          "federation.known_servers",
			"synapse_federation_server_oldest_inbound_pdu_in_staging":   "federation.oldest_staged_pdu",
			"synapse_federation_server_number_inbound_pdu_in_staging":   "federation.staged_pdus",
			"synapse_admin_mau_current":                                 "users.mau",
			"synapse_admin_mau_max":                                     "users.mau_max",
			"synapse_admin_daily_active_users":                          "users.daily_active",
			"synapse_non_deactivated_user_count":                        "users.non_deactivated",
			"synapse_user_registrations_total":                          "users.registrations",
			"synapse_user_logins_total":                                 "users.logins",
			"synapse_background_process_in_flight_count":                "background_processes.in_flight",
			"synapse_storage_schedule_time":                             "db.schedule_time",
			"synapse_threadpool_working_threads":                        "threadpool.working",
			"synapse_threadpool_max_threads":                            "threadpool.max",
			"synapse_notifier_listeners":                                "notifier.listeners",
			"synapse_notifier_rooms":                                    "notifier.rooms",
			"synapse_notifier_users":                                    "notifier.users",
			"synapse_pushers":                                           "pushers",
			"synapse_http_httppusher_http_pushes_processed_total":       "push.processed",
			"synapse_http_httppusher_http_pushes_failed_total":          "push.failed",
			"synapse_rate_limit_reject_total":                           "rate_limit.rejected",
			"synapse_rate_limit_sleep_total":                            "rate_limit.slept",
			"synapse_background_update_status":                          "background_update.status",
			"synapse_appservice_api_failed_transactions_total":          "appservice.failed_transactions",
			"synapse_scheduler_running_tasks":                           "scheduler.running_tasks",
			"synapse_build_info":                                        "build_info",
			"process_resident_memory_bytes":                             "process.resident_memory",
			"process_cpu_seconds_total":                                 "process.cpu_seconds",
			"process_open_fds":                                          "process.open_fds",
		},
		Labels: []string{"method", "servlet", "code", "name", "version"},
	})

	// Home Assistant : intégration « prometheus: » dans configuration.yaml, /api/prometheus avec un
	// jeton d'accès longue durée (bearer_token_file). Une série par entité : disponibilité,
	// changements d'état, capteurs usuels (température, humidité, batterie, puissance, énergie, CO2),
	// interrupteurs, lumières, thermostats, serrures, mises à jour disponibles. Une maison de
	// plusieurs centaines d'entités dépasse le plafond de 2000 séries : max_series à relever.
	register(Profile{
		Name: "homeassistant", Description: "Home Assistant (domotique)", Port: 8123,
		Path: "/api/prometheus", Namespace: "homeassistant",
		Metrics: []string{
			"homeassistant_entity_available", "homeassistant_state_change_total", "homeassistant_sensor_temperature_celsius",
			"homeassistant_sensor_humidity_percent", "homeassistant_sensor_battery_percent", "homeassistant_sensor_power_w",
			"homeassistant_sensor_energy_kwh", "homeassistant_sensor_carbon_dioxide_ppm", "homeassistant_binary_sensor_state",
			"homeassistant_switch_state", "homeassistant_light_brightness_percent", "homeassistant_climate_current_temperature_celsius",
			"homeassistant_climate_target_temperature_celsius", "homeassistant_climate_action", "homeassistant_cover_position",
			"homeassistant_lock_state", "homeassistant_update_state", "homeassistant_input_boolean_state",
			"homeassistant_alarm_control_panel_state", "homeassistant_device_tracker_state",
			"process_resident_memory_bytes", "process_cpu_seconds_total", "process_open_fds",
		},
		Rename: map[string]string{
			"homeassistant_entity_available":                    "entity.available",
			"homeassistant_state_change_total":                  "entity.state_changes",
			"homeassistant_sensor_temperature_celsius":          "sensor.temperature",
			"homeassistant_sensor_humidity_percent":             "sensor.humidity",
			"homeassistant_sensor_battery_percent":              "sensor.battery",
			"homeassistant_sensor_power_w":                      "sensor.power",
			"homeassistant_sensor_energy_kwh":                   "sensor.energy",
			"homeassistant_sensor_carbon_dioxide_ppm":           "sensor.co2",
			"homeassistant_binary_sensor_state":                 "binary_sensor.state",
			"homeassistant_switch_state":                        "switch.state",
			"homeassistant_light_brightness_percent":            "light.brightness",
			"homeassistant_climate_current_temperature_celsius": "climate.current_temperature",
			"homeassistant_climate_target_temperature_celsius":  "climate.target_temperature",
			"homeassistant_climate_action":                      "climate.action",
			"homeassistant_cover_position":                      "cover.position",
			"homeassistant_lock_state":                          "lock.state",
			"homeassistant_update_state":                        "update.available",
			"homeassistant_input_boolean_state":                 "input_boolean.state",
			"homeassistant_alarm_control_panel_state":           "alarm.state",
			"homeassistant_device_tracker_state":                "device_tracker.state",
			"process_resident_memory_bytes":                     "process.resident_memory",
			"process_cpu_seconds_total":                         "process.cpu_seconds",
			"process_open_fds":                                  "process.open_fds",
		},
		Labels: []string{"entity", "domain", "friendly_name", "action", "state"},
	})

	// Mastodon (vu sur 4.7.6) : MASTODON_PROMETHEUS_EXPORTER_ENABLED=true. Mode local
	// (MASTODON_PROMETHEUS_EXPORTER_LOCAL=true) : le web (Puma) et Sidekiq servent chacun leur
	// /metrics (port 9394 par défaut, un port par service sur une même machine, deux instances de
	// ce profil) ; sinon le processus « prometheus_exporter » les regroupe sur 9394. Puma (fils,
	// file d'attente), requêtes HTTP (avec ..._WEB_DETAILED_METRICS=true), Sidekiq (tâches par file,
	// échecs, files et leur latence avec ..._SIDEKIQ_DETAILED_METRICS=true), pool ActiveRecord.
	register(Profile{
		Name: "mastodon", Description: "Mastodon (web Puma et Sidekiq, exportateur prometheus_exporter)", Port: 9394,
		Namespace: "mastodon",
		Metrics: []string{
			"http_requests_total", "http_request_duration_seconds", "http_request_queue_duration_seconds",
			"http_request_sql_duration_seconds", "http_request_redis_duration_seconds", "puma_workers", "puma_booted_workers",
			"puma_running_threads", "puma_request_backlog", "puma_thread_pool_capacity", "puma_max_threads", "puma_busy_threads",
			"sidekiq_jobs_total", "sidekiq_failed_jobs_total", "sidekiq_dead_jobs_total", "sidekiq_restarted_jobs_total",
			"sidekiq_job_duration_seconds", "sidekiq_queue_backlog", "sidekiq_queue_latency_seconds", "sidekiq_process_busy",
			"sidekiq_process_concurrency", "sidekiq_stats_dead_size", "sidekiq_stats_enqueued", "sidekiq_stats_failed",
			"sidekiq_stats_processed", "sidekiq_stats_processes_size", "sidekiq_stats_retry_size", "sidekiq_stats_scheduled_size",
			"sidekiq_stats_workers_size", "active_record_connection_pool_busy", "active_record_connection_pool_size",
			"active_record_connection_pool_waiting", "active_record_connection_pool_dead", "rss", "heap_live_slots",
			"major_gc_ops_total", "collector_working",
		},
		Rename: map[string]string{
			"http_requests_total":                   "http.requests",
			"http_request_duration_seconds":         "http.request_duration",
			"http_request_queue_duration_seconds":   "http.queue_duration",
			"http_request_sql_duration_seconds":     "http.sql_duration",
			"http_request_redis_duration_seconds":   "http.redis_duration",
			"puma_workers":                          "puma.workers",
			"puma_booted_workers":                   "puma.booted_workers",
			"puma_running_threads":                  "puma.running_threads",
			"puma_request_backlog":                  "puma.request_backlog",
			"puma_thread_pool_capacity":             "puma.thread_pool_capacity",
			"puma_max_threads":                      "puma.max_threads",
			"puma_busy_threads":                     "puma.busy_threads",
			"sidekiq_jobs_total":                    "sidekiq.jobs",
			"sidekiq_failed_jobs_total":             "sidekiq.failed_jobs",
			"sidekiq_dead_jobs_total":               "sidekiq.dead_jobs",
			"sidekiq_restarted_jobs_total":          "sidekiq.restarted_jobs",
			"sidekiq_job_duration_seconds":          "sidekiq.job_duration",
			"sidekiq_queue_backlog":                 "sidekiq.queue.backlog",
			"sidekiq_queue_latency_seconds":         "sidekiq.queue.latency",
			"sidekiq_process_busy":                  "sidekiq.process.busy",
			"sidekiq_process_concurrency":           "sidekiq.process.concurrency",
			"sidekiq_stats_dead_size":               "sidekiq.stats.dead",
			"sidekiq_stats_enqueued":                "sidekiq.stats.enqueued",
			"sidekiq_stats_failed":                  "sidekiq.stats.failed",
			"sidekiq_stats_processed":               "sidekiq.stats.processed",
			"sidekiq_stats_processes_size":          "sidekiq.stats.processes",
			"sidekiq_stats_retry_size":              "sidekiq.stats.retry",
			"sidekiq_stats_scheduled_size":          "sidekiq.stats.scheduled",
			"sidekiq_stats_workers_size":            "sidekiq.stats.workers",
			"active_record_connection_pool_busy":    "db_pool.busy",
			"active_record_connection_pool_size":    "db_pool.size",
			"active_record_connection_pool_waiting": "db_pool.waiting",
			"active_record_connection_pool_dead":    "db_pool.dead",
			"rss":                                   "process.rss",
			"heap_live_slots":                       "ruby.heap_live_slots",
			"major_gc_ops_total":                    "ruby.major_gc",
			"collector_working":                     "collector.working",
		},
		Labels: []string{"status", "queue", "type"},
	})

	// Serveur de streaming de Mastodon (vu sur 4.7.6) : /metrics sur son port (4000), sans réglage.
	// Clients connectés (websocket, eventsource), canaux, messages envoyés, abonnements Redis, pool
	// PostgreSQL, retard de la boucle d'événements de Node.js.
	register(Profile{
		Name: "mastodon_streaming", Description: "Mastodon, serveur de streaming", Port: 4000,
		Namespace: "mastodon_streaming",
		Metrics: []string{
			"connected_clients", "connected_channels", "messages_sent_total", "redis_subscriptions",
			"redis_messages_received_total", "pg_pool_total_connections", "pg_pool_idle_connections", "pg_pool_waiting_queries",
			"nodejs_eventloop_lag_seconds", "nodejs_eventloop_lag_p99_seconds", "process_resident_memory_bytes",
			"process_cpu_seconds_total", "process_open_fds",
		},
		Rename: map[string]string{
			"connected_clients":                "clients",
			"connected_channels":               "channels",
			"messages_sent_total":              "messages_sent",
			"redis_subscriptions":              "redis.subscriptions",
			"redis_messages_received_total":    "redis.messages_received",
			"pg_pool_total_connections":        "pg_pool.total",
			"pg_pool_idle_connections":         "pg_pool.idle",
			"pg_pool_waiting_queries":          "pg_pool.waiting",
			"nodejs_eventloop_lag_seconds":     "eventloop.lag",
			"nodejs_eventloop_lag_p99_seconds": "eventloop.lag_p99",
			"process_resident_memory_bytes":    "process.resident_memory",
			"process_cpu_seconds_total":        "process.cpu_seconds",
			"process_open_fds":                 "process.open_fds",
		},
		Labels: []string{"type"},
	})
}
