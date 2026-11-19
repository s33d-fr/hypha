package integrations

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/hostfs"
	"s33d.fr/hypha/agent/internal/model"
)

// ── Redis et Valkey (§ 37) : commande INFO, sans bibliothèque ──────────────────────────────
//
// Une connexion par collecte : AUTH si un mot de passe est posé, puis INFO. Mémoire, clients,
// clés par base, commandes, taux de succès du cache, évictions, réplication (lien avec le
// principal), persistance (dernière sauvegarde RDB, écriture AOF).

const redisTimeout = 5 * time.Second

var redisCounters = []struct{ field, metric, unit string }{
	{"total_commands_processed", "redis.commands", "command"},
	{"total_connections_received", "redis.connections.received", "connection"},
	{"rejected_connections", "redis.connections.rejected", "connection"},
	{"evicted_keys", "redis.keys.evicted", "key"},
	{"expired_keys", "redis.keys.expired", "key"},
	{"keyspace_hits", "redis.keyspace.hits", "hit"},
	{"keyspace_misses", "redis.keyspace.misses", "miss"},
	{"total_net_input_bytes", "redis.net.input", "byte"},
	{"total_net_output_bytes", "redis.net.output", "byte"},
}

type Redis struct {
	cfg  config.RedisConfig
	det  detector
	prev map[string]float64
}

func newRedis(cfg config.RedisConfig) *Redis {
	mode := cfg.Enabled
	if mode == "auto" && (cfg.Password != "" || (cfg.Address != "" && cfg.Address != "127.0.0.1:6379")) {
		mode = "true" // une adresse ou un mot de passe posés disent l'intention
	}
	return &Redis{cfg: cfg, det: detector{mode: mode}}
}

func (r *Redis) name() string          { return "redis" }
func (r *Redis) period() time.Duration { return 30 * time.Second }
func (r *Redis) close()                {}

func (r *Redis) dial(ctx context.Context) (net.Conn, error) {
	d := net.Dialer{Timeout: redisTimeout}
	if strings.HasPrefix(r.cfg.Address, "/") {
		return d.DialContext(ctx, "unix", hostfs.Root(r.cfg.Address))
	}
	return d.DialContext(ctx, "tcp", r.cfg.Address)
}

// redisCommand : commande en tableau RESP ; réponse simple, erreur ou chaîne longue.
func redisCommand(rw *bufio.ReadWriter, args ...string) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "*%d\r\n", len(args))
	for _, a := range args {
		fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(a), a)
	}
	if _, err := rw.WriteString(b.String()); err != nil {
		return "", err
	}
	if err := rw.Flush(); err != nil {
		return "", err
	}
	line, err := rw.ReadString('\n')
	if err != nil {
		return "", err
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return "", fmt.Errorf("réponse vide")
	}
	switch line[0] {
	case '+':
		return line[1:], nil
	case '-':
		return "", fmt.Errorf("%s", line[1:])
	case '$':
		n, err := strconv.Atoi(line[1:])
		if err != nil || n < 0 || n > 16<<20 {
			return "", fmt.Errorf("longueur de réponse invalide : %q", line)
		}
		buf := make([]byte, n+2)
		if _, err := io.ReadFull(rw, buf); err != nil {
			return "", err
		}
		return string(buf[:n]), nil
	}
	return "", fmt.Errorf("réponse inattendue : %q", model.Truncate(line, 80))
}

// parseRedisInfo : « clé:valeur » par ligne ; les lignes « # Section » et vides sont ignorées.
func parseRedisInfo(s string) map[string]string {
	out := map[string]string{}
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || l[0] == '#' {
			continue
		}
		if k, v, ok := strings.Cut(l, ":"); ok {
			out[k] = v
		}
	}
	return out
}

// keyspace : « db0:keys=12,expires=3,avg_ttl=0 » → clés et clés à expiration par base.
func keyspace(info map[string]string) map[string][2]float64 {
	out := map[string][2]float64{}
	for k, v := range info {
		if !strings.HasPrefix(k, "db") {
			continue
		}
		if _, err := strconv.Atoi(k[2:]); err != nil {
			continue
		}
		var keys, exp float64
		for _, part := range strings.Split(v, ",") {
			name, val, _ := strings.Cut(part, "=")
			f, _ := strconv.ParseFloat(val, 64)
			switch name {
			case "keys":
				keys = f
			case "expires":
				exp = f
			}
		}
		out[k] = [2]float64{keys, exp}
	}
	return out
}

func (r *Redis) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := r.det.skip(now); skip {
		return nil, st
	}
	ts := model.Ms(now)
	kv := []string{"instance", r.cfg.Address}
	fail := func(err error) ([]model.Check, string) {
		if silent, st := r.det.failed(err.Error()); silent {
			return nil, st
		}
		return []model.Check{{TS: ts, Name: "redis.can_connect", Status: model.CheckCrit, Message: err.Error(),
			Tags: tags(kv...)}}, "erreur : " + err.Error()
	}
	ctx, cancel := context.WithTimeout(ctx, 2*redisTimeout)
	defer cancel()
	conn, err := r.dial(ctx)
	if err != nil {
		return fail(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * redisTimeout))
	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	if r.cfg.Password != "" {
		args := []string{"AUTH", r.cfg.Password}
		if r.cfg.Username != "" {
			args = []string{"AUTH", r.cfg.Username, r.cfg.Password}
		}
		if _, err := redisCommand(rw, args...); err != nil {
			return fail(fmt.Errorf("authentification refusée : %s", firstWord(err.Error())))
		}
	}
	raw, err := redisCommand(rw, "INFO")
	if err != nil {
		return fail(fmt.Errorf("INFO : %w", err))
	}
	r.det.ok()
	info := parseRedisInfo(raw)
	num := func(k string) (float64, bool) {
		v, err := strconv.ParseFloat(info[k], 64)
		return v, err == nil
	}
	gauge := func(name, unit, field string) {
		if v, ok := num(field); ok {
			b.Gauge(name, unit, v, kv...)
		}
	}
	gauge("redis.uptime", "second", "uptime_in_seconds")
	gauge("redis.clients.connected", "connection", "connected_clients")
	gauge("redis.clients.blocked", "connection", "blocked_clients")
	gauge("redis.mem.used", "byte", "used_memory")
	gauge("redis.mem.rss", "byte", "used_memory_rss")
	gauge("redis.mem.fragmentation_ratio", "", "mem_fragmentation_ratio")
	gauge("redis.rdb.changes_since_last_save", "change", "rdb_changes_since_last_save")
	if mx, ok := num("maxmemory"); ok && mx > 0 {
		b.Gauge("redis.mem.maxmemory", "byte", mx, kv...)
		if used, ok := num("used_memory"); ok {
			b.Gauge("redis.mem.usage", "percent", round1(100*used/mx), kv...)
		}
	}
	ks := keyspace(info)
	dbs := make([]string, 0, len(ks))
	for db := range ks {
		dbs = append(dbs, db)
	}
	sort.Strings(dbs)
	for _, db := range dbs {
		dkv := append(append([]string{}, kv...), "db", db)
		b.Gauge("redis.keys", "key", ks[db][0], dkv...)
		b.Gauge("redis.keys.expires", "key", ks[db][1], dkv...)
	}
	cur := map[string]float64{}
	for _, c := range redisCounters {
		if v, ok := num(c.field); ok {
			cur[c.field] = v
		}
	}
	if r.prev != nil {
		for _, c := range redisCounters {
			if v, ok := cur[c.field]; ok {
				if d, ok := deltaF(v, r.prev[c.field]); ok {
					b.Add(c.metric, model.TypeCount, c.unit, d, kv...)
				}
			}
		}
		hits, ok1 := deltaF(cur["keyspace_hits"], r.prev["keyspace_hits"])
		miss, ok2 := deltaF(cur["keyspace_misses"], r.prev["keyspace_misses"])
		if ok1 && ok2 && hits+miss > 0 {
			b.Gauge("redis.keyspace.hit_rate", "percent", round1(100*hits/(hits+miss)), kv...)
		}
	}
	r.prev = cur

	checks := []model.Check{}
	role := info["role"]
	if role == "slave" || role == "replica" {
		st, msg := model.CheckOK, fmt.Sprintf("réplique de %s:%s, lien %s", info["master_host"], info["master_port"], info["master_link_status"])
		if info["master_link_status"] != "up" {
			st = model.CheckCrit
			if d, ok := num("master_link_down_since_seconds"); ok && d >= 0 {
				msg += fmt.Sprintf(" depuis %s", (time.Duration(d) * time.Second).String())
			}
		}
		if v, ok := num("master_last_io_seconds_ago"); ok && v >= 0 {
			b.Gauge("redis.replication.last_io", "second", v, kv...)
		}
		checks = append(checks, model.Check{TS: ts, Name: "redis.replication", Status: st, Message: msg, Tags: tags(kv...)})
	} else if v, ok := num("connected_slaves"); ok {
		b.Gauge("redis.replication.replicas", "replica", v, kv...)
	}
	var persist []string
	if s := info["rdb_last_bgsave_status"]; s != "" && s != "ok" {
		persist = append(persist, "dernière sauvegarde RDB en échec")
	}
	if info["aof_enabled"] == "1" && info["aof_last_write_status"] != "" && info["aof_last_write_status"] != "ok" {
		persist = append(persist, "écriture AOF en échec")
	}
	if len(persist) > 0 {
		checks = append(checks, model.Check{TS: ts, Name: "redis.persistence", Status: model.CheckCrit, Message: strings.Join(persist, " ; "), Tags: tags(kv...)})
	} else {
		checks = append(checks, model.Check{TS: ts, Name: "redis.persistence", Status: model.CheckOK, Message: "sauvegardes à jour", Tags: tags(kv...)})
	}
	product := "Redis"
	version := info["redis_version"]
	if v := info["valkey_version"]; v != "" {
		product, version = "Valkey", v
	}
	if role == "master" {
		role = "principal"
	} else if role == "slave" || role == "replica" {
		role = "réplique"
	}
	checks = append(checks, model.Check{TS: ts, Name: "redis.can_connect", Status: model.CheckOK,
		Message: fmt.Sprintf("%s %s joignable (%s)", product, version, role), Tags: tags(kv...)})
	return checks, "ok"
}

func firstWord(s string) string {
	if i := strings.IndexByte(s, ' '); i > 0 {
		return s[:i]
	}
	return s
}
