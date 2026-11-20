package openmetrics

import (
	"math"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// Traduction des familles en séries HYPHA (une instance = un état : valeurs précédentes des
// compteurs, histogrammes et résumés, pour les débits).

// quantiles calculés pour les histogrammes (mêmes centiles que les distributions StatsD).
var histQuantiles = []float64{0.5, 0.95, 0.99}

type matcher func(string) bool

func compileMatchers(patterns []string) []matcher {
	var out []matcher
	for _, p := range patterns {
		if rest, ok := strings.CutPrefix(p, "re:"); ok {
			if re, err := regexp.Compile("^(?:" + rest + ")$"); err == nil {
				out = append(out, re.MatchString)
			}
			continue
		}
		if strings.ContainsAny(p, "*?[") {
			pat := p
			out = append(out, func(s string) bool { ok, _ := path.Match(pat, s); return ok })
			continue
		}
		name := p
		out = append(out, func(s string) bool { return s == name })
	}
	return out
}

func anyMatch(ms []matcher, names []string) bool {
	for _, m := range ms {
		for _, n := range names {
			if m(n) {
				return true
			}
		}
	}
	return false
}

// point : une valeur à envoyer, avant le plafond de cardinalité.
type point struct {
	name, typ, unit string
	kv              []string // étiquettes de l'échantillon, triées par clé
	value           float64
	key             string
}

type prevValue struct {
	v  float64
	ts time.Time
}

type histState struct {
	buckets map[float64]float64
	sum     float64
	count   float64
	ts      time.Time
}

type summaryState struct {
	sum, count float64
	ts         time.Time
}

// converter : sélection, renommage, étiquettes et état des débits d'une instance.
type converter struct {
	cfg       config.OpenMetricsInstance
	include   []matcher
	exclude   []matcher
	labels    map[string]bool
	allLabels bool
	present   map[string]bool // familles de la lecture en cours

	prev     map[string]prevValue
	hist     map[string]histState
	summ     map[string]summaryState
	nextPrev map[string]prevValue
	nextHist map[string]histState
	nextSumm map[string]summaryState
}

func newConverter(cfg config.OpenMetricsInstance) *converter {
	c := &converter{cfg: cfg, include: compileMatchers(cfg.Metrics), exclude: compileMatchers(cfg.ExcludeMetrics),
		labels: map[string]bool{}, prev: map[string]prevValue{}, hist: map[string]histState{}, summ: map[string]summaryState{}}
	for _, l := range cfg.Labels {
		if l == "*" {
			c.allLabels = true
		}
		c.labels[l] = true
	}
	return c
}

// candidates : noms sous lesquels une famille peut être désignée dans la conf (un compteur
// avec ou sans son suffixe _total), sauf quand l'autre nom est lui-même une famille de
// l'exposition : Envoy publie envoy_cluster_upstream_rq (par code) ET
// envoy_cluster_upstream_rq_total, qui ne doivent ni se confondre ni porter le même nom.
func (c *converter) candidates(f *Family) []string {
	out := []string{f.Name}
	if f.Type == "counter" {
		alt := f.Name + "_total"
		if base, ok := strings.CutSuffix(f.Name, "_total"); ok {
			alt = base
		}
		if !c.present[alt] {
			out = append(out, alt)
		}
	}
	return out
}

func (c *converter) selected(f *Family) bool {
	names := c.candidates(f)
	if len(c.include) > 0 && !anyMatch(c.include, names) {
		return false
	}
	return !anyMatch(c.exclude, names)
}

var metricCharRe = regexp.MustCompile(`[^A-Za-z0-9_.\-/]`)

// outName : espace de noms + nom renommé, ou nom Prometheus (sans _total pour un compteur).
func (c *converter) outName(f *Family) string {
	base := ""
	for _, n := range c.candidates(f) {
		if r, ok := c.cfg.Rename[n]; ok {
			base = r
			break
		}
	}
	if base == "" {
		base = f.Name
		if f.Type == "counter" {
			base = strings.TrimSuffix(base, "_total")
		}
	}
	name := metricCharRe.ReplaceAllString(c.cfg.Namespace+"."+base, "_")
	return model.Truncate(name, 200)
}

// unitOf : unité de la famille (# UNIT, sinon suffixe du nom).
func unitOf(f *Family) string {
	u := f.Unit
	if u == "" {
		n := strings.TrimSuffix(f.Name, "_total")
		switch {
		case strings.HasSuffix(n, "_bytes"):
			u = "bytes"
		case strings.HasSuffix(n, "_seconds"):
			u = "seconds"
		}
	}
	switch u {
	case "bytes":
		return "byte"
	case "seconds":
		return "second"
	}
	return ""
}

// rateUnit : unité d'un débit tiré d'un compteur de cette unité.
func rateUnit(u string) string {
	switch u {
	case "byte":
		return "byte_per_second"
	case "second":
		return "" // secondes par seconde : une fraction (cœurs occupés, part du temps)
	}
	return "operation_per_second"
}

var tagKeyCharRe = regexp.MustCompile(`[^a-z0-9_.\-/]`)

// reserved : étiquettes posées par l'agent, qu'une étiquette Prometheus ne doit pas écraser.
var reserved = map[string]bool{"host": true, config.InstanceTag: true, "endpoint": true}

func tagKey(name string) string {
	k := tagKeyCharRe.ReplaceAllString(strings.ToLower(name), "_")
	if reserved[k] {
		k = "exported_" + k
	}
	return model.Truncate(k, 100)
}

// kvOf : étiquettes gardées (liste blanche), hors skip (le, quantile), triées par clé.
func (c *converter) kvOf(s Sample, skip string) []string {
	var keep []Label
	for _, l := range s.Labels {
		if l.Name == skip || l.Value == "" {
			continue
		}
		if c.allLabels || c.labels[l.Name] {
			keep = append(keep, Label{Name: tagKey(l.Name), Value: model.Truncate(l.Value, model.MaxTagValue)})
		}
	}
	sort.Slice(keep, func(i, j int) bool { return keep[i].Name < keep[j].Name })
	kv := make([]string, 0, 2*len(keep))
	for _, l := range keep {
		kv = append(kv, l.Name, l.Value)
	}
	return kv
}

func seriesKey(name string, kv []string) string {
	return name + "\x00" + strings.Join(kv, "\x00")
}

// Convert traduit une lecture ; now date les débits. Les points sont triés (nom, étiquettes).
func (c *converter) Convert(fams []*Family, now time.Time) []point {
	c.nextPrev, c.nextHist, c.nextSumm = map[string]prevValue{}, map[string]histState{}, map[string]summaryState{}
	c.present = make(map[string]bool, len(fams))
	for _, f := range fams {
		c.present[f.Name] = true
	}
	var out []point
	for _, f := range fams {
		if !c.selected(f) {
			continue
		}
		name, unit := c.outName(f), unitOf(f)
		switch f.Type {
		case "counter":
			out = c.counter(out, f, name, unit, now)
		case "histogram", "gaugehistogram":
			out = c.histogram(out, f, name, unit, now)
		case "summary":
			out = c.summary(out, f, name, unit, now)
		default:
			out = c.gauge(out, f, name, unit)
		}
	}
	c.prev, c.hist, c.summ = c.nextPrev, c.nextHist, c.nextSumm
	sort.SliceStable(out, func(i, j int) bool { return out[i].key < out[j].key })
	return out
}

type agg struct {
	kv []string
	v  float64
}

// sumBy additionne les échantillons qui ont les mêmes étiquettes gardées.
func sumBy(order *[]string, m map[string]*agg, key string, kv []string, v float64) {
	if a, ok := m[key]; ok {
		a.v += v
		return
	}
	*order = append(*order, key)
	m[key] = &agg{kv: kv, v: v}
}

func (c *converter) gauge(out []point, f *Family, name, unit string) []point {
	var order []string
	m := map[string]*agg{}
	for _, s := range f.Samples {
		if strings.HasSuffix(s.Name, "_created") || math.IsNaN(s.Value) || math.IsInf(s.Value, 0) {
			continue
		}
		kv := c.kvOf(s, "")
		sumBy(&order, m, seriesKey(name, kv), kv, s.Value)
	}
	for _, k := range order {
		a := m[k]
		out = append(out, point{name: name, typ: model.TypeGauge, unit: unit, kv: a.kv, value: a.v, key: k})
	}
	return out
}

func (c *converter) counter(out []point, f *Family, name, unit string, now time.Time) []point {
	var order []string
	m := map[string]*agg{}
	for _, s := range f.Samples {
		if strings.HasSuffix(s.Name, "_created") || math.IsNaN(s.Value) || math.IsInf(s.Value, 0) {
			continue
		}
		kv := c.kvOf(s, "")
		sumBy(&order, m, seriesKey(name, kv), kv, s.Value)
	}
	for _, k := range order {
		a := m[k]
		c.nextPrev[k] = prevValue{v: a.v, ts: now}
		p, ok := c.prev[k]
		dt := now.Sub(p.ts).Seconds()
		if !ok || dt <= 0 || a.v < p.v {
			continue // première lecture, ou compteur remis à zéro (redémarrage de la cible)
		}
		out = append(out, point{name: name, typ: model.TypeGauge, unit: rateUnit(unit), kv: a.kv, value: (a.v - p.v) / dt, key: k})
	}
	return out
}

func (c *converter) histogram(out []point, f *Family, name, unit string, now time.Time) []point {
	gaugeHist := f.Type == "gaugehistogram"
	type acc struct {
		kv         []string
		buckets    map[float64]float64
		sum, count float64
		hasCount   bool
	}
	var order []string
	m := map[string]*acc{}
	for _, s := range f.Samples {
		kv := c.kvOf(s, "le")
		k := seriesKey(name, kv)
		a, ok := m[k]
		if !ok {
			a = &acc{kv: kv, buckets: map[float64]float64{}}
			m[k] = a
			order = append(order, k)
		}
		suffix := strings.TrimPrefix(s.Name, f.Name)
		switch suffix {
		case "_bucket":
			le, ok := s.Label("le")
			if !ok {
				continue
			}
			ub, err := strconv.ParseFloat(le, 64)
			if err != nil {
				continue
			}
			a.buckets[ub] += s.Value
		case "_sum", "_gsum":
			a.sum += s.Value
		case "_count", "_gcount":
			a.count += s.Value
			a.hasCount = true
		}
	}
	for _, k := range order {
		a := m[k]
		if !a.hasCount {
			if inf, ok := a.buckets[math.Inf(1)]; ok {
				a.count = inf
			}
		}
		if gaugeHist {
			out = append(out,
				point{name: name + ".count", typ: model.TypeGauge, kv: a.kv, value: a.count, key: seriesKey(name+".count", a.kv)},
				point{name: name + ".sum", typ: model.TypeGauge, unit: unit, kv: a.kv, value: a.sum, key: seriesKey(name+".sum", a.kv)})
			continue
		}
		c.nextHist[k] = histState{buckets: a.buckets, sum: a.sum, count: a.count, ts: now}
		p, ok := c.hist[k]
		dt := now.Sub(p.ts).Seconds()
		if !ok || dt <= 0 || a.count < p.count {
			continue
		}
		dCount, dSum := a.count-p.count, a.sum-p.sum
		out = append(out,
			point{name: name + ".count", typ: model.TypeGauge, unit: "operation_per_second", kv: a.kv, value: dCount / dt, key: seriesKey(name+".count", a.kv)},
			point{name: name + ".sum", typ: model.TypeGauge, unit: rateUnit(unit), kv: a.kv, value: dSum / dt, key: seriesKey(name+".sum", a.kv)})
		if dCount > 0 {
			out = append(out, point{name: name + ".avg", typ: model.TypeGauge, unit: unit, kv: a.kv, value: dSum / dCount, key: seriesKey(name+".avg", a.kv)})
			delta := map[float64]float64{}
			for ub, v := range a.buckets {
				delta[ub] = v - p.buckets[ub]
			}
			for _, q := range histQuantiles {
				if v, ok := bucketQuantile(q, delta); ok {
					n := name + "." + quantileName(q)
					out = append(out, point{name: n, typ: model.TypeGauge, unit: unit, kv: a.kv, value: v, key: seriesKey(n, a.kv)})
				}
			}
		}
		if c.cfg.HistogramBuckets {
			for ub, v := range a.buckets {
				d := v - p.buckets[ub]
				if d < 0 {
					continue
				}
				kv := append(append([]string{}, a.kv...), "upper_bound", formatBound(ub))
				n := name + ".bucket"
				out = append(out, point{name: n, typ: model.TypeGauge, unit: "operation_per_second", kv: kv, value: d / dt, key: seriesKey(n, kv)})
			}
		}
	}
	return out
}

func formatBound(ub float64) string {
	if math.IsInf(ub, 1) {
		return "inf"
	}
	return strconv.FormatFloat(ub, 'g', -1, 64)
}

// bucketQuantile estime un centile à partir de seaux cumulés (même méthode que la fonction
// histogram_quantile de Prometheus : interpolation linéaire dans le seau qui contient le rang).
func bucketQuantile(q float64, buckets map[float64]float64) (float64, bool) {
	if len(buckets) < 2 {
		return 0, false
	}
	bounds := make([]float64, 0, len(buckets))
	for ub := range buckets {
		bounds = append(bounds, ub)
	}
	sort.Float64s(bounds)
	if !math.IsInf(bounds[len(bounds)-1], 1) {
		return 0, false
	}
	total := buckets[bounds[len(bounds)-1]]
	if total <= 0 {
		return 0, false
	}
	rank := q * total
	prevBound, prevCount := 0.0, 0.0
	for i, ub := range bounds {
		cum := buckets[ub]
		if cum >= rank {
			if math.IsInf(ub, 1) {
				if i == 0 {
					return 0, false
				}
				return bounds[i-1], true // au-delà du dernier seau fini : sa borne
			}
			if i == 0 && ub <= 0 {
				return ub, true
			}
			if cum == prevCount {
				return ub, true
			}
			return prevBound + (ub-prevBound)*(rank-prevCount)/(cum-prevCount), true
		}
		prevBound, prevCount = ub, cum
	}
	return 0, false
}

// quantileName : 0.5 → p50, 0.99 → p99, 0.999 → p99_9.
func quantileName(q float64) string {
	return "p" + strings.ReplaceAll(strconv.FormatFloat(q*100, 'f', -1, 64), ".", "_")
}

func (c *converter) summary(out []point, f *Family, name, unit string, now time.Time) []point {
	type acc struct {
		kv         []string
		quantiles  map[float64]float64
		sum, count float64
	}
	var order []string
	m := map[string]*acc{}
	for _, s := range f.Samples {
		kv := c.kvOf(s, "quantile")
		k := seriesKey(name, kv)
		a, ok := m[k]
		if !ok {
			a = &acc{kv: kv, quantiles: map[float64]float64{}}
			m[k] = a
			order = append(order, k)
		}
		switch strings.TrimPrefix(s.Name, f.Name) {
		case "":
			qs, ok := s.Label("quantile")
			if !ok || math.IsNaN(s.Value) {
				continue
			}
			q, err := strconv.ParseFloat(qs, 64)
			if err != nil || q < 0 || q > 1 {
				continue
			}
			// Séries additionnées (étiquette écartée) : un centile ne s'additionne pas, le
			// plus élevé est gardé.
			if cur, seen := a.quantiles[q]; !seen || s.Value > cur {
				a.quantiles[q] = s.Value
			}
		case "_sum":
			a.sum += s.Value
		case "_count":
			a.count += s.Value
		}
	}
	for _, k := range order {
		a := m[k]
		qs := make([]float64, 0, len(a.quantiles))
		for q := range a.quantiles {
			qs = append(qs, q)
		}
		sort.Float64s(qs)
		for _, q := range qs {
			n := name + "." + quantileName(q)
			out = append(out, point{name: n, typ: model.TypeGauge, unit: unit, kv: a.kv, value: a.quantiles[q], key: seriesKey(n, a.kv)})
		}
		c.nextSumm[k] = summaryState{sum: a.sum, count: a.count, ts: now}
		p, ok := c.summ[k]
		dt := now.Sub(p.ts).Seconds()
		if !ok || dt <= 0 || a.count < p.count {
			continue
		}
		dCount, dSum := a.count-p.count, a.sum-p.sum
		out = append(out,
			point{name: name + ".count", typ: model.TypeGauge, unit: "operation_per_second", kv: a.kv, value: dCount / dt, key: seriesKey(name+".count", a.kv)},
			point{name: name + ".sum", typ: model.TypeGauge, unit: rateUnit(unit), kv: a.kv, value: dSum / dt, key: seriesKey(name+".sum", a.kv)})
		if dCount > 0 {
			out = append(out, point{name: name + ".avg", typ: model.TypeGauge, unit: unit, kv: a.kv, value: dSum / dCount, key: seriesKey(name+".avg", a.kv)})
		}
	}
	return out
}
