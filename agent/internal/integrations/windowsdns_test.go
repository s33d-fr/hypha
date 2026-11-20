package integrations

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// Sortie réelle du script sur DC-ESSAI (zones intégrées à l'annuaire de essai.local, 20/11/2026).
const dnsJSONEssai = `{"zones":[{"name":"_msdcs.essai.local","type":"Primary","paused":false,"shutdown":false,"ds":true,"reverse":false,"dynamic":"Secure","signed":false,"serial":12},{"name":"essai.local","type":"Primary","paused":false,"shutdown":false,"ds":true,"reverse":false,"dynamic":"Secure","signed":false,"serial":22}],"errors":[]}`

// Zones en défaut (même forme) : secondaire expirée, secondaire au transfert refusé, zone suspendue.
const dnsJSONPannes = `{"zones":[
 {"name":"exemple.fr","type":"Primary","paused":true,"shutdown":false,"ds":false,"reverse":false,"dynamic":"None","signed":false,"serial":2026112001},
 {"name":"partenaire.fr","type":"Secondary","paused":false,"shutdown":true,"ds":false,"reverse":false,"dynamic":"None","signed":false,"transfer_result":9002,"transfer_success_age_s":700000},
 {"name":"autre.fr","type":"Secondary","paused":false,"shutdown":false,"ds":false,"reverse":false,"dynamic":"None","signed":false,"transfer_result":5,"transfer_success_age_s":3000},
 {"name":"1.168.192.in-addr.arpa","type":"Primary","paused":false,"shutdown":false,"ds":true,"reverse":true,"dynamic":"Secure","signed":false,"serial":7},
 {"name":"labo.exemple.fr","type":"Primary","paused":false,"shutdown":false,"ds":false,"reverse":false,"dynamic":"None","signed":false,"serial":3}],"errors":[]}`

func TestWindowsDNS(t *testing.T) {
	f := &fakeRole{present: true, ps: dnsJSONEssai, svc: allRunning("DNS"), pdh: pdhValues{
		`\DNS\Total Query Received/sec`:    {"": 0.3},
		`\DNS\Recursive Query Failure/sec`: {"": 0},
		`\DNS\Caching Memory`:              {"": 16456},
		`\DNS\Zone Transfer Failure`:       {"": 4},
		`\DNS\Dynamic Update Rejected`:     {"": 10},
	}}
	w := newWindowsDNS(config.WindowsDNSConfig{Enabled: "auto"}, f.deps())
	var askedAddr, askedZone string
	w.query = func(_ context.Context, addr, zone string, _ time.Duration) (dnsAnswer, error) {
		askedAddr, askedZone = addr, zone
		return dnsAnswer{Authoritative: true, Answers: 1, RTT: 1208 * time.Microsecond}, nil
	}
	now := time.Now()
	b := model.NewSeriesBuilder(0, testTags)
	checks, st := w.collect(context.Background(), now, b, testTags)
	if st != "ok" || askedAddr != "127.0.0.1:53" || askedZone != "essai.local" {
		t.Fatalf("%q %s %s", st, askedAddr, askedZone)
	}
	got := checkMap(checks, "dns_zone")
	if c := got["windows_dns.can_connect/"]; c.Status != model.CheckOK || c.Message != "SOA de essai.local rendu en 1.2 ms" {
		t.Fatalf("can_connect : %+v", c)
	}
	if c := got["windows_dns.zone/essai.local"]; c.Status != model.CheckOK || !strings.Contains(c.Message, "intégrée à l'annuaire") {
		t.Fatalf("zone : %+v", c)
	}
	series := seriesByKey(b, "dns_zone")
	if series["windows_dns.queries"] != 0.3 || series["windows_dns.memory.cache"] != 16456 || series["windows_dns.zones"] != 2 ||
		series["windows_dns.zone.serial/dns_zone=essai.local"] != 22 || series["windows_dns.response_time"] != 1.208 {
		t.Fatalf("mesures : %v", series)
	}
	if _, ok := series["windows_dns.zone_transfers.failed"]; ok {
		t.Fatal("compteur cumulé rendu dès le premier relevé")
	}
	// Relevé suivant : les compteurs cumulés deviennent des différences.
	f.pdh[`\DNS\Zone Transfer Failure`] = map[string]float64{"": 6}
	f.pdh[`\DNS\Dynamic Update Rejected`] = map[string]float64{"": 3} // service relancé : on repart
	b = model.NewSeriesBuilder(0, testTags)
	w.collect(context.Background(), now.Add(30*time.Second), b, testTags)
	series = seriesByKey(b)
	if series["windows_dns.zone_transfers.failed"] != 2 {
		t.Fatalf("différence : %v", series)
	}
	if _, ok := series["windows_dns.dynamic_updates.rejected"]; ok {
		t.Fatal("compteur remis à zéro rendu comme une différence négative")
	}

	// Service arrêté : can_connect CRIT sans question posée.
	f.svc["DNS"] = svcState{State: "arrêté"}
	askedZone = ""
	checks, _ = w.collect(context.Background(), now.Add(time.Minute), model.NewSeriesBuilder(0, testTags), testTags)
	if c := checkMap(checks, "")["windows_dns.can_connect/"]; c.Status != model.CheckCrit || askedZone != "" {
		t.Fatalf("arrêté : %+v", c)
	}
	// Réponse REFUSED, puis réponse non autoritaire.
	f.svc["DNS"] = svcState{State: "running"}
	w.query = func(context.Context, string, string, time.Duration) (dnsAnswer, error) {
		return dnsAnswer{RCode: 5}, nil
	}
	checks, _ = w.collect(context.Background(), now.Add(2*time.Minute), model.NewSeriesBuilder(0, testTags), testTags)
	if c := checkMap(checks, "")["windows_dns.can_connect/"]; c.Status != model.CheckCrit || !strings.Contains(c.Message, "REFUSED") {
		t.Fatalf("REFUSED : %+v", c)
	}
	w.query = func(context.Context, string, string, time.Duration) (dnsAnswer, error) {
		return dnsAnswer{RTT: time.Millisecond}, nil
	}
	checks, _ = w.collect(context.Background(), now.Add(3*time.Minute), model.NewSeriesBuilder(0, testTags), testTags)
	if c := checkMap(checks, "")["windows_dns.can_connect/"]; c.Status != model.CheckCrit || !strings.Contains(c.Message, "non autoritaire") {
		t.Fatalf("non autoritaire : %+v", c)
	}
}

func TestWindowsDNSZones(t *testing.T) {
	f := &fakeRole{present: true, ps: dnsJSONPannes, svc: allRunning("DNS")}
	w := newWindowsDNS(config.WindowsDNSConfig{Enabled: "auto", ExcludeZones: []string{"LABO.*"}}, f.deps())
	var asked string
	w.query = func(_ context.Context, _, zone string, _ time.Duration) (dnsAnswer, error) {
		asked = zone
		return dnsAnswer{Authoritative: true}, nil
	}
	b := model.NewSeriesBuilder(0, testTags)
	checks, _ := w.collect(context.Background(), time.Now(), b, testTags)
	got := checkMap(checks, "dns_zone")
	for z, want := range map[string]int{"exemple.fr": model.CheckWarn, "partenaire.fr": model.CheckCrit, "autre.fr": model.CheckWarn,
		"1.168.192.in-addr.arpa": model.CheckOK} {
		if c := got["windows_dns.zone/"+z]; c.Status != want {
			t.Errorf("%s : %+v", z, c)
		}
	}
	if _, ok := got["windows_dns.zone/labo.exemple.fr"]; ok {
		t.Error("zone exclue contrôlée")
	}
	if !strings.Contains(got["windows_dns.zone/autre.fr"].Message, "code 5") || !strings.Contains(got["windows_dns.zone/partenaire.fr"].Message, "arrêtée") {
		t.Fatalf("messages : %+v", got)
	}
	// Aucune zone principale en marche hors exclusions et suspendues : pas de question, service jugé seul.
	if asked != "" || got["windows_dns.can_connect/"].Status != model.CheckOK {
		t.Fatalf("question posée à %q : %+v", asked, got["windows_dns.can_connect/"])
	}
	if s := seriesByKey(b, "dns_zone"); s["windows_dns.zones"] != 4 || s["windows_dns.zone.transfer_age/dns_zone=autre.fr"] != 3000 {
		t.Fatalf("mesures : %v", s)
	}
	// Zone d'essai réglée.
	w = newWindowsDNS(config.WindowsDNSConfig{Enabled: "auto", TestZone: "corp.exemple", Address: "192.0.2.1:53"}, f.deps())
	if z := w.testZone(dnsInfo{}); z != "corp.exemple" || w.cfg.Address != "192.0.2.1:53" {
		t.Fatalf("zone réglée : %s", z)
	}
}

// fakeDNSServer répond à une question SOA : flags et code de retour imposés.
func fakeDNSServer(t *testing.T, flags byte, rcode byte) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 512)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			q := append([]byte{}, buf[:n]...)
			// Une réponse étrangère d'abord (autre identifiant) : elle doit être ignorée.
			bad := append([]byte{}, q...)
			binary.BigEndian.PutUint16(bad, binary.BigEndian.Uint16(q)+1)
			bad[2] |= 0x80
			pc.WriteTo(bad, addr)
			r := append([]byte{}, q...)
			r[2] = 0x80 | flags
			r[3] = rcode
			binary.BigEndian.PutUint16(r[6:], 1)
			pc.WriteTo(r, addr)
		}
	}()
	return pc.LocalAddr().String()
}

func TestDNSQuerySOA(t *testing.T) {
	pkt, err := dnsPacket(0x1234, "essai.local.")
	if err != nil || len(pkt) != 12+len("\x05essai\x05local\x00")+4 || pkt[0] != 0x12 || pkt[5] != 1 || pkt[len(pkt)-3] != 6 {
		t.Fatalf("paquet : % x %v", pkt, err)
	}
	if _, err := dnsPacket(1, "a..b"); err == nil {
		t.Fatal("nom invalide accepté")
	}
	addr := fakeDNSServer(t, 0x04, 0) // AA
	ans, err := dnsQuerySOA(context.Background(), addr, "essai.local", time.Second)
	if err != nil || ans.RCode != 0 || !ans.Authoritative || ans.Answers != 1 || ans.RTT <= 0 {
		t.Fatalf("réponse : %+v %v", ans, err)
	}
	addr = fakeDNSServer(t, 0, 5)
	if ans, err = dnsQuerySOA(context.Background(), addr, "essai.local", time.Second); err != nil || ans.RCode != 5 || ans.Authoritative {
		t.Fatalf("REFUSED : %+v %v", ans, err)
	}
	// Personne n'écoute : délai, puis erreur.
	pc, _ := net.ListenPacket("udp", "127.0.0.1:0")
	silent := pc.LocalAddr().String()
	defer pc.Close()
	if _, err := dnsQuerySOA(context.Background(), silent, "essai.local", 100*time.Millisecond); err == nil {
		t.Fatal("aucune réponse attendue")
	}
	if dnsRCodeName(2) != "SERVFAIL" || dnsRCodeName(9) != "RCODE 9" {
		t.Fatal("codes")
	}
}

func TestWindowsDNSRecovery(t *testing.T) {
	f := &fakeRole{present: true, ps: dnsJSONEssai, svc: allRunning("DNS")}
	w := newWindowsDNS(config.WindowsDNSConfig{Enabled: "auto"}, f.deps())
	var asked []string
	w.query = func(_ context.Context, _, zone string, _ time.Duration) (dnsAnswer, error) {
		asked = append(asked, zone)
		return dnsAnswer{Authoritative: true}, nil
	}
	now := time.Now()
	w.collect(context.Background(), now, model.NewSeriesBuilder(0, testTags), testTags)
	// Service en cours de relance : avertissement, zones relues aussitôt (illisibles).
	f.svc["DNS"] = svcState{State: "en cours de démarrage"}
	f.ps, f.psErr = "", errors.New("Échec de l’énumération des zones du serveur DC-ESSAI.")
	checks, _ := w.collect(context.Background(), now.Add(30*time.Second), model.NewSeriesBuilder(0, testTags), testTags)
	got := checkMap(checks, "dns_zone")
	if got["windows_dns.can_connect/"].Status != model.CheckWarn || got["windows_dns.zone/"].Status != model.CheckUnknown || f.psCalls != 2 {
		t.Fatalf("relance : %d lectures, %+v", f.psCalls, got)
	}
	// Service revenu, zones toujours illisibles : la question SOA part quand même vers la dernière zone.
	f.svc["DNS"] = svcState{State: "running"}
	checks, _ = w.collect(context.Background(), now.Add(time.Minute), model.NewSeriesBuilder(0, testTags), testTags)
	if c := checkMap(checks, "")["windows_dns.can_connect/"]; c.Status != model.CheckOK || len(asked) != 2 || asked[1] != "essai.local" {
		t.Fatalf("question sans zones : %v %+v", asked, c)
	}
	// Zones de nouveau lisibles une minute après l'échec.
	f.ps, f.psErr = dnsJSONEssai, nil
	checks, _ = w.collect(context.Background(), now.Add(2*time.Minute), model.NewSeriesBuilder(0, testTags), testTags)
	if c := checkMap(checks, "dns_zone")["windows_dns.zone/essai.local"]; c.Status != model.CheckOK {
		t.Fatalf("zones relues : %d lectures, %+v", f.psCalls, c)
	}
}
