//go:build linux

package integrations

import (
	"fmt"
	"os"
	"syscall"
)

// secretFileProblem : un fichier de phrase de passe (Borg, restic) doit être un fichier ordinaire,
// lisible par son seul propriétaire (aucun droit pour le groupe ni pour les autres), et appartenir à
// root quand l'agent tourne en root. Rend la raison d'un refus, ou « ».
func secretFileProblem(p string) string {
	fi, err := os.Stat(p)
	if err != nil {
		return fmt.Sprintf("fichier %s illisible : %v", p, err)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Sprintf("%s : fichier ordinaire attendu", p)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return fmt.Sprintf("%s ouvert au groupe ou à tous (droits %o, 600 ou 400 attendus) : refusé", p, fi.Mode().Perm())
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && st.Uid != 0 && os.Geteuid() == 0 {
		return fmt.Sprintf("%s : propriétaire uid %d, root attendu : refusé", p, st.Uid)
	}
	return ""
}
