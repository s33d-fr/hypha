package integrations

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/hostfs"
	"s33d.fr/hypha/agent/internal/model"
)

// ── Gunicorn : maîtres et workers lus dans /proc ────────────────────────────────────────────
//
// Un maître est un processus Gunicorn dont le parent n'en est pas un ; ses workers sont ses
// enfants. Nom de l'application : le titre « gunicorn: master [nom] » (setproctitle installé),
// sinon --name, sinon le module WSGI de la ligne de commande. Un worker dont le temps processeur
// a avancé depuis le passage précédent est « working », sinon « idle ». Les requêtes, durées et
// codes de réponse viennent du StatsD natif de Gunicorn (--statsd-host 127.0.0.1:8125), reçu
// par l'agent : gunicorn.requests, gunicorn.request.duration, gunicorn.request.status.<code>.

var gunicornTitleRe = regexp.MustCompile(`^gunicorn: (master|worker) \[(.+?)\]`)

// GunicornStatsD : métriques que Gunicorn envoie lui-même par StatsD (--statsd-host vers l'écoute
// StatsD de l'agent) ; citées par le tableau prêt et la documentation, jamais produites ici.
var GunicornStatsD = []string{"gunicorn.requests", "gunicorn.request.duration", "gunicorn.workers",
	"gunicorn.log.critical", "gunicorn.log.error", "gunicorn.log.warning", "gunicorn.log.exception"}

type gunicornProc struct {
	pid, ppid int
	app       string
	ticks     float64
	title     bool // titre posé par setproctitle
}

type Gunicorn struct {
	cfg   config.GunicornConfig
	det   detector
	ticks map[int]float64
	seen  map[string]bool
}

func newGunicorn(cfg config.GunicornConfig) *Gunicorn {
	return &Gunicorn{cfg: cfg, det: detector{mode: intent(cfg.Enabled, len(cfg.Apps) > 0)}, seen: map[string]bool{}}
}

func (g *Gunicorn) name() string          { return "gunicorn" }
func (g *Gunicorn) period() time.Duration { return 30 * time.Second }
func (g *Gunicorn) close()                {}

// gunicornArgs : nom d'application d'une ligne de commande « … gunicorn [options] module:app ».
func gunicornArgs(argv []string) (string, bool) {
	isPython := func(a string) bool { return strings.HasPrefix(filepath.Base(a), "python") }
	at := -1
	switch {
	case len(argv) > 0 && filepath.Base(argv[0]) == "gunicorn":
		at = 0
	case len(argv) > 1 && isPython(argv[0]) && filepath.Base(argv[1]) == "gunicorn":
		at = 1 // script lancé par son interpréteur (#!/usr/bin/python3)
	case len(argv) > 2 && isPython(argv[0]) && argv[1] == "-m" && argv[2] == "gunicorn":
		at = 2
	default:
		return "", false
	}
	app := ""
	for i := at + 1; i < len(argv); i++ {
		a := argv[i]
		switch {
		case a == "-n" || a == "--name":
			if i+1 < len(argv) {
				return argv[i+1], true
			}
		case strings.HasPrefix(a, "--name="):
			return strings.TrimPrefix(a, "--name="), true
		case strings.HasPrefix(a, "-"):
			// options à valeur séparée : la valeur n'est pas le module
			if !strings.Contains(a, "=") && gunicornValueOpt[a] {
				i++
			}
		case app == "":
			app = a
		}
	}
	if app == "" {
		app = "gunicorn"
	}
	return app, true
}

var gunicornValueOpt = map[string]bool{
	"-b": true, "--bind": true, "-w": true, "--workers": true, "-k": true, "--worker-class": true, "-c": true,
	"--config": true, "-t": true, "--timeout": true, "--threads": true, "--statsd-host": true, "--statsd-prefix": true,
	"--log-level": true, "--access-logfile": true, "--error-logfile": true, "-u": true, "--user": true, "-g": true,
	"--group": true, "--chdir": true, "--pid": true, "-p": true, "--max-requests": true, "--max-requests-jitter": true,
	"--keep-alive": true, "--graceful-timeout": true, "--backlog": true, "--worker-connections": true, "-e": true, "--env": true,
	"--forwarded-allow-ips": true, "--log-config": true, "--pythonpath": true, "--worker-tmp-dir": true, "--umask": true,
	"--access-logformat": true, "--certfile": true, "--keyfile": true,
}

// scanGunicorn lit /proc : processus Gunicorn (titre ou ligne de commande).
func scanGunicorn(proc string) ([]gunicornProc, error) {
	entries, err := os.ReadDir(proc)
	if err != nil {
		return nil, err
	}
	var out []gunicornProc
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(proc, e.Name(), "cmdline"))
		if err != nil || len(raw) == 0 {
			continue
		}
		var gp gunicornProc
		title := strings.TrimSpace(string(bytes.ReplaceAll(bytes.TrimRight(raw, "\x00"), []byte{0}, []byte{' '})))
		if m := gunicornTitleRe.FindStringSubmatch(title); m != nil {
			gp = gunicornProc{pid: pid, app: m[2], title: true}
		} else if !bytes.Contains(raw, []byte("gunicorn")) {
			continue
		} else if app, ok := gunicornArgs(strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")); ok {
			gp = gunicornProc{pid: pid, app: app}
		} else {
			continue
		}
		stat, err := os.ReadFile(filepath.Join(proc, e.Name(), "stat"))
		if err != nil {
			continue
		}
		// pid (comm) state ppid … utime(14) stime(15) : après la dernière parenthèse.
		s := string(stat)
		i := strings.LastIndexByte(s, ')')
		if i < 0 {
			continue
		}
		f := strings.Fields(s[i+1:])
		if len(f) < 13 {
			continue
		}
		gp.ppid, _ = strconv.Atoi(f[1])
		ut, _ := strconv.ParseFloat(f[11], 64)
		st, _ := strconv.ParseFloat(f[12], 64)
		gp.ticks = ut + st
		out = append(out, gp)
	}
	return out, nil
}

func (g *Gunicorn) collect(_ context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := g.det.skip(now); skip {
		return nil, st
	}
	ts := model.Ms(now)
	procs, err := scanGunicorn(hostfs.Proc())
	if err != nil {
		if silent, st := g.det.failed(err.Error()); silent {
			return nil, st
		}
		return nil, "erreur : " + err.Error()
	}
	byPID := map[int]gunicornProc{}
	for _, p := range procs {
		byPID[p.pid] = p
	}
	type app struct {
		masters       []int
		working, idle int
	}
	apps := map[string]*app{}
	masterApp := map[int]string{}
	for _, p := range procs {
		if _, parentIsG := byPID[p.ppid]; !parentIsG {
			a := apps[p.app]
			if a == nil {
				a = &app{}
				apps[p.app] = a
			}
			a.masters = append(a.masters, p.pid)
			masterApp[p.pid] = p.app
		}
	}
	ticks := map[int]float64{}
	for _, p := range procs {
		name, ok := masterApp[p.ppid]
		if !ok {
			continue // maître, ou petit-enfant (processus lancé par un worker)
		}
		ticks[p.pid] = p.ticks
		if old, seen := g.ticks[p.pid]; seen && p.ticks > old {
			apps[name].working++
		} else {
			apps[name].idle++
		}
	}
	g.ticks = ticks
	want := map[string]bool{}
	for _, a := range g.cfg.Apps {
		want[a] = true
	}
	if len(apps) == 0 && len(want) == 0 && !g.det.seen {
		if silent, st := g.det.failed("aucun processus Gunicorn"); silent {
			return nil, st
		}
	}
	g.det.ok()
	for a := range apps {
		g.seen[a] = true
	}
	names := map[string]bool{}
	for a := range apps {
		names[a] = true
	}
	for a := range want {
		names[a] = true
	}
	for a := range g.seen {
		if len(want) == 0 {
			names[a] = true
		}
	}
	list := make([]string, 0, len(names))
	for a := range names {
		list = append(list, a)
	}
	sort.Strings(list)
	var checks []model.Check
	bad := 0
	for _, name := range list {
		kv := []string{"app", name}
		a := apps[name]
		if a == nil {
			bad++
			checks = append(checks, model.Check{TS: ts, Name: "gunicorn.is_running", Status: model.CheckCrit,
				Message: "maître Gunicorn « " + name + " » absent", Tags: tags(kv...)})
			continue
		}
		// gunicorn.workers est déjà pris par la jauge que Gunicorn envoie par StatsD (sans étiquette).
		b.Gauge("gunicorn.worker_processes", "process", float64(a.working), append(kv, "state", "working")...)
		b.Gauge("gunicorn.worker_processes", "process", float64(a.idle), append(kv, "state", "idle")...)
		b.Gauge("gunicorn.master_processes", "process", float64(len(a.masters)), kv...)
		st, msg := model.CheckOK, fmt.Sprintf("%d worker(s), dont %d occupé(s)", a.working+a.idle, a.working)
		if a.working+a.idle == 0 {
			st, msg = model.CheckCrit, "maître sans worker"
			bad++
		}
		checks = append(checks, model.Check{TS: ts, Name: "gunicorn.is_running", Status: st, Message: msg, Tags: tags(kv...)})
	}
	if bad > 0 {
		return checks, fmt.Sprintf("partiel : %d application(s) en défaut", bad)
	}
	return checks, "ok"
}
