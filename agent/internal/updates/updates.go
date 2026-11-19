// Package updates relève les mises à jour en attente et le redémarrage requis
// (docs/CONTRAT-04.md §§ 15 et 31), sans jamais rafraîchir les listes de paquets (ni
// « apt update », ni dépôt interrogé) : l'agent lit ce que le système sait déjà.
// La liste est relue au plus une fois par heure ; l'inventaire et les métriques
// system.updates.* la partagent.
package updates

import (
	"context"
	"sync"
	"time"

	"s33d.fr/hypha/agent/internal/model"
)

// Result : dernier relevé.
type Result struct {
	Known          bool // la liste a pu être lue
	Source         string
	Pending        int
	Security       int
	List           []model.PendingUpdate
	RebootRequired bool
	Err            error
	At             time.Time
}

// Checker garde le dernier relevé.
type Checker struct {
	mu   sync.Mutex
	last Result
}

// Get rend le relevé, refait s'il date de plus de maxAge. Le redémarrage requis est relu à
// chaque appel quand il ne coûte qu'un accès disque ou registre.
func (c *Checker) Get(ctx context.Context, maxAge time.Duration) Result {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last.At.IsZero() || time.Since(c.last.At) > maxAge {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		r := collect(ctx)
		cancel()
		r.At = time.Now()
		c.last = r
	}
	r := c.last
	if v, ok := quickReboot(); ok {
		r.RebootRequired = v
	}
	return r
}

// Updates rend la partie « updates » de l'inventaire (nil si la liste est illisible).
func (r Result) Updates() *model.Updates {
	if !r.Known {
		return nil
	}
	list := r.List
	if list == nil {
		list = []model.PendingUpdate{}
	}
	return &model.Updates{Pending: r.Pending, Security: r.Security, List: list}
}
