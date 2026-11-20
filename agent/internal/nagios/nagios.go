package nagios

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

const (
	maxStdout     = 64 << 10 // Nagios 4 coupe à 8 Kio par défaut ; Centreon en écrit parfois plus
	maxStderr     = 4 << 10
	maxConcurrent = 4 // plugins lancés en même temps au plus
	maxMessage    = 3500
)

// Options : ce que le lanceur demande à l'agent.
type Options struct {
	Tags      model.TagFunc
	Post      func(series []model.Series, checks []model.Check)
	SetStatus func(name, state string)
	Log       *slog.Logger
	// ConfigFiles : fichiers de conf qui déclarent des plugins (propriétaire vérifié).
	ConfigFiles []string
}

// Check : un plugin déclaré.
type Check struct {
	cfg  config.NagiosCheck
	o    Options
	sem  chan struct{}
	deny string // motif de refus calculé au démarrage (conf modifiable par un autre compte)

	mu    sync.Mutex
	state string
}

// Manager fait tourner les plugins.
type Manager struct {
	list []*Check
}

// New prépare les plugins ; si un fichier de conf qui en déclare n'est pas sûr, aucun de ses
// plugins ne sera lancé (état d'erreur et contrôle UNKNOWN qui le disent).
func New(cfgs []config.NagiosCheck, o Options) *Manager {
	deny := ""
	for _, f := range o.ConfigFiles {
		if why := unsafeConfig(f); why != "" {
			deny = why
			break
		}
	}
	sem := make(chan struct{}, maxConcurrent)
	m := &Manager{}
	for _, c := range cfgs {
		m.list = append(m.list, &Check{cfg: c, o: o, sem: sem, deny: deny})
	}
	return m
}

// Len rend le nombre de plugins.
func (m *Manager) Len() int { return len(m.list) }

func (c *Check) name() string { return "nagios:" + c.cfg.Name }

func (c *Check) setState(s string) {
	c.mu.Lock()
	c.state = s
	c.mu.Unlock()
	if c.o.SetStatus != nil {
		c.o.SetStatus(c.name(), s)
	}
}

func (c *Check) tags(kv ...string) map[string]string {
	base := []string{"nagios_check", c.cfg.Name}
	for _, k := range model.SortedKeys(c.cfg.Tags) {
		base = append(base, k, c.cfg.Tags[k])
	}
	return c.o.Tags(append(base, kv...)...)
}

// Resolve trouve l'exécutable : chemin absolu tel quel, simple nom cherché dans les dossiers
// de plugins connus (jamais dans le PATH, qu'un autre compte pourrait détourner).
func Resolve(name string) (string, error) {
	if filepath.IsAbs(name) {
		return name, nil
	}
	if runtime.GOOS == "windows" || strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("%s : chemin absolu attendu", name)
	}
	for _, d := range config.NagiosPluginDirs {
		p := filepath.Join(d, name)
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			return p, nil
		}
	}
	return "", fmt.Errorf("plugin %s introuvable dans %s", name, strings.Join(config.NagiosPluginDirs, ", "))
}

// limited garde le début d'une sortie et compte le reste.
type limited struct {
	b       bytes.Buffer
	max     int
	dropped int
}

func (l *limited) Write(p []byte) (int, error) {
	n := min(max(l.max-l.b.Len(), 0), len(p))
	l.b.Write(p[:n])
	l.dropped += len(p) - n
	return len(p), nil
}

// Result : résultat d'un lancement.
type Result struct {
	Status   int
	Output   string
	Stderr   string
	Duration time.Duration
	Err      string // le plugin n'a pas pu tourner (introuvable, refusé, délai)
}

func (c *Check) env() []string {
	var env []string
	if runtime.GOOS == "windows" {
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(strings.ToUpper(kv), "HYPHA_") {
				env = append(env, kv)
			}
		}
	} else {
		env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C", "HOME=/tmp"}
	}
	for _, k := range model.SortedKeys(c.cfg.Env) {
		env = append(env, k+"="+c.cfg.Env[k])
	}
	return env
}

// Exec lance le plugin une fois.
func (c *Check) Exec(ctx context.Context) Result {
	if c.deny != "" {
		return Result{Status: model.CheckUnknown, Err: c.deny}
	}
	exe, err := Resolve(c.cfg.Command[0])
	if err != nil {
		return Result{Status: model.CheckUnknown, Err: err.Error()}
	}
	if why := unsafeExecutable(exe); why != "" {
		return Result{Status: model.CheckUnknown, Err: why}
	}
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout.D())
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, c.cfg.Command[1:]...)
	cmd.Env = c.env()
	cmd.Dir = os.TempDir()
	if err := prepareCmd(cmd, c.cfg.User); err != nil {
		return Result{Status: model.CheckUnknown, Err: err.Error()}
	}
	cmd.WaitDelay = 2 * time.Second
	stdout, stderr := &limited{max: maxStdout}, &limited{max: maxStderr}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	start := time.Now()
	err = cmd.Run()
	r := Result{Output: strings.ToValidUTF8(stdout.b.String(), ""), Stderr: strings.ToValidUTF8(stderr.b.String(), ""),
		Duration: time.Since(start)}
	var ee *exec.ExitError
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		r.Status, r.Err = model.CheckCrit, fmt.Sprintf("délai de %s dépassé : plugin arrêté", c.cfg.Timeout.D())
	case err == nil:
		r.Status = model.CheckOK
	case errors.As(err, &ee) && ee.ExitCode() >= 0:
		switch code := ee.ExitCode(); code {
		case 1, 2, 3:
			r.Status = code
		default:
			r.Status = model.CheckUnknown // code hors convention : UNKNOWN, comme Nagios
		}
	default:
		r.Status, r.Err = model.CheckUnknown, err.Error()
	}
	if stdout.dropped > 0 {
		r.Output += fmt.Sprintf("\n[sortie coupée : %d octets de plus]", stdout.dropped)
	}
	return r
}

var spaceRe = regexp.MustCompile(`\s+`)

// Measure traduit un résultat en contrôle et en séries.
func (c *Check) Measure(r Result, now time.Time) ([]model.Series, model.Check) {
	ts := model.Ms(now)
	text, long, perf := SplitOutput(r.Output)
	msg := text
	switch {
	case r.Err != "":
		msg = r.Err
		if text != "" {
			msg += " ; " + text
		}
	case msg == "" && strings.TrimSpace(r.Stderr) != "":
		msg = "(aucune sortie) " + strings.TrimSpace(r.Stderr)
	case msg == "":
		msg = "(aucune sortie)"
	}
	if long != "" {
		msg += "\n" + long
	}
	check := model.Check{TS: ts, Name: "nagios.check", Status: r.Status, Message: model.Truncate(msg, maxMessage), Tags: c.tags()}
	var series []model.Series
	add := func(name, unit string, v float64, kv ...string) {
		series = append(series, model.Series{Metric: name, Type: model.TypeGauge, Unit: unit, Tags: c.tags(kv...),
			Points: []model.Point{{TS: ts, Value: v}}})
	}
	if r.Duration > 0 {
		add("nagios.duration", "second", r.Duration.Seconds())
	}
	if r.Err == "" && c.cfg.PerfdataOn() {
		seen := map[string]bool{}
		for _, p := range ParsePerfdata(perf) {
			label := model.Truncate(spaceRe.ReplaceAllString(strings.TrimSpace(p.Label), " "), model.MaxTagValue)
			if label == "" || seen[label] || len(seen) >= 100 {
				continue
			}
			seen[label] = true
			v, unit := Normalize(p.Value, p.UOM)
			add("nagios.perfdata", unit, v, "perf_label", label)
			for _, b := range []struct {
				suffix string
				v      *float64
			}{{".warn", p.Warn}, {".crit", p.Crit}, {".min", p.Min}, {".max", p.Max}} {
				if b.v != nil {
					x, _ := Normalize(*b.v, p.UOM)
					add("nagios.perfdata"+b.suffix, unit, x, "perf_label", label)
				}
			}
		}
	}
	return series, check
}

func (c *Check) once(ctx context.Context, now time.Time) {
	defer func() {
		if r := recover(); r != nil {
			c.setState(fmt.Sprintf("erreur : panique : %v", r))
		}
	}()
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return
	}
	r := c.Exec(ctx)
	<-c.sem
	if ctx.Err() != nil && r.Err != "" {
		return // arrêt de l'agent pendant le lancement : rien à rendre
	}
	series, check := c.Measure(r, now)
	switch {
	case r.Err != "":
		c.setState("erreur : " + r.Err)
	default:
		c.setState("ok")
	}
	c.o.Post(series, []model.Check{check})
}

func (c *Check) run(ctx context.Context) {
	every := c.cfg.Interval.D()
	t := time.NewTimer(time.Duration(rand.Int64N(int64(min(every, 30*time.Second)))))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			c.once(ctx, now)
			next := every - time.Since(now)
			if next < time.Second {
				next = time.Second
			}
			t.Reset(next)
		}
	}
}

// Run fait tourner les plugins jusqu'à l'arrêt (4 lancés en même temps au plus).
func (m *Manager) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, c := range m.list {
		if c.deny != "" {
			c.setState("erreur : " + c.deny)
			if c.o.Log != nil {
				c.o.Log.Warn("plugins Nagios refusés", "plugin", c.cfg.Name, "motif", c.deny)
			}
		} else {
			c.setState("ok")
		}
		wg.Add(1)
		go func(c *Check) {
			defer wg.Done()
			c.run(ctx)
		}(c)
	}
	wg.Wait()
}

// Once lance chaque plugin une fois (run --once).
func (m *Manager) Once(ctx context.Context) {
	var wg sync.WaitGroup
	for _, c := range m.list {
		wg.Add(1)
		go func(c *Check) {
			defer wg.Done()
			c.once(ctx, time.Now())
		}(c)
	}
	wg.Wait()
}

// Active : plugins en service (capacités de l'agent).
func (m *Manager) Active() []string {
	var out []string
	for _, c := range m.list {
		c.mu.Lock()
		st := c.state
		c.mu.Unlock()
		if st != "" && st != "disabled" {
			out = append(out, c.name())
		}
	}
	return out
}
