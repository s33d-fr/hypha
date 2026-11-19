//go:build !linux

package checks

import (
	"context"

	"github.com/shirou/gopsutil/v4/process"
)

// listProcesses : noms des processus (et lignes de commande si un contrôle le demande).
func listProcesses(ctx context.Context, needCmdline bool) ([]procEntry, error) {
	ps, err := process.ProcessesWithContext(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]procEntry, 0, len(ps))
	for _, p := range ps {
		n, err := p.NameWithContext(ctx)
		if err != nil {
			continue
		}
		e := procEntry{name: n}
		if needCmdline {
			if cl, err := p.CmdlineWithContext(ctx); err == nil {
				e.cmdline = cl
				e.argv0 = argv0Name(cl)
			}
		}
		out = append(out, e)
	}
	return out, nil
}
