//go:build linux

package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Unité systemd de l'agent et sa commande de pose (contrat § 27 ; packaging/hypha-agent.service).
const (
	unitName  = "hypha-agent.service"
	applyHook = "apply-update"
)

// install pose le nouveau binaire. Si le dossier du binaire est inscriptible : copie à côté
// puis rename atomique. Sinon (unité durcie, ProtectSystem=full rend /usr en lecture seule
// pour l'agent), le binaire vérifié est préparé sous le répertoire d'état avec SHA256SUMS
// et sa signature ; l'unité le pose à la relance par « ExecStartPre=-+… apply-update »,
// qui revérifie la signature avec la clé embarquée avant le rename.
func install(p installPlan) error {
	dir := filepath.Dir(p.exe)
	tmp := filepath.Join(dir, ".hypha-agent.new")
	err := copyFile(p.newPath, tmp, 0o755)
	if err == nil {
		if err := os.Rename(tmp, p.exe); err != nil {
			os.Remove(tmp)
			return fmt.Errorf("remplacement de %s : %w", p.exe, err)
		}
		syncDir(dir)
		return nil
	}
	if !errors.Is(err, syscall.EROFS) && !errors.Is(err, os.ErrPermission) {
		return fmt.Errorf("écriture de %s : %w", tmp, err)
	}
	if os.Getenv("INVOCATION_ID") == "" || !hasApplyHook() {
		return fmt.Errorf("%s en lecture seule pour l'agent et unité sans « ExecStartPre=-+%s %s » : poser l'unité de la 0.4.0 (paquet ou install.sh)", dir, p.exe, applyHook)
	}
	return stage(p)
}

// hasApplyHook dit si l'unité en place pose les binaires préparés.
func hasApplyHook() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", "show", "--property=ExecStartPre", "--value", unitName).Output()
	return err == nil && strings.Contains(string(out), applyHook)
}

// Clé et exécutable de la pose préparée (remplacés dans les tests).
var (
	stagedPublicKey  = EmbeddedPublicKey
	stagedExecutable = os.Executable
)

type stagedInfo struct {
	Version string `json:"version"`
	Asset   string `json:"asset"`
}

func stageDir(workDir string) string { return filepath.Join(workDir, "staged") }

func stage(p installPlan) error {
	d := stageDir(p.workDir)
	if err := os.MkdirAll(d, 0o700); err != nil {
		return err
	}
	if err := os.Rename(p.newPath, filepath.Join(d, p.asset)); err != nil {
		return err
	}
	info, _ := json.Marshal(stagedInfo{Version: p.version, Asset: p.asset})
	for name, data := range map[string][]byte{"SHA256SUMS": p.sums, "SHA256SUMS.sig": p.sig, "staged.json": info} {
		if err := os.WriteFile(filepath.Join(d, name), data, 0o600); err != nil {
			return err
		}
	}
	p.log.Info("binaire vérifié préparé : posé par l'unité à la relance", "dossier", d)
	return nil
}

// ApplyStaged pose le binaire préparé (commande « hypha-agent apply-update », lancée par
// l'unité avant l'agent, avec tous les droits). Sans préparation, ne fait rien. Toute
// anomalie est notée dans apply-error.txt (relu par l'agent, qui la rend dans
// update_status) et l'ancien binaire reste en place.
func ApplyStaged(stateDir string) error {
	workDir := filepath.Join(stateDir, DirName)
	d := stageDir(workDir)
	if _, err := os.Stat(d); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	err := applyStaged(d)
	_ = os.RemoveAll(d)
	if err != nil {
		_ = os.WriteFile(filepath.Join(workDir, "apply-error.txt"), []byte(err.Error()+"\n"), 0o600)
	}
	return err
}

func applyStaged(d string) error {
	var info stagedInfo
	raw, err := os.ReadFile(filepath.Join(d, "staged.json"))
	if err != nil {
		return fmt.Errorf("préparation illisible : %w", err)
	}
	if err := json.Unmarshal(raw, &info); err != nil || info.Asset == "" || strings.ContainsAny(info.Asset, `/\`) {
		return errors.New("préparation illisible : staged.json")
	}
	sums, err := os.ReadFile(filepath.Join(d, "SHA256SUMS"))
	if err != nil {
		return err
	}
	sig, err := os.ReadFile(filepath.Join(d, "SHA256SUMS.sig"))
	if err != nil {
		return errors.New("signature absente de la préparation")
	}
	pub, err := stagedPublicKey()
	if err != nil {
		return err
	}
	if err := VerifySums(pub, sums, sig); err != nil {
		return err
	}
	table, err := ParseSums(sums)
	if err != nil {
		return err
	}
	bin := filepath.Join(d, info.Asset)
	got, err := fileSHA256(bin)
	if err != nil {
		return err
	}
	if want := table[info.Asset]; want == "" || want != got {
		return fmt.Errorf("somme SHA-256 du binaire préparé fausse")
	}
	exe, err := stagedExecutable()
	if err != nil {
		return err
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	dir := filepath.Dir(exe)
	tmp := filepath.Join(dir, ".hypha-agent.new")
	if err := copyFile(bin, tmp, 0o755); err != nil {
		return fmt.Errorf("écriture de %s : %w", tmp, err)
	}
	if err := os.Rename(tmp, exe); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("remplacement de %s : %w", exe, err)
	}
	syncDir(dir)
	return nil
}

func syncDir(dir string) {
	if f, err := os.Open(dir); err == nil {
		_ = f.Sync()
		f.Close()
	}
}

func cleanupOld() {}

// Relaunch : appelé quand l'agent s'est arrêté proprement après avoir posé un nouveau
// binaire. Sous systemd, sortir suffit (Restart=always relance, ExecStartPre pose un binaire
// préparé) ; au premier plan, le processus se remplace par le nouveau binaire.
func Relaunch() int {
	if os.Getenv("INVOCATION_ID") != "" {
		return 0
	}
	exe, err := os.Executable()
	if err != nil {
		return 1
	}
	if err := syscall.Exec(exe, os.Args, os.Environ()); err != nil {
		fmt.Fprintf(os.Stderr, "relance de %s impossible : %v\n", exe, err)
		return 1
	}
	return 0
}
