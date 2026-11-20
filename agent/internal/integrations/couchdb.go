package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── CouchDB : API REST du nœud (_node/_local/_stats, _system), tâches actives ───────────────
//
// _stats et _system demandent un compte administrateur du serveur (CouchDB 3). Tant que les
// identifiants ne sont pas posés, la sonde reste inactive.

type couchStat struct {
	Value json.RawMessage `json:"value"`
	Type  string          `json:"type"`
}

type couchHisto struct {
	Mean       float64     `json:"arithmetic_mean"`
	Max        float64     `json:"max"`
	Percentile [][]float64 `json:"percentile"`
}

type CouchDB struct {
	probe
	cfg  config.ServiceProbe
	http *httpProbe
}

func newCouchDB(cfg config.ServiceProbe) *CouchDB {
	return &CouchDB{cfg: cfg, http: newHTTPProbe(cfg.URL, cfg.Username, cfg.Password, cfg.Insecure),
		probe: newProbe("couchdb", "couchdb.can_connect", intent(cfg.Enabled, cfg.Username != ""), "instance", cfg.URL)}
}

func (c *CouchDB) period() time.Duration { return 30 * time.Second }

// flattenCouch parcourt l'arbre de _stats : chemin « couchdb.httpd.requests » → statistique.
func flattenCouch(prefix string, raw json.RawMessage, out map[string]couchStat) {
	var node map[string]json.RawMessage
	if json.Unmarshal(raw, &node) != nil {
		return
	}
	if t, ok := node["type"]; ok {
		if _, ok := node["value"]; ok {
			var s couchStat
			if json.Unmarshal(raw, &s) == nil {
				_ = t
				out[prefix] = s
				return
			}
		}
	}
	for k, v := range node {
		p := k
		if prefix != "" {
			p = prefix + "." + k
		}
		flattenCouch(p, v, out)
	}
}

// Statistiques retenues : chemin dans _stats → métrique ; les compteurs donnent un écart.
var couchStats = []struct{ path, metric, unit string }{
	{"couchdb.httpd.requests", "couchdb.couchdb.httpd.requests", "request"},
	{"couchdb.httpd.bulk_requests", "couchdb.couchdb.httpd.bulk_requests", "request"},
	{"couchdb.httpd.view_reads", "couchdb.couchdb.httpd.view_reads", "read"},
	{"couchdb.httpd.aborted_requests", "couchdb.couchdb.httpd.aborted_requests", "request"},
	{"couchdb.database_reads", "couchdb.couchdb.database_reads", "read"},
	{"couchdb.database_writes", "couchdb.couchdb.database_writes", "write"},
	{"couchdb.open_databases", "couchdb.couchdb.open_databases", "database"},
	{"couchdb.open_os_files", "couchdb.couchdb.open_os_files", "file"},
	{"couchdb.auth_cache_misses", "couchdb.couchdb.auth_cache_misses", "miss"},
	{"couchdb.auth_cache_hits", "couchdb.couchdb.auth_cache_hits", "hit"},
	{"couchdb.document_inserts", "couchdb.couchdb.document_inserts", "document"},
	{"couchdb.document_writes", "couchdb.couchdb.document_writes", "document"},
	{"couchdb.query_server.process_errors", "couchdb.couchdb.query_server.process_errors", "error"},
	{"couch_replicator.jobs.running", "couchdb.couch_replicator.jobs.running", "job"},
	{"couch_replicator.jobs.pending", "couchdb.couch_replicator.jobs.pending", "job"},
	{"couch_replicator.jobs.crashed", "couchdb.couch_replicator.jobs.crashed", "job"},
	{"couch_replicator.jobs.total", "couchdb.couch_replicator.jobs.total", "job"},
	{"mem3.shard_cache.miss", "couchdb.mem3.shard_cache.miss", "miss"},
	{"fabric.read_repairs.failure", "couchdb.fabric.read_repairs.failure", "error"},
}

func (c *CouchDB) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := c.det.skip(now); skip {
		return nil, st
	}
	if c.cfg.Username == "" && c.det.mode == "auto" {
		return nil, "absent" // _stats exige un compte administrateur
	}
	ts := model.Ms(now)
	var welcome struct {
		CouchDB string `json:"couchdb"`
		Version string `json:"version"`
	}
	err := c.http.getJSON(ctx, "/", &welcome)
	if err == nil && welcome.CouchDB == "" {
		err = fmt.Errorf("la réponse ne vient pas de CouchDB")
	}
	var raw json.RawMessage
	if err == nil {
		err = c.http.getJSON(ctx, "/_node/_local/_stats", &raw)
	}
	if err != nil {
		return c.fail(ts, tags, err)
	}
	c.det.ok()
	c.begin()
	defer c.end()
	kv := c.kvs()
	stats := map[string]couchStat{}
	flattenCouch("", raw, stats)
	for _, s := range couchStats {
		st, ok := stats[s.path]
		if !ok {
			continue
		}
		var v float64
		if json.Unmarshal(st.Value, &v) != nil {
			continue
		}
		if st.Type == "counter" && !strings.HasPrefix(s.path, "couch_replicator.jobs.") &&
			s.path != "couchdb.open_databases" && s.path != "couchdb.open_os_files" {
			c.count(b, s.metric, s.unit, s.path, v, kv...)
		} else {
			b.Gauge(s.metric, s.unit, v, kv...)
		}
	}
	if st, ok := stats["couchdb.request_time"]; ok {
		var h couchHisto
		if json.Unmarshal(st.Value, &h) == nil {
			b.Gauge("couchdb.couchdb.request_time.avg", "millisecond", round1(h.Mean), kv...)
			b.Gauge("couchdb.couchdb.request_time.max", "millisecond", round1(h.Max), kv...)
			for _, p := range h.Percentile {
				if len(p) == 2 && (p[0] == 95 || p[0] == 99) {
					name := map[float64]string{95: "couchdb.couchdb.request_time.p95", 99: "couchdb.couchdb.request_time.p99"}[p[0]]
					b.Gauge(name, "millisecond", round1(p[1]), kv...)
				}
			}
		}
	}
	// Codes de réponse par famille (2xx, 3xx, 4xx, 5xx).
	fam := map[string]float64{}
	for k, st := range stats {
		code, ok := strings.CutPrefix(k, "couchdb.httpd_status_codes.")
		if !ok || len(code) != 3 {
			continue
		}
		var v float64
		if json.Unmarshal(st.Value, &v) == nil {
			fam[code[:1]+"xx"] += v
		}
	}
	for f, v := range fam {
		c.count(b, "couchdb.couchdb.httpd_status_codes", "response", "status/"+f, v, c.kvs("status_class", f)...)
	}
	var sys struct {
		Uptime    float64            `json:"uptime"`
		Memory    map[string]float64 `json:"memory"`
		RunQueue  float64            `json:"run_queue"`
		Processes float64            `json:"process_count"`
		MsgQueues map[string]any     `json:"message_queues"`
	}
	if err := c.http.getJSON(ctx, "/_node/_local/_system", &sys); err == nil {
		b.Gauge("couchdb.uptime", "second", sys.Uptime, kv...)
		b.Gauge("couchdb.erlang.run_queue", "process", sys.RunQueue, kv...)
		b.Gauge("couchdb.erlang.processes", "process", sys.Processes, kv...)
		if v, ok := sys.Memory["processes_used"]; ok {
			b.Gauge("couchdb.erlang.memory.processes", "byte", v, kv...)
		}
		var total float64
		for _, v := range sys.Memory {
			total += v
		}
		if total > 0 {
			b.Gauge("couchdb.erlang.memory.total", "byte", total, kv...)
		}
	}
	var tasks []struct {
		Type string `json:"type"`
	}
	if err := c.http.getJSON(ctx, "/_active_tasks", &tasks); err == nil {
		byType := map[string]float64{"indexer": 0, "replication": 0, "database_compaction": 0, "view_compaction": 0}
		for _, t := range tasks {
			byType[t.Type]++
		}
		for t, n := range byType {
			b.Gauge("couchdb.active_tasks", "task", n, c.kvs("task_type", t)...)
		}
	}
	return []model.Check{c.okCheck(ts, tags, "CouchDB "+welcome.Version+" joignable")}, "ok"
}
