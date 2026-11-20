package logs

import (
	"strings"
	"testing"
	"time"

	"s33d.fr/hypha/agent/internal/config"
)

// Événements rendus par EvtFormatMessage sur un Windows Server 2025 en français (DC-ESSAI,
// contrôleur du domaine d'essai essai.local, 20/11/2026), données raccourcies.
const (
	evt4625 = `<Event xmlns='http://schemas.microsoft.com/win/2004/08/events/event'><System><Provider Name='Microsoft-Windows-Security-Auditing' Guid='{54849625-5478-4994-a5ba-3e3b0328c30d}'/><EventID>4625</EventID><Version>0</Version><Level>0</Level><Task>12544</Task><Opcode>0</Opcode><Keywords>0x8010000000000000</Keywords><TimeCreated SystemTime='2026-11-20T03:03:05.1234567Z'/><EventRecordID>2512</EventRecordID><Correlation/><Execution ProcessID='744' ThreadID='800'/><Channel>Security</Channel><Computer>dc-essai.essai.local</Computer><Security/></System><EventData><Data Name='SubjectUserSid'>S-1-0-0</Data><Data Name='TargetUserName'>nexistepas-v3w</Data><Data Name='TargetDomainName'>ESSAI</Data><Data Name='Status'>0xc000006d</Data><Data Name='IpAddress'>127.0.0.1</Data></EventData><RenderingInfo Culture='fr-FR'><Message>Échec d’ouverture de session d’un compte.</Message><Level>Information</Level><Task>Logon</Task><Opcode>Informations</Opcode><Channel>Sécurité</Channel><Provider>Microsoft Windows security auditing.</Provider><Keywords><Keyword>Échec de l’audit</Keyword></Keywords></RenderingInfo></Event>`
	evt4726 = `<Event xmlns='http://schemas.microsoft.com/win/2004/08/events/event'><System><Provider Name='Microsoft-Windows-Security-Auditing' Guid='{54849625-5478-4994-a5ba-3e3b0328c30d}'/><EventID>4726</EventID><Version>0</Version><Level>0</Level><Task>13824</Task><Opcode>0</Opcode><Keywords>0x8020000000000000</Keywords><TimeCreated SystemTime='2026-11-20T03:03:09.0000000Z'/><EventRecordID>2520</EventRecordID><Correlation/><Execution ProcessID='744' ThreadID='812'/><Channel>Security</Channel><Computer>dc-essai.essai.local</Computer><Security/></System><EventData><Data Name='TargetUserName'>essai-v3w</Data></EventData><RenderingInfo Culture='fr-FR'><Message>Un compte d’utilisateur a été supprimé.</Message><Level>Information</Level><Task>User Account Management</Task><Opcode>Informations</Opcode><Keywords><Keyword>Succès de l’audit</Keyword></Keywords></RenderingInfo></Event>`
	evtDNS  = `<Event xmlns='http://schemas.microsoft.com/win/2004/08/events/event'><System><Provider Name='Microsoft-Windows-DNS-Server-Service' Guid='{71a551f5-c893-4849-886b-b5ec8502641e}'/><EventID>4013</EventID><Version>0</Version><Level>3</Level><Task>0</Task><Opcode>0</Opcode><Keywords>0x8000000000020000</Keywords><TimeCreated SystemTime='2026-11-20T03:00:12.0000000Z'/><EventRecordID>9</EventRecordID><Correlation/><Execution ProcessID='2940' ThreadID='3000'/><Channel>DNS Server</Channel><Computer>dc-essai.essai.local</Computer><Security UserID='S-1-5-18'/></System><EventData></EventData><RenderingInfo Culture='fr-FR'><Message>Le serveur DNS attend que les services de domaine Active Directory signalent que la synchronisation initiale de l’annuaire est terminée.</Message><Level>Avertissement</Level><Task></Task><Opcode>Informations</Opcode><Keywords><Keyword>Classique</Keyword></Keywords></RenderingInfo></Event>`
)

func mustEvent(t *testing.T, x string) *evtEvent {
	t.Helper()
	ev, err := decodeEvent(x)
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

func TestEventlogFilterMatch(t *testing.T) {
	failed, deleted, dns := mustEvent(t, evt4625), mustEvent(t, evt4726), mustEvent(t, evtDNS)
	for _, c := range []struct {
		name string
		ch   config.EventlogChannel
		want [3]bool // 4625, 4726, DNS 4013
	}{
		{"sans filtre", config.EventlogChannel{Channel: "x"}, [3]bool{true, true, true}},
		{"échecs d'audit", config.EventlogChannel{Levels: []string{"audit_failure"}}, [3]bool{true, false, false}},
		{"succès d'audit", config.EventlogChannel{Levels: []string{"audit_success"}}, [3]bool{false, true, false}},
		// Un audit (niveau 0) n'est pas une « information » ; un avertissement de niveau 3 n'est pas un échec d'audit.
		{"avertissements", config.EventlogChannel{Levels: []string{"warning"}}, [3]bool{false, false, true}},
		{"informations", config.EventlogChannel{Levels: []string{"info"}}, [3]bool{false, false, false}},
		{"plages", config.EventlogChannel{EventIDs: []string{"4625", "4720-4726"}}, [3]bool{true, true, false}},
		{"exclusion", config.EventlogChannel{ExcludeEventIDs: []string{"4700-4799"}}, [3]bool{true, false, true}},
		{"sources (sans casse, glob)", config.EventlogChannel{Providers: []string{"microsoft-windows-dns-*"}}, [3]bool{false, false, true}},
		{"sources écartées", config.EventlogChannel{ExcludeProviders: []string{"Microsoft-Windows-Security-Auditing"}}, [3]bool{false, false, true}},
		{"cumul", config.EventlogChannel{EventIDs: []string{"4625"}, Levels: []string{"audit_success"}}, [3]bool{false, false, false}},
	} {
		f := newEvtFilter(c.ch)
		got := [3]bool{f.match(failed), f.match(deleted), f.match(dns)}
		if got != c.want {
			t.Errorf("%s : %v, attendu %v", c.name, got, c.want)
		}
	}
}

func TestEventlogFilterXPath(t *testing.T) {
	f := newEvtFilter(config.EventlogChannel{Channel: "Security", Levels: []string{"error", "audit_failure", "critical"},
		EventIDs: []string{"4625", "4720-4726"}, ExcludeEventIDs: []string{"4634"}})
	q := f.query(2511)
	want := "*[System[EventRecordID>2511 and (Level=0 or Level=1 or Level=2) and (EventID=4625 or (EventID>=4720 and EventID<=4726))]]"
	if q != want {
		t.Fatalf("XPath :\n%s\nattendu :\n%s", q, want)
	}
	// Requête refusée par Windows : les filtres ne se jugent plus qu'ici.
	f.xpath = false
	if q := f.query(7); q != "*[System[EventRecordID>7]]" {
		t.Fatalf("sans XPath : %s", q)
	}
	// Trop d'identifiants pour une requête XPath : filtrés ici seulement.
	var many []string
	for i := 0; i < maxXPathIDs+1; i++ {
		many = append(many, "4"+strings.Repeat("0", 2)+string(rune('0'+i%10)))
	}
	if q := newEvtFilter(config.EventlogChannel{EventIDs: many}).query(0); strings.Contains(q, "EventID") {
		t.Fatalf("trop d'identifiants dans la requête : %s", q)
	}
	if q := newEvtFilter(config.EventlogChannel{Levels: []string{"information"}}).query(0); !strings.Contains(q, "(Level=0 or Level=4)") {
		t.Fatalf("informations : %s", q)
	}
}

func TestEventlogAttributes(t *testing.T) {
	l := eventToLog(mustEvent(t, evt4625), time.Now())
	ev := l.Attributes["eventlog"].(map[string]any)
	for k, want := range map[string]any{
		"channel": "Security", "event_id": "4625", "level": "0", "level_name": "audit_failure", "audit": "failure",
		"level_text": "Information", "task_text": "Logon", "keywords_text": "Échec de l’audit", "record_id": uint64(2512),
	} {
		if ev[k] != want {
			t.Errorf("%s = %#v, attendu %#v", k, ev[k], want)
		}
	}
	if l.Status != "warning" || l.Service != "Microsoft-Windows-Security-Auditing" || !strings.HasPrefix(l.Message, "Échec") {
		t.Fatalf("ligne : %+v", l)
	}
	if d := ev["data"].(map[string]string); d["TargetUserName"] != "nexistepas-v3w" || d["IpAddress"] != "127.0.0.1" {
		t.Fatalf("données : %v", d)
	}
	l = eventToLog(mustEvent(t, evtDNS), time.Now())
	ev = l.Attributes["eventlog"].(map[string]any)
	if ev["level_name"] != "warning" || ev["level_text"] != "Avertissement" || ev["audit"] != nil || ev["task_text"] != nil || l.Status != "warning" {
		t.Fatalf("DNS : %v %s", ev, l.Status)
	}
	// Service et étiquettes propres au canal.
	f := newEvtFilter(config.EventlogChannel{Channel: "Security", Service: "securite", Tags: map[string]string{"role": "dc"}})
	f.apply(&l)
	if l.Service != "securite" || l.Tags["role"] != "dc" {
		t.Fatalf("service, étiquettes : %+v", l)
	}
}

func TestAddChannels(t *testing.T) {
	list := []config.EventlogChannel{{Channel: "System"}, {Channel: "dns server", Levels: []string{"error"}}}
	out := addChannels(list, "Directory Service", "DNS Server", "DFS Replication")
	if got := strings.Join(config.EventlogNames(out), "|"); got != "System|dns server*|Directory Service|DFS Replication" {
		t.Fatalf("canaux : %s", got)
	}
	if len(list) != 2 {
		t.Fatal("liste d'origine modifiée")
	}
}
