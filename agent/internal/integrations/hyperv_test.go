package integrations

import (
	"context"
	"strings"
	"testing"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

func TestHypervVMs(t *testing.T) {
	h := newHyperv(config.IntegrationToggle{Enabled: "auto"})
	h.run = func(context.Context) ([]byte, error) {
		return []byte(`{"vms":[
			{"name":"web","state":"Running","cpu":12,"vcpus":2,"mem_assigned":2147483648,"mem_demand":1073741824,"uptime":3600,
			 "heartbeat":"OkApplicationsHealthy","repl_state":"Disabled","repl_health":"NotApplicable","start_action":"StartIfRunning",
			 "checkpoints":2,"oldest_checkpoint_s":864000},
			{"name":"base","state":"RunningCritical","heartbeat":"OkApplicationsUnknown","repl_state":"Disabled","start_action":"StartIfRunning"},
			{"name":"dc","state":"Off","repl_state":"Disabled","start_action":"Start"},
			{"name":"essai","state":"Off","repl_state":"Disabled","start_action":"StartIfRunning"},
			{"name":"app","state":"Running","cpu":1,"heartbeat":"LostCommunication","repl_state":"Replicating","repl_health":"Warning","start_action":"Start"}]}`), nil
	}
	b := model.NewSeriesBuilder(0, testTags)
	checks, st := h.collect(context.Background(), time.Now(), b, testTags)
	got := map[string]model.Check{}
	for _, c := range checks {
		got[c.Name+"/"+c.Tags["hyperv_vm"]] = c
	}
	for key, want := range map[string]struct {
		st   int
		text string
	}{
		"hyperv.vm/web":             {model.CheckOK, "web en marche ; 2 point(s) de contrôle, le plus ancien de 240h0m0s"},
		"hyperv.vm/base":            {model.CheckCrit, "état critique (RunningCritical)"},
		"hyperv.vm/dc":              {model.CheckCrit, "démarrage automatique est « toujours »"},
		"hyperv.vm/essai":           {model.CheckOK, "essai arrêtée"},
		"hyperv.vm/app":             {model.CheckWarn, "signal de vie perdu"},
		"hyperv.vm.replication/app": {model.CheckWarn, "réplication en avertissement (Replicating)"},
		"hyperv.can_connect/":       {model.CheckOK, "5 VM, 2 en marche"},
	} {
		c, ok := got[key]
		if !ok || c.Status != want.st || !strings.Contains(c.Message, want.text) {
			t.Fatalf("%s : %+v (attendu %d %q)", key, c, want.st, want.text)
		}
	}
	if st != "ok" {
		t.Fatalf("état %q", st)
	}
	series := map[string]float64{}
	for _, s := range b.Series() {
		series[s.Metric+"/"+s.Tags["hyperv_vm"]] = s.Points[0].Value
	}
	if series["hyperv.vm.cpu.usage/web"] != 12 || series["hyperv.vm.checkpoint.age/web"] != 10 || series["hyperv.vms.running/"] != 2 {
		t.Fatalf("mesures : %v", series)
	}
	// Sans le rôle Hyper-V : muet.
	h = newHyperv(config.IntegrationToggle{Enabled: "auto"})
	h.run = func(context.Context) ([]byte, error) { return []byte(`{"absent":true}`), nil }
	if checks, st := h.collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags); len(checks) != 0 || st != "absent" {
		t.Fatalf("sans Hyper-V : %q %+v", st, checks)
	}
}
