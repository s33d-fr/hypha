//go:build !linux && !windows

package actions

import (
	"context"
	"os/exec"
)

func prepareCmd(*exec.Cmd) {}

func serviceAction(context.Context, string, string) (int, string) {
	return -1, "pilotage des services non pris en charge sur ce système"
}

func scriptUnsafe(string) string { return "scripts non pris en charge sur ce système" }

func runScript(context.Context, string) (int, string) { return -1, "non pris en charge" }
