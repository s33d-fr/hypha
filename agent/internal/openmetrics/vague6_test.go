package openmetrics

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Profil zot : exposition relevée sur Zot v2.1.23 (image complète, extension metrics) le 21/11/2026,
// après l'envoi et le téléchargement d'une image (testdata/vague6/zot.prom, familles zot_ seules).
func TestZotProfileOnRealExposition(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "vague6", "zot.prom"))
	if err != nil {
		t.Fatal(err)
	}
	c := newConverter(profileInstance(t, "integrations:\n  openmetrics:\n    - profile: zot\n"))
	fams, bad, err := Parse(strings.NewReader(string(raw)))
	if err != nil || bad > 0 || len(fams) == 0 {
		t.Fatalf("exposition illisible (%d lignes refusées, %v)", bad, err)
	}
	t0 := time.Unix(1_800_000_000, 0)
	c.Convert(fams, t0)
	fams, _, _ = Parse(strings.NewReader(bump(string(raw), 10)))
	by := map[string][]point{}
	for _, p := range c.Convert(fams, t0.Add(10*time.Second)) {
		by[p.name] = append(by[p.name], p)
	}
	for _, w := range []string{"zot.http.requests", "zot.http.latency.p95", "zot.repo.downloads", "zot.repo.uploads",
		"zot.repo.storage", "zot.storage.lock_latency.p95", "zot.scheduler.queue", "zot.scheduler.workers",
		"zot.scheduler.workers_total", "zot.gc.duration.count"} {
		if len(by[w]) == 0 {
			t.Errorf("%s absente ; reçues : %v", w, names(by))
		}
	}
	label := func(p point, k string) string {
		for i := 0; i+1 < len(p.kv); i += 2 {
			if p.kv[i] == k {
				return p.kv[i+1]
			}
		}
		return ""
	}
	for _, p := range by["zot.repo.storage"] {
		if label(p, "repo") != "essai/memcached" {
			t.Errorf("étiquette repo : %v", p.kv)
		}
	}
	for _, p := range by["zot.storage.lock_latency.p95"] {
		if label(p, "locktype") == "" {
			t.Errorf("étiquette locktype : %v", p.kv)
		}
	}
	if len(by["zot.http.requests"]) < 5 {
		t.Errorf("requêtes par code et méthode : %d séries", len(by["zot.http.requests"]))
	}
}
