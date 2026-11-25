// Package agent assemble les collecteurs et les fait tourner aux cadences du contrat (§ 3) :
// métriques et contrôles à chaque intervalle (15 s), inventaire au démarrage puis toutes
// les 5 min, processus et connexions toutes les 60 s, conteneurs toutes les 30 s, journaux
// en continu (lots de 500 lignes ou 5 s).
//
// Chaque collecteur tourne dans sa propre boucle : un collecteur lent ou en panne ne
// retarde pas les autres, et une panique est rattrapée et notée au lieu d'arrêter l'agent.
package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"

	"s33d.fr/hypha/agent/internal/actions"
	"s33d.fr/hypha/agent/internal/agentlog"
	"s33d.fr/hypha/agent/internal/checks"
	"s33d.fr/hypha/agent/internal/cmdb"
	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/docker"
	"s33d.fr/hypha/agent/internal/integrations"
	"s33d.fr/hypha/agent/internal/inventory"
	"s33d.fr/hypha/agent/internal/logs"
	"s33d.fr/hypha/agent/internal/metrics"
	"s33d.fr/hypha/agent/internal/model"
	"s33d.fr/hypha/agent/internal/netconn"
	"s33d.fr/hypha/agent/internal/openmetrics"
	"s33d.fr/hypha/agent/internal/otlp"
	"s33d.fr/hypha/agent/internal/plugins"
	"s33d.fr/hypha/agent/internal/procs"
	"s33d.fr/hypha/agent/internal/sender"
	"s33d.fr/hypha/agent/internal/snmp"
	"s33d.fr/hypha/agent/internal/state"
	"s33d.fr/hypha/agent/internal/statsd"
	"s33d.fr/hypha/agent/internal/status"
	"s33d.fr/hypha/agent/internal/synthetics"
	"s33d.fr/hypha/agent/internal/syslog"
	"s33d.fr/hypha/agent/internal/update"
	"s33d.fr/hypha/agent/internal/updates"
)

// Cadences fixées par le contrat.
const (
	hostEvery        = 5 * time.Minute
	processesEvery   = 60 * time.Second
	connectionsEvery = 60 * time.Second
	containersEvery  = 30 * time.Second
	statusFileEvery  = 60 * time.Second
	maxSeriesPerPost = 5000
)

// StatusFile est le fichier d'état lu par `hypha-agent status`.
const StatusFile = "status.json"

// Options règle une exécution.
type Options struct {
	Version string
	DryRun  bool // afficher les corps au lieu de les envoyer ; ne rien écrire sur disque
	Once    bool // un seul cycle de chaque collecteur, puis sortie
	Logger  *slog.Logger
	Out     io.Writer
}

// Agent réunit l'état partagé par les collecteurs.
type Agent struct {
	cfg      *config.Config
	opt      Options
	log      *slog.Logger
	hostname string
	store    *state.Store
	status   *status.Registry
	sender   *sender.Sender
	limiter  *agentlog.Limiter
	started  time.Time

	sys      *metrics.System
	procs    *procs.Collector
	docker   *docker.Collector
	checks   *checks.Runner
	statsd   *statsd.Server
	integr   *integrations.Runner
	agentID  string
	totalMem atomic.Uint64 // écrit par l'inventaire, lu par processus et conteneurs

	// 0.4.0 (CONTRAT-04 §§ 14, 15, 27, 28, 29).
	updates     *updates.Checker
	updater     *update.Updater
	updateOff   string
	actions     *actions.Executor
	synth       *synthetics.Runner
	snmp        *snmp.Manager
	inventory   *cmdb.Collector
	configHash  string
	restart     atomic.Bool
	integrFirst atomic.Bool
	cancelRun   context.CancelFunc
	// 0.5.0 : moteurs génériques (v05.go).
	omMgr        *openmetrics.Manager
	pluginsMgr   *plugins.Manager
	syslogSrv    *syslog.Server
	controlPause time.Time // canal de contrôle en pause jusqu'à cette heure (erreurs)
	controlFails int

	mu        sync.Mutex
	lastStats sender.Stats
	flushers  []func() // écriture finale des curseurs de journaux, après le dernier envoi
}

// New prépare l'agent (identité, expéditeur, collecteurs).
func New(cfg *config.Config, opt Options) (*Agent, error) {
	hostname := strings.TrimSpace(cfg.Hostname)
	if hostname == "" {
		h, err := os.Hostname()
		if err != nil {
			return nil, fmt.Errorf("nom d'hôte : %w", err)
		}
		// Nom court en minuscules : « WEB-01 » et « web-01 » désignent la même machine, et les
		// requêtes host:web-01 ne doivent pas dépendre de la casse choisie par l'installateur.
		hostname = strings.ToLower(h)
	}
	a := &Agent{
		cfg: cfg, opt: opt, log: opt.Logger, hostname: hostname,
		store:   &state.Store{Dir: config.StateDir(), ReadOnly: opt.DryRun},
		status:  status.New(),
		limiter: agentlog.NewLimiter(time.Minute),
		started: time.Now(),
	}
	if err := a.store.Ensure(); err != nil {
		return nil, fmt.Errorf("répertoire d'état %s : %w", a.store.Dir, err)
	}
	id, ephemeral, err := a.store.AgentID()
	if err != nil {
		return nil, err
	}
	a.agentID = id
	if ephemeral {
		a.log.Info("dry-run : identité éphémère, rien n'est écrit sur disque", "agent_id", id)
	}
	info := model.AgentInfo{ID: id, Version: opt.Version, Hostname: hostname, OS: runtime.GOOS, Arch: runtime.GOARCH}
	a.sender, err = sender.New(sender.Options{
		URL: cfg.URL, APIKey: cfg.APIKey, Agent: info, Proxy: cfg.Proxy, TLSSkipVerify: cfg.TLSSkipVerify, TLSPin: cfg.TLSPin,
		BufferDir: cfg.Buffer.Dir, BufferMaxBytes: int64(cfg.Buffer.MaxMB) << 20,
		DryRun: opt.DryRun, Out: opt.Out, Logger: opt.Logger,
	})
	if err != nil {
		return nil, err
	}

	excl := cfg.Network.ExcludeInterfaces
	a.sys = metrics.New(excl, a.log)
	a.status.Set("metrics", "ok")
	a.checks = checks.NewRunner(cfg.Checks, a.log)
	if cfg.Processes.Enabled {
		a.procs = procs.New(cfg.Processes.Include)
		a.status.Set("processes", "ok")
	} else {
		a.status.Set("processes", "disabled")
	}
	if !cfg.Network.Enabled {
		a.status.Set("network", "disabled")
	} else {
		a.status.Set("network", "ok")
	}
	switch {
	case cfg.Containers.Enabled == "false":
		a.status.Set("docker", "disabled")
	case runtime.GOOS != "linux":
		a.status.Set("docker", "absent")
	case docker.Available(docker.DefaultSocket):
		a.docker = docker.New(docker.DefaultSocket)
		a.status.Set("docker", "ok")
	case cfg.Containers.Enabled == "true":
		a.status.Set("docker", "erreur : "+docker.DefaultSocket+" introuvable")
	default:
		a.status.Set("docker", "absent")
	}
	a.updates = &updates.Checker{}
	a.integr = integrations.New(cfg.Integrations, integrations.Deps{
		Log: a.log, SetStatus: a.status.Set, Updates: a.updates,
		Post:    func(route string, fields map[string]any) { a.sender.Post(route, fields, nil) },
		AgentID: a.agentID, AgentVersion: a.opt.Version, Tags: a.cfg.Tags,
	})
	a.configHash = cfg.Hash()
	if err := a.setupV04(); err != nil {
		return nil, err
	}
	a.setupV05()

	// État initial, envoyé avec le premier inventaire ; chaque collecteur le met à jour ensuite.
	a.status.Set("checks", "ok")
	if cfg.Logs.Enabled {
		a.status.Set("logs", "ok")
	} else {
		a.status.Set("logs", "disabled")
	}
	if v, err := mem.VirtualMemory(); err == nil {
		a.totalMem.Store(v.Total)
	}
	return a, nil
}

// tags rend les étiquettes communes : celles de la conf, plus host (toujours l'hôte de
// l'agent), plus les paires données.
func (a *Agent) tags(kv ...string) map[string]string {
	t := make(map[string]string, len(a.cfg.Tags)+1+len(kv)/2)
	for k, v := range a.cfg.Tags {
		t[k] = v
	}
	t["host"] = a.hostname
	for i := 0; i+1 < len(kv); i += 2 {
		t[kv[i]] = kv[i+1]
	}
	return t
}

// Run fait tourner l'agent jusqu'à l'annulation de ctx (ou un seul cycle avec Once).
func (a *Agent) Run(ctx context.Context) error {
	// Empreinte : deux fils d'exécution Go suffisent au travail de l'agent, et une limite
	// douce de mémoire fait ramasser les miettes plus tôt plutôt que de laisser gonfler.
	runtime.GOMAXPROCS(min(2, runtime.NumCPU()))
	debug.SetMemoryLimit(48 << 20)
	a.log.Info("démarrage", "version", a.opt.Version, "hôte", a.hostname, "agent_id", a.agentID,
		"url", a.cfg.URL, "dry_run", a.opt.DryRun, "once", a.opt.Once, "fichiers", a.cfg.Files)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	a.cancelRun = cancel
	if !a.opt.DryRun {
		a.updater.Startup()
	}
	if a.opt.Once {
		return a.runOnce(ctx)
	}
	err := a.runForever(ctx)
	if a.restart.Load() {
		return ErrRestart
	}
	return err
}

func (a *Agent) runForever(ctx context.Context) error {
	// L'expéditeur survit quelques secondes à l'arrêt pour envoyer les derniers corps ;
	// au-delà, ce qui reste part dans le tampon disque.
	sendCtx, stopSend := context.WithCancel(context.Background())
	defer stopSend()
	a.sender.Start(sendCtx, true)

	a.startListeners(ctx)
	logsDone := a.startLogs(ctx, false)

	now := time.Now()
	a.hostCycle(ctx, now)
	a.startEvents(now)
	if a.procs != nil {
		a.procs.Collect(now, a.totalMem.Load()) // premier relevé : base des pourcentages CPU
	}
	if a.docker != nil {
		a.docker.Collect(ctx, now, a.totalMem.Load(), a.tags)
	}

	var wg sync.WaitGroup
	every := func(name string, first, period time.Duration, fn func(context.Context, time.Time)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.loop(ctx, name, first, period, fn)
		}()
	}
	every("metrics", 0, a.cfg.Interval.D(), a.metricsCycle)
	// Intégrations dans leur propre boucle : une base de données lente ne retarde pas les
	// métriques système.
	every("integrations", 0, a.cfg.Interval.D(), a.integrationsCycle)
	every("host", hostEvery, hostEvery, a.hostCycle)
	if a.procs != nil {
		every("processes", processesEvery, processesEvery, a.processesCycle)
	}
	if a.cfg.Network.Enabled {
		every("network", 10*time.Second, connectionsEvery, a.connectionsCycle)
	}
	if a.docker != nil {
		every("docker", containersEvery, containersEvery, a.containersCycle)
	}
	every("status", 5*time.Second, statusFileEvery, func(context.Context, time.Time) { a.writeStatusFile() })
	goRun := func(fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.safe("v04", fn)
		}()
	}
	a.startV04(ctx, every, goRun)
	a.startV05(ctx, goRun)

	<-ctx.Done()
	a.log.Info("arrêt demandé")
	wg.Wait()
	<-logsDone // le dernier lot de journaux est confié à l'expéditeur
	a.integr.Close()
	time.AfterFunc(5*time.Second, stopSend)
	a.sender.Close()
	a.flushStates()
	a.writeStatusFile()
	a.log.Info("arrêté")
	return nil
}

// runOnce : un cycle de chaque collecteur. Les débits et pourcentages demandent deux
// relevés : un premier est pris, puis le cycle réel 2 s plus tard.
func (a *Agent) runOnce(ctx context.Context) error {
	a.sender.Start(ctx, false)
	a.startListeners(ctx)

	prime := time.Now()
	a.sys.Collect(ctx, prime, a.tags)
	if a.procs != nil {
		a.procs.Collect(prime, a.totalMem.Load())
	}
	if a.docker != nil {
		a.docker.Collect(ctx, prime, a.totalMem.Load(), a.tags)
	}
	a.integr.Collect(ctx, prime, a.tags)
	a.integr.ResetSchedule()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(2 * time.Second):
	}

	now := time.Now()
	a.hostCycle(ctx, now)
	a.startEvents(now)
	a.metricsCycle(ctx, now)
	a.integrationsCycle(ctx, now)
	a.integr.Close()
	a.onceV05(ctx)
	if a.snmp != nil {
		a.snmp.PollAll(ctx)
	}
	if a.inventory != nil {
		a.inventory.Once(ctx, true)
	}
	if a.procs != nil {
		a.processesCycle(ctx, now)
	}
	if a.cfg.Network.Enabled {
		a.connectionsCycle(ctx, now)
	}
	if a.docker != nil {
		a.containersCycle(ctx, now)
	}
	<-a.startLogs(ctx, true)
	a.sender.Close()
	a.flushStates()
	return nil
}

// loop attend first, exécute fn, puis recommence toutes les period.
func (a *Agent) loop(ctx context.Context, name string, first, period time.Duration, fn func(context.Context, time.Time)) {
	t := time.NewTimer(first)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			a.safe(name, func() { fn(ctx, now) })
			// Prochain passage aligné sur la période, même si celui-ci a duré.
			next := period - time.Since(now)
			if next < time.Second {
				next = time.Second
			}
			t.Reset(next)
		}
	}
}

// safe rattrape une panique d'un collecteur : notée, puis le cycle suivant a lieu.
func (a *Agent) safe(name string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			err := fmt.Errorf("panique : %v", r)
			a.status.Error(name, err)
			a.log.Error("collecteur en panique, cycle abandonné", "collecteur", name, "err", r, "pile", string(debug.Stack()))
		}
	}()
	fn()
}

func (a *Agent) collectorError(name string, err error) {
	a.status.Error(name, err)
	if a.limiter.Allow(name + err.Error()) {
		a.log.Warn("erreur de collecte", "collecteur", name, "err", err)
	}
}

func (a *Agent) startListeners(ctx context.Context) {
	if a.cfg.StatsD.Enabled {
		s := statsd.New(a.cfg.StatsD.Listen, a.log)
		if err := s.Start(ctx); err != nil {
			a.status.Set("statsd", "erreur : "+err.Error())
			a.log.Warn("écoute StatsD impossible", "adresse", a.cfg.StatsD.Listen, "err", err)
		} else {
			a.statsd = s
			a.status.Set("statsd", "ok")
		}
	} else {
		a.status.Set("statsd", "disabled")
	}
	if a.cfg.OTLP.Enabled {
		tr, err := sender.NewTransport(a.cfg.Proxy, a.cfg.TLSSkipVerify, a.cfg.TLSPin)
		if err == nil {
			r := otlp.New(a.cfg.OTLP.Listen, a.cfg.URL, a.cfg.APIKey, a.opt.Version, tr, a.opt.DryRun, a.log)
			err = r.Start(ctx)
		}
		if err != nil {
			a.status.Set("otlp", "erreur : "+err.Error())
			a.log.Warn("relais OTLP impossible", "adresse", a.cfg.OTLP.Listen, "err", err)
		} else {
			a.status.Set("otlp", "ok")
		}
	} else {
		a.status.Set("otlp", "disabled")
	}
}

// metricsCycle : métriques système, StatsD, métriques de l'agent, puis contrôles.
func (a *Agent) metricsCycle(ctx context.Context, now time.Time) {
	res := a.sys.Collect(ctx, now, a.tags)
	for _, err := range res.Errors {
		a.collectorError("metrics", err)
	}
	if len(res.Errors) == 0 {
		a.status.Set("metrics", "ok")
		a.status.Clear("metrics")
	} else {
		a.status.Set("metrics", fmt.Sprintf("partiel : %d erreur(s)", len(res.Errors)))
	}
	series, events := res.Series, res.Events

	var extraChecks []model.Check
	if a.statsd != nil {
		s, c, e, dropped := a.statsd.Flush(now, a.tags)
		series = append(series, s...)
		extraChecks, events = c, append(events, e...)
		if dropped > 0 && a.limiter.Allow("statsd-dropped") {
			a.log.Warn("StatsD : trop de séries distinctes, valeurs jetées", "jetées", dropped)
		}
	}
	if s, c := a.flushSyslog(now); len(s)+len(c) > 0 {
		series = append(series, s...)
		extraChecks = append(extraChecks, c...)
	}

	if ps, pc, err := a.checks.Processes(ctx, now, a.tags); err != nil {
		a.collectorError("checks", err)
	} else {
		series = append(series, ps...)
		extraChecks = append(extraChecks, pc...)
	}

	st := a.sender.TakeStats()
	a.mu.Lock()
	a.lastStats = st
	a.mu.Unlock()
	b := model.NewSeriesBuilder(model.Ms(now), a.tags)
	b.Gauge("hypha.agent.payload_bytes", "byte", float64(st.SentBytes))
	b.Gauge("hypha.agent.dropped", "count", float64(st.Dropped))
	b.Gauge("hypha.agent.buffered", "byte", float64(st.BufferedBytes))
	series = append(series, b.Series()...)
	a.postSeries(series)

	cs, ce, err := a.checks.Run(ctx, now, a.tags)
	if err != nil {
		a.collectorError("checks", err)
		a.status.Set("checks", "partiel : "+err.Error())
	} else {
		a.status.Set("checks", "ok")
		a.status.Clear("checks")
	}
	a.sender.Post(sender.RouteChecks, map[string]any{"checks": append(cs, extraChecks...)}, nil)
	events = append(events, ce...)
	if len(events) > 0 {
		a.sender.Post(sender.RouteEvents, map[string]any{"events": events}, nil)
	}
}

// integrationsCycle : PostgreSQL, HAProxy, nginx.
func (a *Agent) integrationsCycle(ctx context.Context, now time.Time) {
	series, checks := a.integr.Collect(ctx, now, a.tags)
	if len(series) > 0 {
		a.postSeries(series)
	}
	if len(checks) > 0 {
		a.sender.Post(sender.RouteChecks, map[string]any{"checks": checks}, nil)
	}
	// Les intégrations actives entrent dans les capacités : l'inventaire part de nouveau
	// après le premier cycle, sans attendre 5 minutes.
	if !a.opt.Once && a.integrFirst.CompareAndSwap(false, true) {
		a.hostCycle(ctx, now)
	}
}

// postSeries découpe un grand nombre de séries (StatsD très bavard) en plusieurs envois.
func (a *Agent) postSeries(series []model.Series) {
	for len(series) > 0 {
		n := min(len(series), maxSeriesPerPost)
		a.sender.Post(sender.RouteMetrics, map[string]any{"series": series[:n]}, nil)
		series = series[n:]
	}
}

func (a *Agent) hostCycle(ctx context.Context, _ time.Time) {
	tags := make(map[string]string, len(a.cfg.Tags))
	for k, v := range a.cfg.Tags {
		tags[k] = v
	}
	h, errs := inventory.Collect(ctx, a.hostname, a.opt.Version, tags, a.cfg.Network.ExcludeInterfaces, a.status.Snapshot())
	for _, err := range errs {
		a.collectorError("host", err)
	}
	if h.MemTotal > 0 {
		a.totalMem.Store(h.MemTotal)
	}
	h.Capabilities = a.capabilities()
	h.ConfigHash = a.configHash
	us := a.updater.Status()
	h.Status.Update = &us
	a.sender.Post(sender.RouteHost, map[string]any{"host": h}, nil)
}

// startEvents : démarrage de l'agent, et redémarrage de l'hôte si la date de démarrage
// a changé depuis la dernière exécution.
func (a *Agent) startEvents(now time.Time) {
	text := fmt.Sprintf("hypha-agent %s (%s/%s) sur %s, identifiant %s.", a.opt.Version, runtime.GOOS, runtime.GOARCH, a.hostname, a.agentID)
	if us := a.updater.Status(); us.State == "idle" && us.To == a.opt.Version && us.From != "" && us.From != us.To {
		text += " Mise à jour automatique depuis la version " + us.From + "."
	}
	events := []model.Event{{
		TS: model.Ms(now), Source: "agent", Type: "agent_start",
		Title:     "Démarrage de l'agent HYPHA " + a.opt.Version,
		Text:      text,
		AlertType: "info", Priority: "low", Tags: a.tags(), AggregationKey: "agent:" + a.hostname,
	}}
	if boot, err := host.BootTime(); err == nil && boot > 0 {
		prev, _ := a.store.ReadText("boot_time")
		if p, err := strconv.ParseInt(prev, 10, 64); err == nil {
			// La date de démarrage calculée peut bouger d'une seconde : on tolère une minute.
			if d := int64(boot) - p; d > 60 || d < -60 {
				bt := time.Unix(int64(boot), 0)
				events = append(events, model.Event{
					TS: model.Ms(now), Source: "agent", Type: "host_reboot",
					Title:     "Redémarrage de l'hôte " + a.hostname,
					Text:      "Hôte redémarré le " + bt.Format("02/01/2006 à 15:04:05 MST") + ".",
					AlertType: "warning", Priority: "normal", Tags: a.tags(), AggregationKey: "reboot:" + a.hostname,
				})
			}
		}
		if err := a.store.WriteFile("boot_time", []byte(strconv.FormatUint(boot, 10)+"\n")); err != nil {
			a.collectorError("host", err)
		}
	}
	a.sender.Post(sender.RouteEvents, map[string]any{"events": events}, nil)
}

func (a *Agent) processesCycle(_ context.Context, now time.Time) {
	list, err := a.procs.Collect(now, a.totalMem.Load())
	if err != nil {
		a.collectorError("processes", err)
		a.status.Set("processes", "erreur : "+err.Error())
		return
	}
	a.status.Set("processes", "ok")
	if list == nil {
		list = []model.Process{}
	}
	a.sender.Post(sender.RouteProcesses, map[string]any{"ts": model.Ms(now), "processes": list}, nil)
}

func (a *Agent) connectionsCycle(ctx context.Context, now time.Time) {
	list, err := netconn.Collect(ctx, procs.NewNameResolver())
	if err != nil {
		a.collectorError("network", err)
		a.status.Set("network", "erreur : "+err.Error())
		return
	}
	// En conteneur, rattacher une socket à son processus demande de lire /proc/<pid>/fd des
	// processus de l'hôte, ce que Docker refuse sans SYS_PTRACE et hors profil AppArmor :
	// les connexions partent quand même, sans processus ; l'état le dit et donne le remède.
	sockets, named := 0, 0
	for _, c := range list {
		sockets += c.Count
		if c.Process != "" {
			named += c.Count
		}
	}
	if os.Getenv("HOST_PROC") != "" && sockets > 0 && named == 0 {
		a.status.Set("network", "partiel : processus des connexions illisibles (lancer le conteneur avec --cap-add SYS_PTRACE --security-opt apparmor=unconfined)")
	} else {
		a.status.Set("network", "ok")
	}
	a.sender.Post(sender.RouteConnections, map[string]any{"ts": model.Ms(now), "connections": list}, nil)
}

func (a *Agent) containersCycle(ctx context.Context, now time.Time) {
	cts, series, err := a.docker.Collect(ctx, now, a.totalMem.Load(), a.tags)
	if err != nil {
		a.collectorError("docker", err)
		a.status.Set("docker", "erreur : "+err.Error())
		return
	}
	a.status.Set("docker", "ok")
	a.status.Clear("docker")
	a.sender.Post(sender.RouteContainers, map[string]any{"ts": model.Ms(now), "containers": cts}, nil)
	if len(series) > 0 {
		a.postSeries(series)
	}
}

// startLogs lance les sources de journaux ; le canal rendu se ferme quand le dernier lot
// a été confié à l'expéditeur. once : un aperçu des dernières lignes de chaque source.
func (a *Agent) startLogs(ctx context.Context, once bool) <-chan struct{} {
	done := make(chan struct{})
	lc := a.cfg.Logs
	if !lc.Enabled {
		a.status.Set("logs", "disabled")
		if a.cfg.Syslog.Enabled {
			a.status.Set("syslog", "erreur : logs.enabled est faux, les lignes reçues n'auraient nulle part où aller")
		}
		close(done)
		return done
	}
	a.status.Set("logs", "ok")
	pipe := logs.NewPipeline(lc.ExcludePatterns, a.tags, func(batch []model.Log, onDurable func()) {
		a.sender.Post(sender.RouteLogs, map[string]any{"logs": batch}, onDurable)
	}, a.log)
	pipe.SetObserver(a.integr.ObserveLog)
	pctx, stopPipe := context.WithCancel(context.Background())
	go func() {
		pipe.Run(pctx)
		close(done)
	}()
	setter := func(name string) func(string) { return func(s string) { a.status.Set(name, s) } }

	var sources sync.WaitGroup
	start := func(run func(context.Context), oneShot func(context.Context) error, flush func()) {
		a.mu.Lock()
		a.flushers = append(a.flushers, flush)
		a.mu.Unlock()
		sources.Add(1)
		go func() {
			defer sources.Done()
			if once {
				if err := oneShot(ctx); err != nil {
					a.collectorError("logs", err)
				}
				return
			}
			run(ctx)
		}()
	}

	switch {
	case runtime.GOOS != "linux":
	case !lc.Journald:
		a.status.Set("journald", "disabled")
	case !logs.JournaldAvailable():
		a.status.Set("journald", "absent")
	default:
		j := logs.NewJournald(pipe, a.store, a.log, setter("journald"))
		start(j.Run, j.Once, j.FlushState)
	}
	if len(lc.Files) > 0 {
		f := logs.NewFiles(lc.Files, pipe, a.store, a.log, setter("files"))
		a.status.Set("files", "ok")
		start(f.Run, func(c context.Context) error { f.Once(c); return nil }, f.FlushState)
	}
	if runtime.GOOS == "windows" {
		// Vague 3 : canaux des rôles présents (Directory Service, DNS Server…) ajoutés à la conf.
		channels := logs.EventlogChannels(lc.WindowsEventlog, a.cfg.Integrations, a.log)
		if len(channels) == 0 || !logs.EventlogAvailable() {
			a.status.Set("eventlog", "disabled")
		} else {
			e := logs.NewEventlog(channels, pipe, a.store, a.log, setter("eventlog"))
			start(e.Run, e.Once, e.FlushState)
		}
	}
	if a.cfg.Syslog.Enabled && !once {
		a.startSyslog(pipe, start)
	}
	go func() {
		sources.Wait()
		stopPipe() // sources arrêtées : la file envoie ce qu'elle tient encore, puis s'arrête
	}()
	return done
}

// flushStates écrit les curseurs de journaux une dernière fois, après que l'expéditeur a
// confirmé les derniers lots.
func (a *Agent) flushStates() {
	a.mu.Lock()
	fs := a.flushers
	a.mu.Unlock()
	for _, f := range fs {
		f()
	}
}

// statusFile : contenu de status.json.
type statusFile struct {
	PID           int                 `json:"pid"`
	Version       string              `json:"version"`
	AgentID       string              `json:"agent_id"`
	Hostname      string              `json:"hostname"`
	URL           string              `json:"url"`
	Started       time.Time           `json:"started"`
	Updated       time.Time           `json:"updated"`
	LastSendOK    *time.Time          `json:"last_send_ok,omitempty"`
	LastError     string              `json:"last_error,omitempty"`
	BufferedCount int                 `json:"buffered_count"`
	BufferedBytes int64               `json:"buffered_bytes"`
	Collectors    map[string]string   `json:"collectors"`
	Errors        []string            `json:"errors"`
	Update        *model.UpdateStatus `json:"update,omitempty"`
	ConfigFiles   []string            `json:"config_files,omitempty"`
}

// ReadStatusFile lit status.json (pour `hypha-agent status`).
func ReadStatusFile(dir string) (map[string]any, error) {
	var v map[string]any
	err := (&state.Store{Dir: dir}).ReadJSON(StatusFile, &v)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%s absent : l'agent n'a jamais tourné en service sur cet hôte", StatusFile)
	}
	return v, err
}

func (a *Agent) writeStatusFile() {
	a.mu.Lock()
	st := a.lastStats
	a.mu.Unlock()
	snap := a.status.Snapshot()
	sf := statusFile{
		PID: os.Getpid(), Version: a.opt.Version, AgentID: a.agentID, Hostname: a.hostname, URL: a.cfg.URL,
		Started: a.started, Updated: time.Now(), LastError: st.LastError,
		BufferedCount: st.BufferedCount, BufferedBytes: st.BufferedBytes,
		Collectors: snap.Collectors, Errors: snap.Errors, ConfigFiles: a.cfg.Files,
	}
	us := a.updater.Status()
	sf.Update = &us
	if !st.LastOK.IsZero() {
		t := st.LastOK
		sf.LastSendOK = &t
	}
	if err := a.store.WriteJSON(StatusFile, sf); err != nil && a.limiter.Allow("statusfile") {
		a.log.Warn("état de l'agent non écrit", "err", err)
	}
}
