package syslog

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/logs"
	"s33d.fr/hypha/agent/internal/model"
)

func TestParse5424(t *testing.T) {
	m := Parse(`<165>1 2026-11-20T03:04:05.123Z fw01.example.org evntslog 1234 ID47 [exampleSDID@32473 iut="3" eventSource="Appli\"cation" eventID="1011"][meta lang="fr"] ` + "\uFEFF" + `Message ascii`)
	if !m.HasPRI || m.Facility != 20 || m.Severity != 5 || m.Version != 1 || m.Hostname != "fw01.example.org" ||
		m.AppName != "evntslog" || m.ProcID != "1234" || m.MsgID != "ID47" || m.Text != "Message ascii" {
		t.Fatalf("%+v", m)
	}
	if m.SD["exampleSDID@32473"]["eventSource"] != `Appli"cation` || m.SD["meta"]["lang"] != "fr" {
		t.Fatalf("données structurées : %+v", m.SD)
	}
	if m := Parse(`<29>1 2026-11-20T03:26:42+01:00 relais-essai rsyslog-relais - - -  vague1 : relayé`); m.Text != "vague1 : relayé" {
		t.Fatalf("espace de tête (rsyslog) : %q", m.Text)
	}
	m = Parse(`<14>1 - - - - - -`)
	if m.Hostname != "" || m.Text != "" || m.Severity != 6 {
		t.Fatalf("valeurs nulles : %+v", m)
	}
}

func TestParse3164(t *testing.T) {
	for _, c := range []struct{ raw, host, app, pid, text string }{
		{`<34>Oct 11 22:14:15 mymachine su: 'su root' failed for lonvick on /dev/pts/8`, "mymachine", "su", "", "'su root' failed for lonvick on /dev/pts/8"},
		{`<86>Nov  5 03:00:00 web-01 sshd[4242]: Accepted publickey for root`, "web-01", "sshd", "4242", "Accepted publickey for root"},
		{`<13>2026-11-20T03:00:00.000+01:00 nas01 kernel: eth0 link up`, "nas01", "kernel", "", "eth0 link up"},
		{`<166>Nov 20 03:00:00.123 asa01 %ASA-6-302013: Built outbound TCP connection`, "asa01", "", "", "%ASA-6-302013: Built outbound TCP connection"},
		{`<189>date=2026-11-20 time=03:00:00 devname="FGT60F" logid="0000000013" type="traffic" action="deny"`, "", "", "", `date=2026-11-20 time=03:00:00 devname="FGT60F" logid="0000000013" type="traffic" action="deny"`},
		{`sans priorité du tout`, "", "", "", "sans priorité du tout"},
	} {
		m := Parse(c.raw)
		if m.Hostname != c.host || m.AppName != c.app || m.ProcID != c.pid || m.Text != c.text {
			t.Errorf("%q → hôte %q app %q pid %q texte %q", c.raw, m.Hostname, m.AppName, m.ProcID, m.Text)
		}
	}
	if m := Parse("sans priorité"); m.HasPRI || m.Facility != 1 || m.Severity != 5 {
		t.Fatalf("sans PRI : %+v", m)
	}
}

func TestBuild(t *testing.T) {
	l := Build(Parse(`<11>Nov 20 03:00:00 sw01 stp: port 3 blocking`), "", "192.0.2.7", time.Unix(100, 0), "syslog")
	if l.Status != "error" || l.Service != "stp" || l.Source != "syslog" || l.Tags["syslog_host"] != "sw01" || l.TS != 100000 {
		t.Fatalf("%+v", l)
	}
	sl := l.Attributes["syslog"].(map[string]any)
	if sl["facility"] != "user" || sl["hostname"] != "sw01" || sl["timestamp"] != "Nov 20 03:00:00" {
		t.Fatalf("attributs : %+v", sl)
	}
	if l.Attributes["network"].(map[string]any)["client"].(map[string]any)["ip"] != "192.0.2.7" {
		t.Fatal("adresse de l'émetteur")
	}
	l = Build(Parse(`<190>date=2026-11-20 devname=x`), "", "192.0.2.9", time.Unix(0, 0), "syslog")
	if l.Tags["syslog_host"] != "192.0.2.9" || l.Service != "syslog" || l.Status != "info" {
		t.Fatalf("sans nom d'hôte : %+v", l)
	}
}

type sink struct {
	mu   sync.Mutex
	logs []model.Log
	full bool
}

func (s *sink) submit(e logs.Entry) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.full {
		return false
	}
	s.logs = append(s.logs, e.Log)
	return true
}

func (s *sink) wait(t *testing.T, n int) []model.Log {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		if len(s.logs) >= n {
			out := append([]model.Log{}, s.logs...)
			s.mu.Unlock()
			return out
		}
		s.mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%d lignes attendues, %d reçues", n, len(s.logs))
	return nil
}

func tags(kv ...string) map[string]string {
	m := map[string]string{"host": "agent"}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}

func TestListenersEndToEnd(t *testing.T) {
	cfg := config.SyslogConfig{Enabled: true, UDP: "127.0.0.1:0", TCP: "127.0.0.1:0", MaxConnections: 4, RateLimit: 1000,
		Service: "syslog", Parsers: []config.SyslogParser{{Match: "127.0.0.0/8", Parser: "kv"}}, Tags: map[string]string{"site": "lab"}}
	k := &sink{}
	s := New(cfg, k.submit, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	var udp, tcp string
	for i := 0; i < 100 && (udp == "" || tcp == ""); i++ {
		for _, a := range s.Addrs() {
			if p, ok := strings.CutPrefix(a, "UDP "); ok {
				udp = p
			}
			if p, ok := strings.CutPrefix(a, "TCP "); ok {
				tcp = p
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	u, err := net.Dial("udp", udp)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(u, "<13>Nov 20 03:00:00 dev1 app: par UDP\n")
	u.Close()
	c, err := net.Dial("tcp", tcp)
	if err != nil {
		t.Fatal(err)
	}
	m1 := "<14>1 2026-11-20T03:00:00Z dev2 app - - - un\nmessage sur deux lignes"
	fmt.Fprintf(c, "%d %s", len(m1), m1)                          // octet-counting
	fmt.Fprint(c, "<12>Nov 20 03:00:01 dev3 app: fin de ligne\n") // terminée par LF
	got := k.wait(t, 3)
	c.Close()
	texts := map[string]model.Log{}
	for _, l := range got {
		texts[l.Message] = l
	}
	if l, ok := texts["un\nmessage sur deux lignes"]; !ok || l.Tags["syslog_host"] != "dev2" {
		t.Fatalf("octet-counting : %+v", got)
	}
	if l, ok := texts["fin de ligne"]; !ok || l.Status != "warning" || l.Tags["site"] != "lab" {
		t.Fatalf("LF : %+v", got)
	}
	if l := texts["par UDP"]; l.Attributes["syslog"].(map[string]any)["parser"] != "kv" {
		t.Fatalf("modèle d'analyse : %+v", l.Attributes)
	}
	series, checks := s.Flush(time.Now(), tags)
	counts := map[string]float64{}
	for _, x := range series {
		if x.Metric == "syslog.messages" {
			counts[x.Tags["syslog_host"]] = x.Points[0].Value
		}
	}
	if counts["dev1"] != 1 || counts["dev2"] != 1 || counts["dev3"] != 1 || checks[0].Status != model.CheckOK {
		t.Fatalf("mesures %v, contrôle %+v", counts, checks)
	}
	series, _ = s.Flush(time.Now(), tags)
	for _, x := range series {
		if x.Metric == "syslog.messages" && x.Points[0].Value != 0 {
			t.Fatalf("émetteur muet : %+v", x)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("arrêt bloqué")
	}
}

func TestLimitsAndRefusals(t *testing.T) {
	k := &sink{}
	s := New(config.SyslogConfig{RateLimit: 2, AllowedSources: []string{"192.0.2.0/24"}, Service: "syslog"}, k.submit, nil)
	now := time.Now()
	s.handle("<13>x: a", "198.51.100.1", now)
	for i := 0; i < 5; i++ {
		s.handle("<13>x: b", "192.0.2.1", now)
	}
	k.full = true
	s.handle("<13>x: c", "192.0.2.1", now.Add(time.Second))
	series, _ := s.Flush(now, tags)
	dropped := map[string]float64{}
	for _, x := range series {
		if x.Metric == "syslog.dropped" {
			dropped[x.Tags["reason"]] = x.Points[0].Value
		}
	}
	if len(k.logs) != 2 || dropped["source refusée"] != 1 || dropped["débit"] != 3 || dropped["file pleine"] != 1 {
		t.Fatalf("lignes %d, jetées %v", len(k.logs), dropped)
	}
}

func TestSyslogValidation(t *testing.T) {
	p := filepath.Join(t.TempDir(), "hypha.yaml")
	os.WriteFile(p, []byte("api_key: hph_"+strings.Repeat("a", 40)+"\nsyslog:\n  enabled: true\n  udp: 0.0.0.0:80\n  tcp: \"\"\n  parsers: [{match: \"x[\", parser: \"Fortinet!\"}]\n"), 0o600)
	c, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	errs := strings.Join(c.Validate(true), "\n")
	for _, want := range []string{"port 80 est interdit", "parser \"Fortinet!\" invalide", "match \"x[\""} {
		if !strings.Contains(errs, want) {
			t.Errorf("%q absent de :\n%s", want, errs)
		}
	}
	if d := config.Default().Syslog; d.Enabled {
		t.Fatal("syslog doit être coupé par défaut")
	}
}
