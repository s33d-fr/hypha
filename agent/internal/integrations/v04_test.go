package integrations

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

func near(a, b float64) bool { d := a - b; return d < 1e-12 && d > -1e-12 }

func TestApacheStatus(t *testing.T) {
	s, err := parseApacheStatus("ServerVersion: Apache/2.4.62\nTotal Accesses: 1200\nTotal kBytes: 512\nBusyWorkers: 3\nIdleWorkers: 47\nConnsTotal: 5\nScoreboard: __W_\n")
	if err != nil || s.accesses != 1200 || s.kbytes != 512 || s.busy != 3 || s.idle != 47 || s.conns != 5 {
		t.Fatalf("mod_status : %+v %v", s, err)
	}
	if _, err := parseApacheStatus("<html>Not Found</html>"); err == nil {
		t.Fatal("page quelconque acceptée")
	}
}

func TestBindJSON(t *testing.T) {
	data := []byte(`{"json-stats-version":"1.8","qtypes":{"A":100,"AAAA":40},"rcodes":{"NOERROR":120,"NXDOMAIN":15,"SERVFAIL":2},
	 "nsstats":{"Requestv4":140,"RecursClients":3},
	 "views":{"_default":{"zones":[{"name":"s33d.fr","class":"IN","serial":2026111801,"type":"primary"},{"name":"0.in-addr.arpa","serial":-1}]},
	          "_bind":{"zones":[{"name":"authors.bind","serial":0}]}}}`)
	s, err := parseBindJSON(data)
	if err != nil || s.qtypes["A"] != 100 || s.nxdomain != 15 || s.servfl != 2 || s.recursive != 3 || len(s.zones) != 1 || s.zones[0].serial != 2026111801 {
		t.Fatalf("bind : %+v %v", s, err)
	}
}

func TestPostfix(t *testing.T) {
	data := []byte(`{"queue_name": "deferred", "queue_id": "4Xb1", "arrival_time": 1, "message_size": 100, "recipients": []}
{"queue_name": "deferred", "queue_id": "4Xb2"}
{"queue_name": "active", "queue_id": "4Xb3"}
`)
	c, err := countPostqueueJSON(data)
	if err != nil || c["deferred"] != 2 || c["active"] != 1 || c["hold"] != 0 {
		t.Fatalf("postqueue -j : %v %v", c, err)
	}
	p := newPostfix(config.IntegrationToggle{Enabled: "auto"})
	p.on.Store(true)
	p.observe(&model.Log{Source: "journald", Service: "postfix/smtp", Message: "4Xb1: to=<a@b.fr>, relay=mx[1.2.3.4]:25, delay=1, status=sent (250 OK)"})
	p.observe(&model.Log{Source: "journald", Service: "postfix/smtp", Message: "4Xb2: to=<c@d.fr>, status=deferred (connect timed out)"})
	p.observe(&model.Log{Source: "file", Service: "mail", Message: "Nov 18 10:00:00 mx postfix/smtp[12]: 4Xb3: to=<e@f.fr>, status=bounced (unknown user)"})
	p.observe(&model.Log{Source: "journald", Service: "dovecot", Message: "status=sent"})
	if p.sent.Load() != 1 || p.defd.Load() != 1 || p.bnc.Load() != 1 {
		t.Fatalf("remises : %d %d %d", p.sent.Load(), p.defd.Load(), p.bnc.Load())
	}
}

func TestDovecot(t *testing.T) {
	v23 := "metric_name\tfield\tcount\tsum\tmin\tmax\tavg\nauth_success\tduration\t120\t5\t0\t1\t0\nauth_failures\tduration\t7\t1\t0\t1\t0\n"
	ok, fail, conns, found := parseDoveadmStats(v23)
	if !found || ok != 120 || fail != 7 || conns != -1 {
		t.Fatalf("2.3 : %d %d %d %v", ok, fail, conns, found)
	}
	v22 := "reset_timestamp\tlast_update\tnum_logins\tnum_connected_sessions\tauth_successes\tauth_failures\n1731800000\t1731897600.1\t500\t12\t480\t20\n"
	ok, fail, conns, found = parseDoveadmStats(v22)
	if !found || ok != 480 || fail != 20 || conns != 12 {
		t.Fatalf("2.2 : %d %d %d %v", ok, fail, conns, found)
	}
	if n := parseDoveadmWho("username  # proto (pids) (ips)\nalice     3 imap  (1 2 3) (203.0.113.2)\nbob       1 imap  (4) (203.0.113.3)\n"); n != 4 {
		t.Fatalf("who : %d", n)
	}
}

func TestOpenVPN(t *testing.T) {
	v2 := `TITLE,OpenVPN 2.6.12
TIME,2026-11-18 10:00:00,1731920400
HEADER,CLIENT_LIST,Common Name,Real Address,Virtual Address,Virtual IPv6 Address,Bytes Received,Bytes Sent,Connected Since,Connected Since (time_t),Username,Client ID,Peer ID,Data Channel Cipher
CLIENT_LIST,alice-portable,198.51.100.7:51820,192.0.2.82,,123456,654321,2026-11-18 09:00:00,1731916800,UNDEF,0,0,AES-256-GCM
END
`
	c, err := parseOpenVPNStatus(v2)
	if err != nil || len(c) != 1 || c[0].name != "alice-portable" || c[0].in != 123456 || c[0].out != 654321 {
		t.Fatalf("v2 : %+v %v", c, err)
	}
	v3 := strings.ReplaceAll(v2, ",", "\t")
	if c, err := parseOpenVPNStatus(v3); err != nil || len(c) != 1 || c[0].out != 654321 {
		t.Fatalf("v3 : %+v %v", c, err)
	}
	old := "TITLE,OpenVPN 2.4\nHEADER,CLIENT_LIST,Common Name,Real Address,Virtual Address,Bytes Received,Bytes Sent,Connected Since,Connected Since (time_t),Username\nCLIENT_LIST,bob,1.2.3.4:1194,192.0.2.86,100,200,Wed Nov 18 09:00:00 2026,1731916800,UNDEF\n"
	if c, err := parseOpenVPNStatus(old); err != nil || len(c) != 1 || c[0].in != 100 || c[0].out != 200 {
		t.Fatalf("2.4 : %+v %v", c, err)
	}
	v1 := "OpenVPN CLIENT LIST\nUpdated,Wed Nov 18 10:00:00 2026\nCommon Name,Real Address,Bytes Received,Bytes Sent,Connected Since\nalice,5.6.7.8:1194,10,20,Wed Nov 18 09:00:00 2026\nROUTING TABLE\nVirtual Address,Common Name,Real Address,Last Ref\n10.8.0.10,alice,5.6.7.8:1194,Wed Nov 18 09:59:00 2026\nGLOBAL STATS\nEND\n"
	if c, err := parseOpenVPNStatus(v1); err != nil || len(c) != 1 || c[0].name != "alice" || c[0].out != 20 {
		t.Fatalf("v1 : %+v %v", c, err)
	}
	cs := "OpenVPN STATISTICS\nUpdated,2026-11-18 07:06:31\nTUN/TAP read bytes,2913794\nTUN/TAP write bytes,1364832\nTCP/UDP read bytes,4070544\nTCP/UDP write bytes,2174024\nAuth read bytes,0\nEND\n"
	if c, ok := parseOpenVPNClientStats(cs); !ok || c.in != 4070544 || c.out != 2174024 {
		t.Fatalf("tunnel client : %+v %v", c, ok)
	}
	if _, ok := parseOpenVPNClientStats(v2); ok {
		t.Fatal("statut de serveur lu comme un tunnel client")
	}
	if _, err := parseOpenVPNStatus("bonjour\n"); err == nil {
		t.Fatal("fichier quelconque accepté")
	}
	if vpnInstance("/run/openvpn-server/status-server.log") != "server" || vpnInstance("/var/log/openvpn/openvpn-status.log") != "openvpn" {
		t.Fatal(vpnInstance("/run/openvpn-server/status-server.log"), vpnInstance("/var/log/openvpn/openvpn-status.log"))
	}
}

func TestFail2ban(t *testing.T) {
	jails := parseF2BJails("Status\n|- Number of jail:\t2\n`- Jail list:\tsshd, recidive\n")
	if len(jails) != 2 || jails[1] != "recidive" {
		t.Fatalf("prisons : %v", jails)
	}
	j, ok := parseF2BJail("Status for the jail: sshd\n|- Filter\n|  |- Currently failed:\t3\n|  |- Total failed:\t120\n`- Actions\n   |- Currently banned:\t1\n   |- Total banned:\t15\n")
	if !ok || j.failed != 3 || j.banned != 1 {
		t.Fatalf("prison : %+v", j)
	}
}

func writeCert(t *testing.T, path string, notAfter time.Time) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "hypha.s33d.fr"},
		NotBefore: notAfter.Add(-90 * 24 * time.Hour), NotAfter: notAfter}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Dir(path), 0o700)
	os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
}

func TestX509(t *testing.T) {
	dir := t.TempDir()
	writeCert(t, filepath.Join(dir, "a", "cert.pem"), time.Now().Add(60*24*time.Hour))
	writeCert(t, filepath.Join(dir, "b", "cert.pem"), time.Now().Add(10*24*time.Hour))
	writeCert(t, filepath.Join(dir, "c", "cert.pem"), time.Now().Add(-24*time.Hour))
	os.MkdirAll(filepath.Join(dir, "d"), 0o700)
	os.WriteFile(filepath.Join(dir, "d", "cert.pem"), []byte("pas un certificat"), 0o600)
	off := false
	x := newX509(config.X509Config{Enabled: "auto", Paths: []string{filepath.Join(dir, "*", "cert.pem")}, Discover: &off})
	b := model.NewSeriesBuilder(0, testTags)
	checks, st := x.collect(nil, time.Now(), b, testTags)
	byPath := map[string]int{}
	for _, c := range checks {
		if c.Name == "tls.cert.expiry" {
			byPath[filepath.Base(filepath.Dir(c.Tags["cert_path"]))] = c.Status
		}
	}
	if byPath["a"] != model.CheckOK || byPath["b"] != model.CheckWarn || byPath["c"] != model.CheckCrit || byPath["d"] != model.CheckUnknown {
		t.Fatalf("échéances : %v", byPath)
	}
	if !strings.HasPrefix(st, "partiel") || len(b.Series()) != 3 {
		t.Fatalf("état %q, %d séries", st, len(b.Series()))
	}
}

// Un certificat renouvelé efface son alerte au plus tard à la relecture suivante (certRescan).
func TestX509RenewedCertSeenAtNextRescan(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "cert.pem")
	writeCert(t, path, time.Now().Add(5*24*time.Hour))
	off := false
	x := newX509(config.X509Config{Enabled: "auto", Paths: []string{path}, Discover: &off})
	status := func(at time.Time) int {
		checks, _ := x.collect(nil, at, model.NewSeriesBuilder(0, testTags), testTags)
		for _, c := range checks {
			if c.Name == "tls.cert.expiry" {
				return c.Status
			}
		}
		return -1
	}
	t0 := time.Now()
	if s := status(t0); s != model.CheckCrit {
		t.Fatalf("avant renouvellement : %d", s)
	}
	writeCert(t, path, time.Now().Add(90*24*time.Hour))
	if s := status(t0.Add(certRescan - time.Second)); s != model.CheckCrit {
		t.Fatalf("avant la relecture, le relevé précédent sert : %d", s)
	}
	if s := status(t0.Add(certRescan)); s != model.CheckOK {
		t.Fatalf("après la relecture : %d", s)
	}
}

func TestSmartctl(t *testing.T) {
	ata := `{"device":{"name":"/dev/sda"},"smart_status":{"passed":true},"temperature":{"current":34},"power_on_time":{"hours":21000},
	 "ata_smart_attributes":{"table":[{"id":5,"value":100,"raw":{"value":8}},{"id":177,"value":93,"raw":{"value":120}}]},"smartctl":{"exit_status":0}}`
	d, ok := parseSmartctl([]byte(ata))
	if !ok || !*d.passed || d.temp != 34 || d.realloc != 8 || d.hours != 21000 || d.wear != 7 || d.device != "sda" {
		t.Fatalf("ATA : %+v", d)
	}
	nvme := `{"device":{"name":"/dev/nvme0"},"smart_status":{"passed":false},"nvme_smart_health_information_log":{"percentage_used":12,"temperature":41,"power_on_hours":900},"smartctl":{"exit_status":8}}`
	d, ok = parseSmartctl([]byte(nvme))
	if !ok || *d.passed || d.wear != 12 || d.temp != 41 || d.hours != 900 {
		t.Fatalf("NVMe : %+v", d)
	}
	if d, ok := parseSmartctl([]byte(`{"device":{"name":"/dev/sdb"},"smartctl":{"exit_status":2}}`)); !ok || !d.standby {
		t.Fatalf("veille : %+v", d)
	}
	if _, ok := parseSmartctl([]byte(`{"device":{"name":"/dev/vda"},"smartctl":{"exit_status":4}}`)); ok {
		t.Fatal("disque virtuel sans SMART accepté")
	}
	if devs := parseSmartScan([]byte(`{"devices":[{"name":"/dev/sda","type":"sat"},{"name":"/dev/nvme0","type":"nvme"}]}`)); len(devs) != 2 || devs[0].typ != "sat" {
		t.Fatalf("scan : %+v", devs)
	}
}

func TestHwmon(t *testing.T) {
	root := t.TempDir()
	w := func(p, v string) {
		os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o700)
		os.WriteFile(filepath.Join(root, p), []byte(v+"\n"), 0o600)
	}
	w("hwmon0/name", "coretemp")
	w("hwmon0/temp1_input", "45000")
	w("hwmon0/temp1_label", "Package id 0")
	w("hwmon0/temp2_input", "-273000")
	w("hwmon1/name", "nct6775")
	w("hwmon1/fan1_input", "1200")
	r := readHwmon(root)
	if len(r) != 2 || r[0].sensor != "coretemp/Package id 0" || r[0].value != 45 || r[1].kind != "fan" || r[1].value != 1200 {
		t.Fatalf("capteurs : %+v", r)
	}
}

func TestNTP(t *testing.T) {
	c, err := parseChronyTracking("Reference ID    : C00002F5 (192.0.2.245)\nStratum         : 3\nSystem time     : 0.000012345 seconds slow of NTP time\nLeap status     : Normal\n")
	if err != nil || !c.synced || c.offset != -0.000012345 {
		t.Fatalf("chrony : %+v %v", c, err)
	}
	c, _ = parseChronyTracking("Reference ID    : 00000000 ()\nSystem time     : 0.000000000 seconds fast of NTP time\nLeap status     : Not synchronised\n")
	if c.synced {
		t.Fatal("chrony sans source vu synchronisé")
	}
	c, err = parseTimesyncStatus("       Server: 192.0.2.245 (ntp.example.org)\nPoll interval: 34min 8s (min: 32s; max 34min 8s)\n         Leap: normal\n       Offset: +1.234ms\n")
	if err != nil || !near(c.offset, 0.001234) || !strings.Contains(c.source, "ntp.example.org") {
		t.Fatalf("timesyncd : %+v %v", c, err)
	}
	c, err = parseNtpqRV("associd=0 status=0615 leap_none, sync_ntp, 1 event, clock_sync,\nversion=\"ntpd 4.2.8p15\", leap=00, stratum=3, refid=192.0.2.245,\noffset=-2.345, sys_jitter=0.5\n")
	if err != nil || !c.synced || !near(c.offset, -0.002345) {
		t.Fatalf("ntpq : %+v %v", c, err)
	}
	if s, ok := parseW32tmStatus("Indicateur de saut : 3(pas de synchronisation)\nStrate : 0\n"); !ok || s {
		t.Fatal("w32tm non synchronisé (texte français)")
	}
	if s, ok := parseW32tmStatus("Leap Indicator: 0(no warning)\nStratum: 4\n"); !ok || !s {
		t.Fatal("w32tm synchronisé")
	}
	if v, ok := parseW32tmStripchart("Tracking ntp.example.org [192.0.2.245:123].\nCollecting 1 samples.\n10:00:00, +00.0012345s\n"); !ok || v != 0.0012345 {
		t.Fatalf("stripchart : %v %v", v, ok)
	}
}

func TestVirtualBox(t *testing.T) {
	reg := `<?xml version="1.0"?><VirtualBox><Global><MachineRegistry>
	<MachineEntry uuid="{0B5B4A1C-1111-2222-3333-444455556666}" src="/home/alice/VirtualBox VMs/VM-01/VM-01.vbox"/>
	<MachineEntry uuid="{aaaaaaaa-1111-2222-3333-444455556666}" src="lab/lab.vbox"/>
	</MachineRegistry></Global></VirtualBox>`
	e, err := parseVBoxRegistry([]byte(reg))
	if err != nil || len(e) != 2 || e[0][0] != "0b5b4a1c-1111-2222-3333-444455556666" || e[1][1] != "lab/lab.vbox" {
		t.Fatalf("VirtualBox.xml : %v %v", e, err)
	}
	name, auto, err := parseVBoxMachine([]byte(`<VirtualBox><Machine uuid="{x}" name="VM-01"><Autostart enabled="true" delay="0"/></Machine></VirtualBox>`))
	if err != nil || name != "VM-01" || !auto {
		t.Fatalf(".vbox : %q %v %v", name, auto, err)
	}
	if v := startvmArg([]string{"/usr/lib/virtualbox/VBoxHeadless", "--comment", "VM-01", "--startvm", "0b5b4a1c-1111-2222-3333-444455556666", "--vrde", "config"}); v != "0b5b4a1c-1111-2222-3333-444455556666" {
		t.Fatal(v)
	}
}

func TestSystemdTimers(t *testing.T) {
	if d := parseSystemdSpan("1h 30min"); d != 90*time.Minute {
		t.Fatal(d)
	}
	if v := lastTrigger("@1794957240", "", 0); v != 1794957240*1_000_000 {
		t.Fatal(v)
	}
	if v := lastTrigger("Wed 2026-11-18 00:14:00 CET", "22h 43min 13.219319s", 1000); v != 1000*1_000_000+(22*3600+43*60+13)*1_000_000+219319 {
		t.Fatalf("durée monotone : %d", v)
	}
	if v := lastTrigger("n/a", "0", 1000); v != 0 {
		t.Fatal("jamais déclenché")
	}
	if d := monotonicPeriod("{ OnUnitActiveUSec=1d ; next_elapse=Thu 2026-11-19 06:12:01 CET }"); d != 24*time.Hour {
		t.Fatal(d)
	}
	if s := calendarSpec("{ OnCalendar=*-*-* 06,18:00:00 ; next_elapse=Wed 2026-11-18 18:00:00 CET }"); s != "*-*-* 06,18:00:00" {
		t.Fatal(s)
	}
	out := "  Original form: daily\nNormalized form: *-*-* 00:00:00\n    Next elapse: Thu 2026-11-19 00:00:00 CET\n       (in UTC): Wed 2026-11-18 23:00:00 UTC\n" +
		"       From now: 5h 10min left\n   Iteration #2: Fri 2026-11-20 00:00:00 CET\n       (in UTC): Thu 2026-11-19 23:00:00 UTC\n       From now: 1 day 5h left\n"
	if p := calendarPeriodOf(out); p != 24*time.Hour {
		t.Fatalf("période calendaire : %s", p)
	}
	if p := calendarPeriodOf("    Next elapse: Wed 2026-11-18 07:10:00 CET\n        Iter. #2: Wed 2026-11-18 07:20:00 CET\n"); p != 10*time.Minute {
		t.Fatalf("période calendaire (systemd ancien) : %s", p)
	}
	now := time.Now()
	if st, _ := timerCheck(timerProps{unit: "x.service", unitResult: "exit-code"}, time.Hour, now); st != model.CheckCrit {
		t.Fatal("passage en échec non vu")
	}
	if st, _ := timerCheck(timerProps{unit: "x.service", unitResult: "success", lastTrigger: now.Add(-3 * time.Hour).UnixMicro()}, time.Hour, now); st != model.CheckWarn {
		t.Fatal("timer en retard non vu")
	}
	if st, _ := timerCheck(timerProps{unit: "x.service", unitResult: "success", lastTrigger: now.Add(-30 * time.Minute).UnixMicro()}, time.Hour, now); st != model.CheckOK {
		t.Fatal("timer à l'heure en alerte")
	}
}

// anacron.timer (OnCalendar=*-*-* 07..23:30:00) : muet la nuit sans être en retard.
func TestTimerCalendarGap(t *testing.T) {
	out := "  Original form: *-*-* 07..23:30:00\nNormalized form: *-*-* 07..23:30:00\n" +
		"    Next elapse: Wed 2026-11-18 07:30:00 CET\n       (in UTC): Wed 2026-11-18 06:30:00 UTC\n" +
		"   Iteration #2: Wed 2026-11-18 08:30:00 CET\n       (in UTC): Wed 2026-11-18 07:30:00 UTC\n"
	due := secondElapseUTC(out)
	if !due.Equal(time.Date(2026, 11, 18, 7, 30, 0, 0, time.UTC)) {
		t.Fatalf("deuxième passage : %v", due)
	}
	tp := timerProps{unit: "anacron.service", unitResult: "success",
		lastTrigger: time.Date(2026, 11, 17, 22, 30, 0, 0, time.UTC).UnixMicro()}
	if st, msg := timerCheckDue(tp, due, time.Date(2026, 11, 18, 6, 28, 0, 0, time.UTC)); st != model.CheckOK {
		t.Fatalf("anacron vu en retard le matin : %s", msg)
	}
	if st, _ := timerCheckDue(tp, due, time.Date(2026, 11, 18, 7, 45, 0, 0, time.UTC)); st != model.CheckWarn {
		t.Fatal("deux passages manqués non signalés")
	}
	if !secondElapseUTC("Next elapse: never\n").IsZero() {
		t.Fatal("sortie sans passage")
	}
	if calendarFallback(time.Hour) != 12*time.Hour || calendarFallback(24*time.Hour) != 24*time.Hour || calendarFallback(0) != 0 {
		t.Fatal("repli de période")
	}
}

// ntpsec : « sync_unspec » et « no_sys_peer » n'empêchent pas une horloge synchronisée.
func TestNtpqNtpsec(t *testing.T) {
	rv := "associd=0 status=0018 leap_none, sync_unspec, 1 event, no_sys_peer,\nleap=00, stratum=2, precision=-24,\n" +
		"refid=203.0.113.123,\noffset=0.096853, frequency=-20.197,\n"
	c, err := parseNtpqRV(rv)
	if err != nil || !c.synced || c.source != "203.0.113.123" {
		t.Fatalf("ntpsec synchronisé vu autrement : %+v %v", c, err)
	}
	if c, _ := parseNtpqRV(strings.Replace(rv, "leap=00", "leap=11", 1)); c.synced {
		t.Fatal("saut en alarme vu synchronisé")
	}
	if c, _ := parseNtpqRV(strings.Replace(rv, "stratum=2", "stratum=16", 1)); c.synced {
		t.Fatal("strate 16 vue synchronisée")
	}
	if c, _ := parseNtpqRV("associd=0 status=c016 leap_alarm, sync_unspec,\noffset=0.1,\n"); c.synced {
		t.Fatal("leap_alarm sans champ leap vu synchronisé")
	}
}

func TestDBMDeltas(t *testing.T) {
	prev := map[string]dbmRow{
		"1|app|10|true": {queryID: "1", db: "app", calls: 100, totalMS: 1000, rows: 100},
		"2|app|10|true": {queryID: "2", db: "app", calls: 50, totalMS: 9000},
		"3|app|10|true": {queryID: "3", db: "app", calls: 10, totalMS: 10},
	}
	cur := map[string]dbmRow{
		"1|app|10|true":  {queryID: "1", db: "app", calls: 110, totalMS: 1500, rows: 120},
		"1|app|11|true":  {queryID: "1", db: "app", calls: 5, totalMS: 100}, // autre identité, nouvelle
		"2|app|10|true":  {queryID: "2", db: "app", calls: 50, totalMS: 9000}, // aucun appel
		"3|app|10|true":  {queryID: "3", db: "app", calls: 2, totalMS: 1},     // remise à zéro
		"4|crm|10|false": {queryID: "4", db: "crm", calls: 1, totalMS: 2000},
	}
	got := dbmDeltas(cur, prev, 50)
	if len(got) != 2 || got[0].QueryID != "4" || got[1].QueryID != "1" || got[1].Calls != 15 || got[1].TotalMS != 600 || got[1].Rows != 20 {
		t.Fatalf("écarts : %+v", got)
	}
	if one := dbmDeltas(cur, prev, 1); len(one) != 1 || one[0].QueryID != "4" {
		t.Fatalf("plafond : %+v", one)
	}
	if !versionAtLeast("1.10", 1, 9) || versionAtLeast("1.7", 1, 8) {
		t.Fatal("versions de pg_stat_statements")
	}
}

func TestDetector(t *testing.T) {
	d := detector{mode: "auto"}
	now := time.Now()
	if skip, _ := d.skip(now); skip {
		t.Fatal("premier essai sauté")
	}
	if silent, st := d.failed("x"); !silent || st != "absent" {
		t.Fatal("logiciel jamais vu signalé en erreur")
	}
	if skip, st := d.skip(now.Add(time.Minute)); !skip || st != "absent" {
		t.Fatal("détection retentée avant 10 minutes")
	}
	if skip, _ := d.skip(now.Add(11 * time.Minute)); skip {
		t.Fatal("détection non retentée après 10 minutes")
	}
	d.ok()
	if silent, st := d.failed("panne"); silent || st != "erreur : panne" {
		t.Fatal("panne d'un logiciel vu passée sous silence")
	}
	off := detector{mode: "false"}
	if skip, st := off.skip(now); !skip || st != "disabled" {
		t.Fatal("intégration coupée lancée")
	}
}

func TestPostgresActivity(t *testing.T) {
	b := model.NewSeriesBuilder(0, testTags)
	pgSeries(b, &pgSnapshot{connByState: map[string]int64{}, locks: map[string]int64{"AccessShareLock": 4},
		activity: &pgActivity{waiting: 2, longest: 31.5, idleInTx: 1}}, nil, 0)
	got := index(b.Series())
	expectValue(t, got, "postgresql.waiting_queries{}", 2)
	expectValue(t, got, "postgresql.longest_query_s{}", 31.5)
	expectValue(t, got, "postgresql.idle_in_transaction{}", 1)
	expectValue(t, got, "postgresql.locks{lock_mode=AccessShareLock}", 4)
}
