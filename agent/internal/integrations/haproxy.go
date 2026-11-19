package integrations

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/hostfs"
	"s33d.fr/hypha/agent/internal/model"
)

// Types de ligne de « show stat » (colonne type).
const (
	hapFrontend = 0
	hapBackend  = 1
	hapServer   = 2
)

// HAProxy lit « show stat » (CSV) par la socket d'administration, ou la page CSV des stats.
type HAProxy struct {
	cfg    config.HAProxyConfig
	client *http.Client
	prev   map[string]hapRow
	prevAt time.Time
}

func newHAProxy(cfg config.HAProxyConfig) *HAProxy {
	return &HAProxy{cfg: cfg, prev: map[string]hapRow{}, client: &http.Client{Timeout: 5 * time.Second}}
}

func (h *HAProxy) name() string { return "haproxy" }
func (h *HAProxy) close()       {}

// hapRow : les colonnes utiles d'une ligne ; -1 pour une colonne vide (les codes HTTP
// n'existent pas pour un proxy en mode tcp).
type hapRow struct {
	px, sv, status               string
	typ                          int
	qcur, scur                   int64
	bin, bout, ereq, econ, eresp int64
	h2xx, h3xx, h4xx, h5xx       int64
}

func (r hapRow) key() string { return strconv.Itoa(r.typ) + "|" + r.px + "|" + r.sv }

func (h *HAProxy) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	socket := hostfs.Root(h.cfg.Socket)
	useSocket := h.cfg.Socket != ""
	if useSocket {
		if _, err := os.Stat(socket); err != nil {
			useSocket = false
		}
	}
	switch {
	case h.cfg.Enabled == "false":
		return nil, "disabled"
	case !useSocket && h.cfg.URL == "":
		if h.cfg.Enabled == "auto" {
			return nil, "absent"
		}
		return nil, "erreur : ni socket " + h.cfg.Socket + " ni url"
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var data []byte
	var err error
	if useSocket {
		data, err = hapFromSocket(ctx, socket)
	} else {
		data, err = h.fromURL(ctx)
	}
	if err != nil {
		return nil, "erreur : " + err.Error()
	}
	rows, err := parseHAProxyStat(data)
	if err != nil {
		return nil, "erreur : " + err.Error()
	}
	dt := 0.0
	if !h.prevAt.IsZero() {
		dt = now.Sub(h.prevAt).Seconds()
	}
	checks := hapSeries(b, now, rows, h.prev, dt, tags)
	h.prev = make(map[string]hapRow, len(rows))
	for _, r := range rows {
		h.prev[r.key()] = r
	}
	h.prevAt = now
	return checks, "ok"
}

// hapFromSocket envoie « show stat » ; HAProxy répond puis ferme la connexion.
func hapFromSocket(ctx context.Context, path string) ([]byte, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	if dl, ok := ctx.Deadline(); ok {
		c.SetDeadline(dl)
	}
	if _, err := c.Write([]byte("show stat\n")); err != nil {
		return nil, err
	}
	return io.ReadAll(io.LimitReader(c, 64<<20))
}

func (h *HAProxy) fromURL(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.cfg.URL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s : HTTP %d", h.cfg.URL, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

// parseHAProxyStat lit le CSV de « show stat » ; les colonnes sont retrouvées par leur
// nom dans l'en-tête (« # pxname,svname,… »), leur ordre variant selon les versions.
func parseHAProxyStat(data []byte) ([]hapRow, error) {
	data = bytes.TrimPrefix(bytes.TrimSpace(data), []byte("# "))
	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("sortie de show stat illisible : %w", err)
	}
	col := map[string]int{}
	for i, name := range header {
		col[strings.TrimSpace(name)] = i
	}
	for _, need := range []string{"pxname", "svname", "type", "scur", "bin", "bout", "status"} {
		if _, ok := col[need]; !ok {
			return nil, errors.New("sortie de show stat inattendue : colonne " + need + " absente")
		}
	}
	var rows []hapRow
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		get := func(name string) string {
			if i, ok := col[name]; ok && i < len(rec) {
				return strings.TrimSpace(rec[i])
			}
			return ""
		}
		num := func(name string) int64 {
			v, err := strconv.ParseInt(get(name), 10, 64)
			if err != nil {
				return -1
			}
			return v
		}
		typ, err := strconv.Atoi(get("type"))
		if err != nil || typ > hapServer {
			continue // ligne d'écouteur (type 3) ou vide
		}
		rows = append(rows, hapRow{
			px: get("pxname"), sv: get("svname"), status: get("status"), typ: typ,
			qcur: num("qcur"), scur: num("scur"), bin: num("bin"), bout: num("bout"),
			ereq: num("ereq"), econ: num("econ"), eresp: num("eresp"),
			h2xx: num("hrsp_2xx"), h3xx: num("hrsp_3xx"), h4xx: num("hrsp_4xx"), h5xx: num("hrsp_5xx"),
		})
	}
	return rows, nil
}

// serverUp : UP (et ses transitions « UP 1/3 »), sans contrôle de santé, en drainage ou
// NOLB comptent comme en service ; DOWN et MAINT non.
func serverUp(status string) bool {
	s := strings.ToUpper(status)
	return strings.HasPrefix(s, "UP") || s == "NO CHECK" || strings.HasPrefix(s, "DRAIN") || strings.HasPrefix(s, "NOLB") || s == "OPEN"
}

// hapSeries construit les séries et le contrôle haproxy.backend_up de chaque backend.
func hapSeries(b *model.SeriesBuilder, now time.Time, rows []hapRow, prev map[string]hapRow, dt float64, tags model.TagFunc) []model.Check {
	type counts struct{ up, total int }
	servers := map[string]*counts{}
	var order []string
	for _, r := range rows {
		switch r.typ {
		case hapServer:
			up := 0.0
			if serverUp(r.status) {
				up = 1
			}
			b.Gauge("haproxy.server.up", "", up, "backend", r.px, "server", r.sv)
			c := servers[r.px]
			if c == nil {
				c = &counts{}
				servers[r.px] = c
				order = append(order, r.px)
			}
			c.total++
			if up == 1 {
				c.up++
			}
			continue
		case hapFrontend, hapBackend:
		default:
			continue
		}
		kind, tag := "frontend", "frontend"
		if r.typ == hapBackend {
			kind, tag = "backend", "backend"
		}
		prefix := "haproxy." + kind + "."
		if r.scur >= 0 {
			b.Gauge(prefix+"sessions.current", "count", float64(r.scur), tag, r.px)
		}
		if r.typ == hapBackend && r.qcur >= 0 {
			b.Gauge(prefix+"queue.current", "count", float64(r.qcur), tag, r.px)
		}
		p, had := prev[r.key()]
		if !had || dt <= 0 {
			continue
		}
		rate := func(name, unit string, c, o int64) {
			if c < 0 || o < 0 {
				return
			}
			if v, ok := rateOf(c, o, dt); ok {
				b.Gauge(prefix+name, unit, v, tag, r.px)
			}
		}
		rate("bytes.in", "byte_per_second", r.bin, p.bin)
		rate("bytes.out", "byte_per_second", r.bout, p.bout)
		rate("response.2xx", "operation_per_second", r.h2xx, p.h2xx)
		rate("response.3xx", "operation_per_second", r.h3xx, p.h3xx)
		rate("response.4xx", "operation_per_second", r.h4xx, p.h4xx)
		rate("response.5xx", "operation_per_second", r.h5xx, p.h5xx)
		if r.typ == hapFrontend {
			rate("errors", "operation_per_second", r.ereq, p.ereq) // requêtes en erreur
		} else if r.econ >= 0 && r.eresp >= 0 && p.econ >= 0 && p.eresp >= 0 {
			rate("errors", "operation_per_second", r.econ+r.eresp, p.econ+p.eresp) // connexion ou réponse en erreur
		}
	}

	// Un backend sans aucun serveur en service ne peut plus rien servir : CRIT.
	ts := model.Ms(now)
	var checks []model.Check
	for _, px := range order {
		c := servers[px]
		st := model.CheckOK
		if c.up == 0 {
			st = model.CheckCrit
		}
		checks = append(checks, model.Check{TS: ts, Name: "haproxy.backend_up", Status: st,
			Message: fmt.Sprintf("%d/%d serveurs en service", c.up, c.total), Tags: tags("backend", px)})
	}
	return checks
}
