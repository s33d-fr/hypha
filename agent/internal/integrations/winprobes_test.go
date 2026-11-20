package integrations

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

func seriesByKey(b *model.SeriesBuilder, tagKeys ...string) map[string]float64 {
	out := map[string]float64{}
	for _, s := range b.Series() {
		k := s.Metric
		for _, t := range tagKeys {
			if v, ok := s.Tags[t]; ok {
				k += "/" + t + "=" + v
			}
		}
		out[k] = s.Points[0].Value
	}
	return out
}

// loadIntegrations lit une conf comme l'agent (valeurs par défaut, normalisation, validation).
func loadIntegrations(t *testing.T, key, body string) config.IntegrationsConfig {
	t.Helper()
	p := filepath.Join(t.TempDir(), "hypha.yaml")
	var b strings.Builder
	b.WriteString("api_key: hph_" + strings.Repeat("a", 40) + "\nintegrations:\n  " + key + ":\n")
	for _, l := range strings.Split(strings.Trim(body, "\n"), "\n") {
		b.WriteString("    " + l + "\n")
	}
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if errs := c.Validate(true); len(errs) > 0 {
		t.Fatalf("conf refusée : %v", errs)
	}
	return c.Integrations
}

func TestPerfCounters(t *testing.T) {
	cfg := loadIntegrations(t, "perf_counters", `
- name: systeme
  namespace: win
  max_series: 4
  tags: {role: essai}
  counters:
    - {path: '\Processor(*)\% Processor Time', metric: cpu.time, unit: percent, exclude_instances: [_total]}
    - {path: '\LogicalDisk(*)\% Free Space', instance_tag: disk, instances: ['?:']}
    - {path: '\Memory\Available MBytes', metric: mem.available_mb}
    - {path: '\Objet Inexistant\Compteur'}
`).PerfCounters[0]
	read := func(paths []string) (pdhValues, []string, error) {
		if len(paths) != 4 {
			t.Fatalf("chemins : %v", paths)
		}
		return pdhValues{
			`\Processor(*)\% Processor Time`: {"0": 25.5, "1": 28, "_Total": 26.7},
			`\LogicalDisk(*)\% Free Space`:   {"C:": 83.2, "HarddiskVolume1": 40, "_Total": 80},
			`\Memory\Available MBytes`:       {"": 2715},
		}, []string{`\Objet Inexistant\Compteur`}, nil
	}
	p := newPerfSet(cfg, read, nil)
	if p.name() != "perf_counters:systeme" || p.period() != 30*time.Second {
		t.Fatalf("%s %s", p.name(), p.period())
	}
	b := model.NewSeriesBuilder(0, testTags)
	checks, st := p.collect(context.Background(), time.Now(), b, testTags)
	got := seriesByKey(b, "instance", "disk")
	for k, want := range map[string]float64{
		"win.cpu.time/instance=0": 25.5, "win.cpu.time/instance=1": 28, "win.free_space_pct/disk=C:": 83.2,
		"win.mem.available_mb": 2715, "perf_counters.series": 4, "perf_counters.series.dropped": 0,
	} {
		if v, ok := got[k]; !ok || v != want {
			t.Errorf("%s = %v (présent %v), attendu %v", k, v, ok, want)
		}
	}
	if _, ok := got["win.cpu.time/instance=_Total"]; ok {
		t.Error("_Total non écarté")
	}
	for _, s := range b.Series() {
		if s.Tags[config.InstanceTag] != "systeme" || s.Tags["role"] != "essai" {
			t.Fatalf("étiquettes : %v", s.Tags)
		}
	}
	if len(checks) != 1 || checks[0].Name != "perf_counters.can_collect" || checks[0].Status != model.CheckWarn ||
		!strings.Contains(checks[0].Message, `absent(s) : \Objet Inexistant\Compteur`) || !strings.HasPrefix(st, "partiel") {
		t.Fatalf("contrôle : %+v %q", checks, st)
	}

	// Plafond de séries.
	cfg.MaxSeries = 2
	b = model.NewSeriesBuilder(0, testTags)
	checks, _ = newPerfSet(cfg, read, nil).collect(context.Background(), time.Now(), b, testTags)
	got = seriesByKey(b)
	if got["perf_counters.series"] != 2 || got["perf_counters.series.dropped"] != 2 || !strings.Contains(checks[0].Message, "max_series (2)") {
		t.Fatalf("plafond : %v %+v", got, checks)
	}

	// Rien n'existe : CRIT ; lecture en échec : CRIT et requête refermée.
	none := func([]string) (pdhValues, []string, error) {
		return pdhValues{}, []string{`\A\B`, `\C\D`, `\E\F`, `\G\H`}, nil
	}
	checks, _ = newPerfSet(cfg, none, nil).collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags)
	if checks[0].Status != model.CheckCrit || !strings.Contains(checks[0].Message, "aucun des compteurs") {
		t.Fatalf("aucun compteur : %+v", checks)
	}
	closed := false
	failing := newPerfSet(cfg, func([]string) (pdhValues, []string, error) {
		return nil, nil, errors.New("PdhCollectQueryData : 0xc0000bc6")
	},
		func() { closed = true })
	checks, st = failing.collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags)
	if checks[0].Status != model.CheckCrit || !closed || !strings.HasPrefix(st, "erreur") {
		t.Fatalf("échec : %+v %q fermé=%v", checks, st, closed)
	}
}

func TestGuardTimeout(t *testing.T) {
	var g guard
	release := make(chan struct{})
	_, err := guardRun(&g, 20*time.Millisecond, func() (int, error) { <-release; return 1, nil })
	if err == nil || !strings.Contains(err.Error(), "délai de 20ms dépassé") {
		t.Fatalf("délai : %v", err)
	}
	// Relevé précédent encore bloqué : on n'en empile pas un second.
	if _, err := guardRun(&g, time.Second, func() (int, error) { return 2, nil }); !errors.Is(err, errBusy) {
		t.Fatalf("bloqué : %v", err)
	}
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for g.busy.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if v, err := guardRun(&g, time.Second, func() (int, error) { return 3, nil }); v != 3 || err != nil {
		t.Fatalf("après déblocage : %d %v", v, err)
	}
	if _, err := guardRun(&g, time.Second, func() (int, error) { panic("boum") }); err == nil || !strings.Contains(err.Error(), "boum") {
		t.Fatalf("panique : %v", err)
	}
}

func TestWMIQuery(t *testing.T) {
	c := loadIntegrations(t, "wmi", `
- name: os
  class: Win32_OperatingSystem
  properties: [FreePhysicalMemory, NumberOfProcesses, {property: FreeVirtualMemory, metric: free_virtual_kb, unit: kibibyte}, Caption]
  tag_by: [Caption, CSName]
- name: services-auto
  class: Win32_Service
  properties: [ProcessId]
  filter: "StartMode = 'Auto' AND State = 'Running'"
  tag_by: [Name]
  max_series: 2
`)
	osq := c.WMI[0]
	var gotNS, gotQ string
	w := newWMISet(osq, func(ns, q string, props []string) ([]map[string]any, error) {
		gotNS, gotQ = ns, q
		if strings.Join(props, ",") != "FreePhysicalMemory,NumberOfProcesses,FreeVirtualMemory,Caption,CSName" {
			t.Fatalf("propriétés : %v", props)
		}
		// Valeurs telles que les rend go-ole : uint64 en texte, uint32, texte.
		return []map[string]any{{"FreePhysicalMemory": "2780808", "NumberOfProcesses": uint32(74), "FreeVirtualMemory": "4275608",
			"Caption": "Microsoft Windows Server 2025 Standard Evaluation", "CSName": "DC-ESSAI"}}, nil
	})
	b := model.NewSeriesBuilder(0, testTags)
	checks, st := w.collect(context.Background(), time.Now(), b, testTags)
	if gotNS != `root\cimv2` || gotQ != "SELECT FreePhysicalMemory, NumberOfProcesses, FreeVirtualMemory, Caption, CSName FROM Win32_OperatingSystem" {
		t.Fatalf("requête : %s %s", gotNS, gotQ)
	}
	got := seriesByKey(b, "cs_name")
	if got["os.free_physical_memory/cs_name=DC-ESSAI"] != 2780808 || got["os.number_of_processes/cs_name=DC-ESSAI"] != 74 ||
		got["os.free_virtual_kb/cs_name=DC-ESSAI"] != 4275608 || got["wmi.rows"] != 1 || got["wmi.series"] != 3 {
		t.Fatalf("séries : %v", got)
	}
	for _, s := range b.Series() {
		if s.Metric == "os.free_virtual_kb" && (s.Unit != "kibibyte" || s.Tags["caption"] == "") {
			t.Fatalf("unité, étiquette : %+v", s)
		}
	}
	if checks[0].Name != "wmi.can_query" || checks[0].Status != model.CheckWarn || !strings.Contains(checks[0].Message, "non numérique(s), à mettre dans tag_by : Caption") || !strings.HasPrefix(st, "partiel") {
		t.Fatalf("contrôle : %+v", checks)
	}

	svc := c.WMI[1]
	rows := []map[string]any{{"ProcessId": int32(948), "Name": "DcomLaunch"}, {"ProcessId": int32(2940), "Name": "DNS"}, {"processid": int64(444), "name": "HyphaAgent"}}
	w = newWMISet(svc, func(ns, q string, _ []string) ([]map[string]any, error) {
		if q != "SELECT ProcessId, Name FROM Win32_Service WHERE StartMode = 'Auto' AND State = 'Running'" {
			t.Fatalf("requête : %s", q)
		}
		return rows, nil
	})
	b = model.NewSeriesBuilder(0, testTags)
	checks, _ = w.collect(context.Background(), time.Now(), b, testTags)
	got = seriesByKey(b, "name")
	if got["services_auto.process_id/name=DNS"] != 2940 || got["wmi.series.dropped"] != 1 || checks[0].Status != model.CheckWarn {
		t.Fatalf("plafond : %v %+v", got, checks)
	}
	// Aucune ligne : avertissement ; requête en échec : critique.
	w = newWMISet(svc, func(string, string, []string) ([]map[string]any, error) { return nil, nil })
	if checks, _ = w.collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags); checks[0].Status != model.CheckWarn {
		t.Fatalf("vide : %+v", checks)
	}
	w = newWMISet(svc, func(string, string, []string) ([]map[string]any, error) {
		return nil, errors.New("Exception occurred. (Classe non valide )")
	})
	if checks, st = w.collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags); checks[0].Status != model.CheckCrit || !strings.HasPrefix(st, "erreur") {
		t.Fatalf("échec : %+v %q", checks, st)
	}
	for in, want := range map[any]float64{true: 1, false: 0, "12.5": 12.5, uint64(1) << 40: 1 << 40, int16(-3): -3, float32(0.5): 0.5} {
		if v, ok := wmiNumber(in); !ok || v != want {
			t.Errorf("wmiNumber(%#v) = %v %v", in, v, ok)
		}
	}
	if _, ok := wmiNumber("Running"); ok {
		t.Error("texte pris pour un nombre")
	}
}

func TestEventlogCounter(t *testing.T) {
	e := newEventlogCounter()
	if _, st := e.collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags); st != "absent" {
		t.Fatalf("avant tout événement : %q", st)
	}
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.observe(&model.Log{Source: "eventlog", Status: "error", Attributes: map[string]any{"eventlog": map[string]any{"channel": "System"}}})
		}()
	}
	wg.Wait()
	e.observe(&model.Log{Source: "eventlog", Status: "warning", Attributes: map[string]any{"eventlog": map[string]any{"channel": "Security"}}})
	e.observe(&model.Log{Source: "journald", Status: "error"})
	b := model.NewSeriesBuilder(0, testTags)
	if _, st := e.collect(context.Background(), time.Now(), b, testTags); st != "ok" {
		t.Fatal(st)
	}
	got := seriesByKey(b, "channel", "status")
	if len(got) != 2 || got["windows.eventlog.events/channel=System/status=error"] != 3 || got["windows.eventlog.events/channel=Security/status=warning"] != 1 {
		t.Fatalf("comptes : %v", got)
	}
	if b.Series()[0].Type != model.TypeCount {
		t.Fatalf("type : %s", b.Series()[0].Type)
	}
	// Intervalle suivant sans événement : zéros pour les couples déjà vus.
	b = model.NewSeriesBuilder(0, testTags)
	e.collect(context.Background(), time.Now(), b, testTags)
	if got := seriesByKey(b, "channel", "status"); len(got) != 2 || got["windows.eventlog.events/channel=System/status=error"] != 0 {
		t.Fatalf("zéros : %v", got)
	}
}
