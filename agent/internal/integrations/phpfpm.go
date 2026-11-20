package integrations

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/hostfs"
	"s33d.fr/hypha/agent/internal/model"
)

// ── PHP-FPM : page de statut (pm.status_path), en FastCGI sur la socket du pool ─────────────
//
// L'agent parle FastCGI directement à la socket du pool (aucune page à ouvrir dans le serveur
// web), ou lit la page en HTTP si url est posée. Sans conf, chaque socket /run/php/php*-fpm.sock
// ou /run/php-fpm/*.sock est lue : un pool par socket, étiqueté par son nom (pool).

const phpfpmTimeout = 5 * time.Second

var phpfpmSocketGlobs = []string{"/run/php/php*-fpm.sock", "/run/php/*.sock", "/run/php-fpm/*.sock", "/var/run/php-fpm/*.sock"}

type PHPFPM struct {
	probe
	cfg  config.PHPFPMConfig
	note string // raison d'un pool trouvé mais illisible (page de statut non réglée)
}

func newPHPFPM(cfg config.PHPFPMConfig) *PHPFPM {
	return &PHPFPM{cfg: cfg, probe: newProbe("phpfpm", "php_fpm.can_connect", intent(cfg.Enabled, cfg.Address != "" || cfg.URL != ""))}
}

func (p *PHPFPM) period() time.Duration { return 30 * time.Second }

// targets : la cible posée, sinon les sockets des pools trouvées sur l'hôte.
func (p *PHPFPM) targets() []string {
	switch {
	case p.cfg.URL != "":
		return []string{p.cfg.URL}
	case p.cfg.Address != "":
		return []string{p.cfg.Address}
	}
	seen := map[string]bool{}
	var out []string
	for _, g := range phpfpmSocketGlobs {
		matches, _ := filepath.Glob(hostfs.Root(g))
		for _, m := range matches {
			if r, err := filepath.EvalSymlinks(m); err == nil {
				m = r
			}
			if !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
	}
	sort.Strings(out)
	return out
}

type phpfpmStatus struct {
	Pool            string  `json:"pool"`
	ProcessManager  string  `json:"process manager"`
	StartSince      float64 `json:"start since"`
	AcceptedConn    float64 `json:"accepted conn"`
	ListenQueue     float64 `json:"listen queue"`
	MaxListenQueue  float64 `json:"max listen queue"`
	ListenQueueLen  float64 `json:"listen queue len"`
	IdleProcesses   float64 `json:"idle processes"`
	ActiveProcesses float64 `json:"active processes"`
	TotalProcesses  float64 `json:"total processes"`
	MaxActive       float64 `json:"max active processes"`
	MaxChildren     float64 `json:"max children reached"`
	SlowRequests    float64 `json:"slow requests"`
	MemoryPeak      float64 `json:"memory peak"`
}

// fetch lit une page du pool (chemin et requête) : FastCGI vers une socket, ou HTTP.
func (p *PHPFPM) fetch(ctx context.Context, target, path, query string) (int, []byte, error) {
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		u := target
		if path != p.cfg.StatusPath { // le ping vit à côté de la page de statut
			if i := strings.LastIndex(u, p.cfg.StatusPath); i > 0 {
				u = u[:i] + path
			}
		}
		if query != "" {
			if strings.Contains(u, "?") {
				u += "&" + query
			} else {
				u += "?" + query
			}
		}
		h := newHTTPProbe(u, "", "", false)
		body, code, err := h.do(ctx, http.MethodGet, "", nil, nil)
		return code, body, err
	}
	return fcgiGet(ctx, target, path, query)
}

// fcgiGet : une requête FastCGI GET (rôle responder) ; rend le code de la réponse CGI.
func fcgiGet(ctx context.Context, address, path, query string) (int, []byte, error) {
	network := "tcp"
	if filepath.IsAbs(address) {
		network = "unix"
	}
	d := net.Dialer{Timeout: phpfpmTimeout}
	conn, err := d.DialContext(ctx, network, address)
	if err != nil {
		return 0, nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * phpfpmTimeout))
	var req bytes.Buffer
	writeRec := func(typ byte, content []byte) {
		pad := (8 - len(content)%8) % 8
		req.Write([]byte{1, typ, 0, 1, byte(len(content) >> 8), byte(len(content)), byte(pad), 0})
		req.Write(content)
		req.Write(make([]byte, pad))
	}
	writeRec(1, []byte{0, 1, 0, 0, 0, 0, 0, 0}) // BEGIN_REQUEST, rôle responder, sans keep-alive
	var params bytes.Buffer
	for _, kv := range [][2]string{
		{"GATEWAY_INTERFACE", "CGI/1.1"}, {"SERVER_SOFTWARE", "hypha-agent"}, {"SERVER_PROTOCOL", "HTTP/1.1"},
		{"REQUEST_METHOD", "GET"}, {"SCRIPT_NAME", path}, {"SCRIPT_FILENAME", path}, {"DOCUMENT_URI", path},
		{"REQUEST_URI", path + map[bool]string{true: "?" + query, false: ""}[query != ""]},
		{"QUERY_STRING", query}, {"REMOTE_ADDR", "127.0.0.1"}, {"SERVER_ADDR", "127.0.0.1"}, {"SERVER_NAME", "localhost"},
	} {
		for _, s := range kv {
			if n := len(s); n < 128 {
				params.WriteByte(byte(n))
			} else {
				_ = binary.Write(&params, binary.BigEndian, uint32(n)|1<<31)
			}
		}
		params.WriteString(kv[0])
		params.WriteString(kv[1])
	}
	writeRec(4, params.Bytes()) // PARAMS
	writeRec(4, nil)
	writeRec(5, nil) // STDIN vide
	if _, err := conn.Write(req.Bytes()); err != nil {
		return 0, nil, err
	}
	var stdout, stderr bytes.Buffer
	rd := bufio.NewReader(conn)
	for {
		var h [8]byte
		if _, err := io.ReadFull(rd, h[:]); err != nil {
			return 0, nil, fmt.Errorf("réponse FastCGI incomplète : %v", err)
		}
		n := int(h[4])<<8 | int(h[5])
		buf := make([]byte, n+int(h[6]))
		if _, err := io.ReadFull(rd, buf); err != nil {
			return 0, nil, fmt.Errorf("réponse FastCGI incomplète : %v", err)
		}
		switch h[1] {
		case 6:
			if stdout.Len() < 4<<20 {
				stdout.Write(buf[:n])
			}
		case 7:
			stderr.Write(buf[:min(n, 512)])
		case 3: // END_REQUEST
			return parseCGI(stdout.Bytes())
		}
	}
}

// parseCGI sépare les en-têtes CGI du corps ; « Status: 404 Not Found » donne le code.
func parseCGI(out []byte) (int, []byte, error) {
	head, body, ok := bytes.Cut(out, []byte("\r\n\r\n"))
	if !ok {
		head, body, ok = bytes.Cut(out, []byte("\n\n"))
	}
	if !ok {
		return 0, nil, fmt.Errorf("réponse FastCGI sans en-têtes")
	}
	code := 200
	for _, l := range strings.Split(string(head), "\n") {
		k, v, _ := strings.Cut(strings.TrimSpace(l), ":")
		if strings.EqualFold(k, "Status") {
			if c, err := strconv.Atoi(strings.Fields(v + " 0")[0]); err == nil {
				code = c
			}
		}
	}
	return code, body, nil
}

func (p *PHPFPM) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := p.det.skip(now); skip {
		if st == "absent" && p.note != "" {
			return nil, p.note
		}
		return nil, st
	}
	ts := model.Ms(now)
	targets := p.targets()
	if len(targets) == 0 {
		return p.fail(ts, tags, fmt.Errorf("aucune socket de pool PHP-FPM trouvée"))
	}
	ctx, cancel := context.WithTimeout(ctx, 3*phpfpmTimeout)
	defer cancel()
	p.begin()
	defer p.end()
	var checks []model.Check
	var errs []string
	read := 0
	for _, t := range targets {
		code, body, err := p.fetch(ctx, t, p.cfg.StatusPath, "json")
		if err == nil && code == 404 {
			err = fmt.Errorf("%s : page %s introuvable (pm.status_path = %s dans la conf du pool)", t, p.cfg.StatusPath, p.cfg.StatusPath)
		} else if err == nil && code != 200 {
			err = fmt.Errorf("%s : HTTP %d", t, code)
		}
		var s phpfpmStatus
		if err == nil {
			if jerr := json.Unmarshal(body, &s); jerr != nil || s.Pool == "" {
				err = fmt.Errorf("%s : page de statut illisible", t)
			}
		}
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		read++
		kv := p.kvs("pool", s.Pool)
		b.Gauge("php_fpm.processes.active", "process", s.ActiveProcesses, kv...)
		b.Gauge("php_fpm.processes.idle", "process", s.IdleProcesses, kv...)
		b.Gauge("php_fpm.processes.total", "process", s.TotalProcesses, kv...)
		b.Gauge("php_fpm.processes.max_active", "process", s.MaxActive, kv...)
		b.Gauge("php_fpm.listen_queue.size", "connection", s.ListenQueue, kv...)
		b.Gauge("php_fpm.listen_queue.max", "connection", s.MaxListenQueue, kv...)
		b.Gauge("php_fpm.listen_queue.len", "connection", s.ListenQueueLen, kv...)
		b.Gauge("php_fpm.uptime", "second", s.StartSince, kv...)
		if s.TotalProcesses > 0 {
			b.Gauge("php_fpm.processes.usage", "percent", round1(100*s.ActiveProcesses/s.TotalProcesses), kv...)
		}
		if s.MemoryPeak > 0 {
			b.Gauge("php_fpm.memory.peak", "byte", s.MemoryPeak, kv...)
		}
		p.count(b, "php_fpm.requests.accepted", "request", s.Pool+"/accepted", s.AcceptedConn, kv...)
		p.count(b, "php_fpm.requests.slow", "request", s.Pool+"/slow", s.SlowRequests, kv...)
		p.count(b, "php_fpm.processes.max_reached", "occurrence", s.Pool+"/max_children", s.MaxChildren, kv...)
		msg := fmt.Sprintf("pool %s joignable (%s, %g processus)", s.Pool, s.ProcessManager, s.TotalProcesses)
		if p.cfg.PingPath != "" {
			if code, body, err := p.fetch(ctx, t, p.cfg.PingPath, ""); err == nil && code == 200 {
				st := model.CheckOK
				if strings.TrimSpace(string(body)) != "pong" {
					st = model.CheckWarn
				}
				checks = append(checks, model.Check{TS: ts, Name: "php_fpm.can_ping", Status: st,
					Message: "ping : " + model.Truncate(strings.TrimSpace(string(body)), 60), Tags: tags(kv...)})
			}
		}
		checks = append(checks, model.Check{TS: ts, Name: "php_fpm.can_connect", Status: model.CheckOK, Message: msg, Tags: tags(kv...)})
	}
	if read == 0 {
		err := fmt.Errorf("%s", strings.Join(errs, " ; "))
		if p.det.mode == "auto" && !p.det.seen && strings.Contains(err.Error(), "introuvable") {
			p.note = "inactif : " + err.Error()
			p.det.failed(err.Error())
			return nil, p.note
		}
		return p.fail(ts, tags, err)
	}
	p.det.ok()
	if len(errs) > 0 {
		checks = append(checks, model.Check{TS: ts, Name: "php_fpm.can_connect", Status: model.CheckCrit,
			Message: strings.Join(errs, " ; "), Tags: tags(p.where...)})
		return checks, "partiel : " + strings.Join(errs, " ; ")
	}
	return checks, "ok"
}
