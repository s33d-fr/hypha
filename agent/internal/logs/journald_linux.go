//go:build linux

package logs

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"s33d.fr/hypha/agent/internal/model"
	"s33d.fr/hypha/agent/internal/state"
)

const journaldStateName = "journald_cursor.json"

// Journald lit le journal systemd par `journalctl -o json -f` : un sous-processus plutôt
// que la bibliothèque libsystemd, pour garder un binaire statique sans cgo.
type Journald struct {
	pipe    *Pipeline
	store   *state.Store
	saver   *state.Saver
	log     *slog.Logger
	setStat func(string)

	mu      sync.Mutex
	durable string // curseur de la dernière entrée en sécurité (écrit sur disque)
	seen    string // curseur de la dernière entrée lue (reprise si journalctl s'arrête)
}

type journaldState struct {
	Cursor string `json:"cursor"`
}

// JournaldAvailable dit si journalctl est présent.
func JournaldAvailable() bool {
	_, err := exec.LookPath("journalctl")
	return err == nil
}

// NewJournald prépare la lecture.
func NewJournald(pipe *Pipeline, store *state.Store, log *slog.Logger, setStat func(string)) *Journald {
	j := &Journald{pipe: pipe, store: store, log: log, setStat: setStat}
	j.saver = state.NewSaver(store, journaldStateName, func() any {
		j.mu.Lock()
		defer j.mu.Unlock()
		return journaldState{Cursor: j.durable}
	}, func(err error) { log.Warn("curseur journald non écrit", "err", err) })
	return j
}

// Run suit le journal jusqu'à l'arrêt ; journalctl est relancé s'il s'arrête.
func (j *Journald) Run(ctx context.Context) {
	var st journaldState
	if err := j.store.ReadJSON(journaldStateName, &st); err != nil && !errors.Is(err, os.ErrNotExist) {
		j.log.Warn("curseur journald illisible : reprise à la fin du journal", "err", err)
	}
	j.durable, j.seen = st.Cursor, st.Cursor
	go j.saver.Run(ctx)
	backoff := time.Second
	for ctx.Err() == nil {
		j.mu.Lock()
		cursor := j.seen
		j.mu.Unlock()
		// Sans curseur (premier démarrage) : à partir de maintenant, sans l'historique.
		args := []string{"--output=json", "--all", "--no-pager", "--follow"}
		if cursor != "" {
			args = append(args, "--after-cursor="+cursor)
		} else {
			args = append(args, "--lines=0")
		}
		start := time.Now()
		err := j.stream(ctx, args)
		if ctx.Err() != nil {
			return
		}
		j.setStat(fmt.Sprintf("erreur : journalctl arrêté (%v), relance", err))
		j.log.Warn("journalctl s'est arrêté, relance", "err", err, "attente", backoff)
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(2*backoff, time.Minute)
	}
}

// Once lit les 20 dernières entrées (aperçu de --once).
func (j *Journald) Once(ctx context.Context) error {
	return j.stream(ctx, []string{"--output=json", "--all", "--no-pager", "--lines=20"})
}

func (j *Journald) stream(ctx context.Context, args []string) error {
	cmd := exec.CommandContext(ctx, "journalctl", args...)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &limitedWriter{w: &stderr, n: 4096}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	j.setStat("ok")
	r := bufio.NewReaderSize(out, 256<<10)
	for {
		line, err := readLine(r, 4<<20)
		if len(line) > 0 {
			if e, ok := j.entry(line); ok {
				if !j.pipe.Submit(ctx, e) {
					break
				}
			}
		}
		if err != nil {
			break
		}
	}
	werr := cmd.Wait()
	if msg := strings.TrimSpace(stderr.String()); msg != "" {
		return fmt.Errorf("%v : %s", werr, msg)
	}
	return werr
}

// entry convertit une entrée JSON de journalctl en ligne de journal.
func (j *Journald) entry(line []byte) (Entry, bool) {
	var raw map[string]any
	if err := json.Unmarshal(line, &raw); err != nil {
		return Entry{}, false
	}
	f := func(k string) string { return jfield(raw[k]) }
	cursor := f("__CURSOR")
	if cursor == "" {
		return Entry{}, false
	}
	j.mu.Lock()
	j.seen = cursor
	j.mu.Unlock()

	l := model.Log{Source: "journald", Attributes: map[string]any{}}
	if us, err := strconv.ParseInt(f("__REALTIME_TIMESTAMP"), 10, 64); err == nil {
		l.TS = us / 1000
	} else {
		l.TS = time.Now().UnixMilli()
	}
	prio := ""
	if p, err := strconv.Atoi(f("PRIORITY")); err == nil {
		prio = SyslogStatus(p)
	}
	// Service : le nom du conteneur pour les journaux Docker, sinon l'identifiant syslog
	// (nginx, sshd, kernel…), sinon la commande, sinon l'unité.
	unit := f("_SYSTEMD_UNIT")
	for _, k := range []string{"CONTAINER_NAME", "SYSLOG_IDENTIFIER", "_COMM"} {
		if v := f(k); v != "" {
			l.Service = v
			break
		}
	}
	if l.Service == "" {
		l.Service = strings.TrimSuffix(unit, ".service")
	}
	jd := map[string]any{}
	for attr, k := range map[string]string{
		"unit": "_SYSTEMD_UNIT", "user_unit": "_SYSTEMD_USER_UNIT", "pid": "_PID", "comm": "_COMM",
		"transport": "_TRANSPORT", "syslog_identifier": "SYSLOG_IDENTIFIER", "container_id": "CONTAINER_ID",
	} {
		if v := f(k); v != "" {
			jd[attr] = v
		}
	}
	l.Attributes["journald"] = jd

	ApplyLine(&l, f("MESSAGE"), time.Now())
	// La priorité journald fait foi, sauf « info » : c'est la valeur par défaut de tout ce
	// qu'un service écrit sur sa sortie standard, et le texte (ERROR…, niveau JSON) en dit
	// alors plus.
	if prio != "" && (prio != "info" || l.Status == "") {
		l.Status = prio
	}
	return Entry{Log: l, Commit: func() {
		j.mu.Lock()
		j.durable = cursor
		j.mu.Unlock()
		j.saver.MarkDirty()
	}}, true
}

// jfield lit un champ journald : texte, tableau d'octets (champ non UTF-8 ou binaire),
// ou tableau de valeurs (champ répété : on garde la première).
func jfield(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		if len(t) == 0 {
			return ""
		}
		if _, isNum := t[0].(float64); isNum {
			b := make([]byte, 0, len(t))
			for _, x := range t {
				n, _ := x.(float64)
				b = append(b, byte(n))
			}
			return string(b)
		}
		return jfield(t[0])
	case nil:
		return ""
	}
	return fmt.Sprint(v)
}

// readLine lit une ligne entière ; une ligne de plus de max octets est sautée (une entrée
// journald démesurée ne doit pas faire gonfler la mémoire de l'agent).
func readLine(r *bufio.Reader, max int) ([]byte, error) {
	var buf []byte
	tooLong := false
	for {
		chunk, err := r.ReadSlice('\n')
		if !tooLong {
			if len(buf)+len(chunk) > max {
				tooLong, buf = true, nil
			} else {
				buf = append(buf, chunk...)
			}
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if tooLong {
			return nil, err
		}
		return bytes.TrimRight(buf, "\n"), err
	}
}

// limitedWriter garde les premiers octets de la sortie d'erreur de journalctl.
type limitedWriter struct {
	w io.Writer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.n > 0 {
		k := min(len(p), l.n)
		l.w.Write(p[:k])
		l.n -= k
	}
	return len(p), nil
}

// FlushState écrit le curseur maintenant (arrêt : après la confirmation du dernier lot).
func (j *Journald) FlushState() { j.saver.Flush() }
