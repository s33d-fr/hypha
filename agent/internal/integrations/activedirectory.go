package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── Active Directory (vague 3) : contrôleur de domaine, en lecture seule ─────────────────────
//
// Branché sous Windows (winprobes_windows.go) quand le service NTDS existe. Toutes les 30 s :
// compteurs NTDS (LDAP, lectures et écritures de l'annuaire, réplication entrante et sortante),
// authentifications Kerberos et NTLM, sémaphores Netlogon (goulot de NTLM), état des services du
// rôle. Toutes les 5 min, par PowerShell (module ActiveDirectory, servi par ADWS) : partenaires de
// réplication et leurs échecs, rôles FSMO tenus, partages SYSVOL et NETLOGON (sans eux le contrôleur
// ne sert plus les stratégies de groupe ni les scripts d'ouverture de session). Les journaux
// Directory Service et DFS Replication s'ajoutent à la collecte (logs : EventlogChannels).

type pdhMetric struct {
	path, metric, unit string
}

// adCounters : compteurs relevés (noms anglais, lus par PdhAddEnglishCounter quelle que soit la
// langue de Windows ; un compteur absent d'une version est ignoré).
var adCounters = []pdhMetric{
	{`\NTDS\LDAP Client Sessions`, "active_directory.ldap.client_sessions", "count"},
	{`\NTDS\LDAP Active Threads`, "active_directory.ldap.active_threads", "count"},
	{`\NTDS\LDAP Bind Time`, "active_directory.ldap.bind_time", "millisecond"},
	{`\NTDS\LDAP Successful Binds/sec`, "active_directory.ldap.binds", "operation_per_second"},
	{`\NTDS\LDAP Searches/sec`, "active_directory.ldap.searches", "operation_per_second"},
	{`\NTDS\LDAP Writes/sec`, "active_directory.ldap.writes", "operation_per_second"},
	{`\NTDS\LDAP New Connections/sec`, "active_directory.ldap.new_connections", "operation_per_second"},
	{`\NTDS\LDAP New SSL Connections/sec`, "active_directory.ldap.new_ssl_connections", "operation_per_second"},
	{`\NTDS\DS Directory Reads/sec`, "active_directory.ds.reads", "operation_per_second"},
	{`\NTDS\DS Directory Writes/sec`, "active_directory.ds.writes", "operation_per_second"},
	{`\NTDS\DS Directory Searches/sec`, "active_directory.ds.searches", "operation_per_second"},
	{`\NTDS\DS Threads in Use`, "active_directory.ds.threads_in_use", "count"},
	{`\NTDS\DS Notify Queue Size`, "active_directory.ds.notify_queue", "count"},
	{`\NTDS\DRA Inbound Bytes Total/sec`, "active_directory.dra.inbound.bytes", "byte_per_second"},
	{`\NTDS\DRA Outbound Bytes Total/sec`, "active_directory.dra.outbound.bytes", "byte_per_second"},
	{`\NTDS\DRA Inbound Objects/sec`, "active_directory.dra.inbound.objects", "operation_per_second"},
	{`\NTDS\DRA Outbound Objects/sec`, "active_directory.dra.outbound.objects", "operation_per_second"},
	{`\NTDS\DRA Pending Replication Synchronizations`, "active_directory.dra.pending_syncs", "count"},
	{`\NTDS\DRA Pending Replication Operations`, "active_directory.dra.pending_ops", "count"},
	{`\NTDS\ATQ Outstanding Queued Requests`, "active_directory.atq.queued_requests", "count"},
	{`\NTDS\ATQ Request Latency`, "active_directory.atq.request_latency", "millisecond"},
	{`\Security System-Wide Statistics\Kerberos Authentications`, "active_directory.kerberos.authentications", "operation_per_second"},
	{`\Security System-Wide Statistics\NTLM Authentications`, "active_directory.ntlm.authentications", "operation_per_second"},
	{`\Security System-Wide Statistics\KDC AS Requests`, "active_directory.kerberos.as_requests", "operation_per_second"},
	{`\Security System-Wide Statistics\KDC TGS Requests`, "active_directory.kerberos.tgs_requests", "operation_per_second"},
	{`\Netlogon(_Total)\Semaphore Waiters`, "active_directory.netlogon.semaphore_waiters", "count"},
	{`\Netlogon(_Total)\Semaphore Holders`, "active_directory.netlogon.semaphore_holders", "count"},
	{`\Netlogon(_Total)\Semaphore Timeouts`, "active_directory.netlogon.semaphore_timeouts", "count"},
	{`\Netlogon(_Total)\Average Semaphore Hold Time`, "active_directory.netlogon.semaphore_hold_time", "second"},
}

// adServices : services du rôle ; un service absent (version, rôle non installé) est ignoré, un
// service désactivé aussi (FRS remplacé par DFSR, par exemple).
var adServices = []string{"NTDS", "Netlogon", "Kdc", "ADWS", "DFSR", "IsmServ", "W32Time", "LanmanServer", "DNS"}

// svcState : état d'un service Windows (running, stopped…) et démarrage désactivé.
type svcState struct {
	State    string
	Disabled bool
}

// winRoleDeps : accès à Windows, remplacés dans les tests.
type winRoleDeps struct {
	present  func() bool // le rôle est installé (service présent)
	pdh      pdhReadFunc // compteurs
	closePDH func()      // requête PDH à rouvrir
	services func(names []string) map[string]svcState
	ps       func(ctx context.Context) ([]byte, error) // script PowerShell du rôle (JSON)
}

type adPartner struct {
	Partner    string  `json:"partner"`
	Partition  string  `json:"partition"`
	LastResult int64   `json:"last_result"`
	Failures   float64 `json:"failures"`
	SuccessAge float64 `json:"success_age_s"` // -1 : jamais réussie
}

type adInfo struct {
	Domain     string      `json:"domain"`
	Forest     string      `json:"forest"`
	DomainMode string      `json:"domain_mode"`
	ForestMode string      `json:"forest_mode"`
	Site       string      `json:"site"`
	Roles      []string    `json:"roles"`
	Partners   []adPartner `json:"partners"`
	Shares     []string    `json:"shares"`
	Errors     []string    `json:"errors"`
}

// adScript : rien d'écrit, tout en lecture ; une section en échec n'empêche pas les autres.
const adScript = `$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [Text.Encoding]::UTF8
$WarningPreference = 'SilentlyContinue'
$ProgressPreference = 'SilentlyContinue'
$r = [ordered]@{ roles = @(); partners = @(); shares = @(); errors = @() }
$me = $env:COMPUTERNAME
try { $s = @(Get-SmbShare -ErrorAction Stop | Where-Object { $_.Name -in 'SYSVOL','NETLOGON' } | ForEach-Object { $_.Name }); $r.shares = $s } catch { $r.errors += 'partages : ' + $_.Exception.Message }
try {
  Import-Module ActiveDirectory
  $dom = Get-ADDomain -Current LocalComputer; $for = Get-ADForest -Current LocalComputer
  $r.domain = [string]$dom.DNSRoot; $r.forest = [string]$for.Name
  $r.domain_mode = [string]$dom.DomainMode; $r.forest_mode = [string]$for.ForestMode
  try { $r.site = [string](Get-ADDomainController -Identity $me).Site } catch {}
  foreach ($p in @(@('PDCEmulator', $dom.PDCEmulator), @('RIDMaster', $dom.RIDMaster), @('InfrastructureMaster', $dom.InfrastructureMaster),
                   @('SchemaMaster', $for.SchemaMaster), @('DomainNamingMaster', $for.DomainNamingMaster))) {
    if ([string]$p[1] -like "$me.*" -or [string]$p[1] -eq $me) { $r.roles += $p[0] }
  }
  $now = Get-Date
  $r.partners = @(Get-ADReplicationPartnerMetadata -Target $me -Scope Server -PartnerType Inbound -ErrorAction SilentlyContinue | ForEach-Object {
    [ordered]@{ partner = [string]$_.Partner; partition = [string]$_.Partition; last_result = [int64]$_.LastReplicationResult
      failures = [int64]$_.ConsecutiveReplicationFailures
      success_age_s = $(if ($_.LastReplicationSuccess -and $_.LastReplicationSuccess.Year -gt 1601) { [int64]($now - $_.LastReplicationSuccess).TotalSeconds } else { -1 }) }
  })
} catch { $r.errors += 'annuaire : ' + $_.Exception.Message }
ConvertTo-Json -InputObject $r -Depth 5 -Compress`

type activeDirectory struct {
	cfg    config.ActiveDirectoryConfig
	det    detector
	d      winRoleDeps
	info   cache[adInfo]
	domain string // dernier domaine lu : étiquettes stables quand la lecture échoue
	ntdsUp *bool  // état de NTDS au relevé précédent
}

// adHealthy : relevé PowerShell sans défaut (sinon relu chaque minute pour voir le rétablissement).
func adHealthy(r adInfo) bool {
	if len(r.Errors) > 0 {
		return false
	}
	have := map[string]bool{}
	for _, s := range r.Shares {
		have[strings.ToUpper(s)] = true
	}
	if !have["SYSVOL"] || !have["NETLOGON"] {
		return false
	}
	for _, p := range r.Partners {
		if p.LastResult != 0 || p.SuccessAge < 0 {
			return false
		}
	}
	return true
}

// serviceCheck : en marche = OK ; démarrage ou arrêt en cours = avertissement (relance) ; sinon critique.
func serviceCheck(name string, s svcState) (int, string) {
	switch {
	case s.State == "running":
		return model.CheckOK, name + " en marche"
	case strings.HasPrefix(s.State, "en cours"):
		return model.CheckWarn, name + " : " + s.State
	}
	return model.CheckCrit, name + " : " + s.State
}

func newActiveDirectory(cfg config.ActiveDirectoryConfig, d winRoleDeps) *activeDirectory {
	return &activeDirectory{cfg: cfg, det: detector{mode: cfg.Enabled}, d: d}
}

func (a *activeDirectory) name() string          { return "active_directory" }
func (a *activeDirectory) period() time.Duration { return 30 * time.Second }
func (a *activeDirectory) close() {
	if a.d.closePDH != nil {
		a.d.closePDH()
	}
}

// partnerName : « CN=NTDS Settings,CN=DC2,CN=Servers,… » → DC2.
func partnerName(dn string) string {
	parts := strings.Split(dn, ",")
	if len(parts) > 1 && strings.EqualFold(strings.TrimSpace(parts[0]), "CN=NTDS Settings") {
		return strings.TrimPrefix(strings.TrimSpace(parts[1]), "CN=")
	}
	return dn
}

// partitionName : « DC=essai,DC=local » → essai.local ; « CN=Configuration,DC=… » → Configuration.
func partitionName(dn string) string {
	var dcs []string
	first := ""
	for i, p := range strings.Split(dn, ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(p), "=")
		switch {
		case strings.EqualFold(k, "DC"):
			dcs = append(dcs, v)
		case i == 0:
			first = v
		}
	}
	if first != "" {
		return first
	}
	if len(dcs) > 0 {
		return strings.Join(dcs, ".")
	}
	return dn
}

func (a *activeDirectory) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := a.det.skip(now); skip {
		return nil, st
	}
	if !a.d.present() {
		if a.cfg.Enabled == "true" {
			return nil, "erreur : rôle AD DS absent (service NTDS)"
		}
		_, st := a.det.failed("rôle AD DS absent")
		return nil, st
	}
	a.det.ok()
	ts := model.Ms(now)
	var checks []model.Check

	// Services du rôle.
	svc := a.d.services(adServices)
	ntdsUp := svc["NTDS"].State == "running"
	for _, name := range adServices {
		s, ok := svc[name]
		if !ok || s.Disabled {
			continue
		}
		st, msg := serviceCheck(name, s)
		checks = append(checks, model.Check{TS: ts, Name: "active_directory.service", Status: st, Message: msg, Tags: tags("windows_service", name)})
	}

	// Compteurs.
	paths := make([]string, len(adCounters))
	for i, c := range adCounters {
		paths[i] = c.path
	}
	vals, _, err := a.d.pdh(paths)
	if err != nil && !errors.Is(err, errNoData) {
		a.close()
	}
	for _, c := range adCounters {
		if v, ok := vals[c.path][""]; ok {
			b.Gauge(c.metric, c.unit, v)
		}
	}
	switch {
	case !ntdsUp:
		checks = append(checks, connCheck("active_directory.can_connect", ts, false, "service NTDS arrêté : l'annuaire ne répond plus", tags()))
	case err != nil && !errors.Is(err, errNoData):
		checks = append(checks, connCheck("active_directory.can_connect", ts, false, model.Truncate("compteurs NTDS illisibles : "+err.Error(), 300), tags()))
	}

	// Réplication, FSMO, partages : PowerShell toutes les 5 min, chaque minute tant qu'un défaut est vu,
	// et tout de suite quand NTDS s'arrête ou repart (partages retirés et republiés).
	refresh := 5 * time.Minute
	if !a.info.at.IsZero() && (a.info.err != nil || !adHealthy(a.info.value)) {
		refresh = time.Minute
	}
	if a.ntdsUp != nil && *a.ntdsUp != ntdsUp {
		a.info.at = time.Time{}
	}
	a.ntdsUp = &ntdsUp
	info, perr := a.info.get(now, refresh, func() (adInfo, error) {
		var r adInfo
		out, err := a.d.ps(ctx)
		if err == nil {
			err = json.Unmarshal(out, &r)
		}
		return r, err
	})
	if info.Domain != "" {
		a.domain = info.Domain
	}
	dom := []string{}
	if a.domain != "" {
		dom = []string{"ad_domain", a.domain}
	}
	if perr != nil {
		checks = append(checks, model.Check{TS: ts, Name: "active_directory.replication", Status: model.CheckUnknown,
			Message: model.Truncate("état de la réplication illisible : "+perr.Error(), 300), Tags: tags(dom...)})
		if ntdsUp && (err == nil || errors.Is(err, errNoData)) {
			checks = append(checks, connCheck("active_directory.can_connect", ts, true, "annuaire en marche (compteurs NTDS lus)", tags()))
		}
		return checks, "partiel : " + perr.Error()
	}
	dirErr := ""
	for _, e := range info.Errors {
		if strings.HasPrefix(e, "annuaire") {
			dirErr = e
		}
	}
	// Partages SYSVOL et NETLOGON.
	shareErr := false
	for _, e := range info.Errors {
		shareErr = shareErr || strings.HasPrefix(e, "partages")
	}
	if !shareErr {
		have := map[string]bool{}
		for _, s := range info.Shares {
			have[strings.ToUpper(s)] = true
		}
		var missing []string
		for _, s := range []string{"SYSVOL", "NETLOGON"} {
			if !have[s] {
				missing = append(missing, s)
			}
		}
		st, msg := model.CheckOK, "partages SYSVOL et NETLOGON publiés"
		if len(missing) > 0 {
			st, msg = model.CheckCrit, "partage(s) "+strings.Join(missing, " et ")+" absent(s) : stratégies de groupe et scripts d'ouverture de session non servis (réplication SYSVOL initiale non terminée ?)"
		}
		checks = append(checks, model.Check{TS: ts, Name: "active_directory.sysvol", Status: st, Message: msg, Tags: tags(dom...)})
	}
	// Partenaires de réplication.
	if dirErr != "" {
		checks = append(checks, model.Check{TS: ts, Name: "active_directory.replication", Status: model.CheckUnknown,
			Message: model.Truncate("lecture de l'annuaire impossible (ADWS arrêté ?) : "+dirErr, 300), Tags: tags(dom...)})
	} else {
		sort.Slice(info.Partners, func(i, j int) bool {
			return info.Partners[i].Partner+info.Partners[i].Partition < info.Partners[j].Partner+info.Partners[j].Partition
		})
		failures, maxAge := 0.0, 0.0
		for _, p := range info.Partners {
			kv := append(append([]string{}, dom...), "ad_partner", partnerName(p.Partner), "ad_partition", partitionName(p.Partition))
			failures += p.Failures
			st, msg := model.CheckOK, "réplication réussie"
			if p.SuccessAge >= 0 {
				b.Gauge("active_directory.replication.age", "second", p.SuccessAge, kv...)
				maxAge = max(maxAge, p.SuccessAge)
				msg += " il y a " + (time.Duration(p.SuccessAge) * time.Second).String()
			}
			switch {
			case p.SuccessAge < 0:
				st, msg = model.CheckCrit, fmt.Sprintf("jamais répliqué depuis ce partenaire (dernier résultat %d)", p.LastResult)
			case p.LastResult != 0 && p.SuccessAge > 86400:
				st, msg = model.CheckCrit, fmt.Sprintf("%g échec(s) de suite (code %d), dernière réussite il y a plus de 24 h", p.Failures, p.LastResult)
			case p.LastResult != 0:
				st, msg = model.CheckWarn, fmt.Sprintf("%g échec(s) de suite (code %d)", p.Failures, p.LastResult)
			}
			checks = append(checks, model.Check{TS: ts, Name: "active_directory.replication", Status: st,
				Message: partnerName(p.Partner) + " / " + partitionName(p.Partition) + " : " + msg, Tags: tags(kv...)})
		}
		b.Gauge("active_directory.replication.partners", "count", float64(len(info.Partners)), dom...)
		b.Gauge("active_directory.replication.failures", "count", failures, dom...)
		if len(info.Partners) == 0 {
			checks = append(checks, model.Check{TS: ts, Name: "active_directory.replication", Status: model.CheckOK,
				Message: "aucun partenaire de réplication entrante (contrôleur unique du domaine)", Tags: tags(dom...)})
		}
		b.Gauge("active_directory.fsmo_roles", "count", float64(len(info.Roles)), dom...)
	}
	if ntdsUp && (err == nil || errors.Is(err, errNoData)) {
		msg := "annuaire en marche"
		if info.Domain != "" {
			msg = fmt.Sprintf("contrôleur du domaine %s (forêt %s, niveau %s), site %s", info.Domain, info.Forest, info.DomainMode, info.Site)
		}
		if len(info.Roles) > 0 {
			msg += " ; rôles FSMO : " + strings.Join(info.Roles, ", ")
		}
		checks = append(checks, connCheck("active_directory.can_connect", ts, true, model.Truncate(msg, 300), tags()))
	}
	return checks, "ok"
}
