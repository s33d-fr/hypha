// hypha-agent : agent de collecte HYPHA (docs/CONTRAT.md § 3 à 5).
//
//	hypha-agent run [--config FICHIER] [--dry-run] [--once] [--debug]
//	hypha-agent status [--config FICHIER]
//	hypha-agent check-config [--config FICHIER]
//	hypha-agent version
//	hypha-agent install|uninstall|start|stop     (Windows : service « Hypha Agent »)
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"s33d.fr/hypha/agent/internal/agent"
	"s33d.fr/hypha/agent/internal/agentlog"
	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/integrations"
	"s33d.fr/hypha/agent/internal/update"
	"s33d.fr/hypha/agent/internal/winsvc"
)

// version est fixée à la compilation : -ldflags "-X main.version=0.1.0".
var version = "0.0.0-dev"

// restartHelper : sous-commande cachée de relance du service Windows (update.Relaunch).
const restartHelper = "__restart-service"

func main() {
	if winsvc.IsService() {
		// Lancé par le gestionnaire de services Windows : pas de console, journal dans un fichier.
		if err := winsvc.RunService(runService); err != nil {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var code int
	switch cmd {
	case "run":
		code = cmdRun(args)
	case "status":
		code = cmdStatus(args)
	case "check-config":
		code = cmdCheckConfig(args)
	case "version", "--version", "-v":
		fmt.Printf("hypha-agent %s (%s/%s, %s)\n", version, runtime.GOOS, runtime.GOARCH, runtime.Version())
	case "install":
		code = cmdInstall(args)
	case "uninstall":
		code = report("désinstallation du service", winsvc.Uninstall())
	case "start":
		code = report("démarrage du service", winsvc.Start())
	case "stop":
		code = report("arrêt du service", winsvc.Stop())
	case "apply-update":
		// Lancée par l'unité systemd avant l'agent (ExecStartPre=-+…) : pose un binaire
		// préparé et vérifié par l'agent, après nouvelle vérification de sa signature.
		if err := update.ApplyStaged(config.StateDir()); err != nil {
			fmt.Fprintf(os.Stderr, "mise à jour non posée : %v\n", err)
		}
	case restartHelper:
		// Processus détaché lancé par le service Windows qui vient de poser une nouvelle version.
		if err := winsvc.RestartWhenStopped(60 * time.Second); err != nil {
			fmt.Fprintln(os.Stderr, err)
			code = 1
		}
	case integrations.DialChildCommand:
		// Sous-processus lancé par l'intégration PostgreSQL sous l'identité de l'utilisateur
		// de la base (authentification peer) ; jamais appelé à la main.
		if len(args) != 1 {
			code = 2
		} else if err := integrations.RunDialChild(args[0]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			code = 1
		}
	case "help", "-h", "--help":
		usage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "commande inconnue : %s\n\n", cmd)
		usage(os.Stderr)
		code = 2
	}
	os.Exit(code)
}

func usage(w io.Writer) {
	fmt.Fprintf(w, `hypha-agent %s : agent de collecte HYPHA

Commandes :
  run [--config F] [--dry-run] [--once] [--debug]
                     collecte au premier plan ; --dry-run affiche les corps JSON au lieu
                     de les envoyer (et n'écrit rien sur disque) ; --once fait un seul cycle
  status [--config F]        état de l'agent installé
  check-config [--config F]  vérifie la configuration
  version                    version de l'agent
  install | uninstall | start | stop
                     service Windows « Hypha Agent » (sous Linux : systemctl)
  apply-update               pose une mise à jour préparée et vérifiée (lancée par l'unité systemd)

Configuration par défaut : %s, puis conf.d/*.yaml du même dossier
(ordre alphabétique, fusion par clé).
Variables d'environnement : HYPHA_API_KEY et HYPHA_URL l'emportent sur les fichiers.
`, version, config.DefaultPath())
}

func report(what string, err error) int {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s : %v\n", what, err)
		return 1
	}
	fmt.Printf("%s : fait\n", what)
	return 0
}

// loadConfig lit et valide la conf ; les erreurs sont écrites sur stderr.
func loadConfig(path string, requireKey bool) (*config.Config, bool) {
	cfg, err := config.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration : %v\n", err)
		return nil, false
	}
	errs := cfg.Validate(requireKey)
	for _, w := range cfg.Warnings {
		fmt.Fprintf(os.Stderr, "avertissement : %s\n", w)
	}
	for _, e := range errs {
		fmt.Fprintf(os.Stderr, "erreur : %s\n", e)
	}
	return cfg, len(errs) == 0
}

func cmdRun(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	cfgPath := fs.String("config", config.DefaultPath(), "fichier de configuration")
	dry := fs.Bool("dry-run", false, "afficher les corps JSON au lieu de les envoyer")
	once := fs.Bool("once", false, "un seul cycle de collecte, puis sortie")
	debug := fs.Bool("debug", false, "journal détaillé")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, ok := loadConfig(*cfgPath, !*dry)
	if !ok {
		return 1
	}
	logger := agentlog.New(os.Stderr, *debug)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, err := agent.New(cfg, agent.Options{Version: version, DryRun: *dry, Once: *once, Logger: logger, Out: os.Stdout})
	if err != nil {
		logger.Error("démarrage impossible", "err", err)
		return 1
	}
	if err := a.Run(ctx); errors.Is(err, agent.ErrRestart) {
		stop()
		return update.Relaunch()
	} else if err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("arrêt sur erreur", "err", err)
		return 1
	}
	return 0
}

// runService est le corps du service Windows.
func runService(ctx context.Context) error {
	cfgPath := config.DefaultPath()
	for i, a := range os.Args {
		if (a == "--config" || a == "-config") && i+1 < len(os.Args) {
			cfgPath = os.Args[i+1]
		} else if v, ok := strings.CutPrefix(a, "--config="); ok {
			cfgPath = v
		}
	}
	dir := config.StateDir()
	_ = os.MkdirAll(dir, 0o700)
	var w io.Writer = os.Stderr
	if rf, err := agentlog.OpenRotating(filepath.Join(dir, "agent.log"), 10<<20); err == nil {
		defer rf.Close()
		w = rf
	}
	logger := agentlog.New(w, false)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		logger.Error("configuration illisible", "fichier", cfgPath, "err", err)
		return err
	}
	if errs := cfg.Validate(true); len(errs) > 0 {
		for _, e := range errs {
			logger.Error("configuration invalide", "fichier", cfgPath, "err", e)
		}
		return errors.New("configuration invalide")
	}
	for _, wmsg := range cfg.Warnings {
		logger.Warn("configuration", "avertissement", wmsg)
	}
	a, err := agent.New(cfg, agent.Options{Version: version, Logger: logger, Out: io.Discard})
	if err != nil {
		logger.Error("démarrage impossible", "err", err)
		return err
	}
	err = a.Run(ctx)
	if errors.Is(err, agent.ErrRestart) {
		// Nouvelle version posée : une aide détachée relance le service une fois arrêté ; à
		// défaut, sortie en erreur pour que la relance sur échec du service prenne le relais.
		if update.Relaunch() != 0 {
			logger.Error("aide de relance non lancée : relance par le gestionnaire de services")
			return err
		}
		return nil
	}
	return err
}

func cmdInstall(args []string) int {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	cfgPath := fs.String("config", config.DefaultPath(), "fichier de configuration du service")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	exe, err := os.Executable()
	if err != nil {
		return report("installation du service", err)
	}
	if exe, err = filepath.Abs(exe); err != nil {
		return report("installation du service", err)
	}
	return report("installation du service "+winsvc.Name, winsvc.Install(exe, "run", "--config", *cfgPath))
}

func cmdCheckConfig(args []string) int {
	fs := flag.NewFlagSet("check-config", flag.ContinueOnError)
	cfgPath := fs.String("config", config.DefaultPath(), "fichier de configuration")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, ok := loadConfig(*cfgPath, true)
	if cfg == nil {
		return 1
	}
	fmt.Printf("Fichier        : %s\n", cfg.Path)
	if len(cfg.Files) > 1 {
		fmt.Printf("Fusionnés      : %s\n", strings.Join(cfg.Files[1:], ", "))
	}
	fmt.Printf("Serveur        : %s\n", cfg.URL)
	fmt.Printf("Clé            : %s\n", cfg.MaskedKey())
	if len(cfg.FromEnv) > 0 {
		fmt.Printf("Environnement  : %s\n", strings.Join(cfg.FromEnv, ", "))
	}
	host := cfg.Hostname
	if host == "" {
		h, _ := os.Hostname()
		host = strings.ToLower(h) + " (nom système)"
	}
	fmt.Printf("Hôte           : %s\n", host)
	keys := make([]string, 0, len(cfg.Tags))
	for k := range cfg.Tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var tags []string
	for _, k := range keys {
		tags = append(tags, k+":"+cfg.Tags[k])
	}
	fmt.Printf("Étiquettes     : %s\n", strings.Join(tags, ", "))
	fmt.Printf("Intervalle     : %s\n", cfg.Interval.D())
	fmt.Printf("Tampon disque  : %s (%d Mo au plus)\n", cfg.Buffer.Dir, cfg.Buffer.MaxMB)
	fmt.Printf("Journaux       : actifs=%v journald=%v eventlog=%v fichiers=%d exclusions=%d\n",
		cfg.Logs.Enabled, cfg.Logs.Journald, cfg.Logs.WindowsEventlog, len(cfg.Logs.Files), len(cfg.Logs.ExcludePatterns))
	fmt.Printf("Contrôles      : systemd=%d services Windows=%d http=%d tcp=%d\n",
		len(cfg.Checks.SystemdUnits), len(cfg.Checks.WindowsServices), len(cfg.Checks.HTTP), len(cfg.Checks.TCP))
	fmt.Printf("StatsD         : %v %s ; OTLP : %v %s\n", cfg.StatsD.Enabled, cfg.StatsD.Listen, cfg.OTLP.Enabled, cfg.OTLP.Listen)
	fmt.Printf("Conteneurs     : %s ; processus : %v ; réseau : %v\n", cfg.Containers.Enabled, cfg.Processes.Enabled, cfg.Network.Enabled)
	fmt.Printf("Contrôle       : canal=%v (%s) ; mise à jour automatique=%v\n", cfg.Control.Enabled, cfg.Control.Interval.D(), cfg.AutoUpdate)
	if cfg.Actions.Enabled {
		fmt.Printf("Actions        : actives, gestes %s, services %v, interdits %v, scripts %v, délai %s\n",
			strings.Join(cfg.Actions.Allow, ", "), cfg.Actions.Services, cfg.Actions.DenyServices,
			config.SortedScriptNames(cfg.Actions.Scripts), cfg.Actions.Timeout.D())
	} else {
		fmt.Printf("Actions        : coupées\n")
	}
	fmt.Printf("Synthétiques   : %v ; inventaire : %v (%s) ; SNMP : %v, %d équipement(s), traps %v\n",
		cfg.Synthetics.Enabled, cfg.Inventory.Enabled, cfg.Inventory.Interval.D(), cfg.SNMP.Enabled, len(cfg.SNMP.Devices), cfg.SNMP.Traps.Enabled)
	fmt.Printf("Contrôles 0.4  : processus=%d\n", len(cfg.Checks.Processes))
	if y, err := cfg.RedactedYAML(); err == nil {
		fmt.Printf("\nConf fusionnée (secrets masqués) :\n%s", y)
	}
	if !ok {
		fmt.Println("\nConfiguration INVALIDE (détail ci-dessus, sur la sortie d'erreur).")
		return 1
	}
	fmt.Println("\nConfiguration valide.")
	return 0
}

func cmdStatus(args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	cfgPath := fs.String("config", config.DefaultPath(), "fichier de configuration")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	fmt.Printf("hypha-agent %s\n", version)
	svcState := winsvc.State()
	fmt.Printf("Service        : %s\n", svcState)
	if cfg, err := config.Load(*cfgPath); err == nil {
		fmt.Printf("Configuration  : %s (serveur %s, clé %s)\n", cfg.Path, cfg.URL, cfg.MaskedKey())
		if n, b := bufferUsage(cfg.Buffer.Dir); n > 0 {
			fmt.Printf("Tampon disque  : %d envoi(s) en attente, %.1f Mo\n", n, float64(b)/(1<<20))
		} else {
			fmt.Printf("Tampon disque  : vide\n")
		}
	} else {
		fmt.Printf("Configuration  : %v\n", err)
	}
	st, err := agent.ReadStatusFile(config.StateDir())
	if err != nil {
		fmt.Printf("État           : %v\n", err)
		return 1
	}
	if v, ok := st["agent_id"].(string); ok {
		fmt.Printf("Identifiant    : %s\n", v)
	}
	fresh := false
	if v, ok := st["updated"].(string); ok {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			fresh = time.Since(t) < 3*time.Minute
			fmt.Printf("État écrit le  : %s (il y a %s)\n", t.Local().Format("02/01/2006 15:04:05"), time.Since(t).Round(time.Second))
		}
	}
	if v, ok := st["last_send_ok"].(string); ok {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			fmt.Printf("Dernier envoi  : %s (il y a %s)\n", t.Local().Format("02/01/2006 15:04:05"), time.Since(t).Round(time.Second))
		}
	} else {
		fmt.Printf("Dernier envoi  : aucun envoi réussi\n")
	}
	if v, ok := st["last_error"].(string); ok && v != "" {
		fmt.Printf("Dernière erreur: %s\n", v)
	}
	if c, ok := st["collectors"].(map[string]any); ok {
		fmt.Println("Collecteurs    :")
		keys := make([]string, 0, len(c))
		for k := range c {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("  %-10s %v\n", k, c[k])
		}
	}
	if u, ok := st["update"].(map[string]any); ok {
		line := fmt.Sprint(u["state"])
		if to, _ := u["to"].(string); to != "" {
			line += fmt.Sprintf(" (%v → %s)", u["from"], to)
		}
		if e, _ := u["error"].(string); e != "" {
			line += " : " + e
		}
		fmt.Printf("Mise à jour    : %s\n", line)
	}
	if f, ok := st["config_files"].([]any); ok && len(f) > 1 {
		fmt.Printf("Conf lue       : %v\n", f)
	}
	if e, ok := st["errors"].([]any); ok && len(e) > 0 {
		fmt.Println("Erreurs récentes :")
		for _, x := range e {
			fmt.Printf("  %v\n", x)
		}
	}
	if !fresh || svcState != "actif" {
		return 1
	}
	return 0
}

// bufferUsage compte les envois en attente dans le tampon disque.
func bufferUsage(dir string) (int, int64) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0
	}
	var n int
	var size int64
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".gz") {
			if info, err := e.Info(); err == nil {
				n++
				size += info.Size()
			}
		}
	}
	return n, size
}
