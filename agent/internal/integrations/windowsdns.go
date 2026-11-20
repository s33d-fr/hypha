package integrations

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	globpath "path"
	"sort"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── DNS Microsoft (vague 3) : rôle DNS de Windows Server, en lecture seule ────────────────────
//
// Branché sous Windows (winprobes_windows.go) quand le service DNS existe. Toutes les 30 s :
// compteurs de l'objet DNS (requêtes reçues, réponses, récursion et ses échecs, mises à jour
// dynamiques refusées, transferts de zone en échec, mémoire), et une vraie question SOA posée au
// serveur (127.0.0.1:53 par défaut) pour prouver qu'il répond. Toutes les 5 min, par PowerShell
// (module DnsServer) : zones, leur état (suspendue, arrêtée), le numéro de série des zones
// principales, le dernier transfert des zones secondaires. Le journal DNS Server s'ajoute à la
// collecte (logs : EventlogChannels).

var dnsCounters = []pdhMetric{
	{`\DNS\Total Query Received/sec`, "windows_dns.queries", "operation_per_second"},
	{`\DNS\Total Response Sent/sec`, "windows_dns.responses", "operation_per_second"},
	{`\DNS\UDP Query Received/sec`, "windows_dns.queries.udp", "operation_per_second"},
	{`\DNS\TCP Query Received/sec`, "windows_dns.queries.tcp", "operation_per_second"},
	{`\DNS\Recursive Queries/sec`, "windows_dns.recursive.queries", "operation_per_second"},
	{`\DNS\Recursive Query Failure/sec`, "windows_dns.recursive.failures", "operation_per_second"},
	{`\DNS\Recursive TimeOut/sec`, "windows_dns.recursive.timeouts", "operation_per_second"},
	{`\DNS\Dynamic Update Received/sec`, "windows_dns.dynamic_updates", "operation_per_second"},
	{`\DNS\Query Dropped Total/sec`, "windows_dns.queries.dropped", "operation_per_second"},
	{`\DNS\Caching Memory`, "windows_dns.memory.cache", "byte"},
	{`\DNS\Database Node Memory`, "windows_dns.memory.database", "byte"},
}

// dnsTotals : compteurs cumulés depuis le démarrage du service, rendus en différence par relevé.
var dnsTotals = []pdhMetric{
	{`\DNS\Dynamic Update Rejected`, "windows_dns.dynamic_updates.rejected", "count"},
	{`\DNS\Secure Update Failure`, "windows_dns.secure_updates.failed", "count"},
	{`\DNS\Zone Transfer Failure`, "windows_dns.zone_transfers.failed", "count"},
	{`\DNS\Zone Transfer Success`, "windows_dns.zone_transfers.succeeded", "count"},
}

type dnsZone struct {
	Name               string   `json:"name"`
	Type               string   `json:"type"`
	Paused             bool     `json:"paused"`
	Shutdown           bool     `json:"shutdown"`
	DS                 bool     `json:"ds"`
	Reverse            bool     `json:"reverse"`
	Dynamic            string   `json:"dynamic"`
	Signed             bool     `json:"signed"`
	Serial             *float64 `json:"serial"`
	TransferResult     *int64   `json:"transfer_result"`
	TransferSuccessAge *float64 `json:"transfer_success_age_s"`
}

type dnsInfo struct {
	Zones  []dnsZone `json:"zones"`
	Errors []string  `json:"errors"`
}

const dnsScript = `$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [Text.Encoding]::UTF8
$WarningPreference = 'SilentlyContinue'
$ProgressPreference = 'SilentlyContinue'
$r = [ordered]@{ zones = @(); errors = @() }
try {
  Import-Module DnsServer
  $now = Get-Date; $n = 0
  $r.zones = @(Get-DnsServerZone | Where-Object { -not $_.IsAutoCreated -and $_.ZoneName -ne 'TrustAnchors' } | Select-Object -First 1000 | ForEach-Object {
    $z = $_; $o = [ordered]@{ name = [string]$z.ZoneName; type = [string]$z.ZoneType; paused = [bool]$z.IsPaused; shutdown = [bool]$z.IsShutdown
      ds = [bool]$z.IsDsIntegrated; reverse = [bool]$z.IsReverseLookupZone; dynamic = [string]$z.DynamicUpdate; signed = [bool]$z.IsSigned }
    if ($z.ZoneType -eq 'Primary' -and -not $z.IsShutdown -and $n -lt 100) {
      $n++
      try { $o.serial = [int64](@(Get-DnsServerResourceRecord -ZoneName $z.ZoneName -RRType Soa -Name '@')[0].RecordData.SerialNumber) } catch {}
    }
    if ($z.PSObject.Properties['LastZoneTransferResult'] -and $z.LastZoneTransferResult -ne $null) { $o.transfer_result = [int64]$z.LastZoneTransferResult }
    if ($z.PSObject.Properties['LastSuccessfulZoneTransfer'] -and $z.LastSuccessfulZoneTransfer -and $z.LastSuccessfulZoneTransfer.Year -gt 1601) {
      $o.transfer_success_age_s = [int64]($now - $z.LastSuccessfulZoneTransfer).TotalSeconds }
    $o
  })
} catch { $r.errors += $_.Exception.Message }
ConvertTo-Json -InputObject $r -Depth 4 -Compress`

// dnsHealthy : aucune zone suspendue, arrêtée ou au transfert en échec.
func dnsHealthy(r dnsInfo) bool {
	for _, z := range r.Zones {
		if z.Paused || z.Shutdown || (z.TransferResult != nil && *z.TransferResult != 0) {
			return false
		}
	}
	return len(r.Errors) == 0
}

type windowsDNS struct {
	cfg  config.WindowsDNSConfig
	det  detector
	d    winRoleDeps
	info cache[dnsInfo]
	prev map[string]float64 // compteurs cumulés au relevé précédent
	// lastZone : dernière zone interrogée ; la question SOA reste posée quand les zones sont illisibles.
	lastZone string
	dnsUp    *bool // état du service au relevé précédent
	// query pose la question SOA (remplacée dans les tests).
	query func(ctx context.Context, addr, zone string, timeout time.Duration) (dnsAnswer, error)
}

func newWindowsDNS(cfg config.WindowsDNSConfig, d winRoleDeps) *windowsDNS {
	if cfg.Address == "" {
		cfg.Address = "127.0.0.1:53"
	}
	return &windowsDNS{cfg: cfg, det: detector{mode: cfg.Enabled}, d: d, prev: map[string]float64{}, query: dnsQuerySOA}
}

func (w *windowsDNS) name() string          { return "windows_dns" }
func (w *windowsDNS) period() time.Duration { return 30 * time.Second }
func (w *windowsDNS) close() {
	if w.d.closePDH != nil {
		w.d.closePDH()
	}
}

func (w *windowsDNS) excluded(zone string) bool {
	z := strings.ToLower(zone)
	for _, g := range w.cfg.ExcludeZones {
		if ok, _ := globpath.Match(strings.ToLower(g), z); ok {
			return true
		}
	}
	return false
}

// testZone : zone de la question SOA (réglée, sinon la première zone principale de recherche
// directe en marche, hors _msdcs).
func (w *windowsDNS) testZone(info dnsInfo) string {
	if w.cfg.TestZone != "" {
		return w.cfg.TestZone
	}
	if len(info.Zones) == 0 {
		return w.lastZone
	}
	best := ""
	for _, z := range info.Zones {
		if z.Type == "Primary" && !z.Reverse && !z.Shutdown && !z.Paused && !strings.HasPrefix(strings.ToLower(z.Name), "_msdcs.") && !w.excluded(z.Name) {
			if best == "" || len(z.Name) < len(best) {
				best = z.Name
			}
		}
	}
	return best
}

func (w *windowsDNS) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := w.det.skip(now); skip {
		return nil, st
	}
	if !w.d.present() {
		if w.cfg.Enabled == "true" {
			return nil, "erreur : rôle DNS absent (service DNS)"
		}
		_, st := w.det.failed("rôle DNS absent")
		return nil, st
	}
	w.det.ok()
	ts := model.Ms(now)
	var checks []model.Check
	svc := w.d.services([]string{"DNS"})
	up := svc["DNS"].State == "running"

	paths := make([]string, 0, len(dnsCounters)+len(dnsTotals))
	for _, c := range append(append([]pdhMetric{}, dnsCounters...), dnsTotals...) {
		paths = append(paths, c.path)
	}
	vals, _, err := w.d.pdh(paths)
	if err != nil && !errors.Is(err, errNoData) {
		w.close()
	}
	for _, c := range dnsCounters {
		if v, ok := vals[c.path][""]; ok {
			b.Gauge(c.metric, c.unit, v)
		}
	}
	for _, c := range dnsTotals {
		v, ok := vals[c.path][""]
		if !ok {
			continue
		}
		if p, had := w.prev[c.path]; had && v >= p {
			b.Add(c.metric, model.TypeCount, c.unit, v-p)
		}
		w.prev[c.path] = v // service relancé (v < p) : on repart de cette valeur
	}

	// Zones : toutes les 5 min, chaque minute tant qu'une zone est en défaut ou illisible, et tout de
	// suite quand le service s'arrête ou repart.
	refresh := 5 * time.Minute
	if !w.info.at.IsZero() && (w.info.err != nil || !dnsHealthy(w.info.value)) {
		refresh = time.Minute
	}
	if w.dnsUp != nil && *w.dnsUp != up {
		w.info.at = time.Time{}
	}
	w.dnsUp = &up
	info, perr := w.info.get(now, refresh, func() (dnsInfo, error) {
		var r dnsInfo
		out, err := w.d.ps(ctx)
		if err == nil {
			err = json.Unmarshal(out, &r)
		}
		if err == nil && len(r.Errors) > 0 && len(r.Zones) == 0 {
			err = errors.New(strings.Join(r.Errors, " ; "))
		}
		return r, err
	})
	zonesOK := perr == nil
	if !zonesOK {
		checks = append(checks, model.Check{TS: ts, Name: "windows_dns.zone", Status: model.CheckUnknown,
			Message: model.Truncate("zones illisibles : "+perr.Error(), 300), Tags: tags()})
	} else {
		sort.Slice(info.Zones, func(i, j int) bool { return info.Zones[i].Name < info.Zones[j].Name })
		count := 0
		for _, z := range info.Zones {
			if w.excluded(z.Name) {
				continue
			}
			count++
			kv := []string{"dns_zone", z.Name}
			if z.Serial != nil {
				b.Gauge("windows_dns.zone.serial", "", *z.Serial, kv...)
			}
			if z.TransferSuccessAge != nil {
				b.Gauge("windows_dns.zone.transfer_age", "second", *z.TransferSuccessAge, kv...)
			}
			kind := strings.ToLower(z.Type)
			if z.DS {
				kind += ", intégrée à l'annuaire"
			}
			st, msg := model.CheckOK, "zone "+kind+" en service"
			switch {
			case z.Shutdown:
				st, msg = model.CheckCrit, "zone "+kind+" arrêtée (zone secondaire expirée, ou chargement impossible) : le serveur ne la sert plus"
			case z.Paused:
				st, msg = model.CheckWarn, "zone "+kind+" suspendue : le serveur ne répond plus pour elle"
			case z.TransferResult != nil && *z.TransferResult != 0:
				st, msg = model.CheckWarn, fmt.Sprintf("zone %s : dernier transfert depuis le principal en échec (code %d)", kind, *z.TransferResult)
			}
			checks = append(checks, model.Check{TS: ts, Name: "windows_dns.zone", Status: st, Message: z.Name + " : " + msg, Tags: tags(kv...)})
		}
		b.Gauge("windows_dns.zones", "count", float64(count))
	}

	// La question posée au serveur.
	zone := w.testZone(info)
	if zone != "" {
		w.lastZone = zone
	}
	switch {
	case !up:
		if state := svc["DNS"].State; strings.HasPrefix(state, "en cours") {
			// Relance en cours (le service attend l'annuaire au démarrage d'un contrôleur) : avertissement.
			checks = append(checks, model.Check{TS: ts, Name: "windows_dns.can_connect", Status: model.CheckWarn,
				Message: "service DNS " + state + " : le serveur ne répond pas encore", Tags: tags()})
		} else {
			checks = append(checks, connCheck("windows_dns.can_connect", ts, false, "service DNS "+state+" : le serveur ne répond plus", tags()))
		}
	case zone == "":
		msg := "service DNS en marche (aucune zone principale à interroger)"
		checks = append(checks, connCheck("windows_dns.can_connect", ts, true, msg, tags()))
	default:
		ans, err := w.query(ctx, w.cfg.Address, zone, 3*time.Second)
		switch {
		case err != nil:
			checks = append(checks, connCheck("windows_dns.can_connect", ts, false, model.Truncate(fmt.Sprintf("pas de réponse de %s au SOA de %s : %v", w.cfg.Address, zone, err), 300), tags()))
		case ans.RCode != 0:
			checks = append(checks, connCheck("windows_dns.can_connect", ts, false, fmt.Sprintf("%s répond %s au SOA de %s", w.cfg.Address, dnsRCodeName(ans.RCode), zone), tags()))
		default:
			b.Gauge("windows_dns.response_time", "millisecond", float64(ans.RTT.Microseconds())/1000)
			msg := fmt.Sprintf("SOA de %s rendu en %.1f ms", zone, float64(ans.RTT.Microseconds())/1000)
			if !ans.Authoritative {
				msg += " (réponse non autoritaire : la zone n'est pas chargée ?)"
			}
			checks = append(checks, connCheck("windows_dns.can_connect", ts, ans.Authoritative, msg, tags()))
		}
	}
	if perr != nil {
		return checks, "partiel : " + perr.Error()
	}
	return checks, "ok"
}

// ── question DNS minimale (RFC 1035), sans dépendance ───────────────────────────────────────

type dnsAnswer struct {
	RCode         int
	Authoritative bool
	Answers       int
	RTT           time.Duration
}

func dnsRCodeName(c int) string {
	switch c {
	case 1:
		return "FORMERR"
	case 2:
		return "SERVFAIL"
	case 3:
		return "NXDOMAIN"
	case 4:
		return "NOTIMP"
	case 5:
		return "REFUSED"
	}
	return fmt.Sprintf("RCODE %d", c)
}

// dnsPacket : question SOA, sans récursion demandée (le serveur répond de ses propres zones).
func dnsPacket(id uint16, zone string) ([]byte, error) {
	msg := make([]byte, 12, 64)
	binary.BigEndian.PutUint16(msg[0:], id)
	binary.BigEndian.PutUint16(msg[4:], 1) // une question
	for _, label := range strings.Split(strings.TrimSuffix(zone, "."), ".") {
		if label == "" || len(label) > 63 {
			return nil, fmt.Errorf("nom de zone %q invalide", zone)
		}
		msg = append(msg, byte(len(label)))
		msg = append(msg, label...)
	}
	msg = append(msg, 0, 0, 6, 0, 1) // fin du nom, type SOA, classe IN
	return msg, nil
}

func dnsQuerySOA(ctx context.Context, addr, zone string, timeout time.Duration) (dnsAnswer, error) {
	var idb [2]byte
	_, _ = rand.Read(idb[:])
	id := binary.BigEndian.Uint16(idb[:])
	pkt, err := dnsPacket(id, zone)
	if err != nil {
		return dnsAnswer{}, err
	}
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "udp", addr)
	if err != nil {
		return dnsAnswer{}, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	start := time.Now()
	if _, err := conn.Write(pkt); err != nil {
		return dnsAnswer{}, err
	}
	buf := make([]byte, 4096)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return dnsAnswer{}, err
		}
		if n < 12 || binary.BigEndian.Uint16(buf[0:]) != id || buf[2]&0x80 == 0 {
			continue // réponse étrangère ou tronquée : on attend la nôtre
		}
		return dnsAnswer{RCode: int(buf[3] & 0x0f), Authoritative: buf[2]&0x04 != 0,
			Answers: int(binary.BigEndian.Uint16(buf[6:])), RTT: time.Since(start)}, nil
	}
}
