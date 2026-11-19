package checks

import (
	"log/slog"
	"regexp"
)

// Services à démarrage automatique qui s'arrêtent normalement d'eux-mêmes, en motifs
// (expressions régulières, sans distinction de casse) : les services de mise à jour
// portent souvent leur version dans leur nom (GoogleUpdaterService159.0.8140.0), une
// liste de noms exacts ne suffit pas. Liste tirée des faux CRIT relevés sur le parc et de
// la macro {$SERVICE.NAME.NOT_MATCHES} du modèle « Windows by Zabbix agent » de Zabbix.
// checks.windows_services_exclude de la conf s'y ajoute. Les services à démarrage sur
// déclencheur et les services par utilisateur sont écartés en plus, par leur configuration.
var defaultAutoExclude = []string{
	`^GoogleUpdater`, `^gupdate`, `^edgeupdate`, `^MicrosoftEdgeElevationService$`,
	`^sppsvc$`, `^RemoteRegistry$`, `^MapsBroker$`, `^tiledatamodelsvc$`, `^CDPSvc$`,
	`^WbioSrvc$`, `^ShellHWDetection$`, `^TrustedInstaller$`, `^clr_optimization`,
	`^NetTcpPortSharing$`, `^AdobeARMservice$`, `^BITS$`,
	// Agents d'invité d'un hyperviseur SPICE, présents sur des modèles de VM recopiés
	`^spice-agent$`, `^vdservice$`,
	// Reste de la liste Zabbix
	`^MMCSS$`, `^SysmonLog$`, `^gpsvc$`, `^Pml Driver HPZ12$`, `^Net Driver HPZ12$`,
	`^IntelAudioService$`, `^Intel\(R\) TPM Provisioning Service$`, `^dbupdate`, `^DoSvc$`,
	`^CDPUserSvc_`, `^WpnUserService_`, `^OneSyncSvc_`, `^TabletInputService$`, `^wuauserv$`,
	`^cbdhsvc_`, `^GISvc$`,
}

// compileExcludes assemble les motifs par défaut et ceux de la conf (déjà validés par
// check-config ; un motif invalide est ignoré ici plutôt que d'arrêter l'agent).
func compileExcludes(extra []string, log *slog.Logger) []*regexp.Regexp {
	var out []*regexp.Regexp
	for _, p := range append(append([]string{}, defaultAutoExclude...), extra...) {
		re, err := regexp.Compile("(?i)" + p)
		if err != nil {
			log.Warn("motif d'exclusion de service ignoré", "motif", p, "err", err)
			continue
		}
		out = append(out, re)
	}
	return out
}

func excludedService(name string, excludes []*regexp.Regexp) bool {
	for _, re := range excludes {
		if re.MatchString(name) {
			return true
		}
	}
	return false
}
