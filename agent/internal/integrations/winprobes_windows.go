//go:build windows

package integrations

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"

	ole "github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc/mgr"

	"s33d.fr/hypha/agent/internal/config"
)

// windowsProbes : sondes Windows de la vague 3 (platform_windows.go).
func windowsProbes(cfg config.IntegrationsConfig) []integration {
	list := []integration{
		newEventlogCounter(),
		newActiveDirectory(cfg.ActiveDirectory, roleDeps("NTDS", adScript)),
		newWindowsDNS(cfg.WindowsDNS, roleDeps("DNS", dnsScript)),
	}
	for _, p := range cfg.PerfCounters {
		r := &pdhReader{}
		list = append(list, newPerfSet(p, r.read, r.close))
	}
	for _, w := range cfg.WMI {
		list = append(list, newWMISet(w, wmiQuery))
	}
	return list
}

// roleDeps : accès réels d'un rôle (service témoin, compteurs, services, script PowerShell).
func roleDeps(service, script string) winRoleDeps {
	r := &pdhReader{}
	return winRoleDeps{
		present:  func() bool { _, ok := serviceStates([]string{service})[service]; return ok },
		pdh:      r.read,
		closePDH: r.close,
		services: serviceStates,
		ps: func(ctx context.Context) ([]byte, error) {
			ps, ok := findTool("powershell")
			if !ok {
				return nil, errors.New("powershell introuvable")
			}
			return runTool(ctx, 90*time.Second, ps, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
		},
	}
}

// ── compteurs PDH, avec ou sans (*) ──────────────────────────────────────────────────────

type pdhReader struct {
	mu      sync.Mutex
	q       *pdhQuery
	key     string
	missing []string
}

func (r *pdhReader) close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.q.close()
	r.q = nil
}

// read ouvre la requête au premier relevé (compteurs absents notés, ignorés), puis relève.
func (r *pdhReader) read(paths []string) (pdhValues, []string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := strings.Join(paths, "\x00")
	if r.q != nil && r.key != key {
		r.q.close()
		r.q = nil
	}
	if r.q == nil {
		q, err := openPDHAny(paths)
		if err != nil {
			return nil, paths, err
		}
		r.q, r.key, r.missing = q, key, nil
		for _, p := range paths {
			if _, ok := q.counters[p]; !ok {
				r.missing = append(r.missing, p)
			}
		}
	}
	scalars, err := r.q.read()
	if err != nil && !errors.Is(err, errNoData) {
		r.q.close()
		r.q = nil
		return nil, nil, err
	}
	out := pdhValues{}
	for _, p := range paths {
		if _, ok := r.q.counters[p]; !ok {
			continue
		}
		if _, inst, _, _ := config.SplitCounterPath(p); inst == "*" {
			if v := r.q.instances(p); len(v) > 0 {
				out[p] = v
			}
		} else if v, ok := scalars[p]; ok {
			out[p] = map[string]float64{"": v}
		}
	}
	return out, r.missing, err
}

// ── services ─────────────────────────────────────────────────────────────────────────────

// serviceStates : état des services existants (les absents ne sont pas dans le résultat).
func serviceStates(names []string) map[string]svcState {
	out := map[string]svcState{}
	m, err := mgr.Connect()
	if err != nil {
		return out
	}
	defer m.Disconnect()
	for _, n := range names {
		s, err := m.OpenService(n)
		if err != nil {
			continue
		}
		st := svcState{State: "inconnu"}
		if q, err := s.Query(); err == nil {
			st.State = winStateName(uint32(q.State))
		}
		if c, err := s.Config(); err == nil {
			st.Disabled = c.StartType == windows.SERVICE_DISABLED
		}
		s.Close()
		out[n] = st
	}
	return out
}

func winStateName(s uint32) string {
	switch s {
	case windows.SERVICE_STOPPED:
		return "arrêté"
	case windows.SERVICE_START_PENDING:
		return "en cours de démarrage"
	case windows.SERVICE_STOP_PENDING:
		return "en cours d'arrêt"
	case windows.SERVICE_RUNNING:
		return "running"
	case windows.SERVICE_PAUSED:
		return "en pause"
	}
	return fmt.Sprintf("état %d", s)
}

// ── WMI par COM (SWbemLocator), en lecture seule ─────────────────────────────────────────

// wmiQuery exécute une requête WQL ; chaque appel a son fil système et son initialisation COM.
func wmiQuery(namespace, wql string, props []string) (rows []map[string]any, err error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("WMI : %v", p)
		}
	}()
	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
		var oe *ole.OleError
		if !errors.As(err, &oe) || oe.Code() != 1 { // S_FALSE : déjà initialisé
			return nil, err
		}
	}
	defer ole.CoUninitialize()
	unk, err := oleutil.CreateObject("WbemScripting.SWbemLocator")
	if err != nil {
		return nil, err
	}
	defer unk.Release()
	locator, err := unk.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return nil, err
	}
	defer locator.Release()
	sv, err := oleutil.CallMethod(locator, "ConnectServer", nil, namespace)
	if err != nil {
		return nil, fmt.Errorf("espace %s : %w", namespace, err)
	}
	services := sv.ToIDispatch()
	defer services.Release()
	rv, err := oleutil.CallMethod(services, "ExecQuery", wql)
	if err != nil {
		return nil, err
	}
	result := rv.ToIDispatch()
	defer result.Release()
	err = oleutil.ForEach(result, func(v *ole.VARIANT) error {
		defer v.Clear() // libère l'objet rendu par l'énumérateur
		item := v.ToIDispatch()
		row := map[string]any{}
		for _, p := range props {
			pv, err := oleutil.GetProperty(item, p)
			if err != nil {
				continue
			}
			row[p] = pv.Value()
			pv.Clear()
		}
		rows = append(rows, row)
		return nil
	})
	return rows, err
}
