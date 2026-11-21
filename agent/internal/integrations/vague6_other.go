//go:build !windows

package integrations

import "s33d.fr/hypha/agent/internal/config"

// wsusIntegrations : WSUS n'existe que sous Windows.
func wsusIntegrations(config.IntegrationsConfig) []integration { return nil }
