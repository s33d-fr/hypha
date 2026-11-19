package logs

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
	"s33d.fr/hypha/agent/internal/state"
)

// Les tests appellent glob/poll directement et lisent la file d'entrée de la Pipeline :
// pas d'attente des cadences réelles (1 s, 5 s).

func testFiles(t *testing.T, dir string) *Files {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	pipe := NewPipeline(nil, func(...string) map[string]string { return map[string]string{} }, nil, log)
	store := &state.Store{Dir: filepath.Join(dir, "state")}
	return NewFiles([]config.FileSource{{Path: filepath.Join(dir, "*.log"), Service: "svc"}}, pipe, store, log, func(string) {})
}

// drain rend les messages en attente et applique leurs positions (comme un envoi réussi).
func drain(f *Files) []string {
	var out []string
	for {
		select {
		case e := <-f.pipe.in:
			if !e.Skip {
				out = append(out, e.Log.Message)
			}
			if e.Commit != nil {
				e.Commit()
			}
		default:
			return out
		}
	}
}

func appendFile(t *testing.T, path, s string) {
	t.Helper()
	fh, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	fh.WriteString(s)
	fh.Close()
}

func pollAll(f *Files, now time.Time) {
	for path, tf := range f.files {
		if !f.poll(context.Background(), tf, now) {
			tf.f.Close()
			delete(f.files, path)
		}
	}
}

func expect(t *testing.T, step string, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s : %q, attendu %q", step, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s : %q, attendu %q", step, got, want)
		}
	}
}

func TestFilesRotationTruncationResume(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	appendFile(t, path, "ancien1\nancien2\n")

	f := testFiles(t, dir)
	f.glob(true) // démarrage : fichier inconnu pris à la fin
	now := time.Now()
	pollAll(f, now)
	expect(t, "démarrage", drain(f))

	appendFile(t, path, "neuf1\r\nneuf2\n")
	pollAll(f, now)
	expect(t, "ajout", drain(f), "neuf1", "neuf2")

	// Rotation par renommage : la fin de l'ancien fichier, puis le nouveau depuis le début.
	appendFile(t, path, "avant-rotation\n")
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	appendFile(t, path, "apres1\n")
	pollAll(f, now)
	expect(t, "rotation", drain(f), "avant-rotation", "apres1")

	// Troncature sur place (copytruncate).
	if err := os.Truncate(path, 0); err != nil {
		t.Fatal(err)
	}
	appendFile(t, path, "t1\n")
	pollAll(f, now)
	expect(t, "troncature", drain(f), "t1")

	// Ligne sans fin de ligne : retenue, puis envoyée après le délai.
	appendFile(t, path, "partielle")
	pollAll(f, now)
	expect(t, "partielle retenue", drain(f))
	pollAll(f, now.Add(partialFlushAfter+time.Second))
	expect(t, "partielle envoyée", drain(f), "partielle")

	// Redémarrage de l'agent : reprise exacte après la dernière position en sécurité.
	f.saver.Flush()
	for _, tf := range f.files {
		tf.f.Close()
	}
	appendFile(t, path, "pendant-arret\n")
	f2 := testFiles(t, dir)
	if err := f2.store.ReadJSON(filesStateName, &f2.registry); err != nil {
		t.Fatal(err)
	}
	f2.glob(true)
	pollAll(f2, now)
	expect(t, "reprise", drain(f2), "pendant-arret")
	for _, tf := range f2.files {
		tf.f.Close()
	}

	// Fichier apparu après le démarrage : lu depuis le début.
	other := filepath.Join(dir, "autre.log")
	appendFile(t, other, "o1\no2\n")
	f2.glob(false)
	pollAll(f2, now)
	expect(t, "nouveau fichier", drain(f2), "o1", "o2")
	for _, tf := range f2.files {
		tf.f.Close()
	}
}

func TestPipelineBatchAndExclude(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	type sent struct {
		n    int
		done func()
	}
	got := make(chan sent, 10)
	p := NewPipeline([]string{"^EXCLURE"}, func(...string) map[string]string { return map[string]string{"host": "h"} },
		func(l []model.Log, done func()) { got <- sent{len(l), done} }, log)
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() { p.Run(ctx); close(finished) }()
	commits := 0
	for i := 0; i < batchLines+3; i++ {
		msg := "ligne"
		if i == 1 {
			msg = "EXCLURE ceci"
		}
		p.Submit(ctx, Entry{Log: model.Log{Message: msg}, Commit: func() { commits++ }})
	}
	first := <-got
	if first.n != batchLines {
		t.Fatalf("premier lot : %d lignes, attendu %d", first.n, batchLines)
	}
	first.done()
	cancel()
	<-finished
	last := <-got
	if last.n != 2 { // 503 lignes moins 1 exclue moins 500
		t.Fatalf("dernier lot : %d lignes, attendu 2", last.n)
	}
	last.done()
	if commits != batchLines+3 {
		t.Fatalf("positions avancées : %d, attendu %d (exclue comprise)", commits, batchLines+3)
	}
}
