package openmetrics

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"s33d.fr/hypha/agent/internal/omprofiles"
)

// Profils de la vague 8 : expositions relevées sur Caddy 2.11.9 (un serveur amont arrêté : erreurs et
// réponses 503 présentes) et MinIO RELEASE.2025-10-15T17-29-55Z (grappe et seaux, après le scanner),
// en conteneur dans un environnement d'essai ; adresses remplacées par des adresses de documentation.
func TestVague8ProfilesOnRealExpositions(t *testing.T) {
	cases := []struct {
		profile, file string
		want          []string
	}{
		{"caddy", "caddy-2.11.9.prom", []string{"caddy.http.requests", "caddy.http.requests_in_flight", "caddy.http.request_errors",
			"caddy.http.request.duration.count", "caddy.http.request.duration.p95", "caddy.http.response.size.sum",
			"caddy.reverse_proxy.upstream.healthy", "caddy.config.last_reload_successful",
			"caddy.config.last_reload_success_timestamp", "caddy.process.resident_memory", "caddy.go.goroutines"}},
		{"minio", "minio-2025-10-15.prom", []string{"minio.cluster.capacity.usable.free", "minio.cluster.capacity.usable.total",
			"minio.cluster.drives.online", "minio.cluster.drives.offline", "minio.cluster.nodes.online", "minio.cluster.health.status",
			"minio.cluster.erasure_set.healing_drives", "minio.cluster.erasure_set.status", "minio.cluster.usage.objects",
			"minio.cluster.usage.bytes", "minio.cluster.buckets", "minio.s3.requests", "minio.s3.requests.errors",
			"minio.s3.traffic.received", "minio.node.uptime"}},
		{"minio_bucket", "minio_bucket-2025-10-15.prom", []string{"minio.bucket.usage.bytes", "minio.bucket.usage.objects",
			"minio.bucket.requests", "minio.bucket.traffic.received"}},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			prof, ok := omprofiles.Get(tc.profile)
			if !ok {
				t.Fatalf("profil %s absent", tc.profile)
			}
			raw, err := os.ReadFile(filepath.Join("testdata", "vague8", tc.file))
			if err != nil {
				t.Fatal(err)
			}
			cfg := profileInstance(t, "integrations:\n  openmetrics:\n    - profile: "+tc.profile+"\n")
			c := newConverter(cfg)
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
			for _, w := range tc.want {
				if len(by[w]) == 0 {
					t.Errorf("%s absente ; reçues : %v", w, names(by))
				}
			}
			for n := range by {
				if !strings.HasPrefix(n, prof.Namespace+".") {
					t.Errorf("%s hors de l'espace de noms %s", n, prof.Namespace)
				}
			}
			// Santé des amonts de Caddy : une série par serveur amont, étiquette upstream gardée.
			if tc.profile == "caddy" {
				ups := map[string]bool{}
				for _, p := range by["caddy.reverse_proxy.upstream.healthy"] {
					ups[kvOf(p)["upstream"]] = true
				}
				if !ups["origine:80"] || !ups["192.0.2.250:80"] {
					t.Errorf("amonts : %v", ups)
				}
				for _, p := range by["caddy.http.request.duration.count"] {
					if kv := kvOf(p); kv["code"] == "" || kv["method"] != "" {
						t.Errorf("étiquettes de la durée : %v", p.kv)
					}
				}
			}
		})
	}
}

func kvOf(p point) map[string]string {
	m := map[string]string{}
	for i := 0; i+1 < len(p.kv); i += 2 {
		m[p.kv[i]] = p.kv[i+1]
	}
	return m
}
