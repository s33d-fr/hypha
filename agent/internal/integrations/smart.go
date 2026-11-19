package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// SMART : smartctl -j -a, une fois toutes les 30 minutes, sans réveiller un disque en
// veille (-n standby : ses dernières valeurs restent alors en cache).
type SMART struct {
	cfg   config.IntegrationToggle
	det   detector
	cache cache[smartScan]
	last  map[string]smartDisk
}

type smartScan struct {
	disks  []smartDisk
	failed []string
}

type smartDisk struct {
	device                         string
	passed                         *bool
	temp, realloc, hours, wear     float64
	hasTemp, hasRealloc, hasHours  bool
	hasWear, standby               bool
}

func newSMART(cfg config.IntegrationToggle) *SMART {
	return &SMART{cfg: cfg, det: detector{mode: cfg.Enabled}, last: map[string]smartDisk{}}
}

func (s *SMART) name() string          { return "smart" }
func (s *SMART) close()                {}
func (s *SMART) period() time.Duration { return 5 * time.Minute }

// parseSmartctl lit « smartctl -j -a » : verdict, température, secteurs réalloués (ATA 5),
// heures de fonctionnement, usure (NVMe percentage_used ; ATA 177, 231, 233 normalisés).
func parseSmartctl(data []byte) (smartDisk, bool) {
	var r struct {
		Device struct {
			Name string `json:"name"`
		} `json:"device"`
		SmartStatus *struct {
			Passed bool `json:"passed"`
		} `json:"smart_status"`
		Temperature *struct {
			Current float64 `json:"current"`
		} `json:"temperature"`
		PowerOnTime *struct {
			Hours float64 `json:"hours"`
		} `json:"power_on_time"`
		ATA *struct {
			Table []struct {
				ID    int     `json:"id"`
				Value float64 `json:"value"`
				Raw   struct {
					Value float64 `json:"value"`
				} `json:"raw"`
			} `json:"table"`
		} `json:"ata_smart_attributes"`
		NVMe *struct {
			PercentageUsed *float64 `json:"percentage_used"`
			Temperature    *float64 `json:"temperature"`
			PowerOnHours   *float64 `json:"power_on_hours"`
		} `json:"nvme_smart_health_information_log"`
		Smartctl struct {
			ExitStatus int `json:"exit_status"`
		} `json:"smartctl"`
	}
	if json.Unmarshal(data, &r) != nil {
		return smartDisk{}, false
	}
	d := smartDisk{device: strings.TrimPrefix(r.Device.Name, "/dev/")}
	if r.Smartctl.ExitStatus&2 != 0 && r.SmartStatus == nil && r.Temperature == nil {
		d.standby = true
		return d, true
	}
	if r.SmartStatus != nil {
		p := r.SmartStatus.Passed
		d.passed = &p
	}
	if r.Temperature != nil {
		d.temp, d.hasTemp = r.Temperature.Current, true
	}
	if r.PowerOnTime != nil {
		d.hours, d.hasHours = r.PowerOnTime.Hours, true
	}
	if r.ATA != nil {
		for _, a := range r.ATA.Table {
			switch a.ID {
			case 5:
				d.realloc, d.hasRealloc = a.Raw.Value, true
			case 177, 231, 233:
				if !d.hasWear && a.Value > 0 && a.Value <= 100 {
					d.wear, d.hasWear = 100-a.Value, true
				}
			}
		}
	}
	if r.NVMe != nil {
		if r.NVMe.PercentageUsed != nil {
			d.wear, d.hasWear = *r.NVMe.PercentageUsed, true
		}
		if !d.hasTemp && r.NVMe.Temperature != nil {
			d.temp, d.hasTemp = *r.NVMe.Temperature, true
		}
		if !d.hasHours && r.NVMe.PowerOnHours != nil {
			d.hours, d.hasHours = *r.NVMe.PowerOnHours, true
		}
	}
	if d.passed == nil && !d.hasTemp && !d.hasHours {
		return d, false // disque sans SMART (disque virtuel)
	}
	return d, true
}

type smartDevice struct{ name, typ string }

// parseSmartScan lit « smartctl --scan -j ».
func parseSmartScan(data []byte) []smartDevice {
	var r struct {
		Devices []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"devices"`
	}
	if json.Unmarshal(data, &r) != nil {
		return nil
	}
	var out []smartDevice
	for _, d := range r.Devices {
		out = append(out, smartDevice{d.Name, d.Type})
	}
	return out
}

func (s *SMART) scan(ctx context.Context, tool string) (smartScan, error) {
	out, err := runTool(ctx, 20*time.Second, tool, "--scan", "-j")
	devs := parseSmartScan(out)
	if len(devs) == 0 {
		if err == nil {
			err = errNoData
		}
		return smartScan{}, err
	}
	var res smartScan
	for _, d := range devs {
		args := []string{"-j", "-a", "-n", "standby"}
		if d.typ != "" {
			args = append(args, "-d", d.typ)
		}
		data, _ := runTool(ctx, 30*time.Second, tool, append(args, d.name)...)
		disk, ok := parseSmartctl(data)
		if !ok {
			continue
		}
		if disk.device == "" {
			disk.device = filepath.Base(d.name)
		}
		res.disks = append(res.disks, disk)
	}
	if len(res.disks) == 0 {
		return res, fmt.Errorf("aucun disque ne publie de données SMART")
	}
	return res, nil
}

func (s *SMART) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := s.det.skip(now); skip {
		return nil, st
	}
	tool, ok := findTool("smartctl")
	if !ok {
		_, st := s.det.failed("smartctl introuvable")
		return nil, st
	}
	res, err := s.cache.get(now, 30*time.Minute, func() (smartScan, error) { return s.scan(ctx, tool) })
	if err != nil {
		if silent, st := s.det.failed(err.Error()); silent {
			return nil, st
		} else {
			return []model.Check{canConnect("smart", model.Ms(now), false, err.Error(), tags())}, st
		}
	}
	s.det.ok()
	for _, d := range res.disks {
		if d.standby {
			d = s.last[d.device] // en veille : dernières valeurs connues
			if d.device == "" {
				continue
			}
		} else {
			s.last[d.device] = d
		}
		if d.passed != nil {
			b.Gauge("smart.health", "", b2f(*d.passed), "device", d.device)
		}
		if d.hasTemp {
			b.Gauge("smart.temperature", "celsius", d.temp, "device", d.device)
		}
		if d.hasRealloc {
			b.Gauge("smart.reallocated_sectors", "count", d.realloc, "device", d.device)
		}
		if d.hasHours {
			b.Gauge("smart.power_on_hours", "", d.hours, "device", d.device)
		}
		if d.hasWear {
			b.Gauge("smart.wear_level", "percent", d.wear, "device", d.device)
		}
	}
	return []model.Check{canConnect("smart", model.Ms(now), true, fmt.Sprintf("%d disque(s) lus", len(res.disks)), tags())}, "ok"
}

func b2f(v bool) float64 {
	if v {
		return 1
	}
	return 0
}
