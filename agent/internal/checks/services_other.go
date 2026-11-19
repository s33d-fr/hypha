//go:build !linux && !windows

package checks

import (
	"log/slog"

	"s33d.fr/hypha/agent/internal/config"
)

// Ni systemd ni services Windows : seuls les contrôles HTTP, TCP et hypha.agent.up tournent.
func newServiceWatcher(config.ChecksConfig, *slog.Logger) serviceWatcher { return nil }
