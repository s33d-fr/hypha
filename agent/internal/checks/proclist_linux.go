//go:build linux

package checks

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"

	"s33d.fr/hypha/agent/internal/hostfs"
)

// listProcesses lit /proc/<pid>/comm (et cmdline si un contrôle le demande).
func listProcesses(_ context.Context, needCmdline bool) ([]procEntry, error) {
	dirs, err := filepath.Glob(hostfs.Proc("[0-9]*"))
	if err != nil {
		return nil, err
	}
	out := make([]procEntry, 0, len(dirs))
	for _, d := range dirs {
		comm, err := os.ReadFile(filepath.Join(d, "comm"))
		if err != nil {
			continue // processus terminé entre-temps
		}
		p := procEntry{name: strings.TrimSpace(string(comm))}
		if needCmdline {
			if cl, err := os.ReadFile(filepath.Join(d, "cmdline")); err == nil {
				p.cmdline = strings.TrimSpace(string(bytes.ReplaceAll(cl, []byte{0}, []byte{' '})))
				p.argv0 = argv0Name(p.cmdline)
			}
		}
		out = append(out, p)
	}
	return out, nil
}
