package omprofiles

// Vague 5 des intégrations (IA et GPU) : serveurs d'inférence et GPU NVIDIA lus au format
// Prometheus. Ollama (API JSON) et nvidia-smi (sans exportateur) ont leurs sondes dédiées dans
// le paquet integrations.

func init() {
	// vLLM (serveur compatible OpenAI, port 8000) : requêtes en cours et en attente, cache KV,
	// jetons, préemptions, latences (premier jeton, entre jetons, de bout en bout, file d'attente,
	// préremplissage, décodage). Noms de la doc officielle (docs.vllm.ai, « Metrics ») ; les
	// anciens noms (gpu_cache_usage_perc, time_per_output_token_seconds, num_requests_swapped)
	// restent lus pour les versions antérieures. kv_cache.usage est une fraction (1 = plein).
	register(Profile{
		Name: "vllm", Description: "vLLM (serveur d'inférence compatible OpenAI)", Port: 8000,
		Namespace: "vllm",
		Metrics: []string{
			"vllm:num_requests_running", "vllm:num_requests_waiting", "vllm:num_requests_swapped",
			"vllm:kv_cache_usage_perc", "vllm:gpu_cache_usage_perc", "vllm:cpu_cache_usage_perc",
			"vllm:prompt_tokens_total", "vllm:generation_tokens_total", "vllm:num_preemptions_total",
			"vllm:request_success_total", "vllm:prefix_cache_queries_total", "vllm:prefix_cache_hits_total",
			"vllm:time_to_first_token_seconds", "vllm:inter_token_latency_seconds", "vllm:time_per_output_token_seconds",
			"vllm:request_time_per_output_token_seconds", "vllm:e2e_request_latency_seconds",
			"vllm:request_queue_time_seconds", "vllm:request_inference_time_seconds",
			"vllm:request_prefill_time_seconds", "vllm:request_decode_time_seconds",
		},
		Rename: map[string]string{
			"vllm:num_requests_running":                  "requests.running",
			"vllm:num_requests_waiting":                  "requests.waiting",
			"vllm:num_requests_swapped":                  "requests.swapped",
			"vllm:kv_cache_usage_perc":                   "kv_cache.usage",
			"vllm:gpu_cache_usage_perc":                  "kv_cache.usage",
			"vllm:cpu_cache_usage_perc":                  "kv_cache.cpu_usage",
			"vllm:prompt_tokens_total":                   "tokens.prompt",
			"vllm:generation_tokens_total":               "tokens.generation",
			"vllm:num_preemptions_total":                 "preemptions",
			"vllm:request_success_total":                 "requests.success",
			"vllm:prefix_cache_queries_total":            "prefix_cache.queries",
			"vllm:prefix_cache_hits_total":               "prefix_cache.hits",
			"vllm:time_to_first_token_seconds":           "time_to_first_token",
			"vllm:inter_token_latency_seconds":           "inter_token_latency",
			"vllm:time_per_output_token_seconds":         "inter_token_latency",
			"vllm:request_time_per_output_token_seconds": "request.time_per_output_token",
			"vllm:e2e_request_latency_seconds":           "request.latency",
			"vllm:request_queue_time_seconds":            "request.queue_time",
			"vllm:request_inference_time_seconds":        "request.inference_time",
			"vllm:request_prefill_time_seconds":          "request.prefill_time",
			"vllm:request_decode_time_seconds":           "request.decode_time",
		},
		Labels: []string{"model_name", "engine", "finished_reason"},
	})

	// LiteLLM (passerelle vers les fournisseurs de modèles, port 4000) : requêtes et échecs de la
	// passerelle, latences (totale, de l'API du fournisseur, surcoût de LiteLLM, premier jeton),
	// jetons, dépense, déploiements (requêtes, réponses en échec, état, mises au repos), limites
	// restantes, requêtes en vol, cache. Édition libre (licence MIT) : éprouvé sur LiteLLM 1.110.1 sans
	// licence. /metrics demande une clé depuis la 1.85 (bearer_token_file) et renvoie vers /metrics/.
	// Latences en secondes, sauf overhead.latency en millisecondes (aide de la famille).
	register(Profile{
		Name: "litellm", Description: "LiteLLM (passerelle de modèles d'IA)", Port: 4000,
		Namespace: "litellm",
		Metrics: []string{
			"litellm_proxy_total_requests_metric_total", "litellm_proxy_failed_requests_metric_total",
			"litellm_request_total_latency_metric", "litellm_llm_api_latency_metric", "litellm_overhead_latency_metric",
			"litellm_llm_api_time_to_first_token_metric",
			"litellm_total_tokens_metric_total", "litellm_input_tokens_metric_total", "litellm_output_tokens_metric_total",
			"litellm_spend_metric_total",
			"litellm_deployment_total_requests_total", "litellm_deployment_success_responses_total",
			"litellm_deployment_failure_responses_total", "litellm_deployment_state", "litellm_deployment_cooled_down_total",
			"litellm_remaining_requests_metric", "litellm_remaining_tokens_metric",
			"litellm_in_flight_requests", "litellm_cache_hits_metric_total", "litellm_cache_misses_metric_total",
			"litellm_llm_api_failed_requests_metric_total", "litellm_request_queue_time_seconds",
			"litellm_deployment_latency_per_output_token",
		},
		Rename: map[string]string{
			"litellm_proxy_total_requests_metric_total":    "requests",
			"litellm_proxy_failed_requests_metric_total":   "requests.failed",
			"litellm_request_total_latency_metric":         "request.latency",
			"litellm_llm_api_latency_metric":               "llm_api.latency",
			"litellm_overhead_latency_metric":              "overhead.latency",
			"litellm_llm_api_time_to_first_token_metric":   "time_to_first_token",
			"litellm_total_tokens_metric_total":            "tokens.total",
			"litellm_input_tokens_metric_total":            "tokens.input",
			"litellm_output_tokens_metric_total":           "tokens.output",
			"litellm_spend_metric_total":                   "spend",
			"litellm_deployment_total_requests_total":      "deployment.requests",
			"litellm_deployment_success_responses_total":   "deployment.success",
			"litellm_deployment_failure_responses_total":   "deployment.failures",
			"litellm_deployment_state":                     "deployment.state",
			"litellm_deployment_cooled_down_total":         "deployment.cooled_down",
			"litellm_remaining_requests_metric":            "remaining.requests",
			"litellm_remaining_tokens_metric":              "remaining.tokens",
			"litellm_in_flight_requests":                   "requests.in_flight",
			"litellm_cache_hits_metric_total":              "cache.hits",
			"litellm_cache_misses_metric_total":            "cache.misses",
			"litellm_llm_api_failed_requests_metric_total": "llm_api.failed",
			"litellm_request_queue_time_seconds":           "request.queue_time",
			"litellm_deployment_latency_per_output_token":  "deployment.latency_per_output_token",
		},
		// Étiquettes gardées : modèles et fournisseur ; jamais l'utilisateur, son courriel, son
		// adresse IP ni la clé (hashed_api_key, user_email, client_ip, end_user sont écartées).
		Labels: []string{"requested_model", "model", "model_group", "litellm_model_name", "api_provider", "status_code",
			"exception_status", "exception_class"},
	})

	// Hugging Face Text Generation Inference (port 8080 de la doc, -p 8080:80) : file d'attente,
	// lot en cours, requêtes et échecs, durées (de bout en bout, file, inférence, par jeton) en
	// secondes, jetons générés et longueur des entrées. Noms de la doc officielle (« Metrics »).
	register(Profile{
		Name: "tgi", Description: "Hugging Face Text Generation Inference", Port: 8080,
		Namespace: "tgi",
		Metrics: []string{
			"tgi_queue_size", "tgi_batch_current_size", "tgi_batch_current_max_tokens",
			"tgi_request_count", "tgi_request_success", "tgi_request_failure",
			"tgi_request_duration", "tgi_request_queue_duration", "tgi_request_inference_duration",
			"tgi_request_mean_time_per_token_duration", "tgi_request_generated_tokens", "tgi_request_input_length",
			"tgi_batch_inference_count", "tgi_batch_inference_success",
		},
		Rename: map[string]string{
			"tgi_queue_size":                           "queue.size",
			"tgi_batch_current_size":                   "batch.size",
			"tgi_batch_current_max_tokens":             "batch.max_tokens",
			"tgi_request_count":                        "requests",
			"tgi_request_success":                      "requests.success",
			"tgi_request_failure":                      "requests.failure",
			"tgi_request_duration":                     "request.duration",
			"tgi_request_queue_duration":               "request.queue_duration",
			"tgi_request_inference_duration":           "request.inference_duration",
			"tgi_request_mean_time_per_token_duration": "request.time_per_token",
			"tgi_request_generated_tokens":             "request.generated_tokens",
			"tgi_request_input_length":                 "request.input_length",
			"tgi_batch_inference_count":                "batch.inferences",
			"tgi_batch_inference_success":              "batch.inferences_success",
		},
		Labels: []string{"method", "err"},
	})

	// llama.cpp (llama-server lancé avec --metrics, port 8080) : requêtes en cours et différées,
	// jetons d'entrée (dont repris du cache) et générés, débits moyens (jetons/s), appels de décodage.
	// Noms relevés sur llama-server build 12464 ; kv_cache_* n'existent que dans les versions antérieures.
	register(Profile{
		Name: "llamacpp", Description: "llama.cpp (llama-server --metrics)", Port: 8080,
		Namespace: "llamacpp",
		Metrics: []string{
			"llamacpp:requests_processing", "llamacpp:requests_deferred",
			"llamacpp:prompt_tokens_total", "llamacpp:prompt_tokens_cached_total", "llamacpp:prompt_seconds_total",
			"llamacpp:tokens_predicted_total", "llamacpp:tokens_predicted_seconds_total",
			"llamacpp:prompt_tokens_seconds", "llamacpp:predicted_tokens_seconds",
			"llamacpp:kv_cache_usage_ratio", "llamacpp:kv_cache_tokens",
			"llamacpp:n_decode_total", "llamacpp:n_busy_slots_per_decode",
		},
		Rename: map[string]string{
			"llamacpp:requests_processing":            "requests.processing",
			"llamacpp:requests_deferred":              "requests.deferred",
			"llamacpp:prompt_tokens_total":            "tokens.prompt",
			"llamacpp:prompt_tokens_cached_total":     "tokens.prompt_cached",
			"llamacpp:prompt_seconds_total":           "prompt.busy",
			"llamacpp:tokens_predicted_total":         "tokens.predicted",
			"llamacpp:tokens_predicted_seconds_total": "predicted.busy",
			"llamacpp:prompt_tokens_seconds":          "prompt.throughput",
			"llamacpp:predicted_tokens_seconds":       "predicted.throughput",
			"llamacpp:kv_cache_usage_ratio":           "kv_cache.usage",
			"llamacpp:kv_cache_tokens":                "kv_cache.tokens",
			"llamacpp:n_decode_total":                 "decode.calls",
			"llamacpp:n_busy_slots_per_decode":        "decode.busy_slots",
		},
	})

	// Exportateur DCGM de NVIDIA (dcgm-exporter, port 9400) : mêmes noms HYPHA que la sonde
	// nvidia-smi (espace gpu) quand l'unité est la même. Noms jusqu'à la 4.8 (default-counters.csv :
	// utilisation en %, mémoire en Mio, horloges en MHz) et noms de la 4.9 (fractions, octets, hertz),
	// rangés sous des noms distincts quand l'unité change. La température de la mémoire n'est pas
	// retenue : DCGM la rend par une valeur sentinelle (≈ 9,2e18) sur les GPU sans capteur.
	register(Profile{
		Name: "dcgm", Description: "NVIDIA DCGM Exporter (GPU)", Port: 9400,
		Namespace: "gpu",
		Metrics: []string{
			"DCGM_FI_DEV_GPU_UTIL", "DCGM_FI_DEV_MEM_COPY_UTIL", "DCGM_FI_DEV_ENC_UTIL", "DCGM_FI_DEV_DEC_UTIL",
			"DCGM_FI_DEV_GPU_TEMP", "DCGM_FI_DEV_POWER_USAGE", "DCGM_FI_DEV_SM_CLOCK", "DCGM_FI_DEV_MEM_CLOCK",
			"DCGM_FI_DEV_FB_USED", "DCGM_FI_DEV_FB_FREE", "DCGM_FI_DEV_FB_RESERVED",
			"DCGM_FI_DEV_XID_ERRORS", "DCGM_FI_DEV_PCIE_REPLAY_COUNTER",
			"DCGM_FI_DEV_UNCORRECTABLE_REMAPPED_ROWS", "DCGM_FI_DEV_CORRECTABLE_REMAPPED_ROWS", "DCGM_FI_DEV_ROW_REMAP_FAILURE",
			"DCGM_FI_PROF_GR_ENGINE_ACTIVE", "DCGM_FI_PROF_PIPE_TENSOR_ACTIVE", "DCGM_FI_PROF_DRAM_ACTIVE",
			"DCGM_FI_DEV_GPU_UTIL_RATIO", "DCGM_FI_DEV_MEMORY_UTIL_RATIO", "DCGM_FI_DEV_GPU_TEMP_CELSIUS",
			"DCGM_FI_DEV_BOARD_POWER_WATTS", "DCGM_FI_DEV_SM_CLOCK_HERTZ", "DCGM_FI_DEV_MEMORY_CLOCK_HERTZ",
			"DCGM_FI_DEV_FB_USED_BYTES", "DCGM_FI_DEV_FB_FREE_BYTES", "DCGM_FI_DEV_FB_CAPACITY_BYTES", "DCGM_FI_DEV_FB_RESERVED_BYTES",
			"DCGM_FI_DEV_XID_ERROR", "DCGM_FI_DEV_PCIE_REPLAY_TOTAL",
			"DCGM_FI_DEV_ROW_REMAP_UNCORRECTABLE_TOTAL", "DCGM_FI_DEV_ROW_REMAP_CORRECTABLE_TOTAL", "DCGM_FI_DEV_ROW_REMAP_FAILED",
			"DCGM_FI_PROF_GR_ENGINE_UTIL_RATIO", "DCGM_FI_PROF_TENSOR_UTIL_RATIO", "DCGM_FI_PROF_DRAM_UTIL_RATIO",
		},
		Rename: map[string]string{
			"DCGM_FI_DEV_GPU_UTIL":                      "utilization",
			"DCGM_FI_DEV_MEM_COPY_UTIL":                 "memory.utilization",
			"DCGM_FI_DEV_ENC_UTIL":                      "encoder.utilization",
			"DCGM_FI_DEV_DEC_UTIL":                      "decoder.utilization",
			"DCGM_FI_DEV_GPU_TEMP":                      "temperature",
			"DCGM_FI_DEV_POWER_USAGE":                   "power.draw",
			"DCGM_FI_DEV_SM_CLOCK":                      "clock.sm",
			"DCGM_FI_DEV_MEM_CLOCK":                     "clock.mem",
			"DCGM_FI_DEV_FB_USED":                       "fb.used_mib",
			"DCGM_FI_DEV_FB_FREE":                       "fb.free_mib",
			"DCGM_FI_DEV_FB_RESERVED":                   "fb.reserved_mib",
			"DCGM_FI_DEV_XID_ERRORS":                    "xid_error",
			"DCGM_FI_DEV_PCIE_REPLAY_COUNTER":           "pcie.replays",
			"DCGM_FI_DEV_UNCORRECTABLE_REMAPPED_ROWS":   "remapped_rows.uncorrectable",
			"DCGM_FI_DEV_CORRECTABLE_REMAPPED_ROWS":     "remapped_rows.correctable",
			"DCGM_FI_DEV_ROW_REMAP_FAILURE":             "row_remap_failure",
			"DCGM_FI_PROF_GR_ENGINE_ACTIVE":             "engine.active",
			"DCGM_FI_PROF_PIPE_TENSOR_ACTIVE":           "tensor.active",
			"DCGM_FI_PROF_DRAM_ACTIVE":                  "dram.active",
			"DCGM_FI_DEV_GPU_UTIL_RATIO":                "utilization_ratio",
			"DCGM_FI_DEV_MEMORY_UTIL_RATIO":             "memory.utilization_ratio",
			"DCGM_FI_DEV_GPU_TEMP_CELSIUS":              "temperature",
			"DCGM_FI_DEV_BOARD_POWER_WATTS":             "power.draw",
			"DCGM_FI_DEV_SM_CLOCK_HERTZ":                "clock.sm_hz",
			"DCGM_FI_DEV_MEMORY_CLOCK_HERTZ":            "clock.mem_hz",
			"DCGM_FI_DEV_FB_USED_BYTES":                 "memory.used",
			"DCGM_FI_DEV_FB_FREE_BYTES":                 "memory.free",
			"DCGM_FI_DEV_FB_CAPACITY_BYTES":             "memory.total",
			"DCGM_FI_DEV_FB_RESERVED_BYTES":             "memory.reserved",
			"DCGM_FI_DEV_XID_ERROR":                     "xid_error",
			"DCGM_FI_DEV_PCIE_REPLAY_TOTAL":             "pcie.replays",
			"DCGM_FI_DEV_ROW_REMAP_UNCORRECTABLE_TOTAL": "remapped_rows.uncorrectable",
			"DCGM_FI_DEV_ROW_REMAP_CORRECTABLE_TOTAL":   "remapped_rows.correctable",
			"DCGM_FI_DEV_ROW_REMAP_FAILED":              "row_remap_failure",
			"DCGM_FI_PROF_GR_ENGINE_UTIL_RATIO":         "engine.active",
			"DCGM_FI_PROF_TENSOR_UTIL_RATIO":            "tensor.active",
			"DCGM_FI_PROF_DRAM_UTIL_RATIO":              "dram.active",
		},
		Labels: []string{"gpu", "UUID", "modelName", "pod", "namespace", "container"},
	})
}
