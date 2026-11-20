package integrations

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── Harbor : API v2.0 ───────────────────────────────────────────────────────────────────────
//
// /api/v2.0/health (santé de chaque composant, sans compte), /systeminfo (version, lecture
// seule) ; avec un compte administrateur (ou un robot système aux droits de lecture) :
// /statistics (projets, dépôts, stockage consommé) et /systeminfo/volumes (disque du registre).

type Harbor struct {
	probe
	cfg  config.ServiceProbe
	http *httpProbe
}

func newHarbor(cfg config.ServiceProbe) *Harbor {
	return &Harbor{cfg: cfg, http: newHTTPProbe(cfg.URL, cfg.Username, cfg.Password, cfg.Insecure),
		probe: newProbe("harbor", "harbor.can_connect", intent(cfg.Enabled, cfg.URL != ""), "instance", cfg.URL)}
}

func (h *Harbor) period() time.Duration { return 60 * time.Second }

func (h *Harbor) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := h.det.skip(now); skip {
		return nil, st
	}
	if h.cfg.URL == "" {
		return nil, "absent"
	}
	ts := model.Ms(now)
	var health struct {
		Status     string `json:"status"`
		Components []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Error  string `json:"error"`
		} `json:"components"`
	}
	err := h.http.getJSON(ctx, "/api/v2.0/health", &health)
	if err == nil && health.Status == "" {
		err = fmt.Errorf("/api/v2.0/health : réponse inattendue")
	}
	if err != nil {
		return h.fail(ts, tags, err)
	}
	h.det.ok()
	kv := h.kvs()
	var checks []model.Check
	sort.Slice(health.Components, func(i, j int) bool { return health.Components[i].Name < health.Components[j].Name })
	var bad []string
	for _, c := range health.Components {
		ckv := append(append([]string{}, kv...), "component", c.Name)
		st, v := model.CheckOK, 1.0
		msg := c.Name + " : " + c.Status
		if c.Status != "healthy" {
			st, v = model.CheckCrit, 0
			if c.Error != "" {
				msg += " (" + model.Truncate(c.Error, 200) + ")"
			}
			bad = append(bad, c.Name)
		}
		b.Gauge("harbor.component.healthy", "", v, ckv...)
		checks = append(checks, model.Check{TS: ts, Name: "harbor.component", Status: st, Message: msg, Tags: tags(ckv...)})
	}
	st, msg := model.CheckOK, "tous les composants en bonne santé"
	if health.Status != "healthy" {
		st, msg = model.CheckCrit, "composants en défaut : "+strings.Join(bad, ", ")
	}
	checks = append(checks, model.Check{TS: ts, Name: "harbor.status", Status: st, Message: msg, Tags: tags(kv...)})
	var info struct {
		Version  string `json:"harbor_version"`
		ReadOnly *bool  `json:"read_only"`
	}
	version := ""
	if err := h.http.getJSON(ctx, "/api/v2.0/systeminfo", &info); err == nil {
		version = strings.SplitN(info.Version, "-", 2)[0]
		if info.ReadOnly != nil {
			v := 0.0
			if *info.ReadOnly {
				v = 1
			}
			b.Gauge("harbor.registry.read_only", "", v, kv...)
		}
	}
	var errs []string
	if h.cfg.Username != "" {
		var stats struct {
			PrivateProjects float64 `json:"private_project_count"`
			PublicProjects  float64 `json:"public_project_count"`
			TotalProjects   float64 `json:"total_project_count"`
			PrivateRepos    float64 `json:"private_repo_count"`
			PublicRepos     float64 `json:"public_repo_count"`
			TotalRepos      float64 `json:"total_repo_count"`
			Storage         float64 `json:"total_storage_consumption"`
		}
		if err := h.http.getJSON(ctx, "/api/v2.0/statistics", &stats); err == nil {
			b.Gauge("harbor.projects.count", "project", stats.TotalProjects, kv...)
			b.Gauge("harbor.projects.public", "project", stats.PublicProjects, kv...)
			b.Gauge("harbor.repos.count", "repository", stats.TotalRepos, kv...)
			b.Gauge("harbor.storage.consumption", "byte", stats.Storage, kv...)
		} else {
			errs = append(errs, err.Error())
		}
		var vol struct {
			Storage []struct {
				Total float64 `json:"total"`
				Free  float64 `json:"free"`
			} `json:"storage"`
		}
		if err := h.http.getJSON(ctx, "/api/v2.0/systeminfo/volumes", &vol); err == nil && len(vol.Storage) > 0 {
			s := vol.Storage[0]
			b.Gauge("harbor.disk.total", "byte", s.Total, kv...)
			b.Gauge("harbor.disk.free", "byte", s.Free, kv...)
			if s.Total > 0 {
				b.Gauge("harbor.disk.usage", "percent", round1(100*(s.Total-s.Free)/s.Total), kv...)
			}
		} else if err != nil {
			errs = append(errs, err.Error())
		}
	}
	msg = "Harbor joignable"
	if version != "" {
		msg = "Harbor " + version + " joignable"
	}
	checks = append(checks, h.okCheck(ts, tags, msg))
	if len(errs) > 0 {
		return checks, "partiel : " + strings.Join(errs, " ; ")
	}
	return checks, "ok"
}
