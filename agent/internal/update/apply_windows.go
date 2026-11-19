//go:build windows

package update

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// RestartHelperCommand : sous-commande cachée qui relance le service une fois arrêté.
const RestartHelperCommand = "__restart-service"

// install : l'exécutable en cours ne peut pas être écrasé, mais il peut être renommé ; il
// devient <exe>.old (effacé au démarrage suivant), le nouveau prend sa place.
func install(p installPlan) error {
	old := p.exe + ".old"
	_ = os.Remove(old)
	tmp := p.exe + ".new"
	if err := copyFile(p.newPath, tmp, 0o755); err != nil {
		return fmt.Errorf("écriture de %s : %w", tmp, err)
	}
	if err := os.Rename(p.exe, old); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("renommage de l'exécutable en cours : %w", err)
	}
	if err := os.Rename(tmp, p.exe); err != nil {
		_ = os.Rename(old, p.exe)
		os.Remove(tmp)
		return fmt.Errorf("pose du nouvel exécutable : %w", err)
	}
	return nil
}

// ApplyStaged : sans objet sous Windows (le binaire est posé directement).
func ApplyStaged(string) error { return nil }

func cleanupOld() {
	if exe, err := os.Executable(); err == nil {
		_ = os.Remove(exe + ".old")
	}
}

// Relaunch lance, détaché, le nouvel exécutable avec la sous-commande qui attend l'arrêt du
// service puis le redémarre. Code non nul : l'aide n'a pas pu partir, le service doit alors
// sortir en erreur pour que la relance sur échec du gestionnaire de services prenne le relais.
func Relaunch() int {
	exe, err := os.Executable()
	if err != nil {
		return 1
	}
	cmd := exec.Command(exe, RestartHelperCommand)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP,
	}
	if err := cmd.Start(); err != nil {
		return 1
	}
	_ = cmd.Process.Release()
	return 0
}
