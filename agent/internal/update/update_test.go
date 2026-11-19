package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"s33d.fr/hypha/agent/internal/model"
)

func sign(t *testing.T, priv ed25519.PrivateKey, data []byte) []byte {
	t.Helper()
	return []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, data)))
}

func TestEmbeddedPublicKey(t *testing.T) {
	pub, err := EmbeddedPublicKey()
	if err != nil {
		t.Fatalf("clé embarquée : %v", err)
	}
	if len(pub) != ed25519.PublicKeySize {
		t.Fatalf("clé de %d octets", len(pub))
	}
}

func TestVerifySums(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	sums := []byte(strings.Repeat("a", 64) + "  hypha-agent-linux-amd64\n")
	sig := sign(t, priv, sums)

	if err := VerifySums(pub, sums, sig); err != nil {
		t.Fatalf("signature valide refusée : %v", err)
	}
	if err := VerifySums(pub, sums, append(sig, '\n')); err != nil {
		t.Fatalf("saut de ligne final refusé : %v", err)
	}
	altered := append([]byte{}, sums...)
	altered[0] = 'b'
	if err := VerifySums(pub, altered, sig); err == nil {
		t.Fatal("SHA256SUMS altéré accepté")
	}
	raw, _ := base64.StdEncoding.DecodeString(string(sig))
	raw[10] ^= 1
	if err := VerifySums(pub, sums, []byte(base64.StdEncoding.EncodeToString(raw))); err == nil {
		t.Fatal("signature altérée acceptée")
	}
	if err := VerifySums(pub, sums, nil); err == nil {
		t.Fatal("signature absente acceptée")
	}
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	if err := VerifySums(other, sums, sig); err == nil {
		t.Fatal("signature d'une autre clé acceptée")
	}
	if err := VerifySums(nil, sums, sig); !errors.Is(err, ErrNoPublicKey) {
		t.Fatalf("clé publique absente : %v", err)
	}
	if _, err := ParsePublicKey("  \n"); !errors.Is(err, ErrNoPublicKey) {
		t.Fatalf("clé vide : %v", err)
	}
	if _, err := ParsePublicKey(base64.StdEncoding.EncodeToString([]byte("court"))); err == nil {
		t.Fatal("clé de mauvaise taille acceptée")
	}
}

func TestParseSums(t *testing.T) {
	a, b := strings.Repeat("a", 64), strings.Repeat("B", 64)
	m, err := ParseSums([]byte(a + "  hypha-agent-linux-amd64\n" + b + " *hypha-agent-windows-amd64.exe\n\n"))
	if err != nil {
		t.Fatal(err)
	}
	if m["hypha-agent-linux-amd64"] != a || m["hypha-agent-windows-amd64.exe"] != strings.ToLower(b) {
		t.Fatalf("table : %v", m)
	}
	if _, err := ParseSums([]byte("zz  x\n")); err == nil {
		t.Fatal("somme invalide acceptée")
	}
	if n, _ := AssetName("windows", "amd64"); n != "hypha-agent-windows-amd64.exe" {
		t.Fatal(n)
	}
	if _, err := AssetName("darwin", "arm64"); err == nil {
		t.Fatal("plateforme sans binaire acceptée")
	}
	if v := ParseVersionOutput("hypha-agent 0.4.0 (linux/amd64, go1.26)\n"); v != "0.4.0" {
		t.Fatal(v)
	}
}

// release : faux serveur /install/ (le « binaire » est un script qui répond à version et
// check-config, comme le vrai).
type release struct {
	files map[string][]byte
}

func newRelease(t *testing.T, priv ed25519.PrivateKey, version string, tamper func(map[string][]byte)) *release {
	t.Helper()
	asset, err := AssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skip(err)
	}
	bin := []byte(fmt.Sprintf("#!/bin/sh\ncase \"$1\" in version) echo 'hypha-agent %s (%s/%s, test)';; check-config) exit 0;; esac\n", version, runtime.GOOS, runtime.GOARCH))
	sum := func(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
	v := []byte(version + "\n")
	sums := []byte(sum(bin) + "  " + asset + "\n" + sum(v) + "  version.txt\n")
	r := &release{files: map[string][]byte{asset: bin, "version.txt": v, "SHA256SUMS": sums, "SHA256SUMS.sig": sign(t, priv, sums)}}
	if tamper != nil {
		tamper(r.files)
	}
	return r
}

func (r *release) serve(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		data, ok := r.files[strings.TrimPrefix(req.URL.Path, "/install/")]
		if !ok {
			http.NotFound(w, req)
			return
		}
		w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newTestUpdater(t *testing.T, url string, pub ed25519.PublicKey, statuses *[]model.UpdateStatus) *Updater {
	dir := t.TempDir()
	return New(Options{
		BaseURL: url, Transport: http.DefaultTransport, StateDir: dir, Version: "0.3.9",
		ConfigPath: filepath.Join(dir, "hypha.yaml"), Enabled: true, PublicKey: pub,
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		OnStatus: func(s model.UpdateStatus) { *statuses = append(*statuses, s) },
	})
}

func TestPrepare(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("le faux binaire est un script shell")
	}
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	var installed []installPlan
	installFn = func(p installPlan) error { installed = append(installed, p); return nil }
	t.Cleanup(func() { installFn = install })

	cases := []struct {
		name    string
		tamper  func(map[string][]byte)
		pub     ed25519.PublicKey
		version string
		wantErr string
	}{
		{name: "valide", version: "0.4.0"},
		{name: "SHA256SUMS altéré", version: "0.4.0", wantErr: "signature",
			tamper: func(f map[string][]byte) { f["SHA256SUMS"] = append(f["SHA256SUMS"], '\n') }},
		{name: "signature absente", version: "0.4.0", wantErr: "signature",
			tamper: func(f map[string][]byte) { delete(f, "SHA256SUMS.sig") }},
		{name: "binaire altéré", version: "0.4.0", wantErr: "somme SHA-256",
			tamper: func(f map[string][]byte) {
				for k := range f {
					if strings.HasPrefix(k, "hypha-agent-") {
						f[k] = append(f[k], '#')
					}
				}
			}},
		{name: "autre version publiée", version: "0.4.1", wantErr: "version publiée"},
		{name: "clé de production", version: "0.4.0", pub: nil, wantErr: "signature"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			installed = nil
			r := newRelease(t, priv, c.version, c.tamper)
			srv := r.serve(t)
			key := pub
			if c.name == "clé de production" {
				key = c.pub
			}
			var statuses []model.UpdateStatus
			u := newTestUpdater(t, srv.URL, key, &statuses)
			err := u.Prepare(context.Background(), "0.4.0")
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("refus inattendu : %v", err)
				}
				if len(installed) != 1 || installed[0].version != "0.4.0" {
					t.Fatalf("pose : %+v", installed)
				}
				if last := statuses[len(statuses)-1]; last.State != StateInstalling {
					t.Fatalf("dernier état %q", last.State)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("erreur attendue %q, obtenue %v", c.wantErr, err)
			}
			if len(installed) != 0 {
				t.Fatal("binaire posé malgré le refus")
			}
			if last := statuses[len(statuses)-1]; last.State != StateFailed || last.Error == "" {
				t.Fatalf("état final %+v", last)
			}
		})
	}
}

func TestStartupAfterInstall(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	write := func(p persisted) {
		u := &Updater{o: Options{StateDir: dir}, st: p}
		u.save()
	}
	write(persisted{Target: "0.4.0", From: "0.3.9", Phase: "installing"})
	u := New(Options{StateDir: dir, Version: "0.4.0", Log: logger})
	u.Startup()
	if s := u.Status(); s.State != StateIdle || s.From != "0.3.9" || s.To != "0.4.0" {
		t.Fatalf("réussite non vue : %+v", s)
	}

	write(persisted{Target: "0.4.0", From: "0.3.9", Phase: "installing"})
	os.MkdirAll(filepath.Join(dir, DirName), 0o700)
	os.WriteFile(filepath.Join(dir, DirName, "apply-error.txt"), []byte("signature fausse\n"), 0o600)
	u = New(Options{StateDir: dir, Version: "0.3.9", Log: logger})
	u.Startup()
	if s := u.Status(); s.State != StateFailed || s.Error != "signature fausse" {
		t.Fatalf("échec non vu : %+v", s)
	}
	if _, err := os.Stat(filepath.Join(dir, DirName)); !os.IsNotExist(err) {
		t.Fatal("dossier de travail non effacé")
	}
}

func TestWantRespectsConf(t *testing.T) {
	var statuses []model.UpdateStatus
	u := New(Options{StateDir: t.TempDir(), Version: "0.3.9", Enabled: false,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), OnStatus: func(s model.UpdateStatus) { statuses = append(statuses, s) }})
	u.Want(context.Background(), "0.4.0", func() { t.Fatal("relance sans auto_update") })
	if len(statuses) != 1 || !strings.Contains(statuses[0].Error, "auto_update") {
		t.Fatalf("état : %+v", statuses)
	}
	u.Want(context.Background(), "0.3.9", func() { t.Fatal("relance vers la même version") })
}
