//go:build windows

package actions

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

func prepareCmd(*exec.Cmd) {}

// serviceAction pilote le service par le gestionnaire de services : arrêt puis démarrage
// pour restart, en attendant chaque fois l'état visé.
func serviceAction(ctx context.Context, verb, name string) (int, string) {
	m, err := mgr.Connect()
	if err != nil {
		return -1, "gestionnaire de services : " + err.Error()
	}
	defer m.Disconnect()
	s, real, err := openService(m, name)
	if err != nil {
		return -1, err.Error()
	}
	defer s.Close()
	var steps []string
	stop := func() error {
		st, err := s.Query()
		if err != nil {
			return err
		}
		if st.State == svc.Stopped {
			steps = append(steps, "déjà arrêté")
			return nil
		}
		if _, err := s.Control(svc.Stop); err != nil && !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
			return fmt.Errorf("arrêt : %w", err)
		}
		if err := wait(ctx, s, svc.Stopped); err != nil {
			return err
		}
		steps = append(steps, "arrêté")
		return nil
	}
	start := func() error {
		if err := s.Start(); err != nil && !errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
			return fmt.Errorf("démarrage : %w", err)
		}
		if err := wait(ctx, s, svc.Running); err != nil {
			return err
		}
		steps = append(steps, "démarré")
		return nil
	}
	switch verb {
	case "stop":
		err = stop()
	case "start":
		err = start()
	case "restart":
		if err = stop(); err == nil {
			err = start()
		}
	default:
		err = fmt.Errorf("geste %s inconnu", verb)
	}
	out := fmt.Sprintf("service %s : %s", real, strings.Join(steps, ", "))
	if err != nil {
		return 1, out + "\n" + err.Error()
	}
	return 0, out
}

// openService ouvre un service par son nom, ou par son nom affiché.
func openService(m *mgr.Mgr, name string) (*mgr.Service, string, error) {
	if s, err := m.OpenService(name); err == nil {
		return s, name, nil
	}
	names, err := m.ListServices()
	if err != nil {
		return nil, "", err
	}
	for _, n := range names {
		s, err := m.OpenService(n)
		if err != nil {
			continue
		}
		if c, err := s.Config(); err == nil && strings.EqualFold(c.DisplayName, name) {
			return s, n, nil
		}
		s.Close()
	}
	return nil, "", fmt.Errorf("service %s introuvable", name)
}

func wait(ctx context.Context, s *mgr.Service, want svc.State) error {
	for {
		st, err := s.Query()
		if err != nil {
			return err
		}
		if st.State == want {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("délai dépassé en attendant l'état %d (état %d)", want, st.State)
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// scriptUnsafe : le script doit exister et être un fichier ordinaire.
func scriptUnsafe(p string) string {
	fi, err := os.Stat(p)
	if err != nil {
		return fmt.Sprintf("script %s introuvable", p)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Sprintf("script %s : fichier ordinaire attendu", p)
	}
	switch strings.ToLower(filepath.Ext(p)) {
	case ".ps1", ".cmd", ".bat", ".exe":
		return ""
	}
	return fmt.Sprintf("script %s : extension .ps1, .cmd, .bat ou .exe attendue", p)
}

// runScript lance le script, sans argument, par son interpréteur.
func runScript(ctx context.Context, p string) (int, string) {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".ps1":
		return runCommand(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", p)
	case ".cmd", ".bat":
		return runCommand(ctx, "cmd.exe", "/d", "/c", p)
	}
	return runCommand(ctx, p)
}
