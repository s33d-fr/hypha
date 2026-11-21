package integrations

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// Vague 8 : chaque faux serveur rend les réponses relevées sur le vrai logiciel, en conteneur dans un
// environnement d'essai (testdata/vague8, adresses remplacées par des adresses de documentation).

func v8File(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "vague8", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// v8Series : valeur de la première série qui porte ce nom et ces étiquettes.
func v8Series(t *testing.T, series []model.Series, metric string, kv ...string) (float64, bool) {
	t.Helper()
	for _, s := range series {
		if s.Metric != metric {
			continue
		}
		ok := true
		for i := 0; i+1 < len(kv); i += 2 {
			if s.Tags[kv[i]] != kv[i+1] {
				ok = false
			}
		}
		if ok && len(s.Points) > 0 {
			return s.Points[len(s.Points)-1].Value, true
		}
	}
	return 0, false
}

func v8Expect(t *testing.T, series []model.Series, metric string, want float64, kv ...string) {
	t.Helper()
	got, ok := v8Series(t, series, metric, kv...)
	switch {
	case !ok:
		t.Errorf("%s %v absente", metric, kv)
	case want >= 0 && got != want:
		t.Errorf("%s %v = %v, %v attendu", metric, kv, got, want)
	}
}

func v8Collect(t *testing.T, it integration, now time.Time) ([]model.Check, string, []model.Series) {
	t.Helper()
	b := model.NewSeriesBuilder(0, testTags)
	checks, state := it.collect(context.Background(), now, b, testTags)
	return checks, state, b.Series()
}

func v8Check(t *testing.T, checks []model.Check, name string, want int, kv ...string) model.Check {
	t.Helper()
	for _, c := range checks {
		if c.Name != name {
			continue
		}
		ok := true
		for i := 0; i+1 < len(kv); i += 2 {
			if c.Tags[kv[i]] != kv[i+1] {
				ok = false
			}
		}
		if !ok {
			continue
		}
		if c.Status != want {
			t.Errorf("%s %v : %v (%s), %v attendu", name, kv, c.Status, c.Message, want)
		}
		return c
	}
	t.Errorf("contrôle %s %v absent", name, kv)
	return model.Check{}
}

// ── Pi-hole 6.6 ──────────────────────────────────────────────────────────────────────────────

func fakePiHole(t *testing.T, password string, blocking *string) (*httptest.Server, *int) {
	const sid = "sid-essai"
	deleted := 0
	files := map[string]string{"/api/stats/summary": "pihole-6.6-summary.json", "/api/info/version": "pihole-6.6-version.json",
		"/api/info/messages": "pihole-6.6-messages.json"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth" {
			switch r.Method {
			case http.MethodPost:
				var body struct{ Password string }
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body.Password != password {
					w.WriteHeader(http.StatusUnauthorized)
					fmt.Fprint(w, `{"session":{"valid":false,"totp":false,"sid":null,"validity":-1,"message":"password incorrect"}}`)
					return
				}
				fmt.Fprintf(w, `{"session":{"valid":true,"totp":false,"sid":%q,"csrf":"x","validity":1800,"message":"password correct"}}`, sid)
			case http.MethodDelete:
				if r.Header.Get("X-FTL-SID") == sid {
					deleted++
				}
				w.WriteHeader(http.StatusNoContent)
			}
			return
		}
		if r.Header.Get("X-FTL-SID") != sid {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":{"key":"unauthorized","message":"Unauthorized","hint":null}}`)
			return
		}
		if r.URL.Path == "/api/dns/blocking" {
			fmt.Fprintf(w, `{"blocking":%q,"timer":%s,"took":0.0001}`, *blocking, map[bool]string{true: "250", false: "null"}[*blocking == "disabled"])
			return
		}
		if f, ok := files[r.URL.Path]; ok {
			w.Write(v8File(t, f))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &deleted
}

func TestPiHoleOnRealResponses(t *testing.T) {
	blocking := "enabled"
	srv, deleted := fakePiHole(t, "mdp-appli", &blocking)
	p := newPiHole(config.PiHoleConfig{Enabled: "auto", URL: srv.URL, Password: "mdp-appli"})
	checks, state, series := v8Collect(t, p, time.Now())
	if state != "ok" {
		t.Fatalf("état %q", state)
	}
	v8Expect(t, series, "pihole.queries.total", 10)
	v8Expect(t, series, "pihole.queries.blocked", 3)
	v8Expect(t, series, "pihole.queries.percent_blocked", 30)
	v8Expect(t, series, "pihole.queries.by_status", 3, "status", "gravity")
	v8Expect(t, series, "pihole.queries.by_reply", 3, "reply", "nxdomain")
	v8Expect(t, series, "pihole.clients.active", 2)
	v8Expect(t, series, "pihole.gravity.domains_blocked", 76105)
	v8Expect(t, series, "pihole.gravity.age", -1)
	v8Expect(t, series, "pihole.blocking.enabled", 1)
	v8Expect(t, series, "pihole.messages", 0)
	v8Expect(t, series, "pihole.updates_available", 0)
	c := v8Check(t, checks, "pihole.can_connect", model.CheckOK)
	if !strings.Contains(c.Message, "core v6.6") || !strings.Contains(c.Message, "FTL v6.9") {
		t.Errorf("message : %s", c.Message)
	}
	v8Check(t, checks, "pihole.blocking", model.CheckOK)
	// Blocage coupé pour 250 s : avertissement ; la session est réutilisée (aucune nouvelle connexion).
	blocking = "disabled"
	checks, _, series = v8Collect(t, p, time.Now().Add(time.Minute))
	if c := v8Check(t, checks, "pihole.blocking", model.CheckWarn); !strings.Contains(c.Message, "250") {
		t.Errorf("message : %s", c.Message)
	}
	v8Expect(t, series, "pihole.blocking.enabled", 0)
	p.close()
	if *deleted != 1 {
		t.Errorf("session non fermée à l'arrêt")
	}
	// Mot de passe refusé : contrôle critique (intention posée).
	bad := newPiHole(config.PiHoleConfig{Enabled: "auto", URL: srv.URL, Password: "faux"})
	checks, state, _ = v8Collect(t, bad, time.Now())
	if c := v8Check(t, checks, "pihole.can_connect", model.CheckCrit); !strings.Contains(c.Message, "mot de passe refusé") {
		t.Errorf("message : %s (%s)", c.Message, state)
	}
}

// ── AdGuard Home 1.0.0 ───────────────────────────────────────────────────────────────────────

func TestAdGuardOnRealResponses(t *testing.T) {
	files := map[string]string{"/control/status": "adguard-1.0.0-status.json", "/control/stats": "adguard-1.0.0-stats.json",
		"/control/filtering/status": "adguard-1.0.0-filtering.json"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "hypha" || p != "mdp" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodGet {
			t.Errorf("méthode %s sur %s", r.Method, r.URL.Path)
		}
		if f, ok := files[r.URL.Path]; ok {
			w.Write(v8File(t, f))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	a := newAdGuard(config.AdGuardConfig{Enabled: "auto", URL: srv.URL, Username: "hypha", Password: "mdp"})
	checks, state, series := v8Collect(t, a, time.Now())
	if state != "ok" {
		t.Fatalf("état %q", state)
	}
	v8Expect(t, series, "adguard.queries", 8)
	v8Expect(t, series, "adguard.queries.blocked", 2)
	v8Expect(t, series, "adguard.queries.percent_blocked", 25)
	v8Expect(t, series, "adguard.processing_time", 69.4)
	v8Expect(t, series, "adguard.upstream.avg_time", 110.2, "upstream", "192.0.2.53:53")
	v8Expect(t, series, "adguard.upstream.responses", 5, "upstream", "192.0.2.53:53")
	v8Expect(t, series, "adguard.filter.rules", 192147, "filter", "AdGuard DNS filter")
	v8Expect(t, series, "adguard.filters.enabled", 1)
	v8Expect(t, series, "adguard.filters.oldest_update_age", -1)
	v8Expect(t, series, "adguard.protection_enabled", 1)
	v8Check(t, checks, "adguard.protection", model.CheckOK)
	if c := v8Check(t, checks, "adguard.can_connect", model.CheckOK); !strings.Contains(c.Message, "v1.0.0") {
		t.Errorf("message : %s", c.Message)
	}
	// Sans compte en mode auto : absent, aucun appel.
	if _, st, _ := v8Collect(t, newAdGuard(config.AdGuardConfig{Enabled: "auto", URL: srv.URL}), time.Now()); st != "absent" {
		t.Errorf("sans compte : %q", st)
	}
	checks, _, _ = v8Collect(t, newAdGuard(config.AdGuardConfig{Enabled: "true", URL: srv.URL, Username: "hypha", Password: "x"}), time.Now())
	if c := v8Check(t, checks, "adguard.can_connect", model.CheckCrit); !strings.Contains(c.Message, "401") {
		t.Errorf("message : %s", c.Message)
	}
}

// ── Unbound 1.26.2 ───────────────────────────────────────────────────────────────────────────

func TestUnboundStats(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("script de commande")
	}
	dir := t.TempDir()
	stats := filepath.Join(dir, "stats.txt")
	os.WriteFile(stats, v8File(t, "unbound-1.26.2-stats.txt"), 0o644)
	script := filepath.Join(dir, "unbound-control")
	os.WriteFile(script, []byte("#!/bin/sh\n[ \"$1\" = \"-c\" ] && [ \"$3\" = stats_noreset ] && exec cat "+stats+"\necho \"error: mauvais arguments $*\"; exit 1\n"), 0o755)
	u := newUnbound(config.UnboundConfig{Enabled: "auto", Command: config.CommandLine{script}, ConfigFile: "/etc/unbound/unbound.conf",
		Timeout: config.Duration(5 * time.Second)})
	now := time.Now()
	checks, state, series := v8Collect(t, u, now)
	if state != "ok" {
		t.Fatalf("état %q %v", state, checks)
	}
	v8Expect(t, series, "unbound.time.up", 50.265694)
	v8Expect(t, series, "unbound.mem.cache.rrset", 117448)
	v8Expect(t, series, "unbound.recursion.time.avg", 0.245583)
	if _, ok := v8Series(t, series, "unbound.num.queries"); ok {
		t.Errorf("écart publié dès le premier relevé")
	}
	// Second relevé : 10 requêtes de plus, dont 6 en cache.
	txt := strings.NewReplacer("total.num.queries=5\n", "total.num.queries=15\n", "total.num.cachehits=2\n", "total.num.cachehits=8\n",
		"total.num.cachemiss=3\n", "total.num.cachemiss=7\n", "num.answer.rcode.NXDOMAIN=1\n", "num.answer.rcode.NXDOMAIN=3\n").Replace(string(v8File(t, "unbound-1.26.2-stats.txt")))
	os.WriteFile(stats, []byte(txt), 0o644)
	u.det.triedAt = time.Time{}
	checks, _, series = v8Collect(t, u, now.Add(31*time.Second))
	v8Expect(t, series, "unbound.num.queries", 10)
	v8Expect(t, series, "unbound.num.cachehits", 6)
	v8Expect(t, series, "unbound.cache.hit_rate", 60)
	v8Expect(t, series, "unbound.num.answer.rcode", 2, "rcode", "NXDOMAIN")
	v8Expect(t, series, "unbound.num.query.type", 0, "query_type", "A")
	v8Check(t, checks, "unbound.can_connect", model.CheckOK)
	// remote-control coupé : la sortie d'erreur d'unbound-control devient un contrôle critique.
	os.WriteFile(script, []byte("#!/bin/sh\necho 'error: connect: No such file or directory for /run/unbound.ctl'; exit 1\n"), 0o755)
	checks, _, _ = v8Collect(t, u, now.Add(62*time.Second))
	if c := v8Check(t, checks, "unbound.can_connect", model.CheckCrit); !strings.Contains(c.Message, "unbound.ctl") {
		t.Errorf("message : %s", c.Message)
	}
}

// ── Caddy 2.11.9 (API d'administration) ──────────────────────────────────────────────────────

func TestCaddyAdminAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("écriture %s %s", r.Method, r.URL.Path)
		}
		switch r.URL.Path {
		case "/config/apps/http/servers":
			w.Write(v8File(t, "caddy-2.11.9-servers.json"))
		case "/reverse_proxy/upstreams":
			w.Write(v8File(t, "caddy-2.11.9-upstreams.json"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := newCaddy(config.CaddyConfig{Enabled: "auto", URL: srv.URL})
	checks, state, series := v8Collect(t, c, time.Now())
	if state != "ok" {
		t.Fatalf("état %q", state)
	}
	v8Expect(t, series, "caddy.servers", 2)
	v8Expect(t, series, "caddy.routes", 2)
	v8Expect(t, series, "caddy.upstreams", 2)
	v8Expect(t, series, "caddy.upstream.fails", 0, "upstream", "origine:80")
	v8Expect(t, series, "caddy.upstream.requests", 0, "upstream", "192.0.2.250:80")
	v8Check(t, checks, "caddy.can_connect", model.CheckOK)
	// Rien n'écoute : en mode auto, absent sans contrôle.
	srv.Close()
	off := newCaddy(config.CaddyConfig{Enabled: "auto", URL: "http://127.0.0.1:2019"})
	off.http = newHTTPProbe(srv.URL, "", "", false)
	if checks, st, _ := v8Collect(t, off, time.Now()); st != "absent" || len(checks) != 0 {
		t.Errorf("caddy absent : %q %v", st, checks)
	}
}

// ── Mosquitto 2.1.4 ──────────────────────────────────────────────────────────────────────────

// fakeBroker : courtier MQTT 3.1.1 minimal qui rend les sujets $SYS relevés (messages retenus).
func fakeBroker(t *testing.T, user, pass string, subackCode byte, published *int) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	var sys [][2]string
	for _, line := range strings.Split(string(v8File(t, "mosquitto-2.1.4-sys.txt")), "\n") {
		if topic, val, ok := strings.Cut(line, " "); ok {
			// « $SYS/broker/retained messages/count 55 » : le sujet contient une espace.
			if strings.HasPrefix(val, "messages/count") {
				topic, val = topic+" messages/count", strings.TrimSpace(strings.TrimPrefix(val, "messages/count"))
			}
			sys = append(sys, [2]string{topic, val})
		}
	}
	var mu sync.Mutex
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				rd := bufio.NewReader(c)
				typ, body, err := mqttRead(rd)
				if err != nil || typ != 1 {
					return
				}
				// CONNECT : nom du protocole (6), niveau (1), drapeaux (1), maintien (2), identifiant, compte.
				flags := body[7]
				rest := body[10:]
				next := func() string {
					n := int(rest[0])<<8 | int(rest[1])
					s := string(rest[2 : 2+n])
					rest = rest[2+n:]
					return s
				}
				next()
				var u, p string
				if flags&0x80 != 0 {
					u = next()
				}
				if flags&0x40 != 0 {
					p = next()
				}
				if u != user || p != pass {
					c.Write([]byte{0x20, 2, 0, 5})
					return
				}
				c.Write([]byte{0x20, 2, 0, 0})
				for {
					typ, body, err := mqttRead(rd)
					if err != nil {
						return
					}
					switch typ {
					case 3:
						mu.Lock()
						*published++
						mu.Unlock()
					case 8:
						c.Write([]byte{0x90, 3, body[0], body[1], subackCode})
						if subackCode == 0x80 {
							continue
						}
						for _, kv := range sys {
							msg := append(mqttString(kv[0]), kv[1]...)
							c.Write(mqttPacket(0x31, msg)) // PUBLISH retenu, QoS 0
						}
					case 14:
						return
					}
				}
			}(c)
		}
	}()
	return ln.Addr().String()
}

func TestMosquittoSys(t *testing.T) {
	published := 0
	addr := fakeBroker(t, "hypha", "mdp", 0, &published)
	m := newMosquitto(config.MosquittoConfig{Enabled: "auto", URL: "mqtt://" + addr, Username: "hypha", Password: "mdp",
		Timeout: config.Duration(3 * time.Second)})
	now := time.Now()
	start := time.Now()
	checks, state, series := v8Collect(t, m, now)
	if state != "ok" {
		t.Fatalf("état %q %v", state, checks)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("relevé trop long : %s (les messages retenus suffisent)", d)
	}
	v8Expect(t, series, "mosquitto.clients.connected", 0)
	v8Expect(t, series, "mosquitto.retained_messages", 55)
	v8Expect(t, series, "mosquitto.store.messages.bytes", 183)
	v8Expect(t, series, "mosquitto.heap.current", 833991)
	v8Expect(t, series, "mosquitto.uptime", 133)
	v8Expect(t, series, "mosquitto.load.connections.1min", 0.92)
	if c := v8Check(t, checks, "mosquitto.can_connect", model.CheckOK); !strings.Contains(c.Message, "2.1.4") {
		t.Errorf("message : %s", c.Message)
	}
	m.det.triedAt = time.Time{}
	_, _, series = v8Collect(t, m, now.Add(time.Minute))
	v8Expect(t, series, "mosquitto.messages.received", 0)
	if published != 0 {
		t.Errorf("le client a publié %d message(s)", published)
	}
	// Mauvais compte : CONNACK 5 ; ACL sans $SYS : abonnement refusé.
	checks, _, _ = v8Collect(t, newMosquitto(config.MosquittoConfig{Enabled: "true", URL: "mqtt://" + addr, Username: "hypha", Password: "x",
		Timeout: config.Duration(2 * time.Second)}), now)
	if c := v8Check(t, checks, "mosquitto.can_connect", model.CheckCrit); !strings.Contains(c.Message, "CONNACK 5") {
		t.Errorf("message : %s", c.Message)
	}
	acl := fakeBroker(t, "hypha", "mdp", 0x80, &published)
	checks, _, _ = v8Collect(t, newMosquitto(config.MosquittoConfig{Enabled: "true", URL: "mqtt://" + acl, Username: "hypha", Password: "mdp",
		Timeout: config.Duration(2 * time.Second)}), now)
	if c := v8Check(t, checks, "mosquitto.can_connect", model.CheckCrit); !strings.Contains(c.Message, "ACL") {
		t.Errorf("message : %s", c.Message)
	}
}

// ── Proxmox Backup Server 4.2 ────────────────────────────────────────────────────────────────

func fakePBS(t *testing.T) *httptest.Server {
	files := map[string]string{"/status/datastore-usage": "pbs-4.2-datastore-usage.json", "/admin/datastore/essai/groups": "pbs-4.2-groups.json",
		"/admin/datastore/essai/snapshots": "pbs-4.2-snapshots.json", "/admin/verify": "pbs-4.2-verify.json",
		"/admin/prune": "pbs-4.2-prune.json", "/admin/gc": "pbs-4.2-gc.json", "/admin/sync": "pbs-4.2-sync.json",
		"/nodes/localhost/status": "pbs-4.2-node-status.json", "/version": "pbs-4.2-version.json"}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "PBSAPIToken=hypha@pbs!lecture:secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodGet {
			t.Errorf("écriture %s %s", r.Method, r.URL.Path)
		}
		p := strings.TrimPrefix(r.URL.Path, "/api2/json")
		if strings.Contains(p, "subscription") {
			t.Errorf("abonnement lu")
		}
		switch {
		case p == "/admin/datastore/essai/namespace":
			fmt.Fprint(w, `{"data":[{"ns":""}]}`)
			return
		case p == "/nodes/localhost/tasks" && r.URL.Query().Get("errors") == "1":
			w.Write(v8File(t, "pbs-4.2-tasks-errors.json"))
			return
		case p == "/nodes/localhost/tasks" && r.URL.Query().Get("typefilter") == "backup":
			w.Write(v8File(t, "pbs-4.2-tasks-backup.json"))
			return
		}
		if f, ok := files[p]; ok {
			w.Write(v8File(t, f))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestPBSOnRealResponses(t *testing.T) {
	srv := fakePBS(t)
	sum := sha256.Sum256(srv.Certificate().Raw)
	var fpr []string
	for _, c := range sum {
		fpr = append(fpr, hex.EncodeToString([]byte{c}))
	}
	var posted []map[string]any
	d := Deps{Post: func(route string, f map[string]any) { posted = append(posted, f) }}
	cfg := config.PBSConfig{Enabled: "auto", URL: srv.URL, TokenID: "hypha@pbs!lecture", TokenSecret: "secret",
		Fingerprint: strings.Join(fpr, ":"), Interval: config.Duration(time.Minute), MaxGroups: 500}
	p := newPBS(cfg, d)
	now := time.Unix(1795228100, 0)
	checks, state, series := v8Collect(t, p, now)
	if state != "ok" {
		t.Fatalf("état %q %v", state, checks)
	}
	v8Expect(t, series, "proxmox_backup.datastore.total", 102915489792, "datastore", "essai")
	v8Expect(t, series, "proxmox_backup.datastore.usage", 36.3, "datastore", "essai")
	v8Expect(t, series, "proxmox_backup.datastore.chunks", 35, "datastore", "essai")
	v8Expect(t, series, "proxmox_backup.datastore.groups", 1, "datastore", "essai")
	v8Expect(t, series, "proxmox_backup.group.last_backup.age", 140, "backup_type", "host", "backup_id", "pbs-essai")
	v8Expect(t, series, "proxmox_backup.group.snapshots", 2, "backup_id", "pbs-essai")
	v8Expect(t, series, "proxmox_backup.datastore.snapshots.verify_failed", 1, "datastore", "essai")
	v8Expect(t, series, "proxmox_backup.datastore.snapshots", 2, "datastore", "essai")
	v8Expect(t, series, "proxmox_backup.node.memory.total", 8321568768)
	v8Check(t, checks, "proxmox_backup.datastore", model.CheckOK, "datastore", "essai")
	if c := v8Check(t, checks, "proxmox_backup.job", model.CheckCrit, "job_type", "verify", "job_id", "v-essai"); !strings.Contains(c.Message, "verification failed") {
		t.Errorf("message : %s", c.Message)
	}
	v8Check(t, checks, "proxmox_backup.job", model.CheckOK, "job_type", "prune", "job_id", "p-essai")
	v8Check(t, checks, "proxmox_backup.job", model.CheckOK, "job_type", "garbage_collection", "job_id", "essai")
	if c := v8Check(t, checks, "proxmox_backup.backup", model.CheckCrit, "backup_id", "interrompue"); !strings.Contains(c.Message, "connection error") {
		t.Errorf("message : %s", c.Message)
	}
	v8Check(t, checks, "proxmox_backup.backup", model.CheckOK, "backup_type", "host", "backup_id", "pbs-essai", "datastore", "essai")
	if c := v8Check(t, checks, "proxmox_backup.can_connect", model.CheckOK); !strings.Contains(c.Message, "4.2.14") {
		t.Errorf("message : %s", c.Message)
	}
	if len(posted) != 0 {
		t.Errorf("historique relayé au premier relevé : %v", posted)
	}
	// Relevé suivant, curseur reculé : les deux tâches en échec partent en événements.
	p.since = 1795227900
	_, _, series = v8Collect(t, p, now.Add(time.Minute))
	if len(posted) != 1 || len(posted[0]["events"].([]model.Event)) != 2 {
		t.Fatalf("événements : %v", posted)
	}
	v8Expect(t, series, "proxmox_backup.tasks.failed", 1, "pbs_task", "verificationjob")
	// Empreinte fausse : refus TLS, contrôle critique.
	cfg.Fingerprint = strings.Repeat("00", 32)
	checks, _, _ = v8Collect(t, newPBS(cfg, d), now)
	if c := v8Check(t, checks, "proxmox_backup.can_connect", model.CheckCrit); !strings.Contains(c.Message, "empreinte") {
		t.Errorf("message : %s", c.Message)
	}
	// Jeton sans droit sur les magasins : liste vide, avertissement explicite.
	empty := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"data":[]}`) }))
	defer empty.Close()
	cfg.URL, cfg.Fingerprint, cfg.Insecure = empty.URL, "", true
	checks, _, _ = v8Collect(t, newPBS(cfg, d), now)
	if c := v8Check(t, checks, "proxmox_backup.can_connect", model.CheckWarn); !strings.Contains(c.Message, "aucun magasin visible") {
		t.Errorf("message : %s", c.Message)
	}
	if s, ns, typ, id := pbsWorkerID(`essai:prod/vm/100`); s != "essai" || ns != "prod" || typ != "vm" || id != "100" {
		t.Errorf("pbsWorkerID : %s %s %s %s", s, ns, typ, id)
	}
}

// ── Borg 1.4.4 et restic 0.18.1 ──────────────────────────────────────────────────────────────

func TestBorgListAndCheckpoint(t *testing.T) {
	dir := t.TempDir()
	pass := filepath.Join(dir, "borg.pass")
	os.WriteFile(pass, []byte("phrase-essai\n"), 0o600)
	var gotArgs, gotEnv []string
	var gotStdin []byte
	b := newBackupTool("borg", config.BackupToolConfig{Enabled: "auto", Command: config.CommandLine{"/usr/bin/borg"},
		Interval: config.Duration(15 * time.Minute), Timeout: config.Duration(time.Minute),
		Repositories: []config.BackupRepo{{Name: "serveur", Repository: "ssh://borg@192.0.2.20/srv/borg/essai", PassphraseFile: pass,
			RSH: "ssh -i /root/.ssh/borg", Tags: map[string]string{"site": "lab"}}}})
	b.baseDir = filepath.Join(dir, "base")
	b.run = func(_ context.Context, _ time.Duration, env []string, stdin []byte, path string, args ...string) ([]byte, []byte, error) {
		gotArgs, gotEnv, gotStdin = args, env, stdin
		return v8File(t, "borg-1.4.4-list.json"), nil, nil
	}
	// Horloge locale de la machine de l'essai (UTC) : 02:29:21 + 10 min.
	now := time.Date(2026, 11, 21, 2, 39, 21, 0, time.Local)
	checks, state, series := v8Collect(t, b, now)
	if state != "ok" {
		t.Fatalf("état %q %v", state, checks)
	}
	if strings.Join(gotArgs, " ") != "list --json --consider-checkpoints --bypass-lock ssh://borg@192.0.2.20/srv/borg/essai" {
		t.Errorf("arguments : %v", gotArgs)
	}
	if string(gotStdin) != "phrase-essai\n" || !strings.Contains(strings.Join(gotEnv, "|"), "BORG_PASSPHRASE_FD=0") ||
		!strings.Contains(strings.Join(gotEnv, "|"), "BORG_RSH=ssh -i /root/.ssh/borg") || strings.Contains(strings.Join(gotEnv, "|"), "phrase-essai") {
		t.Errorf("phrase de passe ou environnement : %q %v", gotStdin, gotEnv)
	}
	v8Expect(t, series, "borg.archives", 1, "repository", "serveur", "site", "lab")
	v8Expect(t, series, "borg.checkpoints", 1, "repository", "serveur")
	v8Expect(t, series, "borg.last_archive.age", 600, "repository", "serveur")
	if c := v8Check(t, checks, "borg.backup", model.CheckWarn, "repository", "serveur"); !strings.Contains(c.Message, "interrompue") {
		t.Errorf("message : %s", c.Message)
	}
	v8Check(t, checks, "borg.can_connect", model.CheckOK, "repository", "serveur")
	// Phrase de passe fausse : la sortie de borg devient le message du contrôle critique.
	b.run = func(context.Context, time.Duration, []string, []byte, string, ...string) ([]byte, []byte, error) {
		return nil, v8File(t, "borg-1.4.4-erreur-phrase.txt"), fmt.Errorf("exit status 2")
	}
	b.nextRun = time.Time{}
	checks, state, _ = v8Collect(t, b, now)
	if c := v8Check(t, checks, "borg.can_connect", model.CheckCrit); !strings.Contains(c.Message, "is incorrect") || !strings.HasPrefix(state, "erreur") {
		t.Errorf("message : %s (%s)", c.Message, state)
	}
	// Fichier ouvert à tous : refusé avant tout lancement.
	if runtime.GOOS == "linux" {
		os.Chmod(pass, 0o644)
		b.nextRun = time.Time{}
		checks, _, _ = v8Collect(t, b, now)
		if c := v8Check(t, checks, "borg.can_connect", model.CheckCrit); !strings.Contains(c.Message, "refusé") {
			t.Errorf("message : %s", c.Message)
		}
	}
}

func TestResticSnapshots(t *testing.T) {
	dir := t.TempDir()
	pass := filepath.Join(dir, "restic.pass")
	os.WriteFile(pass, []byte("x"), 0o600)
	var gotArgs []string
	r := newBackupTool("restic", config.BackupToolConfig{Enabled: "auto", Command: config.CommandLine{"/usr/bin/restic"},
		Interval: config.Duration(15 * time.Minute), Timeout: config.Duration(time.Minute), Stats: true,
		Repositories: []config.BackupRepo{{Name: "nas", Repository: "/srv/restic", PasswordFile: pass}}})
	r.run = func(_ context.Context, _ time.Duration, _ []string, _ []byte, _ string, args ...string) ([]byte, []byte, error) {
		gotArgs = args
		if args[len(args)-1] == "raw-data" {
			return v8File(t, "restic-0.18.1-stats.json"), nil, nil
		}
		return v8File(t, "restic-0.18.1-snapshots.json"), nil, nil
	}
	now := time.Date(2026, 11, 21, 3, 29, 46, 626239184, time.UTC)
	checks, state, series := v8Collect(t, r, now)
	if state != "ok" {
		t.Fatalf("état %q %v", state, checks)
	}
	if !strings.Contains(strings.Join(gotArgs, " "), "--repo /srv/restic --password-file "+pass+" --no-lock --no-cache --json") {
		t.Errorf("arguments : %v", gotArgs)
	}
	v8Expect(t, series, "restic.snapshots", 2, "repository", "nas")
	v8Expect(t, series, "restic.last_snapshot.age", 3600, "repository", "nas")
	v8Expect(t, series, "restic.last_snapshot.files", 39)
	v8Expect(t, series, "restic.last_snapshot.added", 5493271)
	v8Expect(t, series, "restic.last_snapshot.duration", 0.9)
	v8Expect(t, series, "restic.host.last_snapshot.age", 3600, "restic_host", "serveur-exemple")
	v8Expect(t, series, "restic.repository.size", 6453134)
	v8Expect(t, series, "restic.repository.compression_ratio", 5.4)
	v8Check(t, checks, "restic.backup", model.CheckOK, "repository", "nas")
	r.run = func(context.Context, time.Duration, []string, []byte, string, ...string) ([]byte, []byte, error) {
		return nil, v8File(t, "restic-0.18.1-erreur-phrase.txt"), fmt.Errorf("exit status 12")
	}
	r.nextRun = time.Time{}
	checks, _, _ = v8Collect(t, r, now)
	if c := v8Check(t, checks, "restic.can_connect", model.CheckCrit); !strings.Contains(c.Message, "wrong password") || strings.Contains(c.Message, "message_type") {
		t.Errorf("message : %s", c.Message)
	}
	// Dépôt injoignable : restic réessaie, l'agent l'arrête au délai ; la cause vient des réessais.
	e := backupErr("restic", fmt.Errorf("délai dépassé"), []byte("Stat(<config/>) returned error, retrying after 1.206905942s: "+
		"Head \"http://:***@192.0.2.29:8000/essai/config\": dial tcp 192.0.2.29:8000: connect: no route to host\n"+
		`{"message_type":"exit_error","code":1,"message":"Fatal: unable to open config file: context canceled\nIs there a repository at the following location?\nrest:http://192.0.2.29:8000/essai/"}`+"\n"))
	if e.Error() != "restic : délai dépassé, dépôt injoignable (dial tcp 192.0.2.29:8000: connect: no route to host)" {
		t.Errorf("message : %v", e)
	}
	// Aucun dépôt déclaré : absent.
	if _, st, _ := v8Collect(t, newBackupTool("restic", config.BackupToolConfig{Enabled: "auto"}), now); st != "absent" {
		t.Errorf("sans dépôt : %q", st)
	}
}

// Un dépôt lent ne retient pas la boucle des intégrations : l'outil tourne en tâche de fond, son
// résultat est publié au relevé suivant, puis republié chaque minute avec l'âge recalculé.
func TestBackupToolDoesNotBlock(t *testing.T) {
	dir := t.TempDir()
	pass := filepath.Join(dir, "p")
	os.WriteFile(pass, []byte("x"), 0o600)
	release := make(chan struct{})
	r := newBackupTool("restic", config.BackupToolConfig{Enabled: "auto", Command: config.CommandLine{"/usr/bin/restic"},
		Interval: config.Duration(15 * time.Minute), Timeout: config.Duration(time.Minute),
		Repositories: []config.BackupRepo{{Name: "lent", Repository: "/srv/r", PasswordFile: pass}}})
	r.wait = 50 * time.Millisecond
	calls := 0
	r.run = func(context.Context, time.Duration, []string, []byte, string, ...string) ([]byte, []byte, error) {
		calls++
		<-release
		return v8File(t, "restic-0.18.1-snapshots.json"), nil, nil
	}
	now := time.Date(2026, 11, 21, 3, 29, 46, 626239184, time.UTC)
	start := time.Now()
	checks, state, _ := v8Collect(t, r, now)
	if time.Since(start) > time.Second || len(checks) != 0 || state != "partiel : premier relevé en cours" {
		t.Fatalf("premier relevé : %s, %q, %v", time.Since(start), state, checks)
	}
	close(release)
	for i := 0; i < 100; i++ {
		r.mu.Lock()
		busy := r.running
		r.mu.Unlock()
		if !busy {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	checks, state, series := v8Collect(t, r, now.Add(time.Minute))
	v8Check(t, checks, "restic.backup", model.CheckOK, "repository", "lent")
	v8Expect(t, series, "restic.last_snapshot.age", 3660, "repository", "lent")
	_, _, series = v8Collect(t, r, now.Add(2*time.Minute))
	v8Expect(t, series, "restic.last_snapshot.age", 3720, "repository", "lent")
	if calls != 1 || state != "ok" {
		t.Errorf("restic lancé %d fois (1 attendu avant l'intervalle), état %q", calls, state)
	}
}

// Étiquettes fixes : posées sur les mesures et les contrôles, sans écraser celles de la sonde.
func TestV8FixedTags(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/config/apps/http/servers":
			w.Write(v8File(t, "caddy-2.11.9-servers.json"))
		case "/reverse_proxy/upstreams":
			w.Write(v8File(t, "caddy-2.11.9-upstreams.json"))
		}
	}))
	defer srv.Close()
	it := named("caddy", "front", Deps{}, func(Deps) integration {
		return withTags(newCaddy(config.CaddyConfig{Enabled: "true", URL: srv.URL}), map[string]string{"essai": "vague8", "instance": "x"})
	})
	checks, _, series := v8Collect(t, it, time.Now())
	v8Expect(t, series, "caddy.servers", 2, "essai", "vague8", "instance_name", "front", "instance", srv.URL)
	v8Check(t, checks, "caddy.can_connect", model.CheckOK, "essai", "vague8", "instance_name", "front")
	if it.name() != "caddy:front" {
		t.Errorf("nom : %s", it.name())
	}
}
