package sender

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"s33d.fr/hypha/agent/internal/model"
)

// Serveur en panne (503) puis rétabli : le corps passe par le tampon disque, puis part
// une fois le recul écoulé ; l'en-tête de clé, le gzip et l'objet agent sont vérifiés.
func TestRetryThroughBuffer(t *testing.T) {
	var calls atomic.Int32
	var mu sync.Mutex
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if r.Header.Get("X-Hypha-Api-Key") != "hph_test" || r.Header.Get("Content-Encoding") != "gzip" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var b map[string]any
		json.NewDecoder(zr).Decode(&b)
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		mu.Lock()
		bodies = append(bodies, b)
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"status":"ok","accepted":1,"dropped":0}`))
	}))
	defer srv.Close()

	s, err := New(Options{URL: srv.URL, APIKey: "hph_test", Agent: model.AgentInfo{ID: "id-1", Version: "t"},
		BufferDir: t.TempDir(), BufferMaxBytes: 1 << 20, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx, true)
	durable := make(chan struct{}, 1)
	s.Post(RouteMetrics, map[string]any{"series": []model.Series{}}, func() { durable <- struct{}{} })
	select {
	case <-durable: // rangé sur disque après le 503
	case <-time.After(5 * time.Second):
		t.Fatal("corps jamais mis en sécurité")
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if n, _ := s.buf.Stats(); n == 0 && calls.Load() >= 2 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if n, _ := s.buf.Stats(); n != 0 {
		t.Fatalf("tampon non vidé : %d corps", n)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 {
		t.Fatalf("corps reçus : %d", len(bodies))
	}
	agent, _ := bodies[0]["agent"].(map[string]any)
	if agent["id"] != "id-1" {
		t.Fatalf("objet agent absent ou faux : %v", bodies[0])
	}
}

// 401 : abandon immédiat, rien dans le tampon, le corps compte comme jeté.
func TestDropOnUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"unknown_key","detail":"clé inconnue"}`))
	}))
	defer srv.Close()
	s, err := New(Options{URL: srv.URL, APIKey: "hph_x", BufferDir: t.TempDir(), BufferMaxBytes: 1 << 20,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx, false)
	s.Post(RouteChecks, map[string]any{"checks": []model.Check{}}, nil)
	s.Close()
	st := s.TakeStats()
	if st.Dropped != 1 || st.BufferedCount != 0 {
		t.Fatalf("attendu 1 jeté et tampon vide : %+v", st)
	}
}

// Tampon borné : les plus anciens partent en premier.
func TestBufferEviction(t *testing.T) {
	b, err := OpenBuffer(t.TempDir(), 3000)
	if err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, 1000)
	evicted := 0
	for i := 0; i < 5; i++ {
		n, err := b.Put("/r", payload)
		if err != nil {
			t.Fatal(err)
		}
		evicted += n
	}
	count, size := b.Stats()
	if count != 2 || size > 3000 || evicted != 3 {
		t.Fatalf("après 5 corps de 1 Ko dans 3 Ko : %d corps, %d octets, %d jetés", count, size, evicted)
	}
	_, route, gz, ok := b.Oldest()
	if !ok || route != "/r" || len(gz) != 1000 {
		t.Fatalf("relecture : %v %q %d", ok, route, len(gz))
	}
}
