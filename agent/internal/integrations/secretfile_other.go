//go:build !linux

package integrations

import (
	"fmt"
	"os"
)

// secretFileProblem : hors Linux, seule la présence d'un fichier ordinaire est vérifiée (les droits
// des fichiers Windows se règlent par ACL, hors de portée d'un contrôle simple).
func secretFileProblem(p string) string {
	fi, err := os.Stat(p)
	if err != nil {
		return fmt.Sprintf("fichier %s illisible : %v", p, err)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Sprintf("%s : fichier ordinaire attendu", p)
	}
	return ""
}
