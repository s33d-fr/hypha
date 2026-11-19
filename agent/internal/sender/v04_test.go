package sender

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"s33d.fr/hypha/agent/internal/model"
)

// Un équipement SNMP envoie sous sa propre identité : l'objet agent posé n'est pas écrasé.
func TestPostKeepsDeviceIdentity(t *testing.T) {
	var out strings.Builder
	s, _ := New(Options{DryRun: true, Out: &out, Agent: model.AgentInfo{ID: "agent-1"}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	s.Post(RouteHost, map[string]any{"agent": model.AgentInfo{ID: "device-1", Via: "agent-1"}, "host": map[string]any{}}, nil)
	s.Post(RouteHost, map[string]any{"host": map[string]any{}}, nil)
	text := out.String()
	if !strings.Contains(text, `"id": "device-1"`) || !strings.Contains(text, `"via": "agent-1"`) || !strings.Contains(text, `"id": "agent-1"`) {
		t.Fatalf("identités : %s", text)
	}
}

// Do : requête directe avec les en-têtes d'identité (canal de contrôle, compte rendu).
func TestDoHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Hypha-Agent-Id") != "agent-1" || r.Header.Get("X-Hypha-Agent-Version") != "0.4.0" || r.Header.Get("X-Hypha-Api-Key") != "hph_k" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.Method == http.MethodPost {
			zr, err := gzip.NewReader(r.Body)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			var b map[string]any
			json.NewDecoder(zr).Decode(&b)
			if b["status"] != "succeeded" || b["agent"] == nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
		}
		w.Write([]byte(`{"server_time":1,"target_version":null,"actions":[],"synthetics":[]}`))
	}))
	defer srv.Close()
	s, err := New(Options{URL: srv.URL, APIKey: "hph_k", Agent: model.AgentInfo{ID: "agent-1", Version: "0.4.0"},
		BufferDir: t.TempDir(), BufferMaxBytes: 1 << 20, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	code, body, err := s.Do(context.Background(), http.MethodGet, RouteControl, nil)
	if err != nil || code != 200 || !strings.Contains(string(body), "server_time") {
		t.Fatalf("GET : %d %s %v", code, body, err)
	}
	code, _, err = s.Do(context.Background(), http.MethodPost, RouteActionResult(42), map[string]any{"status": "succeeded"})
	if err != nil || code != 200 {
		t.Fatalf("POST : %d %v", code, err)
	}
	if RouteActionResult(42) != "/api/v1/agent/actions/42/result" {
		t.Fatal(RouteActionResult(42))
	}
}
