package integrations

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/hostfs"
	"s33d.fr/hypha/agent/internal/model"
)

// ── Unbound : « unbound-control stats_noreset » ─────────────────────────────────────────────
//
// remote-control doit être actif (control-enable: yes ; socket Unix control-interface: /run/…ctl,
// ou TCP avec les clés de unbound-control-setup, lisibles par root). stats_noreset ne remet pas les
// compteurs à zéro : un autre outil qui lit « stats » (et les remet à zéro) fait seulement sauter un
// écart. Compteurs (total.*, num.*) → écarts par intervalle ; jauges (file des requêtes, temps de
// récursion, mémoire, caches) → valeur. Avec extended-statistics: yes, requêtes par type et
// réponses par code.

var unboundCounters = map[string]string{
	"total.num.queries": "unbound.num.queries", "total.num.cachehits": "unbound.num.cachehits",
	"total.num.cachemiss": "unbound.num.cachemiss", "total.num.prefetch": "unbound.num.prefetch",
	"total.num.expired": "unbound.num.expired", "total.num.recursivereplies": "unbound.num.recursivereplies",
	"total.num.queries_ip_ratelimited": "unbound.num.queries_ip_ratelimited", "total.num.queries_timed_out": "unbound.num.queries_timed_out",
	"total.num.queries_discard_timeout": "unbound.num.queries_discard_timeout", "total.num.queries_wait_limit": "unbound.num.queries_wait_limit",
	"total.requestlist.overwritten": "unbound.requestlist.overwritten", "total.requestlist.exceeded": "unbound.requestlist.exceeded",
	"num.answer.secure": "unbound.num.answer.secure", "num.answer.bogus": "unbound.num.answer.bogus",
	"num.rrset.bogus": "unbound.num.rrset.bogus", "unwanted.queries": "unbound.unwanted.queries",
	"unwanted.replies": "unbound.unwanted.replies", "num.query.tcp": "unbound.num.query.tcp",
	"num.query.tls": "unbound.num.query.tls", "num.query.https": "unbound.num.query.https",
	"num.query.ipv6": "unbound.num.query.ipv6", "num.query.ratelimited": "unbound.num.query.ratelimited",
	"num.query.tcpout": "unbound.num.query.tcpout", "num.query.udpout": "unbound.num.query.udpout",
}

var unboundGauges = map[string]string{
	"total.requestlist.avg": "unbound.requestlist.avg", "total.requestlist.max": "unbound.requestlist.max",
	"total.requestlist.current.all": "unbound.requestlist.current", "total.recursion.time.avg": "unbound.recursion.time.avg",
	"total.recursion.time.median": "unbound.recursion.time.median", "total.tcpusage": "unbound.tcpusage",
	"time.up": "unbound.time.up", "mem.cache.rrset": "unbound.mem.cache.rrset", "mem.cache.message": "unbound.mem.cache.message",
	"mem.mod.iterator": "unbound.mem.mod.iterator", "mem.mod.validator": "unbound.mem.mod.validator",
	"msg.cache.count": "unbound.msg.cache.count", "rrset.cache.count": "unbound.rrset.cache.count",
	"infra.cache.count": "unbound.infra.cache.count", "key.cache.count": "unbound.key.cache.count",
}

var unboundUnits = map[string]string{
	"time.up": "second", "total.recursion.time.avg": "second", "total.recursion.time.median": "second",
	"mem.cache.rrset": "byte", "mem.cache.message": "byte", "mem.mod.iterator": "byte", "mem.mod.validator": "byte",
}

type Unbound struct {
	probe
	cfg config.UnboundConfig
}

func newUnbound(cfg config.UnboundConfig) *Unbound {
	explicit := len(cfg.Command) != 1 || cfg.Command[0] != "unbound-control" || cfg.ConfigFile != ""
	return &Unbound{cfg: cfg, probe: newProbe("unbound", "unbound.can_connect", intent(cfg.Enabled, explicit))}
}

func (u *Unbound) period() time.Duration { return 30 * time.Second }

func (u *Unbound) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := u.cfg.Command
	path := cmd[0]
	if !filepath.IsAbs(path) {
		p, ok := findTool(path)
		if !ok {
			return nil, fmt.Errorf("%s introuvable", path)
		}
		path = p
	}
	full := append([]string{}, cmd[1:]...)
	if u.cfg.ConfigFile != "" {
		full = append(full, "-c", u.cfg.ConfigFile)
	}
	return runTool(ctx, u.cfg.Timeout.D(), path, append(full, args...)...)
}

// parseUnboundStats lit les lignes « clé=valeur » ; une ligne d'erreur rend une erreur.
func parseUnboundStats(out []byte) (map[string]float64, error) {
	vals := map[string]float64{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			if strings.HasPrefix(line, "error") {
				return nil, fmt.Errorf("unbound-control : %s", model.Truncate(line, 200))
			}
			continue
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err == nil {
			vals[k] = f
		}
	}
	if _, ok := vals["total.num.queries"]; !ok {
		return nil, fmt.Errorf("unbound-control stats_noreset : aucune statistique (remote-control actif ?)")
	}
	return vals, nil
}

func (u *Unbound) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := u.det.skip(now); skip {
		return nil, st
	}
	if u.det.mode == "auto" && !u.det.seen && !fileExists(hostfs.Etc("unbound", "unbound.conf")) {
		return nil, "absent"
	}
	ts := model.Ms(now)
	out, err := u.run(ctx, "stats_noreset")
	vals, perr := parseUnboundStats(out)
	switch {
	case perr == nil:
		err = nil // sortie complète, même avec un code de retour non nul
	case err == nil, strings.HasPrefix(perr.Error(), "unbound-control : error"):
		err = perr // ligne « error … » de la sortie standard, plus parlante que le code de retour
	}
	if err != nil {
		return u.fail(ts, tags, err)
	}
	u.det.ok()
	kv := u.kvs()
	u.begin()
	for k, name := range unboundCounters {
		if v, ok := vals[k]; ok {
			u.count(b, name, "query", k, v, kv...)
		}
	}
	for k, name := range unboundGauges {
		if v, ok := vals[k]; ok {
			b.Gauge(name, unboundUnits[k], v, kv...)
		}
	}
	for k, v := range vals {
		switch {
		case strings.HasPrefix(k, "num.query.type."):
			t := strings.TrimPrefix(k, "num.query.type.")
			if len(t) <= 16 {
				u.count(b, "unbound.num.query.type", "query", k, v, u.kvs("query_type", t)...)
			}
		case strings.HasPrefix(k, "num.answer.rcode."):
			r := strings.TrimPrefix(k, "num.answer.rcode.")
			if len(r) <= 16 {
				u.count(b, "unbound.num.answer.rcode", "response", k, v, u.kvs("rcode", r)...)
			}
		}
	}
	u.ratio(b, "unbound.cache.hit_rate", "total.num.cachehits", "total.num.cachemiss", kv...)
	u.end()
	msg := fmt.Sprintf("Unbound en service depuis %.0f s, %.0f requêtes", vals["time.up"], vals["total.num.queries"])
	return []model.Check{u.okCheck(ts, tags, msg)}, "ok"
}
