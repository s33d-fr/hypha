// Package actions exécute les gestes à distance validés par un humain (docs/CONTRAT-04.md
// § 28) : redémarrer, démarrer ou arrêter un service, lancer un script nommé dans la conf,
// mettre l'agent à jour. Aucune commande libre : le serveur ne choisit qu'un geste de la
// liste blanche et sa cible ; le chemin d'un script vient toujours de la conf de l'agent.
//
// L'agent refuse (refused_by_agent, motif dans output) tout ce que sa conf n'admet pas,
// toute action expirée, et n'exécute jamais deux fois la même action.
package actions

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
	"s33d.fr/hypha/agent/internal/state"
)

// Statuts du compte rendu.
const (
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusRefused   = "refused_by_agent"
)

// MaxOutput : sortie rendue au serveur, 64 Kio au plus.
const MaxOutput = 64 << 10

// Action : une action reçue du canal de contrôle.
type Action struct {
	ID        int64          `json:"id"`
	Kind      string         `json:"kind"`
	Params    map[string]any `json:"params"`
	ExpiresAt int64          `json:"expires_at"`

	deadline time.Time // échéance ramenée à l'horloge locale
}

// Result : compte rendu (POST /api/v1/agent/actions/{id}/result).
type Result struct {
	Status   string `json:"status"`
	ExitCode *int   `json:"exit_code"`
	Output   string `json:"output"`
}

// Deps : ce que l'exécuteur demande au reste de l'agent.
type Deps struct {
	// Report envoie un compte rendu (avec ses reprises).
	Report func(ctx context.Context, id int64, r Result)
	// CheckService envoie aussitôt le contrôle de l'unité touchée.
	CheckService func(ctx context.Context, name string)
	// Update pose une version (nil : mise à jour impossible sur cet agent, motif dans UpdateOff).
	Update    func(ctx context.Context, version string) error
	UpdateOff string
	// Restart relance l'agent après une mise à jour posée.
	Restart func()
}

// Executor traite les actions une par une, dans l'ordre reçu.
type Executor struct {
	cfg   config.ActionsConfig
	store *state.Store
	log   *slog.Logger
	d     Deps
	queue chan Action

	mu   sync.Mutex
	seen []int64
}

const seenFile = "actions_seen.json"

// New prépare l'exécuteur ; les identifiants déjà traités sont relus du répertoire d'état.
func New(cfg config.ActionsConfig, store *state.Store, log *slog.Logger, d Deps) *Executor {
	e := &Executor{cfg: cfg, store: store, log: log, d: d, queue: make(chan Action, 32)}
	_ = store.ReadJSON(seenFile, &e.seen)
	return e
}

// Capabilities rend les gestes que l'agent accepte (host.capabilities.actions).
func (e *Executor) Capabilities() model.ActionCapabilities {
	c := model.ActionCapabilities{Enabled: e.cfg.Enabled, Kinds: []string{}, Scripts: []string{}, Services: []string{}, DenyServices: []string{}}
	if !e.cfg.Enabled {
		return c
	}
	for _, k := range e.cfg.Allow {
		if k == "update_agent" && e.d.Update == nil {
			continue
		}
		if !slices.Contains(c.Kinds, k) {
			c.Kinds = append(c.Kinds, k)
		}
	}
	if slices.Contains(c.Kinds, "run_script") {
		c.Scripts = config.SortedScriptNames(e.cfg.Scripts)
	}
	if slices.ContainsFunc(c.Kinds, func(k string) bool { return strings.HasSuffix(k, "_service") }) {
		c.Services = append(c.Services, e.cfg.Services...)
		c.DenyServices = append(append(c.DenyServices, selfServices...), e.cfg.DenyServices...)
	}
	return c
}

// Submit range les actions reçues ; serverTime (ms) corrige l'écart d'horloge pour
// l'échéance. Une action déjà vue est ignorée.
func (e *Executor) Submit(list []Action, serverTime int64) {
	skew := time.Duration(0)
	if serverTime > 0 {
		skew = time.Until(time.UnixMilli(serverTime)) // serveur − local
	}
	for _, a := range list {
		if a.ID <= 0 || e.markSeen(a.ID) {
			continue
		}
		a.deadline = time.UnixMilli(a.ExpiresAt).Add(-skew)
		select {
		case e.queue <- a:
		default:
			e.log.Warn("file d'actions pleine : action refusée", "id", a.ID)
			go e.d.Report(context.Background(), a.ID, refused("file d'actions de l'agent pleine"))
		}
	}
}

// markSeen note un identifiant ; rend vrai s'il était déjà connu. Les 500 derniers sont
// gardés sur disque : une action relivrée après une relance n'est pas rejouée.
func (e *Executor) markSeen(id int64) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if slices.Contains(e.seen, id) {
		return true
	}
	e.seen = append(e.seen, id)
	if len(e.seen) > 500 {
		e.seen = e.seen[len(e.seen)-500:]
	}
	if err := e.store.WriteJSON(seenFile, e.seen); err != nil {
		e.log.Warn("actions vues non écrites", "err", err)
	}
	return false
}

// Run traite la file jusqu'à l'arrêt.
func (e *Executor) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case a := <-e.queue:
			e.handle(ctx, a)
		}
	}
}

func refused(why string) Result { return Result{Status: StatusRefused, Output: why} }

func (e *Executor) handle(ctx context.Context, a Action) {
	defer func() {
		if p := recover(); p != nil {
			e.log.Error("action en panique", "id", a.ID, "err", p)
			e.d.Report(ctx, a.ID, Result{Status: StatusFailed, Output: fmt.Sprintf("erreur interne de l'agent : %v", p)})
		}
	}()
	plan, why := e.decide(a, time.Now())
	if why != "" {
		e.log.Warn("action refusée", "id", a.ID, "geste", a.Kind, "motif", why)
		e.d.Report(ctx, a.ID, refused(why))
		return
	}
	e.log.Info("action exécutée", "id", a.ID, "geste", a.Kind, "cible", plan.target)
	e.d.Report(ctx, a.ID, Result{Status: StatusRunning})
	actx, cancel := context.WithTimeout(ctx, e.cfg.Timeout.D())
	defer cancel()

	var res Result
	switch a.Kind {
	case "restart_service", "start_service", "stop_service":
		code, out := serviceAction(actx, strings.TrimSuffix(a.Kind, "_service"), plan.target)
		res = finish(code, out)
		if e.d.CheckService != nil {
			e.d.CheckService(ctx, plan.target)
		}
	case "run_script":
		code, out := runScript(actx, plan.target)
		res = finish(code, out)
	case "update_agent":
		if err := e.d.Update(actx, plan.target); err != nil {
			res = Result{Status: StatusFailed, Output: err.Error()}
		} else {
			zero := 0
			res = Result{Status: StatusSucceeded, ExitCode: &zero, Output: "version " + plan.target + " posée, relance de l'agent"}
			e.d.Report(ctx, a.ID, res)
			if e.d.Restart != nil {
				e.d.Restart()
			}
			return
		}
	}
	e.d.Report(ctx, a.ID, res)
}

func finish(code int, out string) Result {
	r := Result{Status: StatusSucceeded, Output: bound(out)}
	if code < 0 {
		r.Status = StatusFailed
		return r
	}
	c := code
	r.ExitCode = &c
	if code != 0 {
		r.Status = StatusFailed
	}
	return r
}

// bound coupe une sortie à MaxOutput octets en le disant.
func bound(s string) string {
	if len(s) <= MaxOutput {
		return s
	}
	note := fmt.Sprintf("\n[sortie coupée : %d octets de plus]", len(s)-MaxOutput)
	return model.Truncate(s, MaxOutput-len(note)) + note
}

type plan struct{ target string }

var serviceNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9@:._\-$\\]{0,254}$`)

// selfServices : le service de l'agent n'est jamais touché par une action (l'agent ne
// pourrait plus rendre compte), quelle que soit la conf.
var selfServices = []string{"hypha-agent.service", "hypha-agent", "HyphaAgent"}

// decide applique la conf : rend la cible, ou le motif du refus.
func (e *Executor) decide(a Action, now time.Time) (plan, string) {
	switch {
	case !e.cfg.Enabled:
		return plan{}, "actions coupées sur cet agent (actions.enabled: false)"
	case !a.deadline.IsZero() && !now.Before(a.deadline):
		return plan{}, "action expirée avant son exécution"
	case !slices.Contains(config.ActionKinds, a.Kind):
		return plan{}, fmt.Sprintf("geste %q inconnu de l'agent", a.Kind)
	case !slices.Contains(e.cfg.Allow, a.Kind):
		return plan{}, fmt.Sprintf("geste %s hors de actions.allow (%s)", a.Kind, strings.Join(e.cfg.Allow, ", "))
	}
	switch a.Kind {
	case "restart_service", "start_service", "stop_service":
		name := param(a.Params, "unit")
		if name == "" {
			name = param(a.Params, "service")
		}
		if name == "" {
			return plan{}, "paramètre unit (ou service) manquant"
		}
		if !serviceNameRe.MatchString(name) {
			return plan{}, fmt.Sprintf("nom de service %q refusé", name)
		}
		if runtime.GOOS == "linux" && !strings.Contains(name, ".") {
			name += ".service"
		}
		if matchService(selfServices, name) {
			return plan{}, "le service de l'agent HYPHA n'est jamais touché par une action"
		}
		if matchService(e.cfg.DenyServices, name) {
			return plan{}, fmt.Sprintf("%s interdit par actions.deny_services", name)
		}
		if !matchService(e.cfg.Services, name) {
			return plan{}, fmt.Sprintf("%s hors de actions.services", name)
		}
		return plan{target: name}, ""
	case "run_script":
		n := param(a.Params, "script")
		p, ok := e.cfg.Scripts[n]
		if n == "" || !ok {
			return plan{}, fmt.Sprintf("script %q absent de actions.scripts", n)
		}
		if why := scriptUnsafe(p); why != "" {
			return plan{}, why
		}
		return plan{target: p}, ""
	case "update_agent":
		if e.d.Update == nil {
			why := e.d.UpdateOff
			if why == "" {
				why = "mise à jour impossible sur cet agent"
			}
			return plan{}, why
		}
		v := param(a.Params, "version")
		if v == "" {
			return plan{}, "paramètre version manquant"
		}
		return plan{target: v}, ""
	}
	return plan{}, "geste non pris en charge"
}

func param(p map[string]any, k string) string {
	switch v := p[k].(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

// matchService compare un nom de service aux motifs (glob) ; sous Linux le suffixe
// .service est facultatif dans le motif, sous Windows la casse est ignorée.
func matchService(patterns []string, name string) bool {
	short := strings.TrimSuffix(name, ".service")
	fold := runtime.GOOS == "windows"
	for _, p := range patterns {
		pp, n, s := p, name, short
		if fold {
			pp, n, s = strings.ToLower(p), strings.ToLower(name), strings.ToLower(short)
		}
		if ok, _ := path.Match(pp, n); ok {
			return true
		}
		if ok, _ := path.Match(pp, s); ok {
			return true
		}
	}
	return false
}
