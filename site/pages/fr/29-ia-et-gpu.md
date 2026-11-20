---
id: ai-gpu
path: /docs/ia-et-gpu/
title: IA et GPU : Ollama, vLLM, LiteLLM, TGI, llama.cpp et GPU NVIDIA
nav: IA et GPU
section: Intégrations
order: 29
updated: 2026-11-20
description: Superviser les serveurs d'IA avec HYPHA : Ollama, vLLM, LiteLLM, Text Generation Inference, llama.cpp, et les GPU NVIDIA par nvidia-smi ou l'exportateur DCGM.
lead: Files d'attente d'inférence, latences, jetons, cache KV, modèles chargés et santé des GPU, lus en lecture seule par l'agent, sans jamais lancer de génération.
---
## En un coup d'œil

| Logiciel | Collecte | Activation | Espace de noms |
|---|---|---|---|
| Ollama | API locale (`/api/version`, `/api/tags`, `/api/ps`) | auto sur `127.0.0.1:11434` | `ollama.*` |
| vLLM | lecteur OpenMetrics, profil `vllm` (port 8000) | à déclarer | `vllm.*` |
| LiteLLM | lecteur OpenMetrics, profil `litellm` (port 4000) | à déclarer | `litellm.*` |
| Text Generation Inference | lecteur OpenMetrics, profil `tgi` (port 8080) | à déclarer | `tgi.*` |
| llama.cpp (`llama-server`) | lecteur OpenMetrics, profil `llamacpp` (port 8080) | à déclarer | `llamacpp.*` |
| GPU NVIDIA | `nvidia-smi` | coupée par défaut | `gpu.*` |
| GPU NVIDIA (DCGM) | lecteur OpenMetrics, profil `dcgm` (port 9400) | à déclarer | `gpu.*` |

Aucune de ces intégrations n'envoie de requête de génération : l'agent lit des listes et des mesures, aucun modèle n'est chargé ni facturé par lui.

## Ollama

L'agent lit trois routes de l'API, une fois par minute : la version, les modèles installés (la durée de cette liste sert de temps de réponse) et les modèles chargés en mémoire.

```yaml
integrations:
  ollama:
    url: http://127.0.0.1:11434     # par défaut ; une autre adresse active l'intégration
    instances:                      # autres serveurs Ollama, lus depuis cet agent
      - {name: gpu-2, url: http://192.0.2.12:11434}
```

| Mesure | Sens |
|---|---|
| `ollama.models.installed`, `ollama.models.size` | modèles installés, place totale sur le disque |
| `ollama.model.size` | taille de chaque modèle (étiquettes `ollama_model`, `ollama_family`, `ollama_parameters`, `ollama_quantization`) |
| `ollama.models.loaded`, `ollama.loaded.size`, `ollama.loaded.vram` | modèles chargés et mémoire occupée |
| `ollama.model.loaded.size`, `.vram`, `.vram_pct` | par modèle chargé ; sous 100 %, une partie du modèle tourne sur le processeur |
| `ollama.model.loaded.expires_in` | secondes avant le déchargement (absent pour `keep_alive: -1`) |
| `ollama.model.loaded.context_length` | contexte du modèle chargé |
| `ollama.response_time` | durée de la liste des modèles, en secondes |

Contrôle `ollama.can_connect` : version, modèles installés et chargés.

## Serveurs d'inférence par le lecteur OpenMetrics

vLLM, LiteLLM, TGI et llama.cpp publient leurs mesures au format Prometheus. Un profil intégré donne le port, les familles retenues, leurs noms HYPHA et les étiquettes gardées :

```yaml
integrations:
  openmetrics:
    - profile: vllm                          # http://127.0.0.1:8000/metrics
    - profile: litellm
      bearer_token_file: /etc/hypha/litellm.key
    - profile: tgi
      url: http://127.0.0.1:3000/metrics
    - profile: llamacpp                      # llama-server lancé avec --metrics
```

Chaque cible publie le contrôle `openmetrics.can_connect` et porte l'étiquette `instance_name` (le nom du profil, sauf `name` posé). Le fonctionnement du lecteur (débits, histogrammes, plafond de séries) est décrit dans [la configuration](/docs/configuration/).

### vLLM

- `vllm.requests.running`, `vllm.requests.waiting` : requêtes en cours et en attente.
- `vllm.kv_cache.usage` : occupation du cache KV, en fraction (1 = plein).
- `vllm.tokens.prompt`, `vllm.tokens.generation` : jetons par seconde ; `vllm.preemptions`, `vllm.requests.success` (étiquette `finished_reason`).
- Latences, en secondes, avec `.avg`, `.p50`, `.p95`, `.p99` : `vllm.time_to_first_token`, `vllm.inter_token_latency`, `vllm.request.latency`, `vllm.request.queue_time`, `vllm.request.prefill_time`, `vllm.request.decode_time`.

Étiquettes gardées : `model_name`, `engine`, `finished_reason`. Les noms des versions antérieures de vLLM (`gpu_cache_usage_perc`, `time_per_output_token_seconds`) sont lus sous les mêmes noms HYPHA.

### LiteLLM

Les mesures Prometheus font partie de l'édition libre de LiteLLM. Activez le rappel dans sa conf :

```yaml
litellm_settings:
  callbacks: ["prometheus"]
```

Depuis la version 1.85, `/metrics` demande une clé : donnez-la à l'agent par `bearer_token_file` (fichier lisible par l'agent seul), ou servez les mesures sur un port à part.

- `litellm.requests`, `litellm.requests.failed` : requêtes de la passerelle et échecs, par seconde.
- `litellm.request.latency`, `litellm.llm_api.latency`, `litellm.time_to_first_token` (secondes) et `litellm.overhead.latency` (millisecondes), avec leurs centiles.
- `litellm.tokens.input`, `.output`, `.total`, `litellm.spend` (dépense estimée par LiteLLM, par seconde).
- `litellm.deployment.state` (0 sain, 1 panne partielle, 2 panne), `litellm.deployment.requests`, `.success`, `.failures`, `.cooled_down`.
- `litellm.requests.in_flight`, `litellm.remaining.requests`, `litellm.remaining.tokens`, `litellm.cache.hits`, `.misses`.

Étiquettes gardées : modèle demandé, modèle, groupe de modèles, fournisseur, code de réponse, classe d'erreur. L'utilisateur, son adresse électronique, son adresse IP et l'empreinte de sa clé ne sont jamais conservés.

### Text Generation Inference

- `tgi.queue.size`, `tgi.batch.size`, `tgi.batch.max_tokens` : file d'attente et lot en cours.
- `tgi.requests`, `tgi.requests.success`, `tgi.requests.failure` (étiquette `err`).
- Durées en secondes, avec centiles : `tgi.request.duration`, `.queue_duration`, `.inference_duration`, `.time_per_token` ; `tgi.request.generated_tokens`, `tgi.request.input_length`.

Le profil suit les noms de la documentation de TGI. L'image Docker sert sur le port 80 du conteneur : indiquez l'`url` du port publié.

### llama.cpp

Lancez `llama-server` avec `--metrics`.

- `llamacpp.requests.processing`, `llamacpp.requests.deferred` (requêtes en attente d'un emplacement libre).
- `llamacpp.tokens.prompt`, `.prompt_cached`, `.predicted` : jetons par seconde.
- `llamacpp.prompt.throughput`, `llamacpp.predicted.throughput` : débits moyens en jetons par seconde.
- `llamacpp.decode.calls`, `llamacpp.decode.busy_slots`.

## GPU NVIDIA

### Par nvidia-smi

Sans exportateur, l'agent lit `nvidia-smi` toutes les 30 secondes. L'intégration est coupée par défaut :

```yaml
integrations:
  nvidia_gpu:
    enabled: auto          # active si nvidia-smi est présent ; true pour la forcer
    # path: /usr/bin/nvidia-smi
    # processes: false     # sans la liste des processus de calcul
```

| Mesure | Sens |
|---|---|
| `gpu.count` | nombre de GPU |
| `gpu.utilization`, `gpu.memory.utilization` | occupation du GPU et du contrôleur mémoire (%) |
| `gpu.memory.total`, `.used`, `.free` (octets), `gpu.memory.usage` (%) | mémoire du GPU |
| `gpu.temperature`, `gpu.memory.temperature` | températures (°C) |
| `gpu.power.draw`, `gpu.power.limit` (W), `gpu.power.usage` (%) | puissance |
| `gpu.clock.sm`, `gpu.clock.mem`, `gpu.clock.sm.max` | horloges (MHz) |
| `gpu.fan.speed`, `gpu.pstate` | ventilateur (%), état de performance (0 = P0) |
| `gpu.ecc.corrected`, `gpu.ecc.uncorrected` | erreurs ECC depuis le chargement du pilote |
| `gpu.throttle.thermal` | 1 si les horloges sont réduites par la chaleur |
| `gpu.processes`, `gpu.process.memory` | processus de calcul et mémoire par programme (`process_name`) |

Étiquettes : `gpu` (index), `gpu_uuid`, `gpu_name`. Contrôles : `gpu.can_query` (nvidia-smi répond, pilote chargé et en accord avec sa bibliothèque) et `gpu.health` par GPU (critique : erreurs ECC non corrigées ; avertissement : ralentissement thermique).

Les valeurs que le GPU ne fournit pas (`[N/A]`, mémoire unifiée des machines Grace et DGX Spark, ventilateur absent) ne sont pas envoyées. Un champ inconnu du pilote est retiré de la requête et la lecture reprend.

### Par l'exportateur DCGM

Avec `dcgm-exporter` (Kubernetes, grappes de calcul), déclarez le profil `dcgm` :

```yaml
integrations:
  openmetrics:
    - profile: dcgm        # http://127.0.0.1:9400/metrics
```

Les mesures prennent les mêmes noms que celles de nvidia-smi quand l'unité est la même (`gpu.utilization`, `gpu.temperature`, `gpu.power.draw`, `gpu.clock.sm`, `gpu.memory.used` en octets depuis dcgm-exporter 4.9) ; les autres gardent un nom à part : `gpu.fb.used_mib`, `gpu.fb.free_mib` (Mio, jusqu'à la 4.8), `gpu.utilization_ratio` (fraction, 4.9), `gpu.xid_error`, `gpu.pcie.replays`, `gpu.remapped_rows.*`, `gpu.engine.active`, `gpu.tensor.active`, `gpu.dram.active`. Étiquettes : `gpu`, `uuid`, `modelname`, et `pod`, `namespace`, `container` sous Kubernetes. La température de la mémoire n'est pas retenue : DCGM la rend par une valeur sentinelle sur les GPU sans capteur.

Choisissez nvidia-smi ou DCGM sur une même machine, pas les deux.

## Modèles de moniteurs

Famille **IA et GPU** du catalogue : Ollama injoignable ou lent, serveur d'inférence injoignable (par `instance_name`), file d'attente d'inférence vLLM ou TGI, latence du premier jeton vLLM, cache KV vLLM saturé, requêtes différées par llama.cpp, déploiement LiteLLM en panne, GPU trop chaud, GPU saturé, mémoire GPU pleine, GPU en défaut, nvidia-smi en échec, erreur XID d'un GPU.

## Tableaux prêts

**Tableaux de bord › Catalogue**, famille **IA et GPU** : GPU NVIDIA, Ollama, vLLM, LiteLLM, Text Generation Inference et llama.cpp.
