package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── Keycloak : santé (port de gestion) et sessions par royaume (API d'administration) ───────
//
// Santé : KC_HEALTH_ENABLED=true, url du port de gestion (http://127.0.0.1:9000), lecture de
// /health/ready (base de données comprise). Sessions : un client confidentiel à « service
// account » (client_credentials) dont le compte porte view-realm et view-users du
// realm-management de chaque royaume lu. Les mesures JVM et HTTP de Keycloak ne sont publiées
// qu'au format Prometheus (/metrics) : elles relèvent du lecteur OpenMetrics.

const keycloakMaxRealms = 50

type Keycloak struct {
	probe
	cfg      config.KeycloakConfig
	health   *httpProbe
	admin    *httpProbe
	token    string
	tokenEnd time.Time
}

func newKeycloak(cfg config.KeycloakConfig) *Keycloak {
	k := &Keycloak{cfg: cfg, probe: newProbe("keycloak", "keycloak.can_connect",
		intent(cfg.Enabled, cfg.URL != "" || cfg.ClientID != ""), "instance", firstNonEmpty(cfg.URL, cfg.AdminURL))}
	if cfg.URL != "" {
		k.health = newHTTPProbe(cfg.URL, "", "", cfg.Insecure)
	}
	if cfg.AdminURL != "" {
		k.admin = newHTTPProbe(cfg.AdminURL, "", "", cfg.Insecure)
	}
	return k
}

func (k *Keycloak) period() time.Duration { return 60 * time.Second }

// login : jeton du compte de service (client_credentials), gardé jusqu'à 30 s de sa fin.
func (k *Keycloak) login(ctx context.Context, now time.Time) error {
	if k.token != "" && now.Before(k.tokenEnd) {
		return nil
	}
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {k.cfg.ClientID}, "client_secret": {k.cfg.ClientSecret}}
	path := "/realms/" + url.PathEscape(k.cfg.Realm) + "/protocol/openid-connect/token"
	body, code, err := k.admin.do(ctx, http.MethodPost, path, strings.NewReader(form.Encode()),
		map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	if err != nil {
		return err
	}
	if code == http.StatusUnauthorized || code == http.StatusBadRequest {
		return fmt.Errorf("jeton refusé (HTTP %d) : client_id, client_secret, « Service accounts roles » du client", code)
	}
	if err := httpStatusErr("jeton", code, nil); err != nil {
		return err
	}
	var tok struct {
		AccessToken string  `json:"access_token"`
		ExpiresIn   float64 `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tok); err != nil || tok.AccessToken == "" {
		return fmt.Errorf("jeton illisible")
	}
	k.token = tok.AccessToken
	k.tokenEnd = now.Add(time.Duration(tok.ExpiresIn)*time.Second - 30*time.Second)
	return nil
}

func (k *Keycloak) adminGet(ctx context.Context, path string, out any) error {
	k.admin.headers = map[string]string{"Authorization": "Bearer " + k.token}
	defer func() { k.admin.headers = nil }()
	err := k.admin.getJSON(ctx, path, out)
	if err != nil && strings.Contains(err.Error(), "HTTP 401") {
		k.token = "" // jeton révoqué : nouveau au prochain passage
	}
	return err
}

func (k *Keycloak) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := k.det.skip(now); skip {
		return nil, st
	}
	if k.health == nil && k.admin == nil {
		return nil, "absent"
	}
	ts := model.Ms(now)
	kv := k.kvs()
	var checks []model.Check
	status := ""
	if k.health != nil {
		var h struct {
			Status string `json:"status"`
			Checks []struct {
				Name   string `json:"name"`
				Status string `json:"status"`
			} `json:"checks"`
		}
		body, code, err := k.health.do(ctx, http.MethodGet, "/health/ready", nil, nil)
		if err == nil {
			if jerr := json.Unmarshal(body, &h); jerr != nil || h.Status == "" {
				if code == http.StatusNotFound {
					err = fmt.Errorf("/health/ready introuvable (KC_HEALTH_ENABLED=true, port de gestion 9000)")
				} else {
					err = fmt.Errorf("/health/ready : HTTP %d", code)
				}
			}
		}
		if err != nil {
			return k.fail(ts, tags, err)
		}
		k.det.ok()
		status = h.Status
		up := 0.0
		if h.Status == "UP" {
			up = 1
		}
		b.Gauge("keycloak.health.ready", "", up, kv...)
		if h.Status != "UP" {
			var down []string
			for _, c := range h.Checks {
				if c.Status != "UP" {
					down = append(down, c.Name)
				}
			}
			return []model.Check{{TS: ts, Name: "keycloak.can_connect", Status: model.CheckCrit,
				Message: "Keycloak pas prêt : " + firstNonEmpty(strings.Join(down, ", "), h.Status), Tags: tags(kv...)}}, "erreur : pas prêt"
		}
	}
	state := "ok"
	if k.admin != nil && k.cfg.ClientID != "" {
		err := k.login(ctx, now)
		var realms []struct {
			Realm   string `json:"realm"`
			Enabled bool   `json:"enabled"`
		}
		if err == nil {
			if len(k.cfg.Realms) > 0 {
				for _, r := range k.cfg.Realms {
					realms = append(realms, struct {
						Realm   string `json:"realm"`
						Enabled bool   `json:"enabled"`
					}{r, true})
				}
			} else {
				err = k.adminGet(ctx, "/admin/realms", &realms)
			}
		}
		if err != nil {
			if k.health == nil {
				return k.fail(ts, tags, err)
			}
			checks = append(checks, model.Check{TS: ts, Name: "keycloak.admin_api", Status: model.CheckCrit, Message: err.Error(), Tags: tags(kv...)})
			state = "partiel : " + err.Error()
		} else {
			k.det.ok()
			sort.Slice(realms, func(i, j int) bool { return realms[i].Realm < realms[j].Realm })
			var errs []string
			for i, r := range realms {
				if i >= keycloakMaxRealms || !r.Enabled {
					continue
				}
				rkv := append(append([]string{}, kv...), "realm", r.Realm)
				var stats []struct {
					ClientID string `json:"clientId"`
					Active   any    `json:"active"`
					Offline  any    `json:"offline"`
				}
				if err := k.adminGet(ctx, "/admin/realms/"+url.PathEscape(r.Realm)+"/client-session-stats", &stats); err != nil {
					errs = append(errs, r.Realm+" : "+err.Error())
					continue
				}
				var act, off float64
				for _, s := range stats {
					a, _ := num(s.Active)
					o, _ := num(s.Offline)
					act += a
					off += o
					ckv := append(append([]string{}, rkv...), "client", s.ClientID)
					b.Gauge("keycloak.client.sessions.active", "session", a, ckv...)
					b.Gauge("keycloak.client.sessions.offline", "session", o, ckv...)
				}
				b.Gauge("keycloak.sessions.active", "session", act, rkv...)
				b.Gauge("keycloak.sessions.offline", "session", off, rkv...)
				var users any
				if err := k.adminGet(ctx, "/admin/realms/"+url.PathEscape(r.Realm)+"/users/count", &users); err == nil {
					if n, ok := num(users); ok {
						b.Gauge("keycloak.users", "user", n, rkv...)
					}
				}
			}
			st, msg := model.CheckOK, fmt.Sprintf("API d'administration lue (%d royaume(s))", len(realms))
			if len(errs) > 0 {
				st, msg = model.CheckWarn, strings.Join(errs, " ; ")
				state = "partiel : " + msg
			}
			checks = append(checks, model.Check{TS: ts, Name: "keycloak.admin_api", Status: st, Message: model.Truncate(msg, 400), Tags: tags(kv...)})
		}
	}
	msg := "Keycloak joignable"
	if status != "" {
		msg = "Keycloak prêt (" + status + ")"
	}
	checks = append(checks, k.okCheck(ts, tags, msg))
	return checks, state
}
