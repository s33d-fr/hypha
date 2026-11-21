package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/hostfs"
	"s33d.fr/hypha/agent/internal/model"
)

// ── Odoo (vague 6) : santé, version, temps de réponse, processus ────────────────────────────
//
// Toutes les 30 s : /web/health (Odoo 17 et suivants ; ?db_server_status=1 depuis Odoo 18 :
// le serveur PostgreSQL est joint, et sa panne rend « fail »), version par XML-RPC
// (/xmlrpc/2/common, méthode version, sans compte) avec repli sur /web/webclient/version_info
// (XML-RPC retiré ou coupé), temps de réponse de chacun. Quand l'url vise la machine : processus
// d'Odoo lus dans /proc (maître ou serveur à fils, workers : HTTP et cron quand setproctitle les
// nomme, sinon « worker », processus gevent), leur nombre et leur mémoire résidente. Rien n'est écrit, aucune base ouverte.

type Odoo struct {
	probe
	cfg     config.OdooConfig
	http    *httpProbe
	xmlrpc  *httpProbe
	proc    string
	version string
}

func newOdoo(cfg config.OdooConfig) *Odoo {
	return &Odoo{cfg: cfg, http: newHTTPProbe(cfg.URL, "", "", cfg.Insecure),
		xmlrpc: newHTTPProbe(cfg.URL+"/xmlrpc/2/common", "", "", cfg.Insecure), proc: hostfs.Proc(),
		probe: newProbe("odoo", "odoo.can_connect", intent(cfg.Enabled, cfg.URL != config.DefaultOdooURL), "instance", cfg.URL)}
}

func (o *Odoo) period() time.Duration { return 30 * time.Second }

type odooHealth struct {
	Status   string `json:"status"`
	DBServer *bool  `json:"db_server_status"`
}

// health : /web/health?db_server_status=1, puis /web/health seul (Odoo 17 refuse le paramètre).
func (o *Odoo) health(ctx context.Context) (odooHealth, time.Duration, error) {
	var h odooHealth
	var last error
	for _, p := range []string{"/web/health?db_server_status=1", "/web/health"} {
		t0 := time.Now()
		data, code, err := o.http.do(ctx, http.MethodGet, p, nil, map[string]string{"Accept": "application/json"})
		took := time.Since(t0)
		if err != nil {
			return h, took, err
		}
		h = odooHealth{}
		if json.Unmarshal(data, &h) == nil && h.Status != "" {
			return h, took, nil // 200 « pass », ou 500 « fail » avec db_server_status: false
		}
		last = httpStatusErr("/web/health", code, data)
		if last == nil {
			last = fmt.Errorf("/web/health : réponse inattendue (Odoo 17 ou suivant attendu)")
		}
		if code == http.StatusNotFound {
			break
		}
	}
	return h, 0, last
}

// version : XML-RPC common.version ; repli sur /web/webclient/version_info (JSON-RPC).
func (o *Odoo) versionInfo(ctx context.Context) (string, string, time.Duration, error) {
	t0 := time.Now()
	v, err := xmlrpcCall(ctx, o.xmlrpc, "version")
	if err == nil {
		if m, ok := v.(map[string]any); ok {
			sv, _ := m["server_version"].(string)
			return sv, "xmlrpc", time.Since(t0), nil
		}
		err = fmt.Errorf("version : réponse XML-RPC inattendue")
	}
	t0 = time.Now()
	data, code, e2 := o.http.do(ctx, http.MethodPost, "/web/webclient/version_info",
		strings.NewReader(`{"jsonrpc":"2.0","method":"call","params":{}}`), map[string]string{"Content-Type": "application/json"})
	if e2 == nil && code == http.StatusOK {
		var r struct {
			Result struct {
				ServerVersion string `json:"server_version"`
			} `json:"result"`
		}
		if json.Unmarshal(data, &r) == nil && r.Result.ServerVersion != "" {
			return r.Result.ServerVersion, "version_info", time.Since(t0), nil
		}
	}
	return "", "", 0, err
}

func (o *Odoo) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := o.det.skip(now); skip {
		return nil, st
	}
	ts := model.Ms(now)
	h, took, err := o.health(ctx)
	if err != nil {
		return o.fail(ts, tags, err)
	}
	o.det.ok()
	kv := o.kvs()
	var checks []model.Check
	var errs []string
	b.Gauge("odoo.response_time", "millisecond", round1(float64(took.Microseconds())/1000), o.kvs("endpoint", "health")...)
	st, msg := model.CheckOK, "Odoo répond (/web/health : pass)"
	if h.DBServer != nil {
		v := 0.0
		if *h.DBServer {
			v = 1
			msg += ", serveur PostgreSQL joint"
		}
		b.Gauge("odoo.database.reachable", "", v, kv...)
	}
	if h.Status != "pass" {
		st, msg = model.CheckCrit, "Odoo en défaut (/web/health : "+h.Status+")"
		if h.DBServer != nil && !*h.DBServer {
			msg += " : serveur PostgreSQL injoignable"
		}
	}
	checks = append(checks, model.Check{TS: ts, Name: "odoo.health", Status: st, Message: msg, Tags: tags(kv...)})

	if v, via, took, err := o.versionInfo(ctx); err == nil {
		o.version = v
		b.Gauge("odoo.response_time", "millisecond", round1(float64(took.Microseconds())/1000), o.kvs("endpoint", via)...)
	} else {
		errs = append(errs, err.Error())
	}

	if o.cfg.ProcessesOn() {
		if procs, err := scanOdoo(o.proc); err == nil {
			count := map[string]float64{"main": 0, "worker": 0, "http": 0, "cron": 0, "gevent": 0}
			mem := map[string]float64{}
			for _, p := range procs {
				count[p.role]++
				mem[p.role] += p.rss
			}
			for role, n := range count {
				b.Gauge("odoo.processes", "process", n, o.kvs("role", role)...)
				if n > 0 {
					b.Gauge("odoo.processes.memory", "byte", mem[role], o.kvs("role", role)...)
				}
			}
			b.Gauge("odoo.workers", "process", count["worker"]+count["http"]+count["cron"], kv...)
		} else {
			errs = append(errs, "processus : "+err.Error())
		}
	}
	ok := "Odoo"
	if o.version != "" {
		ok += " " + o.version
	}
	checks = append(checks, o.okCheck(ts, tags, ok+" joignable"))
	if len(errs) > 0 {
		return checks, "partiel : " + strings.Join(errs, " ; ")
	}
	return checks, "ok"
}

type odooProc struct {
	pid, ppid int
	role      string // main, worker (sans titre), http, cron, gevent
	rss       float64
}

// odooTitleRe : titre posé par setproctitle sur les workers (« odoo: WorkerHTTP 42 »).
var odooTitleRe = regexp.MustCompile(`^odoo: (Worker\w*|gevent)`)

// odooRole : rôle d'un processus Odoo d'après son titre ou sa ligne de commande ; "" si ce n'en est pas un.
func odooRole(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	if m := odooTitleRe.FindStringSubmatch(strings.Join(argv, " ")); m != nil {
		switch m[1] {
		case "WorkerHTTP":
			return "http"
		case "WorkerCron":
			return "cron"
		case "gevent":
			return "gevent"
		}
		return "http"
	}
	isBin := func(a string) bool {
		switch filepath.Base(a) {
		case "odoo", "odoo-bin", "odoo.py", "openerp-server":
			return true
		}
		return false
	}
	at := -1
	switch {
	case isBin(argv[0]):
		at = 0
	case len(argv) > 1 && strings.HasPrefix(filepath.Base(argv[0]), "python") && isBin(argv[1]):
		at = 1 // script lancé par son interpréteur
	default:
		return ""
	}
	for _, a := range argv[at+1:] {
		if a == "gevent" {
			return "gevent"
		}
	}
	// Les commandes d'administration (shell, scaffold, db…) ne sont pas le serveur.
	if len(argv) > at+1 && !strings.HasPrefix(argv[at+1], "-") && argv[at+1] != "server" {
		return ""
	}
	return "main"
}

// scanOdoo : processus Odoo de /proc et leur mémoire résidente.
func scanOdoo(proc string) ([]odooProc, error) {
	entries, err := os.ReadDir(proc)
	if err != nil {
		return nil, err
	}
	page := float64(os.Getpagesize())
	var out []odooProc
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(proc, e.Name(), "cmdline"))
		if err != nil || len(raw) == 0 || !bytes.Contains(raw, []byte("odoo")) && !bytes.Contains(raw, []byte("openerp")) {
			continue
		}
		role := odooRole(strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00"))
		if role == "" {
			continue
		}
		p := odooProc{pid: pid, role: role}
		if stat, err := os.ReadFile(filepath.Join(proc, e.Name(), "stat")); err == nil {
			// pid (comm) state ppid … : après la dernière parenthèse.
			if i := bytes.LastIndexByte(stat, ')'); i > 0 {
				if f := strings.Fields(string(stat[i+1:])); len(f) > 1 {
					p.ppid, _ = strconv.Atoi(f[1])
				}
			}
		}
		if st, err := os.ReadFile(filepath.Join(proc, e.Name(), "statm")); err == nil {
			if f := strings.Fields(string(st)); len(f) > 1 {
				if v, err := strconv.ParseFloat(f[1], 64); err == nil {
					p.rss = v * page
				}
			}
		}
		out = append(out, p)
	}
	// Sans setproctitle (image officielle), un worker garde la ligne de commande du maître : c'est
	// alors un enfant d'un autre processus Odoo.
	mains := map[int]bool{}
	for _, p := range out {
		if p.role == "main" {
			mains[p.pid] = true
		}
	}
	for i := range out {
		if out[i].role == "main" && mains[out[i].ppid] {
			out[i].role = "worker"
		}
	}
	return out, nil
}
