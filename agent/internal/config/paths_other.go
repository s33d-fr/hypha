//go:build !windows

package config

// DefaultPath : emplacement de la conf sous Linux (contrat § 5).
func DefaultPath() string { return "/etc/hypha/hypha.yaml" }

// StateDir : identité, curseurs de journaux, positions de fichiers, tampon.
func StateDir() string { return "/var/lib/hypha" }
