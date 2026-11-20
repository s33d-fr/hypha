package integrations

import (
	"context"
	"fmt"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── Fluentd : greffon monitor_agent (/api/plugins.json) ────────────────────────────────────
//
// <source> @type monitor_agent ; bind 127.0.0.1 ; port 24220 </source>. Une série par greffon
// de sortie à tampon : file d'attente, octets en attente, place libre, nouveaux essais,
// enregistrements émis et écrits. Nommer chaque greffon (@id) : sans lui, son identifiant
// change à chaque redémarrage de Fluentd.

const fluentdMaxPlugins = 200

type Fluentd struct {
	probe
	cfg  config.ServiceProbe
	http *httpProbe
}

func newFluentd(cfg config.ServiceProbe) *Fluentd {
	explicit := cfg.URL != "" && cfg.URL != "http://127.0.0.1:24220"
	return &Fluentd{cfg: cfg, http: newHTTPProbe(cfg.URL, cfg.Username, cfg.Password, cfg.Insecure),
		probe: newProbe("fluentd", "fluentd.can_connect", intent(cfg.Enabled, explicit), "instance", cfg.URL)}
}

func (f *Fluentd) period() time.Duration { return 30 * time.Second }

var fluentdGauges = map[string][2]string{
	"buffer_queue_length":                  {"fluentd.buffer_queue_length", "chunk"},
	"buffer_total_queued_size":             {"fluentd.buffer_total_queued_size", "byte"},
	"buffer_available_buffer_space_ratios": {"fluentd.buffer_available_buffer_space_ratios", "percent"},
	"buffer_stage_length":                  {"fluentd.buffer_stage_length", "chunk"},
	"buffer_stage_byte_size":               {"fluentd.buffer_stage_byte_size", "byte"},
	"retry_count":                          {"fluentd.retry_count", "retry"},
}

var fluentdCounters = map[string][2]string{
	"emit_records":     {"fluentd.emit_records", "record"},
	"emit_count":       {"fluentd.emit_count", "event"},
	"write_count":      {"fluentd.write_count", "write"},
	"rollback_count":   {"fluentd.rollback_count", "write"},
	"slow_flush_count": {"fluentd.slow_flush_count", "flush"},
	"flush_time_count": {"fluentd.flush_time_count", "millisecond"},
}

func (f *Fluentd) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := f.det.skip(now); skip {
		return nil, st
	}
	ts := model.Ms(now)
	var resp struct {
		Plugins []map[string]any `json:"plugins"`
	}
	err := f.http.getJSON(ctx, "/api/plugins.json", &resp)
	if err == nil && resp.Plugins == nil {
		err = fmt.Errorf("/api/plugins.json : réponse inattendue")
	}
	if err != nil {
		return f.fail(ts, tags, err)
	}
	f.det.ok()
	f.begin()
	defer f.end()
	outputs, retrying := 0, 0
	for i, p := range resp.Plugins {
		if i >= fluentdMaxPlugins {
			break
		}
		id, _ := p["plugin_id"].(string)
		typ, _ := p["type"].(string)
		cat, _ := p["plugin_category"].(string)
		if cat == "output" {
			outputs++
		}
		kv := f.kvs("plugin_id", id, "type", typ, "plugin_category", cat)
		for k, m := range fluentdGauges {
			if v, ok := num(p[k]); ok && p[k] != nil {
				b.Gauge(m[0], m[1], v, kv...)
				if k == "retry_count" && v > 0 {
					retrying++
				}
			}
		}
		for k, m := range fluentdCounters {
			if v, ok := num(p[k]); ok && p[k] != nil {
				f.count(b, m[0], m[1], id+"/"+k, v, kv...)
			}
		}
	}
	msg := fmt.Sprintf("monitor_agent lu (%d greffons, %d sorties)", len(resp.Plugins), outputs)
	if retrying > 0 {
		msg += fmt.Sprintf(", %d sortie(s) en nouvel essai", retrying)
	}
	return []model.Check{f.okCheck(ts, tags, strings.TrimSpace(msg))}, "ok"
}
