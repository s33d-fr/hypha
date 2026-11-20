package integrations

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/hostfs"
	"s33d.fr/hypha/agent/internal/model"
)

// ── Supervisor : API XML-RPC (supervisor.getAllProcessInfo) ─────────────────────────────────
//
// Par la socket Unix du [unix_http_server] (trouvée seule aux emplacements des paquets), ou par
// le [inet_http_server] (url). Un contrôle supervisord.process.status par programme : RUNNING en
// OK, STARTING en UNKNOWN, STOPPED en WARN (arrêt voulu le plus souvent), le reste en CRIT.

var supervisordSockets = []string{"/run/supervisor.sock", "/var/run/supervisor.sock",
	"/run/supervisor/supervisor.sock", "/var/run/supervisor/supervisor.sock", "/run/supervisord.sock", "/var/run/supervisord.sock"}

const supervisordMaxProcs = 200

type Supervisord struct {
	probe
	cfg config.SupervisordConfig
}

func newSupervisord(cfg config.SupervisordConfig) *Supervisord {
	where := cfg.URL
	if where == "" {
		where = cfg.Socket
	}
	return &Supervisord{cfg: cfg, probe: newProbe("supervisord", "supervisord.can_connect",
		intent(cfg.Enabled, cfg.URL != "" || cfg.Socket != "" || cfg.Username != ""))}
}

func (s *Supervisord) period() time.Duration { return 30 * time.Second }

func (s *Supervisord) client() (*httpProbe, error) {
	if s.cfg.URL != "" {
		return newHTTPProbe(s.cfg.URL, s.cfg.Username, s.cfg.Password, false), nil
	}
	cands := supervisordSockets
	if s.cfg.Socket != "" {
		cands = []string{s.cfg.Socket}
	}
	for _, c := range cands {
		p := hostfs.Root(c)
		if fi, err := os.Stat(p); err == nil && fi.Mode()&os.ModeSocket != 0 {
			h := newHTTPProbe("http://localhost/RPC2", s.cfg.Username, s.cfg.Password, false)
			h.client = unixHTTP(p)
			return h, nil
		}
	}
	if s.cfg.Socket != "" {
		return nil, fmt.Errorf("socket %s absente", s.cfg.Socket)
	}
	return nil, fmt.Errorf("aucune socket de Supervisor trouvée")
}

// ── XML-RPC réduit : appel sans paramètre, réponse en valeurs Go ─────────────────────────

type xrNode struct {
	XMLName xml.Name
	Nodes   []xrNode `xml:",any"`
	Text    string   `xml:",chardata"`
}

// xrDecode convertit un nœud <value> : chaîne, entier, booléen, réel, tableau, structure.
func xrDecode(n xrNode) any {
	if n.XMLName.Local == "value" {
		if len(n.Nodes) == 0 {
			return n.Text // <value>texte</value> : chaîne implicite
		}
		n = n.Nodes[0]
	}
	switch n.XMLName.Local {
	case "string":
		return n.Text
	case "int", "i4", "i8":
		v, _ := strconv.ParseInt(strings.TrimSpace(n.Text), 10, 64)
		return float64(v)
	case "double":
		v, _ := strconv.ParseFloat(strings.TrimSpace(n.Text), 64)
		return v
	case "boolean":
		return strings.TrimSpace(n.Text) == "1"
	case "array":
		var out []any
		for _, d := range n.Nodes { // <data>
			for _, v := range d.Nodes {
				out = append(out, xrDecode(v))
			}
		}
		return out
	case "struct":
		out := map[string]any{}
		for _, m := range n.Nodes { // <member><name/><value/></member>
			var name string
			var val any
			for _, x := range m.Nodes {
				switch x.XMLName.Local {
				case "name":
					name = strings.TrimSpace(x.Text)
				case "value":
					val = xrDecode(x)
				}
			}
			out[name] = val
		}
		return out
	}
	return n.Text
}

// xmlrpcCall appelle une méthode sans paramètre ; une faute XML-RPC devient une erreur.
func xmlrpcCall(ctx context.Context, h *httpProbe, method string) (any, error) {
	body := `<?xml version="1.0"?><methodCall><methodName>` + method + `</methodName><params></params></methodCall>`
	data, code, err := h.do(ctx, http.MethodPost, "", strings.NewReader(body), map[string]string{"Content-Type": "text/xml"})
	if err != nil {
		return nil, err
	}
	if err := httpStatusErr(method, code, data); err != nil {
		return nil, err
	}
	var resp xrNode
	if err := xml.NewDecoder(io.LimitReader(bytes.NewReader(data), 16<<20)).Decode(&resp); err != nil {
		return nil, fmt.Errorf("%s : réponse XML-RPC illisible", method)
	}
	for _, n := range resp.Nodes {
		switch n.XMLName.Local {
		case "fault":
			if len(n.Nodes) > 0 {
				if f, ok := xrDecode(n.Nodes[0]).(map[string]any); ok {
					return nil, fmt.Errorf("%s : faute %v (%v)", method, f["faultCode"], f["faultString"])
				}
			}
			return nil, fmt.Errorf("%s : faute XML-RPC", method)
		case "params":
			for _, p := range n.Nodes {
				for _, v := range p.Nodes {
					return xrDecode(v), nil
				}
			}
		}
	}
	return nil, fmt.Errorf("%s : réponse XML-RPC vide", method)
}

var supervisordStatus = map[string]int{
	"RUNNING": model.CheckOK, "STARTING": model.CheckUnknown, "STOPPED": model.CheckWarn, "STOPPING": model.CheckWarn,
	"BACKOFF": model.CheckCrit, "EXITED": model.CheckCrit, "FATAL": model.CheckCrit, "UNKNOWN": model.CheckUnknown,
}

func (s *Supervisord) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := s.det.skip(now); skip {
		return nil, st
	}
	ts := model.Ms(now)
	h, err := s.client()
	var raw any
	if err == nil {
		raw, err = xmlrpcCall(ctx, h, "supervisor.getAllProcessInfo")
	}
	list, ok := raw.([]any)
	if err == nil && !ok {
		err = fmt.Errorf("supervisor.getAllProcessInfo : réponse inattendue")
	}
	if err != nil {
		return s.fail(ts, tags, err)
	}
	s.det.ok()
	version := ""
	if v, err := xmlrpcCall(ctx, h, "supervisor.getSupervisorVersion"); err == nil {
		version, _ = v.(string)
	}
	byState := map[string]float64{}
	for st := range supervisordStatus {
		byState[st] = 0
	}
	var checks []model.Check
	type proc struct {
		name string
		info map[string]any
	}
	var procs []proc
	for _, x := range list {
		m, ok := x.(map[string]any)
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		if g, _ := m["group"].(string); g != "" && g != name {
			name = g + ":" + name
		}
		procs = append(procs, proc{name, m})
	}
	sort.Slice(procs, func(i, j int) bool { return procs[i].name < procs[j].name })
	for i, p := range procs {
		state, _ := p.info["statename"].(string)
		byState[state]++
		if i >= supervisordMaxProcs {
			continue
		}
		kv := s.kvs("supervisord_process", p.name)
		start, _ := num(p.info["start"])
		cur, _ := num(p.info["now"])
		if state == "RUNNING" && start > 0 && cur >= start {
			b.Gauge("supervisord.process.uptime", "second", cur-start, kv...)
		}
		st, known := supervisordStatus[state]
		if !known {
			st = model.CheckUnknown
		}
		msg := state
		if d, _ := p.info["description"].(string); d != "" {
			msg += " : " + d
		}
		if e, _ := p.info["spawnerr"].(string); e != "" && !strings.Contains(msg, e) {
			msg += " (" + e + ")"
		}
		checks = append(checks, model.Check{TS: ts, Name: "supervisord.process.status", Status: st,
			Message: model.Truncate(msg, 300), Tags: tags(kv...)})
	}
	for st, n := range byState {
		b.Gauge("supervisord.process.count", "process", n, s.kvs("status", strings.ToLower(st))...)
	}
	msg := fmt.Sprintf("%d programme(s) suivi(s)", len(procs))
	if version != "" {
		msg = "Supervisor " + version + " joignable, " + msg
	}
	checks = append(checks, s.okCheck(ts, tags, msg))
	return checks, "ok"
}
