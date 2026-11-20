package integrations

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// Sortie réelle du script sur DC-ESSAI (Windows Server 2025, contrôleur unique de essai.local, 20/11/2026).
const adJSONEssai = `{"roles":["PDCEmulator","RIDMaster","InfrastructureMaster","SchemaMaster","DomainNamingMaster"],"partners":[],"shares":["NETLOGON","SYSVOL"],"errors":[],"domain":"essai.local","forest":"essai.local","domain_mode":"Windows2025Domain","forest_mode":"Windows2025Forest","site":"Default-First-Site-Name"}`

// Deux contrôleurs, réplication en échec depuis DC2 (exemple construit sur la même forme).
const adJSONPartners = `{"roles":["RIDMaster"],"shares":["SYSVOL"],"errors":[],"domain":"corp.exemple","forest":"corp.exemple","domain_mode":"Windows2016Domain","site":"Paris",
 "partners":[
  {"partner":"CN=NTDS Settings,CN=DC2,CN=Servers,CN=Paris,CN=Sites,CN=Configuration,DC=corp,DC=exemple","partition":"DC=corp,DC=exemple","last_result":1722,"failures":4,"success_age_s":3600},
  {"partner":"CN=NTDS Settings,CN=DC2,CN=Servers,CN=Paris,CN=Sites,CN=Configuration,DC=corp,DC=exemple","partition":"CN=Configuration,DC=corp,DC=exemple","last_result":0,"failures":0,"success_age_s":120},
  {"partner":"CN=NTDS Settings,CN=DC3,CN=Servers,CN=Lyon,CN=Sites,CN=Configuration,DC=corp,DC=exemple","partition":"DC=corp,DC=exemple","last_result":8524,"failures":200,"success_age_s":172800},
  {"partner":"CN=NTDS Settings,CN=DC4,CN=Servers,CN=Lyon,CN=Sites,CN=Configuration,DC=corp,DC=exemple","partition":"DC=corp,DC=exemple","last_result":8456,"failures":1,"success_age_s":-1}]}`

type fakeRole struct {
	present  bool
	svc      map[string]svcState
	pdh      pdhValues
	pdhErr   error
	ps       string
	psErr    error
	psCalls  int
	closings int
}

func (f *fakeRole) deps() winRoleDeps {
	return winRoleDeps{
		present:  func() bool { return f.present },
		pdh:      func([]string) (pdhValues, []string, error) { return f.pdh, nil, f.pdhErr },
		closePDH: func() { f.closings++ },
		services: func(names []string) map[string]svcState {
			out := map[string]svcState{}
			for _, n := range names {
				if s, ok := f.svc[n]; ok {
					out[n] = s
				}
			}
			return out
		},
		ps: func(context.Context) ([]byte, error) { f.psCalls++; return []byte(f.ps), f.psErr },
	}
}

func allRunning(names ...string) map[string]svcState {
	m := map[string]svcState{}
	for _, n := range names {
		m[n] = svcState{State: "running"}
	}
	return m
}

func checkMap(checks []model.Check, tag string) map[string]model.Check {
	out := map[string]model.Check{}
	for _, c := range checks {
		out[c.Name+"/"+c.Tags[tag]] = c
	}
	return out
}

func TestActiveDirectory(t *testing.T) {
	f := &fakeRole{present: true, ps: adJSONEssai,
		svc: allRunning("NTDS", "Netlogon", "Kdc", "ADWS", "DFSR", "IsmServ", "W32Time", "LanmanServer", "DNS"),
		pdh: pdhValues{
			`\NTDS\LDAP Client Sessions`:                                {"": 13},
			`\NTDS\LDAP Searches/sec`:                                   {"": 0.9},
			`\NTDS\LDAP Bind Time`:                                      {"": 2},
			`\Security System-Wide Statistics\Kerberos Authentications`: {"": 0.06},
			`\Netlogon(_Total)\Semaphore Waiters`:                       {"": 0},
		}}
	f.svc["IsmServ"] = svcState{State: "arrêté", Disabled: true} // désactivé : ignoré
	a := newActiveDirectory(config.ActiveDirectoryConfig{Enabled: "auto"}, f.deps())
	if a.name() != "active_directory" || a.period() != 30*time.Second {
		t.Fatal(a.name())
	}
	now := time.Now()
	b := model.NewSeriesBuilder(0, testTags)
	checks, st := a.collect(context.Background(), now, b, testTags)
	if st != "ok" {
		t.Fatalf("état %q", st)
	}
	got := checkMap(checks, "windows_service")
	if c := got["active_directory.can_connect/"]; c.Status != model.CheckOK || !strings.Contains(c.Message, "contrôleur du domaine essai.local (forêt essai.local, niveau Windows2025Domain)") ||
		!strings.Contains(c.Message, "rôles FSMO : PDCEmulator, RIDMaster, InfrastructureMaster, SchemaMaster, DomainNamingMaster") {
		t.Fatalf("can_connect : %+v", c)
	}
	if _, ok := got["active_directory.service/IsmServ"]; ok || got["active_directory.service/Netlogon"].Status != model.CheckOK || len(checks) != 8+3 {
		t.Fatalf("services : %d %+v", len(checks), got)
	}
	if c := got["active_directory.sysvol/"]; c.Status != model.CheckOK || c.Tags["ad_domain"] != "essai.local" {
		t.Fatalf("sysvol : %+v", c)
	}
	if c := got["active_directory.replication/"]; c.Status != model.CheckOK || !strings.Contains(c.Message, "contrôleur unique") {
		t.Fatalf("réplication : %+v", c)
	}
	series := seriesByKey(b)
	for k, want := range map[string]float64{"active_directory.ldap.client_sessions": 13, "active_directory.ldap.searches": 0.9,
		"active_directory.ldap.bind_time": 2, "active_directory.kerberos.authentications": 0.06, "active_directory.fsmo_roles": 5,
		"active_directory.replication.partners": 0, "active_directory.replication.failures": 0} {
		if series[k] != want {
			t.Errorf("%s = %v, attendu %v", k, series[k], want)
		}
	}
	// Le script n'est relancé que toutes les 5 minutes.
	a.collect(context.Background(), now.Add(30*time.Second), model.NewSeriesBuilder(0, testTags), testTags)
	if f.psCalls != 1 {
		t.Fatalf("PowerShell lancé %d fois", f.psCalls)
	}

	// Netlogon arrêté, NTDS arrêté : services et can_connect en CRIT.
	f.svc["Netlogon"] = svcState{State: "arrêté"}
	f.svc["NTDS"] = svcState{State: "arrêté"}
	checks, _ = a.collect(context.Background(), now.Add(time.Minute), model.NewSeriesBuilder(0, testTags), testTags)
	got = checkMap(checks, "windows_service")
	if got["active_directory.service/Netlogon"].Status != model.CheckCrit || got["active_directory.service/NTDS"].Status != model.CheckCrit ||
		got["active_directory.can_connect/"].Status != model.CheckCrit || !strings.Contains(got["active_directory.can_connect/"].Message, "NTDS arrêté") {
		t.Fatalf("pannes : %+v", got)
	}
}

func TestActiveDirectoryReplication(t *testing.T) {
	f := &fakeRole{present: true, ps: adJSONPartners, svc: allRunning("NTDS", "Netlogon")}
	a := newActiveDirectory(config.ActiveDirectoryConfig{Enabled: "true"}, f.deps())
	b := model.NewSeriesBuilder(0, testTags)
	checks, _ := a.collect(context.Background(), time.Now(), b, testTags)
	rep := map[string]model.Check{}
	for _, c := range checks {
		if c.Name == "active_directory.replication" {
			rep[c.Tags["ad_partner"]+"/"+c.Tags["ad_partition"]] = c
		}
	}
	for k, want := range map[string]int{"DC2/corp.exemple": model.CheckWarn, "DC2/Configuration": model.CheckOK,
		"DC3/corp.exemple": model.CheckCrit, "DC4/corp.exemple": model.CheckCrit} {
		if c, ok := rep[k]; !ok || c.Status != want {
			t.Errorf("%s : %+v, attendu %d", k, c, want)
		}
	}
	if !strings.Contains(rep["DC2/corp.exemple"].Message, "4 échec(s) de suite (code 1722)") || !strings.Contains(rep["DC4/corp.exemple"].Message, "jamais répliqué") {
		t.Fatalf("messages : %+v", rep)
	}
	series := seriesByKey(b, "ad_partner", "ad_partition")
	if series["active_directory.replication.failures"] != 205 || series["active_directory.replication.partners"] != 4 ||
		series["active_directory.replication.age/ad_partner=DC3/ad_partition=corp.exemple"] != 172800 {
		t.Fatalf("mesures : %v", series)
	}
	sysvol := checkMap(checks, "")["active_directory.sysvol/"]
	if sysvol.Status != model.CheckCrit || !strings.Contains(sysvol.Message, "NETLOGON absent") {
		t.Fatalf("sysvol : %+v", sysvol)
	}
	if partnerName("CN=NTDS Settings,CN=DC9,CN=Servers") != "DC9" || partitionName("DC=a,DC=b") != "a.b" || partitionName("CN=Schema,CN=Configuration,DC=a") != "Schema" {
		t.Fatal("noms")
	}
}

func TestActiveDirectoryDegraded(t *testing.T) {
	// ADWS arrêté : le module ActiveDirectory ne répond plus, les partages restent lus.
	f := &fakeRole{present: true, svc: allRunning("NTDS"), ps: `{"roles":[],"partners":[],"shares":["SYSVOL","NETLOGON"],"errors":["annuaire : Impossible de trouver un serveur par défaut avec les services Web Active Directory en cours d'exécution."]}`}
	a := newActiveDirectory(config.ActiveDirectoryConfig{Enabled: "auto"}, f.deps())
	checks, st := a.collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags)
	got := checkMap(checks, "")
	if got["active_directory.replication/"].Status != model.CheckUnknown || !strings.Contains(got["active_directory.replication/"].Message, "ADWS") ||
		got["active_directory.sysvol/"].Status != model.CheckOK || got["active_directory.can_connect/"].Status != model.CheckOK || st != "ok" {
		t.Fatalf("ADWS arrêté : %+v %q", got, st)
	}
	// PowerShell en échec : réplication inconnue, compteurs toujours lus.
	f = &fakeRole{present: true, svc: allRunning("NTDS"), psErr: errors.New("powershell.exe : délai dépassé"), pdhErr: errNoData}
	a = newActiveDirectory(config.ActiveDirectoryConfig{Enabled: "auto"}, f.deps())
	checks, st = a.collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags)
	got = checkMap(checks, "")
	if got["active_directory.replication/"].Status != model.CheckUnknown || got["active_directory.can_connect/"].Status != model.CheckOK || !strings.HasPrefix(st, "partiel") {
		t.Fatalf("PowerShell en échec : %+v %q", got, st)
	}
	// Compteurs illisibles : can_connect CRIT, requête PDH refermée.
	f = &fakeRole{present: true, svc: allRunning("NTDS"), ps: adJSONEssai, pdhErr: errors.New("PdhCollectQueryData : 0x800007d5")}
	a = newActiveDirectory(config.ActiveDirectoryConfig{Enabled: "auto"}, f.deps())
	checks, _ = a.collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags)
	if c := checkMap(checks, "")["active_directory.can_connect/"]; c.Status != model.CheckCrit || f.closings != 1 {
		t.Fatalf("compteurs : %+v fermetures %d", c, f.closings)
	}
	// Pas un contrôleur de domaine : muet en auto, erreur si forcé.
	f = &fakeRole{present: false}
	if checks, st := newActiveDirectory(config.ActiveDirectoryConfig{Enabled: "auto"}, f.deps()).collect(context.Background(), time.Now(),
		model.NewSeriesBuilder(0, testTags), testTags); len(checks) != 0 || st != "absent" {
		t.Fatalf("absent : %q %+v", st, checks)
	}
	if _, st := newActiveDirectory(config.ActiveDirectoryConfig{Enabled: "true"}, f.deps()).collect(context.Background(), time.Now(),
		model.NewSeriesBuilder(0, testTags), testTags); !strings.HasPrefix(st, "erreur") {
		t.Fatalf("forcé : %q", st)
	}
}

// Défauts vus sur DC-ESSAI (20/11) : relevé en échec gardé 5 minutes, étiquettes ad_domain
// changeantes, service en cours de démarrage compté comme arrêté.
func TestActiveDirectoryRecovery(t *testing.T) {
	f := &fakeRole{present: true, svc: allRunning("NTDS", "Netlogon", "DNS"), ps: adJSONEssai}
	a := newActiveDirectory(config.ActiveDirectoryConfig{Enabled: "auto"}, f.deps())
	now := time.Now()
	a.collect(context.Background(), now, model.NewSeriesBuilder(0, testTags), testTags)
	// NTDS s'arrête : relevé refait aussitôt, partages retirés, annuaire illisible.
	f.svc["NTDS"] = svcState{State: "arrêté"}
	f.svc["DNS"] = svcState{State: "en cours de démarrage"}
	f.ps = `{"roles":[],"partners":[],"shares":[],"errors":["annuaire : Impossible de trouver un serveur par défaut avec les services Web Active Directory en cours d'exécution."]}`
	checks, _ := a.collect(context.Background(), now.Add(30*time.Second), model.NewSeriesBuilder(0, testTags), testTags)
	got := checkMap(checks, "windows_service")
	if f.psCalls != 2 || got["active_directory.sysvol/"].Status != model.CheckCrit || got["active_directory.replication/"].Status != model.CheckUnknown {
		t.Fatalf("arrêt de NTDS : %d lectures, %+v", f.psCalls, got)
	}
	if got["active_directory.service/DNS"].Status != model.CheckWarn || got["active_directory.service/NTDS"].Status != model.CheckCrit {
		t.Fatalf("services : %+v", got)
	}
	for _, c := range checks {
		if (c.Name == "active_directory.sysvol" || c.Name == "active_directory.replication") && c.Tags["ad_domain"] != "essai.local" {
			t.Fatalf("étiquette ad_domain perdue : %+v", c)
		}
	}
	// NTDS repart : relevé refait aussitôt.
	f.svc["NTDS"] = svcState{State: "running"}
	f.svc["DNS"] = svcState{State: "running"}
	f.ps = adJSONEssai
	checks, _ = a.collect(context.Background(), now.Add(time.Minute), model.NewSeriesBuilder(0, testTags), testTags)
	if got = checkMap(checks, ""); f.psCalls != 3 || got["active_directory.sysvol/"].Status != model.CheckOK || got["active_directory.replication/"].Status != model.CheckOK {
		t.Fatalf("retour de NTDS : %d lectures, %+v", f.psCalls, got)
	}
	// PowerShell en échec, NTDS en marche tout du long : relu une minute après, pas cinq.
	f.psErr = errors.New("powershell.exe : délai dépassé")
	a.info.at = time.Time{}
	a.collect(context.Background(), now.Add(2*time.Minute), model.NewSeriesBuilder(0, testTags), testTags)
	f.psErr = nil
	a.collect(context.Background(), now.Add(2*time.Minute+30*time.Second), model.NewSeriesBuilder(0, testTags), testTags)
	if f.psCalls != 4 {
		t.Fatalf("relu avant la minute : %d", f.psCalls)
	}
	checks, _ = a.collect(context.Background(), now.Add(3*time.Minute), model.NewSeriesBuilder(0, testTags), testTags)
	if got = checkMap(checks, ""); f.psCalls != 5 || got["active_directory.replication/"].Status != model.CheckOK {
		t.Fatalf("échec gardé : %d lectures, %+v", f.psCalls, got)
	}
	// Relevé sain : gardé 5 minutes.
	a.collect(context.Background(), now.Add(5*time.Minute), model.NewSeriesBuilder(0, testTags), testTags)
	if f.psCalls != 5 {
		t.Fatalf("relevé sain relu trop tôt : %d", f.psCalls)
	}
}
