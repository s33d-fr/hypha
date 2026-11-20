package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── Wazuh : API REST du manager (55000) ─────────────────────────────────────────────────────
//
// Compte de l'API au rôle « readonly » (ou un compte qui lit manager:read et agent:read). Jeton
// JWT gardé jusqu'à 60 s de sa fin. État des démons du manager, agents par état de connexion,
// statistiques d'analysisd (événements, alertes, files) et de remoted (sessions, octets).

// Démons sans lesquels le manager ne remplit plus son office : arrêtés, ils passent
// wazuh.manager.daemons en CRIT ; les autres (agentlessd, integratord, maild…) sont facultatifs.
var wazuhCoreDaemons = map[string]bool{
	"wazuh-analysisd": true, "wazuh-remoted": true, "wazuh-db": true, "wazuh-modulesd": true,
	"wazuh-execd": true, "wazuh-logcollector": true, "wazuh-monitord": true, "wazuh-syscheckd": true,
	"wazuh-apid": true, "wazuh-authd": true,
}

type Wazuh struct {
	probe
	cfg      config.ServiceProbe
	http     *httpProbe
	token    string
	tokenEnd time.Time
}

func newWazuh(cfg config.ServiceProbe) *Wazuh {
	return &Wazuh{cfg: cfg, http: newHTTPProbe(cfg.URL, "", "", cfg.Insecure),
		probe: newProbe("wazuh", "wazuh.can_connect", intent(cfg.Enabled, cfg.Username != ""), "instance", cfg.URL)}
}

func (w *Wazuh) period() time.Duration { return 60 * time.Second }

func (w *Wazuh) login(ctx context.Context, now time.Time) error {
	if w.token != "" && now.Before(w.tokenEnd) {
		return nil
	}
	h := newHTTPProbe(w.cfg.URL, w.cfg.Username, w.cfg.Password, w.cfg.Insecure)
	body, code, err := h.do(ctx, http.MethodPost, "/security/user/authenticate", nil, nil)
	if err != nil {
		return err
	}
	if code == http.StatusUnauthorized {
		return fmt.Errorf("compte %q refusé par l'API (HTTP 401)", w.cfg.Username)
	}
	if err := httpStatusErr("/security/user/authenticate", code, nil); err != nil {
		return err
	}
	var r struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err != nil || r.Data.Token == "" {
		return fmt.Errorf("jeton illisible")
	}
	w.token = r.Data.Token
	w.tokenEnd = now.Add(14 * time.Minute) // durée par défaut de l'API : 900 s
	return nil
}

func (w *Wazuh) get(ctx context.Context, path string, out any) error {
	w.http.headers = map[string]string{"Authorization": "Bearer " + w.token}
	err := w.http.getJSON(ctx, path, out)
	if err != nil && strings.Contains(err.Error(), "HTTP 401") {
		w.token = ""
	}
	return err
}

func (w *Wazuh) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := w.det.skip(now); skip {
		return nil, st
	}
	if w.cfg.Username == "" && w.det.mode == "auto" {
		return nil, "absent" // l'API exige un compte
	}
	ts := model.Ms(now)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var info struct {
		Data struct {
			Version string `json:"api_version"`
		} `json:"data"`
	}
	err := w.login(ctx, now)
	if err == nil {
		err = w.get(ctx, "/", &info)
	}
	if err != nil {
		return w.fail(ts, tags, err)
	}
	w.det.ok()
	w.begin()
	defer w.end()
	kv := w.kvs()
	var checks []model.Check
	var errs []string

	var status struct {
		Data struct {
			Items []map[string]string `json:"affected_items"`
		} `json:"data"`
	}
	if err := w.get(ctx, "/manager/status", &status); err == nil && len(status.Data.Items) > 0 {
		var down []string
		running := 0.0
		names := make([]string, 0, len(status.Data.Items[0]))
		for d := range status.Data.Items[0] {
			names = append(names, d)
		}
		sort.Strings(names)
		for _, d := range names {
			st := status.Data.Items[0][d]
			if st == "running" {
				running++
			} else if wazuhCoreDaemons[d] || st == "failed" {
				down = append(down, d+" ("+st+")")
			}
		}
		b.Gauge("wazuh.manager.daemons.running", "process", running, kv...)
		cst, msg := model.CheckOK, fmt.Sprintf("%g démons en marche", running)
		if len(down) > 0 {
			cst, msg = model.CheckCrit, "démons arrêtés : "+strings.Join(down, ", ")
		}
		checks = append(checks, model.Check{TS: ts, Name: "wazuh.manager.daemons", Status: cst, Message: msg, Tags: tags(kv...)})
	} else if err != nil {
		errs = append(errs, err.Error())
	}

	var summary struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := w.get(ctx, "/agents/summary/status", &summary); err == nil {
		// 4.4 et plus : {"connection": {...}, "configuration": {...}} ; avant : à plat.
		conn := summary.Data
		if raw, ok := summary.Data["connection"]; ok {
			_ = json.Unmarshal(raw, &conn)
		}
		for _, s := range []string{"active", "disconnected", "never_connected", "pending"} {
			var v float64
			if json.Unmarshal(conn[s], &v) == nil {
				b.Gauge("wazuh.agents", "agent", v, w.kvs("status", s)...)
			}
		}
		var total float64
		if json.Unmarshal(conn["total"], &total) == nil {
			b.Gauge("wazuh.agents.total", "agent", total, kv...)
		}
	} else {
		errs = append(errs, err.Error())
	}

	var analysisd struct {
		Data struct {
			Items []map[string]any `json:"affected_items"`
		} `json:"data"`
	}
	if err := w.get(ctx, "/manager/stats/analysisd", &analysisd); err == nil && len(analysisd.Data.Items) > 0 {
		a := analysisd.Data.Items[0]
		for k, m := range map[string]string{"events_received": "wazuh.analysisd.events.received",
			"events_processed": "wazuh.analysisd.events.processed", "events_dropped": "wazuh.analysisd.events.dropped",
			"total_events_decoded": "wazuh.analysisd.events.decoded", "alerts_written": "wazuh.analysisd.alerts_written",
			"firewall_written": "wazuh.analysisd.firewall_written"} {
			if v, ok := num(a[k]); ok && a[k] != nil {
				w.count(b, m, "event", k, v, kv...)
			}
		}
		for k, m := range map[string]string{"event_queue_usage": "wazuh.analysisd.event_queue.usage",
			"alerts_queue_usage": "wazuh.analysisd.alerts_queue.usage", "archives_queue_usage": "wazuh.analysisd.archives_queue.usage"} {
			if v, ok := num(a[k]); ok && a[k] != nil {
				b.Gauge(m, "percent", round1(100*v), kv...)
			}
		}
	}

	var remoted struct {
		Data struct {
			Items []map[string]any `json:"affected_items"`
		} `json:"data"`
	}
	if err := w.get(ctx, "/manager/stats/remoted", &remoted); err == nil && len(remoted.Data.Items) > 0 {
		r := remoted.Data.Items[0]
		if v, ok := num(r["tcp_sessions"]); ok && r["tcp_sessions"] != nil {
			b.Gauge("wazuh.remoted.tcp_sessions", "session", v, kv...)
		}
		q, ok1 := num(r["queue_size"])
		t, ok2 := num(r["total_queue_size"])
		if ok1 && ok2 && r["queue_size"] != nil && t > 0 {
			b.Gauge("wazuh.remoted.queue.usage", "percent", round1(100*q/t), kv...)
		}
		for k, m := range map[string][2]string{"recv_bytes": {"wazuh.remoted.bytes.received", "byte"},
			"sent_bytes": {"wazuh.remoted.bytes.sent", "byte"}, "evt_count": {"wazuh.remoted.events", "event"},
			"discarded_count": {"wazuh.remoted.discarded", "message"}, "ctrl_msg_count": {"wazuh.remoted.control_messages", "message"}} {
			if v, ok := num(r[k]); ok && r[k] != nil {
				w.count(b, m[0], m[1], "remoted/"+k, v, kv...)
			}
		}
	}

	checks = append(checks, w.okCheck(ts, tags, "API Wazuh "+info.Data.Version+" joignable"))
	if len(errs) > 0 {
		return checks, "partiel : " + strings.Join(errs, " ; ")
	}
	return checks, "ok"
}
