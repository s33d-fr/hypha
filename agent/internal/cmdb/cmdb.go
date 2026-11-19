// Package cmdb fait l'inventaire matériel et logiciel de l'hôte (docs/CONTRAT-04.md § 15) :
// au démarrage puis toutes les 6 h, envoyé vers /api/v1/intake/inventory seulement si son
// empreinte a changé, et au moins une fois par jour.
package cmdb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"sort"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
	"s33d.fr/hypha/agent/internal/state"
	"s33d.fr/hypha/agent/internal/updates"
)

const (
	stateFile  = "inventory.json"
	resendDays = 24 * time.Hour
)

// Collector relève et envoie l'inventaire.
type Collector struct {
	cfg      config.InventoryConfig
	store    *state.Store
	updates  *updates.Checker
	post     func(fields map[string]any)
	log      *slog.Logger
	setState func(string)
}

type sent struct {
	Fingerprint string `json:"fingerprint"`
	SentAt      int64  `json:"sent_at"`
}

// New prépare le collecteur ; post reçoit le corps (hors objet agent).
func New(cfg config.InventoryConfig, store *state.Store, up *updates.Checker, post func(map[string]any), setState func(string), log *slog.Logger) *Collector {
	return &Collector{cfg: cfg, store: store, updates: up, post: post, setState: setState, log: log}
}

// Run : premier relevé 30 s après le démarrage (l'agent a d'abord des choses plus urgentes),
// puis à l'intervalle de la conf.
func (c *Collector) Run(ctx context.Context) {
	t := time.NewTimer(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.Once(ctx, false)
			t.Reset(c.cfg.Interval.D())
		}
	}
}

// Once relève l'inventaire et l'envoie s'il a changé (ou force).
func (c *Collector) Once(ctx context.Context, force bool) {
	defer func() {
		if p := recover(); p != nil {
			c.log.Error("inventaire en panique, relevé abandonné", "err", p)
			c.setState("erreur : panique de l'inventaire")
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	inv, errs := Collect(ctx, c.cfg.Packages)
	up := c.updates.Get(ctx, time.Hour)
	inv.Updates = up.Updates()
	inv.OS.RebootRequired = up.RebootRequired
	for _, e := range errs {
		c.log.Debug("inventaire partiel", "err", e)
	}
	fp := Fingerprint(inv)
	var prev sent
	_ = c.store.ReadJSON(stateFile, &prev)
	if !force && prev.Fingerprint == fp && time.Since(time.UnixMilli(prev.SentAt)) < resendDays {
		c.setState("ok")
		return
	}
	now := time.Now()
	c.post(map[string]any{"ts": now.UnixMilli(), "hardware": inv.Hardware, "os": inv.OS,
		"updates": inv.Updates, "packages": inv.Packages})
	if err := c.store.WriteJSON(stateFile, sent{Fingerprint: fp, SentAt: now.UnixMilli()}); err != nil {
		c.log.Warn("empreinte de l'inventaire non écrite", "err", err)
	}
	if len(errs) > 0 {
		c.setState("partiel : " + errs[0].Error())
	} else {
		c.setState("ok")
	}
}

// Fingerprint : empreinte de l'inventaire (paquets triés), indépendante de l'heure du relevé.
func Fingerprint(inv model.Inventory) string {
	sort.Slice(inv.Packages, func(i, j int) bool {
		a, b := inv.Packages[i], inv.Packages[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Arch < b.Arch
	})
	raw, _ := json.Marshal(inv)
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}
