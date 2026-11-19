package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── Hyper-V (§ 45) : Get-VM sur l'hôte Windows, en lecture seule ────────────────────────────
//
// Branché sous Windows seulement (platform_windows.go) ; la lecture du résultat est commune pour
// être testée partout. Contrôles par VM : état critique (stockage de la VM injoignable), pause,
// arrêt d'une VM à démarrage automatique « toujours », signal de vie du système invité perdu,
// santé de la réplication. Mesures : CPU, mémoire attribuée et demandée, points de contrôle et
// âge du plus ancien (un AVHDX oublié grossit sans fin).

const hypervScript = `$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [Text.Encoding]::UTF8
if (-not (Get-Service vmms -ErrorAction SilentlyContinue)) { '{"absent":true}'; exit 0 }
Import-Module Hyper-V
$now = Get-Date
$vms = @(Get-VM | ForEach-Object {
  $cp = @(Get-VMSnapshot -VM $_ -ErrorAction SilentlyContinue | Sort-Object CreationTime)
  [pscustomobject]@{
    name = $_.Name; id = [string]$_.Id; state = [string]$_.State; cpu = $_.CPUUsage; vcpus = $_.ProcessorCount
    mem_assigned = $_.MemoryAssigned; mem_demand = $_.MemoryDemand; uptime = [int64]$_.Uptime.TotalSeconds
    heartbeat = [string]$_.Heartbeat; repl_state = [string]$_.ReplicationState; repl_health = [string]$_.ReplicationHealth
    start_action = [string]$_.AutomaticStartAction; checkpoints = $cp.Count
    oldest_checkpoint_s = $(if ($cp.Count) { [int64]($now - $cp[0].CreationTime).TotalSeconds } else { 0 })
  }
})
ConvertTo-Json -InputObject @{ vms = $vms } -Depth 4 -Compress`

type Hyperv struct {
	cfg config.IntegrationToggle
	det detector
	run func(ctx context.Context) ([]byte, error)
}

func newHyperv(cfg config.IntegrationToggle) *Hyperv {
	h := &Hyperv{cfg: cfg, det: detector{mode: cfg.Enabled}}
	h.run = func(ctx context.Context) ([]byte, error) {
		ps, ok := findTool("powershell")
		if !ok {
			return nil, fmt.Errorf("powershell introuvable")
		}
		return runTool(ctx, 45*time.Second, ps, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", hypervScript)
	}
	return h
}

func (h *Hyperv) name() string          { return "hyperv" }
func (h *Hyperv) period() time.Duration { return 60 * time.Second }
func (h *Hyperv) close()                {}

type hvVM struct {
	Name        string  `json:"name"`
	ID          string  `json:"id"`
	State       string  `json:"state"`
	CPU         float64 `json:"cpu"`
	VCPUs       float64 `json:"vcpus"`
	MemAssigned float64 `json:"mem_assigned"`
	MemDemand   float64 `json:"mem_demand"`
	Uptime      float64 `json:"uptime"`
	Heartbeat   string  `json:"heartbeat"`
	ReplState   string  `json:"repl_state"`
	ReplHealth  string  `json:"repl_health"`
	StartAction string  `json:"start_action"`
	Checkpoints float64 `json:"checkpoints"`
	OldestCP    float64 `json:"oldest_checkpoint_s"`
}

// hypervVMState : contrôle d'une VM selon son état, son démarrage automatique et son signal de vie.
func hypervVMState(v hvVM) (int, string) {
	switch {
	case strings.HasSuffix(v.State, "Critical"):
		return model.CheckCrit, fmt.Sprintf("état critique (%s) : stockage de la VM injoignable ou plein", v.State)
	case v.State == "Paused":
		return model.CheckWarn, "en pause"
	case (v.State == "Off" || v.State == "Saved") && v.StartAction == "Start":
		return model.CheckCrit, "arrêtée alors que son démarrage automatique est « toujours »"
	case v.State == "Off":
		return model.CheckOK, "arrêtée"
	case v.State == "Saved":
		return model.CheckOK, "état enregistré"
	case v.State == "Running" && (v.Heartbeat == "LostCommunication" || v.Heartbeat == "Error"):
		return model.CheckWarn, "en marche, mais le système invité ne répond plus (signal de vie perdu)"
	case v.State == "Running":
		return model.CheckOK, "en marche"
	}
	return model.CheckOK, strings.ToLower(v.State)
}

func (h *Hyperv) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := h.det.skip(now); skip {
		return nil, st
	}
	ts := model.Ms(now)
	out, err := h.run(ctx)
	var r struct {
		Absent bool   `json:"absent"`
		VMs    []hvVM `json:"vms"`
	}
	if err == nil {
		err = json.Unmarshal(out, &r)
	}
	if err == nil && r.Absent {
		if h.cfg.Enabled == "true" {
			return nil, "erreur : rôle Hyper-V absent (service vmms)"
		}
		return nil, "absent"
	}
	if err != nil {
		if silent, st := h.det.failed(err.Error()); silent {
			return nil, st
		}
		return []model.Check{canConnect("hyperv", ts, false, model.Truncate(err.Error(), 300), tags())}, "erreur : " + err.Error()
	}
	h.det.ok()
	sort.Slice(r.VMs, func(i, j int) bool { return r.VMs[i].Name < r.VMs[j].Name })
	var checks []model.Check
	running := 0
	for _, v := range r.VMs {
		kv := []string{"hyperv_vm", v.Name}
		st, msg := hypervVMState(v)
		if v.State == "Running" {
			running++
			b.Gauge("hyperv.vm.cpu.usage", "percent", v.CPU, kv...)
			b.Gauge("hyperv.vm.uptime", "second", v.Uptime, kv...)
		}
		if v.VCPUs > 0 {
			b.Gauge("hyperv.vm.vcpus", "core", v.VCPUs, kv...)
		}
		b.Gauge("hyperv.vm.mem.assigned", "byte", v.MemAssigned, kv...)
		b.Gauge("hyperv.vm.mem.demand", "byte", v.MemDemand, kv...)
		b.Gauge("hyperv.vm.checkpoints", "count", v.Checkpoints, kv...)
		if v.Checkpoints > 0 {
			b.Gauge("hyperv.vm.checkpoint.age", "day", round1(v.OldestCP/86400), kv...)
			msg += fmt.Sprintf(" ; %g point(s) de contrôle, le plus ancien de %s", v.Checkpoints, (time.Duration(v.OldestCP) * time.Second).Round(time.Hour))
		}
		checks = append(checks, model.Check{TS: ts, Name: "hyperv.vm", Status: st, Message: v.Name + " " + msg, Tags: tags(kv...)})
		if v.ReplState != "" && v.ReplState != "Disabled" {
			rst, rmsg := model.CheckOK, "réplication saine ("+v.ReplState+")"
			switch v.ReplHealth {
			case "Critical":
				rst, rmsg = model.CheckCrit, "réplication en état critique ("+v.ReplState+")"
			case "Warning":
				rst, rmsg = model.CheckWarn, "réplication en avertissement ("+v.ReplState+")"
			}
			checks = append(checks, model.Check{TS: ts, Name: "hyperv.vm.replication", Status: rst, Message: v.Name + " : " + rmsg, Tags: tags(kv...)})
		}
	}
	b.Gauge("hyperv.vms.total", "count", float64(len(r.VMs)))
	b.Gauge("hyperv.vms.running", "count", float64(running))
	checks = append(checks, canConnect("hyperv", ts, true, fmt.Sprintf("Hyper-V : %d VM, %d en marche", len(r.VMs), running), tags()))
	return checks, "ok"
}
