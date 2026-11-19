package integrations

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── Memcached (§ 38) : commande « stats » du protocole texte ───────────────────────────────

var memcachedCounters = []struct{ field, metric, unit string }{
	{"cmd_get", "memcached.cmd.get", "command"},
	{"cmd_set", "memcached.cmd.set", "command"},
	{"get_hits", "memcached.get.hits", "hit"},
	{"get_misses", "memcached.get.misses", "miss"},
	{"evictions", "memcached.evictions", "item"},
	{"rejected_connections", "memcached.connections.rejected", "connection"},
	{"bytes_read", "memcached.bytes.read", "byte"},
	{"bytes_written", "memcached.bytes.written", "byte"},
}

type Memcached struct {
	cfg  config.MemcachedConfig
	det  detector
	prev map[string]float64
}

func newMemcached(cfg config.MemcachedConfig) *Memcached {
	mode := cfg.Enabled
	if mode == "auto" && cfg.Address != "" && cfg.Address != "127.0.0.1:11211" {
		mode = "true"
	}
	return &Memcached{cfg: cfg, det: detector{mode: mode}}
}

func (m *Memcached) name() string          { return "memcached" }
func (m *Memcached) period() time.Duration { return 30 * time.Second }
func (m *Memcached) close()                {}

func memcachedStats(ctx context.Context, address string) (map[string]string, error) {
	d := net.Dialer{Timeout: 5 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte("stats\r\n")); err != nil {
		return nil, err
	}
	out := map[string]string{}
	sc := bufio.NewScanner(conn)
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if l == "END" {
			return out, nil
		}
		f := strings.Fields(l)
		if len(f) == 3 && f[0] == "STAT" {
			out[f[1]] = f[2]
		} else if strings.HasPrefix(l, "ERROR") || strings.HasPrefix(l, "SERVER_ERROR") || strings.HasPrefix(l, "CLIENT_ERROR") {
			return nil, fmt.Errorf("%s", l)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("réponse incomplète (END absent)")
}

func (m *Memcached) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := m.det.skip(now); skip {
		return nil, st
	}
	ts := model.Ms(now)
	kv := []string{"instance", m.cfg.Address}
	st, err := memcachedStats(ctx, m.cfg.Address)
	if err != nil {
		if silent, s := m.det.failed(err.Error()); silent {
			return nil, s
		}
		return []model.Check{{TS: ts, Name: "memcached.can_connect", Status: model.CheckCrit, Message: err.Error(), Tags: tags(kv...)}},
			"erreur : " + err.Error()
	}
	m.det.ok()
	num := func(k string) (float64, bool) {
		v, err := strconv.ParseFloat(st[k], 64)
		return v, err == nil
	}
	for _, g := range []struct{ field, metric, unit string }{
		{"uptime", "memcached.uptime", "second"}, {"curr_connections", "memcached.connections.current", "connection"},
		{"curr_items", "memcached.items", "item"}, {"bytes", "memcached.bytes", "byte"},
		{"limit_maxbytes", "memcached.bytes.limit", "byte"}, {"threads", "memcached.threads", "thread"},
	} {
		if v, ok := num(g.field); ok {
			b.Gauge(g.metric, g.unit, v, kv...)
		}
	}
	if used, ok := num("bytes"); ok {
		if mx, ok := num("limit_maxbytes"); ok && mx > 0 {
			b.Gauge("memcached.bytes.usage", "percent", round1(100*used/mx), kv...)
		}
	}
	cur := map[string]float64{}
	for _, c := range memcachedCounters {
		if v, ok := num(c.field); ok {
			cur[c.field] = v
		}
	}
	if m.prev != nil {
		for _, c := range memcachedCounters {
			if v, ok := cur[c.field]; ok {
				if d, ok := deltaF(v, m.prev[c.field]); ok {
					b.Add(c.metric, model.TypeCount, c.unit, d, kv...)
				}
			}
		}
		h, ok1 := deltaF(cur["get_hits"], m.prev["get_hits"])
		mi, ok2 := deltaF(cur["get_misses"], m.prev["get_misses"])
		if ok1 && ok2 && h+mi > 0 {
			b.Gauge("memcached.get.hit_rate", "percent", round1(100*h/(h+mi)), kv...)
		}
	}
	m.prev = cur
	return []model.Check{{TS: ts, Name: "memcached.can_connect", Status: model.CheckOK,
		Message: "Memcached " + st["version"] + " joignable", Tags: tags(kv...)}}, "ok"
}
