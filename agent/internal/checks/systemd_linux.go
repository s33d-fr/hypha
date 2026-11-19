//go:build linux

package checks

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"sort"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// systemdWatcher : toutes les unités en échec sont signalées (CRIT), plus les unités
// suivies (checks.systemd_units), quel que soit leur état.
type systemdWatcher struct {
	monitored  []string
	log        *slog.Logger
	prev       map[string]string // unité suivie → ActiveState au cycle précédent
	failedPrev map[string]bool   // unités non suivies signalées en échec au cycle précédent
	started    bool
}

func newServiceWatcher(cfg config.ChecksConfig, log *slog.Logger) serviceWatcher {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return nil // pas de systemd (conteneur, init ancien) : rien à surveiller
	}
	w := &systemdWatcher{log: log, prev: map[string]string{}, failedPrev: map[string]bool{}}
	for _, u := range cfg.SystemdUnits {
		w.monitored = append(w.monitored, unitName(u))
	}
	return w
}

// unitName complète un nom sans suffixe (« nginx » → « nginx.service »), comme systemctl.
func unitName(u string) string {
	u = strings.TrimSpace(u)
	if !strings.Contains(u, ".") {
		return u + ".service"
	}
	return u
}

type unitProps struct {
	ID, LoadState, ActiveState, SubState, Result, Type string
}

func (w *systemdWatcher) collect(ctx context.Context, now time.Time, tags model.TagFunc) ([]model.Check, []model.Event, error) {
	failed, err := listFailed(ctx)
	if err != nil {
		return nil, nil, err
	}
	isMonitored := map[string]bool{}
	for _, u := range w.monitored {
		isMonitored[u] = true
	}
	failedNow := map[string]bool{}
	for _, u := range failed {
		if !isMonitored[u] {
			failedNow[u] = true
		}
	}
	// Une seule commande `systemctl show` pour toutes les unités à décrire : suivies,
	// en échec, et celles qui étaient en échec au cycle précédent (pour leur retour à OK).
	query := append([]string{}, w.monitored...)
	for u := range failedNow {
		query = append(query, u)
	}
	for u := range w.failedPrev {
		if !failedNow[u] {
			query = append(query, u)
		}
	}
	props, err := show(ctx, query)
	if err != nil {
		return nil, nil, err
	}

	ts := model.Ms(now)
	var checks []model.Check
	var events []model.Event
	for _, u := range query {
		p, ok := props[u]
		if !ok {
			continue
		}
		monitored := isMonitored[u]
		st, msg := unitStatus(p, monitored)
		checks = append(checks, model.Check{TS: ts, Name: "systemd.unit", Status: st, Message: msg, Tags: tags("unit", u)})

		key := strings.TrimSuffix(u, ".service")
		switch {
		case monitored:
			if old, seen := w.prev[u]; seen && old != p.ActiveState && w.started {
				events = append(events, stateEvent(now, "systemd", key, old, p.ActiveState, msg, alertFor(p.ActiveState), tags("unit", u)))
			}
			w.prev[u] = p.ActiveState
		case failedNow[u] && !w.failedPrev[u] && w.started:
			events = append(events, stateEvent(now, "systemd", key, "", "failed", msg, "error", tags("unit", u)))
		case !failedNow[u] && w.failedPrev[u]:
			events = append(events, stateEvent(now, "systemd", key, "failed", p.ActiveState, msg, alertFor(p.ActiveState), tags("unit", u)))
		}
	}
	w.failedPrev = failedNow
	w.started = true
	return checks, events, nil
}

// checkOne décrit une unité, suivie ou non (après une action à distance).
func (w *systemdWatcher) checkOne(ctx context.Context, now time.Time, unit string, tags model.TagFunc) []model.Check {
	unit = unitName(unit)
	props, err := show(ctx, []string{unit})
	if err != nil {
		return nil
	}
	p, ok := props[unit]
	if !ok {
		return nil
	}
	st, msg := unitStatus(p, true)
	if p.ActiveState == "inactive" {
		st = model.CheckOK // arrêtée à la demande (stop_service) : état voulu, pas une panne
	}
	return []model.Check{{TS: model.Ms(now), Name: "systemd.unit", Status: st, Message: msg, Tags: tags("unit", unit)}}
}

func alertFor(active string) string {
	switch active {
	case "failed":
		return "error"
	case "inactive":
		return "warning"
	case "active":
		return "success"
	}
	return "info"
}

// unitStatus : active → OK ; en transition → WARN ; failed → CRIT ; inactive → CRIT pour
// une unité suivie (elle devrait tourner), sauf tâche ponctuelle (oneshot) terminée sans
// erreur ; unité inconnue → UNKNOWN.
func unitStatus(p unitProps, monitored bool) (int, string) {
	if p.LoadState == "not-found" {
		return model.CheckUnknown, "unité introuvable"
	}
	msg := fmt.Sprintf("%s (%s)", p.ActiveState, p.SubState)
	switch p.ActiveState {
	case "active":
		return model.CheckOK, msg
	case "activating", "deactivating", "reloading", "refreshing":
		return model.CheckWarn, msg
	case "failed":
		return model.CheckCrit, fmt.Sprintf("failed (Result: %s)", p.Result)
	case "inactive":
		if p.Type == "oneshot" && (p.Result == "success" || p.Result == "") {
			return model.CheckOK, msg + ", tâche ponctuelle terminée"
		}
		if !monitored {
			return model.CheckOK, msg // ancienne unité en échec, arrêtée proprement depuis
		}
		return model.CheckCrit, msg
	}
	return model.CheckUnknown, msg
}

// listFailed rend les unités en échec : sortie JSON de systemctl si elle est connue
// (systemd ≥ 246 environ), sinon lecture de la sortie texte.
func listFailed(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", "list-units", "--state=failed", "--all", "--no-legend", "--no-pager", "--plain", "--output=json").Output()
	if err == nil {
		var rows []struct {
			Unit string `json:"unit"`
		}
		if json.Unmarshal(out, &rows) == nil {
			names := make([]string, 0, len(rows))
			for _, r := range rows {
				names = append(names, r.Unit)
			}
			sort.Strings(names)
			return names, nil
		}
	}
	out, err = exec.CommandContext(ctx, "systemctl", "list-units", "--state=failed", "--all", "--no-legend", "--no-pager", "--plain").Output()
	if err != nil {
		return nil, fmt.Errorf("systemctl list-units : %w", err)
	}
	var names []string
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) > 0 && (f[0] == "●" || f[0] == "*") {
			f = f[1:]
		}
		if len(f) > 0 {
			names = append(names, f[0])
		}
	}
	sort.Strings(names)
	return names, nil
}

// show décrit les unités demandées ; les blocs de `systemctl show` sortent dans l'ordre
// des arguments, séparés par une ligne vide.
func show(ctx context.Context, units []string) (map[string]unitProps, error) {
	out := map[string]unitProps{}
	if len(units) == 0 {
		return out, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	args := append([]string{"show", "--no-pager", "--property=Id,LoadState,ActiveState,SubState,Result,Type", "--"}, units...)
	data, err := exec.CommandContext(ctx, "systemctl", args...).Output()
	if err != nil && len(data) == 0 {
		return nil, fmt.Errorf("systemctl show : %w", err)
	}
	blocks := strings.Split(strings.TrimSpace(string(data)), "\n\n")
	for i, b := range blocks {
		if i >= len(units) {
			break
		}
		var p unitProps
		for _, line := range strings.Split(b, "\n") {
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			switch k {
			case "Id":
				p.ID = v
			case "LoadState":
				p.LoadState = v
			case "ActiveState":
				p.ActiveState = v
			case "SubState":
				p.SubState = v
			case "Result":
				p.Result = v
			case "Type":
				p.Type = v
			}
		}
		out[units[i]] = p
	}
	return out, nil
}
