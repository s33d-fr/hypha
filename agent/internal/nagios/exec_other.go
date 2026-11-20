//go:build !linux

package nagios

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

// prepareCmd : hors Linux, pas de changement de compte ; le processus est tué au délai dépassé
// (les droits des fichiers relèvent des ACL : dossier de l'agent réservé aux administrateurs).
func prepareCmd(_ *exec.Cmd, account string) error {
	if account != "" {
		return fmt.Errorf("user %s : changement de compte non pris en charge sur %s", account, runtime.GOOS)
	}
	return nil
}

func unsafeConfig(string) string { return "" }

func unsafeExecutable(p string) string {
	fi, err := os.Stat(p)
	if err != nil {
		return fmt.Sprintf("plugin %s introuvable", p)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Sprintf("plugin %s : fichier ordinaire attendu", p)
	}
	return ""
}
