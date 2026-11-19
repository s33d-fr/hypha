// Package synthetics fait de l'agent un emplacement privé de tests synthétiques
// (docs/CONTRAT-04.md § 29) : les tests arrivent par le canal de contrôle, tournent à leur
// intervalle depuis le réseau de l'hôte, et leurs résultats partent vers
// /api/v1/intake/synthetics avec location = agent:<hostname>.
//
// Les sondes reprennent la sémantique de celles du serveur (server/hypha/synthetics/probes.py) :
// mêmes clés de config, mêmes détails, mêmes messages, pour qu'un test donne le même
// verdict d'où qu'il parte. Différence voulue : un emplacement privé vise aussi les
// adresses internes.
package synthetics

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"s33d.fr/hypha/agent/internal/model"
)

// Test : un test reçu dans /agent/control.
type Test struct {
	ID        int64          `json:"id"`
	Kind      string         `json:"kind"`
	Target    string         `json:"target"`
	Config    map[string]any `json:"config"`
	Steps     []Step         `json:"steps"`
	IntervalS int            `json:"interval_s"`
}

type entry struct {
	t       Test
	hash    [32]byte
	next    time.Time
	running bool
}

// Runner planifie et exécute les tests.
type Runner struct {
	location string
	max      int
	post     func([]model.SyntheticResult)
	log      *slog.Logger
	setState func(string)

	mu      sync.Mutex
	tests   map[int64]*entry
	results []model.SyntheticResult
	skipped map[int64]string // tests ignorés (icmp impossible ici) et leur motif
}

// New prépare l'emplacement ; post reçoit les résultats par lots, setState l'état du collecteur.
func New(hostname string, maxConcurrent int, post func([]model.SyntheticResult), setState func(string), log *slog.Logger) *Runner {
	return &Runner{location: "agent:" + hostname, max: max(1, maxConcurrent), post: post, log: log, setState: setState,
		tests: map[int64]*entry{}, skipped: map[int64]string{}}
}

// Update remplace la liste des tests ; un test inchangé garde son prochain passage, un test
// nouveau part dans la minute (étalé, pour ne pas tout lancer à la même seconde).
func (r *Runner) Update(list []Test) {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	keep := map[int64]bool{}
	for _, t := range list {
		if t.ID <= 0 {
			continue
		}
		keep[t.ID] = true
		raw, _ := json.Marshal(t)
		h := sha256.Sum256(raw)
		if e, ok := r.tests[t.ID]; ok && e.hash == h {
			continue
		}
		spread := min(interval(t), time.Minute)
		r.tests[t.ID] = &entry{t: t, hash: h, next: now.Add(time.Duration(rand.Int64N(int64(spread))))}
	}
	for id := range r.tests {
		if !keep[id] {
			delete(r.tests, id)
			delete(r.skipped, id)
		}
	}
}

func interval(t Test) time.Duration {
	return time.Duration(max(t.IntervalS, 10)) * time.Second
}

// Count rend le nombre de tests suivis.
func (r *Runner) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.tests)
}

// Run fait tourner les tests jusqu'à l'arrêt ; les résultats partent toutes les 10 s.
func (r *Runner) Run(ctx context.Context) {
	sem := make(chan struct{}, r.max)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	flush := time.NewTicker(10 * time.Second)
	defer flush.Stop()
	var wg sync.WaitGroup
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			r.flush()
			return
		case <-flush.C:
			r.flush()
		case now := <-tick.C:
			for _, e := range r.due(now) {
				select {
				case sem <- struct{}{}:
				default:
					r.release(e, now) // toutes les places prises : au prochain tour
					continue
				}
				wg.Add(1)
				go func(e *entry) {
					defer wg.Done()
					defer func() { <-sem }()
					r.runOne(ctx, e)
				}(e)
			}
		}
	}
}

func (r *Runner) due(now time.Time) []*entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*entry
	for _, e := range r.tests {
		if !e.running && !now.Before(e.next) {
			e.running = true
			e.next = now.Add(interval(e.t))
			out = append(out, e)
		}
	}
	return out
}

func (r *Runner) release(e *entry, now time.Time) {
	r.mu.Lock()
	e.running = false
	e.next = now.Add(time.Second)
	r.mu.Unlock()
}

func (r *Runner) runOne(ctx context.Context, e *entry) {
	defer func() {
		if p := recover(); p != nil {
			r.log.Error("sonde synthétique en panique", "test", e.t.ID, "err", p)
		}
		r.mu.Lock()
		e.running = false
		r.mu.Unlock()
	}()
	start := time.Now()
	res := Execute(ctx, e.t)
	if res.Skip != "" {
		r.mu.Lock()
		first := r.skipped[e.t.ID] == ""
		r.skipped[e.t.ID] = res.Skip
		r.mu.Unlock()
		if first {
			r.log.Warn("test synthétique ignoré sur cet agent", "test", e.t.ID, "type", e.t.Kind, "motif", res.Skip)
		}
		r.setState("partiel : " + res.Skip)
		return
	}
	out := model.SyntheticResult{
		TestID: e.t.ID, TS: start.UnixMilli(), OK: res.OK, Details: res.Details, Location: r.location,
		StatusCode: res.StatusCode, LatencyMS: res.LatencyMS,
	}
	if out.Details == nil {
		out.Details = map[string]any{}
	}
	if res.Error != "" {
		msg := res.Error
		out.Error = &msg
	}
	r.mu.Lock()
	r.results = append(r.results, out)
	n := len(r.results)
	r.mu.Unlock()
	if n >= 50 {
		r.flush()
	}
}

func (r *Runner) flush() {
	r.mu.Lock()
	batch := r.results
	r.results = nil
	r.mu.Unlock()
	if len(batch) > 0 {
		r.post(batch)
	}
}

// Result : verdict d'une sonde. Skip non vide : le test ne peut pas tourner ici (motif).
type Result struct {
	OK         bool
	StatusCode *int
	LatencyMS  *float64
	Error      string
	Details    map[string]any
	Skip       string
}

// Execute lance un test, borné à 3 fois son délai plus 5 s (comme le serveur).
func Execute(ctx context.Context, t Test) Result {
	cfg := t.Config
	if cfg == nil {
		cfg = map[string]any{}
	}
	budget := time.Duration((cfgFloat(cfg, "timeout_s", 10)*3 + 5) * float64(time.Second))
	if t.Kind == "multistep" {
		var total float64
		for _, s := range t.Steps {
			total += stepTimeout(s).Seconds()
		}
		budget = time.Duration((total + 5) * float64(time.Second))
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	switch t.Kind {
	case "http":
		return probeHTTP(ctx, t.Target, cfg)
	case "ssl":
		return probeSSL(ctx, t.Target, cfg)
	case "tcp":
		return probeTCP(ctx, t.Target, cfg)
	case "dns":
		return probeDNS(ctx, t.Target, cfg)
	case "icmp":
		return probeICMP(ctx, t.Target, cfg)
	case "multistep":
		return probeMultistep(ctx, t.Steps, cfg)
	case "smtp", "imap":
		return probeMail(ctx, t.Kind, t.Target, cfg)
	}
	return Result{Error: "type de test inconnu : " + t.Kind}
}

func ptrInt(v int) *int { return &v }

func ptrMS(d time.Duration) *float64 {
	v := round2(float64(d.Microseconds()) / 1000)
	return &v
}

func round2(v float64) float64 { return float64(int64(v*100+0.5)) / 100 }

func msSince(t time.Time) float64 { return round2(float64(time.Since(t).Microseconds()) / 1000) }

func cfgFloat(cfg map[string]any, k string, def float64) float64 {
	switch v := cfg[k].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case json.Number:
		if f, err := v.Float64(); err == nil {
			return f
		}
	}
	return def
}

func cfgString(cfg map[string]any, k, def string) string {
	if v, ok := cfg[k].(string); ok && v != "" {
		return v
	}
	return def
}

func cfgBool(cfg map[string]any, k string, def bool) bool {
	if v, ok := cfg[k].(bool); ok {
		return v
	}
	return def
}
