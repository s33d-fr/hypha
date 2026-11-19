// Package status tient l'état des collecteurs et les dernières erreurs, envoyés avec
// l'inventaire (host.status, contrat § 3.1) et affichés par `hypha-agent status`.
package status

import (
	"fmt"
	"sync"
	"time"

	"s33d.fr/hypha/agent/internal/model"
)

const maxErrors = 10

// Registry est partagé par tous les collecteurs.
type Registry struct {
	mu         sync.Mutex
	collectors map[string]string
	errors     []string
	lastErr    map[string]string
}

// New crée un registre vide.
func New() *Registry {
	return &Registry{collectors: map[string]string{}, lastErr: map[string]string{}}
}

// Set fixe l'état d'un collecteur : "ok", "disabled", "absent", ou une phrase d'erreur.
func (r *Registry) Set(collector, state string) {
	r.mu.Lock()
	r.collectors[collector] = state
	r.mu.Unlock()
}

// Error note une erreur ; la même erreur répétée par le même collecteur n'est gardée
// qu'une fois (sinon elle chasserait les autres de la liste).
func (r *Registry) Error(collector string, err error) {
	if err == nil {
		return
	}
	msg := err.Error()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.lastErr[collector] == msg {
		return
	}
	r.lastErr[collector] = msg
	line := fmt.Sprintf("%s %s : %s", time.Now().UTC().Format(time.RFC3339), collector, msg)
	r.errors = append(r.errors, line)
	if len(r.errors) > maxErrors {
		r.errors = r.errors[len(r.errors)-maxErrors:]
	}
}

// Clear oublie la dernière erreur d'un collecteur rétabli (une rechute sera de nouveau notée).
func (r *Registry) Clear(collector string) {
	r.mu.Lock()
	delete(r.lastErr, collector)
	r.mu.Unlock()
}

// Snapshot rend une copie pour l'envoi.
func (r *Registry) Snapshot() model.HostStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := make(map[string]string, len(r.collectors))
	for k, v := range r.collectors {
		c[k] = v
	}
	e := make([]string, len(r.errors))
	copy(e, r.errors)
	return model.HostStatus{Collectors: c, Errors: e}
}
