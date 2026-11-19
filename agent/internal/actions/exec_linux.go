//go:build linux

package actions

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// prepareCmd : la commande et ses enfants forment un groupe, tué en entier au délai dépassé.
func prepareCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

// serviceAction : systemctl restart|start|stop -- <unité>, puis l'état atteint.
func serviceAction(ctx context.Context, verb, unit string) (int, string) {
	code, out := runCommand(ctx, "systemctl", verb, "--no-ask-password", "--", unit)
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	st, err := exec.CommandContext(sctx, "systemctl", "show", "--property=ActiveState,SubState", "--value", "--", unit).Output()
	if err == nil {
		f := strings.Fields(string(st))
		if len(f) == 2 {
			out = strings.TrimRight(out, "\n")
			if out != "" {
				out += "\n"
			}
			out += fmt.Sprintf("systemctl %s %s : code %d ; état %s (%s)", verb, unit, code, f[0], f[1])
		}
	}
	return code, out
}

// scriptUnsafe refuse un script qu'un autre compte que root pourrait modifier : l'agent le
// lancerait en root.
func scriptUnsafe(p string) string {
	fi, err := os.Stat(p)
	if err != nil {
		return fmt.Sprintf("script %s introuvable", p)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Sprintf("script %s : fichier ordinaire attendu", p)
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return fmt.Sprintf("script %s modifiable par le groupe ou par tous (droits %o) : refusé", p, fi.Mode().Perm())
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && st.Uid != 0 && os.Geteuid() == 0 {
		return fmt.Sprintf("script %s : propriétaire uid %d, root attendu", p, st.Uid)
	}
	if fi.Mode().Perm()&0o100 == 0 {
		return fmt.Sprintf("script %s non exécutable (chmod 755)", p)
	}
	return ""
}

// runScript lance le script, sans argument, depuis la racine.
func runScript(ctx context.Context, p string) (int, string) {
	return runCommand(ctx, p)
}
