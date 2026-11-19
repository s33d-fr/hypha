//go:build windows

// Package winsvc pose et pilote le service Windows « Hypha Agent » (contrat § 5) :
// démarrage automatique, relance sur échec, compte LocalSystem (lecture des journaux
// d'événements et de tous les processus).
package winsvc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// Name est le nom court du service (sc query HyphaAgent) ; DisplayName celui des consoles.
const (
	Name        = "HyphaAgent"
	DisplayName = "Hypha Agent"
	description = "Agent de collecte HYPHA (S33D) : métriques, journaux, contrôles, processus."
)

// IsService dit si le binaire a été lancé par le gestionnaire de services.
func IsService() bool {
	ok, err := svc.IsWindowsService()
	return err == nil && ok
}

// RunService fait tourner run sous le contrôle du gestionnaire de services ; run reçoit
// un contexte annulé à l'arrêt du service ou de la machine.
func RunService(run func(ctx context.Context) error) error {
	return svc.Run(Name, &handler{run: run})
}

type handler struct {
	run func(ctx context.Context) error
}

func (h *handler) Execute(_ []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.run(ctx) }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case c := <-req:
			switch c.Cmd {
			case svc.Interrogate:
				status <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending, WaitHint: 20000}
				cancel()
				select {
				case <-done:
				case <-time.After(20 * time.Second):
				}
				return false, 0
			}
		case err := <-done:
			// Arrêt de l'agent sans demande (conf invalide…) : un code non nul déclenche la
			// relance sur échec configurée à l'installation.
			if err != nil {
				return true, 1
			}
			return false, 0
		}
	}
}

// Install crée le service (démarrage automatique, relance sur échec). Sans effet s'il existe.
func Install(exe string, args ...string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("gestionnaire de services (droits d'administrateur requis) : %w", err)
	}
	defer m.Disconnect()
	if s, err := m.OpenService(Name); err == nil {
		s.Close()
		return nil
	}
	s, err := m.CreateService(Name, exe, mgr.Config{
		DisplayName:  DisplayName,
		Description:  description,
		StartType:    mgr.StartAutomatic,
		ErrorControl: mgr.ErrorNormal,
	}, args...)
	if err != nil {
		return fmt.Errorf("création du service : %w", err)
	}
	defer s.Close()
	// Relance après 10 s, puis 30 s, puis 60 s ; compteur remis à zéro après un jour sans échec.
	actions := []mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 10 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 60 * time.Second},
	}
	if err := s.SetRecoveryActions(actions, 86400); err != nil {
		return fmt.Errorf("relance sur échec : %w", err)
	}
	// Relancer aussi quand l'agent s'arrête seul avec un code d'erreur (pas seulement sur plantage).
	if err := s.SetRecoveryActionsOnNonCrashFailures(true); err != nil {
		return fmt.Errorf("relance sur échec : %w", err)
	}
	return nil
}

// Uninstall arrête puis supprime le service.
func Uninstall() error {
	_ = Stop()
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(Name)
	if err != nil {
		return nil // déjà absent
	}
	defer s.Close()
	return s.Delete()
}

// Start démarre le service ; sans effet s'il tourne déjà.
func Start() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(Name)
	if err != nil {
		return fmt.Errorf("service %s absent : lancer d'abord « hypha-agent install »", Name)
	}
	defer s.Close()
	if st, err := s.Query(); err == nil && st.State == svc.Running {
		return nil
	}
	if err := s.Start(); err != nil && !errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
		return err
	}
	return waitState(s, svc.Running, 30*time.Second)
}

// Stop arrête le service et attend qu'il le soit (30 s au plus).
func Stop() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(Name)
	if err != nil {
		return nil
	}
	defer s.Close()
	st, err := s.Query()
	if err != nil || st.State == svc.Stopped {
		return err
	}
	if _, err := s.Control(svc.Stop); err != nil {
		return err
	}
	return waitState(s, svc.Stopped, 30*time.Second)
}

func waitState(s *mgr.Service, want svc.State, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		st, err := s.Query()
		if err != nil {
			return err
		}
		if st.State == want {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("le service n'a pas atteint l'état attendu en %s", timeout)
}

// RestartWhenStopped attend l'arrêt du service (timeout au plus) puis le démarre : relance
// après une mise à jour, lancée par un processus détaché du service qui s'arrête.
func RestartWhenStopped(timeout time.Duration) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(Name)
	if err != nil {
		return err
	}
	defer s.Close()
	if err := waitState(s, svc.Stopped, timeout); err != nil {
		return err
	}
	if err := s.Start(); err != nil && !errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
		return err
	}
	return waitState(s, svc.Running, 30*time.Second)
}

// State rend l'état du service en clair.
func State() string {
	m, err := mgr.Connect()
	if err != nil {
		return "inconnu (" + err.Error() + ")"
	}
	defer m.Disconnect()
	s, err := m.OpenService(Name)
	if err != nil {
		return "non installé"
	}
	defer s.Close()
	st, err := s.Query()
	if err != nil {
		return "inconnu (" + err.Error() + ")"
	}
	switch st.State {
	case svc.Running:
		return "actif"
	case svc.Stopped:
		return "arrêté"
	case svc.StartPending:
		return "en démarrage"
	case svc.StopPending:
		return "en arrêt"
	}
	return fmt.Sprintf("état %d", st.State)
}
