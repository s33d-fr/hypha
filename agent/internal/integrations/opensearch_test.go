package integrations

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

func TestOpenSearchClusterAndNode(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "hypha" || p != "bon" {
			w.WriteHeader(401)
			return
		}
		switch {
		case r.URL.Path == "/":
			fmt.Fprint(w, `{"name":"search-01","cluster_name":"search-cluster","version":{"distribution":"opensearch","number":"2.19.7"}}`)
		case r.URL.Path == "/_cluster/health":
			fmt.Fprint(w, `{"cluster_name":"search-cluster","status":"yellow","number_of_nodes":3,"number_of_data_nodes":3,"active_shards":50,
				"active_primary_shards":25,"relocating_shards":0,"initializing_shards":0,"unassigned_shards":3,"number_of_pending_tasks":0,
				"active_shards_percent_as_number":94.3}`)
		case strings.HasPrefix(r.URL.Path, "/_nodes/_local/stats"):
			n++
			fmt.Fprintf(w, `{"nodes":{"abc":{"name":"search-01",
				"jvm":{"mem":{"heap_used_percent":61},"gc":{"collectors":{"young":{"collection_time_in_millis":%d},"old":{"collection_time_in_millis":0}}}},
				"os":{"cpu":{"percent":12},"mem":{"used_percent":80}},
				"fs":{"total":{"total_in_bytes":1000,"available_in_bytes":250}},
				"process":{"open_file_descriptors":100,"max_file_descriptors":1000},
				"indices":{"docs":{"count":42},"store":{"size_in_bytes":4096},
					"indexing":{"index_total":%d,"index_time_in_millis":%d},"search":{"query_total":%d,"query_time_in_millis":%d}},
				"thread_pool":{"write":{"queue":0,"rejected":0},"search":{"queue":2,"rejected":%d}},
				"breakers":{"parent":{"tripped":0}}}}}`, 10*n, 100*n, 200*n, 50*n, 150*n, n)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	o := newOpenSearch(config.OpenSearchConfig{Enabled: "auto", URL: srv.URL, Username: "hypha", Password: "bon"})
	var checks []model.Check
	var b *model.SeriesBuilder
	var st string
	for i := 0; i < 2; i++ {
		b = model.NewSeriesBuilder(0, testTags)
		checks, st = o.collect(context.Background(), time.Now().Add(time.Duration(i)*31*time.Second), b, testTags)
	}
	c := byName(checks)
	if st != "ok" || c["opensearch.cluster_health"].Status != model.CheckWarn || !strings.Contains(c["opensearch.cluster_health"].Message, "3 non attribué") ||
		!strings.Contains(c["opensearch.can_connect"].Message, "OpenSearch 2.19.7") || c["opensearch.cluster_health"].Tags["cluster"] != "search-cluster" {
		t.Fatalf("contrôles : %q %+v", st, checks)
	}
	got := map[string]float64{}
	for _, s := range b.Series() {
		got[s.Metric+"/"+s.Tags["pool"]] = s.Points[0].Value
	}
	want := map[string]float64{
		"opensearch.cluster.shards.unassigned/": 3, "opensearch.jvm.heap.usage/": 61, "opensearch.fs.usage/": 75,
		"opensearch.process.fd.usage/": 10, "opensearch.indexing/": 100, "opensearch.indexing.latency/": 2,
		"opensearch.search.latency/": 3, "opensearch.thread_pool.queue/search": 2, "opensearch.thread_pool.rejected.search/": 1,
		"opensearch.jvm.gc.young_time/": 10,
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s = %v, attendu %v (%v)", k, got[k], v, got)
		}
	}
	// Mauvais mot de passe : CRIT sur la connexion, sans le mot de passe dans le message.
	o = newOpenSearch(config.OpenSearchConfig{Enabled: "auto", URL: srv.URL, Username: "hypha", Password: "mauvais"})
	checks, _ = o.collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags)
	if len(checks) != 1 || checks[0].Status != model.CheckCrit || !strings.Contains(checks[0].Message, "401") || strings.Contains(checks[0].Message, "mauvais") {
		t.Fatalf("401 : %+v", checks)
	}
}

func TestOpenSearchAutoSilentWithoutListener(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "net"), 0o755)
	os.WriteFile(filepath.Join(dir, "net", "tcp"), []byte("  sl  local_address rem_address   st\n"), 0o644)
	t.Setenv("HOST_PROC", dir)
	o := newOpenSearch(config.OpenSearchConfig{Enabled: "auto"})
	checks, st := o.collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags)
	if len(checks) != 0 || st != "absent" {
		t.Fatalf("sans écoute : %q %+v", st, checks)
	}
}

func TestListenAddrs(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "net"), 0o755)
	head := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"
	// 192.0.2.83:9200 en écoute, 127.0.0.1:9300 en écoute, une connexion établie sur 9200 ignorée.
	tcp := head +
		"   0: 530200C0:23F0 00000000:0000 0A 00000000:00000000 00:00000000 00000000   110        0 1 1 0 100 0 0 10 0\n" +
		"   1: 0100007F:2454 00000000:0000 0A 00000000:00000000 00:00000000 00000000   110        0 2 1 0 100 0 0 10 0\n" +
		"   2: 530200C0:23F0 540200C0:D431 01 00000000:00000000 00:00000000 00000000   110        0 3 1 0 100 0 0 10 0\n"
	// 198.51.100.83 vue en IPv6 (::ffff:), puis :: (toutes les adresses).
	tcp6 := head +
		"   0: 0000000000000000FFFF0000536433C6:23F0 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000 110 0 4 1 0 100 0 0 10 0\n" +
		"   1: 00000000000000000000000000000000:23F0 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000 110 0 5 1 0 100 0 0 10 0\n"
	os.WriteFile(filepath.Join(dir, "net", "tcp"), []byte(tcp), 0o644)
	os.WriteFile(filepath.Join(dir, "net", "tcp6"), []byte(tcp6), 0o644)
	t.Setenv("HOST_PROC", dir)
	got := strings.Join(listenAddrs(9200), ",")
	if got != "192.0.2.83,198.51.100.83,127.0.0.1" {
		t.Fatalf("écoutes 9200 : %q", got)
	}
	if got := strings.Join(listenAddrs(9300), ","); got != "127.0.0.1" {
		t.Fatalf("écoutes 9300 : %q", got)
	}
}

// Essai contre une vraie grappe, lancé à la main : OPENSEARCH_REEL=1 et OPENSEARCH_URL (lecture seule).
func TestOpenSearchReel(t *testing.T) {
	if os.Getenv("OPENSEARCH_REEL") != "1" {
		t.Skip("OPENSEARCH_REEL=1 pour interroger une vraie grappe")
	}
	o := newOpenSearch(config.OpenSearchConfig{Enabled: "true", URL: os.Getenv("OPENSEARCH_URL")})
	var checks []model.Check
	var b *model.SeriesBuilder
	var st string
	for i := 0; i < 2; i++ {
		b = model.NewSeriesBuilder(0, testTags)
		checks, st = o.collect(context.Background(), time.Now().Add(time.Duration(i)*31*time.Second), b, testTags)
		time.Sleep(2 * time.Second)
	}
	t.Logf("== %s", st)
	for _, c := range checks {
		t.Logf("[%d] %s : %s", c.Status, c.Name, c.Message)
	}
	for _, s := range b.Series() {
		t.Logf("série %s %s = %v", s.Metric, s.Tags["pool"], s.Points[0].Value)
	}
}
