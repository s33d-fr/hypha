package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// cloudConn : socle des connecteurs cloud à tâche de fond (Scaleway, Azure, GCP). Le relevé tourne
// toutes les interval ; la collecte relit le dernier relevé, publie les ressources, et dit la
// connexion en échec si le relevé est périmé.
type cloudConn struct {
	id         string
	enabled    config.Tristate
	configured bool
	missing    string // erreur affichée si enabled: true sans conf complète
	interval   time.Duration
	poll       func(ctx context.Context) vsSnapshot
	pub        *resourcePublisher

	once sync.Once
	stop context.CancelFunc
	mu   sync.Mutex
	snap vsSnapshot
}

func (c *cloudConn) name() string          { return c.id }
func (c *cloudConn) period() time.Duration { return 30 * time.Second }
func (c *cloudConn) close() {
	if c.stop != nil {
		c.stop()
	}
}

func (c *cloudConn) collect(_ context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if c.enabled == "false" {
		return nil, "disabled"
	}
	if !c.configured {
		if c.enabled == "true" {
			return nil, "erreur : " + c.missing
		}
		return nil, "absent"
	}
	c.once.Do(c.start)
	c.mu.Lock()
	s := c.snap
	c.mu.Unlock()
	if s.at.IsZero() {
		return nil, "partiel : premier relevé en cours"
	}
	ts := model.Ms(now)
	if now.Sub(s.at) > 3*c.interval+time.Minute {
		return []model.Check{canConnect(c.id, ts, false, "aucun relevé depuis "+now.Sub(s.at).Round(time.Second).String(), tags())}, "erreur : relevé périmé"
	}
	out := make([]model.Check, 0, len(s.checks))
	for _, ch := range s.checks {
		out = append(out, model.Check{TS: ts, Name: ch.name, Status: ch.status, Message: ch.msg, Tags: tags(ch.kv...)})
	}
	for _, g := range s.gauges {
		b.Gauge(g.name, g.unit, g.v, g.kv...)
	}
	c.pub.publish(now, s.resources, c.id, "ok")
	if len(s.errs) > 0 {
		return out, fmt.Sprintf("partiel : %d lecture(s) en échec", len(s.errs))
	}
	return out, "ok"
}

func (c *cloudConn) start() {
	ctx, cancel := context.WithCancel(context.Background())
	c.stop = cancel
	go func() {
		for {
			pctx, done := context.WithTimeout(ctx, 3*time.Minute)
			s := c.poll(pctx)
			done()
			c.mu.Lock()
			c.snap = s
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return
			case <-time.After(c.interval):
			}
		}
	}()
}

// getJSON : GET authentifié par un en-tête, réponse JSON ; l'erreur reprend le message du
// fournisseur (jamais l'en-tête d'authentification).
func getJSON(ctx context.Context, client *http.Client, url string, header map[string]string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "hypha-agent")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Message string `json:"message"`
			Error   any    `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		msg := e.Message
		switch x := e.Error.(type) {
		case string:
			msg = firstNonEmpty(msg, x)
		case map[string]any:
			if m, ok := x["message"].(string); ok {
				msg = firstNonEmpty(msg, m)
			}
		}
		if msg != "" {
			return resp.StatusCode, fmt.Errorf("HTTP %d : %s", resp.StatusCode, model.Truncate(msg, 200))
		}
		return resp.StatusCode, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return resp.StatusCode, json.Unmarshal(raw, out)
}
