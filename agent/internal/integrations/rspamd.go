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

// ── rspamd : statistiques du contrôleur (GET /stat, en local sans mot de passe) ─────────────

type Rspamd struct {
	cfg    config.URLIntegration
	det    detector
	client *http.Client
	prev   *rspamdStat
}

type rspamdStat struct {
	Version            string           `json:"version"`
	Uptime             float64          `json:"uptime"`
	Scanned            int64            `json:"scanned"`
	Learned            int64            `json:"learned"`
	SpamCount          int64            `json:"spam_count"`
	HamCount           int64            `json:"ham_count"`
	Connections        int64            `json:"connections"`
	ControlConnections int64            `json:"control_connections"`
	Actions            map[string]int64 `json:"actions"`
}

func newRspamd(cfg config.URLIntegration) *Rspamd {
	return &Rspamd{cfg: cfg, det: detector{mode: cfg.Enabled}, client: &http.Client{Timeout: 5 * time.Second}}
}

func (r *Rspamd) name() string          { return "rspamd" }
func (r *Rspamd) close()                {}
func (r *Rspamd) period() time.Duration { return 60 * time.Second }

func parseRspamdStat(body []byte) (*rspamdStat, error) {
	var s rspamdStat
	if err := json.Unmarshal(body, &s); err != nil {
		return nil, fmt.Errorf("réponse illisible : %w", err)
	}
	if s.Version == "" && s.Actions == nil {
		return nil, fmt.Errorf("la page ne ressemble pas à /stat de rspamd")
	}
	return &s, nil
}

// delta : écart d'un compteur cumulé ; un compteur revenu en arrière (redémarrage) ne compte pas.
func delta(cur, prev int64) (float64, bool) {
	if cur < prev {
		return 0, false
	}
	return float64(cur - prev), true
}

func (r *Rspamd) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := r.det.skip(now); skip {
		return nil, st
	}
	ts := model.Ms(now)
	body, err := httpGet(ctx, r.client, r.cfg.URL)
	var s *rspamdStat
	if err == nil {
		s, err = parseRspamdStat(body)
	}
	if err != nil {
		if silent, st := r.det.failed(err.Error()); silent {
			return nil, st
		} else {
			return []model.Check{canConnect("rspamd", ts, false, err.Error(), tags("url", r.cfg.URL))}, st
		}
	}
	r.det.ok()
	b.Gauge("rspamd.uptime", "second", s.Uptime)
	b.Gauge("rspamd.connections", "count", float64(s.Connections))
	if p := r.prev; p != nil && s.Uptime >= p.Uptime {
		for _, c := range []struct {
			name      string
			cur, prev int64
		}{{"rspamd.messages.scanned", s.Scanned, p.Scanned}, {"rspamd.messages.learned", s.Learned, p.Learned},
			{"rspamd.messages.spam", s.SpamCount, p.SpamCount}, {"rspamd.messages.ham", s.HamCount, p.HamCount}} {
			if v, ok := delta(c.cur, c.prev); ok {
				b.Add(c.name, model.TypeCount, "count", v)
			}
		}
		names := make([]string, 0, len(s.Actions))
		for k := range s.Actions {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			if v, ok := delta(s.Actions[k], p.Actions[k]); ok {
				b.Add("rspamd.actions", model.TypeCount, "count", v, "action", strings.ReplaceAll(k, " ", "_"))
			}
		}
	}
	r.prev = s
	return []model.Check{canConnect("rspamd", ts, true, "rspamd "+s.Version+" : statistiques lues", tags("url", r.cfg.URL))}, "ok"
}
