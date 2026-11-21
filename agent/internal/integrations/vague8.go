package integrations

import (
	"context"
	"sort"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── Vague 8 : réseau, stockage et sauvegardes auto-hébergés ─────────────────────────────────
//
// Pi-hole, AdGuard Home, Unbound, Caddy (API d'administration), Mosquitto, Proxmox Backup Server,
// Borg et restic. Mêmes règles que les sondes dédiées de la vague 3 (mode auto, contrôle
// <nom>.can_connect une fois le logiciel vu, écarts de compteurs, instances § 50), plus des
// étiquettes fixes (tags) par entrée de conf.

// vague8Integrations : entrées principales, puis instances, de chaque sonde.
func vague8Integrations(cfg config.IntegrationsConfig, d Deps) []integration {
	in, x := cfg.IntegrationsV8, cfg.V8Extra
	var out []integration
	add := func(kind, name string, tags map[string]string, mk func(Deps) integration) {
		out = append(out, named(kind, name, d, func(d Deps) integration { return withTags(mk(d), tags) }))
	}
	add("pihole", in.PiHole.Name, in.PiHole.Tags, func(Deps) integration { return newPiHole(in.PiHole) })
	for _, c := range x.PiHole {
		add("pihole", c.Name, c.Tags, func(Deps) integration { return newPiHole(c) })
	}
	add("adguard", in.AdGuard.Name, in.AdGuard.Tags, func(Deps) integration { return newAdGuard(in.AdGuard) })
	for _, c := range x.AdGuard {
		add("adguard", c.Name, c.Tags, func(Deps) integration { return newAdGuard(c) })
	}
	add("unbound", in.Unbound.Name, in.Unbound.Tags, func(Deps) integration { return newUnbound(in.Unbound) })
	for _, c := range x.Unbound {
		add("unbound", c.Name, c.Tags, func(Deps) integration { return newUnbound(c) })
	}
	add("caddy", in.Caddy.Name, in.Caddy.Tags, func(Deps) integration { return newCaddy(in.Caddy) })
	for _, c := range x.Caddy {
		add("caddy", c.Name, c.Tags, func(Deps) integration { return newCaddy(c) })
	}
	add("mosquitto", in.Mosquitto.Name, in.Mosquitto.Tags, func(Deps) integration { return newMosquitto(in.Mosquitto) })
	for _, c := range x.Mosquitto {
		add("mosquitto", c.Name, c.Tags, func(Deps) integration { return newMosquitto(c) })
	}
	add("proxmox_backup", in.PBS.Name, in.PBS.Tags, func(d Deps) integration { return newPBS(in.PBS, d) })
	for _, c := range x.PBS {
		add("proxmox_backup", c.Name, c.Tags, func(d Deps) integration { return newPBS(c, d) })
	}
	return append(out, newBackupTool("borg", in.Borg), newBackupTool("restic", in.Restic))
}

// tagged : intégration dont chaque mesure et chaque contrôle portent des étiquettes fixes.
type tagged struct {
	inner integration
	kv    []string
}

// withTags enveloppe l'intégration si des étiquettes sont posées (sinon telle quelle).
func withTags(it integration, tags map[string]string) integration {
	if len(tags) == 0 {
		return it
	}
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	kv := make([]string, 0, 2*len(keys))
	for _, k := range keys {
		kv = append(kv, k, tags[k])
	}
	return &tagged{inner: it, kv: kv}
}

func (w *tagged) name() string { return w.inner.name() }
func (w *tagged) close()       { w.inner.close() }

func (w *tagged) period() time.Duration {
	if p, ok := w.inner.(periodic); ok {
		return p.period()
	}
	return 0
}

func (w *tagged) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	t := func(kv ...string) map[string]string {
		// Étiquettes fixes d'abord : celles de la sonde (instance, magasin…) l'emportent.
		return tags(append(append(make([]string, 0, len(w.kv)+len(kv)), w.kv...), kv...)...)
	}
	return w.inner.collect(ctx, now, b.With(t), t)
}

// ageSeconds : âge en secondes d'un instant (unix), jamais négatif (horloges décalées).
func ageSeconds(now time.Time, unix float64) float64 {
	a := float64(now.UnixMilli())/1000 - unix
	if a < 0 {
		return 0
	}
	return float64(int64(a))
}
