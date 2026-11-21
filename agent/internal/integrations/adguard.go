package integrations

import (
	"context"
	"fmt"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── AdGuard Home : API /control (authentification de base, compte de l'interface) ──────────
//
// /control/status (serveur DNS en marche, protection active, version), /control/stats (totaux de
// la fenêtre de statistiques d'AdGuard Home, 24 h par défaut : requêtes, blocages, temps de
// traitement, réponses et temps moyen de chaque serveur amont), /control/filtering/status (listes
// de filtrage : règles, dernière mise à jour). Aucune écriture, aucun appel à /control/version.json
// (qui interroge les serveurs d'AdGuard).

const adguardMaxFilters = 100

type AdGuard struct {
	probe
	cfg  config.AdGuardConfig
	http *httpProbe
}

func newAdGuard(cfg config.AdGuardConfig) *AdGuard {
	return &AdGuard{cfg: cfg, http: newHTTPProbe(cfg.URL, cfg.Username, cfg.Password, cfg.Insecure),
		probe: newProbe("adguard", "adguard.can_connect", intent(cfg.Enabled, cfg.Username != ""), "instance", cfg.URL)}
}

func (a *AdGuard) period() time.Duration { return 60 * time.Second }

type adguardStatus struct {
	Version                    string   `json:"version"`
	Running                    *bool    `json:"running"`
	ProtectionEnabled          bool     `json:"protection_enabled"`
	ProtectionDisabledDuration float64  `json:"protection_disabled_duration"` // ms restantes
	DNSPort                    float64  `json:"dns_port"`
	DNSAddresses               []string `json:"dns_addresses"`
}

type adguardStats struct {
	NumDNSQueries          *float64             `json:"num_dns_queries"`
	NumBlockedFiltering    float64              `json:"num_blocked_filtering"`
	NumReplacedSafebrowse  float64              `json:"num_replaced_safebrowsing"`
	NumReplacedSafesearch  float64              `json:"num_replaced_safesearch"`
	NumReplacedParental    float64              `json:"num_replaced_parental"`
	AvgProcessingTime      float64              `json:"avg_processing_time"` // secondes
	TopUpstreamsResponses  []map[string]float64 `json:"top_upstreams_responses"`
	TopUpstreamsAvgTime    []map[string]float64 `json:"top_upstreams_avg_time"`
	TopClients             []map[string]float64 `json:"top_clients"`
}

type adguardFiltering struct {
	Enabled  bool    `json:"enabled"`
	Interval float64 `json:"interval"` // heures entre deux mises à jour
	Filters  []struct {
		Name        string  `json:"name"`
		URL         string  `json:"url"`
		Enabled     bool    `json:"enabled"`
		RulesCount  float64 `json:"rules_count"`
		LastUpdated string  `json:"last_updated"`
	} `json:"filters"`
}

func (a *AdGuard) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := a.det.skip(now); skip {
		return nil, st
	}
	if a.cfg.Username == "" && a.det.mode == "auto" {
		return nil, "absent" // l'API exige un compte
	}
	ts := model.Ms(now)
	var st adguardStatus
	err := a.http.getJSON(ctx, "/control/status", &st)
	if err == nil && st.Running == nil {
		err = fmt.Errorf("/control/status : réponse inattendue (AdGuard Home attendu)")
	}
	if err != nil {
		return a.fail(ts, tags, err)
	}
	a.det.ok()
	kv := a.kvs()
	b.Gauge("adguard.running", "", boolF(*st.Running), kv...)
	b.Gauge("adguard.protection_enabled", "", boolF(st.ProtectionEnabled), kv...)
	pst, pmsg := model.CheckOK, "protection active"
	switch {
	case !*st.Running:
		pst, pmsg = model.CheckCrit, "serveur DNS arrêté"
	case !st.ProtectionEnabled:
		pst, pmsg = model.CheckWarn, "protection coupée"
		if st.ProtectionDisabledDuration > 0 {
			pmsg += fmt.Sprintf(" (reprise dans %.0f s)", st.ProtectionDisabledDuration/1000)
		}
	}
	checks := []model.Check{{TS: ts, Name: "adguard.protection", Status: pst, Message: pmsg, Tags: tags(kv...)}}
	var problems []string

	var s adguardStats
	if err := a.http.getJSON(ctx, "/control/stats", &s); err != nil {
		problems = append(problems, err.Error())
	} else if s.NumDNSQueries != nil {
		total := *s.NumDNSQueries
		b.Gauge("adguard.queries", "query", total, kv...)
		b.Gauge("adguard.queries.blocked", "query", s.NumBlockedFiltering, kv...)
		if total > 0 {
			b.Gauge("adguard.queries.percent_blocked", "percent", round1(100*s.NumBlockedFiltering/total), kv...)
		}
		b.Gauge("adguard.queries.safebrowsing", "query", s.NumReplacedSafebrowse, kv...)
		b.Gauge("adguard.queries.safesearch", "query", s.NumReplacedSafesearch, kv...)
		b.Gauge("adguard.queries.parental", "query", s.NumReplacedParental, kv...)
		b.Gauge("adguard.processing_time", "millisecond", round1(1000*s.AvgProcessingTime), kv...)
		b.Gauge("adguard.clients.top", "client", float64(len(s.TopClients)), kv...)
		for i, m := range s.TopUpstreamsResponses {
			if i >= 20 {
				break
			}
			for up, v := range m {
				b.Gauge("adguard.upstream.responses", "response", v, a.kvs("upstream", model.Truncate(up, 100))...)
			}
		}
		for i, m := range s.TopUpstreamsAvgTime {
			if i >= 20 {
				break
			}
			for up, v := range m {
				b.Gauge("adguard.upstream.avg_time", "millisecond", round1(1000*v), a.kvs("upstream", model.Truncate(up, 100))...)
			}
		}
	}

	var f adguardFiltering
	if err := a.http.getJSON(ctx, "/control/filtering/status", &f); err != nil {
		problems = append(problems, err.Error())
	} else {
		b.Gauge("adguard.filtering.enabled", "", boolF(f.Enabled), kv...)
		var on, rules, empty float64
		oldest := -1.0
		for i, fl := range f.Filters {
			if !fl.Enabled || i >= adguardMaxFilters {
				continue
			}
			on++
			rules += fl.RulesCount
			fkv := a.kvs("filter", model.Truncate(firstNonEmpty(fl.Name, fl.URL), 80))
			b.Gauge("adguard.filter.rules", "rule", fl.RulesCount, fkv...)
			if fl.RulesCount == 0 {
				empty++
			}
			if t, err := time.Parse(time.RFC3339, fl.LastUpdated); err == nil {
				age := ageSeconds(now, float64(t.Unix()))
				b.Gauge("adguard.filter.age", "second", age, fkv...)
				if age > oldest {
					oldest = age
				}
			} else {
				empty++ // liste jamais téléchargée
			}
		}
		b.Gauge("adguard.filters.enabled", "list", on, kv...)
		b.Gauge("adguard.filters.rules", "rule", rules, kv...)
		b.Gauge("adguard.filters.empty", "list", empty, kv...)
		if oldest >= 0 {
			b.Gauge("adguard.filters.oldest_update_age", "second", oldest, kv...)
		}
	}
	msg := fmt.Sprintf("AdGuard Home %s : %s", firstNonEmpty(st.Version, "?"), pmsg)
	conn := a.okCheck(ts, tags, msg)
	if len(problems) > 0 {
		conn.Status, conn.Message = model.CheckWarn, msg+" ; lecture partielle : "+model.Truncate(strings.Join(problems, " ; "), 300)
		return append(checks, conn), "partiel : " + model.Truncate(problems[0], 200)
	}
	return append(checks, conn), "ok"
}

func boolF(v bool) float64 {
	if v {
		return 1
	}
	return 0
}
