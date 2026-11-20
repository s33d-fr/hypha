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

// Sorties construites au format décrit par la documentation de nvidia-smi
// (https://docs.nvidia.com/deploy/nvidia-smi/index.html : --query-gpu, --format=csv,noheader,nounits,
// « Any unsupported data is indicated by a "N/A" » ; exemple « nvidia-smi --format=csv,noheader
// --query-gpu=uuid,persistence_mode »). L'UUID du GPU 0 est celui de l'exemple de sortie de la doc
// de DCGM-Exporter. NON ÉPROUVÉ SUR UN VRAI GPU : aucune machine hors production du parc n'en a.
var gpuSamples = []map[string]string{
	{ // GPU sain, sans ventilateur (SXM) : fan.speed non pris en charge.
		"index": "0", "uuid": "GPU-604ac76c-d9cf-fef3-62e9-d92044ab6e52", "name": "NVIDIA A100-SXM4-40GB",
		"driver_version": "550.54.15", "pstate": "P0", "utilization.gpu": "87", "utilization.memory": "41",
		"memory.total": "40960", "memory.used": "30720", "memory.free": "9832", "temperature.gpu": "71",
		"temperature.memory": "65", "power.draw": "312.45", "power.limit": "400.00", "clocks.sm": "1410",
		"clocks.mem": "1215", "clocks.max.sm": "1410", "fan.speed": "[N/A]",
		"ecc.errors.corrected.volatile.total": "0", "ecc.errors.uncorrected.volatile.total": "0",
		"clocks_throttle_reasons.hw_thermal_slowdown": "Not Active", "clocks_throttle_reasons.sw_thermal_slowdown": "Not Active",
	},
	{ // GPU saturé, chaud, avec des erreurs ECC non corrigées.
		"index": "1", "uuid": "GPU-0f2c1a5e-1111-2222-3333-444455556666", "name": "NVIDIA A100-SXM4-40GB",
		"driver_version": "550.54.15", "pstate": "P2", "utilization.gpu": "100", "utilization.memory": "90",
		"memory.total": "40960", "memory.used": "40500", "memory.free": "460", "temperature.gpu": "89",
		"temperature.memory": "[N/A]", "power.draw": "398.10", "power.limit": "400.00", "clocks.sm": "1095",
		"clocks.mem": "1215", "clocks.max.sm": "1410", "fan.speed": "[N/A]",
		"ecc.errors.corrected.volatile.total": "12", "ecc.errors.uncorrected.volatile.total": "2",
		"clocks_throttle_reasons.hw_thermal_slowdown": "Active", "clocks_throttle_reasons.sw_thermal_slowdown": "Not Active",
	},
}

// Mémoire unifiée (DGX Spark, Grace) : nvidia-smi ne rend ni mémoire ni limite de puissance.
var gpuUnified = map[string]string{
	"index": "0", "uuid": "GPU-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "name": "NVIDIA GB10", "driver_version": "580.95.05",
	"pstate": "P0", "utilization.gpu": "3", "utilization.memory": "[N/A]", "memory.total": "[N/A]", "memory.used": "[N/A]",
	"memory.free": "[N/A]", "temperature.gpu": "45", "temperature.memory": "[N/A]", "power.draw": "11.32",
	"power.limit": "[N/A]", "clocks.sm": "2405", "clocks.mem": "[N/A]", "clocks.max.sm": "3003", "fan.speed": "[N/A]",
	"ecc.errors.corrected.volatile.total": "[N/A]", "ecc.errors.uncorrected.volatile.total": "[N/A]",
	"clocks_throttle_reasons.hw_thermal_slowdown": "Not Active", "clocks_throttle_reasons.sw_thermal_slowdown": "Not Active",
}

const gpuProcsSample = `GPU-604ac76c-d9cf-fef3-62e9-d92044ab6e52, 4242, /usr/bin/python3, 20480
GPU-604ac76c-d9cf-fef3-62e9-d92044ab6e52, 4243, python3, 1024
GPU-0f2c1a5e-1111-2222-3333-444455556666, 777, C:\Program Files\app\vllm.exe, [N/A]
`

// fakeSMI rend, pour --query-gpu, les colonnes demandées des GPU donnés ; refuse les champs
// de unknown comme le ferait un pilote qui ne les connaît pas.
func fakeSMI(gpus []map[string]string, procs string, unknown ...string) (func(context.Context, ...string) ([]byte, error), *[]string) {
	var calls []string
	return func(_ context.Context, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		q := strings.TrimPrefix(args[0], "--query-gpu=")
		if q == args[0] {
			return []byte(procs), nil
		}
		fields := strings.Split(q, ",")
		for _, f := range fields {
			for _, u := range unknown {
				if f == u {
					return []byte(`Field "` + u + `" is not a valid field to query.` + "\n\n"), errors.New("nvidia-smi : exit status 2")
				}
			}
		}
		var b strings.Builder
		for _, g := range gpus {
			cols := make([]string, len(fields))
			for i, f := range fields {
				cols[i] = g[f]
			}
			b.WriteString(strings.Join(cols, ", ") + "\n")
		}
		return []byte(b.String()), nil
	}, &calls
}

func TestNvidiaGPUMetricsAndHealth(t *testing.T) {
	n := newNvidiaGPU(config.NvidiaGPUConfig{Enabled: "true"})
	var calls *[]string
	n.run, calls = fakeSMI(gpuSamples, gpuProcsSample, "temperature.memory")
	b := model.NewSeriesBuilder(0, testTags)
	checks, st := n.collect(context.Background(), time.Now(), b, testTags)
	if st != "ok" {
		t.Fatalf("état %q", st)
	}
	if len(*calls) != 3 || strings.Contains((*calls)[1], "temperature.memory") {
		t.Fatalf("le champ refusé n'a pas été retiré : %q", *calls)
	}
	c := map[string]model.Check{}
	for _, x := range checks {
		c[x.Name+"/"+x.Tags["gpu"]] = x
	}
	if x := c["gpu.can_query/"]; x.Status != model.CheckOK || !strings.Contains(x.Message, "2 GPU NVIDIA lu(s) par nvidia-smi, pilote 550.54.15") {
		t.Fatalf("contrôle de lecture : %+v", x)
	}
	if x := c["gpu.health/0"]; x.Status != model.CheckOK {
		t.Fatalf("GPU 0 : %+v", x)
	}
	if x := c["gpu.health/1"]; x.Status != model.CheckCrit || !strings.Contains(x.Message, "2 erreur(s) ECC non corrigée(s)") {
		t.Fatalf("GPU 1 : %+v", x)
	}
	got := index(b.Series())
	g0 := "gpu=0,gpu_name=NVIDIA A100-SXM4-40GB,gpu_uuid=GPU-604ac76c-d9cf-fef3-62e9-d92044ab6e52"
	g1 := "gpu=1,gpu_name=NVIDIA A100-SXM4-40GB,gpu_uuid=GPU-0f2c1a5e-1111-2222-3333-444455556666"
	want := map[string]float64{
		"gpu.count{}":                                         2,
		"gpu.utilization{" + g0 + "}":                         87,
		"gpu.memory.used{" + g0 + "}":                         30720 * mib,
		"gpu.memory.usage{" + g0 + "}":                        75,
		"gpu.memory.usage{" + g1 + "}":                        98.9,
		"gpu.temperature{" + g1 + "}":                         89,
		"gpu.power.usage{" + g1 + "}":                         99.5,
		"gpu.clock.sm{" + g1 + "}":                            1095,
		"gpu.pstate{" + g1 + "}":                              2,
		"gpu.ecc.uncorrected{" + g1 + "}":                     2,
		"gpu.throttle.thermal{" + g1 + "}":                    1,
		"gpu.throttle.thermal{" + g0 + "}":                    0,
		"gpu.processes{" + g0 + "}":                           2,
		"gpu.processes{" + g1 + "}":                           1,
		"gpu.process.memory{" + g0 + ",process_name=python3}": 21504 * mib,
	}
	for k, v := range want {
		if s, ok := got[k]; !ok || s.Points[0].Value != v {
			t.Errorf("%s = %v, attendu %v", k, s.Points, v)
		}
	}
	for k := range got {
		if strings.HasPrefix(k, "gpu.fan.speed") || strings.HasPrefix(k, "gpu.memory.temperature") {
			t.Errorf("valeur non prise en charge envoyée : %s", k)
		}
		if strings.HasPrefix(k, "gpu.process.memory{"+g1) {
			t.Errorf("mémoire de processus inconnue envoyée : %s", k)
		}
	}
}

func TestNvidiaGPUUnifiedMemory(t *testing.T) {
	n := newNvidiaGPU(config.NvidiaGPUConfig{Enabled: "true", Processes: new(bool)})
	n.run, _ = fakeSMI([]map[string]string{gpuUnified}, "")
	b := model.NewSeriesBuilder(0, testTags)
	checks, st := n.collect(context.Background(), time.Now(), b, testTags)
	if st != "ok" || len(checks) != 2 || checks[0].Status != model.CheckOK {
		t.Fatalf("%q %+v", st, checks)
	}
	got := index(b.Series())
	for k := range got {
		if strings.HasPrefix(k, "gpu.memory") || strings.HasPrefix(k, "gpu.power.usage") || strings.HasPrefix(k, "gpu.ecc") ||
			strings.HasPrefix(k, "gpu.processes") {
			t.Errorf("série sans valeur réelle : %s", k)
		}
	}
	if _, ok := got["gpu.power.draw{gpu=0,gpu_name=NVIDIA GB10,gpu_uuid=GPU-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee}"]; !ok {
		t.Errorf("puissance absente : %v", got)
	}
}

func TestNvidiaGPUAbsentAndDriverFailure(t *testing.T) {
	// Coupée par défaut.
	if _, st := newNvidiaGPU(config.Default().Integrations.NvidiaGPU).collect(context.Background(), time.Now(),
		model.NewSeriesBuilder(0, testTags), testTags); st != "disabled" {
		t.Fatalf("défaut : %q", st)
	}
	// Auto sans nvidia-smi : absent, sans alerte.
	n := newNvidiaGPU(config.NvidiaGPUConfig{Enabled: "auto"})
	n.run = func(context.Context, ...string) ([]byte, error) { return nil, errNoNvidiaSMI }
	if c, st := n.collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags); c != nil || st != "absent" {
		t.Fatalf("absent : %q %+v", st, c)
	}
	// Pilote déchargé ou en désaccord avec la bibliothèque (cas classique après une mise à jour).
	n = newNvidiaGPU(config.NvidiaGPUConfig{Enabled: "true"})
	n.run = func(context.Context, ...string) ([]byte, error) {
		return []byte("Failed to initialize NVML: Driver/library version mismatch\n"),
			errors.New("nvidia-smi : Failed to initialize NVML: Driver/library version mismatch")
	}
	c, st := n.collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags)
	if len(c) != 1 || c[0].Name != "gpu.can_query" || c[0].Status != model.CheckCrit || !strings.Contains(c[0].Message, "version mismatch") ||
		!strings.HasPrefix(st, "erreur") {
		t.Fatalf("pilote : %q %+v", st, c)
	}
}

func TestProcName(t *testing.T) {
	for in, want := range map[string]string{"/usr/bin/python3": "python3", `C:\Program Files\app\vllm.exe`: "vllm.exe",
		"python3 -m vllm.entrypoints": "python3", "": "inconnu", "[N/A]": "inconnu"} {
		if got := procName(in); got != want {
			t.Errorf("procName(%q) = %q, attendu %q", in, got, want)
		}
	}
}
