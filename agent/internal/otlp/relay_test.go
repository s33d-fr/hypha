package otlp

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Le relais transmet le corps et ses en-têtes tels quels, ajoute la clé, et rend la
// réponse du serveur à l'application.
func TestRelayAddsKey(t *testing.T) {
	var gotKey, gotType, gotEnc string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey, gotType, gotEnc = r.Header.Get("X-Hypha-Api-Key"), r.Header.Get("Content-Type"), r.Header.Get("Content-Encoding")
		gotBody, _ = io.ReadAll(r.Body)
		if r.URL.Path != "/v1/traces" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := New(addr, srv.URL, "hph_k", "t", http.DefaultTransport, false, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, "http://"+addr+"/v1/traces", bytes.NewReader([]byte{0x0a, 0x01, 0x02}))
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("Content-Encoding", "gzip")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("statut rendu : %d", resp.StatusCode)
	}
	if gotKey != "hph_k" || gotType != "application/x-protobuf" || gotEnc != "gzip" || !bytes.Equal(gotBody, []byte{0x0a, 0x01, 0x02}) {
		t.Fatalf("relayé : clé %q type %q enc %q corps %v", gotKey, gotType, gotEnc, gotBody)
	}
	resp, err = http.Get("http://" + addr + "/v1/traces")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET doit être refusé : %d", resp.StatusCode)
	}
}
