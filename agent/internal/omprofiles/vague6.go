package omprofiles

// Profil de la vague 6 des intégrations : le registre OCI Zot, par son extension « metrics »
// (relevée sur Zot v2.1.23, image complète zot-linux-amd64, en conteneur). Durées en secondes,
// tailles en octets.

func init() {
	// Zot : extensions.metrics.enable: true dans config.json (image complète ; l'image « minimal »
	// n'a pas l'extension et ne sert pas /metrics). Port d'écoute : celui de http.port (5000 dans
	// l'image officielle). Avec l'authentification de Zot, /metrics la demande aussi : en-tête
	// Authorization d'un compte en lecture (headers de l'instance).
	register(Profile{
		Name: "zot", Description: "Registre OCI Zot (extension metrics)", Port: 5000, Namespace: "zot",
		Metrics: []string{
			"zot_http_requests_total", "zot_http_method_latency_seconds", "zot_repo_downloads_total",
			"zot_repo_uploads_total", "zot_repo_storage_bytes", "zot_storage_lock_latency_seconds",
			"zot_gc_duration_seconds", "zot_scheduler_tasksqueue_length", "zot_scheduler_workers",
			"zot_scheduler_workers_total", "zot_scheduler_generators_status",
		},
		Rename: map[string]string{
			"zot_http_requests_total":          "http.requests",
			"zot_http_method_latency_seconds":  "http.latency",
			"zot_repo_downloads_total":         "repo.downloads",
			"zot_repo_uploads_total":           "repo.uploads",
			"zot_repo_storage_bytes":           "repo.storage",
			"zot_storage_lock_latency_seconds": "storage.lock_latency",
			"zot_gc_duration_seconds":          "gc.duration",
			"zot_scheduler_tasksqueue_length":  "scheduler.queue",
			"zot_scheduler_workers":            "scheduler.workers",
			"zot_scheduler_workers_total":      "scheduler.workers_total",
			"zot_scheduler_generators_status":  "scheduler.generators",
		},
		Labels: []string{"code", "method", "repo", "lockType", "priority", "state"},
	})
}
