package integrations

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── Squid : gestionnaire de cache (squid-internal-mgr/counters et info) ──────────────────────
//
// Pages servies par Squid lui-même sur son port (3128) ; « http_access allow localhost manager »
// (conf par défaut) suffit depuis la machine. Un mot de passe cachemgr_passwd se donne par
// password (username : n'importe lequel, le mot de passe seul compte).

var squidCounters = []struct{ field, metric, unit string }{
	{"client_http.requests", "squid.cachemgr.client_http.requests", "request"},
	{"client_http.hits", "squid.cachemgr.client_http.hits", "hit"},
	{"client_http.errors", "squid.cachemgr.client_http.errors", "error"},
	{"client_http.kbytes_in", "squid.cachemgr.client_http.kbytes_in", "kibibyte"},
	{"client_http.kbytes_out", "squid.cachemgr.client_http.kbytes_out", "kibibyte"},
	{"client_http.hit_kbytes_out", "squid.cachemgr.client_http.hit_kbytes_out", "kibibyte"},
	{"server.all.requests", "squid.cachemgr.server.all.requests", "request"},
	{"server.all.errors", "squid.cachemgr.server.all.errors", "error"},
	{"server.all.kbytes_in", "squid.cachemgr.server.all.kbytes_in", "kibibyte"},
	{"server.all.kbytes_out", "squid.cachemgr.server.all.kbytes_out", "kibibyte"},
	{"aborted_requests", "squid.cachemgr.aborted_requests", "request"},
	{"page_faults", "squid.cachemgr.page_faults", "fault"},
	{"cpu_time", "squid.cachemgr.cpu_time", "second"},
}

type Squid struct {
	probe
	cfg  config.ServiceProbe
	http *httpProbe
}

func newSquid(cfg config.ServiceProbe) *Squid {
	explicit := cfg.Password != "" || (cfg.URL != "" && cfg.URL != "http://127.0.0.1:3128")
	return &Squid{cfg: cfg, probe: newProbe("squid", "squid.can_connect", intent(cfg.Enabled, explicit), "instance", cfg.URL),
		http: newHTTPProbe(cfg.URL, cfg.Username, cfg.Password, cfg.Insecure)}
}

func (s *Squid) period() time.Duration { return 30 * time.Second }

// parseKV lit les lignes « clé = valeur » (counters) ; les valeurs non numériques sont ignorées.
func parseSquidKV(data []byte) map[string]float64 {
	out := map[string]float64{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err == nil {
			out[strings.TrimSpace(k)] = f
		}
	}
	return out
}

var (
	squidVersionRe = regexp.MustCompile(`Squid Object Cache: Version (\S+)`)
	squidInfoRe    = regexp.MustCompile(`(?m)^\s*(Number of clients accessing cache|Number of file desc currently in use|Maximum number of file descriptors|Storage Swap size|Storage Mem size|StoreEntries with MemObjects|Number of HTTP requests received):\s*([0-9.]+)`)
)

func (s *Squid) get(ctx context.Context, page string) ([]byte, error) {
	path := "/squid-internal-mgr/" + page
	body, code, err := s.http.do(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}
	if code == http.StatusForbidden || code == http.StatusUnauthorized {
		return nil, fmt.Errorf("%s : accès refusé (HTTP %d : http_access manager, cachemgr_passwd)", path, code)
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("%s : HTTP %d", path, code)
	}
	return body, nil
}

func (s *Squid) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := s.det.skip(now); skip {
		return nil, st
	}
	ts := model.Ms(now)
	body, err := s.get(ctx, "counters")
	var c map[string]float64
	if err == nil {
		c = parseSquidKV(body)
		if _, ok := c["client_http.requests"]; !ok {
			err = fmt.Errorf("la page counters ne ressemble pas à celle de Squid")
		}
	}
	if err != nil {
		return s.fail(ts, tags, err)
	}
	s.det.ok()
	s.begin()
	defer s.end()
	kv := s.kvs()
	for _, x := range squidCounters {
		if v, ok := c[x.field]; ok {
			s.count(b, x.metric, x.unit, x.field, v, kv...)
		}
	}
	if h, ok := s.diff("client_http.hits"); ok {
		if r, ok := s.diff("client_http.requests"); ok && r > 0 {
			b.Gauge("squid.cachemgr.client_http.hit_rate", "percent", round1(100*h/r), kv...)
		}
	}
	version := ""
	if info, err := s.get(ctx, "info"); err == nil {
		if m := squidVersionRe.FindSubmatch(info); m != nil {
			version = string(m[1])
		}
		for _, m := range squidInfoRe.FindAllSubmatch(info, -1) {
			v, _ := strconv.ParseFloat(string(m[2]), 64)
			switch string(m[1]) {
			case "Number of clients accessing cache":
				b.Gauge("squid.clients", "client", v, kv...)
			case "Number of file desc currently in use":
				b.Gauge("squid.fd.used", "file", v, kv...)
			case "Maximum number of file descriptors":
				b.Gauge("squid.fd.max", "file", v, kv...)
			case "Storage Swap size":
				b.Gauge("squid.storage.swap", "kibibyte", v, kv...)
			case "Storage Mem size":
				b.Gauge("squid.storage.mem", "kibibyte", v, kv...)
			case "StoreEntries with MemObjects":
				b.Gauge("squid.objects.in_memory", "object", v, kv...)
			}
		}
	}
	msg := "gestionnaire de cache lu"
	if version != "" {
		msg = "Squid " + version + " joignable"
	}
	return []model.Check{s.okCheck(ts, tags, msg)}, "ok"
}
