package integrations

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── PgBouncer : console d'administration (base « pgbouncer ») ───────────────────────────────
//
// dsn d'un compte listé dans stats_users (ou admin_users) de pgbouncer.ini :
// postgres://hypha:motdepasse@127.0.0.1:6432/pgbouncer. SHOW POOLS (clients et serveurs par
// pool), SHOW STATS (requêtes, transactions, octets, temps), SHOW DATABASES (bases en pause ou
// désactivées), SHOW VERSION. Protocole simple : la console n'accepte que lui.

type PgBouncer struct {
	probe
	cfg  config.DSNProbe
	conn *pgx.Conn
}

func newPgBouncer(cfg config.DSNProbe) *PgBouncer {
	where := []string{}
	if c, err := pgx.ParseConfig(cfg.DSN); cfg.DSN != "" && err == nil {
		where = []string{"instance", fmt.Sprintf("%s:%d", c.Host, c.Port)}
	}
	return &PgBouncer{cfg: cfg, probe: newProbe("pgbouncer", "pgbouncer.can_connect", intent(cfg.Enabled, cfg.DSN != ""), where...)}
}

func (p *PgBouncer) period() time.Duration { return 30 * time.Second }

func (p *PgBouncer) close() {
	if p.conn != nil {
		_ = p.conn.Close(context.Background())
		p.conn = nil
	}
}

// anyFloat convertit une valeur rendue par pgx ou database/sql.
func anyFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case int64:
		return float64(x), true
	case int32:
		return float64(x), true
	case int16:
		return float64(x), true
	case int:
		return float64(x), true
	case uint64:
		return float64(x), true
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case []byte:
		f, err := strconv.ParseFloat(strings.TrimSpace(string(x)), 64)
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case interface{ Float64Value() (pgtype.Float8, error) }: // numeric (SHOW STATS de PgBouncer)
		if f, err := x.Float64Value(); err == nil && f.Valid {
			return f.Float64, true
		}
	}
	return 0, false
}

// showRows : lignes d'une commande SHOW, colonne → valeur.
func showRows(ctx context.Context, conn *pgx.Conn, cmd string) ([]map[string]any, error) {
	rows, err := conn.Query(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("%s : %w", cmd, err)
	}
	defer rows.Close()
	fields := rows.FieldDescriptions()
	var out []map[string]any
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, fmt.Errorf("%s : %w", cmd, err)
		}
		m := make(map[string]any, len(vals))
		for i, v := range vals {
			m[fields[i].Name] = v
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (p *PgBouncer) connect(ctx context.Context) (*pgx.Conn, error) {
	if p.conn != nil && !p.conn.IsClosed() {
		return p.conn, nil
	}
	if p.cfg.DSN == "" {
		return nil, fmt.Errorf("dsn non renseigné")
	}
	cfg, err := pgx.ParseConfig(p.cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("dsn : %w", err)
	}
	cfg.ConnectTimeout = 5 * time.Second
	cfg.RuntimeParams = map[string]string{"application_name": "hypha-agent"}
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	cfg.StatementCacheCapacity, cfg.DescriptionCacheCapacity = 0, 0
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	p.conn = conn
	return conn, nil
}

func str(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	case nil:
		return ""
	}
	return fmt.Sprint(v)
}

var pgbPoolGauges = []struct{ col, metric, unit string }{
	{"cl_active", "pgbouncer.pools.cl_active", "connection"},
	{"cl_waiting", "pgbouncer.pools.cl_waiting", "connection"},
	{"sv_active", "pgbouncer.pools.sv_active", "connection"},
	{"sv_idle", "pgbouncer.pools.sv_idle", "connection"},
	{"sv_used", "pgbouncer.pools.sv_used", "connection"},
	{"sv_tested", "pgbouncer.pools.sv_tested", "connection"},
	{"sv_login", "pgbouncer.pools.sv_login", "connection"},
}

var pgbStats = []struct{ col, metric, unit string }{
	{"total_query_count", "pgbouncer.stats.queries", "query"},
	{"total_xact_count", "pgbouncer.stats.transactions", "transaction"},
	{"total_received", "pgbouncer.stats.bytes_received", "byte"},
	{"total_sent", "pgbouncer.stats.bytes_sent", "byte"},
	{"total_wait_time", "pgbouncer.stats.wait_time", "microsecond"},
}

func (p *PgBouncer) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := p.det.skip(now); skip {
		return nil, st
	}
	ts := model.Ms(now)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := p.connect(ctx)
	var pools []map[string]any
	if err == nil {
		if pools, err = showRows(ctx, conn, "SHOW POOLS"); err != nil {
			p.close()
		}
	}
	if err != nil {
		return p.fail(ts, tags, err)
	}
	p.det.ok()
	p.begin()
	defer p.end()
	for _, r := range pools {
		db := str(r["database"])
		if db == "pgbouncer" {
			continue
		}
		kv := p.kvs("db", db, "user", str(r["user"]))
		for _, g := range pgbPoolGauges {
			if v, ok := anyFloat(r[g.col]); ok {
				b.Gauge(g.metric, g.unit, v, kv...)
			}
		}
		if v, ok := anyFloat(r["maxwait"]); ok {
			us, _ := anyFloat(r["maxwait_us"])
			b.Gauge("pgbouncer.pools.maxwait", "second", v+us/1e6, kv...)
		}
	}
	if stats, err := showRows(ctx, conn, "SHOW STATS"); err == nil {
		for _, r := range stats {
			db := str(r["database"])
			if db == "pgbouncer" {
				continue
			}
			kv := p.kvs("db", db)
			for _, s := range pgbStats {
				if v, ok := anyFloat(r[s.col]); ok {
					p.count(b, s.metric, s.unit, db+"/"+s.col, v, kv...)
				}
			}
			// Temps moyens sur l'intervalle (µs cumulées / nombre), en millisecondes.
			for _, a := range []struct{ time, n, metric string }{
				{"total_query_time", "total_query_count", "pgbouncer.stats.avg_query_time"},
				{"total_xact_time", "total_xact_count", "pgbouncer.stats.avg_transaction_time"},
			} {
				if v, ok := anyFloat(r[a.time]); ok {
					p.cur[db+"/"+a.time] = v
				}
				t, ok1 := p.diff(db + "/" + a.time)
				n, ok2 := p.diff(db + "/" + a.n)
				if ok1 && ok2 && n > 0 {
					b.Gauge(a.metric, "millisecond", round1(t/n/1000), kv...)
				}
			}
		}
	} else {
		p.close()
	}
	var checks []model.Check
	if dbs, err := showRows(ctx, conn, "SHOW DATABASES"); err == nil {
		for _, r := range dbs {
			name := str(r["name"])
			if name == "pgbouncer" {
				continue
			}
			kv := p.kvs("db", name)
			if v, ok := anyFloat(r["current_connections"]); ok {
				b.Gauge("pgbouncer.databases.current_connections", "connection", v, kv...)
			}
			if v, ok := anyFloat(r["pool_size"]); ok {
				b.Gauge("pgbouncer.databases.pool_size", "connection", v, kv...)
			}
			paused, _ := anyFloat(r["paused"])
			disabled, _ := anyFloat(r["disabled"])
			if paused > 0 || disabled > 0 {
				what := "en pause (PAUSE)"
				if disabled > 0 {
					what = "désactivée (DISABLE)"
				}
				checks = append(checks, model.Check{TS: ts, Name: "pgbouncer.database", Status: model.CheckWarn,
					Message: "base " + name + " " + what, Tags: tags(kv...)})
			} else {
				checks = append(checks, model.Check{TS: ts, Name: "pgbouncer.database", Status: model.CheckOK,
					Message: "base " + name + " active", Tags: tags(kv...)})
			}
		}
	}
	version := ""
	if v, err := showRows(ctx, conn, "SHOW VERSION"); err == nil && len(v) > 0 {
		version = str(v[0]["version"])
	}
	msg := "console d'administration lue"
	if version != "" {
		msg = version + " joignable"
	}
	checks = append(checks, p.okCheck(ts, tags, msg))
	return checks, "ok"
}
