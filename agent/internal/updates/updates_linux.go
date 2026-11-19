//go:build linux

package updates

import (
	"context"
	"errors"
	"os"
	"os/exec"

	"s33d.fr/hypha/agent/internal/hostfs"
)

func lookPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// collect : apt (Debian, Ubuntu) ou dnf/yum (Red Hat et dérivées), cache local seulement.
func collect(ctx context.Context) Result {
	var r Result
	switch {
	case lookPath("apt"):
		r.Source = "apt"
		cmd := exec.CommandContext(ctx, "apt", "list", "--upgradable")
		cmd.Env = append(os.Environ(), "LC_ALL=C")
		out, err := cmd.Output()
		if err != nil {
			r.Err = err
			return r
		}
		r.List = parseAptUpgradable(string(out))
		r.Known = true
	case lookPath("dnf") || lookPath("yum"):
		tool := "dnf"
		if !lookPath("dnf") {
			tool = "yum"
		}
		r.Source = tool
		out, err := exec.CommandContext(ctx, tool, "-q", "-C", "check-update").Output()
		var ee *exec.ExitError
		// Code 100 : des mises à jour attendent ; 0 : aucune.
		if err != nil && !(errors.As(err, &ee) && ee.ExitCode() == 100) {
			r.Err = err
			return r
		}
		r.List = parseDnfCheckUpdate(string(out))
		if sec, err := exec.CommandContext(ctx, tool, "-q", "-C", "updateinfo", "list", "--security").Output(); err == nil {
			set := parseDnfSecurity(string(sec))
			for i := range r.List {
				r.List[i].Security = set[r.List[i].Name]
			}
		}
		r.Known = true
		if lookPath("needs-restarting") {
			// needs-restarting -r : code 1 = redémarrage requis.
			err := exec.CommandContext(ctx, "needs-restarting", "-r").Run()
			r.RebootRequired = errors.As(err, &ee) && ee.ExitCode() == 1
		}
	default:
		r.Err = errors.New("ni apt ni dnf")
		return r
	}
	r.Pending, r.Security = len(r.List), countSecurity(r.List)
	return r
}

// quickReboot : /var/run/reboot-required (Debian, Ubuntu).
func quickReboot() (bool, bool) {
	if !lookPath("apt") {
		return false, false
	}
	_, err := os.Stat(hostfs.Root("/var/run/reboot-required"))
	return err == nil, true
}
