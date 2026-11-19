//go:build !windows

package integrations

import "s33d.fr/hypha/agent/internal/config"

// platformIntegrations : IIS et SQL Server n'existent que sous Windows.
func platformIntegrations(config.IntegrationsConfig) []integration { return nil }
