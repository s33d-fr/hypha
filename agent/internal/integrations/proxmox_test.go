package integrations

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

func pveFixture(pass int) map[string]any {
	t0 := float64(time.Now().Add(-2 * time.Hour).Unix())
	tasks := []map[string]any{
		{"upid": "UPID:n1:1", "node": "n1", "type": "vzdump", "id": "", "user": "root@pam", "status": "OK", "starttime": t0, "endtime": t0 + 60},
		{"upid": "UPID:n1:2", "node": "n1", "type": "vzdump", "id": "", "user": "root@pam", "status": "job errors", "starttime": t0 + 3600, "endtime": t0 + 3660},
		{"upid": "UPID:n2:3", "node": "n2", "type": "vzdump", "id": "100", "user": "root@pam", "status": "WARNINGS: 1", "starttime": t0, "endtime": t0 + 30},
		{"upid": "UPID:n2:4", "node": "n2", "type": "qmstart", "id": "101", "user": "root@pam", "starttime": t0 + 7000}, // en cours
	}
	if pass > 0 {
		tasks = append(tasks, map[string]any{"upid": "UPID:n1:5", "node": "n1", "type": "qmstart", "id": "100", "user": "root@pam",
			"status": "start failed: QEMU exited with code 1", "starttime": float64(time.Now().Unix() - 5), "endtime": float64(time.Now().Unix())})
	}
	return map[string]any{
		"/version": map[string]any{"version": "9.1.1", "release": "9.1"},
		"/cluster/status": []map[string]any{
			{"type": "cluster", "name": "labo", "quorate": 1, "nodes": 3},
			{"type": "node", "name": "n2", "online": 1, "local": 0},
			{"type": "node", "name": "n1", "online": 1, "local": 1},
			{"type": "node", "name": "n3", "online": 0, "local": 0},
		},
		"/cluster/resources": []map[string]any{
			{"type": "node", "node": "n1", "status": "online", "cpu": 0.25, "maxcpu": 4, "mem": 4e9, "maxmem": 16e9, "disk": 10e9, "maxdisk": 40e9, "uptime": 3600},
			{"type": "node", "node": "n2", "status": "online", "cpu": 0.1, "maxcpu": 4, "mem": 2e9, "maxmem": 16e9},
			{"type": "node", "node": "n3", "status": "offline"},
			{"type": "storage", "node": "n1", "storage": "local", "status": "available", "disk": 50e9, "maxdisk": 100e9},
			{"type": "storage", "node": "n2", "storage": "nfs", "status": "unknown"},
			{"type": "storage", "node": "n3", "storage": "local", "status": "unknown"},
			{"type": "qemu", "node": "n1", "vmid": 100, "name": "web", "status": "running", "cpu": 0.5, "mem": 1e9, "maxmem": 2e9,
				"diskread": 1000 + 500*pass, "diskwrite": 0, "netin": 10, "netout": 20, "hastate": "started", "template": 0},
			{"type": "lxc", "node": "n2", "vmid": 101, "name": "base", "status": "stopped", "hastate": "started", "template": 0},
			{"type": "qemu", "node": "n1", "vmid": 9000, "name": "gabarit", "status": "stopped", "template": 1},
			{"type": "sdn", "node": "n1", "sdn": "localnetwork", "status": "ok"},
		},
		"/cluster/tasks": tasks,
	}
}

func TestProxmoxCluster(t *testing.T) {
	var mu sync.Mutex
	pass := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "PVEAPIToken=hypha@pve!lecture=bon-secret" {
			w.WriteHeader(401)
			return
		}
		mu.Lock()
		data, ok := pveFixture(pass)[strings.TrimPrefix(r.URL.Path, "/api2/json")]
		mu.Unlock()
		if !ok {
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer srv.Close()
	var posted []model.Event
	p := newProxmox(config.ProxmoxConfig{Enabled: "auto", URL: srv.URL, TokenID: "hypha@pve!lecture", TokenSecret: "bon-secret", Interval: config.Duration(time.Minute)},
		Deps{Post: func(route string, f map[string]any) {
			if route == routeEvents {
				posted = append(posted, f["events"].([]model.Event)...)
			}
		}})
	var checks []model.Check
	var b *model.SeriesBuilder
	var st string
	t0 := time.Now()
	for i := 0; i < 2; i++ {
		mu.Lock()
		pass = i
		mu.Unlock()
		b = model.NewSeriesBuilder(0, testTags)
		checks, st = p.collect(context.Background(), t0.Add(time.Duration(i)*61*time.Second), b, testTags)
	}
	if st != "ok" {
		t.Fatalf("état : %q %+v", st, checks)
	}
	got := map[string]model.Check{}
	for _, c := range checks {
		got[c.Name+"/"+c.Tags["proxmox_node"]+":"+c.Tags["proxmox_storage"]+c.Tags["proxmox_vmid"]] = c
	}
	for key, want := range map[string]struct {
		st   int
		text string
	}{
		"proxmox.cluster.quorum/:": {model.CheckOK, "quorum atteint (2 nœud(s) en ligne)"},
		"proxmox.node/n1:":         {model.CheckOK, "n1 en ligne"},
		"proxmox.node/n3:":         {model.CheckCrit, "n3 hors ligne"},
		"proxmox.storage/n1:local": {model.CheckOK, "occupé à 50 %"},
		"proxmox.storage/n2:nfs":   {model.CheckCrit, "indisponible (unknown)"},
		"proxmox.guest.ha/n1:100":  {model.CheckOK, "HA started, running"},
		"proxmox.guest.ha/n2:101":  {model.CheckCrit, "HA started, stopped"},
		"proxmox.backup/n1:":       {model.CheckCrit, "en échec : job errors"},
		"proxmox.backup/n2:":       {model.CheckWarn, "avec avertissements"},
		"proxmox.can_connect/:":    {model.CheckOK, "Proxmox VE 9.1.1 : 3 nœud(s), 2 invité(s) dont 1 en marche"},
	} {
		c, ok := got[key]
		if !ok || c.Status != want.st || !strings.Contains(c.Message, want.text) {
			t.Fatalf("%s : %+v (attendu %d %q) ; %v", key, c, want.st, want.text, got)
		}
	}
	if _, ok := got["proxmox.storage/n3:local"]; ok {
		t.Fatal("stockage d'un nœud hors ligne signalé en double")
	}
	series := map[string]float64{}
	for _, s := range b.Series() {
		series[s.Metric+"/"+s.Tags["proxmox_node"]+s.Tags["proxmox_vmid"]] = s.Points[0].Value
	}
	for key, want := range map[string]float64{
		"proxmox.node.cpu.usage/n1": 25, "proxmox.node.mem.usage/n1": 25, "proxmox.guest.cpu.usage/n1100": 50,
		"proxmox.guest.disk.read/n1100": 500, "proxmox.guests.running/": 1, "proxmox.nodes.online/": 2,
	} {
		if series[key] != want {
			t.Fatalf("%s = %v (attendu %v) ; %v", key, series[key], want, series)
		}
	}
	if len(posted) != 1 || !strings.Contains(posted[0].Title, "qmstart 100 sur n1 : start failed") || posted[0].AlertType != "error" {
		t.Fatalf("événements : %+v", posted)
	}
	// Jeton refusé : CRIT sans le secret.
	p = newProxmox(config.ProxmoxConfig{Enabled: "auto", URL: srv.URL, TokenID: "hypha@pve!lecture", TokenSecret: "mauvais", Interval: config.Duration(time.Minute)}, Deps{})
	checks, _ = p.collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags)
	if len(checks) != 1 || checks[0].Status != model.CheckCrit || !strings.Contains(checks[0].Message, "401") || strings.Contains(checks[0].Message, "mauvais") {
		t.Fatalf("401 : %+v", checks)
	}
}

// Sur un nœud (pvesh), seul le premier nœud en ligne lit la grappe.
func TestProxmoxLocalOnlyFirstOnlineNode(t *testing.T) {
	p := newProxmox(config.ProxmoxConfig{Enabled: "auto", Interval: config.Duration(time.Minute)}, Deps{})
	fx := pveFixture(0)
	st := fx["/cluster/status"].([]map[string]any)
	st[1]["local"], st[2]["local"] = 1, 0 // ce nœud est n2, n1 est en ligne
	p.get = func(_ context.Context, path string, out any) error {
		b, _ := json.Marshal(fx[path])
		return json.Unmarshal(b, out)
	}
	checks, state := p.collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags)
	if len(checks) != 0 || state != "ok : grappe lue par n1" {
		t.Fatalf("nœud second : %q %+v", state, checks)
	}
	p.get = func(context.Context, string, any) error { return errNoPvesh }
	p.det = detector{mode: "auto"}
	if checks, state := p.collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags); len(checks) != 0 || state != "absent" {
		t.Fatalf("sans pvesh : %q %+v", state, checks)
	}
}

// Essai sur un vrai nœud Proxmox, lancé à la main : PROXMOX_REEL=1 (pvesh, lecture seule), ou
// PROXMOX_URL, PROXMOX_TOKEN_ID, PROXMOX_TOKEN_SECRET pour l'API.
func TestProxmoxReel(t *testing.T) {
	if os.Getenv("PROXMOX_REEL") != "1" {
		t.Skip("PROXMOX_REEL=1 pour lire un vrai Proxmox")
	}
	var events []model.Event
	p := newProxmox(config.ProxmoxConfig{Enabled: "true", URL: os.Getenv("PROXMOX_URL"), TokenID: os.Getenv("PROXMOX_TOKEN_ID"),
		TokenSecret: os.Getenv("PROXMOX_TOKEN_SECRET"), Insecure: os.Getenv("PROXMOX_URL") != "", Interval: config.Duration(time.Minute)},
		Deps{Post: func(_ string, f map[string]any) { events = append(events, f["events"].([]model.Event)...) }})
	var checks []model.Check
	var b *model.SeriesBuilder
	var st string
	for i := 0; i < 2; i++ {
		b = model.NewSeriesBuilder(0, testTags)
		checks, st = p.collect(context.Background(), time.Now(), b, testTags)
		time.Sleep(3 * time.Second)
	}
	t.Logf("== %s", st)
	for _, c := range checks {
		t.Logf("[%d] %s : %s", c.Status, c.Name, c.Message)
	}
	for _, s := range b.Series() {
		t.Logf("série %s %s%s%s = %v", s.Metric, s.Tags["proxmox_node"], s.Tags["proxmox_storage"], s.Tags["proxmox_vmid"], s.Points[0].Value)
	}
	t.Logf("%d événement(s)", len(events))
}
