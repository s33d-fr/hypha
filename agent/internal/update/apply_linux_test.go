//go:build linux

package update

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Pose préparée (unité durcie, ExecStartPre=-+… apply-update) : la signature est revérifiée
// avec la clé embarquée avant le remplacement ; une préparation altérée ne pose rien.
func TestApplyStaged(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	stagedPublicKey = func() (ed25519.PublicKey, error) { return pub, nil }
	t.Cleanup(func() { stagedPublicKey, stagedExecutable = EmbeddedPublicKey, os.Executable })

	for _, c := range []struct {
		name   string
		tamper func(dir string)
		ok     bool
	}{
		{"valide", nil, true},
		{"binaire remplacé après vérification", func(d string) { os.WriteFile(filepath.Join(d, "hypha-agent-linux-amd64"), []byte("malveillant"), 0o700) }, false},
		{"signature retirée", func(d string) { os.Remove(filepath.Join(d, "SHA256SUMS.sig")) }, false},
		{"SHA256SUMS réécrit", func(d string) {
			h := sha256.Sum256([]byte("malveillant"))
			os.WriteFile(filepath.Join(d, "SHA256SUMS"), []byte(hex.EncodeToString(h[:])+"  hypha-agent-linux-amd64\n"), 0o600)
		}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			exe := filepath.Join(root, "bin", "hypha-agent")
			os.MkdirAll(filepath.Dir(exe), 0o755)
			os.WriteFile(exe, []byte("ancien"), 0o755)
			stagedExecutable = func() (string, error) { return exe, nil }

			state := filepath.Join(root, "state")
			work := filepath.Join(state, DirName)
			os.MkdirAll(work, 0o700)
			newBin := []byte("nouveau binaire")
			h := sha256.Sum256(newBin)
			sums := []byte(hex.EncodeToString(h[:]) + "  hypha-agent-linux-amd64\n")
			np := filepath.Join(work, "hypha-agent-linux-amd64")
			os.WriteFile(np, newBin, 0o700)
			err := stage(installPlan{newPath: np, exe: exe, asset: "hypha-agent-linux-amd64", version: "0.4.0",
				sums: sums, sig: sign(t, priv, sums), workDir: work, log: slog.New(slog.NewTextHandler(io.Discard, nil))})
			if err != nil {
				t.Fatal(err)
			}
			if c.tamper != nil {
				c.tamper(stageDir(work))
			}
			err = ApplyStaged(state)
			got, _ := os.ReadFile(exe)
			if c.ok {
				if err != nil || string(got) != "nouveau binaire" {
					t.Fatalf("pose refusée : %v (%q)", err, got)
				}
			} else {
				if err == nil || string(got) != "ancien" {
					t.Fatalf("préparation altérée posée : %v (%q)", err, got)
				}
				msg, _ := os.ReadFile(filepath.Join(work, "apply-error.txt"))
				if !strings.Contains(string(msg), strings.TrimSpace(err.Error())) {
					t.Fatalf("motif non noté pour l'agent : %q", msg)
				}
			}
			if _, err := os.Stat(stageDir(work)); !os.IsNotExist(err) {
				t.Fatal("préparation non effacée")
			}
		})
	}
	if err := ApplyStaged(t.TempDir()); err != nil {
		t.Fatalf("sans préparation : %v", err)
	}
}
