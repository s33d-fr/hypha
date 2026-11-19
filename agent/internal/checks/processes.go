package checks

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// procEntry : un processus, pour les contrôles de présence (checks.processes).
type procEntry struct {
	name    string // nom court (comm sous Linux, nom de l'image sous Windows)
	argv0   string // nom tiré du premier argument (comm est tronqué à 15 caractères)
	cmdline string
}

// procWatch fait les contrôles process.up et la métrique process.count (§ 31), sur une liste
// de processus relue au plus toutes les 30 secondes.
type procWatch struct {
	checks []config.ProcessCheck
	res    []*regexp.Regexp
	needCL bool

	mu     sync.Mutex
	list   []procEntry
	listAt time.Time
}

func newProcWatch(list []config.ProcessCheck) *procWatch {
	w := &procWatch{checks: list}
	for _, c := range list {
		var re *regexp.Regexp
		if c.Match == "regex" {
			re, _ = regexp.Compile(c.Name)
		}
		w.res = append(w.res, re)
		if c.Match != "exact" || len(c.Name) > 15 {
			w.needCL = true
		}
	}
	return w
}

// matches dit si le processus correspond au contrôle : le nom, puis (contains, regex) la
// ligne de commande.
func matches(c config.ProcessCheck, re *regexp.Regexp, p procEntry) bool {
	switch c.Match {
	case "contains":
		return strings.Contains(p.name, c.Name) || strings.Contains(p.cmdline, c.Name)
	case "regex":
		return re != nil && (re.MatchString(p.name) || re.MatchString(p.cmdline))
	}
	n := strings.TrimSuffix(c.Name, ".exe")
	return strings.EqualFold(strings.TrimSuffix(p.name, ".exe"), n) || strings.EqualFold(strings.TrimSuffix(p.argv0, ".exe"), n)
}

func argv0Name(cmdline string) string {
	f := strings.Fields(cmdline)
	if len(f) == 0 {
		return ""
	}
	return filepath.Base(strings.ReplaceAll(f[0], `\`, "/"))
}

func (w *procWatch) run(ctx context.Context, now time.Time, tags model.TagFunc) ([]model.Series, []model.Check, error) {
	if len(w.checks) == 0 {
		return nil, nil, nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.list == nil || now.Sub(w.listAt) >= 30*time.Second {
		list, err := listProcesses(ctx, w.needCL)
		if err != nil {
			return nil, nil, fmt.Errorf("liste des processus : %w", err)
		}
		w.list, w.listAt = list, now
	}
	ts := model.Ms(now)
	b := model.NewSeriesBuilder(ts, tags)
	var out []model.Check
	for i, c := range w.checks {
		n := 0
		for _, p := range w.list {
			if matches(c, w.res[i], p) {
				n++
			}
		}
		lo, hi := c.ProcessBound()
		st := model.CheckOK
		if n < lo || (hi >= 0 && n > hi) {
			st = model.CheckCrit
		}
		want := fmt.Sprintf("au moins %d", lo)
		if hi >= 0 {
			want = fmt.Sprintf("entre %d et %d", lo, hi)
		}
		b.Gauge("process.count", "count", float64(n), "process", c.Name)
		out = append(out, model.Check{TS: ts, Name: "process.up", Status: st,
			Message: fmt.Sprintf("%d processus %s (attendu : %s)", n, c.Name, want), Tags: tags("process", c.Name)})
	}
	return b.Series(), out, nil
}

// Processes : contrôles de checks.processes et métrique process.count.
func (r *Runner) Processes(ctx context.Context, now time.Time, tags model.TagFunc) ([]model.Series, []model.Check, error) {
	return r.procs.run(ctx, now, tags)
}

// CheckService rend aussitôt le contrôle d'une unité systemd ou d'un service Windows (après
// une action à distance, § 28).
func (r *Runner) CheckService(ctx context.Context, name string, tags model.TagFunc) []model.Check {
	if r.services == nil {
		return nil
	}
	return r.services.checkOne(ctx, time.Now(), name, tags)
}
