package integrations

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── Sonatype Nexus Repository : API REST ────────────────────────────────────────────────────
//
// /service/rest/v1/status (disponible, sans compte) et /status/writable (lecture seule ?) ;
// avec un compte portant nx-metrics-all : /status/check (contrôles de santé du système) et
// /service/metrics/data (mesures Dropwizard : JVM, requêtes HTTP) ; avec nx-blobstores-read :
// /service/rest/v1/blobstores (taille et place libre des magasins de blobs).

type Nexus struct {
	probe
	cfg  config.ServiceProbe
	http *httpProbe
}

func newNexus(cfg config.ServiceProbe) *Nexus {
	return &Nexus{cfg: cfg, http: newHTTPProbe(cfg.URL, cfg.Username, cfg.Password, cfg.Insecure),
		probe: newProbe("nexus", "nexus.can_connect", intent(cfg.Enabled, cfg.URL != ""), "instance", cfg.URL)}
}

func (n *Nexus) period() time.Duration { return 60 * time.Second }

type dropwizard struct {
	Gauges map[string]struct {
		Value any `json:"value"`
	} `json:"gauges"`
	Meters map[string]struct {
		Count float64 `json:"count"`
	} `json:"meters"`
	Timers map[string]struct {
		Count float64 `json:"count"`
		Mean  float64 `json:"mean"`
		P95   float64 `json:"p95"`
		P99   float64 `json:"p99"`
		Units string  `json:"duration_units"`
	} `json:"timers"`
}

var nexusGauges = map[string][2]string{
	"jvm.memory.heap.used":             {"nexus.jvm.heap.used", "byte"},
	"jvm.memory.heap.max":              {"nexus.jvm.heap.max", "byte"},
	"jvm.memory.heap.usage":            {"nexus.jvm.heap.usage", "fraction"},
	"jvm.memory.total.used":            {"nexus.jvm.memory.used", "byte"},
	"jvm.thread-states.count":          {"nexus.jvm.threads", "thread"},
	"jvm.thread-states.deadlock.count": {"nexus.jvm.threads.deadlocked", "thread"},
	"jvm.thread-states.blocked.count":  {"nexus.jvm.threads.blocked", "thread"},
	"jvm.vm.uptime":                    {"nexus.uptime", "millisecond"},
	"jvm.fd_usage":                     {"nexus.jvm.fd_usage", "fraction"},
	"readonly.enabled":                 {"nexus.readonly", ""},
}

func (n *Nexus) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := n.det.skip(now); skip {
		return nil, st
	}
	if n.cfg.URL == "" {
		return nil, "absent"
	}
	ts := model.Ms(now)
	_, code, err := n.http.do(ctx, http.MethodGet, "/service/rest/v1/status", nil, nil)
	if err == nil && code != http.StatusOK {
		err = fmt.Errorf("/service/rest/v1/status : HTTP %d (Nexus indisponible)", code)
	}
	if err != nil {
		return n.fail(ts, tags, err)
	}
	n.det.ok()
	kv := n.kvs()
	var checks []model.Check
	var errs []string
	// Écriture : 200 lecture-écriture, 503 lecture seule (ou base indisponible).
	if _, code, err := n.http.do(ctx, http.MethodGet, "/service/rest/v1/status/writable", nil, nil); err == nil {
		st, msg, v := model.CheckOK, "écriture possible", 1.0
		if code != http.StatusOK {
			st, msg, v = model.CheckCrit, fmt.Sprintf("Nexus en lecture seule ou base indisponible (HTTP %d)", code), 0
		}
		b.Gauge("nexus.writable", "", v, kv...)
		checks = append(checks, model.Check{TS: ts, Name: "nexus.writable", Status: st, Message: msg, Tags: tags(kv...)})
	}
	if n.cfg.Username != "" {
		var health map[string]struct {
			Healthy bool   `json:"healthy"`
			Message string `json:"message"`
		}
		if err := n.http.getJSON(ctx, "/service/rest/v1/status/check", &health); err == nil {
			var bad []string
			names := make([]string, 0, len(health))
			for k := range health {
				names = append(names, k)
			}
			sort.Strings(names)
			for _, k := range names {
				h := health[k]
				v := 0.0
				if h.Healthy {
					v = 1
				} else {
					m := k
					if h.Message != "" {
						m += " (" + model.Truncate(h.Message, 120) + ")"
					}
					bad = append(bad, m)
				}
				b.Gauge("nexus.health_check", "", v, append(append([]string{}, kv...), "check", k)...)
			}
			st, msg := model.CheckOK, fmt.Sprintf("%d contrôles de santé au vert", len(health))
			if len(bad) > 0 {
				st, msg = model.CheckWarn, "en défaut : "+strings.Join(bad, " ; ")
			}
			checks = append(checks, model.Check{TS: ts, Name: "nexus.health", Status: st, Message: model.Truncate(msg, 500), Tags: tags(kv...)})
		} else {
			errs = append(errs, err.Error())
		}
		var dw dropwizard
		if err := n.http.getJSON(ctx, "/service/metrics/data", &dw); err == nil {
			for k, m := range nexusGauges {
				if g, ok := dw.Gauges[k]; ok {
					if v, ok := num(g.Value); ok {
						if m[1] == "fraction" {
							b.Gauge(m[0], "percent", round1(100*v), kv...)
						} else {
							b.Gauge(m[0], m[1], v, kv...)
						}
					}
				}
			}
			n.begin()
			for k, m := range dw.Meters {
				// org.eclipse.jetty.webapp.WebAppContext.2xx-responses, …
				if !strings.HasSuffix(k, "xx-responses") {
					continue
				}
				class := k[len(k)-len("2xx-responses"):][:3]
				n.count(b, "nexus.http.responses", "response", k, m.Count, n.kvs("status_class", class)...)
			}
			for k, t := range dw.Timers {
				if !strings.HasSuffix(k, "WebAppContext.requests") {
					continue
				}
				n.count(b, "nexus.http.requests", "request", k, t.Count, kv...)
				scale := 1.0 // durées rendues en millisecondes, sinon en secondes
				if t.Units == "seconds" {
					scale = 1000
				}
				b.Gauge("nexus.http.request.latency.avg", "millisecond", round1(t.Mean*scale), kv...)
				b.Gauge("nexus.http.request.latency.p95", "millisecond", round1(t.P95*scale), kv...)
			}
			n.end()
		} else {
			errs = append(errs, err.Error())
		}
		var stores []struct {
			Name      string  `json:"name"`
			Type      string  `json:"type"`
			Blobs     float64 `json:"blobCount"`
			Size      float64 `json:"totalSizeInBytes"`
			Available float64 `json:"availableSpaceInBytes"`
		}
		if err := n.http.getJSON(ctx, "/service/rest/v1/blobstores", &stores); err == nil {
			for _, s := range stores {
				skv := append(append([]string{}, kv...), "blobstore", s.Name, "blobstore_type", strings.ToLower(s.Type))
				b.Gauge("nexus.blobstore.size", "byte", s.Size, skv...)
				b.Gauge("nexus.blobstore.available", "byte", s.Available, skv...)
				b.Gauge("nexus.blobstore.blobs", "object", s.Blobs, skv...)
			}
		} else {
			errs = append(errs, err.Error())
		}
	}
	checks = append(checks, n.okCheck(ts, tags, "Nexus disponible"))
	if len(errs) > 0 {
		return checks, "partiel : " + strings.Join(errs, " ; ")
	}
	return checks, "ok"
}
