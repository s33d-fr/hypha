//go:build linux

package plugins

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

func plugin(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "check_essai")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.Chmod(p, 0o755)
	return p
}

func run(t *testing.T, cmd []string, timeout time.Duration) Result {
	t.Helper()
	c := &Check{cfg: config.PluginCheck{Name: "essai", Command: cmd, Timeout: config.Duration(timeout)}}
	return c.Exec(context.Background())
}

func TestExecCodes(t *testing.T) {
	p := plugin(t, `echo "LOAD WARNING - $1 $2|load1=$1;1;2;0;"; exit 1`)
	r := run(t, []string{p, "1.5", "$HOME;*|x"}, 5*time.Second)
	if r.Status != model.CheckWarn || r.Err != "" || !strings.HasPrefix(r.Output, "LOAD WARNING - 1.5 $HOME;*|x") {
		t.Fatalf("code 1 ou arguments interprétés : %+v", r)
	}
	if r := run(t, []string{plugin(t, "exit 7")}, 5*time.Second); r.Status != model.CheckUnknown {
		t.Fatalf("code 7 : %+v", r)
	}
	if r := run(t, []string{plugin(t, "echo CRIT; exit 2")}, 5*time.Second); r.Status != model.CheckCrit {
		t.Fatalf("code 2 : %+v", r)
	}
	if r := run(t, []string{plugin(t, "env")}, 5*time.Second); strings.Contains(r.Output, "HYPHA") || !strings.Contains(r.Output, "LC_ALL=C") {
		t.Fatalf("environnement : %q", r.Output)
	}
	start := time.Now()
	r = run(t, []string{plugin(t, "sleep 30 & sleep 30; echo fini")}, time.Second)
	if r.Status != model.CheckCrit || !strings.Contains(r.Err, "délai") || time.Since(start) > 8*time.Second {
		t.Fatalf("délai : %+v en %s", r, time.Since(start))
	}
	big := plugin(t, `i=0; while [ $i -lt 20000 ]; do echo "ligne numéro $i de la sortie"; i=$((i+1)); done`)
	if r := run(t, []string{big}, 10*time.Second); len(r.Output) > maxStdout+100 || !strings.Contains(r.Output, "sortie coupée") {
		t.Fatalf("sortie non bornée : %d octets", len(r.Output))
	}
	if r := run(t, []string{"check_qui_n_existe_pas"}, time.Second); r.Status != model.CheckUnknown || !strings.Contains(r.Err, "introuvable") {
		t.Fatalf("greffon absent : %+v", r)
	}
}

func TestUnsafeFiles(t *testing.T) {
	p := plugin(t, "exit 0")
	os.Chmod(p, 0o777)
	if r := run(t, []string{p}, time.Second); r.Status != model.CheckUnknown || !strings.Contains(r.Err, "modifiable") {
		t.Fatalf("greffon modifiable par tous accepté : %+v", r)
	}
	conf := filepath.Join(t.TempDir(), "50-greffons.yaml")
	os.WriteFile(conf, []byte("integrations: {}\n"), 0o666)
	os.Chmod(conf, 0o666)
	m := New([]config.PluginCheck{{Name: "x", Command: []string{plugin(t, "exit 0")}, Timeout: config.Duration(time.Second)}},
		Options{ConfigFiles: []string{conf}, Tags: func(kv ...string) map[string]string { return map[string]string{} }})
	if r := m.list[0].Exec(context.Background()); r.Status != model.CheckUnknown || !strings.Contains(r.Err, "fichier de conf") {
		t.Fatalf("conf modifiable par tous acceptée : %+v", r)
	}
}
