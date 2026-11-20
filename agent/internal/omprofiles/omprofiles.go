// Package omprofiles décrit les profils du lecteur OpenMetrics/Prometheus (vague 1 des
// intégrations, 0.5.0) : un profil donne, pour un logiciel qui publie ses mesures au format
// Prometheus, le port et le chemin par défaut, les métriques retenues, leurs nouveaux noms,
// les étiquettes gardées comme tags et l'espace de noms. Une instance de la conf
// (integrations.openmetrics) qui nomme un profil hérite de tout cela et peut le compléter.
//
// Les noms à gauche des renommages sont ceux de l'exposition Prometheus (compteurs avec leur
// suffixe _total, tels qu'ils apparaissent sur /metrics) ; à droite, le nom HYPHA sans
// l'espace de noms, qui est ajouté devant (node + load.1 → node.load.1).
package omprofiles

import "sort"

// Profile : réglages par défaut d'un logiciel.
type Profile struct {
	Name        string
	Description string
	Scheme      string // http par défaut
	Port        int
	Path        string
	Namespace   string
	// Metrics : noms exacts, motifs glob (* et ?) ou expressions « re:… » des familles retenues.
	Metrics []string
	Rename  map[string]string
	// Labels : étiquettes Prometheus gardées comme tags ; les séries qui ne diffèrent que par une
	// étiquette écartée sont additionnées.
	Labels []string
	Tags   map[string]string
}

var registry = map[string]Profile{}

func register(p Profile) {
	if p.Scheme == "" {
		p.Scheme = "http"
	}
	if p.Path == "" {
		p.Path = "/metrics"
	}
	registry[p.Name] = p
}

// Get rend le profil nommé.
func Get(name string) (Profile, bool) {
	p, ok := registry[name]
	return p, ok
}

// Names rend les noms des profils, triés.
func Names() []string {
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func init() {
	// Prometheus node_exporter 1.x (port 9100) : charge, processeur par mode, mémoire, systèmes
	// de fichiers, réseau, disques, descripteurs, températures, horloge. Le processeur est
	// additionné sur tous les cœurs (étiquette cpu écartée) : node.cpu.seconds par mode = nombre
	// de cœurs occupés dans ce mode.
	register(Profile{
		Name: "node_exporter", Description: "Prometheus node_exporter (système Linux)", Port: 9100,
		Namespace: "node",
		Metrics: []string{
			"node_load1", "node_load5", "node_load15", "node_cpu_seconds_total",
			"node_memory_MemTotal_bytes", "node_memory_MemAvailable_bytes", "node_memory_MemFree_bytes",
			"node_memory_Buffers_bytes", "node_memory_Cached_bytes", "node_memory_SwapTotal_bytes", "node_memory_SwapFree_bytes",
			"node_filesystem_size_bytes", "node_filesystem_avail_bytes", "node_filesystem_free_bytes",
			"node_filesystem_files", "node_filesystem_files_free", "node_filesystem_readonly",
			"node_network_receive_bytes_total", "node_network_transmit_bytes_total",
			"node_network_receive_errs_total", "node_network_transmit_errs_total",
			"node_network_receive_drop_total", "node_network_transmit_drop_total",
			"node_disk_read_bytes_total", "node_disk_written_bytes_total", "node_disk_io_time_seconds_total",
			"node_disk_reads_completed_total", "node_disk_writes_completed_total",
			"node_procs_running", "node_procs_blocked", "node_context_switches_total", "node_intr_total",
			"node_filefd_allocated", "node_filefd_maximum", "node_boot_time_seconds",
			"node_hwmon_temp_celsius", "node_timex_offset_seconds", "node_timex_sync_status",
		},
		Rename: map[string]string{
			"node_load1": "load.1", "node_load5": "load.5", "node_load15": "load.15",
			"node_cpu_seconds_total":            "cpu.seconds",
			"node_memory_MemTotal_bytes":        "memory.total",
			"node_memory_MemAvailable_bytes":    "memory.available",
			"node_memory_MemFree_bytes":         "memory.free",
			"node_memory_Buffers_bytes":         "memory.buffers",
			"node_memory_Cached_bytes":          "memory.cached",
			"node_memory_SwapTotal_bytes":       "swap.total",
			"node_memory_SwapFree_bytes":        "swap.free",
			"node_filesystem_size_bytes":        "filesystem.size",
			"node_filesystem_avail_bytes":       "filesystem.avail",
			"node_filesystem_free_bytes":        "filesystem.free",
			"node_filesystem_files":             "filesystem.files",
			"node_filesystem_files_free":        "filesystem.files_free",
			"node_filesystem_readonly":          "filesystem.readonly",
			"node_network_receive_bytes_total":  "network.receive.bytes",
			"node_network_transmit_bytes_total": "network.transmit.bytes",
			"node_network_receive_errs_total":   "network.receive.errors",
			"node_network_transmit_errs_total":  "network.transmit.errors",
			"node_network_receive_drop_total":   "network.receive.drops",
			"node_network_transmit_drop_total":  "network.transmit.drops",
			"node_disk_read_bytes_total":        "disk.read.bytes",
			"node_disk_written_bytes_total":     "disk.written.bytes",
			"node_disk_io_time_seconds_total":   "disk.io_time",
			"node_disk_reads_completed_total":   "disk.reads",
			"node_disk_writes_completed_total":  "disk.writes",
			"node_procs_running":                "procs.running",
			"node_procs_blocked":                "procs.blocked",
			"node_context_switches_total":       "context_switches",
			"node_intr_total":                   "interrupts",
			"node_filefd_allocated":             "filefd.allocated",
			"node_filefd_maximum":               "filefd.maximum",
			"node_boot_time_seconds":            "boot_time",
			"node_hwmon_temp_celsius":           "hwmon.temperature",
			"node_timex_offset_seconds":         "time.offset",
			"node_timex_sync_status":            "time.sync_status",
		},
		Labels: []string{"mode", "device", "mountpoint", "fstype", "chip", "sensor"},
	})

	// Prometheus lui-même (port 9090) : séries en tête de la base, échantillons ajoutés, durée
	// des requêtes HTTP (histogramme), régularité des relevés (résumé), règles en échec,
	// notifications perdues, rechargement de la conf, compactions et WAL.
	register(Profile{
		Name: "prometheus", Description: "Serveur Prometheus (mesures de lui-même)", Port: 9090,
		Namespace: "prometheus",
		Metrics: []string{
			"prometheus_tsdb_head_series", "prometheus_tsdb_head_samples_appended_total",
			"prometheus_tsdb_compactions_failed_total", "prometheus_tsdb_wal_corruptions_total",
			"prometheus_tsdb_storage_blocks_bytes", "prometheus_target_interval_length_seconds",
			"prometheus_http_requests_total", "prometheus_http_request_duration_seconds",
			"prometheus_rule_evaluation_failures_total", "prometheus_rule_group_last_duration_seconds",
			"prometheus_notifications_dropped_total", "prometheus_notifications_queue_length",
			"prometheus_config_last_reload_successful", "prometheus_sd_discovered_targets",
			"prometheus_target_scrapes_exceeded_sample_limit_total", "prometheus_ready",
			"process_resident_memory_bytes", "process_cpu_seconds_total", "process_open_fds", "go_goroutines",
		},
		Rename: map[string]string{
			"prometheus_tsdb_head_series":                           "tsdb.head_series",
			"prometheus_tsdb_head_samples_appended_total":           "tsdb.samples_appended",
			"prometheus_tsdb_compactions_failed_total":              "tsdb.compactions_failed",
			"prometheus_tsdb_wal_corruptions_total":                 "tsdb.wal_corruptions",
			"prometheus_tsdb_storage_blocks_bytes":                  "tsdb.storage_blocks",
			"prometheus_target_interval_length_seconds":             "target.interval_length",
			"prometheus_http_requests_total":                        "http.requests",
			"prometheus_http_request_duration_seconds":              "http.request_duration",
			"prometheus_rule_evaluation_failures_total":             "rule.evaluation_failures",
			"prometheus_rule_group_last_duration_seconds":           "rule.group_duration",
			"prometheus_notifications_dropped_total":                "notifications.dropped",
			"prometheus_notifications_queue_length":                 "notifications.queue_length",
			"prometheus_config_last_reload_successful":              "config.last_reload_successful",
			"prometheus_sd_discovered_targets":                      "sd.discovered_targets",
			"prometheus_target_scrapes_exceeded_sample_limit_total": "target.scrapes_exceeded_sample_limit",
			"prometheus_ready":                                      "ready",
			"process_resident_memory_bytes":                         "process.resident_memory",
			"process_cpu_seconds_total":                             "process.cpu_seconds",
			"process_open_fds":                                      "process.open_fds",
			"go_goroutines":                                         "go.goroutines",
		},
		Labels: []string{"code", "handler", "interval", "config"},
	})
}
