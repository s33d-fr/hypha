//go:build windows

package integrations

import "s33d.fr/hypha/agent/internal/config"

// wsusIntegrations : sonde WSUS (coupée par défaut), service témoin WsusService.
func wsusIntegrations(cfg config.IntegrationsConfig) []integration {
	return []integration{newWSUS(cfg.WSUS, roleDeps("WsusService", wsusScriptFor(cfg.WSUS.StaleDays)))}
}
