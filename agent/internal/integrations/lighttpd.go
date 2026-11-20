package integrations

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── Lighttpd : mod_status, page « ?auto » ───────────────────────────────────────────────────
//
// status.status-url = "/server-status" (limité à 127.0.0.1 dans la conf de lighttpd) ; l'agent
// lit <url>?auto. Sans url, la sonde reste inactive : le port 80 de l'hôte est d'abord celui
// d'Apache ou de nginx.

type Lighttpd struct {
	probe
	cfg    config.ServiceProbe
	http   *httpProbe
	lastAt time.Time
}

func newLighttpd(cfg config.ServiceProbe) *Lighttpd {
	u := cfg.URL
	if u != "" && !strings.Contains(u, "?") {
		u += "?auto"
	}
	return &Lighttpd{cfg: cfg, probe: newProbe("lighttpd", "lighttpd.can_connect", intent(cfg.Enabled, cfg.URL != ""), "instance", cfg.URL),
		http: newHTTPProbe(u, cfg.Username, cfg.Password, cfg.Insecure)}
}

func (l *Lighttpd) period() time.Duration { return 15 * time.Second }

// parseLighttpdStatus : « Total Accesses: 12 », « BusyServers: 1 »… ; au moins Uptime et
// BusyServers.
func parseLighttpdStatus(text string) (map[string]float64, error) {
	out := map[string]float64{}
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			out[strings.TrimSpace(k)] = f
		}
	}
	_, up := out["Uptime"]
	_, busy := out["BusyServers"]
	if !up || !busy {
		return nil, fmt.Errorf("la page ne ressemble pas à server-status?auto de lighttpd")
	}
	return out, nil
}

func (l *Lighttpd) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if l.cfg.URL == "" && l.cfg.Enabled != "false" {
		return nil, "absent"
	}
	if skip, st := l.det.skip(now); skip {
		return nil, st
	}
	ts := model.Ms(now)
	body, code, err := l.http.do(ctx, http.MethodGet, "", nil, nil)
	var s map[string]float64
	if err == nil {
		if err = httpStatusErr("server-status", code, body); err == nil {
			s, err = parseLighttpdStatus(string(body))
		}
	}
	if err != nil {
		return l.fail(ts, tags, err)
	}
	l.det.ok()
	l.begin()
	defer l.end()
	dt := now.Sub(l.lastAt).Seconds()
	l.lastAt = now
	kv := l.kvs()
	b.Gauge("lighttpd.performance.uptime", "second", s["Uptime"], kv...)
	b.Gauge("lighttpd.performance.busy_servers", "connection", s["BusyServers"], kv...)
	if v, ok := s["IdleServers"]; ok {
		b.Gauge("lighttpd.performance.idle_server", "connection", v, kv...)
	}
	if v, ok := s["Total Accesses"]; ok {
		b.Gauge("lighttpd.net.hits", "request", v, kv...)
		l.count(b, "lighttpd.net.requests", "request", "accesses", v, kv...)
		if d, ok := l.diff("accesses"); ok && dt > 0 {
			b.Gauge("lighttpd.net.request_per_s", "operation_per_second", round1(d/dt), kv...)
		}
	}
	if v, ok := s["Total kBytes"]; ok {
		b.Gauge("lighttpd.net.bytes", "byte", v*1024, kv...)
		l.count(b, "lighttpd.net.bytes_sent", "byte", "kbytes", v*1024, kv...)
		if d, ok := l.diff("kbytes"); ok && dt > 0 {
			b.Gauge("lighttpd.net.bytes_per_s", "byte_per_second", round1(d/dt), kv...)
		}
	}
	return []model.Check{l.okCheck(ts, tags, fmt.Sprintf("server-status lu (%g connexions actives)", s["BusyServers"]))}, "ok"
}
