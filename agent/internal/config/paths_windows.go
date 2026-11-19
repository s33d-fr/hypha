//go:build windows

package config

import (
	"os"
	"path/filepath"
)

// programData rend %ProgramData% (C:\ProgramData sur une installation standard).
func programData() string {
	if d := os.Getenv("ProgramData"); d != "" {
		return d
	}
	return `C:\ProgramData`
}

// DefaultPath : emplacement de la conf sous Windows (contrat § 5).
func DefaultPath() string { return filepath.Join(programData(), "Hypha", "hypha.yaml") }

// StateDir : identité, curseurs, signets des journaux d'événements, tampon, journal de l'agent.
func StateDir() string { return filepath.Join(programData(), "Hypha") }
