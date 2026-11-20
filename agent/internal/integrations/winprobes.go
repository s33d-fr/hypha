package integrations

import (
	"context"
	"errors"
	"fmt"
	globpath "path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// Sondes Windows de la vague 3 : compteurs de performance choisis (perf_counters), requêtes WMI
// génériques (wmi), comptage des événements collectés (windows_eventlog). La logique est ici,
// commune et testée partout ; la lecture réelle (PDH, COM/WMI) est dans winprobes_windows.go.

// ── garde-fou de délai ────────────────────────────────────────────────────────────────────

// guard : un seul relevé à la fois. Un relevé qui dépasse son délai est abandonné (PDH et WMI ne
// s'interrompent pas) ; tant qu'il n'a pas rendu la main, les suivants sont refusés au lieu de
// s'empiler.
type guard struct{ busy atomic.Bool }

var errBusy = errors.New("relevé précédent toujours en cours (bloqué au-delà de son délai)")

func guardRun[T any](g *guard, timeout time.Duration, fn func() (T, error)) (T, error) {
	var zero T
	if !g.busy.CompareAndSwap(false, true) {
		return zero, errBusy
	}
	type res struct {
		v   T
		err error
	}
	ch := make(chan res, 1)
	go func() {
		defer g.busy.Store(false)
		defer func() {
			if p := recover(); p != nil {
				ch <- res{err: fmt.Errorf("panique : %v", p)}
			}
		}()
		v, err := fn()
		ch <- res{v, err}
	}()
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case r := <-ch:
		return r.v, r.err
	case <-t.C:
		return zero, fmt.Errorf("délai de %s dépassé", timeout)
	}
}

// pdhValues : chemin du compteur → instance → valeur ("" pour un compteur sans (*)).
type pdhValues map[string]map[string]float64

// pdhReadFunc relève des compteurs ; rend aussi les chemins absents de cet hôte.
type pdhReadFunc func(paths []string) (pdhValues, []string, error)

// setTags : étiquettes d'un jeu (instance_name, puis tags de la conf), plus des paires.
func setTags(tags model.TagFunc, name string, extra map[string]string) model.TagFunc {
	keys := model.SortedKeys(extra)
	return func(kv ...string) map[string]string {
		all := make([]string, 0, len(kv)+2+2*len(keys))
		for _, k := range keys {
			all = append(all, k, extra[k])
		}
		all = append(all, config.InstanceTag, name)
		return tags(append(all, kv...)...)
	}
}

func matchInstance(name string, include, exclude []string) bool {
	low := strings.ToLower(name)
	m := func(globs []string) bool {
		for _, g := range globs {
			if ok, _ := globpath.Match(strings.ToLower(g), low); ok {
				return true
			}
		}
		return false
	}
	return (len(include) == 0 || m(include)) && !m(exclude)
}

// ── compteurs de performance (integrations.perf_counters) ─────────────────────────────────

type perfSet struct {
	cfg     config.PerfCounterSet
	read    pdhReadFunc
	closeFn func()
	g       guard
}

func newPerfSet(cfg config.PerfCounterSet, read pdhReadFunc, closeFn func()) *perfSet {
	if closeFn == nil {
		closeFn = func() {}
	}
	return &perfSet{cfg: cfg, read: read, closeFn: closeFn}
}

func (p *perfSet) name() string          { return "perf_counters:" + p.cfg.Name }
func (p *perfSet) period() time.Duration { return p.cfg.Interval.D() }
func (p *perfSet) close()                { p.closeFn() }

type perfResult struct {
	v       pdhValues
	missing []string
}

func (p *perfSet) collect(_ context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	ts := model.Ms(now)
	t := setTags(tags, p.cfg.Name, p.cfg.Tags)
	sb := b.With(t)
	paths := make([]string, 0, len(p.cfg.Counters))
	seen := map[string]bool{}
	for _, c := range p.cfg.Counters {
		if !seen[c.Path] {
			seen[c.Path] = true
			paths = append(paths, c.Path)
		}
	}
	start := time.Now()
	res, err := guardRun(&p.g, p.cfg.Timeout.D(), func() (perfResult, error) {
		v, missing, err := p.read(paths)
		return perfResult{v, missing}, err
	})
	if err != nil && !errors.Is(err, errNoData) {
		if !errors.Is(err, errBusy) && !strings.HasPrefix(err.Error(), "délai") {
			p.close() // requête PDH rouverte au prochain relevé
		}
		return []model.Check{{TS: ts, Name: "perf_counters.can_collect", Status: model.CheckCrit,
			Message: model.Truncate("compteurs illisibles : "+err.Error(), 300), Tags: t()}}, "erreur : " + err.Error()
	}
	sb.Gauge("perf_counters.duration", "millisecond", float64(time.Since(start).Milliseconds()))
	kept, dropped := 0, 0
	for _, c := range p.cfg.Counters {
		vals := res.v[c.Path]
		_, inst, _, _ := config.SplitCounterPath(c.Path)
		name := p.cfg.Namespace + "." + c.Metric
		for _, i := range model.SortedKeys(vals) {
			if inst == "*" && !matchInstance(i, c.Instances, c.ExcludeInstances) {
				continue
			}
			if kept >= p.cfg.MaxSeries {
				dropped++
				continue
			}
			kept++
			if inst == "*" {
				sb.Gauge(name, c.Unit, vals[i], c.InstanceTag, i)
			} else {
				sb.Gauge(name, c.Unit, vals[i])
			}
		}
	}
	sb.Gauge("perf_counters.series", "count", float64(kept))
	sb.Gauge("perf_counters.series.dropped", "count", float64(dropped))
	st, msg := model.CheckOK, fmt.Sprintf("%d série(s) relevée(s)", kept)
	switch {
	case len(res.missing) == len(paths):
		st, msg = model.CheckCrit, "aucun des compteurs n'existe sur cet hôte (noms anglais attendus) : "+strings.Join(res.missing, ", ")
	case len(res.missing) > 0:
		st, msg = model.CheckWarn, fmt.Sprintf("%s ; compteur(s) absent(s) : %s", msg, strings.Join(res.missing, ", "))
	}
	if dropped > 0 {
		st = max(st, model.CheckWarn)
		msg += fmt.Sprintf(" ; %d série(s) écartée(s) au-delà de max_series (%d)", dropped, p.cfg.MaxSeries)
	}
	state := "ok"
	if st != model.CheckOK {
		state = "partiel : " + msg
	}
	return []model.Check{{TS: ts, Name: "perf_counters.can_collect", Status: st, Message: model.Truncate(msg, 300), Tags: t()}}, state
}

// ── requêtes WMI (integrations.wmi) ───────────────────────────────────────────────────────

// wmiQueryFunc exécute une requête WQL dans un espace WMI ; une ligne = propriété → valeur.
type wmiQueryFunc func(namespace, wql string, props []string) ([]map[string]any, error)

type wmiSet struct {
	cfg   config.WMIQuery
	query wmiQueryFunc
	g     guard
}

func newWMISet(cfg config.WMIQuery, q wmiQueryFunc) *wmiSet { return &wmiSet{cfg: cfg, query: q} }

func (w *wmiSet) name() string          { return "wmi:" + w.cfg.Name }
func (w *wmiSet) close()                {}
func (w *wmiSet) period() time.Duration { return w.cfg.Interval.D() }

// wql : SELECT des propriétés mesurées et des propriétés d'étiquette.
func (w *wmiSet) wql() (string, []string) {
	var props []string
	seen := map[string]bool{}
	for _, p := range w.cfg.Properties {
		if !seen[strings.ToLower(p.Property)] {
			seen[strings.ToLower(p.Property)] = true
			props = append(props, p.Property)
		}
	}
	for _, p := range w.cfg.TagBy {
		if !seen[strings.ToLower(p)] {
			seen[strings.ToLower(p)] = true
			props = append(props, p)
		}
	}
	q := "SELECT " + strings.Join(props, ", ") + " FROM " + w.cfg.Class
	if f := strings.TrimSpace(w.cfg.Filter); f != "" {
		q += " WHERE " + f
	}
	return q, props
}

// wmiNumber convertit une valeur WMI en nombre (les entiers 64 bits arrivent en texte).
func wmiNumber(v any) (float64, bool) {
	switch x := v.(type) {
	case int8:
		return float64(x), true
	case int16:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case int:
		return float64(x), true
	case uint8:
		return float64(x), true
	case uint16:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	case float32:
		return float64(x), true
	case float64:
		return x, true
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	}
	return 0, false
}

func rowValue(row map[string]any, prop string) (any, bool) {
	if v, ok := row[prop]; ok {
		return v, true
	}
	for k, v := range row {
		if strings.EqualFold(k, prop) {
			return v, true
		}
	}
	return nil, false
}

func (w *wmiSet) collect(_ context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	ts := model.Ms(now)
	t := setTags(tags, w.cfg.Name, w.cfg.Tags)
	sb := b.With(t)
	q, props := w.wql()
	start := time.Now()
	rows, err := guardRun(&w.g, w.cfg.Timeout.D(), func() ([]map[string]any, error) {
		return w.query(w.cfg.WMINamespace, q, props)
	})
	if err != nil {
		return []model.Check{{TS: ts, Name: "wmi.can_query", Status: model.CheckCrit,
			Message: model.Truncate("requête « "+q+" » en échec : "+err.Error(), 300), Tags: t()}}, "erreur : " + err.Error()
	}
	sb.Gauge("wmi.duration", "millisecond", float64(time.Since(start).Milliseconds()))
	sb.Gauge("wmi.rows", "count", float64(len(rows)))
	kept, dropped, nonNumeric := 0, 0, map[string]bool{}
	for _, row := range rows {
		var kv []string
		for _, tp := range w.cfg.TagBy {
			v, _ := rowValue(row, tp)
			s := strings.TrimSpace(fmt.Sprint(v))
			if v == nil || s == "" {
				s = "(vide)"
			}
			kv = append(kv, config.MetricName(tp), s)
		}
		for _, p := range w.cfg.Properties {
			v, ok := rowValue(row, p.Property)
			if !ok || v == nil {
				continue
			}
			f, ok := wmiNumber(v)
			if !ok {
				nonNumeric[p.Property] = true
				continue
			}
			if kept >= w.cfg.MaxSeries {
				dropped++
				continue
			}
			kept++
			sb.Gauge(w.cfg.Namespace+"."+p.Metric, p.Unit, f, append([]string{}, kv...)...)
		}
	}
	sb.Gauge("wmi.series", "count", float64(kept))
	sb.Gauge("wmi.series.dropped", "count", float64(dropped))
	st, msg := model.CheckOK, fmt.Sprintf("%d ligne(s), %d série(s)", len(rows), kept)
	if len(rows) == 0 {
		st, msg = model.CheckWarn, "aucune ligne rendue par « "+q+" » (filtre trop étroit, ou objet absent)"
	}
	if len(nonNumeric) > 0 {
		st = max(st, model.CheckWarn)
		msg += " ; propriété(s) non numérique(s), à mettre dans tag_by : " + strings.Join(model.SortedKeys(nonNumeric), ", ")
	}
	if dropped > 0 {
		st = max(st, model.CheckWarn)
		msg += fmt.Sprintf(" ; %d série(s) écartée(s) au-delà de max_series (%d)", dropped, w.cfg.MaxSeries)
	}
	state := "ok"
	if st != model.CheckOK {
		state = "partiel : " + msg
	}
	return []model.Check{{TS: ts, Name: "wmi.can_query", Status: st, Message: model.Truncate(msg, 300), Tags: t()}}, state
}

// ── comptage des événements Windows collectés (windows_eventlog) ──────────────────────────

// eventlogCounter compte, par canal et par statut, les événements que l'agent a lus (filtres
// appliqués) : windows.eventlog.events, à chaque intervalle, zéros compris pour les couples déjà vus.
type eventlogCounter struct {
	mu     sync.Mutex
	counts map[[2]string]float64
}

func newEventlogCounter() *eventlogCounter {
	return &eventlogCounter{counts: map[[2]string]float64{}}
}

func (e *eventlogCounter) name() string { return "windows_eventlog" }
func (e *eventlogCounter) close()       {}

func (e *eventlogCounter) observe(l *model.Log) {
	if l.Source != "eventlog" {
		return
	}
	attrs, _ := l.Attributes["eventlog"].(map[string]any)
	ch, _ := attrs["channel"].(string)
	if ch == "" {
		return
	}
	e.mu.Lock()
	e.counts[[2]string{ch, l.Status}]++
	e.mu.Unlock()
}

func (e *eventlogCounter) collect(_ context.Context, _ time.Time, b *model.SeriesBuilder, _ model.TagFunc) ([]model.Check, string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.counts) == 0 {
		return nil, "absent"
	}
	keys := make([][2]string, 0, len(e.counts))
	for k := range e.counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i][0]+"\x00"+keys[i][1] < keys[j][0]+"\x00"+keys[j][1] })
	for _, k := range keys {
		b.Add("windows.eventlog.events", model.TypeCount, "count", e.counts[k], "channel", k[0], "status", k[1])
		e.counts[k] = 0
	}
	return nil, "ok"
}

// connCheck : comme canConnect, nom complet en littéral (le catalogue des tableaux vérifie les noms
// de contrôles contre le code de l'agent).
func connCheck(name string, ts int64, ok bool, msg string, tags map[string]string) model.Check {
	c := canConnect("", ts, ok, msg, tags)
	c.Name = name
	return c
}
