package integrations

import (
	"context"
	"fmt"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/host"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// SystemdTimers : contrôle systemd.timer de chaque timer actif. CRIT si le dernier passage
// de l'unité liée a échoué ; WARN si le timer n'a pas tourné depuis deux périodes (période
// lue dans OnUnitActiveSec/OnUnitInactiveSec, ou calculée par systemd-analyze calendar).
type SystemdTimers struct {
	cfg        config.IntegrationToggle
	det        detector
	periods    map[string]time.Duration // spécification OnCalendar → période
	due        map[string]time.Time     // « spécification|dernier passage » → 2e passage prévu
	noBaseTime bool                     // systemd-analyze sans --base-time (systemd < 253)
}

func newSystemdTimers(cfg config.IntegrationToggle) *SystemdTimers {
	return &SystemdTimers{cfg: cfg, det: detector{mode: cfg.Enabled}, periods: map[string]time.Duration{},
		due: map[string]time.Time{}}
}

func (s *SystemdTimers) name() string          { return "systemd_timers" }
func (s *SystemdTimers) close()                {}
func (s *SystemdTimers) period() time.Duration { return 60 * time.Second }

type timerProps struct {
	id, active, unit         string
	lastTrigger              int64 // µs depuis l'époque, 0 = jamais
	calendar, monotonic      string
	unitResult, unitActive   string
}

// parseShowBlocks lit la sortie de « systemctl show » de plusieurs unités (blocs séparés par
// une ligne vide, dans l'ordre des arguments).
func parseShowBlocks(text string) []map[string]string {
	var out []map[string]string
	for _, blk := range strings.Split(strings.TrimSpace(text), "\n\n") {
		m := map[string]string{}
		for _, l := range strings.Split(blk, "\n") {
			if k, v, ok := strings.Cut(l, "="); ok {
				m[k] = v
			}
		}
		out = append(out, m)
	}
	return out
}

var spanPartRe = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*(us|µs|ms|seconds|second|sec|s|minutes|minute|min|m|hours|hour|hr|h|days|day|d|weeks|week|w|months|month|M|years|year|y)\b`)

// parseSystemdSpan lit une durée systemd : « 1d », « 1h 30min », « 15min », « 1w ».
func parseSystemdSpan(s string) time.Duration {
	var d time.Duration
	for _, m := range spanPartRe.FindAllStringSubmatch(s, -1) {
		v, _ := strconv.ParseFloat(m[1], 64)
		unit := map[string]time.Duration{
			"us": time.Microsecond, "µs": time.Microsecond, "ms": time.Millisecond,
			"s": time.Second, "sec": time.Second, "second": time.Second, "seconds": time.Second,
			"m": time.Minute, "min": time.Minute, "minute": time.Minute, "minutes": time.Minute,
			"h": time.Hour, "hr": time.Hour, "hour": time.Hour, "hours": time.Hour,
			"d": 24 * time.Hour, "day": 24 * time.Hour, "days": 24 * time.Hour,
			"w": 7 * 24 * time.Hour, "week": 7 * 24 * time.Hour, "weeks": 7 * 24 * time.Hour,
			"M": 2629800 * time.Second, "month": 2629800 * time.Second, "months": 2629800 * time.Second,
			"y": 31557600 * time.Second, "year": 31557600 * time.Second, "years": 31557600 * time.Second,
		}[m[2]]
		d += time.Duration(v * float64(unit))
	}
	return d
}

var monoRe = regexp.MustCompile(`OnUnit(?:In)?ActiveUSec=([^;]+);`)
var calRe = regexp.MustCompile(`OnCalendar=([^;]+);`)

// monotonicPeriod : période d'un timer OnUnitActiveSec / OnUnitInactiveSec.
func monotonicPeriod(prop string) time.Duration {
	if m := monoRe.FindStringSubmatch(prop); m != nil {
		return parseSystemdSpan(strings.TrimSpace(m[1]))
	}
	return 0
}

func calendarSpec(prop string) string {
	if m := calRe.FindStringSubmatch(prop); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}

var nextElapseRe = regexp.MustCompile(`(?m)^\s*(?:Next elapse|Iter(?:ation|\.) #\d+):\s*\S+\s+(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})`)

// calendarPeriodOf lit « systemd-analyze calendar --iterations=2 » (lignes « Next elapse: »
// et « Iteration #2: », ou « Iter. #2: » sur un systemd ancien ; heure locale) : écart entre
// les deux prochains passages.
func calendarPeriodOf(text string) time.Duration {
	m := nextElapseRe.FindAllStringSubmatch(text, -1)
	if len(m) < 2 {
		return 0
	}
	a, err1 := time.Parse(time.DateTime, m[0][1])
	b, err2 := time.Parse(time.DateTime, m[1][1])
	if err1 != nil || err2 != nil || !b.After(a) {
		return 0
	}
	return b.Sub(a)
}

// timerCheck décide de l'état d'un timer.
func timerCheck(t timerProps, period time.Duration, now time.Time) (int, string) {
	switch t.unitResult {
	case "", "success":
	default:
		return model.CheckCrit, fmt.Sprintf("dernier passage de %s en échec (Result: %s)", t.unit, t.unitResult)
	}
	if t.unitActive == "failed" {
		return model.CheckCrit, fmt.Sprintf("%s en échec", t.unit)
	}
	if t.lastTrigger == 0 {
		return model.CheckOK, "pas encore déclenché"
	}
	last := time.UnixMicro(t.lastTrigger)
	ago := now.Sub(last)
	msg := fmt.Sprintf("dernier passage il y a %s", ago.Round(time.Minute))
	if period > 0 {
		msg += fmt.Sprintf(" (période %s)", period)
		if ago > 2*period+10*time.Minute {
			return model.CheckWarn, "n'a pas tourné depuis deux périodes : " + msg
		}
	}
	return model.CheckOK, msg
}

func (s *SystemdTimers) collect(ctx context.Context, now time.Time, _ *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if runtime.GOOS != "linux" {
		return nil, "absent"
	}
	if skip, st := s.det.skip(now); skip {
		return nil, st
	}
	sc, ok := findTool("systemctl")
	if !ok {
		_, st := s.det.failed("systemctl introuvable")
		return nil, st
	}
	out, err := runTool(ctx, 10*time.Second, sc, "list-units", "--type=timer", "--state=active", "--no-legend", "--no-pager", "--plain")
	if err != nil {
		_, st := s.det.failed(err.Error())
		return nil, st
	}
	var timers []string
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Fields(l)
		if len(f) > 0 && (f[0] == "●" || f[0] == "*") {
			f = f[1:]
		}
		if len(f) > 0 && strings.HasSuffix(f[0], ".timer") {
			timers = append(timers, f[0])
		}
	}
	if len(timers) == 0 {
		_, st := s.det.failed("aucun timer actif")
		return nil, st
	}
	s.det.ok()
	// Dernier passage : LastTriggerUSec en secondes Unix (--timestamp=unix, systemd ≥ 251) ;
	// sinon, durée monotone depuis le démarrage (« 22h 43min 13.2s ») ajoutée à la date de
	// démarrage.
	props := []string{"Id", "ActiveState", "Unit", "LastTriggerUSec", "LastTriggerUSecMonotonic", "TimersCalendar", "TimersMonotonic"}
	base := []string{"show", "--no-pager", "--property=" + strings.Join(props, ",")}
	tout, err := runTool(ctx, 10*time.Second, sc, append(append(append([]string{}, base...), "--timestamp=unix", "--"), timers...)...)
	if err != nil {
		tout, err = runTool(ctx, 10*time.Second, sc, append(append(base, "--"), timers...)...)
	}
	if err != nil {
		return []model.Check{canConnect("systemd_timers", model.Ms(now), false, err.Error(), tags())}, "erreur : " + err.Error()
	}
	boot, _ := host.BootTimeWithContext(ctx)
	var list []timerProps
	var units []string
	for _, m := range parseShowBlocks(string(tout)) {
		t := timerProps{id: m["Id"], active: m["ActiveState"], unit: m["Unit"], calendar: m["TimersCalendar"], monotonic: m["TimersMonotonic"]}
		t.lastTrigger = lastTrigger(m["LastTriggerUSec"], m["LastTriggerUSecMonotonic"], boot)
		list = append(list, t)
		units = append(units, t.unit)
	}
	uout, err := runTool(ctx, 10*time.Second, sc, append([]string{"show", "--no-pager", "--property=Id,Result,ActiveState", "--"}, units...)...)
	if err == nil {
		for i, m := range parseShowBlocks(string(uout)) {
			if i < len(list) {
				list[i].unitResult, list[i].unitActive = m["Result"], m["ActiveState"]
			}
		}
	}
	ts := model.Ms(now)
	var checks []model.Check
	for _, t := range list {
		if t.active != "active" || t.id == "" {
			continue
		}
		var st int
		var msg string
		if period := monotonicPeriod(t.monotonic); period > 0 {
			st, msg = timerCheck(t, period, now)
		} else {
			// Calendrier : un timer peut se taire des heures en toute normalité
			// (anacron : 07:30 à 23:30). Le retard se juge sur les passages prévus.
			spec := calendarSpec(t.calendar)
			if due := s.secondDue(ctx, spec, t.lastTrigger); !due.IsZero() {
				st, msg = timerCheckDue(t, due, now)
			} else {
				st, msg = timerCheck(t, calendarFallback(s.calendarPeriod(ctx, spec)), now)
			}
		}
		checks = append(checks, model.Check{TS: ts, Name: "systemd.timer", Status: st, Message: msg, Tags: tags("timer", t.id, "unit", t.unit)})
	}
	checks = append(checks, canConnect("systemd_timers", ts, true, fmt.Sprintf("%d timer(s) actif(s)", len(checks)), tags()))
	return checks, "ok"
}

// lastTrigger rend le dernier passage en µs Unix (0 = jamais) : « @1794957240 », sinon la
// durée monotone depuis le démarrage.
func lastTrigger(real, mono string, boot uint64) int64 {
	real, mono = strings.TrimSpace(real), strings.TrimSpace(mono)
	if strings.HasPrefix(real, "@") {
		if n, err := strconv.ParseInt(real[1:], 10, 64); err == nil && n > 0 {
			return n * 1_000_000
		}
		return 0
	}
	if mono == "" || mono == "0" || mono == "n/a" || boot == 0 {
		return 0
	}
	if n, err := strconv.ParseInt(mono, 10, 64); err == nil {
		return int64(boot)*1_000_000 + n
	}
	if d := parseSystemdSpan(mono); d > 0 {
		return int64(boot)*1_000_000 + d.Microseconds()
	}
	return 0
}

// timerCheckDue : timer à calendrier dont le deuxième passage prévu après le dernier
// déclenchement est connu ; WARN dix minutes après ce passage s'il n'a pas eu lieu.
func timerCheckDue(t timerProps, due time.Time, now time.Time) (int, string) {
	st, msg := timerCheck(t, 0, now)
	if st != model.CheckOK || t.lastTrigger == 0 {
		return st, msg
	}
	if now.After(due.Add(10 * time.Minute)) {
		return model.CheckWarn, fmt.Sprintf("n'a pas tourné depuis deux passages prévus : %s, deuxième passage attendu le %s",
			msg, due.Local().Format("02/01 15:04"))
	}
	return model.CheckOK, msg
}

// calendarFallback : sans calcul des passages prévus, une période de calendrier ne fait juger
// un retard qu'au-delà de 12 h, pour ne pas prendre une plage horaire (nuit, week-end) pour une panne.
func calendarFallback(p time.Duration) time.Duration {
	if p > 0 && p < 12*time.Hour {
		return 12 * time.Hour
	}
	return p
}

var utcElapseRe = regexp.MustCompile(`(?m)^\s*\(in UTC\):\s*\S+\s+(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}) UTC`)

// secondElapseUTC lit le deuxième passage de « systemd-analyze calendar --iterations=2 »
// dans ses lignes « (in UTC): », indépendantes du fuseau du processus.
func secondElapseUTC(text string) time.Time {
	m := utcElapseRe.FindAllStringSubmatch(text, -1)
	if len(m) < 2 {
		return time.Time{}
	}
	t, err := time.ParseInLocation(time.DateTime, m[1][1], time.UTC)
	if err != nil {
		return time.Time{}
	}
	return t
}

// secondDue : deuxième passage prévu après le dernier déclenchement (systemd-analyze calendar
// --base-time, systemd ≥ 253), en cache par spécification et dernier passage. Zéro si inconnu.
func (s *SystemdTimers) secondDue(ctx context.Context, spec string, last int64) time.Time {
	if spec == "" || last <= 0 || s.noBaseTime {
		return time.Time{}
	}
	base := strconv.FormatInt(last/1_000_000, 10)
	key := spec + "|" + base
	if d, ok := s.due[key]; ok {
		return d
	}
	var d time.Time
	if sa, ok := findTool("systemd-analyze"); ok {
		out, err := runTool(ctx, 10*time.Second, sa, "calendar", "--iterations=2", "--base-time=@"+base, spec)
		if err == nil {
			d = secondElapseUTC(string(out))
		} else if strings.Contains(err.Error()+string(out), "base-time") {
			s.noBaseTime = true
		}
	}
	if len(s.due) > 512 {
		s.due = map[string]time.Time{}
	}
	s.due[key] = d
	return d
}

// calendarPeriod : période d'une spécification OnCalendar, calculée une fois (systemd-analyze).
func (s *SystemdTimers) calendarPeriod(ctx context.Context, spec string) time.Duration {
	if spec == "" {
		return 0
	}
	if p, ok := s.periods[spec]; ok {
		return p
	}
	var p time.Duration
	if sa, ok := findTool("systemd-analyze"); ok {
		if out, err := runTool(ctx, 10*time.Second, sa, "calendar", "--iterations=2", spec); err == nil {
			p = calendarPeriodOf(string(out))
		}
	}
	s.periods[spec] = p
	return p
}
