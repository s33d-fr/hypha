package integrations

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── KVM et libvirt (§ 43) : virsh sur l'hôte, en lecture seule ─────────────────────────────
//
// Une commande pour tous les domaines (virsh domstats --raw) : état et raison, CPU, vCPU,
// mémoire, disques et réseau ; démarrage automatique (list --autostart) ; pools de stockage
// (pool-info --bytes). Contrôles : domaine planté, mis en pause sur erreur d'entrée-sortie
// (pool plein, le cas classique), arrêté alors que son démarrage automatique est actif ; pool
// inactif ou inaccessible. Les VM ont souvent leur propre agent : elles restent des mesures de
// l'hôte, comme celles de VirtualBox, et non des hôtes à part.

type Libvirt struct {
	cfg  config.LibvirtConfig
	det  detector
	run  func(ctx context.Context, args ...string) ([]byte, error)
	prev map[string]libvirtCounters
	at   time.Time
}

type libvirtCounters struct{ cpu, rd, wr, rx, tx float64 }

func newLibvirt(cfg config.LibvirtConfig) *Libvirt {
	l := &Libvirt{cfg: cfg, det: detector{mode: cfg.Enabled}}
	l.run = func(ctx context.Context, args ...string) ([]byte, error) {
		path, ok := findTool("virsh")
		if !ok {
			return nil, errNoVirsh
		}
		return runTool(ctx, 20*time.Second, path, append([]string{"-c", cfg.URI}, args...)...)
	}
	return l
}

var errNoVirsh = fmt.Errorf("virsh introuvable")

func (l *Libvirt) name() string          { return "libvirt" }
func (l *Libvirt) period() time.Duration { return 60 * time.Second }
func (l *Libvirt) close()                {}

// parseDomstats : sortie de « virsh domstats --raw », domaine → clé → valeur.
func parseDomstats(out []byte) (map[string]map[string]string, []string) {
	doms := map[string]map[string]string{}
	var order []string
	var cur map[string]string
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if name, ok := strings.CutPrefix(line, "Domain: "); ok {
			name = strings.Trim(name, "'")
			cur = map[string]string{}
			doms[name] = cur
			order = append(order, name)
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok && cur != nil {
			cur[k] = v
		}
	}
	return doms, order
}

// parseKV : sortie « Clé:   valeur » de pool-info.
func parseKV(out []byte) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok {
			m[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return m
}

func lines(out []byte) []string {
	var l []string
	for _, s := range strings.Split(string(out), "\n") {
		if s = strings.TrimSpace(s); s != "" {
			l = append(l, s)
		}
	}
	return l
}

func fnum(m map[string]string, k string) (float64, bool) {
	v, ok := m[k]
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(v, 64)
	return f, err == nil
}

// sumIdx : somme d'un compteur sur les disques ou cartes (block.N.rd.bytes…).
func sumIdx(m map[string]string, prefix, suffix string) (float64, bool) {
	n, ok := fnum(m, prefix+".count")
	if !ok {
		return 0, false
	}
	total, any := 0.0, false
	for i := 0; i < int(n); i++ {
		if v, ok := fnum(m, fmt.Sprintf("%s.%d.%s", prefix, i, suffix)); ok {
			total, any = total+v, true
		}
	}
	return total, any
}

// libvirtState : contrôle d'un domaine selon son état (virDomainState) et sa raison.
func libvirtState(state, reason int, autostart bool) (int, string) {
	switch state {
	case 1:
		return model.CheckOK, "en marche"
	case 2:
		return model.CheckOK, "en marche (bloqué sur une ressource)"
	case 3:
		switch reason {
		case 5:
			return model.CheckCrit, "en pause sur erreur d'entrée-sortie (stockage plein ou injoignable)"
		case 6:
			return model.CheckCrit, "en pause par le chien de garde"
		case 10, 13, 14:
			return model.CheckCrit, "en pause après une panne"
		case 2, 3, 4, 7, 9, 11, 12:
			return model.CheckOK, "en pause le temps d'une opération"
		}
		return model.CheckWarn, "en pause"
	case 6:
		return model.CheckCrit, "planté"
	case 7:
		return model.CheckOK, "en veille"
	case 4, 5:
		if autostart {
			return model.CheckCrit, "arrêté alors que son démarrage automatique est actif"
		}
		return model.CheckOK, "arrêté"
	}
	return model.CheckUnknown, "état inconnu"
}

func (l *Libvirt) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := l.det.skip(now); skip {
		return nil, st
	}
	ts := model.Ms(now)
	out, err := l.run(ctx, "domstats", "--raw", "--state", "--cpu-total", "--balloon", "--vcpu", "--block", "--interface")
	if err != nil {
		if err == errNoVirsh { // pas de libvirt sur cette machine
			if l.cfg.Enabled == "true" {
				return nil, "erreur : " + err.Error()
			}
			return nil, "absent"
		}
		if silent, st := l.det.failed(err.Error()); silent {
			return nil, st
		}
		return []model.Check{canConnect("libvirt", ts, false, model.Truncate(err.Error(), 300), tags("libvirt_uri", l.cfg.URI))}, "erreur : " + err.Error()
	}
	l.det.ok()
	doms, order := parseDomstats(out)
	auto := map[string]bool{}
	if a, err := l.run(ctx, "list", "--all", "--autostart", "--name"); err == nil {
		for _, n := range lines(a) {
			auto[n] = true
		}
	}
	elapsed := now.Sub(l.at).Seconds()
	cur := map[string]libvirtCounters{}
	running := 0
	var checks []model.Check
	for _, name := range order {
		m := doms[name]
		kv := []string{"libvirt_domain", name}
		state, _ := fnum(m, "state.state")
		reason, _ := fnum(m, "state.reason")
		st, msg := libvirtState(int(state), int(reason), auto[name])
		if int(state) == 1 || int(state) == 2 {
			running++
		}
		checks = append(checks, model.Check{TS: ts, Name: "libvirt.domain", Status: st, Message: name + " " + msg, Tags: tags(kv...)})
		vcpus, _ := fnum(m, "vcpu.current")
		if vcpus > 0 {
			b.Gauge("libvirt.domain.vcpus", "core", vcpus, kv...)
		}
		if v, ok := fnum(m, "balloon.current"); ok {
			b.Gauge("libvirt.domain.mem.current", "byte", v*1024, kv...)
		}
		if v, ok := fnum(m, "balloon.rss"); ok {
			b.Gauge("libvirt.domain.mem.rss", "byte", v*1024, kv...)
		}
		var c libvirtCounters
		c.cpu, _ = fnum(m, "cpu.time")
		c.rd, _ = sumIdx(m, "block", "rd.bytes")
		c.wr, _ = sumIdx(m, "block", "wr.bytes")
		c.rx, _ = sumIdx(m, "net", "rx.bytes")
		c.tx, _ = sumIdx(m, "net", "tx.bytes")
		cur[name] = c
		if p, ok := l.prev[name]; ok && elapsed > 0 {
			if d, ok := deltaF(c.cpu, p.cpu); ok && vcpus > 0 {
				b.Gauge("libvirt.domain.cpu.usage", "percent", round1(100*d/1e9/elapsed/vcpus), kv...)
			}
			for _, x := range []struct {
				name    string
				now, pr float64
			}{{"libvirt.domain.disk.read", c.rd, p.rd}, {"libvirt.domain.disk.write", c.wr, p.wr},
				{"libvirt.domain.net.rx", c.rx, p.rx}, {"libvirt.domain.net.tx", c.tx, p.tx}} {
				if d, ok := deltaF(x.now, x.pr); ok {
					b.Add(x.name, model.TypeCount, "byte", d, kv...)
				}
			}
		}
	}
	l.prev, l.at = cur, now
	b.Gauge("libvirt.domains.total", "count", float64(len(order)))
	b.Gauge("libvirt.domains.running", "count", float64(running))

	pools := 0
	if pl, err := l.run(ctx, "pool-list", "--all", "--name"); err == nil {
		names := lines(pl)
		sort.Strings(names)
		for _, p := range names {
			info, err := l.run(ctx, "pool-info", "--bytes", p)
			if err != nil {
				continue
			}
			pools++
			m := parseKV(info)
			kv := []string{"libvirt_pool", p}
			st, msg := model.CheckOK, "actif"
			switch m["State"] {
			case "running":
				capa, ok1 := fnum(m, "Capacity")
				alloc, ok2 := fnum(m, "Allocation")
				avail, ok3 := fnum(m, "Available")
				if ok1 && ok2 && capa > 0 {
					pct := round1(100 * alloc / capa)
					b.Gauge("libvirt.pool.usage", "percent", pct, kv...)
					msg = fmt.Sprintf("actif, occupé à %g %%", pct)
				}
				if ok3 {
					b.Gauge("libvirt.pool.available", "byte", avail, kv...)
				}
			case "inaccessible":
				st, msg = model.CheckCrit, "inaccessible"
			default:
				if m["Autostart"] == "yes" {
					st, msg = model.CheckCrit, "inactif alors que son démarrage automatique est actif"
				} else {
					msg = "inactif"
				}
			}
			checks = append(checks, model.Check{TS: ts, Name: "libvirt.pool", Status: st, Message: "pool " + p + " " + msg, Tags: tags(kv...)})
		}
	}
	checks = append(checks, canConnect("libvirt", ts, true,
		fmt.Sprintf("%s : %d domaine(s), %d en marche, %d pool(s)", l.cfg.URI, len(order), running, pools), tags("libvirt_uri", l.cfg.URI)))
	return checks, "ok"
}
