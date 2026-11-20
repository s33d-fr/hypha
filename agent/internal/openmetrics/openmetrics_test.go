package openmetrics

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// Extrait réel de node_exporter 1.8 (format texte 0.0.4), raccourci.
const nodeExp = `# HELP node_cpu_seconds_total Seconds the CPUs spent in each mode.
# TYPE node_cpu_seconds_total counter
node_cpu_seconds_total{cpu="0",mode="idle"} 1000
node_cpu_seconds_total{cpu="0",mode="user"} 100
node_cpu_seconds_total{cpu="1",mode="idle"} 2000
node_cpu_seconds_total{cpu="1",mode="user"} 50
# HELP node_load1 1m load average.
# TYPE node_load1 gauge
node_load1 0.42
# TYPE node_filesystem_avail_bytes gauge
node_filesystem_avail_bytes{device="/dev/sda1",fstype="ext4",mountpoint="/"} 1.2e+10
node_filesystem_avail_bytes{device="tmpfs",fstype="tmpfs",mountpoint="/run"} 4e+08
# TYPE node_network_receive_bytes_total counter
node_network_receive_bytes_total{device="eth0"} 5000
# TYPE node_exporter_build_info gauge
node_exporter_build_info{branch="HEAD",goversion="go1.22",revision="x",version="1.8.2"} 1
# TYPE go_gc_duration_seconds summary
go_gc_duration_seconds{quantile="0"} 1e-05
go_gc_duration_seconds{quantile="0.5"} 2e-05
go_gc_duration_seconds{quantile="1"} 0.001
go_gc_duration_seconds_sum 0.5
go_gc_duration_seconds_count 100
`

const omExp = `# TYPE http_requests counter
# HELP http_requests Requêtes.
http_requests_total{code="200",path="/a"} 10 # {trace_id="abc"} 1 1700000000
http_requests_created{code="200",path="/a"} 1700000000
http_requests_total{code="500",path="/a"} 2
# TYPE req_duration_seconds histogram
# UNIT req_duration_seconds seconds
req_duration_seconds_bucket{le="0.1"} 10
req_duration_seconds_bucket{le="0.5"} 20
req_duration_seconds_bucket{le="1"} 30
req_duration_seconds_bucket{le="+Inf"} 40
req_duration_seconds_sum 25
req_duration_seconds_count 40
# TYPE label_escape gauge
label_escape{v="a\"b\\c\nd",w="x,y"} 3
broken line here
# EOF
`

func TestParseFormats(t *testing.T) {
	fams, bad, err := Parse(strings.NewReader(omExp))
	if err != nil || bad != 1 {
		t.Fatalf("err=%v bad=%d", err, bad)
	}
	by := map[string]*Family{}
	for _, f := range fams {
		by[f.Name] = f
	}
	if f := by["http_requests"]; f == nil || f.Type != "counter" || len(f.Samples) != 3 {
		t.Fatalf("http_requests : %+v", f)
	}
	if f := by["req_duration_seconds"]; f.Type != "histogram" || f.Unit != "seconds" || len(f.Samples) != 6 {
		t.Fatalf("histogramme : %+v", f)
	}
	s := by["label_escape"].Samples[0]
	if v, _ := s.Label("v"); v != "a\"b\\c\nd" {
		t.Fatalf("échappements : %q", v)
	}
	if v, _ := s.Label("w"); v != "x,y" {
		t.Fatalf("virgule dans une valeur : %q", v)
	}
	if _, ok := parseSample(`x{a="1"} NaN`); !ok {
		t.Fatal("NaN refusé")
	}
	if s, ok := parseSample(`x +Inf 1700000000000`); !ok || !math.IsInf(s.Value, 1) {
		t.Fatal("+Inf avec horodatage refusé")
	}
	for _, l := range []string{`x{a="1"`, `x{a=1} 2`, `1x 2`, `x`, `x{} abc`} {
		if _, ok := parseSample(l); ok {
			t.Fatalf("ligne invalide acceptée : %q", l)
		}
	}
}

func find(points []point, name string, kv ...string) (point, bool) {
	for _, p := range points {
		if p.name != name || len(p.kv) != len(kv) {
			continue
		}
		same := true
		for i := range kv {
			if p.kv[i] != kv[i] {
				same = false
			}
		}
		if same {
			return p, true
		}
	}
	return point{}, false
}

func profileInstance(t *testing.T, yaml string) config.OpenMetricsInstance {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "hypha.yaml")
	if err := os.WriteFile(p, []byte("api_key: hph_"+strings.Repeat("a", 40)+"\n"+yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if errs := c.Validate(true); len(errs) > 0 {
		t.Fatalf("conf refusée : %v", errs)
	}
	return c.Integrations.OpenMetrics[0]
}

func TestNodeExporterProfile(t *testing.T) {
	cfg := profileInstance(t, "integrations:\n  openmetrics:\n    - profile: node_exporter\n")
	if cfg.Name != "node_exporter" || cfg.URL != "http://127.0.0.1:9100/metrics" || cfg.Namespace != "node" {
		t.Fatalf("profil mal appliqué : %+v", cfg)
	}
	c := newConverter(cfg)
	fams, _, _ := Parse(strings.NewReader(nodeExp))
	t0 := time.Unix(1000, 0)
	first := c.Convert(fams, t0)
	if p, ok := find(first, "node.load.1"); !ok || p.value != 0.42 {
		t.Fatalf("node.load.1 absent : %+v", first)
	}
	if _, ok := find(first, "node.cpu.seconds", "mode", "idle"); ok {
		t.Fatal("un compteur ne donne pas de débit à la première lecture")
	}
	if _, ok := find(first, "node.filesystem.avail", "device", "/dev/sda1", "fstype", "ext4", "mountpoint", "/"); !ok {
		t.Fatalf("système de fichiers absent : %+v", first)
	}
	for _, p := range first {
		if strings.Contains(p.name, "build_info") || strings.Contains(p.name, "gc_duration") {
			t.Fatalf("famille hors du profil envoyée : %s", p.name)
		}
	}
	second := strings.NewReplacer(`mode="idle"} 1000`, `mode="idle"} 1010`, `mode="idle"} 2000`, `mode="idle"} 2010`,
		`receive_bytes_total{device="eth0"} 5000`, `receive_bytes_total{device="eth0"} 15000`).Replace(nodeExp)
	fams, _, _ = Parse(strings.NewReader(second))
	pts := c.Convert(fams, t0.Add(10*time.Second))
	// cpu écarté : idle additionné sur les deux cœurs, (1010-1000 + 2010-2000) / 10 s = 2 cœurs.
	if p, ok := find(pts, "node.cpu.seconds", "mode", "idle"); !ok || p.value != 2 || p.unit != "" {
		t.Fatalf("débit du processeur : %+v", p)
	}
	if p, ok := find(pts, "node.cpu.seconds", "mode", "user"); !ok || p.value != 0 {
		t.Fatalf("débit user : %+v", p)
	}
	if p, ok := find(pts, "node.network.receive.bytes", "device", "eth0"); !ok || p.value != 1000 || p.unit != "byte_per_second" {
		t.Fatalf("débit réseau : %+v", p)
	}
	// Compteur remis à zéro : pas de débit négatif.
	reset := strings.Replace(second, `receive_bytes_total{device="eth0"} 15000`, `receive_bytes_total{device="eth0"} 10`, 1)
	fams, _, _ = Parse(strings.NewReader(reset))
	pts = c.Convert(fams, t0.Add(20*time.Second))
	if _, ok := find(pts, "node.network.receive.bytes", "device", "eth0"); ok {
		t.Fatal("débit envoyé après une remise à zéro")
	}
}

func TestHistogramSummaryAndCounters(t *testing.T) {
	cfg := config.OpenMetricsInstance{Name: "app", Namespace: "app", Labels: []string{"code"}, MaxSeries: 100,
		Rename: map[string]string{"http_requests_total": "requests"}}
	c := newConverter(cfg)
	fams, _, _ := Parse(strings.NewReader(omExp))
	t0 := time.Unix(0, 0)
	c.Convert(fams, t0)
	next := strings.NewReplacer(
		`le="0.1"} 10`, `le="0.1"} 60`, `le="0.5"} 20`, `le="0.5"} 120`, `le="1"} 30`, `le="1"} 130`,
		`le="+Inf"} 40`, `le="+Inf"} 140`, "seconds_sum 25", "seconds_sum 45", "seconds_count 40", "seconds_count 140",
		`path="/a"} 10 #`, `path="/a"} 30 #`).Replace(omExp)
	fams, _, _ = Parse(strings.NewReader(next))
	pts := c.Convert(fams, t0.Add(10*time.Second))
	if p, ok := find(pts, "app.requests", "code", "200"); !ok || p.value != 2 {
		t.Fatalf("renommage ou débit du compteur : %+v %v", p, pts)
	}
	if p, ok := find(pts, "app.req_duration_seconds.count"); !ok || p.value != 10 {
		t.Fatalf("histogramme .count : %+v", p)
	}
	if p, ok := find(pts, "app.req_duration_seconds.avg"); !ok || math.Abs(p.value-0.2) > 1e-9 || p.unit != "second" {
		t.Fatalf("histogramme .avg : %+v", p)
	}
	// 100 observations de plus : 50 sous 0,1 s, 50 entre 0,1 et 0,5 s ; p50 = 0,1, p95 = 0,1 + 0,4 × 45/50.
	if p, ok := find(pts, "app.req_duration_seconds.p50"); !ok || math.Abs(p.value-0.1) > 1e-9 {
		t.Fatalf("p50 : %+v", p)
	}
	if p, ok := find(pts, "app.req_duration_seconds.p95"); !ok || math.Abs(p.value-0.46) > 1e-9 {
		t.Fatalf("p95 : %+v", p)
	}
	if _, ok := find(pts, "app.label_escape"); !ok {
		t.Fatal("jauge sans étiquette gardée absente")
	}
	if v, ok := bucketQuantile(0.99, map[float64]float64{1: 0, 2: 0, math.Inf(1): 10}); !ok || v != 2 {
		t.Fatalf("centile dans le seau +Inf : %v", v)
	}

	sc := newConverter(config.OpenMetricsInstance{Name: "go", Namespace: "go", MaxSeries: 100})
	fams, _, _ = Parse(strings.NewReader(nodeExp))
	pts = sc.Convert(fams, t0)
	if p, ok := find(pts, "go.go_gc_duration_seconds.p50"); !ok || p.value != 2e-05 {
		t.Fatalf("résumé p50 : %+v", p)
	}
	if _, ok := find(pts, "go.go_gc_duration_seconds.p100"); !ok {
		t.Fatal("résumé quantile 1 absent")
	}
}

func TestSelectionAndLabels(t *testing.T) {
	c := newConverter(config.OpenMetricsInstance{Name: "x", Namespace: "x", MaxSeries: 100,
		Metrics: []string{"node_*", "re:go_.*"}, ExcludeMetrics: []string{"node_exporter_build_info"}, Labels: []string{"*"}})
	fams, _, _ := Parse(strings.NewReader(nodeExp))
	pts := c.Convert(fams, time.Unix(0, 0))
	if _, ok := find(pts, "x.node_load1"); !ok {
		t.Fatal("motif glob")
	}
	for _, p := range pts {
		if strings.Contains(p.name, "build_info") {
			t.Fatal("exclusion ignorée")
		}
	}
	if _, ok := find(pts, "x.go_gc_duration_seconds.p50"); !ok {
		t.Fatal("expression régulière")
	}
	if _, ok := find(pts, "x.node_filesystem_avail_bytes", "device", "tmpfs", "fstype", "tmpfs", "mountpoint", "/run"); !ok {
		t.Fatal("labels: [*] garde toutes les étiquettes")
	}
	if k := tagKey("Host"); k != "exported_host" {
		t.Fatalf("étiquette réservée : %s", k)
	}
}

func TestScrapeEndToEnd(t *testing.T) {
	var calls atomic.Int32
	var auth atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		auth.Store(r.Header.Get("Authorization"))
		if r.URL.Path == "/panne" {
			http.Error(w, "non", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		w.Write([]byte(nodeExp))
	}))
	defer srv.Close()
	tok := filepath.Join(t.TempDir(), "jeton")
	os.WriteFile(tok, []byte("secret-de-test\n"), 0o600)
	base := func(kv ...string) map[string]string {
		m := map[string]string{"host": "essai"}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	cfg := config.OpenMetricsInstance{Name: "cible", Namespace: "cible", URL: srv.URL + "/metrics", Timeout: config.Duration(2 * time.Second),
		Interval: config.Duration(10 * time.Second), MaxSeries: 3, BearerTokenFile: tok, Labels: []string{"mode"}}
	var states []string
	in := newInstance(cfg, Options{Version: "0.5.0", Tags: base, SetStatus: func(_, s string) { states = append(states, s) }})
	series, checks := in.Scrape(context.Background(), time.Now())
	if auth.Load() != "Bearer secret-de-test" {
		t.Fatalf("jeton non envoyé : %v", auth.Load())
	}
	if len(checks) != 1 || checks[0].Status != model.CheckOK || checks[0].Tags[config.InstanceTag] != "cible" {
		t.Fatalf("contrôle : %+v", checks)
	}
	var kept, dropped float64 = -1, -1
	for _, s := range series {
		switch s.Metric {
		case "openmetrics.series":
			kept = s.Points[0].Value
		case "openmetrics.series.dropped":
			dropped = s.Points[0].Value
		}
		if s.Tags["host"] != "essai" || s.Tags["endpoint"] != srv.URL+"/metrics" {
			t.Fatalf("étiquettes : %v", s.Tags)
		}
	}
	if kept != 3 || dropped <= 0 || !strings.HasPrefix(states[len(states)-1], "partiel") {
		t.Fatalf("plafond : gardées %v, écartées %v, état %v", kept, dropped, states)
	}
	cfg.URL = srv.URL + "/panne"
	in = newInstance(cfg, Options{Version: "0.5.0", Tags: base, SetStatus: func(_, s string) { states = append(states, s) }})
	series, checks = in.Scrape(context.Background(), time.Now())
	if len(series) != 0 || checks[0].Status != model.CheckCrit || !strings.Contains(checks[0].Message, "HTTP 500") {
		t.Fatalf("cible en panne : %+v", checks)
	}
}

func TestConfigValidation(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "hypha.yaml")
	os.WriteFile(p, []byte("api_key: hph_"+strings.Repeat("a", 40)+`
integrations:
  openmetrics:
    - {profile: inconnu}
    - {name: a, url: "ftp://x"}
    - {name: a, url: "http://u:p@h:1/m", metrics: ["re:("]}
`), 0o600)
	c, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	errs := strings.Join(c.Validate(true), "\n")
	for _, want := range []string{"profil \"inconnu\" inconnu", "url invalide", "name en double", "identifiants dans l'url", "motif de métrique"} {
		if !strings.Contains(errs, want) {
			t.Errorf("erreur attendue %q dans :\n%s", want, errs)
		}
	}
}
