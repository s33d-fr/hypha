package integrations

import (
	"context"
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

// Réponses tirées des exemples de la documentation de l'API d'Ollama
// (https://github.com/ollama/ollama/blob/main/docs/api.md : « List Local Models », « List Running
// Models », « Version »).
const (
	ollamaVersionDoc = `{"version": "0.5.1"}`
	ollamaTagsDoc    = `{"models": [
  {"name": "deepseek-r1:latest", "model": "deepseek-r1:latest", "modified_at": "2025-05-10T08:06:48.639712648-07:00",
   "size": 4683075271, "digest": "0a8c266910232fd3291e71e5ba1e058cc5af9d411192cf88b6d30e92b6e73163",
   "details": {"parent_model": "", "format": "gguf", "family": "qwen2", "families": ["qwen2"], "parameter_size": "7.6B", "quantization_level": "Q4_K_M"}},
  {"name": "llama3.2:latest", "model": "llama3.2:latest", "modified_at": "2025-05-04T17:37:44.706015396-07:00",
   "size": 2019393189, "digest": "a80c4f17acd55265feec403c7aef86be0c25983ab279d83f3bcd3abbcb5b8b72",
   "details": {"parent_model": "", "format": "gguf", "family": "llama", "families": ["llama"], "parameter_size": "3.2B", "quantization_level": "Q4_K_M"}}
]}`
	ollamaPsDoc = `{"models": [
  {"name": "mistral:latest", "model": "mistral:latest", "size": 5137025024,
   "digest": "2ae6f6dd7a3dd734790bbbf58b8909a606e0e7e97e94b7604e0aa7ae4490e6d8",
   "details": {"parent_model": "", "format": "gguf", "family": "llama", "families": ["llama"], "parameter_size": "7.2B", "quantization_level": "Q4_0"},
   "expires_at": "2024-06-04T14:38:31.83753-07:00", "size_vram": 2568512512}
]}`
)

// fakeOllama sert les trois routes ; ps vide = route absente (404, versions anciennes).
func fakeOllama(t *testing.T, version, tags, ps string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("méthode %s sur %s : la sonde ne doit que lire", r.Method, r.URL.Path)
		}
		body := map[string]string{"/api/version": version, "/api/tags": tags, "/api/ps": ps}[r.URL.Path]
		if body == "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestOllamaModelsAndLoaded(t *testing.T) {
	srv := fakeOllama(t, ollamaVersionDoc, ollamaTagsDoc, ollamaPsDoc)
	o := newOllama(config.OllamaConfig{Enabled: "auto", URL: srv.URL})
	b := model.NewSeriesBuilder(0, testTags)
	now := time.Date(2024, 6, 4, 21, 30, 0, 0, time.UTC) // 8 min 31 s avant l'échéance de mistral
	checks, st := o.collect(context.Background(), now, b, testTags)
	if st != "ok" || len(checks) != 1 || checks[0].Status != model.CheckOK ||
		!strings.Contains(checks[0].Message, "Ollama 0.5.1 joignable : 2 modèle(s) installé(s), 1 chargé(s)") {
		t.Fatalf("contrôle : %q %+v", st, checks)
	}
	got := index(b.Series())
	inst := "instance=" + srv.URL
	want := map[string]float64{
		"ollama.models.installed{" + inst + "}": 2,
		"ollama.models.size{" + inst + "}":      4683075271 + 2019393189,
		"ollama.models.loaded{" + inst + "}":    1,
		"ollama.loaded.vram{" + inst + "}":      2568512512,
		"ollama.model.size{" + inst + ",ollama_family=qwen2,ollama_model=deepseek-r1:latest,ollama_parameters=7.6B,ollama_quantization=Q4_K_M}": 4683075271,
		"ollama.model.loaded.size{" + inst + ",ollama_model=mistral:latest}":                                                                    5137025024,
		"ollama.model.loaded.vram_pct{" + inst + ",ollama_model=mistral:latest}":                                                                50,
	}
	for k, v := range want {
		if s, ok := got[k]; !ok || s.Points[0].Value != v {
			t.Errorf("%s = %v, attendu %v", k, s.Points, v)
		}
	}
	if s, ok := got["ollama.model.loaded.expires_in{"+inst+",ollama_model=mistral:latest}"]; !ok || s.Points[0].Value < 511 || s.Points[0].Value > 512 {
		t.Errorf("échéance : %+v", s)
	}
	if _, ok := got["ollama.response_time{"+inst+"}"]; !ok {
		t.Error("temps de réponse absent")
	}
}

func TestOllamaShimAndOldVersion(t *testing.T) {
	// Relais au dialecte Ollama (échéance lointaine) : pas de mesure d'échéance.
	shimPs := `{"models":[{"name":"m","model":"m","size":0,"digest":"","expires_at":"9999-12-31T23:59:59Z"}]}`
	srv := fakeOllama(t, `{"version":"0.0.0-shim"}`, `{"models":[{"name":"m","model":"m","size":0}]}`, shimPs)
	b := model.NewSeriesBuilder(0, testTags)
	if _, st := newOllama(config.OllamaConfig{Enabled: "true", URL: srv.URL}).collect(context.Background(), time.Now(), b, testTags); st != "ok" {
		t.Fatalf("état %q", st)
	}
	for _, s := range b.Series() {
		if s.Metric == "ollama.model.loaded.expires_in" || s.Metric == "ollama.model.loaded.vram_pct" {
			t.Fatalf("%s envoyé pour un relais : %+v", s.Metric, s.Points)
		}
	}
	// Version sans /api/ps : lecture partielle, contrôle OK.
	srv = fakeOllama(t, ollamaVersionDoc, ollamaTagsDoc, "")
	checks, st := newOllama(config.OllamaConfig{Enabled: "true", URL: srv.URL}).collect(context.Background(), time.Now(),
		model.NewSeriesBuilder(0, testTags), testTags)
	if !strings.HasPrefix(st, "partiel") || checks[0].Status != model.CheckOK {
		t.Fatalf("sans /api/ps : %q %+v", st, checks)
	}
}

func TestOllamaAbsentAndDown(t *testing.T) {
	// Auto, adresse par défaut fermée : absent, sans alerte.
	o := newOllama(config.OllamaConfig{Enabled: "auto", URL: config.DefaultOllamaURL})
	o.cfg.URL = "http://127.0.0.1:1"
	if c, st := o.collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags); c != nil || st != "absent" {
		t.Fatalf("absent : %q %+v", st, c)
	}
	// Un autre service sur le port : pas d'Ollama, absent.
	other := fakeOllama(t, "", "", "")
	o = newOllama(config.OllamaConfig{Enabled: "auto", URL: config.DefaultOllamaURL})
	o.cfg.URL = other.URL
	if c, st := o.collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags); c != nil || st != "absent" {
		t.Fatalf("autre service : %q %+v", st, c)
	}
	// Vu une fois, puis arrêté : CRIT.
	srv := fakeOllama(t, ollamaVersionDoc, ollamaTagsDoc, ollamaPsDoc)
	o = newOllama(config.OllamaConfig{Enabled: "auto", URL: config.DefaultOllamaURL})
	o.cfg.URL = srv.URL
	now := time.Now()
	if _, st := o.collect(context.Background(), now, model.NewSeriesBuilder(0, testTags), testTags); st != "ok" {
		t.Fatalf("premier passage : %q", st)
	}
	srv.Close()
	c, st := o.collect(context.Background(), now.Add(time.Minute), model.NewSeriesBuilder(0, testTags), testTags)
	if len(c) != 1 || c[0].Status != model.CheckCrit || c[0].Name != "ollama.can_connect" || !strings.HasPrefix(st, "erreur") ||
		strings.Contains(c[0].Message, "http://") {
		t.Fatalf("arrêté : %q %+v", st, c)
	}
}

// Essai contre un vrai Ollama, lancé à la main : OLLAMA_REEL=1 OLLAMA_URL=http://127.0.0.1:11434.
func TestOllamaReel(t *testing.T) {
	if os.Getenv("OLLAMA_REEL") != "1" {
		t.Skip("OLLAMA_REEL=1 pour interroger un vrai Ollama")
	}
	o := newOllama(config.OllamaConfig{Enabled: "true", URL: os.Getenv("OLLAMA_URL")})
	b := model.NewSeriesBuilder(0, testTags)
	checks, st := o.collect(context.Background(), time.Now(), b, testTags)
	t.Logf("état : %s", st)
	for _, c := range checks {
		t.Logf("[%d] %s : %s", c.Status, c.Name, c.Message)
	}
	for _, s := range b.Series() {
		t.Logf("série %s %v = %v", s.Metric, s.Tags, s.Points[0].Value)
	}
	if st != "ok" {
		t.Fatalf("état %q", st)
	}
}

// Réponses réelles d'Ollama 0.40.16 relevées dans un environnement d'essai le 20/11/2026 (modèle smollm2:135m
// chargé sur le processeur : size_vram nul, context_length présent).
func TestOllamaRealCapture(t *testing.T) {
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	srv := fakeOllama(t, `{"version":"0.40.16"}`, read("ollama-0.40.16-tags.json"), read("ollama-0.40.16-ps.json"))
	o := newOllama(config.OllamaConfig{Enabled: "true", URL: srv.URL})
	b := model.NewSeriesBuilder(0, testTags)
	now := time.Date(2026, 11, 20, 3, 1, 0, 0, time.UTC)
	checks, st := o.collect(context.Background(), now, b, testTags)
	if st != "ok" || !strings.Contains(checks[0].Message, "Ollama 0.40.16 joignable : 1 modèle(s) installé(s), 1 chargé(s)") {
		t.Fatalf("%q %+v", st, checks)
	}
	got := index(b.Series())
	inst := "instance=" + srv.URL
	m := inst + ",ollama_model=smollm2:135m"
	for k, v := range map[string]float64{
		"ollama.models.size{" + inst + "}":              270898672,
		"ollama.model.loaded.size{" + m + "}":           383212583,
		"ollama.model.loaded.vram_pct{" + m + "}":       0,
		"ollama.model.loaded.context_length{" + m + "}": 4096,
		"ollama.model.loaded.expires_in{" + m + "}":     1783.9872357,
		"ollama.model.size{" + inst + ",ollama_family=llama,ollama_model=smollm2:135m,ollama_parameters=134.52M,ollama_quantization=F16}": 270898672,
	} {
		if s, ok := got[k]; !ok || s.Points[0].Value < v-1e-6 || s.Points[0].Value > v+1e-6 {
			t.Errorf("%s = %v, attendu %v", k, s.Points, v)
		}
	}
}
