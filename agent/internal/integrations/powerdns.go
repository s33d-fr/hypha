package integrations

import (
	"context"
	"fmt"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── PowerDNS : API du serveur web intégré (serveur faisant autorité et Recursor) ────────────
//
// webserver=yes, api=yes, api-key=… dans la conf de PowerDNS ; l'agent lit
// /api/v1/servers/<server_id>/statistics avec l'en-tête X-API-Key. Serveur faisant autorité :
// port 8081 par défaut ; Recursor : 8082. Les compteurs cumulés donnent un écart par intervalle,
// les jauges (latences, tailles de cache, requêtes en cours) leur valeur.

var pdnsAuthStats = map[string]string{
	"udp-queries": "powerdns.authoritative.udp_queries", "tcp-queries": "powerdns.authoritative.tcp_queries",
	"udp-answers": "powerdns.authoritative.udp_answers", "tcp-answers": "powerdns.authoritative.tcp_answers",
	"packetcache-hit": "powerdns.authoritative.packetcache_hit", "packetcache-miss": "powerdns.authoritative.packetcache_miss",
	"query-cache-hit": "powerdns.authoritative.query_cache_hit", "query-cache-miss": "powerdns.authoritative.query_cache_miss",
	"servfail-packets": "powerdns.authoritative.servfail_packets", "timedout-packets": "powerdns.authoritative.timedout_packets",
	"corrupt-packets": "powerdns.authoritative.corrupt_packets", "overload-drops": "powerdns.authoritative.overload_drops",
	"rd-queries": "powerdns.authoritative.rd_queries", "recursing-questions": "powerdns.authoritative.recursing_questions",
	"signatures": "powerdns.authoritative.signatures", "udp-noport-errors": "powerdns.authoritative.udp_noport_errors",
	"udp-recvbuf-errors": "powerdns.authoritative.udp_recvbuf_errors", "deferred-cache-inserts": "powerdns.authoritative.deferred_cache_inserts",
	"latency": "powerdns.authoritative.latency", "packetcache-size": "powerdns.authoritative.packetcache_size",
	"query-cache-size": "powerdns.authoritative.query_cache_size", "qsize-q": "powerdns.authoritative.qsize_q",
	"uptime": "powerdns.authoritative.uptime", "security-status": "powerdns.authoritative.security_status",
	"fd-usage": "powerdns.authoritative.fd_usage", "real-memory-usage": "powerdns.authoritative.real_memory_usage",
	"backend-latency": "powerdns.authoritative.backend_latency", "cache-latency": "powerdns.authoritative.cache_latency",
}

var pdnsRecursorStats = map[string]string{
	"questions": "powerdns.recursor.questions", "all-outqueries": "powerdns.recursor.all_outqueries",
	"answers0-1": "powerdns.recursor.answers0_1", "answers1-10": "powerdns.recursor.answers1_10",
	"answers10-100": "powerdns.recursor.answers10_100", "answers100-1000": "powerdns.recursor.answers100_1000",
	"answers-slow": "powerdns.recursor.answers_slow", "cache-hits": "powerdns.recursor.cache_hits",
	"cache-misses": "powerdns.recursor.cache_misses", "packetcache-hits": "powerdns.recursor.packetcache_hits",
	"packetcache-misses": "powerdns.recursor.packetcache_misses", "outgoing-timeouts": "powerdns.recursor.outgoing_timeouts",
	"servfail-answers": "powerdns.recursor.servfail_answers", "nxdomain-answers": "powerdns.recursor.nxdomain_answers",
	"noerror-answers": "powerdns.recursor.noerror_answers", "tcp-questions": "powerdns.recursor.tcp_questions",
	"throttled-out": "powerdns.recursor.throttled_out", "over-capacity-drops": "powerdns.recursor.over_capacity_drops",
	"resource-limits": "powerdns.recursor.resource_limits", "policy-drops": "powerdns.recursor.policy_drops",
	"unreachables": "powerdns.recursor.unreachables", "dnssec-result-bogus": "powerdns.recursor.dnssec_result_bogus",
	"dnssec-validations": "powerdns.recursor.dnssec_validations", "spoof-prevents": "powerdns.recursor.spoof_prevents",
	"qa-latency": "powerdns.recursor.qa_latency", "cache-entries": "powerdns.recursor.cache_entries",
	"packetcache-entries": "powerdns.recursor.packetcache_entries", "concurrent-queries": "powerdns.recursor.concurrent_queries",
	"uptime": "powerdns.recursor.uptime", "security-status": "powerdns.recursor.security_status",
	"fd-usage": "powerdns.recursor.fd_usage", "real-memory-usage": "powerdns.recursor.real_memory_usage",
	"negcache-entries": "powerdns.recursor.negcache_entries", "x-our-latency": "powerdns.recursor.our_latency",
}

// Jauges : valeur telle quelle (le reste est cumulé).
var pdnsGauges = map[string]bool{
	"latency": true, "packetcache-size": true, "query-cache-size": true, "qsize-q": true, "uptime": true,
	"security-status": true, "fd-usage": true, "real-memory-usage": true, "backend-latency": true, "cache-latency": true,
	"qa-latency": true, "cache-entries": true, "packetcache-entries": true, "concurrent-queries": true,
	"negcache-entries": true, "x-our-latency": true,
}

var pdnsUnits = map[string]string{
	"latency": "microsecond", "qa-latency": "microsecond", "backend-latency": "microsecond", "cache-latency": "microsecond",
	"x-our-latency": "microsecond", "uptime": "second", "real-memory-usage": "byte", "fd-usage": "file",
}

type PowerDNS struct {
	probe
	cfg      config.PowerDNSConfig
	recursor bool
	http     *httpProbe
	stats    map[string]string
}

func newPowerDNS(cfg config.PowerDNSConfig, recursor bool) *PowerDNS {
	kind, can, stats := "powerdns", "powerdns.authoritative.can_connect", pdnsAuthStats
	if recursor {
		kind, can, stats = "powerdns_recursor", "powerdns.recursor.can_connect", pdnsRecursorStats
	}
	h := newHTTPProbe(cfg.URL, "", "", cfg.Insecure)
	h.headers = map[string]string{"X-API-Key": cfg.APIKey}
	return &PowerDNS{cfg: cfg, recursor: recursor, http: h, stats: stats,
		probe: newProbe(kind, can, intent(cfg.Enabled, cfg.APIKey != ""), "instance", cfg.URL)}
}

func (p *PowerDNS) period() time.Duration { return 30 * time.Second }

type pdnsItem struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value any    `json:"value"`
}

func (p *PowerDNS) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := p.det.skip(now); skip {
		return nil, st
	}
	if p.cfg.APIKey == "" && p.det.mode == "auto" {
		return nil, "absent" // l'API exige sa clé
	}
	ts := model.Ms(now)
	var server struct {
		DaemonType string `json:"daemon_type"`
		Version    string `json:"version"`
	}
	base := "/api/v1/servers/" + p.cfg.ServerID
	err := p.http.getJSON(ctx, base, &server)
	if err == nil {
		want := map[bool]string{true: "recursor", false: "authoritative"}[p.recursor]
		if server.DaemonType != want {
			err = fmt.Errorf("serveur %q trouvé à cette adresse, %q attendu", server.DaemonType, want)
		}
	}
	var items []pdnsItem
	if err == nil {
		err = p.http.getJSON(ctx, base+"/statistics", &items)
	}
	if err != nil {
		return p.fail(ts, tags, err)
	}
	p.det.ok()
	p.begin()
	defer p.end()
	kv := p.kvs()
	security := -1.0
	for _, it := range items {
		if it.Type != "StatisticItem" {
			continue
		}
		metric, ok := p.stats[it.Name]
		if !ok {
			continue
		}
		v, ok := num(it.Value)
		if !ok {
			continue
		}
		unit := pdnsUnits[it.Name]
		if pdnsGauges[it.Name] {
			b.Gauge(metric, unit, v, kv...)
			if it.Name == "security-status" {
				security = v
			}
		} else {
			if unit == "" {
				unit = "query"
			}
			p.count(b, metric, unit, it.Name, v, kv...)
		}
	}
	if p.recursor {
		p.ratio(b, "powerdns.recursor.cache_hit_rate", "cache-hits", "cache-misses", kv...)
	} else {
		p.ratio(b, "powerdns.authoritative.packetcache_hit_rate", "packetcache-hit", "packetcache-miss", kv...)
	}
	checks := []model.Check{p.okCheck(ts, tags, fmt.Sprintf("PowerDNS %s %s joignable", map[bool]string{true: "Recursor", false: "Authoritative"}[p.recursor], server.Version))}
	// security-status : 1 à jour, 2 mise à jour conseillée, 3 version vulnérable (0 : jamais
	// interrogé, interrogation coupée ou sans réseau).
	if security >= 1 {
		st, msg := model.CheckOK, "version à jour (security-status 1)"
		switch {
		case security >= 3:
			st, msg = model.CheckCrit, "version vulnérable : mise à jour de sécurité requise (security-status 3)"
		case security >= 2:
			st, msg = model.CheckWarn, "mise à jour conseillée par PowerDNS (security-status 2)"
		}
		name := "powerdns.authoritative.security_status"
		if p.recursor {
			name = "powerdns.recursor.security_status"
		}
		checks = append(checks, model.Check{TS: ts, Name: name, Status: st, Message: msg, Tags: tags(kv...)})
	}
	return checks, "ok"
}
