package integrations

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── GPU NVIDIA (vague 5) : nvidia-smi, sans exportateur DCGM ───────────────────────────────
//
// Une commande pour tous les GPU (--query-gpu, CSV sans unités) et une pour les processus de
// calcul (--query-compute-apps). Utilisation, mémoire, températures, puissance, horloges,
// ventilateur, état de performance, erreurs ECC depuis le chargement du pilote, ralentissement
// thermique. Un champ que le pilote ne connaît pas (versions anciennes ou récentes) est retiré
// de la requête, puis la lecture reprend. Les valeurs « [N/A] » ou « [Not Supported] » (mémoire
// unifiée des DGX Spark et Grace, GPU sans capteur) sont simplement absentes.

// gpuFields : champs demandés, dans l'ordre ; les trois premiers sont indispensables.
var gpuFields = []string{
	"index", "uuid", "name", "driver_version", "pstate",
	"utilization.gpu", "utilization.memory", "memory.total", "memory.used", "memory.free",
	"temperature.gpu", "temperature.memory", "power.draw", "power.limit",
	"clocks.sm", "clocks.mem", "clocks.max.sm", "fan.speed",
	"ecc.errors.corrected.volatile.total", "ecc.errors.uncorrected.volatile.total",
	"clocks_throttle_reasons.hw_thermal_slowdown", "clocks_throttle_reasons.sw_thermal_slowdown",
}

const gpuMaxProcesses = 20 // noms de processus gardés par GPU (les plus gourmands)

type NvidiaGPU struct {
	cfg    config.NvidiaGPUConfig
	det    detector
	run    func(ctx context.Context, args ...string) ([]byte, error)
	fields []string
}

func newNvidiaGPU(cfg config.NvidiaGPUConfig) *NvidiaGPU {
	n := &NvidiaGPU{cfg: cfg, det: detector{mode: cfg.Enabled}, fields: append([]string{}, gpuFields...)}
	n.run = func(ctx context.Context, args ...string) ([]byte, error) {
		path, ok := nvidiaSMIPath(cfg.Path)
		if !ok {
			return nil, errNoNvidiaSMI
		}
		return runTool(ctx, 15*time.Second, path, args...)
	}
	return n
}

var errNoNvidiaSMI = errors.New("nvidia-smi introuvable")

// nvidiaSMIPath : chemin de la conf, sinon le PATH, sinon le dossier historique sous Windows.
func nvidiaSMIPath(configured string) (string, bool) {
	if configured != "" {
		if fi, err := os.Stat(configured); err == nil && !fi.IsDir() {
			return configured, true
		}
		return "", false
	}
	if p, ok := findTool("nvidia-smi"); ok {
		return p, true
	}
	if runtime.GOOS == "windows" {
		for _, p := range []string{
			filepath.Join(os.Getenv("SystemRoot"), "System32", "nvidia-smi.exe"),
			filepath.Join(os.Getenv("ProgramFiles"), "NVIDIA Corporation", "NVSMI", "nvidia-smi.exe"),
		} {
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
				return p, true
			}
		}
	}
	return "", false
}

func (n *NvidiaGPU) name() string          { return "nvidia_gpu" }
func (n *NvidiaGPU) period() time.Duration { return 30 * time.Second }
func (n *NvidiaGPU) close()                {}

var invalidFieldRe = regexp.MustCompile(`Field "([^"]+)" is not a valid field to query`)

// queryGPUs lance --query-gpu ; un champ refusé par le pilote est retiré (s'il n'est pas
// indispensable) et la requête relancée, cinq fois au plus.
func (n *NvidiaGPU) queryGPUs(ctx context.Context) ([]map[string]string, error) {
	for try := 0; try < 6; try++ {
		out, err := n.run(ctx, "--query-gpu="+strings.Join(n.fields, ","), "--format=csv,noheader,nounits")
		if err != nil {
			m := invalidFieldRe.FindStringSubmatch(string(out) + " " + err.Error())
			if m == nil || try == 5 {
				return nil, err
			}
			bad := m[1]
			kept := n.fields[:0:0]
			for i, f := range n.fields {
				if f != bad || i < 3 {
					kept = append(kept, f)
				}
			}
			if len(kept) == len(n.fields) {
				return nil, err
			}
			n.fields = kept
			continue
		}
		return parseGPUCSV(out, n.fields)
	}
	return nil, errors.New("nvidia-smi : requête impossible")
}

// parseGPUCSV : une ligne par GPU, colonnes dans l'ordre des champs demandés.
func parseGPUCSV(out []byte, fields []string) ([]map[string]string, error) {
	r := csv.NewReader(bytes.NewReader(out))
	r.TrimLeadingSpace = true
	r.FieldsPerRecord = -1
	var gpus []map[string]string
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("sortie de nvidia-smi illisible : %v", err)
		}
		if len(rec) != len(fields) {
			if len(rec) == 1 && strings.TrimSpace(rec[0]) == "" {
				continue
			}
			return nil, fmt.Errorf("sortie de nvidia-smi inattendue : %d colonnes pour %d champs", len(rec), len(fields))
		}
		g := map[string]string{}
		for i, f := range fields {
			g[f] = strings.TrimSpace(rec[i])
		}
		gpus = append(gpus, g)
	}
	return gpus, nil
}

// gpuNum : valeur numérique, faux pour « [N/A] », « [Not Supported] », « [Unknown Error] »…
func gpuNum(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "[") || strings.EqualFold(s, "N/A") {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil
}

type gpuProc struct {
	uuid, name string
	mem        float64
	hasMem     bool
}

// queryProcesses : processus de calcul (uuid du GPU, pid, nom, mémoire en Mio).
func (n *NvidiaGPU) queryProcesses(ctx context.Context) ([]gpuProc, error) {
	out, err := n.run(ctx, "--query-compute-apps=gpu_uuid,pid,process_name,used_memory", "--format=csv,noheader,nounits")
	if err != nil {
		return nil, err
	}
	r := csv.NewReader(bytes.NewReader(out))
	r.TrimLeadingSpace = true
	r.FieldsPerRecord = -1
	var procs []gpuProc
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("processus illisibles : %v", err)
		}
		if len(rec) < 4 || !strings.HasPrefix(strings.TrimSpace(rec[0]), "GPU-") {
			continue // « No running processes found » et lignes vides
		}
		p := gpuProc{uuid: strings.TrimSpace(rec[0]), name: procName(rec[2])}
		p.mem, p.hasMem = gpuNum(rec[3])
		procs = append(procs, p)
	}
	return procs, nil
}

// procName : nom court du programme (sans chemin ni arguments).
func procName(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "[") { // « [N/A] », « [Insufficient Permissions] »
		return "inconnu"
	}
	s = strings.ReplaceAll(s, `\`, "/")
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		s = s[i+1:]
	}
	if f := strings.Fields(s); len(f) > 0 {
		s = f[0]
	}
	if s == "" {
		s = "inconnu"
	}
	return s
}

const mib = 1 << 20

func (n *NvidiaGPU) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := n.det.skip(now); skip {
		return nil, st
	}
	ts := model.Ms(now)
	fail := func(err error) ([]model.Check, string) {
		if silent, st := n.det.failed(err.Error()); silent {
			return nil, st
		}
		return []model.Check{{TS: ts, Name: "gpu.can_query", Status: model.CheckCrit, Message: err.Error(), Tags: tags()}},
			"erreur : " + err.Error()
	}
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	gpus, err := n.queryGPUs(ctx)
	if err != nil {
		return fail(err)
	}
	if len(gpus) == 0 {
		return fail(errors.New("nvidia-smi ne voit aucun GPU"))
	}
	n.det.ok()
	b.Gauge("gpu.count", "device", float64(len(gpus)))
	checks := []model.Check{}
	byUUID := map[string][]string{}
	driver := ""
	for _, g := range gpus {
		kv := []string{"gpu", g["index"], "gpu_uuid", g["uuid"], "gpu_name", g["name"]}
		byUUID[g["uuid"]] = kv
		if driver == "" {
			driver = g["driver_version"]
		}
		gauge := func(metric, unit, field string, scale float64) (float64, bool) {
			v, ok := gpuNum(g[field])
			if ok {
				b.Gauge(metric, unit, v*scale, kv...)
			}
			return v, ok
		}
		gauge("gpu.utilization", "percent", "utilization.gpu", 1)
		gauge("gpu.memory.utilization", "percent", "utilization.memory", 1)
		total, okT := gauge("gpu.memory.total", "byte", "memory.total", mib)
		used, okU := gauge("gpu.memory.used", "byte", "memory.used", mib)
		gauge("gpu.memory.free", "byte", "memory.free", mib)
		if okT && okU && total > 0 {
			b.Gauge("gpu.memory.usage", "percent", round1(100*used/total), kv...)
		}
		gauge("gpu.temperature", "celsius", "temperature.gpu", 1)
		gauge("gpu.memory.temperature", "celsius", "temperature.memory", 1)
		draw, okD := gauge("gpu.power.draw", "watt", "power.draw", 1)
		limit, okL := gauge("gpu.power.limit", "watt", "power.limit", 1)
		if okD && okL && limit > 0 {
			b.Gauge("gpu.power.usage", "percent", round1(100*draw/limit), kv...)
		}
		gauge("gpu.clock.sm", "megahertz", "clocks.sm", 1)
		gauge("gpu.clock.mem", "megahertz", "clocks.mem", 1)
		gauge("gpu.clock.sm.max", "megahertz", "clocks.max.sm", 1)
		gauge("gpu.fan.speed", "percent", "fan.speed", 1)
		if p := strings.TrimPrefix(g["pstate"], "P"); p != g["pstate"] {
			if v, ok := gpuNum(p); ok {
				b.Gauge("gpu.pstate", "", v, kv...)
			}
		}
		corr, _ := gauge("gpu.ecc.corrected", "error", "ecc.errors.corrected.volatile.total", 1)
		uncorr, okE := gauge("gpu.ecc.uncorrected", "error", "ecc.errors.uncorrected.volatile.total", 1)
		thermal := false
		for _, f := range []string{"clocks_throttle_reasons.hw_thermal_slowdown", "clocks_throttle_reasons.sw_thermal_slowdown"} {
			if v, ok := g[f]; ok && strings.EqualFold(v, "Active") {
				thermal = true
			}
		}
		if _, known := g["clocks_throttle_reasons.hw_thermal_slowdown"]; known {
			v := 0.0
			if thermal {
				v = 1
			}
			b.Gauge("gpu.throttle.thermal", "", v, kv...)
		}
		label := fmt.Sprintf("GPU %s (%s)", g["index"], g["name"])
		st, msg := model.CheckOK, label+" : sain"
		switch {
		case okE && uncorr > 0:
			st, msg = model.CheckCrit, fmt.Sprintf("%s : %g erreur(s) ECC non corrigée(s) depuis le chargement du pilote", label, uncorr)
		case thermal:
			st, msg = model.CheckWarn, label+" : horloges réduites pour cause de chaleur"
		case corr > 0:
			msg = fmt.Sprintf("%s : sain (%g erreur(s) ECC corrigée(s))", label, corr)
		}
		checks = append(checks, model.Check{TS: ts, Name: "gpu.health", Status: st, Message: msg, Tags: tags(kv...)})
	}

	state := "ok"
	if n.cfg.ProcessesOn() {
		procs, err := n.queryProcesses(ctx)
		if err != nil {
			state = "partiel : processus illisibles (" + err.Error() + ")"
		} else {
			count := map[string]float64{}
			mem := map[string]map[string]float64{}
			for _, p := range procs {
				count[p.uuid]++
				if p.hasMem {
					if mem[p.uuid] == nil {
						mem[p.uuid] = map[string]float64{}
					}
					mem[p.uuid][p.name] += p.mem * mib
				}
			}
			for uuid, kv := range byUUID {
				b.Gauge("gpu.processes", "process", count[uuid], kv...)
				names := make([]string, 0, len(mem[uuid]))
				for name := range mem[uuid] {
					names = append(names, name)
				}
				sort.Slice(names, func(i, j int) bool { return mem[uuid][names[i]] > mem[uuid][names[j]] })
				for i, name := range names {
					if i >= gpuMaxProcesses {
						break
					}
					b.Gauge("gpu.process.memory", "byte", mem[uuid][name], append(append([]string{}, kv...), "process_name", name)...)
				}
			}
		}
	}
	msg := fmt.Sprintf("%d GPU NVIDIA lu(s) par nvidia-smi", len(gpus))
	if driver != "" {
		msg += ", pilote " + driver
	}
	checks = append(checks, model.Check{TS: ts, Name: "gpu.can_query", Status: model.CheckOK, Message: msg, Tags: tags()})
	return checks, state
}
