package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── WSUS (vague 6) : Windows Server Update Services, en lecture seule ───────────────────────
//
// Branché sous Windows (vague6_windows.go) ; COUPÉ par défaut (integrations.wsus.enabled: true
// pour l'activer). Toutes les 60 s : services du rôle (WsusService, IIS, base interne WID). Toutes
// les 5 min, chaque minute tant qu'un défaut est vu, par PowerShell (API d'administration de WSUS,
// assemblage Microsoft.UpdateServices.Administration) : dernière synchronisation et son résultat,
// ordinateurs (en défaut, à mettre à jour, sans rapport depuis stale_days), mises à jour en attente
// d'approbation (dont critiques et de sécurité), fichiers à télécharger, base (WID : taille de
// SUSDB), place libre du dossier de contenu, pool d'applications WsusPool. Rien n'est approuvé,
// refusé, synchronisé ni nettoyé par la sonde.

var wsusServices = []string{"WsusService", "W3SVC", `MSSQL$MICROSOFT##WID`}

// wsusScript : __STALE__ est remplacé par stale_days.
const wsusScript = `$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [Text.Encoding]::UTF8
$WarningPreference = 'SilentlyContinue'
$ProgressPreference = 'SilentlyContinue'
$r = [ordered]@{ errors = @() }
try {
  [void][Reflection.Assembly]::LoadWithPartialName('Microsoft.UpdateServices.Administration')
  $w = [Microsoft.UpdateServices.Administration.AdminProxy]::GetUpdateServer()
  $r.version = [string]$w.Version
  $s = $w.GetStatus()
  $r.status = [ordered]@{ computers = $s.ComputerTargetCount; computers_needing = $s.ComputerTargetsNeedingUpdatesCount
    computers_errors = $s.ComputerTargetsWithUpdateErrorsCount; not_approved = $s.NotApprovedUpdateCount
    critical_not_approved = $s.CriticalOrSecurityUpdatesNotApprovedForInstallCount; needing_files = $s.UpdatesNeedingFilesCount
    approved = $s.ApprovedUpdateCount; declined = $s.DeclinedUpdateCount; expired = $s.ExpiredUpdateCount; total = $s.UpdateCount
    client_errors = $s.UpdatesWithClientErrorsCount; server_errors = $s.UpdatesWithServerErrorsCount }
  $now = [DateTime]::UtcNow
  try {
    $sub = $w.GetSubscription()
    $r.sync_status = [string]$sub.GetSynchronizationStatus()
    $r.sync_auto = [bool]$sub.SynchronizeAutomatically
    # LastSynchronizationTime date la dernière synchronisation, réussie ou non : l'historique dit la dernière réussie.
    $ok = @($sub.GetSynchronizationHistory() | Where-Object { [string]$_.Result -eq 'Succeeded' } | Sort-Object EndTime -Descending | Select-Object -First 1)
    if ($ok.Count -gt 0) { $r.success_age_s = [int64]($now - $ok[0].EndTime.ToUniversalTime()).TotalSeconds } else { $r.success_age_s = -1 }
    $l = $sub.GetLastSynchronizationInfo()
    if ($l -and $l.StartTime.Year -gt 2000) {
      $r.last_sync = [ordered]@{ result = [string]$l.Result; error = [string]$l.Error; error_text = [string]$l.ErrorText
        age_s = [int64]($now - $l.EndTime.ToUniversalTime()).TotalSeconds; duration_s = [int64]($l.EndTime - $l.StartTime).TotalSeconds }
    }
  } catch { $r.errors += 'synchronisation : ' + $_.Exception.Message }
  try {
    $sc = New-Object Microsoft.UpdateServices.Administration.ComputerTargetScope
    $sc.ToLastReportedStatusTime = $now.AddDays(-__STALE__)
    $r.stale = $w.GetComputerTargetCount($sc)
  } catch { $r.errors += 'ordinateurs : ' + $_.Exception.Message }
  try {
    $db = $w.GetDatabaseConfiguration()
    $r.database = [ordered]@{ wid = [bool]$db.IsUsingWindowsInternalDatabase; server = [string]$db.ServerName; name = [string]$db.DatabaseName }
    if ($db.IsUsingWindowsInternalDatabase) {
      $f = Join-Path $env:windir 'WID\Data\SUSDB.mdf'
      if (Test-Path $f) { $r.database.size = (Get-Item $f).Length }
    }
  } catch { $r.errors += 'base : ' + $_.Exception.Message }
  try {
    $p = [string]$w.GetConfiguration().LocalContentCachePath
    $r.content_path = $p
    if ($p -match '^[A-Za-z]:') { $r.content_free = ([IO.DriveInfo]::new($p.Substring(0, 2))).AvailableFreeSpace }
  } catch { $r.errors += 'contenu : ' + $_.Exception.Message }
} catch { $r.errors += 'api : ' + $_.Exception.Message }
try { Import-Module WebAdministration; $r.pool = [string](Get-WebAppPoolState -Name WsusPool).Value } catch {}
ConvertTo-Json -InputObject $r -Depth 4 -Compress`

func wsusScriptFor(staleDays int) string {
	return strings.ReplaceAll(wsusScript, "__STALE__", strconv.Itoa(staleDays))
}

type wsusSync struct {
	Result    string  `json:"result"`
	Error     string  `json:"error"`
	ErrorText string  `json:"error_text"`
	Age       float64 `json:"age_s"`
	Duration  float64 `json:"duration_s"`
}

type wsusInfo struct {
	Version    string             `json:"version"`
	Status     map[string]float64 `json:"status"`
	SyncStatus string             `json:"sync_status"`
	SyncAuto   bool               `json:"sync_auto"`
	SuccessAge *float64           `json:"success_age_s"`
	LastSync   *wsusSync          `json:"last_sync"`
	Stale      *float64           `json:"stale"`
	Database   *struct {
		WID    bool     `json:"wid"`
		Server string   `json:"server"`
		Name   string   `json:"name"`
		Size   *float64 `json:"size"`
	} `json:"database"`
	ContentPath string   `json:"content_path"`
	ContentFree *float64 `json:"content_free"`
	Pool        string   `json:"pool"`
	Errors      []string `json:"errors"`
}

// wsusStatusMetrics : champs de GetStatus() → métriques.
var wsusStatusMetrics = []struct{ key, metric string }{
	{"computers", "wsus.computers"}, {"computers_needing", "wsus.computers.needing_updates"},
	{"computers_errors", "wsus.computers.with_errors"}, {"not_approved", "wsus.updates.not_approved"},
	{"critical_not_approved", "wsus.updates.critical_not_approved"}, {"needing_files", "wsus.updates.needing_files"},
	{"approved", "wsus.updates.approved"}, {"declined", "wsus.updates.declined"}, {"expired", "wsus.updates.expired"},
	{"total", "wsus.updates.total"}, {"client_errors", "wsus.updates.with_client_errors"},
	{"server_errors", "wsus.updates.with_server_errors"},
}

type wsus struct {
	cfg  config.WSUSConfig
	det  detector
	d    winRoleDeps
	info cache[wsusInfo]
}

func newWSUS(cfg config.WSUSConfig, d winRoleDeps) *wsus {
	return &wsus{cfg: cfg, det: detector{mode: cfg.Enabled}, d: d}
}

func (w *wsus) name() string          { return "wsus" }
func (w *wsus) period() time.Duration { return 60 * time.Second }
func (w *wsus) close()                {}

// wsusHealthy : relevé sans défaut (sinon relu chaque minute pour voir le rétablissement).
func (w *wsus) healthy(r wsusInfo) bool {
	if len(r.Errors) > 0 || (r.Pool != "" && r.Pool != "Started") {
		return false
	}
	if r.LastSync != nil && r.LastSync.Result != "Succeeded" && r.SyncStatus != "Running" {
		return false
	}
	return true
}

func (w *wsus) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := w.det.skip(now); skip {
		return nil, st
	}
	if !w.d.present() {
		if w.cfg.Enabled == "true" {
			return nil, "erreur : rôle WSUS absent (service WsusService)"
		}
		_, st := w.det.failed("rôle WSUS absent")
		return nil, st
	}
	w.det.ok()
	ts := model.Ms(now)
	var checks []model.Check
	svc := w.d.services(wsusServices)
	up := svc["WsusService"].State == "running"
	for _, name := range wsusServices {
		s, ok := svc[name]
		if !ok || s.Disabled {
			continue
		}
		st, msg := serviceCheck(name, s)
		checks = append(checks, model.Check{TS: ts, Name: "wsus.service", Status: st, Message: msg, Tags: tags("windows_service", name)})
	}
	refresh := 5 * time.Minute
	if !w.info.at.IsZero() && (w.info.err != nil || !w.healthy(w.info.value)) {
		refresh = time.Minute
	}
	info, err := w.info.get(now, refresh, func() (wsusInfo, error) {
		var r wsusInfo
		out, err := w.d.ps(ctx)
		if err == nil {
			err = json.Unmarshal(out, &r)
		}
		return r, err
	})
	apiErr := ""
	for _, e := range info.Errors {
		if strings.HasPrefix(e, "api") {
			apiErr = e
		}
	}
	switch {
	case !up:
		checks = append(checks, connCheck("wsus.can_connect", ts, false, "service WsusService arrêté : WSUS ne sert plus les clients", tags()))
	case err != nil:
		checks = append(checks, connCheck("wsus.can_connect", ts, false, model.Truncate("API d'administration illisible : "+err.Error(), 300), tags()))
	case apiErr != "":
		checks = append(checks, connCheck("wsus.can_connect", ts, false, model.Truncate("API d'administration en échec : "+apiErr, 300), tags()))
	}
	if err != nil {
		return checks, "partiel : " + err.Error()
	}
	if info.Pool != "" {
		st, msg := model.CheckOK, "pool d'applications WsusPool démarré"
		if info.Pool != "Started" {
			st, msg = model.CheckCrit, "pool d'applications WsusPool : "+info.Pool+" (les clients et la console ne joignent plus WSUS)"
		}
		checks = append(checks, model.Check{TS: ts, Name: "wsus.app_pool", Status: st, Message: msg, Tags: tags()})
	}
	if apiErr != "" {
		return checks, "partiel : " + apiErr
	}
	for _, m := range wsusStatusMetrics {
		if v, ok := info.Status[m.key]; ok {
			b.Gauge(m.metric, "count", v)
		}
	}
	if info.Stale != nil {
		b.Gauge("wsus.computers.stale", "count", *info.Stale)
	}
	if info.Database != nil && info.Database.Size != nil {
		b.Gauge("wsus.database.size", "byte", *info.Database.Size)
	}
	if info.ContentFree != nil {
		b.Gauge("wsus.content.free", "byte", *info.ContentFree)
	}
	// Synchronisation avec le serveur amont (Microsoft Update ou WSUS parent).
	maxAge := w.cfg.SyncMaxAge.D()
	st, msg := model.CheckOK, "synchronisation réussie"
	if info.SuccessAge != nil && *info.SuccessAge >= 0 {
		b.Gauge("wsus.sync.age", "second", *info.SuccessAge)
		msg += " il y a " + (time.Duration(*info.SuccessAge) * time.Second).Round(time.Minute).String()
	}
	if ls := info.LastSync; ls != nil {
		b.Gauge("wsus.sync.duration", "second", ls.Duration)
		failed := 0.0
		if ls.Result != "Succeeded" {
			failed = 1
		}
		b.Gauge("wsus.sync.failed", "", failed)
	}
	switch {
	case info.SyncStatus == "Running":
		st, msg = model.CheckOK, "synchronisation en cours"
	case info.LastSync != nil && info.LastSync.Result == "Failed":
		st, msg = model.CheckCrit, "dernière synchronisation en échec"
		if t := strings.TrimSpace(firstNonEmpty(info.LastSync.ErrorText, info.LastSync.Error)); t != "" {
			msg += " : " + model.Truncate(t, 250)
		}
	case info.SuccessAge == nil || *info.SuccessAge < 0:
		st, msg = model.CheckWarn, "aucune synchronisation réussie (première synchronisation jamais faite ?)"
	case time.Duration(*info.SuccessAge)*time.Second > maxAge:
		st, msg = model.CheckWarn, fmt.Sprintf("dernière synchronisation réussie il y a %s (plus de %s)",
			(time.Duration(*info.SuccessAge) * time.Second).Round(time.Hour), maxAge)
	case info.LastSync != nil && info.LastSync.Result == "Canceled":
		st, msg = model.CheckWarn, "dernière synchronisation annulée"
	}
	if !info.SyncAuto && st == model.CheckOK {
		msg += " (synchronisation manuelle seulement)"
	}
	checks = append(checks, model.Check{TS: ts, Name: "wsus.sync", Status: st, Message: msg, Tags: tags()})
	if up {
		m := "WSUS " + info.Version + " en service"
		if c, ok := info.Status["computers"]; ok {
			m += fmt.Sprintf(", %g ordinateur(s), %g mise(s) à jour en attente d'approbation", c, info.Status["not_approved"])
		}
		if info.Database != nil {
			if info.Database.WID {
				m += ", base interne (WID)"
			} else {
				m += ", base " + info.Database.Name + " sur " + info.Database.Server
			}
		}
		checks = append(checks, connCheck("wsus.can_connect", ts, true, model.Truncate(m, 300), tags()))
	}
	if len(info.Errors) > 0 {
		return checks, "partiel : " + strings.Join(info.Errors, " ; ")
	}
	return checks, "ok"
}
