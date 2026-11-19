package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"time"

	"s33d.fr/hypha/agent/internal/actions"
	"s33d.fr/hypha/agent/internal/cmdb"
	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
	"s33d.fr/hypha/agent/internal/sender"
	"s33d.fr/hypha/agent/internal/snmp"
	"s33d.fr/hypha/agent/internal/synthetics"
	"s33d.fr/hypha/agent/internal/update"
	"s33d.fr/hypha/agent/internal/winsvc"
)

// ErrRestart : l'agent s'est arrêté pour laisser place à un nouveau binaire (§ 27).
var ErrRestart = errors.New("relance après mise à jour")

// controlResponse : réponse de GET /api/v1/agent/control (§ 27).
type controlResponse struct {
	ServerTime    int64              `json:"server_time"`
	TargetVersion *string            `json:"target_version"`
	Actions       []actions.Action   `json:"actions"`
	Synthetics    []synthetics.Test  `json:"synthetics"`
}

// updateOffReason : motif qui interdit la mise à jour automatique ici (vide sinon).
func updateOffReason(opt Options) string {
	switch {
	case opt.DryRun:
		return "mise à jour impossible en --dry-run"
	case os.Getenv("HOST_PROC") != "" || fileExists("/.dockerenv"):
		return "agent en conteneur : mettre à jour l'image (registre-vm.s33d.fr/hypha/agent)"
	case runtime.GOOS == "windows" && !winsvc.IsService():
		return "agent lancé hors du service Windows : mise à jour par le service seulement"
	}
	if _, err := update.AssetName(runtime.GOOS, runtime.GOARCH); err != nil {
		return err.Error()
	}
	if _, err := update.EmbeddedPublicKey(); err != nil {
		return err.Error()
	}
	return ""
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// setupV04 prépare les composants de la 0.4.0 : mise à jour signée, actions, synthétiques,
// SNMP, inventaire.
func (a *Agent) setupV04() error {
	cfg := a.cfg
	a.updateOff = updateOffReason(a.opt)
	tr, err := sender.NewTransport(cfg.Proxy, cfg.TLSSkipVerify)
	if err != nil {
		return err
	}
	a.updater = update.New(update.Options{
		BaseURL: cfg.URL, Transport: tr, StateDir: config.StateDir(), Version: a.opt.Version,
		ConfigPath: cfg.Path, Enabled: cfg.AutoUpdate, Disabled: a.updateOff, Log: a.log,
		OnStatus: a.onUpdateStatus,
	})
	deps := actions.Deps{Report: a.reportAction, CheckService: a.checkServiceNow, Restart: a.requestRestart, UpdateOff: a.updateOff}
	if a.updateOff == "" {
		deps.Update = a.updater.Prepare
	}
	a.actions = actions.New(cfg.Actions, a.store, a.log, deps)
	switch {
	case !cfg.Actions.Enabled:
		a.status.Set("actions", "disabled")
	default:
		a.status.Set("actions", "ok")
	}
	if cfg.Synthetics.Enabled && cfg.Control.Enabled {
		a.synth = synthetics.New(a.hostname, cfg.Synthetics.MaxConcurrent, func(r []model.SyntheticResult) {
			a.sender.Post(sender.RouteSynthetics, map[string]any{"results": r}, nil)
		}, func(s string) { a.status.Set("synthetics", s) }, a.log)
		a.status.Set("synthetics", "ok")
	} else {
		a.status.Set("synthetics", "disabled")
	}
	if cfg.SNMP.Enabled && (len(cfg.SNMP.Devices) > 0 || cfg.SNMP.Traps.Enabled) {
		a.snmp = snmp.New(cfg.SNMP, snmp.Options{
			AgentID: a.agentID, AgentVersion: a.opt.Version, Hostname: a.hostname, Tags: cfg.Tags,
			Store: a.store, Log: a.log,
			Post:      func(route string, fields map[string]any) { a.sender.Post(route, fields, nil) },
			SetStatus: func(s string) { a.status.Set("snmp", s) },
		})
		a.status.Set("snmp", "ok")
	} else {
		a.status.Set("snmp", "disabled")
	}
	if cfg.Inventory.Enabled {
		a.inventory = cmdb.New(cfg.Inventory, a.store, a.updates, func(fields map[string]any) {
			a.sender.Post(sender.RouteInventory, fields, nil)
		}, func(s string) { a.status.Set("inventory", s) }, a.log)
		a.status.Set("inventory", "ok")
	} else {
		a.status.Set("inventory", "disabled")
	}
	if !cfg.Control.Enabled {
		a.status.Set("control", "disabled")
	}
	return nil
}

// startV04 lance les boucles de la 0.4.0.
func (a *Agent) startV04(ctx context.Context, every func(string, time.Duration, time.Duration, func(context.Context, time.Time)), goRun func(func())) {
	if a.cfg.Control.Enabled && !a.opt.DryRun {
		every("control", 5*time.Second, a.cfg.Control.Interval.D(), a.controlCycle)
	}
	goRun(func() { a.actions.Run(ctx) })
	if a.synth != nil {
		goRun(func() { a.synth.Run(ctx) })
	}
	if a.snmp != nil {
		goRun(func() { a.snmp.Run(ctx) })
		if a.cfg.SNMP.Traps.Enabled {
			goRun(func() {
				a.status.Set("snmp_traps", "ok")
				if err := a.snmp.ListenTraps(ctx); err != nil {
					a.status.Set("snmp_traps", "erreur : "+err.Error())
					a.log.Warn("réception des traps SNMP impossible", "adresse", a.cfg.SNMP.Traps.Listen, "err", err)
				}
			})
		}
	}
	if a.inventory != nil {
		goRun(func() { a.inventory.Run(ctx) })
	}
}

// capabilities : ce que l'agent accepte de faire (host.capabilities, § 27).
func (a *Agent) capabilities() *model.Capabilities {
	c := &model.Capabilities{
		Actions:      a.actions.Capabilities(),
		Synthetics:   a.synth != nil,
		AutoUpdate:   a.cfg.AutoUpdate && a.updateOff == "" && a.cfg.Control.Enabled,
		Integrations: a.integr.Active(),
	}
	if !a.cfg.Control.Enabled {
		c.Actions.Enabled, c.Actions.Kinds, c.Actions.Scripts = false, []string{}, []string{}
	}
	if a.snmp != nil {
		c.SNMPDevices = a.snmp.Count()
	}
	return c
}

func (a *Agent) onUpdateStatus(model.UpdateStatus) {
	// L'état de mise à jour se suit en direct : inventaire envoyé aussitôt.
	go a.safe("host", func() { a.hostCycle(context.Background(), time.Now()) })
}

// requestRestart arrête l'agent proprement ; main relance le nouveau binaire.
func (a *Agent) requestRestart() {
	if a.restart.CompareAndSwap(false, true) {
		a.log.Info("arrêt pour relance sur la nouvelle version")
		if a.cancelRun != nil {
			a.cancelRun()
		}
	}
}

// controlCycle : GET /api/v1/agent/control ; actions, synthétiques et version cible (§ 27).
func (a *Agent) controlCycle(ctx context.Context, now time.Time) {
	if now.Before(a.controlPause) {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	code, body, err := a.sender.Do(cctx, http.MethodGet, sender.RouteControl, nil)
	switch {
	case err != nil:
		a.controlFail(now, "erreur : "+err.Error(), time.Duration(min(a.controlFails+1, 10))*30*time.Second)
		return
	case code == http.StatusNotFound:
		a.controlFail(now, "absent : serveur sans canal de contrôle (antérieur à la 0.4.0)", 10*time.Minute)
		return
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		a.controlFail(now, fmt.Sprintf("erreur : HTTP %d (clé refusée)", code), 5*time.Minute)
		return
	case code/100 != 2:
		a.controlFail(now, fmt.Sprintf("erreur : HTTP %d", code), time.Duration(min(a.controlFails+1, 10))*30*time.Second)
		return
	}
	var r controlResponse
	if err := json.Unmarshal(body, &r); err != nil {
		a.controlFail(now, "erreur : réponse illisible : "+err.Error(), time.Minute)
		return
	}
	a.controlFails = 0
	a.status.Set("control", "ok")
	a.status.Clear("control")
	if len(r.Actions) > 0 {
		a.actions.Submit(r.Actions, r.ServerTime)
	}
	if a.synth != nil {
		a.synth.Update(r.Synthetics)
	} else if len(r.Synthetics) > 0 && a.limiter.Allow("synthetics-off") {
		a.log.Warn("tests synthétiques reçus mais synthetics.enabled est faux : ignorés", "tests", len(r.Synthetics))
	}
	if r.TargetVersion != nil {
		a.updater.Want(ctx, *r.TargetVersion, a.requestRestart)
	}
}

func (a *Agent) controlFail(now time.Time, state string, pause time.Duration) {
	a.controlFails++
	a.controlPause = now.Add(pause)
	a.status.Set("control", state)
	a.collectorError("control", errors.New(state))
}

// reportAction envoie le compte rendu d'une action, avec trois essais ; un compte rendu final
// qui ne passe pas part par le tampon disque.
func (a *Agent) reportAction(ctx context.Context, id int64, r actions.Result) {
	body := func() map[string]any {
		return map[string]any{"status": r.Status, "exit_code": r.ExitCode, "output": r.Output}
	}
	route := sender.RouteActionResult(id)
	for _, wait := range []time.Duration{0, 2 * time.Second, 10 * time.Second} {
		if wait > 0 {
			t := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				t.Stop()
			case <-t.C:
			}
			if ctx.Err() != nil {
				break
			}
		}
		rctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		code, _, err := a.sender.Do(rctx, http.MethodPost, route, body())
		cancel()
		if err == nil && code/100 == 2 {
			return
		}
		if err == nil && code >= 400 && code < 500 && code != http.StatusTooManyRequests {
			a.log.Warn("compte rendu d'action refusé par le serveur", "id", id, "statut", code)
			return
		}
	}
	if r.Status != actions.StatusRunning {
		a.sender.Post(route, body(), nil)
	}
}

// checkServiceNow envoie aussitôt le contrôle de l'unité touchée par une action.
func (a *Agent) checkServiceNow(ctx context.Context, name string) {
	if cs := a.checks.CheckService(ctx, name, a.tags); len(cs) > 0 {
		a.sender.Post(sender.RouteChecks, map[string]any{"checks": cs}, nil)
	}
}
