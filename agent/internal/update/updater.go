package update

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"s33d.fr/hypha/agent/internal/model"
)

// États rendus dans host.status.update (§ 27).
const (
	StateIdle        = "idle"
	StateDownloading = "downloading"
	StateVerifying   = "verifying"
	StateInstalling  = "installing"
	StateFailed      = "failed"
)

// Bornes : un échec n'est retenté qu'après une heure, et trois fois au plus pour une même
// version cible (au-delà, il faut changer la cible ou relancer l'agent).
const (
	retryAfter      = time.Hour
	maxAttempts     = 3
	maxSumsBytes    = 1 << 20
	maxSigBytes     = 4 << 10
	maxBinaryBytes  = 256 << 20
	downloadTimeout = 10 * time.Minute
	checkTimeout    = 3 * time.Minute
	stateFile       = "update.json"
	// DirName : dossier de travail sous le répertoire d'état.
	DirName = "update"
)

// Options règle le metteur à jour.
type Options struct {
	BaseURL    string            // url de la conf ; les fichiers sont lus sous <url>/install/
	Transport  http.RoundTripper // mandataire et TLS de l'agent
	StateDir   string
	Version    string
	ConfigPath string
	Enabled    bool   // auto_update
	Disabled   string // motif qui interdit la mise à jour ici (conteneur, plateforme) ; vide sinon
	Log        *slog.Logger
	// OnStatus reçoit chaque changement d'état (envoyé aussitôt dans /intake/host).
	OnStatus func(model.UpdateStatus)
	// PublicKey remplace la clé embarquée (tests).
	PublicKey ed25519.PublicKey
}

// Updater fait une mise à jour à la fois.
type Updater struct {
	o      Options
	client *http.Client
	busy   atomic.Bool

	mu     sync.Mutex
	status model.UpdateStatus
	st     persisted
}

// persisted : suivi d'une mise à jour, gardé d'un démarrage à l'autre.
type persisted struct {
	Target      string `json:"target"`
	From        string `json:"from"`
	Phase       string `json:"phase"` // installing, failed, done
	Error       string `json:"error,omitempty"`
	Attempts    int    `json:"attempts"`
	LastAttempt int64  `json:"last_attempt"`
}

// New prépare le metteur à jour ; Startup doit être appelé avant tout.
func New(o Options) *Updater {
	u := &Updater{o: o, client: &http.Client{Transport: o.Transport, Timeout: downloadTimeout}}
	u.status = model.UpdateStatus{State: StateIdle, TS: time.Now().UnixMilli()}
	return u
}

// Status rend l'état courant.
func (u *Updater) Status() model.UpdateStatus {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.status
}

func (u *Updater) setStatus(s model.UpdateStatus) {
	s.TS = time.Now().UnixMilli()
	u.mu.Lock()
	same := u.status.State == s.State && u.status.To == s.To && u.status.Error == s.Error && u.status.From == s.From
	u.status = s
	u.mu.Unlock()
	if !same && u.o.OnStatus != nil {
		u.o.OnStatus(s)
	}
}

func (u *Updater) workDir() string { return filepath.Join(u.o.StateDir, DirName) }

// Startup relit le suivi : une mise à jour posée avant la relance est réussie si la version
// en cours est la cible, en échec sinon. Les restes de téléchargement sont effacés.
func (u *Updater) Startup() {
	cleanupOld()
	data, err := os.ReadFile(filepath.Join(u.o.StateDir, stateFile))
	if err == nil {
		_ = json.Unmarshal(data, &u.st)
	}
	applyErr := ""
	if b, err := os.ReadFile(filepath.Join(u.workDir(), "apply-error.txt")); err == nil {
		applyErr = strings.TrimSpace(string(b))
	}
	_ = os.RemoveAll(u.workDir())
	switch {
	case u.st.Phase == "installing" && u.st.Target == u.o.Version:
		u.st.Phase, u.st.Error, u.st.Attempts = "done", "", 0
		u.save()
		u.status = model.UpdateStatus{State: StateIdle, From: u.st.From, To: u.st.Target, TS: time.Now().UnixMilli()}
		u.o.Log.Info("mise à jour réussie", "de", u.st.From, "vers", u.st.Target)
	case u.st.Phase == "installing":
		msg := "binaire non remplacé après la relance"
		if applyErr != "" {
			msg = applyErr
		}
		u.st.Phase, u.st.Error = "failed", msg
		u.save()
		u.status = model.UpdateStatus{State: StateFailed, From: u.o.Version, To: u.st.Target, Error: msg, TS: time.Now().UnixMilli()}
		u.o.Log.Error("mise à jour non appliquée", "vers", u.st.Target, "err", msg)
	case u.st.Phase == "failed":
		u.status = model.UpdateStatus{State: StateFailed, From: u.o.Version, To: u.st.Target, Error: u.st.Error, TS: time.Now().UnixMilli()}
	}
}

func (u *Updater) save() {
	data, err := json.MarshalIndent(u.st, "", "  ")
	if err != nil {
		return
	}
	p := filepath.Join(u.o.StateDir, stateFile)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err == nil {
		_ = os.Rename(tmp, p)
	}
}

// Want reçoit la version cible du canal de contrôle (vide = aucune) ; lance la mise à jour en
// arrière-plan si la conf le permet. restart est appelé une fois le nouveau binaire posé.
func (u *Updater) Want(ctx context.Context, target string, restart func()) {
	target = strings.TrimSpace(target)
	if target == "" || target == u.o.Version {
		return
	}
	switch {
	case u.o.Disabled != "":
		u.setStatus(model.UpdateStatus{State: StateIdle, From: u.o.Version, To: target, Error: u.o.Disabled})
		return
	case !u.o.Enabled:
		u.setStatus(model.UpdateStatus{State: StateIdle, From: u.o.Version, To: target, Error: "mise à jour automatique coupée (auto_update: false)"})
		return
	}
	u.mu.Lock()
	st := u.st
	u.mu.Unlock()
	if st.Target == target && st.Phase == "failed" {
		if st.Attempts >= maxAttempts || time.Since(time.UnixMilli(st.LastAttempt)) < retryAfter {
			return
		}
	}
	if u.busy.Load() {
		return
	}
	go func() {
		defer func() {
			if p := recover(); p != nil {
				u.o.Log.Error("mise à jour en panique, abandonnée", "err", p)
				u.busy.Store(false)
			}
		}()
		if err := u.Prepare(ctx, target); err == nil {
			restart()
		}
	}()
}

// Prepare télécharge, vérifie et pose la version target ; rend nil quand il ne reste qu'à
// relancer l'agent. Sert aussi l'action update_agent (§ 28), validée par un humain, qui peut
// viser une version antérieure : c'est la seule rétrogradation possible, toujours explicite.
func (u *Updater) Prepare(ctx context.Context, target string) error {
	if u.o.Disabled != "" {
		return errors.New(u.o.Disabled)
	}
	if !u.busy.CompareAndSwap(false, true) {
		return errors.New("une mise à jour est déjà en cours")
	}
	defer u.busy.Store(false)
	u.mu.Lock()
	if u.st.Target != target {
		u.st = persisted{Target: target}
	}
	u.st.From = u.o.Version
	u.st.Attempts++
	u.st.LastAttempt = time.Now().UnixMilli()
	u.mu.Unlock()

	err := u.prepare(ctx, target)
	u.mu.Lock()
	if err != nil {
		u.st.Phase, u.st.Error = "failed", err.Error()
	} else {
		u.st.Phase, u.st.Error = "installing", ""
	}
	u.save()
	u.mu.Unlock()
	if err != nil {
		u.o.Log.Error("mise à jour refusée ou en échec", "vers", target, "err", err)
		u.setStatus(model.UpdateStatus{State: StateFailed, From: u.o.Version, To: target, Error: err.Error()})
		_ = os.RemoveAll(u.workDir())
		return err
	}
	u.o.Log.Info("nouvelle version posée, relance de l'agent", "de", u.o.Version, "vers", target)
	return nil
}

func (u *Updater) prepare(ctx context.Context, target string) error {
	from := u.o.Version
	status := func(state string) { u.setStatus(model.UpdateStatus{State: state, From: from, To: target}) }
	asset, err := AssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	pub := u.o.PublicKey
	if pub == nil {
		if pub, err = EmbeddedPublicKey(); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	status(StateDownloading)
	sums, err := u.fetch(ctx, "SHA256SUMS", maxSumsBytes)
	if err != nil {
		return err
	}
	sig, err := u.fetch(ctx, "SHA256SUMS.sig", maxSigBytes)
	if err != nil {
		return fmt.Errorf("signature absente : %w", err)
	}
	status(StateVerifying)
	if err := VerifySums(pub, sums, sig); err != nil {
		return err
	}
	table, err := ParseSums(sums)
	if err != nil {
		return err
	}
	want, ok := table[asset]
	if !ok {
		return fmt.Errorf("%s absent de SHA256SUMS", asset)
	}
	// version.txt, couvert par la signature quand build.sh l'inscrit dans SHA256SUMS : une
	// publication d'une autre version est refusée avant de télécharger le binaire.
	if vsum, ok := table["version.txt"]; ok {
		v, err := u.fetch(ctx, "version.txt", 1024)
		if err != nil {
			return err
		}
		if got := sha256Hex(v); got != vsum {
			return errors.New("somme de version.txt fausse")
		}
		if pv := strings.TrimSpace(string(v)); pv != target {
			return fmt.Errorf("version publiée %s, cible %s : rien n'est posé", pv, target)
		}
	}

	if err := os.MkdirAll(u.workDir(), 0o700); err != nil {
		return err
	}
	newPath := filepath.Join(u.workDir(), asset)
	status(StateDownloading)
	got, err := u.download(ctx, asset, newPath)
	if err != nil {
		return err
	}
	status(StateVerifying)
	if got != want {
		return fmt.Errorf("somme SHA-256 de %s fausse (attendue %s, obtenue %s)", asset, want, got)
	}
	if err := u.checkBinary(ctx, newPath, target); err != nil {
		return err
	}
	status(StateInstalling)
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return installFn(installPlan{
		newPath: newPath, exe: exe, asset: asset, version: target,
		sums: sums, sig: sig, workDir: u.workDir(), log: u.o.Log,
	})
}

// checkBinary lance le binaire vérifié : il doit se dire de la version cible et accepter la
// conf en place (check-config), sinon l'agent relancé tomberait en boucle. Un antivirus retient
// le premier lancement d'un exécutable inconnu le temps de l'analyser (Norton : une minute) ;
// sous Windows, un processus tué au délai rend « exit status 1 ».
func (u *Updater) checkBinary(ctx context.Context, path, target string) error {
	if err := os.Chmod(path, 0o755); err != nil {
		return err
	}
	vctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	out, err := exec.CommandContext(vctx, path, "version").Output()
	if err != nil {
		if vctx.Err() != nil {
			return fmt.Errorf("le binaire téléchargé n'a pas répondu en %s (antivirus ?)", checkTimeout)
		}
		return fmt.Errorf("le binaire téléchargé ne s'exécute pas : %w", err)
	}
	if v := ParseVersionOutput(string(out)); v != target {
		return fmt.Errorf("le binaire téléchargé est la version %q, cible %s", v, target)
	}
	cctx, cancel2 := context.WithTimeout(ctx, checkTimeout)
	defer cancel2()
	cmd := exec.CommandContext(cctx, path, "check-config", "--config", u.o.ConfigPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("la version %s refuse la conf en place : %s", target, lastLines(string(out), 3))
	}
	return nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	var keep []string
	for _, l := range lines {
		if strings.Contains(l, "erreur") || strings.Contains(l, "INVALIDE") {
			keep = append(keep, strings.TrimSpace(l))
		}
	}
	if len(keep) == 0 {
		keep = lines
	}
	if len(keep) > n {
		keep = keep[len(keep)-n:]
	}
	return strings.Join(keep, " ; ")
}

func (u *Updater) url(name string) string {
	return strings.TrimRight(u.o.BaseURL, "/") + "/install/" + name
}

func (u *Updater) get(ctx context.Context, name string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.url(name), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "hypha-agent/"+u.o.Version)
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("téléchargement de %s : %w", name, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("téléchargement de %s : HTTP %d", name, resp.StatusCode)
	}
	return resp, nil
}

func (u *Updater) fetch(ctx context.Context, name string, limit int64) ([]byte, error) {
	resp, err := u.get(ctx, name)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("téléchargement de %s : %w", name, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s dépasse %d octets", name, limit)
	}
	return data, nil
}

func (u *Updater) download(ctx context.Context, name, dst string) (string, error) {
	resp, err := u.get(ctx, name)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxBinaryBytes+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", fmt.Errorf("téléchargement de %s : %w", name, err)
	}
	if n > maxBinaryBytes {
		return "", fmt.Errorf("%s dépasse %d octets", name, int64(maxBinaryBytes))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// installFn pose le binaire ; remplacée dans les tests (jamais d'écriture sur le binaire de test).
var installFn = install

// installPlan : ce que la pose du binaire doit connaître.
type installPlan struct {
	newPath, exe, asset, version string
	sums, sig                    []byte
	workDir                      string
	log                          *slog.Logger
}

// copyFile copie src vers dst (créé avec mode), données écrites sur disque avant retour.
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, werr := io.Copy(out, in)
	serr := out.Sync()
	cerr := out.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		os.Remove(dst)
		return err
	}
	return os.Chmod(dst, mode)
}
