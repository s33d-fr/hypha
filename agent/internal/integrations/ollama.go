package integrations

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── Ollama (vague 5) : API locale, en lecture seule ────────────────────────────────────────
//
// Trois lectures par passage, jamais de génération : /api/version, /api/tags (modèles installés,
// dont la durée sert de temps de réponse) et /api/ps (modèles chargés en mémoire, part en mémoire
// vidéo, échéance de déchargement). Une liste ne charge aucun modèle.

const ollamaMaxModels = 200 // au-delà, les plus gros seulement (garde-fou de cardinalité)

type Ollama struct {
	cfg    config.OllamaConfig
	det    detector
	client *http.Client
}

func newOllama(cfg config.OllamaConfig) *Ollama {
	mode := cfg.Enabled
	if mode == "auto" && cfg.URL != "" && strings.TrimRight(cfg.URL, "/") != config.DefaultOllamaURL {
		mode = "true" // une adresse posée dit l'intention
	}
	tr := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{InsecureSkipVerify: cfg.Insecure}} //nolint:gosec // choisi dans la conf
	return &Ollama{cfg: cfg, det: detector{mode: mode}, client: &http.Client{Timeout: 10 * time.Second, Transport: tr}}
}

func (o *Ollama) name() string          { return "ollama" }
func (o *Ollama) period() time.Duration { return 60 * time.Second }
func (o *Ollama) close()                {}

type ollamaDetails struct {
	Family            string `json:"family"`
	ParameterSize     string `json:"parameter_size"`
	QuantizationLevel string `json:"quantization_level"`
}

type ollamaModel struct {
	Name          string        `json:"name"`
	Model         string        `json:"model"`
	Size          float64       `json:"size"`
	SizeVRAM      float64       `json:"size_vram"`
	ExpiresAt     string        `json:"expires_at"`
	ContextLength float64       `json:"context_length"`
	Details       ollamaDetails `json:"details"`
}

func (m ollamaModel) label() string {
	if m.Name != "" {
		return m.Name
	}
	return m.Model
}

func (o *Ollama) get(ctx context.Context, base, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "hypha-agent")
	resp, err := o.client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // l'adresse est déjà dans l'étiquette instance
		}
		return fmt.Errorf("%s : %v", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return fmt.Errorf("%s : HTTP %d", path, resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(out); err != nil {
		return fmt.Errorf("%s : réponse illisible (%v)", path, err)
	}
	return nil
}

func (o *Ollama) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := o.det.skip(now); skip {
		return nil, st
	}
	ts := model.Ms(now)
	base := strings.TrimRight(o.cfg.URL, "/")
	kv := []string{"instance", base}
	fail := func(err error) ([]model.Check, string) {
		if silent, st := o.det.failed(err.Error()); silent {
			return nil, st
		}
		return []model.Check{{TS: ts, Name: "ollama.can_connect", Status: model.CheckCrit, Message: err.Error(), Tags: tags(kv...)}},
			"erreur : " + err.Error()
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	var ver struct {
		Version string `json:"version"`
	}
	if err := o.get(ctx, base, "/api/version", &ver); err != nil {
		return fail(err)
	}
	if ver.Version == "" {
		return fail(fmt.Errorf("/api/version : pas de numéro de version, ce n'est pas Ollama"))
	}
	var tagsResp struct {
		Models []ollamaModel `json:"models"`
	}
	start := time.Now()
	if err := o.get(ctx, base, "/api/tags", &tagsResp); err != nil {
		return fail(err)
	}
	b.Gauge("ollama.response_time", "second", time.Since(start).Seconds(), kv...)
	o.det.ok()

	installed := tagsResp.Models
	var total float64
	for _, m := range installed {
		total += m.Size
	}
	b.Gauge("ollama.models.installed", "model", float64(len(installed)), kv...)
	b.Gauge("ollama.models.size", "byte", total, kv...)
	sort.SliceStable(installed, func(i, j int) bool { return installed[i].Size > installed[j].Size })
	for i, m := range installed {
		if i >= ollamaMaxModels {
			break
		}
		mkv := append(append([]string{}, kv...), "ollama_model", m.label())
		if d := m.Details; d.Family != "" || d.ParameterSize != "" || d.QuantizationLevel != "" {
			mkv = append(mkv, "ollama_family", d.Family, "ollama_parameters", d.ParameterSize, "ollama_quantization", d.QuantizationLevel)
		}
		b.Gauge("ollama.model.size", "byte", m.Size, mkv...)
	}

	checks := []model.Check{}
	msg := fmt.Sprintf("Ollama %s joignable : %d modèle(s) installé(s)", ver.Version, len(installed))
	var ps struct {
		Models []ollamaModel `json:"models"`
	}
	state := "ok"
	if err := o.get(ctx, base, "/api/ps", &ps); err != nil {
		// Versions anciennes sans /api/ps : le reste est lu quand même.
		msg += " ; modèles chargés illisibles (" + err.Error() + ")"
		state = "partiel : " + err.Error()
	} else {
		var size, vram float64
		for i, m := range ps.Models {
			size += m.Size
			vram += m.SizeVRAM
			if i >= ollamaMaxModels {
				continue
			}
			mkv := append(append([]string{}, kv...), "ollama_model", m.label())
			b.Gauge("ollama.model.loaded.size", "byte", m.Size, mkv...)
			b.Gauge("ollama.model.loaded.vram", "byte", m.SizeVRAM, mkv...)
			if m.Size > 0 {
				// Part du modèle en mémoire vidéo : sous 100 %, une partie tourne sur le processeur.
				b.Gauge("ollama.model.loaded.vram_pct", "percent", round1(100*m.SizeVRAM/m.Size), mkv...)
			}
			if m.ContextLength > 0 {
				b.Gauge("ollama.model.loaded.context_length", "token", m.ContextLength, mkv...)
			}
			if t, err := time.Parse(time.RFC3339Nano, m.ExpiresAt); err == nil {
				// Échéance de déchargement ; une échéance lointaine (keep_alive -1) n'est pas une mesure.
				if left := t.Sub(now); left >= 0 && left < 366*24*time.Hour {
					b.Gauge("ollama.model.loaded.expires_in", "second", left.Seconds(), mkv...)
				}
			}
		}
		b.Gauge("ollama.models.loaded", "model", float64(len(ps.Models)), kv...)
		b.Gauge("ollama.loaded.size", "byte", size, kv...)
		b.Gauge("ollama.loaded.vram", "byte", vram, kv...)
		msg += fmt.Sprintf(", %d chargé(s) en mémoire", len(ps.Models))
	}
	checks = append(checks, model.Check{TS: ts, Name: "ollama.can_connect", Status: model.CheckOK, Message: msg, Tags: tags(kv...)})
	return checks, state
}
