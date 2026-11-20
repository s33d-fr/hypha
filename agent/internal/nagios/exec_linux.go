//go:build linux

package nagios

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"syscall"
)

// prepareCmd : le plugin et ses enfants forment un groupe, tué en entier au délai dépassé ;
// avec user, le plugin tourne sous ce compte (l'agent doit être root pour en changer).
func prepareCmd(cmd *exec.Cmd, account string) error {
	attr := &syscall.SysProcAttr{Setpgid: true}
	if account != "" {
		u, err := user.Lookup(account)
		if err != nil {
			return fmt.Errorf("compte %s introuvable", account)
		}
		uid, _ := strconv.ParseUint(u.Uid, 10, 32)
		gid, _ := strconv.ParseUint(u.Gid, 10, 32)
		if os.Geteuid() != 0 && uint32(uid) != uint32(os.Geteuid()) {
			return fmt.Errorf("user %s : l'agent ne tourne pas en root, il ne peut pas changer de compte", account)
		}
		attr.Credential = &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid), NoSetGroups: true}
	}
	cmd.SysProcAttr = attr
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	return nil
}

// unsafeFile : fichier qu'un autre compte que root pourrait modifier (l'agent tourne en root).
func unsafeFile(p, what string) string {
	fi, err := os.Stat(p)
	if err != nil {
		return fmt.Sprintf("%s %s illisible : %v", what, p, err)
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return fmt.Sprintf("%s %s modifiable par le groupe ou par tous (droits %o) : refusé", what, p, fi.Mode().Perm())
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && st.Uid != 0 && os.Geteuid() == 0 {
		return fmt.Sprintf("%s %s : propriétaire uid %d, root attendu : refusé", what, p, st.Uid)
	}
	return ""
}

func unsafeConfig(p string) string { return unsafeFile(p, "fichier de conf") }

func unsafeExecutable(p string) string {
	if why := unsafeFile(p, "plugin"); why != "" {
		return why
	}
	fi, _ := os.Stat(p)
	if !fi.Mode().IsRegular() {
		return fmt.Sprintf("plugin %s : fichier ordinaire attendu", p)
	}
	if fi.Mode().Perm()&0o111 == 0 {
		return fmt.Sprintf("plugin %s non exécutable", p)
	}
	return ""
}
