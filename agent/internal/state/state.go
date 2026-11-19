// Package state garde les petits fichiers d'état de l'agent : identité, curseur
// journald, positions des fichiers suivis, signets des journaux d'événements Windows,
// date de démarrage de l'hôte, état pour `hypha-agent status`.
//
// Toute écriture passe par un fichier temporaire renommé : un arrêt brutal laisse
// l'ancienne version ou la nouvelle, jamais un fichier à moitié écrit.
package state

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Store est un répertoire d'état. En lecture seule (--dry-run), aucune écriture n'a lieu :
// un essai ne doit ni avancer les curseurs ni créer d'identité.
type Store struct {
	Dir      string
	ReadOnly bool
}

// Ensure crée le répertoire (droits 700 : les positions et curseurs disent ce qui tourne).
func (s *Store) Ensure() error {
	if s.ReadOnly {
		return nil
	}
	return os.MkdirAll(s.Dir, 0o700)
}

// Path rend le chemin d'un fichier d'état.
func (s *Store) Path(name string) string { return filepath.Join(s.Dir, name) }

// ReadJSON lit un fichier JSON ; os.ErrNotExist s'il n'existe pas encore.
func (s *Store) ReadJSON(name string, v any) error {
	data, err := os.ReadFile(s.Path(name))
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// WriteJSON écrit un fichier JSON de façon atomique.
func (s *Store) WriteJSON(name string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return s.WriteFile(name, data)
}

// ReadText lit un fichier texte d'une ligne.
func (s *Store) ReadText(name string) (string, error) {
	data, err := os.ReadFile(s.Path(name))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// WriteFile écrit des octets de façon atomique (temporaire, sync, renommage).
func (s *Store) WriteFile(name string, data []byte) error {
	if s.ReadOnly {
		return nil
	}
	if err := s.Ensure(); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.Dir, "."+name+".tmp*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	serr := tmp.Sync()
	cerr := tmp.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), s.Path(name)); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

// AgentID rend l'identifiant persistant de l'agent, créé au premier démarrage et jamais
// régénéré (contrat § 5). En lecture seule, un identifiant éphémère est rendu sans être
// écrit ; ephemeral le signale.
func (s *Store) AgentID() (id string, ephemeral bool, err error) {
	if v, err := s.ReadText("agent_id"); err == nil {
		if _, perr := uuid.Parse(v); perr == nil {
			return v, false, nil
		}
		// Fichier abîmé : on ne l'écrase pas en silence, l'hôte changerait d'identité côté serveur.
		return "", false, errors.New(s.Path("agent_id") + " ne contient pas un UUID valide : le corriger ou le supprimer")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", false, err
	}
	id = uuid.NewString()
	if s.ReadOnly {
		return id, true, nil
	}
	if err := s.WriteFile("agent_id", []byte(id+"\n")); err != nil {
		return "", false, err
	}
	return id, false, nil
}

// Saver regroupe les écritures d'un état qui change sans cesse (curseur journald,
// positions) : au plus une écriture toutes les 2 secondes, plus une à l'arrêt.
type Saver struct {
	store *Store
	name  string
	get   func() any
	mu    sync.Mutex
	dirty bool
	onErr func(error)
}

// NewSaver prépare l'écriture de name ; get rend la valeur à écrire (copie sous verrou).
func NewSaver(store *Store, name string, get func() any, onErr func(error)) *Saver {
	return &Saver{store: store, name: name, get: get, onErr: onErr}
}

// MarkDirty signale un changement à écrire.
func (s *Saver) MarkDirty() {
	s.mu.Lock()
	s.dirty = true
	s.mu.Unlock()
}

// Run écrit périodiquement jusqu'à l'arrêt, puis une dernière fois.
func (s *Saver) Run(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.Flush()
			return
		case <-t.C:
			s.Flush()
		}
	}
}

// Flush écrit maintenant si un changement attend.
func (s *Saver) Flush() {
	s.mu.Lock()
	dirty := s.dirty
	s.dirty = false
	s.mu.Unlock()
	if !dirty {
		return
	}
	if err := s.store.WriteJSON(s.name, s.get()); err != nil && s.onErr != nil {
		s.onErr(err)
	}
}
