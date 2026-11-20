package openmetrics

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Profils de la vague 5 (IA et GPU) : chaque exposition passe deux fois dans le convertisseur
// (débits et histogrammes demandent deux lectures), puis les noms HYPHA attendus sont cherchés.

func convertTwice(t *testing.T, profile, first, second string) []point {
	t.Helper()
	cfg := profileInstance(t, "integrations:\n  openmetrics:\n    - profile: "+profile+"\n")
	c := newConverter(cfg)
	fams, bad, err := Parse(strings.NewReader(first))
	if err != nil || bad != 0 {
		t.Fatalf("%s : exposition refusée (%v, %d lignes)", profile, err, bad)
	}
	t0 := time.Unix(1000, 0)
	c.Convert(fams, t0)
	fams, _, _ = Parse(strings.NewReader(second))
	return c.Convert(fams, t0.Add(10*time.Second))
}

func mustFind(t *testing.T, pts []point, want float64, name string, kv ...string) {
	t.Helper()
	p, ok := find(pts, name, kv...)
	if !ok {
		var names []string
		for _, p := range pts {
			names = append(names, p.name+strings.Join(p.kv, ","))
		}
		t.Fatalf("%s %v absent ; reçu : %v", name, kv, names)
	}
	if want >= 0 && (p.value < want-1e-9 || p.value > want+1e-9) {
		t.Fatalf("%s %v = %v, attendu %v", name, kv, p.value, want)
	}
}

// Exposition vLLM au format de prometheus_client, noms de la doc officielle
// (https://docs.vllm.ai/en/latest/usage/metrics.html), valeurs construites.
const vllmExp = `# HELP vllm:num_requests_running Number of requests in model execution batches.
# TYPE vllm:num_requests_running gauge
vllm:num_requests_running{engine="0",model_name="Qwen/Qwen3-0.6B"} 3.0
# HELP vllm:num_requests_waiting Number of requests waiting to be processed.
# TYPE vllm:num_requests_waiting gauge
vllm:num_requests_waiting{engine="0",model_name="Qwen/Qwen3-0.6B"} 7.0
# HELP vllm:kv_cache_usage_perc KV-cache usage. 1 means 100 percent usage.
# TYPE vllm:kv_cache_usage_perc gauge
vllm:kv_cache_usage_perc{engine="0",model_name="Qwen/Qwen3-0.6B"} 0.42
# HELP vllm:prompt_tokens_total Number of prefill tokens processed.
# TYPE vllm:prompt_tokens_total counter
vllm:prompt_tokens_total{engine="0",model_name="Qwen/Qwen3-0.6B"} PROMPT
# HELP vllm:request_success_total Count of successfully processed requests.
# TYPE vllm:request_success_total counter
vllm:request_success_total{engine="0",finished_reason="stop",model_name="Qwen/Qwen3-0.6B"} 10.0
vllm:request_success_total{engine="0",finished_reason="length",model_name="Qwen/Qwen3-0.6B"} 2.0
# HELP vllm:time_to_first_token_seconds Histogram of time to first token in seconds.
# TYPE vllm:time_to_first_token_seconds histogram
vllm:time_to_first_token_seconds_bucket{engine="0",le="0.1",model_name="Qwen/Qwen3-0.6B"} B1
vllm:time_to_first_token_seconds_bucket{engine="0",le="0.5",model_name="Qwen/Qwen3-0.6B"} B2
vllm:time_to_first_token_seconds_bucket{engine="0",le="+Inf",model_name="Qwen/Qwen3-0.6B"} B3
vllm:time_to_first_token_seconds_count{engine="0",model_name="Qwen/Qwen3-0.6B"} B3
vllm:time_to_first_token_seconds_sum{engine="0",model_name="Qwen/Qwen3-0.6B"} SUM
# HELP vllm:num_preemptions_total Cumulative number of preemption from the engine.
# TYPE vllm:num_preemptions_total counter
vllm:num_preemptions_total{engine="0",model_name="Qwen/Qwen3-0.6B"} 0.0
# HELP python_gc_objects_collected_total Objects collected during gc
# TYPE python_gc_objects_collected_total counter
python_gc_objects_collected_total{generation="0"} 1234.0
`

func TestVLLMProfile(t *testing.T) {
	r := func(prompt, b1, b2, b3, sum string) string {
		return strings.NewReplacer("PROMPT", prompt, "B1", b1, "B2", b2, "B3", b3, "SUM", sum).Replace(vllmExp)
	}
	pts := convertTwice(t, "vllm", r("1000", "0", "0", "0", "0"), r("3000", "5", "10", "10", "2"))
	m := []string{"engine", "0", "model_name", "Qwen/Qwen3-0.6B"}
	mustFind(t, pts, 3, "vllm.requests.running", m...)
	mustFind(t, pts, 7, "vllm.requests.waiting", m...)
	mustFind(t, pts, 0.42, "vllm.kv_cache.usage", m...)
	mustFind(t, pts, 200, "vllm.tokens.prompt", m...) // (3000 − 1000) / 10 s
	mustFind(t, pts, 0.2, "vllm.time_to_first_token.avg", m...)
	mustFind(t, pts, 0.1, "vllm.time_to_first_token.p50", m...)
	mustFind(t, pts, 0, "vllm.requests.success", "engine", "0", "finished_reason", "stop", "model_name", "Qwen/Qwen3-0.6B")
	for _, p := range pts {
		if strings.HasPrefix(p.name, "vllm.python") || strings.Contains(p.name, "gc_objects") {
			t.Fatalf("famille hors profil : %s", p.name)
		}
	}
}

// Exposition de dcgm-exporter : les trois premières familles et leurs échantillons sont ceux de
// l'exemple de la documentation de NVIDIA (https://docs.nvidia.com/datacenter/cloud-native/
// gpu-telemetry/latest/dcgm-exporter.html, « curl localhost:9400/metrics »), sentinelle de la
// température mémoire comprise ; les suivantes reprennent les champs et les aides de
// etc/default-counters.csv de dcgm-exporter 4.8.4, valeurs construites. NON ÉPROUVÉ sur un vrai GPU.
const dcgmDocExp = `# HELP DCGM_FI_DEV_SM_CLOCK SM clock frequency (in MHz).
# TYPE DCGM_FI_DEV_SM_CLOCK gauge
# HELP DCGM_FI_DEV_MEM_CLOCK Memory clock frequency (in MHz).
# TYPE DCGM_FI_DEV_MEM_CLOCK gauge
# HELP DCGM_FI_DEV_MEMORY_TEMP Memory temperature (in C).
# TYPE DCGM_FI_DEV_MEMORY_TEMP gauge
DCGM_FI_DEV_SM_CLOCK{gpu="0", UUID="GPU-604ac76c-d9cf-fef3-62e9-d92044ab6e52"} 139
DCGM_FI_DEV_MEM_CLOCK{gpu="0", UUID="GPU-604ac76c-d9cf-fef3-62e9-d92044ab6e52"} 405
DCGM_FI_DEV_MEMORY_TEMP{gpu="0", UUID="GPU-604ac76c-d9cf-fef3-62e9-d92044ab6e52"} 9223372036854775794
# HELP DCGM_FI_DEV_GPU_TEMP GPU temperature (in C).
# TYPE DCGM_FI_DEV_GPU_TEMP gauge
DCGM_FI_DEV_GPU_TEMP{gpu="0", UUID="GPU-604ac76c-d9cf-fef3-62e9-d92044ab6e52", modelName="NVIDIA A100-SXM4-40GB", Hostname="node1"} 34
# HELP DCGM_FI_DEV_GPU_UTIL GPU utilization (in %).
# TYPE DCGM_FI_DEV_GPU_UTIL gauge
DCGM_FI_DEV_GPU_UTIL{gpu="0", UUID="GPU-604ac76c-d9cf-fef3-62e9-d92044ab6e52", modelName="NVIDIA A100-SXM4-40GB", Hostname="node1"} 87
# HELP DCGM_FI_DEV_FB_USED Framebuffer memory used (in MiB).
# TYPE DCGM_FI_DEV_FB_USED gauge
DCGM_FI_DEV_FB_USED{gpu="0", UUID="GPU-604ac76c-d9cf-fef3-62e9-d92044ab6e52", modelName="NVIDIA A100-SXM4-40GB", Hostname="node1"} 30720
# HELP DCGM_FI_DEV_PCIE_REPLAY_COUNTER Total number of PCIe retries.
# TYPE DCGM_FI_DEV_PCIE_REPLAY_COUNTER counter
DCGM_FI_DEV_PCIE_REPLAY_COUNTER{gpu="0", UUID="GPU-604ac76c-d9cf-fef3-62e9-d92044ab6e52", modelName="NVIDIA A100-SXM4-40GB", Hostname="node1"} @R@
# HELP DCGM_FI_DEV_XID_ERRORS Value of the last XID error encountered.
# TYPE DCGM_FI_DEV_XID_ERRORS gauge
DCGM_FI_DEV_XID_ERRORS{gpu="0", UUID="GPU-604ac76c-d9cf-fef3-62e9-d92044ab6e52", modelName="NVIDIA A100-SXM4-40GB", Hostname="node1"} 0
`

// Noms de dcgm-exporter 4.9 (etc/default-counters.csv de la branche principale), valeurs construites.
const dcgm49Exp = `# HELP DCGM_FI_DEV_GPU_UTIL_RATIO GPU utilization as a ratio.
# TYPE DCGM_FI_DEV_GPU_UTIL_RATIO gauge
DCGM_FI_DEV_GPU_UTIL_RATIO{gpu="0",UUID="GPU-604ac76c-d9cf-fef3-62e9-d92044ab6e52",modelName="NVIDIA H100 80GB HBM3",hostname="node1"} 0.87
# HELP DCGM_FI_DEV_FB_USED_BYTES Used framebuffer memory in bytes.
# TYPE DCGM_FI_DEV_FB_USED_BYTES gauge
DCGM_FI_DEV_FB_USED_BYTES{gpu="0",UUID="GPU-604ac76c-d9cf-fef3-62e9-d92044ab6e52",modelName="NVIDIA H100 80GB HBM3",hostname="node1"} 3.221225472e+10
# HELP DCGM_FI_DEV_GPU_TEMP_CELSIUS GPU temperature (in C).
# TYPE DCGM_FI_DEV_GPU_TEMP_CELSIUS gauge
DCGM_FI_DEV_GPU_TEMP_CELSIUS{gpu="0",UUID="GPU-604ac76c-d9cf-fef3-62e9-d92044ab6e52",modelName="NVIDIA H100 80GB HBM3",hostname="node1"} 61
`

func TestDCGMProfile(t *testing.T) {
	pts := convertTwice(t, "dcgm", strings.Replace(dcgmDocExp, "@R@", "4", 1), strings.Replace(dcgmDocExp, "@R@", "9", 1))
	u := []string{"gpu", "0", "uuid", "GPU-604ac76c-d9cf-fef3-62e9-d92044ab6e52"}
	full := []string{"gpu", "0", "modelname", "NVIDIA A100-SXM4-40GB", "uuid", "GPU-604ac76c-d9cf-fef3-62e9-d92044ab6e52"}
	mustFind(t, pts, 139, "gpu.clock.sm", u...)
	mustFind(t, pts, 405, "gpu.clock.mem", u...)
	mustFind(t, pts, 34, "gpu.temperature", full...)
	mustFind(t, pts, 87, "gpu.utilization", full...)
	mustFind(t, pts, 30720, "gpu.fb.used_mib", full...)
	mustFind(t, pts, 0.5, "gpu.pcie.replays", full...) // (9 − 4) / 10 s
	mustFind(t, pts, 0, "gpu.xid_error", full...)
	for _, p := range pts {
		if p.value > 1e15 || strings.Contains(p.name, "MEMORY_TEMP") || strings.Contains(p.name, "memory.temperature") {
			t.Fatalf("sentinelle de DCGM envoyée : %s = %v", p.name, p.value)
		}
		for i := 0; i < len(p.kv); i += 2 {
			if p.kv[i] == "hostname" {
				t.Fatalf("étiquette Hostname gardée : %v", p.kv)
			}
		}
	}
	pts = convertTwice(t, "dcgm", dcgm49Exp, dcgm49Exp)
	h := []string{"gpu", "0", "modelname", "NVIDIA H100 80GB HBM3", "uuid", "GPU-604ac76c-d9cf-fef3-62e9-d92044ab6e52"}
	mustFind(t, pts, 0.87, "gpu.utilization_ratio", h...)
	mustFind(t, pts, 32212254720, "gpu.memory.used", h...)
	mustFind(t, pts, 61, "gpu.temperature", h...)
}

// Exposition de TGI (metrics-exporter-prometheus), noms de la doc officielle
// (https://huggingface.co/docs/text-generation-inference/reference/metrics), valeurs construites.
// NON ÉPROUVÉ contre un vrai TGI.
const tgiExp = `# TYPE tgi_queue_size gauge
tgi_queue_size 4
# TYPE tgi_batch_current_size gauge
tgi_batch_current_size 8
# TYPE tgi_request_count counter
tgi_request_count COUNT
# TYPE tgi_request_failure counter
tgi_request_failure{err="validation"} 1
# TYPE tgi_request_duration histogram
tgi_request_duration_bucket{le="0.5"} D1
tgi_request_duration_bucket{le="2"} D2
tgi_request_duration_bucket{le="+Inf"} D2
tgi_request_duration_sum DSUM
tgi_request_duration_count D2
`

func TestTGIProfile(t *testing.T) {
	r := func(count, d1, d2, sum string) string {
		return strings.NewReplacer("COUNT", count, "D1", d1, "D2", d2, "DSUM", sum).Replace(tgiExp)
	}
	pts := convertTwice(t, "tgi", r("100", "0", "0", "0"), r("150", "10", "20", "15"))
	mustFind(t, pts, 4, "tgi.queue.size")
	mustFind(t, pts, 8, "tgi.batch.size")
	mustFind(t, pts, 5, "tgi.requests")
	mustFind(t, pts, 0.75, "tgi.request.duration.avg")
	mustFind(t, pts, 0, "tgi.requests.failure", "err", "validation")
}

// Captures réelles relevées dans un environnement d'essai le 20/11/2026 : llama-server build 12464 (modèle
// SmolLM2 135M, trois complétions de 24 jetons) et LiteLLM 1.110.1, édition libre sans licence
// (extrait des familles utiles ; empreintes de clé remplacées par « x »).
func readTestdata(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestLlamaCppProfileRealCapture(t *testing.T) {
	real := readTestdata(t, "llamacpp-b12464.txt")
	before := strings.NewReplacer("llamacpp:tokens_predicted_total 72", "llamacpp:tokens_predicted_total 32",
		"llamacpp:prompt_tokens_total 5", "llamacpp:prompt_tokens_total 1").Replace(real)
	pts := convertTwice(t, "llamacpp", before, real)
	mustFind(t, pts, 4, "llamacpp.tokens.predicted") // (72 − 32) / 10 s
	mustFind(t, pts, 0.4, "llamacpp.tokens.prompt")
	mustFind(t, pts, 0, "llamacpp.tokens.prompt_cached")
	mustFind(t, pts, 17.4847, "llamacpp.predicted.throughput")
	mustFind(t, pts, 41.6955, "llamacpp.prompt.throughput")
	mustFind(t, pts, 0, "llamacpp.requests.deferred")
	mustFind(t, pts, 0, "llamacpp.requests.processing")
	mustFind(t, pts, 1, "llamacpp.decode.busy_slots")
	for _, p := range pts {
		if strings.Contains(p.name, "spec_decode") || strings.Contains(p.name, "n_tokens_max") {
			t.Fatalf("famille hors profil : %s", p.name)
		}
	}
}

func TestLiteLLMProfileRealCapture(t *testing.T) {
	real := readTestdata(t, "litellm-1.110.1.txt")
	pts := convertTwice(t, "litellm", real, real)
	mustFind(t, pts, 1, "litellm.requests.in_flight")
	mustFind(t, pts, 0, "litellm.deployment.state", "api_provider", "ollama", "litellm_model_name", "smollm2:135m")
	mustFind(t, pts, 0, "litellm.requests", "api_provider", "ollama", "requested_model", "smollm2-ollama", "status_code", "200")
	mustFind(t, pts, 0, "litellm.llm_api.latency.count", "api_provider", "ollama", "model", "smollm2:135m", "model_group", "smollm2-ollama",
		"requested_model", "smollm2-ollama")
	for _, p := range pts {
		for i := 0; i < len(p.kv); i += 2 {
			switch p.kv[i] {
			case "client_ip", "user_email", "end_user", "hashed_api_key", "user", "team", "api_base", "model_id":
				t.Fatalf("étiquette personnelle ou trop fine gardée : %s %v", p.name, p.kv)
			}
		}
	}
}
