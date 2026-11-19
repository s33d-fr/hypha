// Package otlp relaie l'OTLP/HTTP local (127.0.0.1:4318) vers le serveur HYPHA en
// ajoutant la clé d'ingestion (contrat § 3.9) : une application instrumentée avec
// OpenTelemetry vise l'agent de son hôte sans connaître la clé.
//
// Le relais est transparent : le corps (protobuf ou JSON, compressé ou non) passe tel quel,
// et la réponse du serveur revient telle quelle à l'application, dont l'exportateur OTLP
// gère lui-même les nouveaux essais.
package otlp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"s33d.fr/hypha/agent/internal/agentlog"
)

const maxBody = 10 << 20 // limite du serveur (10 Mo)

// Relay est le serveur HTTP local.
type Relay struct {
	listen  string
	target  string
	apiKey  string
	version string
	client  *http.Client
	dryRun  bool
	log     *slog.Logger
	limiter *agentlog.Limiter
}

// New prépare le relais ; transport est celui de l'expéditeur (mandataire, TLS).
func New(listen, target, apiKey, version string, transport http.RoundTripper, dryRun bool, log *slog.Logger) *Relay {
	return &Relay{listen: listen, target: target, apiKey: apiKey, version: version, dryRun: dryRun, log: log,
		client:  &http.Client{Transport: transport, Timeout: 30 * time.Second},
		limiter: agentlog.NewLimiter(time.Minute)}
}

// Start ouvre l'écoute ; le serveur s'arrête avec ctx.
func (r *Relay) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", r.listen)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	for _, p := range []string{"/v1/traces", "/v1/metrics", "/v1/logs"} {
		mux.HandleFunc("POST "+p, r.handle)
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 60 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	go srv.Serve(ln)
	return nil
}

func (r *Relay) handle(w http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(io.LimitReader(req.Body, maxBody+1))
	if err != nil {
		http.Error(w, "lecture du corps impossible", http.StatusBadRequest)
		return
	}
	if len(body) > maxBody {
		http.Error(w, "corps de plus de 10 Mo", http.StatusRequestEntityTooLarge)
		return
	}
	if r.dryRun {
		fmt.Printf("### OTLP %s (%d octets, %s) : non relayé (--dry-run)\n\n", req.URL.Path, len(body), req.Header.Get("Content-Type"))
		w.WriteHeader(http.StatusOK)
		return
	}
	out, err := http.NewRequestWithContext(req.Context(), http.MethodPost, r.target+req.URL.Path, bytes.NewReader(body))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, h := range []string{"Content-Type", "Content-Encoding"} {
		if v := req.Header.Get(h); v != "" {
			out.Header.Set(h, v)
		}
	}
	out.Header.Set("X-Hypha-Api-Key", r.apiKey)
	out.Header.Set("User-Agent", "hypha-agent/"+r.version+" (relais OTLP)")
	resp, err := r.client.Do(out)
	if err != nil {
		if r.limiter.Allow("otlp") {
			r.log.Warn("relais OTLP : serveur injoignable", "err", err)
		}
		// 503 : l'exportateur OTLP de l'application retentera plus tard.
		http.Error(w, "serveur HYPHA injoignable", http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()
	for _, h := range []string{"Content-Type", "Retry-After"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, io.LimitReader(resp.Body, 1<<20))
}
