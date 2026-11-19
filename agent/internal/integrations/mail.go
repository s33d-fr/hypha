package integrations

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/hostfs"
	"s33d.fr/hypha/agent/internal/model"
)

// ── Postfix : files d'attente (postqueue -j) et remises tirées des journaux ──────────────

var postfixQueues = []string{"active", "deferred", "hold", "incoming", "maildrop"}

type Postfix struct {
	cfg  config.IntegrationToggle
	det  detector
	sent atomic.Int64
	defd atomic.Int64
	bnc  atomic.Int64
	on   atomic.Bool
}

func newPostfix(cfg config.IntegrationToggle) *Postfix {
	return &Postfix{cfg: cfg, det: detector{mode: cfg.Enabled}}
}

func (p *Postfix) name() string          { return "postfix" }
func (p *Postfix) close()                {}
func (p *Postfix) period() time.Duration { return 60 * time.Second }

// observe compte les remises (status=sent|deferred|bounced) des lignes de postfix/smtp,
// lmtp, local, virtual, pipe, error. Sous journald, l'identifiant vaut « postfix/smtp » ;
// dans mail.log, il est dans le message. Les lignes de journald et du fichier mail.log
// décrivent les mêmes remises : seules celles de journald comptent si elles arrivent.
func (p *Postfix) observe(l *model.Log) {
	if !p.on.Load() {
		return
	}
	msg := l.Message
	i := strings.Index(msg, " status=")
	if i < 0 {
		return
	}
	fromJournal := strings.HasPrefix(l.Service, "postfix")
	if !fromJournal {
		if l.Source == "journald" || !strings.Contains(msg, "postfix") {
			return
		}
	}
	st := msg[i+len(" status="):]
	switch {
	case strings.HasPrefix(st, "sent"):
		p.sent.Add(1)
	case strings.HasPrefix(st, "deferred"):
		p.defd.Add(1)
	case strings.HasPrefix(st, "bounced"):
		p.bnc.Add(1)
	}
}

// countPostqueueJSON compte les messages par file dans la sortie de « postqueue -j » (un
// objet JSON par ligne).
func countPostqueueJSON(data []byte) (map[string]int64, error) {
	out := map[string]int64{}
	for _, q := range postfixQueues {
		out[q] = 0
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64<<10), 8<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var m struct {
			Queue string `json:"queue_name"`
		}
		if err := json.Unmarshal(line, &m); err != nil {
			return nil, err
		}
		out[m.Queue]++
	}
	return out, sc.Err()
}

// spoolCounts : messages des dossiers de file (Postfix antérieur à 3.1, sans postqueue -j).
func spoolCounts() (map[string]int64, error) {
	out := map[string]int64{}
	base := hostfs.Root("/var/spool/postfix")
	if _, err := os.Stat(base); err != nil {
		return nil, err
	}
	for _, q := range postfixQueues {
		var n int64
		_ = filepath.WalkDir(filepath.Join(base, q), func(_ string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				n++
			}
			return nil
		})
		out[q] = n
	}
	return out, nil
}

func (p *Postfix) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := p.det.skip(now); skip {
		return nil, st
	}
	ts := model.Ms(now)
	tool, ok := findTool("postqueue")
	if !ok {
		_, st := p.det.failed("postqueue introuvable")
		return nil, st
	}
	out, err := runTool(ctx, 30*time.Second, tool, "-j")
	var counts map[string]int64
	if err == nil {
		counts, err = countPostqueueJSON(out)
	} else if strings.Contains(err.Error(), "illegal option") || strings.Contains(err.Error(), "invalid option") {
		counts, err = spoolCounts()
	}
	if err != nil {
		if silent, st := p.det.failed(err.Error()); silent {
			return nil, st
		} else {
			return []model.Check{canConnect("postfix", ts, false, err.Error(), tags())}, st
		}
	}
	p.det.ok()
	first := !p.on.Swap(true)
	for _, q := range model.SortedKeys(counts) {
		b.Gauge("postfix.queue.size", "count", float64(counts[q]), "queue", q)
	}
	if !first {
		b.Add("postfix.delivery.sent", model.TypeCount, "count", float64(p.sent.Swap(0)))
		b.Add("postfix.delivery.deferred", model.TypeCount, "count", float64(p.defd.Swap(0)))
		b.Add("postfix.delivery.bounced", model.TypeCount, "count", float64(p.bnc.Swap(0)))
	}
	return []model.Check{canConnect("postfix", ts, true, "files d'attente lues", tags())}, "ok"
}

// ── Dovecot : doveadm stats dump (2.3 et 2.2) et doveadm who ────────────────────────────

type Dovecot struct {
	cfg        config.IntegrationToggle
	det        detector
	prevOK     int64
	prevFail   int64
	havePrev   bool
}

func newDovecot(cfg config.IntegrationToggle) *Dovecot {
	return &Dovecot{cfg: cfg, det: detector{mode: cfg.Enabled}}
}

func (d *Dovecot) name() string          { return "dovecot" }
func (d *Dovecot) close()                {}
func (d *Dovecot) period() time.Duration { return 60 * time.Second }

// parseDoveadmStats lit « doveadm stats dump » : en 2.3, une ligne par métrique
// (metric_name, field, count…), auth_success et auth_failures ; en 2.2 (« dump global »),
// une ligne d'en-têtes puis une ligne de valeurs (auth_successes, auth_failures,
// num_connected_sessions). conns < 0 : inconnu.
func parseDoveadmStats(text string) (ok, fail, conns int64, found bool) {
	conns = -1
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) < 2 {
		return 0, 0, -1, false
	}
	head := strings.Split(lines[0], "\t")
	col := map[string]int{}
	for i, h := range head {
		col[strings.TrimSpace(h)] = i
	}
	if ni, isNew := col["metric_name"]; isNew {
		ci, hasCount := col["count"]
		if !hasCount {
			return 0, 0, -1, false
		}
		for _, l := range lines[1:] {
			f := strings.Split(l, "\t")
			if len(f) <= ci || len(f) <= ni {
				continue
			}
			n, _ := strconv.ParseInt(strings.TrimSpace(f[ci]), 10, 64)
			switch strings.TrimSpace(f[ni]) {
			case "auth_success", "auth_successes":
				ok, found = n, true
			case "auth_failures", "auth_failure":
				fail, found = n, true
			}
		}
		return ok, fail, conns, found
	}
	vals := strings.Split(lines[1], "\t")
	get := func(k string) (int64, bool) {
		i, has := col[k]
		if !has || i >= len(vals) {
			return 0, false
		}
		n, err := strconv.ParseInt(strings.TrimSpace(vals[i]), 10, 64)
		return n, err == nil
	}
	if v, has := get("auth_successes"); has {
		ok, found = v, true
	}
	if v, has := get("auth_failures"); has {
		fail, found = v, true
	}
	if v, has := get("num_connected_sessions"); has {
		conns = v
	}
	return ok, fail, conns, found
}

// parseDoveadmWho additionne la colonne « # » de « doveadm who » (connexions par compte).
func parseDoveadmWho(text string) int64 {
	var n int64
	for i, l := range strings.Split(text, "\n") {
		f := strings.Fields(l)
		if i == 0 || len(f) < 2 {
			continue
		}
		if v, err := strconv.ParseInt(f[1], 10, 64); err == nil {
			n += v
		}
	}
	return n
}

func (d *Dovecot) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := d.det.skip(now); skip {
		return nil, st
	}
	ts := model.Ms(now)
	tool, found := findTool("doveadm")
	if !found {
		_, st := d.det.failed("doveadm introuvable")
		return nil, st
	}
	out, err := runTool(ctx, 15*time.Second, tool, "stats", "dump")
	okN, failN, conns, has := parseDoveadmStats(string(out))
	if err != nil || !has {
		if out2, err2 := runTool(ctx, 15*time.Second, tool, "stats", "dump", "global"); err2 == nil {
			okN, failN, conns, has = parseDoveadmStats(string(out2))
			err = nil
		}
	}
	if err == nil && !has {
		err = errNoData
	}
	if err != nil {
		if silent, st := d.det.failed(err.Error()); silent {
			return nil, st
		} else {
			return []model.Check{canConnect("dovecot", ts, false, err.Error(), tags())}, st
		}
	}
	d.det.ok()
	if conns < 0 {
		if w, err := runTool(ctx, 15*time.Second, tool, "who"); err == nil {
			conns = parseDoveadmWho(string(w))
		}
	}
	if conns >= 0 {
		b.Gauge("dovecot.connections", "count", float64(conns))
	}
	if d.havePrev && okN >= d.prevOK && failN >= d.prevFail {
		b.Add("dovecot.auth.success", model.TypeCount, "count", float64(okN-d.prevOK))
		b.Add("dovecot.auth.failures", model.TypeCount, "count", float64(failN-d.prevFail))
	}
	d.prevOK, d.prevFail, d.havePrev = okN, failN, true
	return []model.Check{canConnect("dovecot", ts, true, "statistiques lues", tags())}, "ok"
}
