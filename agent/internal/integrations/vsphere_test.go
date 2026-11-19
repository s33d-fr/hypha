package integrations

import (
	"context"
	"crypto/tls"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/simulator"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// resourceChecks : contrôles publiés sous l'identité des ressources (hôtes ESXi, VM).
var resourceChecks []model.Check

func newTestVSphere(t *testing.T, targets ...config.VSphereTarget) (*VSphere, *[]map[string]any) {
	var posted []map[string]any
	v := newVSphere(config.VSphereConfig{Enabled: "auto", Interval: config.Duration(60 * time.Second), Targets: targets},
		Deps{AgentID: testAgentID, AgentVersion: "0.4.8", Post: func(route string, f map[string]any) {
			if route == routeChecks {
				info := f["agent"].(model.AgentInfo)
				for _, c := range f["checks"].([]model.Check) {
					c.Tags["_ressource"] = info.Hostname + "/" + info.OS
					resourceChecks = append(resourceChecks, c)
				}
			}
			if route == routeEvents {
				posted = append(posted, f)
			}
		}})
	v.tags = testTags
	v.once.Do(func() {}) // passages lancés par le test
	return v, &posted
}

func countChecks(checks []model.Check, name string, status int) int {
	n := 0
	for _, c := range checks {
		if c.Name == name && c.Status == status {
			n++
		}
	}
	return n
}

func TestVSphereSimulatedESX(t *testing.T) {
	esx := simulator.ESX()
	defer esx.Remove()
	if err := esx.Create(); err != nil {
		t.Fatal(err)
	}
	esx.Service.TLS = new(tls.Config) // certificat autosigné, comme un vrai ESXi
	srv := esx.Service.NewServer()
	defer srv.Close()
	v, posted := newTestVSphere(t, config.VSphereTarget{Name: "esx", URL: "https://" + srv.URL.Host + "/sdk",
		Username: "user", Password: "pass", Insecure: true})
	defer v.close()
	ctx := context.Background()
	resourceChecks = nil
	v.poll(ctx)
	b := model.NewSeriesBuilder(0, testTags)
	checks, state := v.collect(ctx, time.Now(), b, testTags)
	if len(v.snap.resources) < 2 || v.snap.resources[0].device.Type != "esxi_host" {
		t.Fatalf("ressources : %+v", v.snap.resources)
	}
	if state != "ok" {
		t.Fatalf("état %q, erreurs %v", state, v.snap.errs)
	}
	if countChecks(checks, "vsphere.can_connect", model.CheckOK) != 1 || countChecks(checks, "vsphere.datastore.status", model.CheckOK) == 0 {
		t.Fatalf("contrôles de l'agent : %+v", checks)
	}
	if countChecks(resourceChecks, "vsphere.host.status", model.CheckOK) != 1 || countChecks(resourceChecks, "vsphere.vm.status", model.CheckOK) == 0 {
		t.Fatalf("contrôles des ressources : %+v", resourceChecks)
	}
	for _, c := range resourceChecks {
		if !strings.HasSuffix(c.Tags["_ressource"], "/cloud") || c.Tags["host"] == "" {
			t.Fatalf("contrôle hors de sa ressource : %+v", c)
		}
	}
	metrics := map[string]bool{}
	for _, s := range b.Series() {
		metrics[s.Metric] = true
		if s.Tags["vsphere_target"] != "esx" {
			t.Fatalf("série sans cible : %+v", s)
		}
	}
	for _, m := range []string{"vsphere.datastore.in_use", "vsphere.vm.count"} {
		if !metrics[m] {
			t.Fatalf("mesure %s absente : %v", m, metrics)
		}
	}
	if len(*posted) != 0 {
		t.Fatal("premier passage : aucun événement rejoué")
	}

	// Une VM éteinte : un événement relayé, la VM sort des contrôles d'état.
	f := find.NewFinder(v.targets[0].client.Client, true)
	dc, err := f.DefaultDatacenter(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f.SetDatacenter(dc)
	vms, err := f.VirtualMachineList(ctx, "*")
	if err != nil || len(vms) == 0 {
		t.Fatalf("VM du simulateur : %v", err)
	}
	before := countChecks(resourceChecks, "vsphere.vm.status", model.CheckOK)
	task, err := vms[0].PowerOff(ctx)
	if err == nil {
		err = task.Wait(ctx)
	}
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	resourceChecks = nil
	v.poll(ctx)
	v.collect(ctx, time.Now(), model.NewSeriesBuilder(0, testTags), testTags)
	if after := countChecks(resourceChecks, "vsphere.vm.status", model.CheckOK); after != before-1 {
		t.Fatalf("VM éteinte encore suivie : %d puis %d", before, after)
	}
	found := false
	for _, p := range *posted {
		for _, e := range p["events"].([]model.Event) {
			if strings.Contains(e.Type, "PoweredOff") && e.Tags["vsphere_vm"] != "" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("événement d'arrêt absent : %+v ; connexion : %+v", *posted, checks[len(checks)-1])
	}
}

func TestVSphereUnreachableAndConfig(t *testing.T) {
	v, _ := newTestVSphere(t, config.VSphereTarget{Name: "absent", URL: "https://127.0.0.1:1/sdk", Username: "u", Password: "p", Insecure: true})
	v.poll(context.Background())
	checks, state := v.collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags)
	if !strings.HasPrefix(state, "partiel") || countChecks(checks, "vsphere.can_connect", model.CheckCrit) != 1 {
		t.Fatalf("cible injoignable : %q %+v", state, checks)
	}
	w := newVSphere(config.VSphereConfig{Enabled: "auto"}, Deps{})
	if c, st := w.collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags); c != nil || st != "absent" {
		t.Fatalf("sans cible : %q", st)
	}
}

// Essai contre un vrai ESXi ou vCenter, lancé à la main : VSPHERE_REEL=1, VS_URL, VS_USER, VS_PASS.
func TestVSphereReel(t *testing.T) {
	if os.Getenv("VSPHERE_REEL") != "1" {
		t.Skip("VSPHERE_REEL=1 pour interroger un vrai ESXi")
	}
	v, _ := newTestVSphere(t, config.VSphereTarget{Name: "reel", URL: os.Getenv("VS_URL"), Username: os.Getenv("VS_USER"),
		Password: os.Getenv("VS_PASS"), Insecure: true})
	defer v.close()
	v.poll(context.Background())
	b := model.NewSeriesBuilder(0, testTags)
	checks, state := v.collect(context.Background(), time.Now(), b, testTags)
	t.Logf("état %s, relevé en %s, erreurs %v", state, v.snap.took, v.snap.errs)
	for _, c := range checks {
		t.Logf("[%d] %s : %s", c.Status, c.Name, c.Message)
	}
	for _, s := range b.Series() {
		t.Logf("série %s %s%s%s = %v", s.Metric, s.Tags["vsphere_host"], s.Tags["vsphere_datastore"], s.Tags["vsphere_vm"], s.Points[0].Value)
	}
}

func TestVMwareSerial(t *testing.T) {
	if got := vmwareSerial("421A2B3C-4D5E-6F70-8192-A3B4C5D6E7F8"); got != "VMware-42 1a 2b 3c 4d 5e 6f 70-81 92 a3 b4 c5 d6 e7 f8" {
		t.Fatalf("série : %q", got)
	}
	if vmwareSerial("") != "" || vmwareSerial("42") != "" {
		t.Fatal("UUID absent ou tronqué : pas de série")
	}
}
