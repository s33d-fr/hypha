package integrations

import "s33d.fr/hypha/agent/internal/config"

// vague5Integrations : intégrations de la vague 5 (IA et GPU) ; l'entrée principale d'Ollama,
// ses instances (§ 50), puis les GPU NVIDIA par nvidia-smi.
func vague5Integrations(cfg config.IntegrationsConfig, d Deps) []integration {
	out := []integration{
		named("ollama", cfg.Ollama.Name, d, func(Deps) integration { return newOllama(cfg.Ollama) }),
	}
	for _, c := range cfg.OllamaInstances {
		out = append(out, named("ollama", c.Name, d, func(Deps) integration { return newOllama(c) }))
	}
	return append(out, newNvidiaGPU(cfg.NvidiaGPU))
}
