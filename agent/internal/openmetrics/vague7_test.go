package openmetrics

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"s33d.fr/hypha/agent/internal/omprofiles"
)

// Profils de la vague 7 (applications auto-hébergées) : chaque jeu d'essai (testdata/vague7/<fichier>.prom)
// est l'exposition complète relevée sur le vrai logiciel en conteneur (Gitea 28.4.0, Forgejo 13.0.5,
// Synapse 1.165.0, Home Assistant 2026.11.2 avec l'intégration demo, Mastodon 4.7.6 : web, Sidekiq et
// streaming). Le second relevé ajoute 10 à chaque valeur (compteurs, histogrammes et résumés en débits).

func vague7Points(t *testing.T, profile, file string) map[string][]point {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "vague7", file))
	if err != nil {
		t.Fatal(err)
	}
	cfg := profileInstance(t, "integrations:\n  openmetrics:\n    - profile: "+profile+"\n")
	c := newConverter(cfg)
	fams, bad, err := Parse(strings.NewReader(string(raw)))
	if err != nil || bad > 0 || len(fams) == 0 {
		t.Fatalf("%s : exposition illisible (%d lignes refusées, %v)", file, bad, err)
	}
	t0 := time.Unix(1_800_000_000, 0)
	c.Convert(fams, t0)
	fams, _, _ = Parse(strings.NewReader(bump(string(raw), 10)))
	pts := c.Convert(fams, t0.Add(10*time.Second))
	by := map[string][]point{}
	for _, p := range pts {
		by[p.name] = append(by[p.name], p)
	}
	return by
}

func TestVague7ProfilesOnRealExpositions(t *testing.T) {
	cases := []struct {
		profile, file string
		want          []string
	}{
		{"gitea", "gitea.prom", []string{"gitea.repositories", "gitea.users", "gitea.issues.open", "gitea.issues.closed",
			"gitea.organizations", "gitea.mirrors", "gitea.hook_tasks", "gitea.build_info", "gitea.process.resident_memory",
			"gitea.go.goroutines"}},
		{"forgejo", "forgejo.prom", []string{"gitea.repositories", "gitea.users", "gitea.issues.open", "gitea.build_info"}},
		{"synapse", "synapse.prom", []string{"synapse.http.requests", "synapse.http.responses", "synapse.http.response_time.p95",
			"synapse.http.response_time.count", "synapse.events.persisted", "synapse.users.registrations", "synapse.users.logins",
			"synapse.users.mau", "synapse.users.daily_active", "synapse.federation.pending_pdus", "synapse.db.schedule_time.p99",
			"synapse.background_update.status", "synapse.build_info", "synapse.process.resident_memory", "synapse.notifier.rooms"}},
		{"homeassistant", "homeassistant.prom", []string{"homeassistant.entity.available", "homeassistant.entity.state_changes",
			"homeassistant.sensor.temperature", "homeassistant.sensor.battery", "homeassistant.sensor.power",
			"homeassistant.switch.state", "homeassistant.light.brightness", "homeassistant.climate.current_temperature",
			"homeassistant.climate.action", "homeassistant.lock.state", "homeassistant.update.available",
			"homeassistant.process.resident_memory"}},
		{"mastodon", "mastodon_web.prom", []string{"mastodon.http.requests", "mastodon.http.request_duration.avg",
			"mastodon.http.request_duration.p99", "mastodon.http.sql_duration.avg", "mastodon.puma.running_threads", "mastodon.puma.request_backlog",
			"mastodon.puma.thread_pool_capacity", "mastodon.collector.working"}},
		{"mastodon", "mastodon_sidekiq.prom", []string{"mastodon.sidekiq.jobs", "mastodon.sidekiq.job_duration.avg",
			"mastodon.sidekiq.queue.backlog", "mastodon.sidekiq.queue.latency", "mastodon.sidekiq.stats.enqueued",
			"mastodon.sidekiq.stats.retry", "mastodon.sidekiq.stats.dead", "mastodon.sidekiq.process.busy", "mastodon.db_pool.busy",
			"mastodon.process.rss"}},
		{"mastodon_streaming", "mastodon_streaming.prom", []string{"mastodon_streaming.clients", "mastodon_streaming.channels",
			"mastodon_streaming.messages_sent", "mastodon_streaming.redis.subscriptions", "mastodon_streaming.pg_pool.total",
			"mastodon_streaming.eventloop.lag", "mastodon_streaming.process.resident_memory"}},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			prof, _ := omprofiles.Get(tc.profile)
			by := vague7Points(t, tc.profile, tc.file)
			for _, w := range tc.want {
				if len(by[w]) == 0 {
					t.Errorf("%s absente ; reçues : %v", w, names(by))
				}
			}
			renamed := map[string]bool{}
			for _, v := range prof.Rename {
				renamed[v] = true
			}
			for n, ps := range by {
				base, ok := strings.CutPrefix(n, prof.Namespace+".")
				if !ok {
					t.Errorf("%s hors de l'espace de noms %s", n, prof.Namespace)
				}
				if i := strings.LastIndexByte(base, '.'); !renamed[base] && i > 0 && suffixRe.MatchString(base[i+1:]) {
					base = base[:i]
				}
				if !renamed[base] {
					t.Errorf("nom Prometheus non renommé : %s", n)
				}
				for _, p := range ps {
					if math.IsNaN(p.value) || math.IsInf(p.value, 0) {
						t.Errorf("%s : valeur %v", n, p.value)
					}
				}
			}
		})
	}
}

// Étiquettes gardées : état des comptes Gitea, entité de Home Assistant, file de Sidekiq, type de
// client du streaming ; les autres sont additionnées (job_name de Sidekiq, server_name de Synapse).
func TestVague7Labels(t *testing.T) {
	tag := func(p point, k string) string {
		for i := 0; i+1 < len(p.kv); i += 2 {
			if p.kv[i] == k {
				return p.kv[i+1]
			}
		}
		return ""
	}
	g := vague7Points(t, "gitea", "gitea.prom")
	states := map[string]float64{}
	for _, p := range g["gitea.users"] {
		states[tag(p, "state")] = p.value
	}
	if states["active"] != 2+10 || len(states) != 2 {
		t.Errorf("gitea.users par état : %v", states)
	}
	if v := tag(g["gitea.build_info"][0], "version"); v != "28.4.0" {
		t.Errorf("version de Gitea : %q", v)
	}
	f := vague7Points(t, "forgejo", "forgejo.prom")
	if v := tag(f["gitea.build_info"][0], "version"); !strings.HasPrefix(v, "13.0.5") {
		t.Errorf("version de Forgejo : %q", v)
	}
	ha := vague7Points(t, "homeassistant", "homeassistant.prom")
	found := false
	for _, p := range ha["homeassistant.sensor.temperature"] {
		if tag(p, "entity") == "sensor.temperature_cave" && tag(p, "domain") == "sensor" && p.value == 12.5+10 {
			found = true
		}
	}
	if !found {
		t.Errorf("capteur de température de l'essai absent : %v", ha["homeassistant.sensor.temperature"])
	}
	if n := len(ha["homeassistant.entity.available"]); n < 100 {
		t.Errorf("une série de disponibilité par entité attendue, %d reçues", n)
	}
	sk := vague7Points(t, "mastodon", "mastodon_sidekiq.prom")
	queues := map[string]bool{}
	for _, p := range sk["mastodon.sidekiq.jobs"] {
		queues[tag(p, "queue")] = true
		if tag(p, "job_name") != "" {
			t.Errorf("job_name aurait dû être additionnée : %v", p.kv)
		}
	}
	for _, q := range []string{"default", "push", "pull", "scheduler"} {
		if !queues[q] {
			t.Errorf("file %s absente de mastodon.sidekiq.jobs : %v", q, queues)
		}
	}
	st := vague7Points(t, "mastodon_streaming", "mastodon_streaming.prom")
	for _, p := range st["mastodon_streaming.clients"] {
		if ty := tag(p, "type"); ty == "eventsource" && p.value != 2+10 {
			t.Errorf("clients eventsource : %v", p.value)
		}
	}
	sy := vague7Points(t, "synapse", "synapse.prom")
	for _, p := range sy["synapse.events.persisted"] {
		if tag(p, "server_name") != "" {
			t.Errorf("server_name aurait dû être écartée : %v", p.kv)
		}
	}
}

// Adresses par défaut des profils (sans url) et alias Forgejo.
func TestVague7ProfileDefaults(t *testing.T) {
	for prof, url := range map[string]string{
		"gitea":              "http://127.0.0.1:3000/metrics",
		"forgejo":            "http://127.0.0.1:3000/metrics",
		"synapse":            "http://127.0.0.1:9000/_synapse/metrics",
		"homeassistant":      "http://127.0.0.1:8123/api/prometheus",
		"mastodon":           "http://127.0.0.1:9394/metrics",
		"mastodon_streaming": "http://127.0.0.1:4000/metrics",
	} {
		cfg := profileInstance(t, "integrations:\n  openmetrics:\n    - profile: "+prof+"\n")
		if cfg.URL != url || cfg.Name != prof {
			t.Errorf("%s : url %q, nom %q", prof, cfg.URL, cfg.Name)
		}
	}
	g, _ := omprofiles.Get("gitea")
	f, _ := omprofiles.Get("forgejo")
	if f.Namespace != g.Namespace || len(f.Metrics) != len(g.Metrics) || len(f.Rename) != len(g.Rename) {
		t.Fatalf("forgejo doit reprendre le profil gitea")
	}
}
