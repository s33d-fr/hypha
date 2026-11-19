// Package checks fait les contrôles de service (contrat § 3.4) et rend les événements de
// changement d'état (§ 3.5) : hypha.agent.up à chaque cycle, unités systemd ou services
// Windows, contrôles HTTP et TCP de la conf.
package checks

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// serviceWatcher : unités systemd (Linux) ou services Windows.
type serviceWatcher interface {
	collect(ctx context.Context, now time.Time, tags model.TagFunc) ([]model.Check, []model.Event, error)
	// checkOne : contrôle d'une seule unité ou d'un seul service, à la demande.
	checkOne(ctx context.Context, now time.Time, name string, tags model.TagFunc) []model.Check
}

// Runner exécute un cycle de contrôles.
type Runner struct {
	cfg      config.ChecksConfig
	log      *slog.Logger
	services serviceWatcher
	procs    *procWatch
}

// NewRunner prépare les contrôles.
func NewRunner(cfg config.ChecksConfig, log *slog.Logger) *Runner {
	return &Runner{cfg: cfg, log: log, services: newServiceWatcher(cfg, log), procs: newProcWatch(cfg.Processes)}
}

// Run fait un cycle ; err signale un échec de la surveillance des services elle-même.
func (r *Runner) Run(ctx context.Context, now time.Time, tags model.TagFunc) ([]model.Check, []model.Event, error) {
	ts := model.Ms(now)
	checks := []model.Check{{TS: ts, Name: "hypha.agent.up", Status: model.CheckOK, Message: "ok", Tags: tags()}}

	var svcErr error
	var events []model.Event
	if r.services != nil {
		c, e, err := r.services.collect(ctx, now, tags)
		checks = append(checks, c...)
		events = append(events, e...)
		svcErr = err
	}

	// HTTP et TCP en parallèle : un délai d'attente ne retarde pas les autres.
	results := make([]model.Check, len(r.cfg.HTTP)+len(r.cfg.TCP))
	var wg sync.WaitGroup
	for i, h := range r.cfg.HTTP {
		wg.Add(1)
		go func(i int, h config.HTTPCheck) {
			defer wg.Done()
			st, msg := httpCheck(ctx, h)
			results[i] = model.Check{TS: ts, Name: "http", Status: st, Message: msg, Tags: tags("check", h.Name, "url", h.URL)}
		}(i, h)
	}
	for i, t := range r.cfg.TCP {
		wg.Add(1)
		go func(i int, t config.TCPCheck) {
			defer wg.Done()
			st, msg := tcpCheck(ctx, t)
			results[len(r.cfg.HTTP)+i] = model.Check{TS: ts, Name: "tcp", Status: st, Message: msg, Tags: tags("check", t.Name, "address", t.Address)}
		}(i, t)
	}
	wg.Wait()
	checks = append(checks, results...)
	return checks, events, svcErr
}

// httpCheck : statut attendu → OK, autre statut ou erreur → CRIT. Les redirections sont
// suivies (comparaison sur la réponse finale) ; le mandataire de l'agent n'est pas utilisé,
// le contrôle vise des services joignables depuis l'hôte.
func httpCheck(ctx context.Context, h config.HTTPCheck) (int, string) {
	ctx, cancel := context.WithTimeout(ctx, h.Timeout.D())
	defer cancel()
	client := &http.Client{Transport: &http.Transport{
		Proxy:             nil,
		TLSClientConfig:   &tls.Config{MinVersion: tls.VersionTLS12},
		DisableKeepAlives: true,
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.URL, nil)
	if err != nil {
		return model.CheckUnknown, err.Error()
	}
	req.Header.Set("User-Agent", "hypha-agent (contrôle)")
	start := time.Now()
	resp, err := client.Do(req)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		return model.CheckCrit, fmt.Sprintf("injoignable en %d ms : %v", elapsed, err)
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if resp.StatusCode != h.ExpectStatus {
		return model.CheckCrit, fmt.Sprintf("HTTP %d (attendu %d) en %d ms", resp.StatusCode, h.ExpectStatus, elapsed)
	}
	return model.CheckOK, fmt.Sprintf("HTTP %d en %d ms", resp.StatusCode, elapsed)
}

// tcpCheck : connexion établie → OK, refus ou délai dépassé → CRIT.
func tcpCheck(ctx context.Context, t config.TCPCheck) (int, string) {
	d := net.Dialer{Timeout: t.Timeout.D()}
	start := time.Now()
	c, err := d.DialContext(ctx, "tcp", t.Address)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		return model.CheckCrit, fmt.Sprintf("connexion impossible en %d ms : %v", elapsed, err)
	}
	c.Close()
	return model.CheckOK, fmt.Sprintf("connexion établie en %d ms", elapsed)
}

// stateEvent construit l'événement « X : ancien → nouveau ».
func stateEvent(now time.Time, kind, name, from, to, text, alert string, tags map[string]string) model.Event {
	title := fmt.Sprintf("%s : %s → %s", name, from, to)
	if from == "" {
		title = fmt.Sprintf("%s : %s", name, to)
	}
	return model.Event{
		TS: model.Ms(now), Source: "agent", Type: "service_state",
		Title: title, Text: text, AlertType: alert, Priority: "normal",
		Tags: tags, AggregationKey: kind + ":" + name,
	}
}
