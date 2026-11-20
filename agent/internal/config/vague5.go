package config

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// Vague 5 des intégrations (IA et GPU) : Ollama par son API locale et GPU NVIDIA par nvidia-smi.
// vLLM, LiteLLM, TGI, llama.cpp et l'exportateur DCGM passent par le lecteur OpenMetrics
// (profils de omprofiles/vague5.go).

// IntegrationsV5 : intégrations de la vague 5, à plat dans « integrations » (inline).
type IntegrationsV5 struct {
	Ollama    OllamaConfig    `yaml:"ollama"`
	NvidiaGPU NvidiaGPUConfig `yaml:"nvidia_gpu"`

	// OllamaInstances : entrées « instances » d'ollama décodées (§ 50), remplies par normalize.
	OllamaInstances []OllamaConfig `yaml:"-"`
}

// DefaultOllamaURL : adresse d'écoute par défaut d'Ollama.
const DefaultOllamaURL = "http://127.0.0.1:11434"

// OllamaConfig : serveur Ollama lu par son API (version, modèles installés et chargés). Jamais de
// génération : la sonde ne lit que des listes, aucun modèle n'est chargé par elle.
type OllamaConfig struct {
	Enabled  Tristate `yaml:"enabled"` // auto = si l'API répond à l'adresse par défaut (ou si url est fixée)
	URL      string   `yaml:"url"`
	Insecure bool     `yaml:"insecure"` // certificat autosigné en https (derrière un relais)

	Multi `yaml:",inline"` // name et instances (§ 50)
}

// NvidiaGPUConfig : GPU NVIDIA lus par nvidia-smi (sans exportateur DCGM). Coupé par défaut ;
// auto = actif si nvidia-smi est présent sur la machine.
type NvidiaGPUConfig struct {
	Enabled   Tristate `yaml:"enabled"`
	Path      string   `yaml:"path"`      // chemin de nvidia-smi ; sinon le PATH et les dossiers usuels
	Processes *bool    `yaml:"processes"` // processus de calcul par GPU (oui par défaut)
}

// ProcessesOn dit si les processus de calcul sont relevés.
func (n NvidiaGPUConfig) ProcessesOn() bool { return n.Processes == nil || *n.Processes }

func defaultsV5(c *Config) {
	c.Integrations.Ollama = OllamaConfig{Enabled: "auto", URL: DefaultOllamaURL}
	c.Integrations.NvidiaGPU = NvidiaGPUConfig{Enabled: "false"}
}

func (c *Config) normalizeV5() {
	in := &c.Integrations
	in.Ollama.URL = strings.TrimSpace(in.Ollama.URL)
	if in.Ollama.URL == "" {
		in.Ollama.URL = DefaultOllamaURL
	}
	in.NvidiaGPU.Path = strings.TrimSpace(in.NvidiaGPU.Path)
	warn := func(s string) { c.Warnings = append(c.Warnings, s) }
	fail := func(s string) { c.instErrs = append(c.instErrs, s) }
	in.OllamaInstances = decodeInstances("ollama", in.Ollama.Raw,
		func() OllamaConfig { v := Default().Integrations.Ollama; v.Enabled = "true"; return v },
		func(v *OllamaConfig) *Multi { return &v.Multi }, warn, fail)
	for i := range in.OllamaInstances {
		in.OllamaInstances[i].URL = strings.TrimSpace(in.OllamaInstances[i].URL)
	}
}

func (c *Config) validateV5(add, warn func(string, ...any)) {
	in := c.Integrations
	checkOllama := func(at string, o OllamaConfig) {
		u, err := url.Parse(o.URL)
		switch {
		case err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https"):
			add("%s : url invalide %q (http(s)://hôte:port)", at, o.URL)
		case u.User != nil:
			add("%s : pas d'identifiants dans l'url", at)
		}
		if o.Insecure {
			warn("%s : insecure actif, certificat non vérifié", at)
		}
	}
	checkOllama("integrations.ollama", in.Ollama)
	seen := map[string]bool{}
	if in.Ollama.Name != "" {
		if !instanceNameRe.MatchString(in.Ollama.Name) {
			add("integrations.ollama.name invalide : %q (minuscules, chiffres, - et _, 32 au plus)", in.Ollama.Name)
		}
		seen[in.Ollama.Name] = true
	}
	for _, o := range in.OllamaInstances {
		at := fmt.Sprintf("integrations.ollama.instances[%d]", o.idx)
		switch {
		case o.Name == "":
			add("%s : name manquant", at)
		case !instanceNameRe.MatchString(o.Name):
			add("%s : name invalide %q (minuscules, chiffres, - et _, 32 au plus)", at, o.Name)
		case seen[o.Name]:
			add("%s : name %q en double", at, o.Name)
		}
		seen[o.Name] = true
		if o.Name != "" {
			at += " (" + o.Name + ")"
		}
		checkOllama(at, o)
	}
	if p := in.NvidiaGPU.Path; p != "" && !filepath.IsAbs(p) {
		add("integrations.nvidia_gpu.path : chemin absolu attendu, pas %q", p)
	}
}
