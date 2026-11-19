package integrations

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/hostfs"
	"s33d.fr/hypha/agent/internal/model"
)

// Hwmon lit les capteurs du noyau (/sys/class/hwmon) : températures et ventilateurs.
type Hwmon struct {
	cfg config.IntegrationToggle
	det detector
	// root : dossier lu (remplacé dans les tests).
	root string
}

func newHwmon(cfg config.IntegrationToggle) *Hwmon {
	return &Hwmon{cfg: cfg, det: detector{mode: cfg.Enabled}, root: hostfs.Sys("class/hwmon")}
}

func (h *Hwmon) name() string          { return "hwmon" }
func (h *Hwmon) close()                {}
func (h *Hwmon) period() time.Duration { return 60 * time.Second }

type hwReading struct {
	kind, sensor string
	value        float64
}

// readHwmon parcourt <root>/hwmon*/ : temp*_input en millidegrés, fan*_input en tr/min ;
// le capteur s'appelle <name>/<label> (ou <name>/tempN).
func readHwmon(root string) []hwReading {
	dirs, _ := filepath.Glob(filepath.Join(root, "hwmon*"))
	sort.Strings(dirs)
	var out []hwReading
	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(b))
	}
	names := map[string]int{}
	for _, d := range dirs {
		chip := read(filepath.Join(d, "name"))
		if chip == "" {
			chip = filepath.Base(d)
		}
		names[chip]++
		if names[chip] > 1 {
			chip += "." + strconv.Itoa(names[chip]-1)
		}
		for _, kind := range []string{"temp", "fan"} {
			inputs, _ := filepath.Glob(filepath.Join(d, kind+"*_input"))
			sort.Strings(inputs)
			for _, in := range inputs {
				v, err := strconv.ParseFloat(read(in), 64)
				if err != nil {
					continue
				}
				base := strings.TrimSuffix(filepath.Base(in), "_input")
				label := read(filepath.Join(d, base+"_label"))
				if label == "" {
					label = base
				}
				r := hwReading{sensor: chip + "/" + label}
				if kind == "temp" {
					r.kind, r.value = "temperature", v/1000
					if r.value <= -40 || r.value >= 200 { // valeur hors de toute mesure réelle
						continue
					}
				} else {
					r.kind, r.value = "fan", v
				}
				out = append(out, r)
			}
		}
	}
	return out
}

func (h *Hwmon) collect(_ context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := h.det.skip(now); skip {
		return nil, st
	}
	rs := readHwmon(h.root)
	if len(rs) == 0 {
		_, st := h.det.failed("aucun capteur")
		return nil, st
	}
	h.det.ok()
	for _, r := range rs {
		if r.kind == "temperature" {
			b.Gauge("system.temperature", "celsius", r.value, "sensor", r.sensor)
		} else {
			b.Gauge("system.fan.rpm", "rpm", r.value, "sensor", r.sensor)
		}
	}
	return []model.Check{canConnect("hwmon", model.Ms(now), true, strconv.Itoa(len(rs))+" capteur(s) lus", tags())}, "ok"
}
