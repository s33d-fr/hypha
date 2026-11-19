package integrations

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// NTP : décalage de l'horloge et synchronisation (chronyc, systemd-timesyncd, ntpq ; w32tm
// sous Windows).
type NTP struct {
	cfg config.IntegrationToggle
	det detector
}

func newNTP(cfg config.IntegrationToggle) *NTP { return &NTP{cfg: cfg, det: detector{mode: cfg.Enabled}} }

func (n *NTP) name() string          { return "ntp" }
func (n *NTP) close()                {}
func (n *NTP) period() time.Duration { return 60 * time.Second }

type clockState struct {
	offset    float64 // secondes, positif = horloge locale en avance
	hasOffset bool
	synced    bool
	source    string
	tool      string
}

// parseChronyTracking lit « chronyc -n tracking ».
func parseChronyTracking(text string) (clockState, error) {
	c := clockState{tool: "chronyc"}
	found := false
	for _, l := range strings.Split(text, "\n") {
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "Reference ID":
			c.source = v
		case "System time":
			f := strings.Fields(v) // 0.000012345 seconds fast of NTP time
			if len(f) >= 3 {
				x, err := strconv.ParseFloat(f[0], 64)
				if err == nil {
					if f[2] == "slow" {
						x = -x
					}
					c.offset, c.hasOffset, found = x, true, true
				}
			}
		case "Leap status":
			c.synced = v == "Normal" || v == "Insert second" || v == "Delete second"
			found = true
		}
	}
	if !found {
		return c, fmt.Errorf("sortie de chronyc tracking illisible")
	}
	if strings.HasPrefix(c.source, "00000000") || strings.HasPrefix(c.source, "7F7F0101") {
		c.synced = false // aucune source, ou horloge locale
	}
	return c, nil
}

var timespanRe = regexp.MustCompile(`^([+-]?[0-9.]+)(ns|us|µs|ms|s|min)$`)

// parseTimespan lit « +1.234ms », « -250us », « 2.5s ».
func parseTimespan(s string) (float64, bool) {
	m := timespanRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	switch m[2] {
	case "ns":
		v /= 1e9
	case "us", "µs":
		v /= 1e6
	case "ms":
		v /= 1e3
	case "min":
		v *= 60
	}
	return v, true
}

// parseTimesyncStatus lit « timedatectl timesync-status » (Offset, Server).
func parseTimesyncStatus(text string) (clockState, error) {
	c := clockState{tool: "timedatectl"}
	for _, l := range strings.Split(text, "\n") {
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "Offset":
			if x, ok := parseTimespan(v); ok {
				c.offset, c.hasOffset = x, true
			}
		case "Server":
			c.source = strings.TrimSpace(v)
		}
	}
	if !c.hasOffset && c.source == "" {
		return c, fmt.Errorf("sortie de timedatectl timesync-status illisible")
	}
	return c, nil
}

// parseNtpqRV lit « ntpq -c rv » : offset (ms), leap, strate et source.
// Synchronisé = indicateur de saut différent de « alarme » (11) et strate inférieure à 16.
// Le mot d'état (« sync_unspec », « no_sys_peer ») ne compte pas : ntpsec affiche
// « sync_unspec » en temps normal, et « no_sys_peer » le temps qu'une source soit réélue,
// alors que le noyau reste synchronisé.
func parseNtpqRV(text string) (clockState, error) {
	c := clockState{tool: "ntpq"}
	flat := strings.ReplaceAll(text, "\n", " ")
	leapOK, stratumOK := true, true
	sawLeap := false
	for _, part := range strings.Split(flat, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch k {
		case "offset":
			if x, err := strconv.ParseFloat(v, 64); err == nil {
				c.offset, c.hasOffset = x/1000, true
			}
		case "leap":
			sawLeap = true
			leapOK = v != "11" && v != "3"
		case "stratum":
			if s, err := strconv.Atoi(v); err == nil {
				stratumOK = s > 0 && s < 16
			}
		case "refid":
			c.source = strings.Trim(v, `"`)
		}
	}
	if !sawLeap && strings.Contains(flat, "leap_alarm") {
		leapOK = false
	}
	c.synced = leapOK && stratumOK
	if !c.hasOffset {
		return c, fmt.Errorf("sortie de ntpq illisible")
	}
	return c, nil
}

// parseW32tmStatus lit la première ligne de « w32tm /query /status » (indicateur de saut :
// 3 = non synchronisé), indépendamment de la langue de Windows.
func parseW32tmStatus(text string) (synced bool, ok bool) {
	for _, l := range strings.Split(text, "\n") {
		if i := strings.Index(l, ":"); i >= 0 {
			rest := strings.TrimSpace(l[i+1:])
			if len(rest) > 0 && rest[0] >= '0' && rest[0] <= '3' {
				return rest[0] != '3', true
			}
		}
	}
	return false, false
}

var stripchartRe = regexp.MustCompile(`,\s*([+-]\d+(?:\.\d+)?)s`)

// parseW32tmStripchart lit le décalage de « w32tm /stripchart … /dataonly » (« 10:00:00, +00.0012345s »).
func parseW32tmStripchart(text string) (float64, bool) {
	m := stripchartRe.FindAllStringSubmatch(text, -1)
	if len(m) == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(m[len(m)-1][1], 64)
	return v, err == nil
}

func (n *NTP) read(ctx context.Context) (clockState, error) {
	if runtime.GOOS == "windows" {
		return readW32tm(ctx)
	}
	if t, ok := findTool("chronyc"); ok {
		if out, err := runTool(ctx, 10*time.Second, t, "-n", "tracking"); err == nil {
			return parseChronyTracking(string(out))
		}
	}
	if t, ok := findTool("timedatectl"); ok {
		if out, err := runTool(ctx, 10*time.Second, t, "timesync-status"); err == nil {
			c, err := parseTimesyncStatus(string(out))
			if err == nil {
				if s, err := runTool(ctx, 10*time.Second, t, "show", "--property=NTPSynchronized", "--value"); err == nil {
					c.synced = strings.TrimSpace(string(s)) == "yes"
				}
				return c, nil
			}
		}
	}
	if t, ok := findTool("ntpq"); ok {
		if out, err := runTool(ctx, 10*time.Second, t, "-c", "rv"); err == nil {
			c, err := parseNtpqRV(string(out))
			if err == nil {
				// Le noyau tranche quand systemd le donne (horloge disciplinée par ntpd).
				if td, ok := findTool("timedatectl"); ok {
					if s, err := runTool(ctx, 10*time.Second, td, "show", "--property=NTPSynchronized", "--value"); err == nil {
						if v := strings.TrimSpace(string(s)); v == "yes" || v == "no" {
							c.synced = v == "yes"
						}
					}
				}
			}
			return c, err
		}
	}
	return clockState{}, fmt.Errorf("ni chronyc, ni timedatectl timesync-status, ni ntpq")
}

func readW32tm(ctx context.Context) (clockState, error) {
	c := clockState{tool: "w32tm"}
	out, err := runTool(ctx, 15*time.Second, "w32tm", "/query", "/status")
	if err != nil {
		return c, err
	}
	synced, ok := parseW32tmStatus(string(out))
	if !ok {
		return c, fmt.Errorf("sortie de w32tm /query /status illisible")
	}
	c.synced = synced
	src, _ := runTool(ctx, 15*time.Second, "w32tm", "/query", "/source")
	c.source = strings.TrimSpace(string(src))
	peer, _, _ := strings.Cut(c.source, ",")
	low := strings.ToLower(peer)
	if strings.Contains(low, "local cmos") || strings.Contains(low, "free-running") {
		c.synced = false
		return c, nil
	}
	if peer != "" && !strings.Contains(peer, " ") {
		if sc, err := runTool(ctx, 15*time.Second, "w32tm", "/stripchart", "/computer:"+peer, "/samples:1", "/dataonly"); err == nil {
			c.offset, c.hasOffset = parseW32tmStripchart(string(sc))
		}
	}
	return c, nil
}

func (n *NTP) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := n.det.skip(now); skip {
		return nil, st
	}
	c, err := n.read(ctx)
	ts := model.Ms(now)
	if err != nil {
		if silent, st := n.det.failed(err.Error()); silent {
			return nil, st
		} else {
			return []model.Check{{TS: ts, Name: "ntp.in_sync", Status: model.CheckUnknown, Message: err.Error(), Tags: tags()}}, st
		}
	}
	n.det.ok()
	if c.hasOffset {
		b.Gauge("ntp.offset", "second", c.offset)
	}
	st, msg := model.CheckOK, "horloge synchronisée"
	switch {
	case !c.synced:
		st, msg = model.CheckCrit, "horloge non synchronisée"
	case c.hasOffset && math.Abs(c.offset) > 1:
		st, msg = model.CheckWarn, "horloge synchronisée mais décalée"
	}
	if c.hasOffset {
		msg += fmt.Sprintf(" (décalage %+.6f s", c.offset)
		if c.source != "" {
			msg += ", source " + c.source
		}
		msg += ")"
	} else if c.source != "" {
		msg += " (source " + c.source + ")"
	}
	return []model.Check{{TS: ts, Name: "ntp.in_sync", Status: st, Message: msg + " — " + c.tool, Tags: tags()},
		canConnect("ntp", ts, true, c.tool+" lu", tags())}, "ok"
}
