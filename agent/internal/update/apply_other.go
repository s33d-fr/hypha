//go:build !linux && !windows

package update

import "errors"

func install(installPlan) error {
	return errors.New("mise à jour automatique non prise en charge sur ce système")
}

// ApplyStaged : sans objet hors Linux.
func ApplyStaged(string) error { return nil }

func cleanupOld() {}

// Relaunch : sans objet hors Linux et Windows.
func Relaunch() int { return 0 }
