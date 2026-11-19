package integrations

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// Nginx lit la page stub_status (module ngx_http_stub_status_module).
type Nginx struct {
	cfg      config.NginxConfig
	client   *http.Client
	prevReq  int64
	prevAt   time.Time
	havePrev bool
}

func newNginx(cfg config.NginxConfig) *Nginx {
	return &Nginx{cfg: cfg, client: &http.Client{Timeout: 5 * time.Second}}
}

func (n *Nginx) name() string { return "nginx" }
func (n *Nginx) close()       {}

type stubStatus struct {
	active, accepts, handled, requests, reading, writing, waiting int64
}

// parseStubStatus lit :
//
//	Active connections: 291
//	server accepts handled requests
//	 16630948 16630948 31070465
//	Reading: 6 Writing: 179 Waiting: 106
func parseStubStatus(text string) (stubStatus, error) {
	var s stubStatus
	lines := strings.Split(strings.ReplaceAll(text, "\r", ""), "\n")
	found := 0
	for i, line := range lines {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Active connections:"):
			v, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(line, "Active connections:")), 10, 64)
			if err != nil {
				return s, fmt.Errorf("stub_status : %q illisible", line)
			}
			s.active = v
			found++
		case strings.HasPrefix(line, "server accepts handled requests") && i+1 < len(lines):
			f := strings.Fields(lines[i+1])
			if len(f) < 3 {
				return s, fmt.Errorf("stub_status : compteurs illisibles")
			}
			var err1, err2, err3 error
			s.accepts, err1 = strconv.ParseInt(f[0], 10, 64)
			s.handled, err2 = strconv.ParseInt(f[1], 10, 64)
			s.requests, err3 = strconv.ParseInt(f[2], 10, 64)
			if err1 != nil || err2 != nil || err3 != nil {
				return s, fmt.Errorf("stub_status : compteurs illisibles")
			}
			found++
		case strings.HasPrefix(line, "Reading:"):
			f := strings.Fields(line) // Reading: 6 Writing: 179 Waiting: 106
			if len(f) < 6 {
				return s, fmt.Errorf("stub_status : %q illisible", line)
			}
			s.reading, _ = strconv.ParseInt(f[1], 10, 64)
			s.writing, _ = strconv.ParseInt(f[3], 10, 64)
			s.waiting, _ = strconv.ParseInt(f[5], 10, 64)
			found++
		}
	}
	if found < 3 {
		return s, fmt.Errorf("la page ne ressemble pas à stub_status")
	}
	return s, nil
}

func (n *Nginx) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, _ model.TagFunc) ([]model.Check, string) {
	if !n.cfg.Enabled {
		return nil, "disabled"
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, n.cfg.URL, nil)
	if err != nil {
		return nil, "erreur : " + err.Error()
	}
	resp, err := n.client.Do(req)
	if err != nil {
		return nil, "erreur : " + err.Error()
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Sprintf("erreur : %s : HTTP %d", n.cfg.URL, resp.StatusCode)
	}
	s, err := parseStubStatus(string(body))
	if err != nil {
		return nil, "erreur : " + err.Error()
	}
	nginxSeries(b, s, n.prevReq, n.havePrev, now.Sub(n.prevAt).Seconds())
	n.prevReq, n.prevAt, n.havePrev = s.requests, now, true
	return nil, "ok"
}

func nginxSeries(b *model.SeriesBuilder, s stubStatus, prevReq int64, havePrev bool, dt float64) {
	b.Gauge("nginx.connections.active", "count", float64(s.active))
	b.Gauge("nginx.connections.reading", "count", float64(s.reading))
	b.Gauge("nginx.connections.writing", "count", float64(s.writing))
	b.Gauge("nginx.connections.waiting", "count", float64(s.waiting))
	if havePrev {
		if v, ok := rateOf(s.requests, prevReq, dt); ok {
			b.Gauge("nginx.requests", "operation_per_second", v)
		}
	}
}
