package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadV5(t *testing.T, yaml string) (*Config, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "hypha.yaml")
	if err := os.WriteFile(p, []byte("api_key: hph_"+strings.Repeat("b", 40)+"\n"+yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return c, strings.Join(c.Validate(true), "\n")
}

func TestV5Defaults(t *testing.T) {
	c, errs := loadV5(t, "")
	if errs != "" {
		t.Fatalf("erreurs : %s", errs)
	}
	in := c.Integrations
	if in.Ollama.Enabled != "auto" || in.Ollama.URL != DefaultOllamaURL || in.NvidiaGPU.Enabled != "false" || !in.NvidiaGPU.ProcessesOn() {
		t.Fatalf("valeurs par défaut : %+v %+v", in.Ollama, in.NvidiaGPU)
	}
	y, err := c.RedactedYAML()
	if err != nil || !strings.Contains(y, "ollama:") || !strings.Contains(y, "nvidia_gpu:") {
		t.Fatalf("conf affichée : %v\n%s", err, y)
	}
}

func TestV5OllamaInstancesAndValidation(t *testing.T) {
	c, errs := loadV5(t, `integrations:
  ollama:
    name: local
    instances:
      - {name: gpu-2, url: "http://192.0.2.12:11434"}
      - {name: gpu-2, url: "ftp://x"}
      - {url: "http://u:p@h:1"}
  nvidia_gpu: {enabled: auto, path: nvidia-smi, processes: false}
`)
	if len(c.Integrations.OllamaInstances) != 3 || c.Integrations.OllamaInstances[0].Enabled != "true" ||
		c.Integrations.OllamaInstances[0].URL != "http://192.0.2.12:11434" {
		t.Fatalf("instances : %+v", c.Integrations.OllamaInstances)
	}
	for _, want := range []string{"instances[1] : name \"gpu-2\" en double", "instances[1] (gpu-2) : url invalide",
		"instances[2] : name manquant", "pas d'identifiants dans l'url", "nvidia_gpu.path : chemin absolu attendu"} {
		if !strings.Contains(errs, want) {
			t.Errorf("erreur attendue %q dans :\n%s", want, errs)
		}
	}
	if c.Integrations.NvidiaGPU.ProcessesOn() {
		t.Error("processes: false ignoré")
	}
}
