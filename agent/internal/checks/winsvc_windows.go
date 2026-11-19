//go:build windows

package checks

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc/mgr"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// Types de service « par utilisateur » (winsvc.h) : modèles et instances, lancés à
// l'ouverture de session, arrêtés sinon.
const (
	serviceUserService         = 0x40
	serviceUserServiceInstance = 0x80
)

type winService struct {
	name, display string
	state         uint32
	svcType       uint32
	exitCode      uint32
}

type startInfo struct {
	auto, trigger bool
	at            time.Time
}

// winWatcher : services suivis (checks.windows_services), plus tout service à démarrage
// automatique trouvé arrêté.
type winWatcher struct {
	monitored []string
	log       *slog.Logger
	prev      map[string]string // service suivi → état au cycle précédent
	autoPrev  map[string]bool   // services automatiques signalés arrêtés au cycle précédent
	starts    map[string]startInfo
	excludes  []*regexp.Regexp
	autoRule  bool // signaler les services automatiques arrêtés (selon l'édition de Windows)
	started   bool
}

func newServiceWatcher(cfg config.ChecksConfig, log *slog.Logger) serviceWatcher {
	// Règle des services automatiques arrêtés : utile sur un serveur, bruyante sur une
	// édition de poste de travail, où les logiciels de constructeur (mise à jour, audio,
	// éclairage) laissent des services automatiques arrêtés. ProductType : 1 = poste de
	// travail, 2 = contrôleur de domaine, 3 = serveur.
	autoRule := windows.RtlGetVersion().ProductType != 1
	switch cfg.WindowsAutoServices {
	case "always":
		autoRule = true
	case "never":
		autoRule = false
	}
	log.Info("services Windows", "automatiques_arrêtés_signalés", autoRule, "réglage", cfg.WindowsAutoServices)
	return &winWatcher{autoRule: autoRule, monitored: cfg.WindowsServices, log: log, excludes: compileExcludes(cfg.WindowsServicesExclude, log),
		prev: map[string]string{}, autoPrev: map[string]bool{}, starts: map[string]startInfo{}}
}

func stateName(s uint32) string {
	switch s {
	case windows.SERVICE_STOPPED:
		return "stopped"
	case windows.SERVICE_START_PENDING:
		return "start_pending"
	case windows.SERVICE_STOP_PENDING:
		return "stop_pending"
	case windows.SERVICE_RUNNING:
		return "running"
	case windows.SERVICE_CONTINUE_PENDING:
		return "continue_pending"
	case windows.SERVICE_PAUSE_PENDING:
		return "pause_pending"
	case windows.SERVICE_PAUSED:
		return "paused"
	}
	return fmt.Sprintf("état %d", s)
}

func (w *winWatcher) collect(ctx context.Context, now time.Time, tags model.TagFunc) ([]model.Check, []model.Event, error) {
	m, err := mgr.Connect()
	if err != nil {
		return nil, nil, fmt.Errorf("gestionnaire de services : %w", err)
	}
	defer m.Disconnect()
	all, err := enumServices(m.Handle)
	if err != nil {
		return nil, nil, err
	}
	byName := make(map[string]winService, len(all))
	for _, s := range all {
		byName[strings.ToLower(s.name)] = s
	}

	ts := model.Ms(now)
	var checks []model.Check
	var events []model.Event
	isMonitored := map[string]bool{}

	for _, want := range w.monitored {
		key := strings.ToLower(strings.TrimSpace(want))
		isMonitored[key] = true
		s, ok := byName[key]
		if !ok {
			// Nom affiché donné à la place du nom de service : on le retrouve aussi.
			for _, c := range all {
				if strings.EqualFold(c.display, want) {
					s, ok = c, true
					isMonitored[strings.ToLower(c.name)] = true
					break
				}
			}
		}
		if !ok {
			checks = append(checks, model.Check{TS: ts, Name: "windows.service", Status: model.CheckUnknown,
				Message: "service introuvable", Tags: tags("windows_service", want)})
			continue
		}
		state := stateName(s.state)
		st := model.CheckCrit
		switch s.state {
		case windows.SERVICE_RUNNING:
			st = model.CheckOK
		case windows.SERVICE_START_PENDING, windows.SERVICE_STOP_PENDING, windows.SERVICE_CONTINUE_PENDING, windows.SERVICE_PAUSE_PENDING:
			st = model.CheckWarn
		}
		msg := state
		if s.state == windows.SERVICE_STOPPED && s.exitCode != 0 {
			msg = fmt.Sprintf("%s (code de sortie %d)", state, s.exitCode)
		}
		checks = append(checks, model.Check{TS: ts, Name: "windows.service", Status: st, Message: msg, Tags: tags("windows_service", s.name)})
		if old, seen := w.prev[s.name]; seen && old != state && w.started {
			alert := "info"
			switch state {
			case "running":
				alert = "success"
			case "stopped", "paused":
				alert = "error"
			}
			events = append(events, stateEvent(now, "winsvc", s.name, old, state, s.display, alert, tags("windows_service", s.name)))
		}
		w.prev[s.name] = state
	}

	autoNow := map[string]bool{}
	for _, s := range all {
		if !w.autoRule {
			break
		}
		lname := strings.ToLower(s.name)
		if isMonitored[lname] || s.state != windows.SERVICE_STOPPED || s.svcType&(serviceUserService|serviceUserServiceInstance) != 0 {
			continue
		}
		if excludedService(s.name, w.excludes) {
			continue
		}
		info := w.startInfo(m, s.name, now)
		if !info.auto || info.trigger {
			continue
		}
		autoNow[s.name] = true
		msg := "arrêté alors que son démarrage est automatique"
		if s.exitCode != 0 {
			msg += fmt.Sprintf(" (code de sortie %d)", s.exitCode)
		}
		checks = append(checks, model.Check{TS: ts, Name: "windows.service", Status: model.CheckCrit, Message: msg, Tags: tags("windows_service", s.name)})
		if !w.autoPrev[s.name] && w.started {
			events = append(events, stateEvent(now, "winsvc", s.name, "", "stopped", s.display+" : "+msg, "error", tags("windows_service", s.name)))
		}
	}
	// Service automatique reparti : un dernier contrôle OK efface l'alerte côté serveur.
	for name := range w.autoPrev {
		if autoNow[name] {
			continue
		}
		if s, ok := byName[strings.ToLower(name)]; ok {
			checks = append(checks, model.Check{TS: ts, Name: "windows.service", Status: model.CheckOK, Message: stateName(s.state), Tags: tags("windows_service", name)})
			events = append(events, stateEvent(now, "winsvc", name, "stopped", stateName(s.state), s.display, "success", tags("windows_service", name)))
		}
	}
	w.autoPrev = autoNow
	w.started = true
	return checks, events, nil
}

// checkOne décrit un service (après une action à distance) ; un service arrêté à la
// demande est OK.
func (w *winWatcher) checkOne(_ context.Context, now time.Time, name string, tags model.TagFunc) []model.Check {
	m, err := mgr.Connect()
	if err != nil {
		return nil
	}
	defer m.Disconnect()
	all, err := enumServices(m.Handle)
	if err != nil {
		return nil
	}
	for _, s := range all {
		if !strings.EqualFold(s.name, name) && !strings.EqualFold(s.display, name) {
			continue
		}
		st := model.CheckOK
		switch s.state {
		case windows.SERVICE_START_PENDING, windows.SERVICE_STOP_PENDING, windows.SERVICE_CONTINUE_PENDING, windows.SERVICE_PAUSE_PENDING:
			st = model.CheckWarn
		case windows.SERVICE_PAUSED:
			st = model.CheckCrit
		}
		return []model.Check{{TS: model.Ms(now), Name: "windows.service", Status: st, Message: stateName(s.state), Tags: tags("windows_service", s.name)}}
	}
	return nil
}

// startInfo lit (et garde 10 minutes) le type de démarrage d'un service et la présence de
// déclencheurs : un service sur déclencheur s'arrête normalement quand il n'a plus rien à faire.
func (w *winWatcher) startInfo(m *mgr.Mgr, name string, now time.Time) startInfo {
	if si, ok := w.starts[name]; ok && now.Sub(si.at) < 10*time.Minute {
		return si
	}
	si := startInfo{at: now}
	// Droit de lecture de la configuration seulement : mgr.OpenService demande tous les
	// droits, que certains services protégés (antivirus) refusent même à SYSTEM.
	np, _ := windows.UTF16PtrFromString(name)
	h, err := windows.OpenService(m.Handle, np, windows.SERVICE_QUERY_CONFIG)
	if err != nil {
		w.starts[name] = si
		return si
	}
	s := &mgr.Service{Name: name, Handle: h}
	defer s.Close()
	if c, err := s.Config(); err == nil {
		si.auto = c.StartType == mgr.StartAutomatic
	}
	var needed uint32
	err = windows.QueryServiceConfig2(s.Handle, windows.SERVICE_CONFIG_TRIGGER_INFO, nil, 0, &needed)
	if errors.Is(err, syscall.ERROR_INSUFFICIENT_BUFFER) && needed >= 4 {
		buf := make([]byte, needed)
		if windows.QueryServiceConfig2(s.Handle, windows.SERVICE_CONFIG_TRIGGER_INFO, &buf[0], needed, &needed) == nil {
			// SERVICE_TRIGGER_INFO commence par cTriggers (DWORD).
			si.trigger = *(*uint32)(unsafe.Pointer(&buf[0])) > 0
		}
	}
	w.starts[name] = si
	return si
}

// enumServices liste tous les services Win32 avec leur état, en un seul appel
// (EnumServicesStatusEx), plutôt qu'une ouverture par service.
func enumServices(h windows.Handle) ([]winService, error) {
	var needed, returned uint32
	var buf []byte
	for {
		var p *byte
		if len(buf) > 0 {
			p = &buf[0]
		}
		err := windows.EnumServicesStatusEx(h, windows.SC_ENUM_PROCESS_INFO, windows.SERVICE_WIN32,
			windows.SERVICE_STATE_ALL, p, uint32(len(buf)), &needed, &returned, nil, nil)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.ERROR_MORE_DATA) || needed <= uint32(len(buf)) {
			return nil, fmt.Errorf("EnumServicesStatusEx : %w", err)
		}
		buf = make([]byte, needed)
	}
	if returned == 0 {
		return nil, nil
	}
	entries := unsafe.Slice((*windows.ENUM_SERVICE_STATUS_PROCESS)(unsafe.Pointer(&buf[0])), int(returned))
	out := make([]winService, 0, len(entries))
	for _, e := range entries {
		out = append(out, winService{
			name:     windows.UTF16PtrToString(e.ServiceName),
			display:  windows.UTF16PtrToString(e.DisplayName),
			state:    e.ServiceStatusProcess.CurrentState,
			svcType:  e.ServiceStatusProcess.ServiceType,
			exitCode: e.ServiceStatusProcess.Win32ExitCode,
		})
	}
	return out, nil
}
