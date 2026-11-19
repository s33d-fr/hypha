package integrations

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── OpenSearch et Elasticsearch (§ 41) : API REST, nœud local et santé de la grappe ────────
//
// Chaque agent lit son nœud (_nodes/_local/stats) et la santé de la grappe (_cluster/health),
// que les moniteurs regroupent par nom de grappe. Sans url : l'adresse sur laquelle le port
// 9200 écoute sur la machine (OpenSearch n'écoute souvent que sur l'adresse du réseau).

type OpenSearch struct {
	cfg    config.OpenSearchConfig
	det    detector
	client *http.Client
	url    string
	prev   map[string]float64
}

func newOpenSearch(cfg config.OpenSearchConfig) *OpenSearch {
	mode := cfg.Enabled
	if mode == "auto" && cfg.URL != "" {
		mode = "true"
	}
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: cfg.Insecure}} //nolint:gosec // choisi dans la conf
	return &OpenSearch{cfg: cfg, det: detector{mode: mode}, client: &http.Client{Timeout: 10 * time.Second, Transport: tr}}
}

func (o *OpenSearch) name() string          { return "opensearch" }
func (o *OpenSearch) period() time.Duration { return 30 * time.Second }
func (o *OpenSearch) close()                {}

// target : url de la conf, sinon l'adresse locale où le port 9200 écoute.
func (o *OpenSearch) target() (string, bool) {
	if o.cfg.URL != "" {
		return strings.TrimRight(o.cfg.URL, "/"), true
	}
	if addrs := listenAddrs(9200); len(addrs) > 0 {
		return "http://" + addrs[0] + ":9200", true
	}
	return "", false
}

func (o *OpenSearch) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.url+path, nil)
	if err != nil {
		return err
	}
	if o.cfg.Username != "" {
		req.SetBasicAuth(o.cfg.Username, o.cfg.Password)
	}
	req.Header.Set("User-Agent", "hypha-agent")
	resp, err := o.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s : HTTP %d", path, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(out)
}

type osHealth struct {
	Cluster       string  `json:"cluster_name"`
	Status        string  `json:"status"`
	Nodes         float64 `json:"number_of_nodes"`
	DataNodes     float64 `json:"number_of_data_nodes"`
	ActiveShards  float64 `json:"active_shards"`
	Primary       float64 `json:"active_primary_shards"`
	Relocating    float64 `json:"relocating_shards"`
	Initializing  float64 `json:"initializing_shards"`
	Unassigned    float64 `json:"unassigned_shards"`
	PendingTasks  float64 `json:"number_of_pending_tasks"`
	ActivePercent float64 `json:"active_shards_percent_as_number"`
}

func (o *OpenSearch) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := o.det.skip(now); skip {
		return nil, st
	}
	ts := model.Ms(now)
	url, ok := o.target()
	if !ok {
		const msg = "OpenSearch n'écoute plus sur le port 9200"
		if silent, st := o.det.failed(msg); silent {
			return nil, st
		}
		// Déjà vu sur cette machine : le service s'est arrêté, la dernière adresse porte l'alerte.
		return []model.Check{{TS: ts, Name: "opensearch.can_connect", Status: model.CheckCrit, Message: msg, Tags: tags("instance", o.url)}},
			"erreur : " + msg
	}
	o.url = url
	kv := []string{"instance", url}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var root struct {
		Name    string `json:"name"`
		Cluster string `json:"cluster_name"`
		Version struct {
			Number       string `json:"number"`
			Distribution string `json:"distribution"`
		} `json:"version"`
	}
	if err := o.get(ctx, "/", &root); err != nil {
		if silent, st := o.det.failed(err.Error()); silent {
			return nil, st
		}
		return []model.Check{{TS: ts, Name: "opensearch.can_connect", Status: model.CheckCrit, Message: err.Error(), Tags: tags(kv...)}},
			"erreur : " + err.Error()
	}
	o.det.ok()
	product := "Elasticsearch"
	if root.Version.Distribution == "opensearch" {
		product = "OpenSearch"
	}
	ckv := append(append([]string{}, kv...), "cluster", root.Cluster)
	checks := []model.Check{}
	var h osHealth
	if err := o.get(ctx, "/_cluster/health", &h); err == nil {
		g := func(name, unit string, v float64) { b.Gauge(name, unit, v, ckv...) }
		g("opensearch.cluster.nodes", "node", h.Nodes)
		g("opensearch.cluster.data_nodes", "node", h.DataNodes)
		g("opensearch.cluster.shards.active", "shard", h.ActiveShards)
		g("opensearch.cluster.shards.primary", "shard", h.Primary)
		g("opensearch.cluster.shards.relocating", "shard", h.Relocating)
		g("opensearch.cluster.shards.initializing", "shard", h.Initializing)
		g("opensearch.cluster.shards.unassigned", "shard", h.Unassigned)
		g("opensearch.cluster.pending_tasks", "task", h.PendingTasks)
		g("opensearch.cluster.shards.active_percent", "percent", h.ActivePercent)
		st := map[string]int{"green": model.CheckOK, "yellow": model.CheckWarn, "red": model.CheckCrit}[h.Status]
		if _, known := map[string]bool{"green": true, "yellow": true, "red": true}[h.Status]; !known {
			st = model.CheckUnknown
		}
		msg := fmt.Sprintf("grappe %s %s : %g nœud(s), %g fragment(s) actif(s)", h.Cluster, h.Status, h.Nodes, h.ActiveShards)
		if h.Unassigned > 0 {
			msg += fmt.Sprintf(", %g non attribué(s)", h.Unassigned)
		}
		checks = append(checks, model.Check{TS: ts, Name: "opensearch.cluster_health", Status: st, Message: msg, Tags: tags(ckv...)})
	}
	var ns struct {
		Nodes map[string]json.RawMessage `json:"nodes"`
	}
	if err := o.get(ctx, "/_nodes/_local/stats/jvm,os,fs,indices,thread_pool,breaker,process", &ns); err == nil {
		for _, raw := range ns.Nodes {
			o.nodeSeries(b, raw, append(append([]string{}, ckv...), "node", root.Name))
			break
		}
	}
	checks = append(checks, model.Check{TS: ts, Name: "opensearch.can_connect", Status: model.CheckOK,
		Message: fmt.Sprintf("%s %s joignable (nœud %s, grappe %s)", product, root.Version.Number, root.Name, root.Cluster), Tags: tags(kv...)})
	return checks, "ok"
}

// nodeSeries : mesures du nœud local, débits calculés sur les compteurs cumulés.
func (o *OpenSearch) nodeSeries(b *model.SeriesBuilder, raw json.RawMessage, kv []string) {
	var n map[string]any
	if err := json.Unmarshal(raw, &n); err != nil {
		return
	}
	get := func(keys ...string) (float64, bool) {
		var cur any = n
		for _, k := range keys {
			m, ok := cur.(map[string]any)
			if !ok {
				return 0, false
			}
			cur = m[k]
		}
		f, ok := cur.(float64)
		return f, ok
	}
	g := func(name, unit string, keys ...string) {
		if v, ok := get(keys...); ok {
			b.Gauge(name, unit, v, kv...)
		}
	}
	g("opensearch.jvm.heap.usage", "percent", "jvm", "mem", "heap_used_percent")
	g("opensearch.os.cpu", "percent", "os", "cpu", "percent")
	g("opensearch.os.mem.usage", "percent", "os", "mem", "used_percent")
	g("opensearch.docs", "document", "indices", "docs", "count")
	g("opensearch.store.size", "byte", "indices", "store", "size_in_bytes")
	if tot, ok := get("fs", "total", "total_in_bytes"); ok && tot > 0 {
		if av, ok := get("fs", "total", "available_in_bytes"); ok {
			b.Gauge("opensearch.fs.usage", "percent", round1(100*(tot-av)/tot), kv...)
		}
	}
	if fd, ok := get("process", "open_file_descriptors"); ok {
		if mx, ok := get("process", "max_file_descriptors"); ok && mx > 0 {
			b.Gauge("opensearch.process.fd.usage", "percent", round1(100*fd/mx), kv...)
		}
	}
	for _, pool := range []string{"write", "search"} {
		if v, ok := get("thread_pool", pool, "queue"); ok {
			b.Gauge("opensearch.thread_pool.queue", "task", v, append(append([]string{}, kv...), "pool", pool)...)
		}
	}
	cur := map[string]float64{}
	for k, keys := range map[string][]string{
		"index_total": {"indices", "indexing", "index_total"}, "index_ms": {"indices", "indexing", "index_time_in_millis"},
		"query_total": {"indices", "search", "query_total"}, "query_ms": {"indices", "search", "query_time_in_millis"},
		"gc_young": {"jvm", "gc", "collectors", "young", "collection_time_in_millis"},
		"gc_old": {"jvm", "gc", "collectors", "old", "collection_time_in_millis"},
		"rejected_write": {"thread_pool", "write", "rejected"}, "rejected_search": {"thread_pool", "search", "rejected"},
		"breaker_tripped": {"breakers", "parent", "tripped"},
	} {
		if v, ok := get(keys...); ok {
			cur[k] = v
		}
	}
	if o.prev != nil {
		d := func(k string) (float64, bool) {
			if v, ok := cur[k]; ok {
				return deltaF(v, o.prev[k])
			}
			return 0, false
		}
		if v, ok := d("index_total"); ok {
			b.Add("opensearch.indexing", model.TypeCount, "document", v, kv...)
			if ms, ok := d("index_ms"); ok && v > 0 {
				b.Gauge("opensearch.indexing.latency", "millisecond", round1(ms/v), kv...)
			}
		}
		if v, ok := d("query_total"); ok {
			b.Add("opensearch.search.queries", model.TypeCount, "query", v, kv...)
			if ms, ok := d("query_ms"); ok && v > 0 {
				b.Gauge("opensearch.search.latency", "millisecond", round1(ms/v), kv...)
			}
		}
		for _, x := range []struct{ k, name, unit string }{
			{"gc_young", "opensearch.jvm.gc.young_time", "millisecond"}, {"gc_old", "opensearch.jvm.gc.old_time", "millisecond"},
			{"rejected_write", "opensearch.thread_pool.rejected.write", "task"}, {"rejected_search", "opensearch.thread_pool.rejected.search", "task"},
			{"breaker_tripped", "opensearch.breaker.tripped", "event"},
		} {
			if v, ok := d(x.k); ok {
				b.Add(x.name, model.TypeCount, x.unit, v, kv...)
			}
		}
	}
	o.prev = cur
}
