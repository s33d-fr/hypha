package logs

import (
	"testing"
	"time"

	"s33d.fr/hypha/agent/internal/model"
)

func TestGuessLevel(t *testing.T) {
	cases := map[string]string{
		"2026/11/18 02:15:00 [error] 1234#0: upstream timed out": "error",
		"2026-11-18 02:15:01,123 WARNING urllib3 retrying":       "warning",
		"12:00:00.000 [main] ERROR c.s.App - boom":               "error",
		"ts=1 level=warn msg=x":                                  "warning",
		"an error occurred but this is informational text":       "info",
		"<crit> disque":   "critical",
		"rien de spécial": "info",
	}
	for line, want := range cases {
		if got := GuessLevel(line); got != want {
			t.Errorf("GuessLevel(%q) = %q, attendu %q", line, got, want)
		}
	}
}

func TestApplyLineJSON(t *testing.T) {
	now := time.Date(2026, 11, 18, 2, 0, 0, 0, time.UTC)
	var l model.Log
	l.Attributes = map[string]any{"file": "/var/log/app.log"}
	ApplyLine(&l, `{"time":"2026-11-18T01:50:00Z","level":"ERROR","msg":"refus","trace_id":"abc","http":{"status_code":502},"file":"x"}`, now)
	if l.Message != "refus" || l.Status != "error" || l.TraceID == nil || *l.TraceID != "abc" {
		t.Fatalf("décodage : %+v", l)
	}
	if l.TS != time.Date(2026, 11, 18, 1, 50, 0, 0, time.UTC).UnixMilli() {
		t.Fatalf("horodatage de l'application non repris : %d", l.TS)
	}
	if l.Attributes["file"] != "/var/log/app.log" || l.Attributes["http"] == nil {
		t.Fatalf("attributs : %v", l.Attributes)
	}

	var old model.Log
	ApplyLine(&old, `{"time":"2020-01-01T00:00:00Z","msg":"vieux"}`, now)
	if old.TS != 0 {
		t.Fatalf("horodatage hors fenêtre retenu : %d", old.TS)
	}

	var pino model.Log
	ApplyLine(&pino, `{"level":50,"msg":"x"}`, now)
	if pino.Status != "error" {
		t.Fatalf("niveau pino 50 : %q", pino.Status)
	}
}

func TestEventXML(t *testing.T) {
	x := `<Event xmlns='http://schemas.microsoft.com/win/2004/08/events/event'><System><Provider Name='Service Control Manager' Guid='{555908d1}' EventSourceName='Service Control Manager'/><EventID Qualifiers='16384'>7036</EventID><Version>0</Version><Level>2</Level><Task>0</Task><Opcode>0</Opcode><Keywords>0x8080000000000000</Keywords><TimeCreated SystemTime='2026-11-18T01:02:03.4567890Z'/><EventRecordID>12345</EventRecordID><Correlation/><Execution ProcessID='812' ThreadID='9000'/><Channel>System</Channel><Computer>SRV-01</Computer><Security/></System><EventData><Data Name='param1'>Spouleur</Data><Data Name='param2'>arrêté</Data></EventData><RenderingInfo Culture='fr-FR'><Message>Le service Spouleur est entré dans l'état : arrêté.</Message></RenderingInfo></Event>`
	l, id, err := parseEventXML(x, time.Now())
	if err != nil || id != 12345 {
		t.Fatalf("id %d err %v", id, err)
	}
	if l.Status != "error" || l.Service != "Service Control Manager" || l.Message == "" {
		t.Fatalf("événement : %+v", l)
	}
	if l.TS != time.Date(2026, 11, 18, 1, 2, 3, 456000000, time.UTC).UnixMilli() {
		t.Fatalf("horodatage : %d", l.TS)
	}
	evs := splitEvents("<Events>" + x + x + "</Events>")
	if len(evs) != 2 {
		t.Fatalf("découpage wevtutil : %d", len(evs))
	}
	if p := providerName(x); p != "Service Control Manager" {
		t.Fatalf("fournisseur : %q", p)
	}
}
