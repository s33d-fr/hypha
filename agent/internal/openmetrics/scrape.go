package openmetrics

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"s33d.fr/hypha/agent/internal/agentlog"
	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// maxBody : taille maximale d'une exposition (kube-state-metrics d'une grosse grappe tient
// en quelques Mio ; au-delà, la cible est probablement mal choisie).
const maxBody = 32 << 20

const accept = "application/openmetrics-text;version=1.0.0;q=0.9,text/plain;version=0.0.4;q=0.5,*/*;q=0.1"

// Options : ce que le lecteur demande à l'agent.
type Options struct {
	Version   string
	Tags      model.TagFunc // étiquettes communes de l'agent (host, conf)
	Post      func(series []model.Series, checks []model.Check)
	SetStatus func(name, state string)
	Log       *slog.Logger
}

// Instance : une cible et son état.
type Instance struct {
	cfg      config.OpenMetricsInstance
	o        Options
	conv     *converter
	client   *http.Client
	setupErr error
	endpoint string
	limiter  *agentlog.Limiter

	mu    sync.Mutex
	state string
}

// Manager fait tourner les instances, chacune à sa cadence, dans sa propre boucle.
type Manager struct {
	list []*Instance
	o    Options
}

// New prépare les instances de la conf.
func New(cfgs []config.OpenMetricsInstance, o Options) *Manager {
	m := &Manager{o: o}
	for _, c := range cfgs {
		m.list = append(m.list, newInstance(c, o))
	}
	return m
}

// Len rend le nombre d'instances.
func (m *Manager) Len() int { return len(m.list) }

func newInstance(c config.OpenMetricsInstance, o Options) *Instance {
	in := &Instance{cfg: c, o: o, conv: newConverter(c), limiter: agentlog.NewLimiter(10 * time.Minute)}
	in.endpoint = endpointOf(c.URL)
	tr := &http.Transport{
		Proxy: nil, MaxIdleConns: 2, IdleConnTimeout: 2 * time.Minute,
		ResponseHeaderTimeout: c.Timeout.D(), TLSHandshakeTimeout: c.Timeout.D(),
	}
	tc, err := tlsConfig(c)
	if err != nil {
		in.setupErr = err
	}
	tr.TLSClientConfig = tc
	in.client = &http.Client{Transport: tr, Timeout: c.Timeout.D(), CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return errors.New("plus de 3 redirections")
		}
		return nil
	}}
	return in
}

func tlsConfig(c config.OpenMetricsInstance) (*tls.Config, error) {
	tc := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: c.Insecure} //nolint:gosec // choix explicite de la conf
	if c.CAFile != "" {
		pem, err := os.ReadFile(c.CAFile)
		if err != nil {
			return tc, fmt.Errorf("ca_file : %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return tc, fmt.Errorf("ca_file %s : aucun certificat PEM", c.CAFile)
		}
		tc.RootCAs = pool
	}
	if c.CertFile != "" {
		cert, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
		if err != nil {
			return tc, fmt.Errorf("certificat client : %w", err)
		}
		tc.Certificates = []tls.Certificate{cert}
	}
	return tc, nil
}

// endpointOf : l'adresse sans identifiants ni paramètres (étiquette endpoint).
func endpointOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.User, u.RawQuery, u.Fragment = nil, "", ""
	return u.String()
}

func (in *Instance) name() string { return "openmetrics:" + in.cfg.Name }

func (in *Instance) setState(s string) {
	in.mu.Lock()
	in.state = s
	in.mu.Unlock()
	if in.o.SetStatus != nil {
		in.o.SetStatus(in.name(), s)
	}
}

// tags : étiquettes communes + instance + étiquettes de l'échantillon.
func (in *Instance) tags(kv ...string) map[string]string {
	base := []string{config.InstanceTag, in.cfg.Name, "endpoint", in.endpoint}
	for _, k := range model.SortedKeys(in.cfg.Tags) {
		base = append(base, k, in.cfg.Tags[k])
	}
	return in.o.Tags(append(base, kv...)...)
}

// fetch lit l'exposition.
func (in *Instance) fetch(ctx context.Context) ([]byte, error) {
	if in.setupErr != nil {
		return nil, in.setupErr
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, in.cfg.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "hypha-agent/"+in.o.Version)
	for k, v := range in.cfg.Headers {
		req.Header.Set(k, v)
	}
	if f := in.cfg.BearerTokenFile; f != "" {
		tok, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("bearer_token_file : %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(tok)))
	}
	if in.cfg.Username != "" {
		req.SetBasicAuth(in.cfg.Username, in.cfg.Password)
	}
	resp, err := in.client.Do(req)
	if err != nil {
		return nil, cleanErr(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); strings.Contains(ct, "protobuf") {
		return nil, fmt.Errorf("format protobuf non lu (%s) : exposition texte attendue", ct)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, cleanErr(err)
	}
	if len(body) > maxBody {
		return nil, fmt.Errorf("exposition de plus de %d Mio", maxBody>>20)
	}
	return body, nil
}

// cleanErr retire l'URL (qui peut porter un jeton en paramètre) des erreurs du client HTTP.
func cleanErr(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// Scrape : un relevé complet ; rend les séries et le contrôle.
func (in *Instance) Scrape(ctx context.Context, now time.Time) ([]model.Series, []model.Check) {
	ts := model.Ms(now)
	start := time.Now()
	body, err := in.fetch(ctx)
	check := model.Check{TS: ts, Name: "openmetrics.can_connect", Tags: in.tags()}
	if err != nil {
		check.Status, check.Message = model.CheckCrit, fmt.Sprintf("%s : %v", in.endpoint, err)
		in.setState("erreur : " + err.Error())
		return nil, []model.Check{check}
	}
	fams, bad, perr := Parse(bytes.NewReader(body))
	if perr != nil || (len(fams) == 0 && bad > 0) {
		msg := fmt.Sprintf("exposition illisible (%d lignes refusées)", bad)
		if perr != nil {
			msg = "exposition illisible : " + perr.Error()
		}
		check.Status, check.Message = model.CheckCrit, in.endpoint+" : "+msg
		in.setState("erreur : " + msg)
		return nil, []model.Check{check}
	}
	points := in.conv.Convert(fams, now)
	kept := points
	dropped := 0
	if len(points) > in.cfg.MaxSeries {
		kept, dropped = points[:in.cfg.MaxSeries], len(points)-in.cfg.MaxSeries
	}
	out := make([]model.Series, 0, len(kept)+3)
	for _, p := range kept {
		out = append(out, model.Series{Metric: p.name, Type: p.typ, Unit: p.unit, Tags: in.tags(p.kv...),
			Points: []model.Point{{TS: ts, Value: p.value}}})
	}
	self := func(name, unit string, v float64) {
		out = append(out, model.Series{Metric: name, Type: model.TypeGauge, Unit: unit, Tags: in.tags(),
			Points: []model.Point{{TS: ts, Value: v}}})
	}
	self("openmetrics.series", "", float64(len(kept)))
	self("openmetrics.series.dropped", "", float64(dropped))
	self("openmetrics.scrape.duration", "second", time.Since(start).Seconds())
	check.Status = model.CheckOK
	check.Message = fmt.Sprintf("%s : %d familles, %d séries envoyées", in.endpoint, len(fams), len(kept))
	state := "ok"
	switch {
	case dropped > 0:
		check.Message += fmt.Sprintf(", %d écartées (max_series = %d)", dropped, in.cfg.MaxSeries)
		state = fmt.Sprintf("partiel : %d séries écartées (max_series = %d)", dropped, in.cfg.MaxSeries)
		if in.o.Log != nil && in.limiter.Allow("cap") {
			in.o.Log.Warn("OpenMetrics : plafond de séries atteint, séries écartées", "instance", in.cfg.Name,
				"séries", len(points), "plafond", in.cfg.MaxSeries, "écartées", dropped,
				"remède", "restreindre metrics / labels, ou relever max_series")
		}
	case bad > 0:
		state = fmt.Sprintf("partiel : %d lignes illisibles", bad)
	}
	in.setState(state)
	return out, []model.Check{check}
}

func (in *Instance) run(ctx context.Context) {
	every := in.cfg.Interval.D()
	// Départ étalé : plusieurs cibles ne sont pas lues au même instant.
	t := time.NewTimer(time.Duration(rand.Int64N(int64(min(every, 15*time.Second)))))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			in.once(ctx, now)
			next := every - time.Since(now)
			if next < time.Second {
				next = time.Second
			}
			t.Reset(next)
		}
	}
}

func (in *Instance) once(ctx context.Context, now time.Time) {
	defer func() {
		if r := recover(); r != nil {
			in.setState(fmt.Sprintf("erreur : panique : %v", r))
		}
	}()
	sctx, cancel := context.WithTimeout(ctx, in.cfg.Timeout.D()+5*time.Second)
	defer cancel()
	series, checks := in.Scrape(sctx, now)
	in.o.Post(series, checks)
}

// Run fait tourner toutes les instances jusqu'à l'arrêt.
func (m *Manager) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, in := range m.list {
		in.setState("ok")
		wg.Add(1)
		go func(in *Instance) {
			defer wg.Done()
			in.run(ctx)
		}(in)
	}
	wg.Wait()
}

// Once : deux relevés à 2 s d'écart (run --once), pour que les débits existent.
func (m *Manager) Once(ctx context.Context) {
	for _, in := range m.list {
		sctx, cancel := context.WithTimeout(ctx, in.cfg.Timeout.D())
		in.Scrape(sctx, time.Now())
		cancel()
	}
	select {
	case <-ctx.Done():
		return
	case <-time.After(2 * time.Second):
	}
	for _, in := range m.list {
		in.once(ctx, time.Now())
	}
}

// Active : instances en service (capacités de l'agent).
func (m *Manager) Active() []string {
	var out []string
	for _, in := range m.list {
		in.mu.Lock()
		st := in.state
		in.mu.Unlock()
		if st == "ok" || strings.HasPrefix(st, "partiel") || strings.HasPrefix(st, "erreur") {
			out = append(out, in.name())
		}
	}
	return out
}
