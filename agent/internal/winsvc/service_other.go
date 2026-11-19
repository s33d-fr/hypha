//go:build !windows

// Package winsvc : sous Linux, le service est l'unité systemd hypha-agent.service ; ces
// fonctions ne servent qu'à rendre un message clair si on les appelle.
package winsvc

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"
)

// Name : nom de l'unité systemd.
const Name = "hypha-agent"

var errLinux = errors.New("commande réservée à Windows ; sous Linux : systemctl enable --now hypha-agent (ou install.sh)")

// IsService : jamais sous Linux (systemd lance « hypha-agent run »).
func IsService() bool { return false }

// RunService n'existe que sous Windows.
func RunService(func(ctx context.Context) error) error { return errLinux }

// Install n'existe que sous Windows.
func Install(string, ...string) error { return errLinux }

// Uninstall n'existe que sous Windows.
func Uninstall() error { return errLinux }

// Start n'existe que sous Windows.
func Start() error { return errLinux }

// Stop n'existe que sous Windows.
func Stop() error { return errLinux }

// RestartWhenStopped n'existe que sous Windows (sous Linux, systemd relance l'agent).
func RestartWhenStopped(time.Duration) error { return errLinux }

// State rend l'état de l'unité systemd.
func State() string {
	out, err := exec.Command("systemctl", "is-active", Name+".service").Output()
	s := strings.TrimSpace(string(out))
	switch {
	case s == "active":
		return "actif"
	case s != "":
		return s
	case err != nil:
		return "inconnu (" + err.Error() + ")"
	}
	return "inconnu"
}
