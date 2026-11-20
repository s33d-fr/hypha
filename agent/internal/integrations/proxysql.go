package integrations

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── ProxySQL : interface d'administration (port 6032), tables stats ──────────────────────────
//
// dsn d'un compte de l'admin-stats_credentials (lecture des tables stats seulement) ou de
// l'admin-admin_credentials : « stats:motdepasse@tcp(127.0.0.1:6032)/ ». stats_mysql_global
// (connexions, requêtes, transactions), stats_mysql_connection_pool (état et latence de chaque
// serveur MySQL derrière ProxySQL).

var proxysqlGlobal = []struct {
	name, metric, unit string
	counter            bool
}{
	{"Active_Transactions", "proxysql.active_transactions", "transaction", false},
	{"Client_Connections_connected", "proxysql.client.connections_connected", "connection", false},
	{"Client_Connections_non_idle", "proxysql.client.connections_non_idle", "connection", false},
	{"Client_Connections_aborted", "proxysql.client.connections_aborted", "connection", true},
	{"Client_Connections_created", "proxysql.client.connections_created", "connection", true},
	{"Server_Connections_connected", "proxysql.server.connections_connected", "connection", false},
	{"Server_Connections_aborted", "proxysql.server.connections_aborted", "connection", true},
	{"Server_Connections_created", "proxysql.server.connections_created", "connection", true},
	{"Questions", "proxysql.questions", "query", true},
	{"Slow_queries", "proxysql.slow_queries", "query", true},
	{"ProxySQL_Uptime", "proxysql.uptime", "second", false},
	{"Query_Processor_time_nsec", "proxysql.query_processor_time", "nanosecond", true},
	{"Backend_query_time_nsec", "proxysql.backend_query_time", "nanosecond", true},
	{"mysql_backend_buffers_bytes", "proxysql.memory.backend_buffers", "byte", false},
	{"mysql_frontend_buffers_bytes", "proxysql.memory.frontend_buffers", "byte", false},
	{"MyHGM_myconnpoll_get_ok", "proxysql.pool.get_ok", "connection", true},
	{"MyHGM_myconnpoll_destroy", "proxysql.pool.destroyed", "connection", true},
	{"Query_Cache_Memory_bytes", "proxysql.query_cache.memory", "byte", false},
	{"Query_Cache_count_GET_OK", "proxysql.query_cache.hits", "hit", true},
	{"Query_Cache_count_GET", "proxysql.query_cache.gets", "request", true},
	{"ConnPool_get_conn_failure", "proxysql.pool.get_failure", "connection", true},
	{"generated_error_packets", "proxysql.generated_error_packets", "error", true},
	{"Access_Denied_Wrong_Password", "proxysql.access_denied.wrong_password", "connection", true},
	{"Access_Denied_Max_Connections", "proxysql.access_denied.max_connections", "connection", true},
}

var proxysqlBackendStatus = map[string]int{
	"ONLINE": model.CheckOK, "OFFLINE_SOFT": model.CheckWarn, "SHUNNED": model.CheckCrit,
	"OFFLINE_HARD": model.CheckCrit, "SHUNNED_REPLICATION_LAG": model.CheckWarn,
}

type ProxySQL struct {
	probe
	cfg config.DSNProbe
	db  *sql.DB
}

func newProxySQL(cfg config.DSNProbe) *ProxySQL {
	where := []string{}
	if c, err := mysql.ParseDSN(cfg.DSN); cfg.DSN != "" && err == nil {
		where = []string{"instance", c.Addr}
	}
	return &ProxySQL{cfg: cfg, probe: newProbe("proxysql", "proxysql.can_connect", intent(cfg.Enabled, cfg.DSN != ""), where...)}
}

func (p *ProxySQL) period() time.Duration { return 30 * time.Second }

func (p *ProxySQL) close() {
	if p.db != nil {
		_ = p.db.Close()
		p.db = nil
	}
}

func (p *ProxySQL) open() (*sql.DB, error) {
	if p.db != nil {
		return p.db, nil
	}
	if p.cfg.DSN == "" {
		return nil, fmt.Errorf("dsn non renseigné")
	}
	c, err := mysql.ParseDSN(p.cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("dsn : %w", err)
	}
	c.Timeout, c.ReadTimeout, c.WriteTimeout = 5*time.Second, 10*time.Second, 10*time.Second
	conn, err := mysql.NewConnector(c)
	if err != nil {
		return nil, err
	}
	db := sql.OpenDB(conn)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(10 * time.Minute)
	p.db = db
	return db, nil
}

// sqlRows : lignes d'une requête, colonne (en minuscules) → valeur.
func sqlRows(ctx context.Context, db *sql.DB, q string) ([]map[string]any, error) {
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		m := make(map[string]any, len(cols))
		for i, c := range cols {
			m[strings.ToLower(c)] = vals[i]
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (p *ProxySQL) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := p.det.skip(now); skip {
		return nil, st
	}
	ts := model.Ms(now)
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	db, err := p.open()
	var global []map[string]any
	if err == nil {
		global, err = sqlRows(ctx, db, "SELECT Variable_Name, Variable_Value FROM stats_mysql_global")
	}
	if err != nil {
		p.close()
		return p.fail(ts, tags, err)
	}
	p.det.ok()
	p.begin()
	defer p.end()
	vals := map[string]float64{}
	for _, r := range global {
		if v, ok := anyFloat(r["variable_value"]); ok {
			vals[str(r["variable_name"])] = v
		}
	}
	kv := p.kvs()
	for _, g := range proxysqlGlobal {
		v, ok := vals[g.name]
		if !ok {
			continue
		}
		if g.counter {
			p.count(b, g.metric, g.unit, g.name, v, kv...)
		} else {
			b.Gauge(g.metric, g.unit, v, kv...)
		}
	}
	if hits, ok := p.diff("Query_Cache_count_GET_OK"); ok {
		if gets, ok := p.diff("Query_Cache_count_GET"); ok && gets > 0 {
			b.Gauge("proxysql.query_cache.hit_rate", "percent", round1(100*hits/gets), kv...)
		}
	}
	var checks []model.Check
	pool, err := sqlRows(ctx, db, "SELECT hostgroup, srv_host, srv_port, status, ConnUsed, ConnFree, ConnOK, ConnERR, "+
		"Queries, Bytes_data_sent, Bytes_data_recv, Latency_us FROM stats_mysql_connection_pool")
	if err == nil {
		for _, r := range pool {
			srv := str(r["srv_host"]) + ":" + str(r["srv_port"])
			hg := str(r["hostgroup"])
			bkv := p.kvs("hostgroup", hg, "backend", srv)
			gauge := func(col, metric, unit string) {
				if v, ok := anyFloat(r[col]); ok {
					b.Gauge(metric, unit, v, bkv...)
				}
			}
			counter := func(col, metric, unit string) {
				if v, ok := anyFloat(r[col]); ok {
					p.count(b, metric, unit, hg+"/"+srv+"/"+col, v, bkv...)
				}
			}
			gauge("connused", "proxysql.pool.connections_used", "connection")
			gauge("connfree", "proxysql.pool.connections_free", "connection")
			counter("connok", "proxysql.pool.connections_ok", "connection")
			counter("connerr", "proxysql.pool.connections_error", "connection")
			counter("queries", "proxysql.pool.queries", "query")
			counter("bytes_data_sent", "proxysql.pool.bytes_sent", "byte")
			counter("bytes_data_recv", "proxysql.pool.bytes_received", "byte")
			if v, ok := anyFloat(r["latency_us"]); ok {
				b.Gauge("proxysql.pool.latency_ms", "millisecond", round1(v/1000), bkv...)
			}
			status := str(r["status"])
			st, known := proxysqlBackendStatus[status]
			if !known {
				st = model.CheckUnknown
			}
			online := 0.0
			if status == "ONLINE" {
				online = 1
			}
			b.Gauge("proxysql.backend.online", "", online, bkv...)
			checks = append(checks, model.Check{TS: ts, Name: "proxysql.backend.status", Status: st,
				Message: fmt.Sprintf("serveur %s (groupe %s) : %s", srv, hg, status), Tags: tags(bkv...)})
		}
	}
	version := ""
	if v, err := sqlRows(ctx, db, "SELECT variable_value FROM global_variables WHERE variable_name = 'admin-version'"); err == nil && len(v) > 0 {
		version = str(v[0]["variable_value"])
	}
	msg := "interface d'administration lue"
	if version != "" {
		msg = "ProxySQL " + strings.SplitN(version, "-", 2)[0] + " joignable"
	}
	checks = append(checks, p.okCheck(ts, tags, msg))
	return checks, "ok"
}
