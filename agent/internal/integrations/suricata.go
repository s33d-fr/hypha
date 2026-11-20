package integrations

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/hostfs"
	"s33d.fr/hypha/agent/internal/model"
)

// ── Suricata : fichier eve.json (événements « stats » et « alert ») ─────────────────────────
//
// Lecture suivie du fichier (position gardée, rotation reconnue) : dernier relevé « stats »
// (Suricata en écrit un toutes les 8 s par défaut : paquets capturés et perdus, décodage, flux,
// TCP, détection, protocoles applicatifs) et alertes comptées par gravité. Au premier passage,
// seuls les 4 derniers Mio sont lus (aucune alerte passée n'est recomptée). Un relevé « stats »
// plus vieux que stale_after passe suricata.can_read en CRIT (Suricata arrêté ou stats coupées
// dans suricata.yaml : outputs, eve-log, types, stats).

const (
	suricataFirstRead = 4 << 20
	suricataMaxRead   = 64 << 20
)

type Suricata struct {
	probe
	cfg       config.SuricataConfig
	offset    int64
	file      os.FileInfo
	last      map[string]any
	lastAt    time.Time
	sevCounts map[string]float64
	started   bool
}

func newSuricata(cfg config.SuricataConfig) *Suricata {
	explicit := cfg.EveFile != "/var/log/suricata/eve.json"
	return &Suricata{cfg: cfg, sevCounts: map[string]float64{},
		probe: newProbe("suricata", "suricata.can_read", intent(cfg.Enabled, explicit), "eve_file", cfg.EveFile)}
}

func (s *Suricata) period() time.Duration { return 30 * time.Second }

var (
	eveStats = []byte(`"event_type":"stats"`)
	eveAlert = []byte(`"event_type":"alert"`)
)

// readNew lit les lignes ajoutées depuis le dernier passage ; rend le nombre d'octets lus.
func (s *Suricata) readNew(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return 0, err
	}
	start := s.offset
	switch {
	case s.file == nil: // premier passage : la fin du fichier seulement
		start = max(0, fi.Size()-suricataFirstRead)
	case !os.SameFile(s.file, fi) || fi.Size() < s.offset: // rotation ou troncature
		start = 0
	}
	if fi.Size()-start > suricataMaxRead {
		start = fi.Size() - suricataFirstRead // retard trop grand : on saute au plus récent
	}
	countAlerts := s.file != nil
	s.file = fi
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return 0, err
	}
	rd := bufio.NewReaderSize(io.LimitReader(f, fi.Size()-start), 256<<10)
	read := int64(0)
	if start > 0 && s.offset != start {
		// Au milieu d'une ligne : la sauter.
		skipped, err := rd.ReadBytes('\n')
		read += int64(len(skipped))
		if err != nil {
			s.offset = start + read
			return read, nil
		}
	}
	for {
		line, err := rd.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] != '\n' {
			break // ligne en cours d'écriture : relue au prochain passage
		}
		read += int64(len(line))
		switch {
		case bytes.Contains(line, eveStats):
			var ev struct {
				Timestamp string         `json:"timestamp"`
				Stats     map[string]any `json:"stats"`
			}
			if json.Unmarshal(line, &ev) == nil && ev.Stats != nil {
				s.last = ev.Stats
				if t, err := time.Parse("2006-01-02T15:04:05.999999-0700", ev.Timestamp); err == nil {
					s.lastAt = t
				} else {
					s.lastAt = time.Now() // horodatage d'une autre forme : relevé tout juste lu
				}
			}
		case countAlerts && bytes.Contains(line, eveAlert):
			var ev struct {
				Alert struct {
					Severity int    `json:"severity"`
					Action   string `json:"action"`
				} `json:"alert"`
			}
			if json.Unmarshal(line, &ev) == nil {
				s.sevCounts[strconv.Itoa(ev.Alert.Severity)+"/"+ev.Alert.Action]++
			}
		}
		if err != nil {
			break
		}
	}
	s.offset = start + read
	return read, nil
}

// statPath lit un nombre dans l'arbre « stats » (« capture.kernel_packets »).
func statPath(m map[string]any, path ...string) (float64, bool) {
	var cur any = m
	for _, p := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return 0, false
		}
		cur = mm[p]
	}
	f, ok := cur.(float64)
	return f, ok
}

var suricataCounters = []struct {
	path   []string
	metric string
	unit   string
}{
	{[]string{"capture", "kernel_packets"}, "suricata.capture.kernel_packets", "packet"},
	{[]string{"capture", "kernel_drops"}, "suricata.capture.kernel_drops", "packet"},
	{[]string{"capture", "errors"}, "suricata.capture.errors", "error"},
	{[]string{"decoder", "pkts"}, "suricata.decoder.pkts", "packet"},
	{[]string{"decoder", "bytes"}, "suricata.decoder.bytes", "byte"},
	{[]string{"decoder", "invalid"}, "suricata.decoder.invalid", "packet"},
	{[]string{"tcp", "sessions"}, "suricata.tcp.sessions", "session"},
	{[]string{"tcp", "reassembly_gap"}, "suricata.tcp.reassembly_gap", "event"},
	{[]string{"tcp", "ssn_memcap_drop"}, "suricata.tcp.ssn_memcap_drop", "session"},
	{[]string{"tcp", "segment_memcap_drop"}, "suricata.tcp.segment_memcap_drop", "packet"},
	{[]string{"detect", "alert"}, "suricata.detect.alert", "alert"},
	{[]string{"detect", "alert_queue_overflow"}, "suricata.detect.alert_queue_overflow", "alert"},
	{[]string{"detect", "alerts_suppressed"}, "suricata.detect.alerts_suppressed", "alert"},
	{[]string{"flow", "emerg_mode_entered"}, "suricata.flow.emerg_mode_entered", "event"},
	{[]string{"flow", "memcap"}, "suricata.flow.memcap_drop", "flow"},
}

var suricataGauges = []struct {
	path   []string
	metric string
	unit   string
}{
	{[]string{"uptime"}, "suricata.uptime", "second"},
	{[]string{"flow", "memuse"}, "suricata.flow.memuse", "byte"},
	{[]string{"flow", "active"}, "suricata.flow.active", "flow"},
	{[]string{"tcp", "memuse"}, "suricata.tcp.memuse", "byte"},
	{[]string{"tcp", "reassembly_memuse"}, "suricata.tcp.reassembly_memuse", "byte"},
	{[]string{"memcap_pressure"}, "suricata.memcap_pressure", "percent"},
	{[]string{"memcap_pressure_max"}, "suricata.memcap_pressure_max", "percent"},
}

func (s *Suricata) collect(_ context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := s.det.skip(now); skip {
		return nil, st
	}
	ts := model.Ms(now)
	path := hostfs.Root(s.cfg.EveFile)
	if _, err := s.readNew(path); err != nil {
		return s.fail(ts, tags, fmt.Errorf("%s : %v", s.cfg.EveFile, err))
	}
	if s.last == nil {
		if !s.det.seen && s.det.mode == "auto" {
			if silent, st := s.det.failed("aucun relevé stats dans " + s.cfg.EveFile); silent {
				return nil, st
			}
		}
		return s.fail(ts, tags, fmt.Errorf("aucun relevé « stats » dans %s (eve-log : types : - stats)", s.cfg.EveFile))
	}
	s.det.ok()
	kv := s.kvs()
	s.begin()
	defer s.end()
	uptime, _ := statPath(s.last, "uptime")
	if prevUp, ok := s.prev["uptime"]; ok && uptime < prevUp {
		s.prev = nil // Suricata redémarré : compteurs repartis de zéro
	}
	s.cur["uptime"] = uptime
	for _, c := range suricataCounters {
		if v, ok := statPath(s.last, c.path...); ok {
			s.count(b, c.metric, c.unit, c.metric, v, kv...)
		}
	}
	for _, g := range suricataGauges {
		if v, ok := statPath(s.last, g.path...); ok {
			b.Gauge(g.metric, g.unit, v, kv...)
		}
	}
	if p, ok := s.diff("suricata.capture.kernel_packets"); ok && p > 0 {
		if d, ok := s.diff("suricata.capture.kernel_drops"); ok {
			b.Gauge("suricata.capture.drop_rate", "percent", round1(100*d/(p+d)), kv...)
		}
	}
	if app, ok := s.last["app_layer"].(map[string]any); ok {
		if flows, ok := app["flow"].(map[string]any); ok {
			for proto, v := range flows {
				// Protocoles jamais vus (valeur nulle depuis le démarrage) : aucune série.
				if f, ok := v.(float64); ok && f > 0 {
					s.count(b, "suricata.app_layer.flow", "flow", "app/"+proto, f, append(append([]string{}, kv...), "app_proto", proto)...)
				}
			}
		}
	}
	for k, n := range s.sevCounts {
		sev, action, _ := strings.Cut(k, "/")
		b.Add("suricata.alerts", model.TypeCount, "alert", n, append(append([]string{}, kv...), "severity", sev, "action", action)...)
	}
	s.sevCounts = map[string]float64{}
	age := now.Sub(s.lastAt)
	if s.lastAt.IsZero() || age > s.cfg.StaleAfter.D() {
		return []model.Check{{TS: ts, Name: "suricata.can_read", Status: model.CheckCrit,
			Message: fmt.Sprintf("dernier relevé stats vieux de %s : Suricata arrêté ?", age.Round(time.Second)), Tags: tags(kv...)}}, "ok"
	}
	return []model.Check{s.okCheck(ts, tags, fmt.Sprintf("relevé stats de %s (en marche depuis %s)",
		s.lastAt.Format("15:04:05"), (time.Duration(uptime)*time.Second).String()))}, "ok"
}
