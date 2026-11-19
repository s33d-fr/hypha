// Package statsd écoute le format DogStatsD en UDP (contrat § 4) et agrège sur
// l'intervalle de l'agent : c → count (somme), g → gauge (dernière valeur), ms/h/d →
// distribution (.avg .max .p50 .p95 .p99 .count), s → nombre de valeurs distinctes.
// Les contrôles (_sc) et événements (_e) DogStatsD sont aussi acceptés.
package statsd

import (
	"context"
	"log/slog"
	"math"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"s33d.fr/hypha/agent/internal/model"
)

const (
	maxContexts     = 10000 // séries distinctes par intervalle : au-delà, on jette (une appli qui
	maxSamples      = 10000 // mettrait un identifiant dans un nom de métrique ne doit pas noyer l'agent)
	maxNameLen      = 200
	readBufferBytes = 65535
)

type aggregate struct {
	name    string
	kind    string // gauge, count, distribution, set
	tags    map[string]string
	value   float64
	count   float64
	samples []float64
	set     map[string]struct{}
	isTimer bool
}

// Server reçoit et agrège.
type Server struct {
	addr string
	log  *slog.Logger

	mu       sync.Mutex
	contexts map[string]*aggregate
	checks   []model.Check
	events   []model.Event
	dropped  int
	conn     *net.UDPConn
}

// New prépare l'écoute sur addr (127.0.0.1:8125 par défaut).
func New(addr string, log *slog.Logger) *Server {
	return &Server{addr: addr, log: log, contexts: map[string]*aggregate{}}
}

// Start ouvre la socket ; l'erreur (port déjà pris par un autre agent StatsD…) est rendue
// à l'appelant, qui la signale dans l'état des collecteurs.
func (s *Server) Start(ctx context.Context) error {
	ua, err := net.ResolveUDPAddr("udp", s.addr)
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp", ua)
	if err != nil {
		return err
	}
	_ = conn.SetReadBuffer(1 << 20)
	s.conn = conn
	go func() {
		<-ctx.Done()
		conn.Close()
	}()
	go s.loop()
	return nil
}

func (s *Server) loop() {
	buf := make([]byte, readBufferBytes)
	for {
		n, _, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			return // socket fermée à l'arrêt
		}
		for _, line := range strings.Split(string(buf[:n]), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				s.handle(line)
			}
		}
	}
}

func (s *Server) handle(line string) {
	switch {
	case strings.HasPrefix(line, "_sc|"):
		s.handleServiceCheck(line)
	case strings.HasPrefix(line, "_e{"):
		s.handleEvent(line)
	default:
		s.handleMetric(line)
	}
}

// handleMetric : nom:valeur[:valeur…]|type[|@taux][|#étiquette:val,…][|c:…][|T…]
func (s *Server) handleMetric(line string) {
	name, rest, ok := strings.Cut(line, ":")
	if !ok || name == "" || len(name) > maxNameLen {
		return
	}
	parts := strings.Split(rest, "|")
	if len(parts) < 2 {
		return
	}
	values, typ := strings.Split(parts[0], ":"), parts[1]
	rateVal := 1.0
	var tags map[string]string
	for _, p := range parts[2:] {
		switch {
		case strings.HasPrefix(p, "@"):
			if r, err := strconv.ParseFloat(p[1:], 64); err == nil && r > 0 && r <= 1 {
				rateVal = r
			}
		case strings.HasPrefix(p, "#"):
			tags = parseTags(p[1:])
		}
	}
	kind := ""
	switch typ {
	case "c":
		kind = "count"
	case "g":
		kind = "gauge"
	case "ms", "h", "d":
		kind = "distribution"
	case "s":
		kind = "set"
	default:
		return
	}
	key := contextKey(name, kind, tags)

	s.mu.Lock()
	defer s.mu.Unlock()
	a, exists := s.contexts[key]
	if !exists {
		if len(s.contexts) >= maxContexts {
			s.dropped++
			return
		}
		a = &aggregate{name: name, kind: kind, tags: tags, isTimer: typ == "ms"}
		if kind == "set" {
			a.set = map[string]struct{}{}
		}
		s.contexts[key] = a
	}
	for _, raw := range values {
		if kind == "set" {
			a.set[raw] = struct{}{}
			continue
		}
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		switch kind {
		case "count":
			a.value += v / rateVal // le client n'envoie qu'une fraction : on remet à l'échelle
		case "gauge":
			a.value = v // DogStatsD : une jauge est toujours absolue, la dernière valeur l'emporte
		case "distribution":
			a.count += 1 / rateVal
			if len(a.samples) < maxSamples {
				a.samples = append(a.samples, v)
			}
		}
	}
}

// parseTags lit « clé:valeur,clé2,… ». Les clés sont ramenées à [a-z0-9_.-/] (contrat § 2).
func parseTags(s string) map[string]string {
	out := map[string]string{}
	for _, t := range strings.Split(s, ",") {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		k, v, _ := strings.Cut(t, ":")
		k = sanitizeKey(k)
		if k == "" {
			continue
		}
		out[k] = model.Truncate(v, model.MaxTagValue)
	}
	return out
}

func sanitizeKey(k string) string {
	k = strings.ToLower(strings.TrimSpace(k))
	b := []byte(k)
	for i, c := range b {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '.' || c == '-' || c == '/') {
			b[i] = '_'
		}
	}
	return string(b)
}

func contextKey(name, kind string, tags map[string]string) string {
	var sb strings.Builder
	sb.WriteString(name)
	sb.WriteByte('|')
	sb.WriteString(kind)
	for _, k := range model.SortedKeys(tags) {
		sb.WriteByte('|')
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(tags[k])
	}
	return sb.String()
}

// handleServiceCheck : _sc|nom|statut|d:horodatage|h:hôte|#étiquettes|m:message
func (s *Server) handleServiceCheck(line string) {
	parts := strings.Split(line, "|")
	if len(parts) < 3 {
		return
	}
	st, err := strconv.Atoi(parts[2])
	if err != nil || st < 0 || st > 3 {
		return
	}
	c := model.Check{TS: time.Now().UnixMilli(), Name: parts[1], Status: st}
	for _, p := range parts[3:] {
		switch {
		case strings.HasPrefix(p, "d:"):
			if v, err := strconv.ParseInt(p[2:], 10, 64); err == nil {
				c.TS = v * 1000
			}
		case strings.HasPrefix(p, "#"):
			c.Tags = parseTags(p[1:])
		case strings.HasPrefix(p, "m:"):
			c.Message = p[2:]
		}
	}
	s.mu.Lock()
	if len(s.checks) < 1000 {
		s.checks = append(s.checks, c)
	}
	s.mu.Unlock()
}

// handleEvent : _e{longueur_titre,longueur_texte}:titre|texte|d:…|p:low|t:warning|#étiquettes
func (s *Server) handleEvent(line string) {
	head, body, ok := strings.Cut(line, "}:")
	if !ok {
		return
	}
	lens := strings.Split(strings.TrimPrefix(head, "_e{"), ",")
	if len(lens) != 2 {
		return
	}
	tl, err1 := strconv.Atoi(lens[0])
	xl, err2 := strconv.Atoi(lens[1])
	if err1 != nil || err2 != nil || tl < 0 || xl < 0 || tl+1+xl > len(body) {
		return
	}
	e := model.Event{
		TS: time.Now().UnixMilli(), Source: "statsd", Type: "custom",
		Title: body[:tl], Text: strings.ReplaceAll(body[tl+1:tl+1+xl], `\n`, "\n"),
		AlertType: "info", Priority: "normal",
	}
	for _, p := range strings.Split(body[tl+1+xl:], "|") {
		switch {
		case strings.HasPrefix(p, "d:"):
			if v, err := strconv.ParseInt(p[2:], 10, 64); err == nil {
				e.TS = v * 1000
			}
		case strings.HasPrefix(p, "p:"):
			if p[2:] == "low" {
				e.Priority = "low"
			}
		case strings.HasPrefix(p, "t:"):
			switch p[2:] {
			case "error", "warning", "success", "info":
				e.AlertType = p[2:]
			}
		case strings.HasPrefix(p, "k:"):
			e.AggregationKey = p[2:]
		case strings.HasPrefix(p, "#"):
			e.Tags = parseTags(p[1:])
		}
	}
	s.mu.Lock()
	if len(s.events) < 1000 {
		s.events = append(s.events, e)
	}
	s.mu.Unlock()
}

// Flush rend ce qui a été agrégé depuis le dernier appel et remet tout à zéro.
// Les étiquettes communes (conf, host) sont ajoutées ; celles du client l'emportent.
func (s *Server) Flush(now time.Time, tags model.TagFunc) ([]model.Series, []model.Check, []model.Event, int) {
	s.mu.Lock()
	contexts, checks, events, dropped := s.contexts, s.checks, s.events, s.dropped
	s.contexts, s.checks, s.events, s.dropped = map[string]*aggregate{}, nil, nil, 0
	s.mu.Unlock()

	merge := func(own map[string]string) map[string]string {
		t := tags()
		for k, v := range own {
			t[k] = v
		}
		return t
	}
	ts := model.Ms(now)
	var out []model.Series
	add := func(name, typ, unit string, v float64, own map[string]string) {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return
		}
		out = append(out, model.Series{Metric: name, Type: typ, Unit: unit, Tags: merge(own), Points: []model.Point{{TS: ts, Value: v}}})
	}
	for _, a := range contexts {
		switch a.kind {
		case "count":
			add(a.name, model.TypeCount, "", a.value, a.tags)
		case "gauge":
			add(a.name, model.TypeGauge, "", a.value, a.tags)
		case "set":
			add(a.name, model.TypeGauge, "", float64(len(a.set)), a.tags)
		case "distribution":
			if len(a.samples) == 0 {
				continue
			}
			unit := ""
			if a.isTimer {
				unit = "millisecond"
			}
			sort.Float64s(a.samples)
			sum := 0.0
			for _, v := range a.samples {
				sum += v
			}
			add(a.name+".avg", model.TypeDistribution, unit, sum/float64(len(a.samples)), a.tags)
			add(a.name+".max", model.TypeDistribution, unit, a.samples[len(a.samples)-1], a.tags)
			add(a.name+".p50", model.TypeDistribution, unit, percentile(a.samples, 0.50), a.tags)
			add(a.name+".p95", model.TypeDistribution, unit, percentile(a.samples, 0.95), a.tags)
			add(a.name+".p99", model.TypeDistribution, unit, percentile(a.samples, 0.99), a.tags)
			add(a.name+".count", model.TypeDistribution, "count", a.count, a.tags)
		}
	}
	for i := range checks {
		checks[i].Tags = merge(checks[i].Tags)
	}
	for i := range events {
		events[i].Tags = merge(events[i].Tags)
	}
	return out, checks, events, dropped
}

// percentile : rang le plus proche sur des valeurs triées.
func percentile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	i := int(math.Ceil(q*float64(len(sorted)))) - 1
	return sorted[max(0, min(i, len(sorted)-1))]
}
