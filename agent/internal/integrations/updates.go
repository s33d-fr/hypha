package integrations

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
	"s33d.fr/hypha/agent/internal/updates"
)

// Updates publie system.updates.pending, .security et system.reboot_required (§§ 15, 31),
// et sous Windows le contrôle windows.updates.pending. La liste est relue au plus une fois
// par heure (partagée avec l'inventaire).
type Updates struct {
	cfg config.IntegrationToggle
	det detector
	up  *updates.Checker
}

func newUpdatesIntegration(cfg config.IntegrationToggle, up *updates.Checker) *Updates {
	return &Updates{cfg: cfg, det: detector{mode: cfg.Enabled}, up: up}
}

func (u *Updates) name() string          { return "updates" }
func (u *Updates) close()                {}
func (u *Updates) period() time.Duration { return 60 * time.Second }

func (u *Updates) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := u.det.skip(now); skip {
		return nil, st
	}
	r := u.up.Get(ctx, time.Hour)
	ts := model.Ms(now)
	if !r.Known {
		msg := "liste des mises à jour illisible"
		if r.Err != nil {
			msg = r.Err.Error()
		}
		if silent, st := u.det.failed(msg); silent {
			return nil, st
		}
		var checks []model.Check
		if runtime.GOOS == "windows" {
			checks = append(checks, model.Check{TS: ts, Name: "windows.updates.pending", Status: model.CheckUnknown, Message: msg, Tags: tags()})
		}
		return append(checks, canConnect("updates", ts, false, msg, tags())), "erreur : " + msg
	}
	u.det.ok()
	b.Gauge("system.updates.pending", "count", float64(r.Pending))
	b.Gauge("system.updates.security", "count", float64(r.Security))
	b.Gauge("system.reboot_required", "", b2f(r.RebootRequired))
	checks := []model.Check{canConnect("updates", ts, true, fmt.Sprintf("%s : %d en attente dont %d de sécurité", r.Source, r.Pending, r.Security), tags())}
	if runtime.GOOS == "windows" {
		st, msg := model.CheckOK, "aucune mise à jour en attente"
		if r.Pending > 0 {
			st, msg = model.CheckWarn, fmt.Sprintf("%d mise(s) à jour en attente dont %d de sécurité", r.Pending, r.Security)
		}
		if r.RebootRequired {
			msg += " ; redémarrage requis"
		}
		checks = append(checks, model.Check{TS: ts, Name: "windows.updates.pending", Status: st, Message: msg, Tags: tags()})
	}
	return checks, "ok"
}
