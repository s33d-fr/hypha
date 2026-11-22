package actions

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/state"
)

func testCfg() config.ActionsConfig {
	c := config.Default().Actions
	c.Enabled = true
	c.Allow = append(c.Allow, "run_script", "update_agent")
	c.Timeout = config.Duration(5 * time.Second)
	return c
}

func TestDecide(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("noms d'unités systemd")
	}
	e := New(testCfg(), &state.Store{Dir: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)), Deps{})
	now := time.Now()
	act := func(kind string, params map[string]any) Action {
		return Action{ID: 1, Kind: kind, Params: params, deadline: now.Add(time.Minute)}
	}
	cases := []struct {
		a      Action
		target string
		why    string
	}{
		{act("restart_service", map[string]any{"unit": "nginx"}), "nginx.service", ""},
		{act("stop_service", map[string]any{"service": "postgresql@16-main.service"}), "postgresql@16-main.service", ""},
		{act("restart_service", map[string]any{"unit": "sshd"}), "", "deny_services"},
		{act("restart_service", map[string]any{"unit": "ssh.service"}), "", "deny_services"},
		{act("restart_service", map[string]any{"unit": "systemd-journald"}), "", "deny_services"},
		{act("restart_service", map[string]any{"unit": "hypha-agent"}), "", "service de l'agent"},
		{act("restart_service", map[string]any{"unit": "nginx; rm -rf /"}), "", "refusé"},
		{act("restart_service", map[string]any{"unit": "--force"}), "", "refusé"},
		{act("restart_service", nil), "", "manquant"},
		{act("run_command", map[string]any{"cmd": "id"}), "", "inconnu"},
		{act("run_script", map[string]any{"script": "absent"}), "", "absent de actions.scripts"},
		{act("update_agent", map[string]any{"version": "0.4.0"}), "", "mise à jour impossible"},
		{Action{ID: 2, Kind: "restart_service", Params: map[string]any{"unit": "nginx"}, deadline: now.Add(-time.Second)}, "", "expirée"},
	}
	for _, c := range cases {
		p, why := e.decide(c.a, now)
		if c.why == "" && (why != "" || p.target != c.target) {
			t.Errorf("%s %v : refus inattendu %q (cible %q)", c.a.Kind, c.a.Params, why, p.target)
		}
		if c.why != "" && !strings.Contains(why, c.why) {
			t.Errorf("%s %v : motif %q, attendu « %s »", c.a.Kind, c.a.Params, why, c.why)
		}
	}
	off := testCfg()
	off.Enabled = false
	e2 := New(off, &state.Store{Dir: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)), Deps{})
	if _, why := e2.decide(act("restart_service", map[string]any{"unit": "nginx"}), now); !strings.Contains(why, "actions.enabled") {
		t.Fatalf("actions coupées : %q", why)
	}
	limited := testCfg()
	limited.Services = []string{"nginx*", "php*-fpm"}
	e3 := New(limited, &state.Store{Dir: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)), Deps{})
	if _, why := e3.decide(act("restart_service", map[string]any{"unit": "php8.3-fpm"}), now); why != "" {
		t.Fatalf("motif de services : %q", why)
	}
	if _, why := e3.decide(act("restart_service", map[string]any{"unit": "mysql"}), now); !strings.Contains(why, "hors de actions.services") {
		t.Fatalf("service hors liste : %q", why)
	}
}

type reports struct {
	mu   sync.Mutex
	list map[int64][]Result
}

func (r *reports) add(_ context.Context, id int64, res Result) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.list[id] = append(r.list[id], res)
}

func (r *reports) last(id int64) (Result, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	l := r.list[id]
	if len(l) == 0 {
		return Result{}, 0
	}
	return l[len(l)-1], len(l)
}

func writeScript(t *testing.T, dir, name, body string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExecutorScripts(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("scripts shell")
	}
	dir := t.TempDir()
	cfg := testCfg()
	cfg.Timeout = config.Duration(2 * time.Second)
	cfg.Scripts = map[string]string{
		"bavard":   writeScript(t, dir, "bavard.sh", "head -c 200000 /dev/zero | tr '\\0' 'x'; echo; exit 3", 0o755),
		"lent":     writeScript(t, dir, "lent.sh", "sleep 30", 0o755),
		"ouvert":   writeScript(t, dir, "ouvert.sh", "echo non", 0o777),
		"env":      writeScript(t, dir, "env.sh", "env", 0o755),
	}
	t.Setenv("HYPHA_API_KEY", "hph_secret_ne_doit_pas_fuir")
	rep := &reports{list: map[int64][]Result{}}
	var updated atomic.Value
	restarted := make(chan struct{})
	e := New(cfg, &state.Store{Dir: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)), Deps{
		Report:  rep.add,
		Update:  func(_ context.Context, v string) error { updated.Store(v); return nil },
		Restart: func() { close(restarted) },
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go e.Run(ctx)
	exp := time.Now().Add(time.Hour).UnixMilli()
	e.Submit([]Action{
		{ID: 10, Kind: "run_script", Params: map[string]any{"script": "bavard"}, ExpiresAt: exp},
		{ID: 11, Kind: "run_script", Params: map[string]any{"script": "lent"}, ExpiresAt: exp},
		{ID: 12, Kind: "run_script", Params: map[string]any{"script": "ouvert"}, ExpiresAt: exp},
		{ID: 13, Kind: "run_script", Params: map[string]any{"script": "env"}, ExpiresAt: exp},
		{ID: 14, Kind: "update_agent", Params: map[string]any{"version": "0.4.0"}, ExpiresAt: exp},
		{ID: 10, Kind: "run_script", Params: map[string]any{"script": "bavard"}, ExpiresAt: exp},
	}, time.Now().UnixMilli())
	deadline := time.Now().Add(15 * time.Second)
	for {
		if r, _ := rep.last(14); r.Status == StatusSucceeded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("actions non traitées à temps")
		}
		time.Sleep(50 * time.Millisecond)
	}
	r, n := rep.last(10)
	if r.Status != StatusFailed || r.ExitCode == nil || *r.ExitCode != 3 || n != 2 {
		t.Errorf("script en échec : %+v (%d comptes rendus : running puis final, action rejouée ?)", r, n)
	}
	if len(r.Output) > MaxOutput || !strings.Contains(r.Output, "sortie coupée") {
		t.Errorf("sortie non bornée : %d octets", len(r.Output))
	}
	if r, _ := rep.last(11); r.Status != StatusFailed || !strings.Contains(r.Output, "délai dépassé") {
		t.Errorf("délai : %+v", r)
	}
	if r, _ := rep.last(12); r.Status != StatusRefused || !strings.Contains(r.Output, "modifiable") {
		t.Errorf("script modifiable par tous : %+v", r)
	}
	if r, _ := rep.last(13); r.Status != StatusSucceeded || strings.Contains(r.Output, "hph_secret") {
		t.Errorf("clé d'API transmise au script : %+v", r.Status)
	}
	select {
	case <-restarted:
	case <-time.After(5 * time.Second):
		t.Error("agent non relancé après la mise à jour")
	}
	if v, _ := updated.Load().(string); v != "0.4.0" {
		t.Errorf("mise à jour : version %q", v)
	}
	// Une action relivrée après une relance n'est pas rejouée (identifiants sur disque).
	e2 := New(cfg, e.store, slog.New(slog.NewTextHandler(io.Discard, nil)), Deps{Report: rep.add})
	if !e2.markSeen(13) {
		t.Fatal("identifiant traité oublié à la relance")
	}
}

func TestCapabilities(t *testing.T) {
	cfg := testCfg()
	cfg.Scripts = map[string]string{"b": "/b.sh", "a": "/a.sh"}
	e := New(cfg, &state.Store{Dir: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)), Deps{})
	c := e.Capabilities()
	if !c.Enabled || strings.Join(c.Scripts, ",") != "a,b" || strings.Contains(strings.Join(c.Kinds, ","), "update_agent") {
		t.Fatalf("capacités : %+v", c)
	}
	e = New(cfg, &state.Store{Dir: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)), Deps{Update: func(context.Context, string) error { return errors.New("x") }})
	if !strings.Contains(strings.Join(e.Capabilities().Kinds, ","), "update_agent") {
		t.Fatal("update_agent absent alors que la mise à jour est possible")
	}
	// Motifs de services annoncés : le serveur ne propose que ce que l'agent acceptera.
	cfg.Allow = []string{"restart_service"}
	cfg.Services = []string{"nginx*"}
	cfg.DenyServices = []string{"sshd*"}
	c = New(cfg, &state.Store{Dir: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)), Deps{}).Capabilities()
	if strings.Join(c.Services, ",") != "nginx*" || !slices.Contains(c.DenyServices, "sshd*") || !slices.Contains(c.DenyServices, "hypha-agent.service") {
		t.Fatalf("motifs de services : %+v", c)
	}
	cfg.Allow = []string{"run_script"}
	if c = New(cfg, &state.Store{Dir: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)), Deps{}).Capabilities(); len(c.Services) != 0 {
		t.Fatalf("motifs annoncés sans geste sur un service : %+v", c)
	}
}

func TestBound(t *testing.T) {
	var b limitedBuffer
	b.Write([]byte(strings.Repeat("é", MaxOutput)))
	if s := b.String(); len(s) > MaxOutput || !strings.Contains(s, "sortie coupée") {
		t.Fatalf("borne : %d", len(s))
	}
}
