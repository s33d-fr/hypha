package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── Varnish : varnishstat -j, santé des serveurs d'origine par varnishadm backend.list -j ────
//
// command : la commande de base (« varnishstat », « varnishstat -n nom », ou « docker exec
// varnish varnishstat » pour un Varnish en conteneur) ; l'agent y ajoute -j. varnishadm est pris
// au même endroit (même préfixe, même -n) ; sans accès au secret, la santé des serveurs
// d'origine n'est pas lue et le reste continue.

// Compteurs de MAIN retenus : écart par intervalle (flag « c ») ou valeur (flag « g »).
var varnishMain = []struct{ name, unit string }{
	{"uptime", "second"}, {"sess_conn", "connection"}, {"sess_fail", "connection"}, {"sess_dropped", "connection"},
	{"sess_queued", "connection"}, {"client_req", "request"}, {"client_req_400", "request"}, {"client_resp_500", "response"},
	{"cache_hit", "hit"}, {"cache_hitpass", "hit"}, {"cache_hitmiss", "miss"}, {"cache_miss", "miss"},
	{"backend_conn", "connection"}, {"backend_unhealthy", "connection"}, {"backend_busy", "connection"},
	{"backend_fail", "connection"}, {"backend_reuse", "connection"}, {"backend_recycle", "connection"},
	{"backend_retry", "connection"}, {"fetch_failed", "fetch"}, {"n_object", "object"}, {"n_expired", "object"},
	{"n_lru_nuked", "object"}, {"threads", "thread"}, {"threads_limited", "thread"}, {"threads_created", "thread"},
	{"threads_failed", "thread"}, {"thread_queue_len", "request"}, {"s_pipe", "request"}, {"s_pass", "request"},
	{"s_fetch", "request"}, {"s_synth", "request"}, {"s_resp_hdrbytes", "byte"}, {"s_resp_bodybytes", "byte"},
	{"s_req_hdrbytes", "byte"}, {"s_req_bodybytes", "byte"}, {"esi_errors", "error"}, {"bans", "ban"},
}

var varnishMetric = map[string]string{
	"uptime": "varnish.uptime", "sess_conn": "varnish.sess_conn", "sess_fail": "varnish.sess_fail",
	"sess_dropped": "varnish.sess_dropped", "sess_queued": "varnish.sess_queued", "client_req": "varnish.client_req",
	"client_req_400": "varnish.client_req_400", "client_resp_500": "varnish.client_resp_500",
	"cache_hit": "varnish.cache_hit", "cache_hitpass": "varnish.cache_hitpass", "cache_hitmiss": "varnish.cache_hitmiss",
	"cache_miss": "varnish.cache_miss", "backend_conn": "varnish.backend_conn", "backend_unhealthy": "varnish.backend_unhealthy",
	"backend_busy": "varnish.backend_busy", "backend_fail": "varnish.backend_fail", "backend_reuse": "varnish.backend_reuse",
	"backend_recycle": "varnish.backend_recycle", "backend_retry": "varnish.backend_retry", "fetch_failed": "varnish.fetch_failed",
	"n_object": "varnish.n_object", "n_expired": "varnish.n_expired", "n_lru_nuked": "varnish.n_lru_nuked",
	"threads": "varnish.threads", "threads_limited": "varnish.threads_limited", "threads_created": "varnish.threads_created",
	"threads_failed": "varnish.threads_failed", "thread_queue_len": "varnish.thread_queue_len", "s_pipe": "varnish.s_pipe",
	"s_pass": "varnish.s_pass", "s_fetch": "varnish.s_fetch", "s_synth": "varnish.s_synth",
	"s_resp_hdrbytes": "varnish.s_resp_hdrbytes", "s_resp_bodybytes": "varnish.s_resp_bodybytes",
	"s_req_hdrbytes": "varnish.s_req_hdrbytes", "s_req_bodybytes": "varnish.s_req_bodybytes",
	"esi_errors": "varnish.esi_errors", "bans": "varnish.bans",
}

type varnishCounter struct {
	Flag  string  `json:"flag"`
	Value float64 `json:"value"`
}

type Varnish struct {
	probe
	cfg config.CommandProbe
}

func newVarnish(cfg config.CommandProbe) *Varnish {
	explicit := len(cfg.Command) != 1 || cfg.Command[0] != "varnishstat"
	return &Varnish{cfg: cfg, probe: newProbe("varnish", "varnish.can_connect", intent(cfg.Enabled, explicit))}
}

func (v *Varnish) period() time.Duration { return 30 * time.Second }

// parseVarnishstat lit les deux formes de varnishstat -j : « counters » (6.5 et plus) ou les
// compteurs au premier niveau (avant).
func parseVarnishstat(data []byte) (map[string]varnishCounter, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, fmt.Errorf("varnishstat -j illisible : %v", err)
	}
	if raw, ok := top["counters"]; ok {
		if err := json.Unmarshal(raw, &top); err != nil {
			return nil, fmt.Errorf("varnishstat -j illisible : %v", err)
		}
	}
	out := map[string]varnishCounter{}
	for k, raw := range top {
		var c varnishCounter
		if json.Unmarshal(raw, &c) == nil && c.Flag != "" {
			out[k] = c
		}
	}
	if _, ok := out["MAIN.uptime"]; !ok {
		return nil, fmt.Errorf("varnishstat -j : MAIN.uptime absent")
	}
	return out, nil
}

// adminCommand : la commande varnishadm qui correspond à command (même préfixe, mêmes options).
func adminCommand(cmd []string) []string {
	out := append([]string{}, cmd...)
	for i, a := range out {
		if filepath.Base(a) == "varnishstat" {
			out[i] = strings.TrimSuffix(a, "varnishstat") + "varnishadm"
			return out
		}
	}
	return nil
}

type varnishBackend struct {
	name, health, probe string
}

// parseBackendList lit « varnishadm backend.list -j » : [version, args, horodatage, {nom: {…}}].
func parseBackendList(data []byte) ([]varnishBackend, error) {
	var arr []json.RawMessage
	if err := json.Unmarshal(data, &arr); err != nil || len(arr) < 4 {
		return nil, fmt.Errorf("backend.list -j illisible")
	}
	var m map[string]struct {
		AdminHealth  string          `json:"admin_health"`
		ProbeMessage json.RawMessage `json:"probe_message"`
	}
	if err := json.Unmarshal(arr[3], &m); err != nil {
		return nil, fmt.Errorf("backend.list -j illisible : %v", err)
	}
	var out []varnishBackend
	for name, x := range m {
		health, detail := "", ""
		var s string
		var list []any
		switch {
		case json.Unmarshal(x.ProbeMessage, &s) == nil:
			health = s
		case json.Unmarshal(x.ProbeMessage, &list) == nil && len(list) >= 3:
			health = fmt.Sprint(list[len(list)-1])
			detail = fmt.Sprintf("%v/%v", list[0], list[1])
		}
		switch strings.ToLower(x.AdminHealth) {
		case "sick":
			health = "sick (forcé)"
		case "healthy":
			health = "healthy (forcé)"
		}
		out = append(out, varnishBackend{name: name, health: health, probe: detail})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

func (v *Varnish) run(ctx context.Context, cmd []string, args ...string) ([]byte, error) {
	if len(cmd) == 0 {
		return nil, fmt.Errorf("commande vide")
	}
	path := cmd[0]
	if !filepath.IsAbs(path) {
		p, ok := findTool(path)
		if !ok {
			return nil, fmt.Errorf("%s introuvable", path)
		}
		path = p
	}
	return runTool(ctx, v.cfg.Timeout.D(), path, append(append([]string{}, cmd[1:]...), args...)...)
}

func (v *Varnish) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := v.det.skip(now); skip {
		return nil, st
	}
	ts := model.Ms(now)
	out, err := v.run(ctx, v.cfg.Command, "-j")
	var c map[string]varnishCounter
	if err == nil {
		c, err = parseVarnishstat(out)
	}
	if err != nil {
		return v.fail(ts, tags, err)
	}
	v.det.ok()
	v.begin()
	defer v.end()
	kv := v.kvs()
	for _, m := range varnishMain {
		x, ok := c["MAIN."+m.name]
		if !ok {
			continue
		}
		if x.Flag == "c" && m.name != "uptime" {
			v.count(b, varnishMetric[m.name], m.unit, m.name, x.Value, kv...)
		} else {
			b.Gauge(varnishMetric[m.name], m.unit, x.Value, kv...)
		}
	}
	v.ratio(b, "varnish.cache_hit_rate", "cache_hit", "cache_miss", kv...)
	// Stockage (SMA : malloc, SMF : fichier) : octets occupés et libres par stockage.
	for k, x := range c {
		parts := strings.SplitN(k, ".", 3)
		if len(parts) != 3 || (parts[0] != "SMA" && parts[0] != "SMF") || parts[1] == "Transient" {
			continue
		}
		skv := v.kvs("storage", parts[1])
		switch parts[2] {
		case "g_bytes":
			b.Gauge("varnish.storage.used", "byte", x.Value, skv...)
		case "g_space":
			b.Gauge("varnish.storage.free", "byte", x.Value, skv...)
		case "c_fail":
			v.count(b, "varnish.storage.alloc_failed", "allocation", k, x.Value, skv...)
		}
	}
	// Serveurs d'origine (VBE.<vcl>.<nom>.<compteur>) : connexions, requêtes, échecs.
	for k, x := range c {
		if !strings.HasPrefix(k, "VBE.") {
			continue
		}
		i := strings.LastIndexByte(k, '.')
		backend, field := k[4:i], k[i+1:]
		bkv := v.kvs("backend", backend)
		switch field {
		case "conn":
			b.Gauge("varnish.backend.connections", "connection", x.Value, bkv...)
		case "req":
			v.count(b, "varnish.backend.requests", "request", k, x.Value, bkv...)
		case "fail":
			v.count(b, "varnish.backend.failures", "connection", k, x.Value, bkv...)
		}
	}
	var checks []model.Check
	if mgt, ok := c["MGT.child_panic"]; ok {
		v.count(b, "varnish.child_panic", "occurrence", "child_panic", mgt.Value, kv...)
	}
	if adm := adminCommand(v.cfg.Command); adm != nil {
		if out, err := v.run(ctx, adm, "backend.list", "-j"); err == nil {
			if list, err := parseBackendList(out); err == nil {
				for _, be := range list {
					st, msg := model.CheckOK, "serveur d'origine "+be.health
					if strings.HasPrefix(be.health, "sick") {
						st = model.CheckCrit
					}
					if be.probe != "" {
						msg += " (sondes " + be.probe + ")"
					}
					healthy := 0.0
					if st == model.CheckOK {
						healthy = 1
					}
					b.Gauge("varnish.backend.healthy", "", healthy, v.kvs("backend", be.name)...)
					checks = append(checks, model.Check{TS: ts, Name: "varnish.backend_healthy", Status: st, Message: msg,
						Tags: tags(v.kvs("backend", be.name)...)})
				}
			}
		}
	}
	checks = append(checks, v.okCheck(ts, tags, fmt.Sprintf("varnishstat lu (%d compteurs)", len(c))))
	return checks, "ok"
}
