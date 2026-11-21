package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── Borg et restic : âge et état de la dernière sauvegarde de chaque dépôt ──────────────────
//
// L'outil installé est lancé sans shell, en lecture seule et SANS VERROU (borg --bypass-lock,
// restic --no-lock) : une sauvegarde, un élagage ou une vérification en cours ne sont jamais gênés
// ni retardés par la supervision. Borg : « borg list --json --consider-checkpoints » (manifeste du
// dépôt, sans le cache) ; restic : « restic snapshots --json --no-cache ». Phrase de passe : lue dans
// un fichier réservé à root (refusé s'il est ouvert au groupe ou à tous), passée à borg par son
// entrée standard (BORG_PASSPHRASE_FD=0) ; restic lit lui-même son --password-file. Rien ne passe
// en argument ni en variable d'environnement visible. Sous systemd, l'outil tourne dans sa propre
// unité bornée (mémoire, processeur, durée), comme pvesh : un gros dépôt ne pèse pas sur l'agent.

var checkpointRe = regexp.MustCompile(`\.checkpoint(\.\d+)?$`)

const (
	backupStatsEvery = 6 * time.Hour
	backupMaxHosts   = 100
)

// backupRunner lance l'outil : sortie standard, erreur standard, erreur.
type backupRunner func(ctx context.Context, timeout time.Duration, env []string, stdin []byte, path string, args ...string) ([]byte, []byte, error)

type BackupTool struct {
	tool    string // borg, restic
	cfg     config.BackupToolConfig
	run     backupRunner
	baseDir string
	wait    time.Duration // attente d'un relevé tout juste lancé, avant d'émettre l'état gardé

	mu      sync.Mutex
	running bool
	nextRun time.Time
	done    chan struct{}
	cancel  context.CancelFunc
	repos   map[string]*repoRead // dernier relevé de chaque dépôt
}

// repoRead : dernier relevé d'un dépôt ; les âges sont recalculés à chaque émission.
type repoRead struct {
	err     error
	borg    *borgList
	restic  []resticSnapshot
	stats   map[string]float64
	statsAt time.Time
}

func newBackupTool(tool string, cfg config.BackupToolConfig) *BackupTool {
	return &BackupTool{tool: tool, cfg: cfg, run: runBackupTool, baseDir: filepath.Join(config.StateDir(), "borg"),
		wait: 20 * time.Second, repos: map[string]*repoRead{}}
}

func (t *BackupTool) name() string { return t.tool }

// period : l'état gardé est republié chaque minute (âges recalculés, contrôles renouvelés) ; l'outil
// n'est relancé qu'à l'intervalle de la conf (15 min par défaut), en tâche de fond : un dépôt lent ou
// injoignable ne retient jamais la boucle des intégrations plus de 20 s.
func (t *BackupTool) period() time.Duration { return time.Minute }

func (t *BackupTool) close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cancel != nil {
		t.cancel()
	}
}

func (t *BackupTool) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	switch {
	case t.cfg.Enabled == "false":
		return nil, "disabled"
	case len(t.cfg.Repositories) == 0:
		return nil, "absent"
	}
	t.mu.Lock()
	if !t.running && !now.Before(t.nextRun) {
		rctx, cancel := context.WithCancel(context.Background())
		t.running, t.nextRun, t.cancel, t.done = true, now.Add(t.cfg.Interval.D()), cancel, make(chan struct{})
		go t.refresh(rctx, now, t.done)
	}
	running, done := t.running, t.done
	t.mu.Unlock()
	if running {
		select {
		case <-done:
		case <-time.After(t.wait):
		case <-ctx.Done():
		}
	}
	ts := model.Ms(now)
	t.mu.Lock()
	defer t.mu.Unlock()
	var checks []model.Check
	var problems []string
	pending := 0
	for _, r := range t.cfg.Repositories {
		kv := t.kvs(r)
		st := t.repos[r.Name]
		switch {
		case st == nil:
			pending++
			continue
		case st.err != nil:
			problems = append(problems, r.Name+" : "+st.err.Error())
			checks = append(checks, model.Check{TS: ts, Name: t.check("can_connect"), Status: model.CheckCrit,
				Message: model.Truncate("dépôt "+r.Name+" illisible : "+st.err.Error(), 500), Tags: tags(kv...)})
			continue
		case st.borg != nil:
			checks = append(checks, borgChecks(now, ts, b, tags, kv, r.Name, *st.borg)...)
		default:
			checks = append(checks, resticChecks(now, ts, b, tags, kv, r.Name, st.restic)...)
		}
		for name, v := range st.stats {
			unit := "byte"
			if strings.HasSuffix(name, "ratio") || strings.HasSuffix(name, "chunks") || strings.HasSuffix(name, "blobs") {
				unit = ""
			}
			b.Gauge(name, unit, v, kv...)
		}
	}
	n := len(t.cfg.Repositories)
	switch {
	case len(problems) == n:
		return checks, "erreur : " + model.Truncate(problems[0], 200)
	case len(problems) > 0:
		return checks, fmt.Sprintf("partiel : %d dépôt(s) sur %d en erreur (%s)", len(problems), n, model.Truncate(problems[0], 150))
	case pending == n:
		return checks, "partiel : premier relevé en cours"
	}
	return checks, "ok"
}

// refresh relit chaque dépôt, l'un après l'autre (chaque lancement borné par timeout).
func (t *BackupTool) refresh(ctx context.Context, now time.Time, done chan struct{}) {
	defer func() {
		t.mu.Lock()
		t.running = false
		t.mu.Unlock()
		close(done)
	}()
	path, found := t.cfg.Command[0], true
	if !filepath.IsAbs(path) {
		path, found = findTool(path)
	}
	for _, r := range t.cfg.Repositories {
		if ctx.Err() != nil {
			return
		}
		t.mu.Lock()
		prev := t.repos[r.Name]
		t.mu.Unlock()
		next := &repoRead{}
		if prev != nil {
			next.stats, next.statsAt = prev.stats, prev.statsAt
		}
		switch {
		case !found:
			next.err = fmt.Errorf("%s introuvable", t.cfg.Command[0])
		case t.tool == "borg":
			next.borg, next.err = t.readBorg(ctx, path, r)
		default:
			next.restic, next.err = t.readRestic(ctx, path, r)
		}
		if next.err == nil && t.cfg.Stats && (next.statsAt.IsZero() || now.Sub(next.statsAt) >= backupStatsEvery) {
			if vals, err := t.repoStats(ctx, path, r); err == nil {
				next.stats, next.statsAt = vals, now
			}
		}
		t.mu.Lock()
		t.repos[r.Name] = next
		t.mu.Unlock()
	}
}

// check : nom complet d'un contrôle, écrit en entier pour chaque outil.
func (t *BackupTool) check(kind string) string {
	names := map[string]map[string]string{
		"borg":   {"can_connect": "borg.can_connect", "backup": "borg.backup"},
		"restic": {"can_connect": "restic.can_connect", "backup": "restic.backup"},
	}
	return names[t.tool][kind]
}

func (t *BackupTool) kvs(r config.BackupRepo, kv ...string) []string {
	out := make([]string, 0, 2+2*len(r.Tags)+len(kv))
	keys := make([]string, 0, len(r.Tags))
	for k := range r.Tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, k, r.Tags[k])
	}
	return append(append(out, "repository", r.Name), kv...)
}

// invoke lance l'outil pour un dépôt (phrase de passe et variables posées par l'agent).
func (t *BackupTool) invoke(ctx context.Context, path string, r config.BackupRepo, args ...string) ([]byte, error) {
	env := []string{"LANG=C.UTF-8", "LC_ALL=C.UTF-8"}
	var stdin []byte
	var full []string
	for _, f := range []string{r.PassphraseFile, r.PasswordFile, r.RepositoryFile} {
		if f == "" {
			continue
		}
		if why := secretFileProblem(f); why != "" {
			return nil, fmt.Errorf("%s", why)
		}
	}
	switch t.tool {
	case "borg":
		if err := os.MkdirAll(t.baseDir, 0o700); err != nil {
			return nil, fmt.Errorf("dossier de travail de borg : %v", err)
		}
		env = append(env, "BORG_BASE_DIR="+t.baseDir, "BORG_RELOCATED_REPO_ACCESS_IS_OK=yes",
			"BORG_UNKNOWN_UNENCRYPTED_REPO_ACCESS_IS_OK=yes", "BORG_DISPLAY_PASSPHRASE=no")
		if r.RSH != "" {
			env = append(env, "BORG_RSH="+r.RSH)
		}
		if r.PassphraseFile != "" {
			pass, err := os.ReadFile(r.PassphraseFile)
			if err != nil {
				return nil, fmt.Errorf("phrase de passe illisible : %v", err)
			}
			stdin = pass
			env = append(env, "BORG_PASSPHRASE_FD=0")
		}
		full = append(append(full, t.cfg.Command[1:]...), args...)
		full = append(full, "--bypass-lock", r.Repository)
	case "restic":
		full = append(full, t.cfg.Command[1:]...)
		if r.RepositoryFile != "" {
			full = append(full, "--repository-file", r.RepositoryFile)
		} else {
			full = append(full, "--repo", r.Repository)
		}
		full = append(full, "--password-file", r.PasswordFile, "--no-lock", "--no-cache", "--json")
		full = append(full, args...)
	}
	keys := make([]string, 0, len(r.Env))
	for k := range r.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, k+"="+r.Env[k])
	}
	out, stderr, err := t.run(ctx, t.cfg.Timeout.D(), env, stdin, path, full...)
	if err != nil {
		return out, backupErr(t.tool, err, stderr)
	}
	return out, nil
}

// backupErr : message d'erreur lisible (dernière ligne utile de l'outil ; JSON d'erreur de restic).
func backupErr(tool string, err error, stderr []byte) error {
	lines := strings.Split(strings.TrimSpace(string(stderr)), "\n")
	last := ""
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if l == "" {
			continue
		}
		if strings.HasPrefix(l, "{") {
			var j struct {
				Message string `json:"message"`
			}
			if json.Unmarshal([]byte(l), &j) == nil && j.Message != "" {
				l = j.Message
			}
		}
		last = l
		break
	}
	// restic réessaie un dépôt injoignable : la cause est dans la dernière ligne « returned error,
	// retrying after …: <cause> » ; son message final (« context canceled ») ne dit rien.
	cause := ""
	for i := len(lines) - 1; i >= 0; i-- {
		if l := lines[i]; strings.Contains(l, "returned error, retrying") {
			if j := strings.Index(l, "dial tcp"); j >= 0 {
				cause = l[j:]
			} else if j := strings.LastIndex(l, ": "); j >= 0 {
				cause = l[j+2:]
			}
			break
		}
	}
	last = strings.Join(strings.Fields(last), " ") // message JSON de restic sur plusieurs lignes
	msg := err.Error()
	switch {
	case strings.Contains(msg, "délai dépassé") && cause != "":
		msg = "délai dépassé, dépôt injoignable (" + model.Truncate(strings.TrimSpace(cause), 200) + ")"
	case strings.Contains(msg, "délai dépassé") && last != "":
		msg = "délai dépassé (" + model.Truncate(last, 200) + ")"
	case last != "":
		msg = model.Truncate(last, 300)
	case cause != "":
		msg = model.Truncate(cause, 300)
	}
	return fmt.Errorf("%s : %s", tool, msg)
}

// ── Borg ────────────────────────────────────────────────────────────────────────────────────

type borgArchive struct {
	Name  string `json:"name"`
	Start string `json:"start"`
	Time  string `json:"time"`
}

type borgList struct {
	Archives   []borgArchive `json:"archives"`
	Repository struct {
		LastModified string `json:"last_modified"`
		Location     string `json:"location"`
	} `json:"repository"`
	Encryption struct {
		Mode string `json:"mode"`
	} `json:"encryption"`
}

// borgTime : horodatage de borg 1.x (heure locale sans fuseau, « 2026-11-21T02:29:21.000000 »)
// ou RFC 3339 (borg 2).
func borgTime(s string) (time.Time, bool) {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, true
	}
	for _, f := range []string{"2006-01-02T15:04:05.000000", "2006-01-02T15:04:05"} {
		if t, err := time.ParseInLocation(f, s, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func (t *BackupTool) readBorg(ctx context.Context, path string, r config.BackupRepo) (*borgList, error) {
	out, err := t.invoke(ctx, path, r, "list", "--json", "--consider-checkpoints")
	if err != nil {
		return nil, err
	}
	var l borgList
	if err := json.Unmarshal(out, &l); err != nil {
		return nil, fmt.Errorf("borg list : réponse illisible (borg 1.2 ou plus attendu)")
	}
	return &l, nil
}

func borgChecks(now time.Time, ts int64, b *model.SeriesBuilder, tags model.TagFunc, kv []string, name string, l borgList) []model.Check {
	var complete, checkpoints float64
	var newest, newestAny borgArchive
	var newestT, newestAnyT time.Time
	for _, a := range l.Archives {
		at, ok := borgTime(firstNonEmpty(a.Time, a.Start))
		if !ok {
			continue
		}
		if at.After(newestAnyT) {
			newestAny, newestAnyT = a, at
		}
		if checkpointRe.MatchString(a.Name) {
			checkpoints++
			continue
		}
		complete++
		if at.After(newestT) {
			newest, newestT = a, at
		}
	}
	b.Gauge("borg.archives", "archive", complete, kv...)
	b.Gauge("borg.checkpoints", "archive", checkpoints, kv...)
	if !newestT.IsZero() {
		b.Gauge("borg.last_archive.age", "second", ageSeconds(now, float64(newestT.Unix())), kv...)
	}
	if lm, ok := borgTime(l.Repository.LastModified); ok {
		b.Gauge("borg.repository.last_modified.age", "second", ageSeconds(now, float64(lm.Unix())), kv...)
	}
	st, msg := model.CheckOK, ""
	switch {
	case newestT.IsZero():
		st, msg = model.CheckCrit, fmt.Sprintf("dépôt %s : aucune archive complète", name)
	case newestAnyT.After(newestT) && checkpointRe.MatchString(newestAny.Name):
		st, msg = model.CheckWarn, fmt.Sprintf("dépôt %s : dernière sauvegarde interrompue (point de reprise %s du %s) ; dernière archive complète %s du %s",
			name, newestAny.Name, newestAnyT.Format("02/01 15:04"), newest.Name, newestT.Format("02/01 15:04"))
	default:
		msg = fmt.Sprintf("dépôt %s : dernière archive %s du %s", name, newest.Name, newestT.Format("02/01 15:04"))
	}
	return []model.Check{
		{TS: ts, Name: "borg.backup", Status: st, Message: model.Truncate(msg, 500), Tags: tags(kv...)},
		{TS: ts, Name: "borg.can_connect", Status: model.CheckOK,
			Message: fmt.Sprintf("dépôt %s lu : %.0f archive(s), chiffrement %s", name, complete, firstNonEmpty(l.Encryption.Mode, "?")), Tags: tags(kv...)},
	}
}

// ── restic ──────────────────────────────────────────────────────────────────────────────────

type resticSnapshot struct {
	Time     string   `json:"time"`
	Hostname string   `json:"hostname"`
	Paths    []string `json:"paths"`
	ShortID  string   `json:"short_id"`
	Summary  *struct {
		BackupStart     string  `json:"backup_start"`
		BackupEnd       string  `json:"backup_end"`
		FilesProcessed  float64 `json:"total_files_processed"`
		BytesProcessed  float64 `json:"total_bytes_processed"`
		DataAdded       float64 `json:"data_added"`
		DataAddedPacked float64 `json:"data_added_packed"`
	} `json:"summary"`
}

func (t *BackupTool) readRestic(ctx context.Context, path string, r config.BackupRepo) ([]resticSnapshot, error) {
	out, err := t.invoke(ctx, path, r, "snapshots")
	if err != nil {
		return nil, err
	}
	list := []resticSnapshot{}
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, fmt.Errorf("restic snapshots : réponse illisible")
	}
	return list, nil
}

func resticChecks(now time.Time, ts int64, b *model.SeriesBuilder, tags model.TagFunc, kv []string, name string, list []resticSnapshot) []model.Check {
	var newest resticSnapshot
	var newestT time.Time
	hosts := map[string]time.Time{}
	for _, s := range list {
		at, err := time.Parse(time.RFC3339Nano, s.Time)
		if err != nil {
			continue
		}
		if at.After(newestT) {
			newest, newestT = s, at
		}
		if h := s.Hostname; at.After(hosts[h]) {
			hosts[h] = at
		}
	}
	b.Gauge("restic.snapshots", "snapshot", float64(len(list)), kv...)
	if !newestT.IsZero() {
		b.Gauge("restic.last_snapshot.age", "second", ageSeconds(now, float64(newestT.Unix())), kv...)
		if sm := newest.Summary; sm != nil {
			b.Gauge("restic.last_snapshot.files", "file", sm.FilesProcessed, kv...)
			b.Gauge("restic.last_snapshot.bytes", "byte", sm.BytesProcessed, kv...)
			added := sm.DataAddedPacked
			if added == 0 {
				added = sm.DataAdded
			}
			b.Gauge("restic.last_snapshot.added", "byte", added, kv...)
			s, e1 := time.Parse(time.RFC3339Nano, sm.BackupStart)
			e, e2 := time.Parse(time.RFC3339Nano, sm.BackupEnd)
			if e1 == nil && e2 == nil && !e.Before(s) {
				b.Gauge("restic.last_snapshot.duration", "second", round1(e.Sub(s).Seconds()), kv...)
			}
		}
	}
	names := make([]string, 0, len(hosts))
	for h := range hosts {
		names = append(names, h)
	}
	sort.Strings(names)
	for i, h := range names {
		if i >= backupMaxHosts {
			break
		}
		hkv := append(append([]string{}, kv...), "restic_host", model.Truncate(h, 100))
		b.Gauge("restic.host.last_snapshot.age", "second", ageSeconds(now, float64(hosts[h].Unix())), hkv...)
	}
	st, msg := model.CheckOK, ""
	if newestT.IsZero() {
		st, msg = model.CheckCrit, fmt.Sprintf("dépôt %s : aucun instantané", name)
	} else {
		msg = fmt.Sprintf("dépôt %s : dernier instantané %s de %s (%s) du %s", name, newest.ShortID, newest.Hostname,
			model.Truncate(strings.Join(newest.Paths, ", "), 120), newestT.Local().Format("02/01 15:04"))
	}
	return []model.Check{
		{TS: ts, Name: "restic.backup", Status: st, Message: model.Truncate(msg, 500), Tags: tags(kv...)},
		{TS: ts, Name: "restic.can_connect", Status: model.CheckOK,
			Message: fmt.Sprintf("dépôt %s lu : %d instantané(s)", name, len(list)), Tags: tags(kv...)},
	}
}

// repoStats : taille du dépôt (borg info : cache de borg, construit au premier passage ; restic
// stats --mode raw-data : lit l'index). Relu toutes les 6 h seulement (stats: true).
func (t *BackupTool) repoStats(ctx context.Context, path string, r config.BackupRepo) (map[string]float64, error) {
	if t.tool == "borg" {
		out, err := t.invoke(ctx, path, r, "info", "--json")
		if err != nil {
			return nil, err
		}
		var i struct {
			Cache struct {
				Stats struct {
					TotalSize   float64 `json:"total_size"`
					UniqueSize  float64 `json:"unique_size"`
					UniqueCSize float64 `json:"unique_csize"`
					Chunks      float64 `json:"total_unique_chunks"`
				} `json:"stats"`
			} `json:"cache"`
		}
		if err := json.Unmarshal(out, &i); err != nil {
			return nil, err
		}
		s := i.Cache.Stats
		return map[string]float64{"borg.repository.total_size": s.TotalSize, "borg.repository.unique_size": s.UniqueSize,
			"borg.repository.unique_csize": s.UniqueCSize, "borg.repository.unique_chunks": s.Chunks}, nil
	}
	out, err := t.invoke(ctx, path, r, "stats", "--mode", "raw-data")
	if err != nil {
		return nil, err
	}
	var s struct {
		TotalSize    float64 `json:"total_size"`
		Uncompressed float64 `json:"total_uncompressed_size"`
		Ratio        float64 `json:"compression_ratio"`
		Blobs        float64 `json:"total_blob_count"`
	}
	if err := json.Unmarshal(out, &s); err != nil {
		return nil, err
	}
	return map[string]float64{"restic.repository.size": s.TotalSize, "restic.repository.uncompressed_size": s.Uncompressed,
		"restic.repository.compression_ratio": round1(s.Ratio), "restic.repository.blobs": s.Blobs}, nil
}

// runBackupTool : comme runTool, avec une entrée standard et un environnement réduit ; sous systemd,
// dans une unité transitoire bornée (l'outil peut dépasser le plafond mémoire de l'agent).
func runBackupTool(ctx context.Context, timeout time.Duration, env []string, stdin []byte, path string, args ...string) ([]byte, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout+10*time.Second)
	defer cancel()
	bin, argv := path, args
	childEnv := append([]string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}, env...)
	if home, ok := os.LookupEnv("HOME"); ok {
		childEnv = append(childEnv, "HOME="+home)
	}
	if sr, ok := findTool("systemd-run"); ok && fileExists("/run/systemd/system") && os.Geteuid() == 0 {
		pre := []string{"--quiet", "--wait", "--pipe", "--collect", "-p", "MemoryMax=512M", "-p", "CPUQuota=50%",
			"-p", "Nice=10", "-p", "IOSchedulingClass=idle", "-p", "NoNewPrivileges=yes", "-p", "ProtectSystem=full",
			"-p", "ProtectHome=read-only", "-p", "PrivateTmp=yes", "-p", fmt.Sprintf("RuntimeMaxSec=%d", int(timeout.Seconds()))}
		for _, e := range env {
			pre = append(pre, "-E", e)
		}
		bin, argv = sr, append(append(pre, path), args...)
	}
	cmd := exec.CommandContext(ctx, bin, argv...)
	cmd.Env = childEnv
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, stderr bytes.Buffer
	cmd.Stdout = &limited{w: &out, n: 32 << 20}
	cmd.Stderr = &limited{w: &stderr, n: 64 << 10}
	cmd.WaitDelay = 2 * time.Second
	start := time.Now()
	err := cmd.Run()
	if err != nil && (ctx.Err() != nil || time.Since(start) >= timeout) {
		err = fmt.Errorf("délai dépassé") // contexte échu, ou unité arrêtée par RuntimeMaxSec
	}
	return out.Bytes(), stderr.Bytes(), err
}
