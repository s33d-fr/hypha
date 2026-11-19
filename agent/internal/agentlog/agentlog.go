// Package agentlog écrit le journal de l'agent lui-même : sur stderr sous Linux
// (journald le récupère via systemd), dans C:\ProgramData\Hypha\agent.log quand
// l'agent tourne en service Windows (pas de console).
package agentlog

import (
	"io"
	"log/slog"
	"os"
	"sync"
	"time"
)

// New crée le journal de l'agent.
func New(w io.Writer, debug bool) *slog.Logger {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level}))
}

// RotatingFile est un fichier journal à rotation simple : au-delà de max octets,
// le fichier devient <nom>.1 (l'ancien .1 est écrasé). Deux fichiers au plus.
type RotatingFile struct {
	mu   sync.Mutex
	path string
	max  int64
	f    *os.File
	size int64
}

// OpenRotating ouvre (ou crée) le fichier en ajout.
func OpenRotating(path string, max int64) (*RotatingFile, error) {
	r := &RotatingFile{path: path, max: max}
	if err := r.open(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *RotatingFile) open() error {
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	r.f, r.size = f, fi.Size()
	return nil
}

// Write ajoute une ligne, en tournant le fichier si besoin.
func (r *RotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.size+int64(len(p)) > r.max {
		r.f.Close()
		os.Remove(r.path + ".1")
		os.Rename(r.path, r.path+".1")
		if err := r.open(); err != nil {
			return 0, err
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

// Close ferme le fichier.
func (r *RotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.f.Close()
}

// Limiter laisse passer un message par clé et par période : une erreur qui se répète
// à chaque cycle ne doit pas remplir le journal (contrat § 3 : « une fois par minute »).
type Limiter struct {
	mu    sync.Mutex
	every time.Duration
	last  map[string]time.Time
}

// NewLimiter crée un limiteur.
func NewLimiter(every time.Duration) *Limiter {
	return &Limiter{every: every, last: map[string]time.Time{}}
}

// Allow dit si le message de cette clé peut être écrit maintenant.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if t, ok := l.last[key]; ok && now.Sub(t) < l.every {
		return false
	}
	l.last[key] = now
	return true
}
