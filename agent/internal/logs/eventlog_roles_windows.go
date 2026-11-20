//go:build windows

package logs

import (
	"log/slog"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc/mgr"

	"s33d.fr/hypha/agent/internal/config"
)

var (
	procEvtOpenChannelConfig = modwevtapi.NewProc("EvtOpenChannelConfig")
)

// EventlogChannels ajoute aux canaux de la conf ceux des rôles présents (vague 3) : Directory
// Service et DFS Replication sur un contrôleur de domaine, DNS Server sur un serveur DNS, sauf
// réglage logs: false du rôle ou canal déjà listé (l'entrée de la conf, avec ses filtres, l'emporte).
func EventlogChannels(list []config.EventlogChannel, integ config.IntegrationsConfig, log *slog.Logger) []config.EventlogChannel {
	var extra []string
	if integ.ActiveDirectory.LogsOn() && serviceExists("NTDS") {
		extra = append(extra, "Directory Service", "DFS Replication")
	}
	if integ.WindowsDNS.LogsOn() && serviceExists("DNS") {
		extra = append(extra, "DNS Server")
	}
	var present []string
	for _, ch := range extra {
		if channelExists(ch) {
			present = append(present, ch)
		}
	}
	out := addChannels(list, present...)
	if len(out) > len(list) {
		log.Info("journaux d'événements des rôles Windows ajoutés", "canaux", config.EventlogNames(out[len(list):]))
	}
	return out
}

func serviceExists(name string) bool {
	m, err := mgr.Connect()
	if err != nil {
		return false
	}
	defer m.Disconnect()
	s, err := m.OpenService(name)
	if err != nil {
		return false
	}
	s.Close()
	return true
}

// channelExists : le canal est enregistré auprès du service de journalisation.
func channelExists(name string) bool {
	if procEvtOpenChannelConfig.Find() != nil {
		return true // vérification impossible : la lecture dira l'erreur
	}
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return false
	}
	h, _, _ := procEvtOpenChannelConfig.Call(0, uintptr(unsafe.Pointer(p)), 0)
	if h == 0 {
		return false
	}
	procEvtClose.Call(h)
	return true
}
