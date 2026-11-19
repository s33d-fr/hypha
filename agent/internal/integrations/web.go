package integrations

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── Apache (mod_status, ?auto) ───────────────────────────────────────────────────────────

type Apache struct {
	cfg    config.URLIntegration
	det    detector
	client *http.Client
	prev   *apacheStatus
	prevAt time.Time
}

func newApache(cfg config.URLIntegration) *Apache {
	return &Apache{cfg: cfg, det: detector{mode: cfg.Enabled}, client: &http.Client{Timeout: 5 * time.Second}}
}

func (a *Apache) name() string          { return "apache" }
func (a *Apache) close()                {}
func (a *Apache) period() time.Duration { return 15 * time.Second }

type apacheStatus struct {
	accesses, kbytes            int64
	busy, idle, conns           int64
	hasAccess, hasConns         bool
}

// parseApacheStatus lit la page « server-status?auto » (Total Accesses, Total kBytes,
// BusyWorkers, IdleWorkers, ConnsTotal).
func parseApacheStatus(text string) (apacheStatus, error) {
	var s apacheStatus
	found := 0
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			continue
		}
		switch strings.TrimSpace(k) {
		case "Total Accesses":
			s.accesses, s.hasAccess = n, true
		case "Total kBytes":
			s.kbytes = n
		case "BusyWorkers":
			s.busy = n
			found++
		case "IdleWorkers":
			s.idle = n
			found++
		case "ConnsTotal":
			s.conns, s.hasConns = n, true
		}
	}
	if found < 2 {
		return s, fmt.Errorf("la page ne ressemble pas à server-status?auto")
	}
	return s, nil
}

func (a *Apache) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := a.det.skip(now); skip {
		return nil, st
	}
	ts := model.Ms(now)
	body, err := httpGet(ctx, a.client, a.cfg.URL)
	var s apacheStatus
	if err == nil {
		s, err = parseApacheStatus(string(body))
	}
	if err != nil {
		if silent, st := a.det.failed(err.Error()); silent {
			return nil, st
		} else {
			return []model.Check{canConnect("apache", ts, false, err.Error(), tags("url", a.cfg.URL))}, st
		}
	}
	a.det.ok()
	b.Gauge("apache.performance.busy_workers", "count", float64(s.busy))
	b.Gauge("apache.performance.idle_workers", "count", float64(s.idle))
	if s.hasConns {
		b.Gauge("apache.conns_total", "count", float64(s.conns))
	}
	if s.hasAccess {
		b.Gauge("apache.net.hits", "count", float64(s.accesses))
	}
	if a.prev != nil {
		if v, ok := rateOf(s.kbytes*1024, a.prev.kbytes*1024, now.Sub(a.prevAt).Seconds()); ok && s.hasAccess {
			b.Gauge("apache.net.bytes_per_s", "byte_per_second", v)
		}
		if v, ok := rateOf(s.accesses, a.prev.accesses, now.Sub(a.prevAt).Seconds()); ok && s.hasAccess {
			b.Gauge("apache.net.request_per_s", "operation_per_second", v)
		}
	}
	a.prev, a.prevAt = &s, now
	return []model.Check{canConnect("apache", ts, true, "server-status lu", tags("url", a.cfg.URL))}, "ok"
}

// ── BIND (canal de statistiques JSON, sinon rndc status) ─────────────────────────────────

type Bind struct {
	cfg    config.URLIntegration
	det    detector
	client *http.Client
	prev   *bindStats
	prevAt time.Time
}

func newBind(cfg config.URLIntegration) *Bind {
	return &Bind{cfg: cfg, det: detector{mode: cfg.Enabled}, client: &http.Client{Timeout: 5 * time.Second}}
}

func (x *Bind) name() string          { return "bind" }
func (x *Bind) close()                {}
func (x *Bind) period() time.Duration { return 60 * time.Second }

type bindZone struct {
	name, view string
	serial     int64
}

type bindStats struct {
	qtypes           map[string]int64
	nxdomain, servfl int64
	hasRcodes        bool
	recursive        int64
	hasRecursive     bool
	zones            []bindZone
	source           string
}

// parseBindJSON lit la sortie de http://…:8053/json (BIND ≥ 9.10).
func parseBindJSON(data []byte) (*bindStats, error) {
	var doc struct {
		Qtypes  map[string]int64 `json:"qtypes"`
		Rcodes  map[string]int64 `json:"rcodes"`
		Nsstats map[string]int64 `json:"nsstats"`
		Views   map[string]struct {
			Zones []struct {
				Name   string `json:"name"`
				Serial any    `json:"serial"`
			} `json:"zones"`
		} `json:"views"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("statistiques BIND illisibles : %w", err)
	}
	if doc.Qtypes == nil && doc.Nsstats == nil {
		return nil, fmt.Errorf("statistiques BIND sans qtypes ni nsstats")
	}
	s := &bindStats{qtypes: doc.Qtypes, source: "json"}
	if s.qtypes == nil {
		s.qtypes = map[string]int64{}
	}
	if doc.Rcodes != nil {
		s.nxdomain, s.servfl, s.hasRcodes = doc.Rcodes["NXDOMAIN"], doc.Rcodes["SERVFAIL"], true
	}
	if v, ok := doc.Nsstats["RecursClients"]; ok {
		s.recursive, s.hasRecursive = v, true
	}
	for view, v := range doc.Views {
		if view == "_bind" {
			continue
		}
		for _, z := range v.Zones {
			var serial int64 = -1
			switch t := z.Serial.(type) {
			case float64:
				serial = int64(t)
			case string:
				serial, _ = strconv.ParseInt(t, 10, 64)
			}
			if serial >= 0 {
				s.zones = append(s.zones, bindZone{name: z.Name, view: view, serial: serial})
			}
		}
	}
	return s, nil
}

var rndcRecursiveRe = regexp.MustCompile(`(?m)^recursive clients:\s*(\d+)`)

func (x *Bind) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := x.det.skip(now); skip {
		return nil, st
	}
	ts := model.Ms(now)
	var s *bindStats
	body, err := httpGet(ctx, x.client, x.cfg.URL)
	if err == nil {
		s, err = parseBindJSON(body)
	}
	if err != nil {
		if rndc, ok := findTool("rndc"); ok {
			out, rerr := runTool(ctx, 10*time.Second, rndc, "status")
			if rerr == nil {
				s, err = &bindStats{source: "rndc"}, nil
				if m := rndcRecursiveRe.FindSubmatch(out); m != nil {
					s.recursive, _ = strconv.ParseInt(string(m[1]), 10, 64)
					s.hasRecursive = true
				}
			}
		}
	}
	if err != nil {
		if silent, st := x.det.failed(err.Error()); silent {
			return nil, st
		} else {
			return []model.Check{canConnect("bind", ts, false, err.Error(), tags())}, st
		}
	}
	x.det.ok()
	dt := 0.0
	if x.prev != nil {
		dt = now.Sub(x.prevAt).Seconds()
	}
	for _, t := range model.SortedKeys(s.qtypes) {
		if x.prev != nil {
			if v, ok := rateOf(s.qtypes[t], x.prev.qtypes[t], dt); ok {
				b.Gauge("bind.queries", "operation_per_second", v, "type", t)
			}
		}
	}
	if x.prev != nil && s.hasRcodes && x.prev.hasRcodes {
		if v, ok := rateOf(s.nxdomain, x.prev.nxdomain, dt); ok {
			b.Gauge("bind.responses.nxdomain", "operation_per_second", v)
		}
		if v, ok := rateOf(s.servfl, x.prev.servfl, dt); ok {
			b.Gauge("bind.responses.servfail", "operation_per_second", v)
		}
	}
	if s.hasRecursive {
		b.Gauge("bind.recursive_clients", "count", float64(s.recursive))
	}
	for _, z := range s.zones {
		b.Gauge("bind.zone.serial", "", float64(z.serial), "zone", z.name, "view", z.view)
	}
	x.prev, x.prevAt = s, now
	msg := "statistiques JSON lues"
	state := "ok"
	if s.source == "rndc" {
		msg = "rndc status lu (canal de statistiques JSON injoignable : requêtes et zones non relevées)"
		state = "partiel : canal de statistiques JSON injoignable, rndc status seul"
	}
	return []model.Check{canConnect("bind", ts, true, msg, tags())}, state
}
