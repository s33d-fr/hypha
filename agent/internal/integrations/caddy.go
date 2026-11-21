package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── Caddy : API d'administration, en lecture seule ──────────────────────────────────────────
//
// GET /reverse_proxy/upstreams (serveurs amont de tous les mandataires : requêtes en cours, échecs
// comptés par la santé passive) et GET /config/apps/http/servers (serveurs HTTP et routes
// configurés). Rien n'est jamais écrit (aucun POST, PATCH ni DELETE). Les mesures Prometheus (requêtes,
// durées, santé des amonts, rechargement de la configuration) passent par le profil OpenMetrics
// « caddy » sur le même point (/metrics).

const caddyMaxUpstreams = 200

type Caddy struct {
	probe
	cfg  config.CaddyConfig
	http *httpProbe
}

func newCaddy(cfg config.CaddyConfig) *Caddy {
	h := newHTTPProbe(cfg.URL, "", "", cfg.Insecure)
	h.maxBytes = 8 << 20
	return &Caddy{cfg: cfg, http: h,
		probe: newProbe("caddy", "caddy.can_connect", intent(cfg.Enabled, cfg.URL != "http://127.0.0.1:2019"), "instance", cfg.URL)}
}

func (c *Caddy) period() time.Duration { return 30 * time.Second }

type caddyUpstream struct {
	Address     string  `json:"address"`
	NumRequests float64 `json:"num_requests"`
	Fails       float64 `json:"fails"`
}

func (c *Caddy) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := c.det.skip(now); skip {
		return nil, st
	}
	ts := model.Ms(now)
	// /config/apps/http/servers rend null sans application HTTP : un 200 suffit à dire que l'API
	// d'administration répond (et que c'est bien Caddy : un autre logiciel n'a pas ce chemin).
	data, code, err := c.http.do(ctx, http.MethodGet, "/config/apps/http/servers", nil, nil)
	if err == nil {
		err = httpStatusErr("/config/apps/http/servers", code, data)
	}
	var servers map[string]struct {
		Listen []string          `json:"listen"`
		Routes []json.RawMessage `json:"routes"`
	}
	if err == nil {
		if jerr := json.Unmarshal(data, &servers); jerr != nil {
			err = fmt.Errorf("/config/apps/http/servers : réponse illisible (Caddy 2 attendu)")
		}
	}
	if err != nil {
		return c.fail(ts, tags, err)
	}
	c.det.ok()
	kv := c.kvs()
	routes := 0
	for _, s := range servers {
		routes += len(s.Routes)
	}
	b.Gauge("caddy.servers", "server", float64(len(servers)), kv...)
	b.Gauge("caddy.routes", "route", float64(routes), kv...)

	var ups []caddyUpstream
	if err := c.http.getJSON(ctx, "/reverse_proxy/upstreams", &ups); err != nil {
		conn := c.okCheck(ts, tags, fmt.Sprintf("API d'administration : %d serveur(s) HTTP", len(servers)))
		conn.Status, conn.Message = model.CheckWarn, conn.Message+" ; serveurs amont illisibles : "+model.Truncate(err.Error(), 200)
		return []model.Check{conn}, "partiel : " + model.Truncate(err.Error(), 200)
	}
	failing := 0
	for i, u := range ups {
		if i >= caddyMaxUpstreams {
			break
		}
		ukv := c.kvs("upstream", model.Truncate(u.Address, 100))
		b.Gauge("caddy.upstream.requests", "request", u.NumRequests, ukv...)
		b.Gauge("caddy.upstream.fails", "error", u.Fails, ukv...)
		if u.Fails > 0 {
			failing++
		}
	}
	b.Gauge("caddy.upstreams", "server", float64(len(ups)), kv...)
	b.Gauge("caddy.upstreams.failing", "server", float64(failing), kv...)
	msg := fmt.Sprintf("API d'administration : %d serveur(s) HTTP, %d route(s), %d serveur(s) amont", len(servers), routes, len(ups))
	return []model.Check{c.okCheck(ts, tags, msg)}, "ok"
}
