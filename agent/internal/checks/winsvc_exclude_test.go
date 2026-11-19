package checks

import (
	"io"
	"log/slog"
	"testing"
)

// Noms réels : les deux faux CRIT relevés sur un serveur Windows doivent être écartés, les
// services d'infrastructure jamais.
func TestWindowsAutoExclude(t *testing.T) {
	ex := compileExcludes([]string{`^MonAppli\d+$`}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, name := range []string{
		"GoogleUpdaterService159.0.8140.0", "GoogleUpdaterInternalService159.0.8140.0",
		"gupdate", "gupdatem", "edgeupdate", "edgeupdatem", "MicrosoftEdgeElevationService",
		"sppsvc", "BITS", "clr_optimization_v4.0.30319_64", "CDPUserSvc_4a8b2", "monappli42", "spice-agent", "vdservice",
	} {
		if !excludedService(name, ex) {
			t.Errorf("%s devrait être écarté", name)
		}
	}
	for _, name := range []string{
		"Spooler", "W32Time", "WinRM", "MSSQLSERVER", "SQLSERVERAGENT", "Dnscache", "EventLog",
		"BITSx", "MapsBroker2", "HyphaAgent", "wuauserv2",
	} {
		if excludedService(name, ex) {
			t.Errorf("%s ne doit pas être écarté", name)
		}
	}
}
