package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/hostfs"
	"s33d.fr/hypha/agent/internal/model"
)

// ── Pi-hole 6 : API REST de FTL (/api) ─────────────────────────────────────────────────────
//
// Une session (POST /api/auth, mot de passe d'application de préférence) est ouverte au premier
// relevé, gardée tant que Pi-hole l'accepte (elle se prolonge à chaque appel), rouverte après un
// 401, et fermée (DELETE /api/auth) à l'arrêt de l'agent : Pi-hole limite le nombre de sessions.
// Relevé par minute : /api/stats/summary (fenêtre glissante de 24 h de FTL : requêtes, blocages,
// clients, listes), /api/dns/blocking (état du blocage), /api/info/messages (avertissements de
// FTL) ; /api/info/version toutes les heures (mise à jour disponible, lue par Pi-hole lui-même).

var piholeStatuses = map[string]string{
	"UNKNOWN": "unknown", "GRAVITY": "gravity", "FORWARDED": "forwarded", "CACHE": "cache", "REGEX": "regex",
	"DENYLIST": "denylist", "EXTERNAL_BLOCKED_IP": "external_blocked_ip", "EXTERNAL_BLOCKED_NULL": "external_blocked_null",
	"EXTERNAL_BLOCKED_NXRA": "external_blocked_nxra", "GRAVITY_CNAME": "gravity_cname", "REGEX_CNAME": "regex_cname",
	"DENYLIST_CNAME": "denylist_cname", "RETRIED": "retried", "RETRIED_DNSSEC": "retried_dnssec", "IN_PROGRESS": "in_progress",
	"DBBUSY": "dbbusy", "SPECIAL_DOMAIN": "special_domain", "CACHE_STALE": "cache_stale", "EXTERNAL_BLOCKED_EDE15": "external_blocked_ede15",
}

type piholeSummary struct {
	Queries struct {
		Total          *float64           `json:"total"`
		Blocked        float64            `json:"blocked"`
		PercentBlocked float64            `json:"percent_blocked"`
		UniqueDomains  float64            `json:"unique_domains"`
		Forwarded      float64            `json:"forwarded"`
		Cached         float64            `json:"cached"`
		Frequency      float64            `json:"frequency"`
		Status         map[string]float64 `json:"status"`
		Replies        map[string]float64 `json:"replies"`
	} `json:"queries"`
	Clients struct {
		Active float64 `json:"active"`
		Total  float64 `json:"total"`
	} `json:"clients"`
	Gravity struct {
		DomainsBeingBlocked float64 `json:"domains_being_blocked"`
		LastUpdate          float64 `json:"last_update"`
	} `json:"gravity"`
}

type piholeVersion struct {
	Version map[string]struct {
		Local  json.RawMessage `json:"local"`
		Remote json.RawMessage `json:"remote"`
	} `json:"version"`
}

type PiHole struct {
	probe
	cfg  config.PiHoleConfig
	http *httpProbe
	sid  string

	version   string // « core v6.6, FTL v6.9 »
	updates   float64
	versionAt time.Time
}

func newPiHole(cfg config.PiHoleConfig) *PiHole {
	explicit := cfg.Password != "" || cfg.URL != "http://127.0.0.1"
	return &PiHole{cfg: cfg, http: newHTTPProbe(cfg.URL, "", "", cfg.Insecure),
		probe: newProbe("pihole", "pihole.can_connect", intent(cfg.Enabled, explicit), "instance", cfg.URL)}
}

func (p *PiHole) period() time.Duration { return 60 * time.Second }

// login ouvre une session ; un Pi-hole sans mot de passe rend une session valide sans sid.
func (p *PiHole) login(ctx context.Context) error {
	body, _ := json.Marshal(map[string]string{"password": p.cfg.Password})
	data, code, err := p.http.do(ctx, http.MethodPost, "/api/auth", bytes.NewReader(body), map[string]string{"Content-Type": "application/json"})
	if err != nil {
		return err
	}
	var r struct {
		Session struct {
			Valid   bool    `json:"valid"`
			SID     *string `json:"sid"`
			Message string  `json:"message"`
		} `json:"session"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(data, &r)
	switch {
	case code == http.StatusUnauthorized || (code == http.StatusOK && !r.Session.Valid):
		return fmt.Errorf("/api/auth : mot de passe refusé (HTTP %d)", code)
	case code == http.StatusTooManyRequests:
		return fmt.Errorf("/api/auth : trop de sessions ou de tentatives (HTTP 429)")
	case code != http.StatusOK:
		if r.Error.Message != "" {
			return fmt.Errorf("/api/auth : HTTP %d (%s)", code, model.Truncate(r.Error.Message, 120))
		}
		return fmt.Errorf("/api/auth : HTTP %d (Pi-hole 6 attendu)", code)
	}
	p.sid = ""
	if r.Session.SID != nil {
		p.sid = *r.Session.SID
	}
	return nil
}

// get lit une ressource de l'API avec la session ; un 401 rouvre la session une fois.
func (p *PiHole) get(ctx context.Context, path string, out any) error {
	for try := 0; try < 2; try++ {
		hdr := map[string]string{}
		if p.sid != "" {
			hdr["X-FTL-SID"] = p.sid
		}
		data, code, err := p.http.do(ctx, http.MethodGet, path, nil, hdr)
		if err != nil {
			return err
		}
		if code == http.StatusUnauthorized && try == 0 {
			if err := p.login(ctx); err != nil {
				return err
			}
			continue
		}
		if err := httpStatusErr(path, code, data); err != nil {
			return err
		}
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("%s : réponse illisible (%v)", path, err)
		}
		return nil
	}
	return fmt.Errorf("%s : session refusée", path)
}

func (p *PiHole) close() {
	if p.sid == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _, _ = p.http.do(ctx, http.MethodDelete, "/api/auth", nil, map[string]string{"X-FTL-SID": p.sid})
	p.sid = ""
}

func (p *PiHole) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := p.det.skip(now); skip {
		return nil, st
	}
	if p.det.mode == "auto" && !p.det.seen && !fileExists(hostfs.Etc("pihole")) {
		return nil, "absent"
	}
	ts := model.Ms(now)
	var s piholeSummary
	err := p.get(ctx, "/api/stats/summary", &s)
	if err == nil && s.Queries.Total == nil {
		err = fmt.Errorf("/api/stats/summary : réponse inattendue (Pi-hole 6 attendu)")
	}
	if err != nil {
		return p.fail(ts, tags, err)
	}
	p.det.ok()
	kv := p.kvs()
	q := s.Queries
	b.Gauge("pihole.queries.total", "query", *q.Total, kv...)
	b.Gauge("pihole.queries.blocked", "query", q.Blocked, kv...)
	b.Gauge("pihole.queries.percent_blocked", "percent", round1(q.PercentBlocked), kv...)
	b.Gauge("pihole.queries.forwarded", "query", q.Forwarded, kv...)
	b.Gauge("pihole.queries.cached", "query", q.Cached, kv...)
	b.Gauge("pihole.queries.unique_domains", "domain", q.UniqueDomains, kv...)
	b.Gauge("pihole.queries.frequency", "query_per_second", q.Frequency, kv...)
	for k, v := range q.Status {
		if name, ok := piholeStatuses[k]; ok {
			b.Gauge("pihole.queries.by_status", "query", v, p.kvs("status", name)...)
		}
	}
	for k, v := range q.Replies {
		if len(k) <= 24 {
			b.Gauge("pihole.queries.by_reply", "query", v, p.kvs("reply", strings.ToLower(k))...)
		}
	}
	b.Gauge("pihole.clients.active", "client", s.Clients.Active, kv...)
	b.Gauge("pihole.clients.total", "client", s.Clients.Total, kv...)
	b.Gauge("pihole.gravity.domains_blocked", "domain", s.Gravity.DomainsBeingBlocked, kv...)
	if s.Gravity.LastUpdate > 0 {
		b.Gauge("pihole.gravity.age", "second", ageSeconds(now, s.Gravity.LastUpdate), kv...)
	}
	checks := []model.Check{}
	var problems []string

	var bl struct {
		Blocking string   `json:"blocking"`
		Timer    *float64 `json:"timer"`
	}
	if err := p.get(ctx, "/api/dns/blocking", &bl); err != nil {
		problems = append(problems, err.Error())
	} else {
		on := 0.0
		st, msg := model.CheckOK, "blocage actif"
		switch bl.Blocking {
		case "enabled":
			on = 1
		case "disabled":
			st, msg = model.CheckWarn, "blocage coupé"
			if bl.Timer != nil && *bl.Timer > 0 {
				msg += fmt.Sprintf(" (reprise dans %.0f s)", *bl.Timer)
			}
		case "failed":
			st, msg = model.CheckCrit, "blocage en échec (FTL n'a pas pu appliquer les listes)"
		default:
			st, msg = model.CheckUnknown, "état du blocage inconnu : "+model.Truncate(bl.Blocking, 40)
		}
		b.Gauge("pihole.blocking.enabled", "", on, kv...)
		checks = append(checks, model.Check{TS: ts, Name: "pihole.blocking", Status: st, Message: msg, Tags: tags(kv...)})
	}

	var msgs struct {
		Messages []struct {
			Type string `json:"type"`
		} `json:"messages"`
	}
	if err := p.get(ctx, "/api/info/messages", &msgs); err != nil {
		problems = append(problems, err.Error())
	} else {
		by := map[string]float64{}
		for _, m := range msgs.Messages {
			by[strings.ToLower(model.Truncate(m.Type, 32))]++
		}
		b.Gauge("pihole.messages", "message", float64(len(msgs.Messages)), kv...)
		types := make([]string, 0, len(by))
		for t := range by {
			types = append(types, t)
		}
		sort.Strings(types)
		for _, t := range types {
			b.Gauge("pihole.messages.by_type", "message", by[t], p.kvs("message_type", t)...)
		}
	}

	if p.version == "" || now.Sub(p.versionAt) >= time.Hour {
		var v piholeVersion
		if err := p.get(ctx, "/api/info/version", &v); err == nil {
			p.version, p.updates = piholeVersionText(v)
			p.versionAt = now
		}
	}
	if p.version != "" {
		b.Gauge("pihole.updates_available", "component", p.updates, kv...)
	}
	msg := fmt.Sprintf("Pi-hole %s : %.0f requêtes sur 24 h, %.1f %% bloquées, %.0f domaines en liste",
		firstNonEmpty(p.version, "6"), *q.Total, q.PercentBlocked, s.Gravity.DomainsBeingBlocked)
	conn := p.okCheck(ts, tags, msg)
	if len(problems) > 0 {
		conn.Status, conn.Message = model.CheckWarn, msg+" ; lecture partielle : "+model.Truncate(strings.Join(problems, " ; "), 300)
		return append(checks, conn), "partiel : " + model.Truncate(problems[0], 200)
	}
	return append(checks, conn), "ok"
}

// piholeVersionText : « core v6.6, web v6.6.1, FTL v6.9 » et le nombre de composants dont une
// version plus récente est publiée (selon Pi-hole).
func piholeVersionText(v piholeVersion) (string, float64) {
	type side struct {
		Version string `json:"version"`
	}
	var parts []string
	updates := 0.0
	for _, k := range []string{"core", "web", "ftl"} {
		c, ok := v.Version[k]
		if !ok {
			continue
		}
		var l, r side
		_ = json.Unmarshal(c.Local, &l)
		_ = json.Unmarshal(c.Remote, &r)
		if l.Version == "" {
			continue
		}
		label := k
		if k == "ftl" {
			label = "FTL"
		}
		parts = append(parts, label+" "+l.Version)
		if r.Version != "" && r.Version != l.Version {
			updates++
		}
	}
	return strings.Join(parts, ", "), updates
}
