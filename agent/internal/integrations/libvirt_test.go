package integrations

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

func TestLibvirtDomainsAndPools(t *testing.T) {
	pass := 0
	l := newLibvirt(config.LibvirtConfig{Enabled: "auto", URI: "qemu:///system"})
	l.run = func(_ context.Context, args ...string) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "domstats --raw --state --cpu-total --balloon --vcpu --block --interface":
			// web : 2 vCPU, 60 s de CPU consommées en 60 s entre deux passages = 50 %.
			return []byte(fmt.Sprintf(`Domain: 'web'
  state.state=1
  state.reason=1
  cpu.time=%d
  balloon.current=2097152
  balloon.rss=1048576
  vcpu.current=2
  block.count=1
  block.0.name=vda
  block.0.rd.bytes=%d
  block.0.wr.bytes=1000
  net.count=1
  net.0.rx.bytes=500
  net.0.tx.bytes=700

Domain: 'base'
  state.state=3
  state.reason=5
  vcpu.current=1

Domain: 'sauvegarde'
  state.state=5
  state.reason=1

Domain: 'essai'
  state.state=5
  state.reason=1
`, int64(pass)*60e9, 4096*(pass+1))), nil
		case "list --all --autostart --name":
			return []byte("web\nsauvegarde\n\n"), nil
		case "pool-list --all --name":
			return []byte("default\nnfs\n"), nil
		case "pool-info --bytes default":
			return []byte("Name:           default\nState:          running\nAutostart:      yes\nCapacity:       1000\nAllocation:     925\nAvailable:      75\n"), nil
		case "pool-info --bytes nfs":
			return []byte("Name:           nfs\nState:          inactive\nAutostart:      yes\n"), nil
		}
		return nil, fmt.Errorf("commande inattendue : %v", args)
	}
	var checks []model.Check
	var b *model.SeriesBuilder
	t0 := time.Now()
	for pass = 0; pass < 2; pass++ {
		b = model.NewSeriesBuilder(0, testTags)
		checks, _ = l.collect(context.Background(), t0.Add(time.Duration(pass)*60*time.Second), b, testTags)
	}
	got := map[string]model.Check{}
	for _, c := range checks {
		got[c.Name+"/"+c.Tags["libvirt_domain"]+c.Tags["libvirt_pool"]] = c
	}
	for key, want := range map[string]struct {
		st   int
		text string
	}{
		"libvirt.domain/web":        {model.CheckOK, "web en marche"},
		"libvirt.domain/base":       {model.CheckCrit, "erreur d'entrée-sortie"},
		"libvirt.domain/sauvegarde": {model.CheckCrit, "démarrage automatique est actif"},
		"libvirt.domain/essai":      {model.CheckOK, "essai arrêté"},
		"libvirt.pool/default":      {model.CheckOK, "occupé à 92.5 %"},
		"libvirt.pool/nfs":          {model.CheckCrit, "inactif alors que"},
		"libvirt.can_connect/":      {model.CheckOK, "4 domaine(s), 1 en marche, 2 pool(s)"},
	} {
		c, ok := got[key]
		if !ok || c.Status != want.st || !strings.Contains(c.Message, want.text) {
			t.Fatalf("%s : %+v (attendu %d %q)", key, c, want.st, want.text)
		}
	}
	series := map[string]float64{}
	for _, s := range b.Series() {
		series[s.Metric+"/"+s.Tags["libvirt_domain"]+s.Tags["libvirt_pool"]] = s.Points[0].Value
	}
	for key, want := range map[string]float64{
		"libvirt.domain.cpu.usage/web": 50, "libvirt.domain.mem.current/web": 2 << 30, "libvirt.domain.disk.read/web": 4096,
		"libvirt.pool.usage/default": 92.5, "libvirt.domains.running/": 1,
	} {
		if series[key] != want {
			t.Fatalf("%s = %v (attendu %v) ; %v", key, series[key], want, series)
		}
	}
}

func TestLibvirtAbsentWithoutVirsh(t *testing.T) {
	l := newLibvirt(config.LibvirtConfig{Enabled: "auto", URI: "qemu:///system"})
	l.run = func(context.Context, ...string) ([]byte, error) { return nil, errNoVirsh }
	if checks, st := l.collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags); len(checks) != 0 || st != "absent" {
		t.Fatalf("sans virsh : %q %+v", st, checks)
	}
}

// Essai sur un vrai hôte KVM, lancé à la main : LIBVIRT_REEL=1 (lecture seule).
func TestLibvirtReel(t *testing.T) {
	if os.Getenv("LIBVIRT_REEL") != "1" {
		t.Skip("LIBVIRT_REEL=1 pour lire le libvirt de cette machine")
	}
	l := newLibvirt(config.LibvirtConfig{Enabled: "true", URI: "qemu:///system"})
	var checks []model.Check
	var b *model.SeriesBuilder
	var st string
	for i := 0; i < 2; i++ {
		b = model.NewSeriesBuilder(0, testTags)
		checks, st = l.collect(context.Background(), time.Now(), b, testTags)
		time.Sleep(5 * time.Second)
	}
	t.Logf("== %s", st)
	for _, c := range checks {
		t.Logf("[%d] %s : %s", c.Status, c.Name, c.Message)
	}
	for _, s := range b.Series() {
		t.Logf("série %s %s%s = %v", s.Metric, s.Tags["libvirt_domain"], s.Tags["libvirt_pool"], s.Points[0].Value)
	}
}
