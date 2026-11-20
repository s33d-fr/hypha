---
id: ai-gpu
path: /en/docs/ai-and-gpu/
title: AI and GPU: Ollama, vLLM, LiteLLM, TGI, llama.cpp and NVIDIA GPUs
nav: AI and GPU
section: Integrations
order: 29
updated: 2026-11-20
description: Monitor AI servers with HYPHA: Ollama, vLLM, LiteLLM, Text Generation Inference, llama.cpp, and NVIDIA GPUs through nvidia-smi or the DCGM exporter.
lead: Inference queues, latencies, tokens, KV cache, loaded models and GPU health, read-only by the agent, which never starts a generation.
---
## At a glance

| Software | Collection | Activation | Namespace |
|---|---|---|---|
| Ollama | local API (`/api/version`, `/api/tags`, `/api/ps`) | auto on `127.0.0.1:11434` | `ollama.*` |
| vLLM | OpenMetrics reader, `vllm` profile (port 8000) | declare it | `vllm.*` |
| LiteLLM | OpenMetrics reader, `litellm` profile (port 4000) | declare it | `litellm.*` |
| Text Generation Inference | OpenMetrics reader, `tgi` profile (port 8080) | declare it | `tgi.*` |
| llama.cpp (`llama-server`) | OpenMetrics reader, `llamacpp` profile (port 8080) | declare it | `llamacpp.*` |
| NVIDIA GPUs | `nvidia-smi` | off by default | `gpu.*` |
| NVIDIA GPUs (DCGM) | OpenMetrics reader, `dcgm` profile (port 9400) | declare it | `gpu.*` |

None of these integrations sends a generation request: the agent reads lists and metrics, it never loads a model nor incurs a cost.

## Ollama

Once a minute the agent reads three API routes: the version, the installed models (the duration of this list is the response time) and the models loaded in memory.

```yaml
integrations:
  ollama:
    url: http://127.0.0.1:11434     # default; any other address turns the integration on
    instances:                      # other Ollama servers, read from this agent
      - {name: gpu-2, url: http://192.0.2.12:11434}
```

| Metric | Meaning |
|---|---|
| `ollama.models.installed`, `ollama.models.size` | installed models, total disk space |
| `ollama.model.size` | size of each model (tags `ollama_model`, `ollama_family`, `ollama_parameters`, `ollama_quantization`) |
| `ollama.models.loaded`, `ollama.loaded.size`, `ollama.loaded.vram` | loaded models and memory in use |
| `ollama.model.loaded.size`, `.vram`, `.vram_pct` | per loaded model; below 100 %, part of the model runs on the CPU |
| `ollama.model.loaded.expires_in` | seconds before unloading (absent with `keep_alive: -1`) |
| `ollama.model.loaded.context_length` | context of the loaded model |
| `ollama.response_time` | duration of the model list, in seconds |

Check `ollama.can_connect`: version, installed and loaded models.

## Inference servers through the OpenMetrics reader

vLLM, LiteLLM, TGI and llama.cpp publish their metrics in the Prometheus format. A built-in profile sets the port, the families kept, their HYPHA names and the labels kept:

```yaml
integrations:
  openmetrics:
    - profile: vllm                          # http://127.0.0.1:8000/metrics
    - profile: litellm
      bearer_token_file: /etc/hypha/litellm.key
    - profile: tgi
      url: http://127.0.0.1:3000/metrics
    - profile: llamacpp                      # llama-server started with --metrics
```

Each target publishes the `openmetrics.can_connect` check and carries the `instance_name` tag (the profile name, unless `name` is set). How the reader works (rates, histograms, series cap) is described in [the configuration](/en/docs/configuration/).

### vLLM

- `vllm.requests.running`, `vllm.requests.waiting`: running and waiting requests.
- `vllm.kv_cache.usage`: KV cache usage, as a fraction (1 = full).
- `vllm.tokens.prompt`, `vllm.tokens.generation`: tokens per second; `vllm.preemptions`, `vllm.requests.success` (tag `finished_reason`).
- Latencies, in seconds, with `.avg`, `.p50`, `.p95`, `.p99`: `vllm.time_to_first_token`, `vllm.inter_token_latency`, `vllm.request.latency`, `vllm.request.queue_time`, `vllm.request.prefill_time`, `vllm.request.decode_time`.

Labels kept: `model_name`, `engine`, `finished_reason`. Names from earlier vLLM versions (`gpu_cache_usage_perc`, `time_per_output_token_seconds`) are read under the same HYPHA names.

### LiteLLM

Prometheus metrics are part of LiteLLM's open-source edition. Turn the callback on in its config:

```yaml
litellm_settings:
  callbacks: ["prometheus"]
```

Since version 1.85, `/metrics` requires a key: give it to the agent through `bearer_token_file` (a file only the agent can read), or serve the metrics on a separate port.

- `litellm.requests`, `litellm.requests.failed`: gateway requests and failures, per second.
- `litellm.request.latency`, `litellm.llm_api.latency`, `litellm.time_to_first_token` (seconds) and `litellm.overhead.latency` (milliseconds), with their percentiles.
- `litellm.tokens.input`, `.output`, `.total`, `litellm.spend` (spend estimated by LiteLLM, per second).
- `litellm.deployment.state` (0 healthy, 1 partial outage, 2 outage), `litellm.deployment.requests`, `.success`, `.failures`, `.cooled_down`.
- `litellm.requests.in_flight`, `litellm.remaining.requests`, `litellm.remaining.tokens`, `litellm.cache.hits`, `.misses`.

Labels kept: requested model, model, model group, provider, response code, error class. The user, their e-mail address, their IP address and the hash of their key are never kept.

### Text Generation Inference

- `tgi.queue.size`, `tgi.batch.size`, `tgi.batch.max_tokens`: queue and current batch.
- `tgi.requests`, `tgi.requests.success`, `tgi.requests.failure` (tag `err`).
- Durations in seconds, with percentiles: `tgi.request.duration`, `.queue_duration`, `.inference_duration`, `.time_per_token`; `tgi.request.generated_tokens`, `tgi.request.input_length`.

The profile follows the names of the TGI documentation. The Docker image listens on port 80 inside the container: set the `url` of the published port.

### llama.cpp

Start `llama-server` with `--metrics`.

- `llamacpp.requests.processing`, `llamacpp.requests.deferred` (requests waiting for a free slot).
- `llamacpp.tokens.prompt`, `.prompt_cached`, `.predicted`: tokens per second.
- `llamacpp.prompt.throughput`, `llamacpp.predicted.throughput`: average throughput in tokens per second.
- `llamacpp.decode.calls`, `llamacpp.decode.busy_slots`.

## NVIDIA GPUs

### Through nvidia-smi

Without an exporter, the agent reads `nvidia-smi` every 30 seconds. The integration is off by default:

```yaml
integrations:
  nvidia_gpu:
    enabled: auto          # on when nvidia-smi is present; true to force it
    # path: /usr/bin/nvidia-smi
    # processes: false     # without the list of compute processes
```

| Metric | Meaning |
|---|---|
| `gpu.count` | number of GPUs |
| `gpu.utilization`, `gpu.memory.utilization` | GPU and memory controller usage (%) |
| `gpu.memory.total`, `.used`, `.free` (bytes), `gpu.memory.usage` (%) | GPU memory |
| `gpu.temperature`, `gpu.memory.temperature` | temperatures (°C) |
| `gpu.power.draw`, `gpu.power.limit` (W), `gpu.power.usage` (%) | power |
| `gpu.clock.sm`, `gpu.clock.mem`, `gpu.clock.sm.max` | clocks (MHz) |
| `gpu.fan.speed`, `gpu.pstate` | fan (%), performance state (0 = P0) |
| `gpu.ecc.corrected`, `gpu.ecc.uncorrected` | ECC errors since the driver was loaded |
| `gpu.throttle.thermal` | 1 when clocks are reduced because of heat |
| `gpu.processes`, `gpu.process.memory` | compute processes and memory per program (`process_name`) |

Tags: `gpu` (index), `gpu_uuid`, `gpu_name`. Checks: `gpu.can_query` (nvidia-smi answers, driver loaded and matching its library) and `gpu.health` per GPU (critical: uncorrected ECC errors; warning: thermal slowdown).

Values the GPU does not provide (`[N/A]`, unified memory of Grace and DGX Spark machines, no fan) are not sent. A field unknown to the driver is removed from the query and reading resumes.

### Through the DCGM exporter

With `dcgm-exporter` (Kubernetes, compute clusters), declare the `dcgm` profile:

```yaml
integrations:
  openmetrics:
    - profile: dcgm        # http://127.0.0.1:9400/metrics
```

Metrics take the same names as the nvidia-smi ones when the unit is the same (`gpu.utilization`, `gpu.temperature`, `gpu.power.draw`, `gpu.clock.sm`, `gpu.memory.used` in bytes since dcgm-exporter 4.9); the others keep their own name: `gpu.fb.used_mib`, `gpu.fb.free_mib` (MiB, up to 4.8), `gpu.utilization_ratio` (fraction, 4.9), `gpu.xid_error`, `gpu.pcie.replays`, `gpu.remapped_rows.*`, `gpu.engine.active`, `gpu.tensor.active`, `gpu.dram.active`. Tags: `gpu`, `uuid`, `modelname`, and `pod`, `namespace`, `container` on Kubernetes. Memory temperature is not kept: DCGM reports a sentinel value on GPUs without a sensor.

Pick nvidia-smi or DCGM on a given machine, not both.

## Monitor templates

**IA et GPU** (AI and GPU) family of the catalog: Ollama unreachable or slow, inference server unreachable (by `instance_name`), vLLM or TGI inference queue, vLLM time to first token, vLLM KV cache saturated, requests deferred by llama.cpp, LiteLLM deployment down, GPU too hot, GPU saturated, GPU memory full, GPU faulty, nvidia-smi failing, GPU XID error.

## Ready-made dashboards

**Dashboards › Catalog**, **IA et GPU** (AI and GPU) family: NVIDIA GPUs, Ollama, vLLM, LiteLLM, Text Generation Inference and llama.cpp.
