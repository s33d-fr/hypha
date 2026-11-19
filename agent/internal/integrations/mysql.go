package integrations

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/hostfs"
	"s33d.fr/hypha/agent/internal/model"
)

// ── MySQL et MariaDB (§ 36) ────────────────────────────────────────────────────────────────
//
// Sans dsn : socket locale et compte root authentifié par la socket (unix_socket de MariaDB,
// auth_socket de MySQL : paquets Debian et Ubuntu) ; l'agent tourne en root. Sinon dsn vers un
// compte de supervision (PROCESS, REPLICATION CLIENT). Lecture seule : SHOW GLOBAL STATUS et
// VARIABLES, état de la réplication, taille des bases (toutes les 10 min).

const (
	mysqlTimeout   = 5 * time.Second
	mysqlSizeEvery = 10 * time.Minute
)

// Compteurs cumulés de SHOW GLOBAL STATUS rendus en débit (par intervalle de collecte).
var mysqlCounters = []struct{ status, metric, unit string }{
	{"Questions", "mysql.queries", "query"},
	{"Slow_queries", "mysql.slow_queries", "query"},
	{"Com_select", "mysql.com.select", "query"},
	{"Com_insert", "mysql.com.insert", "query"},
	{"Com_update", "mysql.com.update", "query"},
	{"Com_delete", "mysql.com.delete", "query"},
	{"Aborted_connects", "mysql.connections.aborted", "connection"},
	{"Connection_errors_max_connections", "mysql.connections.refused", "connection"},
	{"Bytes_received", "mysql.bytes.received", "byte"},
	{"Bytes_sent", "mysql.bytes.sent", "byte"},
	{"Innodb_row_lock_waits", "mysql.innodb.row_lock_waits", "wait"},
	{"Created_tmp_disk_tables", "mysql.tmp_disk_tables", "table"},
}

type MySQL struct {
	cfg    config.MySQLConfig
	det    detector
	db     *sql.DB
	prev   map[string]float64
	sizes  map[string]float64
	sizeAt time.Time
}

func newMySQL(cfg config.MySQLConfig) *MySQL {
	mode := cfg.Enabled
	if cfg.DSN != "" && mode == "auto" {
		mode = "true" // un dsn posé dit l'intention : ses échecs se signalent tout de suite
	}
	return &MySQL{cfg: cfg, det: detector{mode: mode}}
}

func (m *MySQL) name() string          { return "mysql" }
func (m *MySQL) period() time.Duration { return 30 * time.Second }
func (m *MySQL) close() {
	if m.db != nil {
		_ = m.db.Close()
		m.db = nil
	}
}

func (m *MySQL) socket() string { return hostfs.Root(m.cfg.Socket) }

func (m *MySQL) instance() string {
	if m.cfg.DSN != "" {
		if c, err := mysql.ParseDSN(m.cfg.DSN); err == nil {
			return c.Net + ":" + c.Addr
		}
		return "dsn"
	}
	return "unix:" + m.cfg.Socket
}

func (m *MySQL) open() (*sql.DB, error) {
	if m.db != nil {
		return m.db, nil
	}
	var c *mysql.Config
	if m.cfg.DSN != "" {
		var err error
		if c, err = mysql.ParseDSN(m.cfg.DSN); err != nil {
			return nil, fmt.Errorf("dsn : %w", err)
		}
	} else {
		c = mysql.NewConfig()
		c.User, c.Net, c.Addr = "root", "unix", m.socket()
	}
	c.Timeout, c.ReadTimeout, c.WriteTimeout = mysqlTimeout, mysqlTimeout, mysqlTimeout
	c.Params = map[string]string{"autocommit": "1"}
	conn, err := mysql.NewConnector(c)
	if err != nil {
		return nil, err
	}
	db := sql.OpenDB(conn)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(10 * time.Minute)
	m.db = db
	return db, nil
}

func (m *MySQL) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	switch {
	case m.cfg.Enabled == "false":
		return nil, "disabled"
	case m.cfg.DSN == "" && m.cfg.Enabled == "auto":
		if _, err := os.Stat(m.socket()); err != nil {
			return nil, "absent"
		}
	}
	if skip, st := m.det.skip(now); skip {
		return nil, st
	}
	ts := model.Ms(now)
	inst := m.instance()
	fail := func(err error) ([]model.Check, string) {
		m.close()
		// Socket trouvée seule (auto), jamais jointe : pas d'alerte, l'état de l'intégration le dit.
		if silent, st := m.det.failed(err.Error()); silent {
			return nil, st
		}
		return []model.Check{{TS: ts, Name: "mysql.can_connect", Status: model.CheckCrit, Message: err.Error(),
			Tags: tags("instance", inst)}}, "erreur : " + err.Error()
	}
	ctx, cancel := context.WithTimeout(ctx, 3*mysqlTimeout)
	defer cancel()
	db, err := m.open()
	if err != nil {
		return fail(err)
	}
	status, err := keyValues(ctx, db, "SHOW GLOBAL STATUS")
	if err != nil {
		return fail(fmt.Errorf("connexion : %w", err))
	}
	vars, err := keyValues(ctx, db, "SHOW GLOBAL VARIABLES WHERE Variable_name IN "+
		"('max_connections','version','version_comment','innodb_buffer_pool_size','read_only')")
	if err != nil {
		return fail(err)
	}
	m.det.ok()
	kv := []string{"instance", inst}
	num := func(m map[string]string, k string) (float64, bool) {
		v, err := strconv.ParseFloat(m[k], 64)
		return v, err == nil
	}
	if v, ok := num(status, "Uptime"); ok {
		b.Gauge("mysql.uptime", "second", v, kv...)
	}
	conns, okC := num(status, "Threads_connected")
	if okC {
		b.Gauge("mysql.connections.current", "connection", conns, kv...)
	}
	if v, ok := num(status, "Threads_running"); ok {
		b.Gauge("mysql.threads.running", "thread", v, kv...)
	}
	if mx, ok := num(vars, "max_connections"); ok && mx > 0 {
		b.Gauge("mysql.connections.max", "connection", mx, kv...)
		if okC {
			b.Gauge("mysql.connections.usage", "percent", round1(100*conns/mx), kv...)
		}
	}
	if tot, ok := num(status, "Innodb_buffer_pool_pages_total"); ok && tot > 0 {
		if free, ok := num(status, "Innodb_buffer_pool_pages_free"); ok {
			b.Gauge("mysql.innodb.buffer_pool.usage", "percent", round1(100*(tot-free)/tot), kv...)
		}
	}
	cur := map[string]float64{}
	for _, c := range mysqlCounters {
		if v, ok := num(status, c.status); ok {
			cur[c.status] = v
		}
	}
	for _, k := range []string{"Innodb_buffer_pool_read_requests", "Innodb_buffer_pool_reads"} {
		if v, ok := num(status, k); ok {
			cur[k] = v
		}
	}
	if m.prev != nil {
		for _, c := range mysqlCounters {
			if v, ok := cur[c.status]; ok {
				if d, ok := deltaF(v, m.prev[c.status]); ok {
					b.Add(c.metric, model.TypeCount, c.unit, d, kv...)
				}
			}
		}
		req, ok1 := deltaF(cur["Innodb_buffer_pool_read_requests"], m.prev["Innodb_buffer_pool_read_requests"])
		rd, ok2 := deltaF(cur["Innodb_buffer_pool_reads"], m.prev["Innodb_buffer_pool_reads"])
		if ok1 && ok2 && req > 0 {
			b.Gauge("mysql.innodb.buffer_pool.hit", "percent", round1(100*(1-rd/req)), kv...)
		}
	}
	m.prev = cur

	checks := []model.Check{}
	role := "primaire"
	repl, rerr := replication(ctx, db)
	switch {
	case rerr != nil:
		checks = append(checks, model.Check{TS: ts, Name: "mysql.replication", Status: model.CheckUnknown,
			Message: "état de la réplication illisible : " + rerr.Error(), Tags: tags(kv...)})
	case repl != nil:
		role = "réplique"
		if repl.lag >= 0 {
			b.Gauge("mysql.replication.lag", "second", repl.lag, kv...)
		}
		st, msg := model.CheckOK, fmt.Sprintf("réplique de %s, retard %s", repl.source, lagText(repl.lag))
		if !repl.io || !repl.sql {
			st = model.CheckCrit
			msg = fmt.Sprintf("réplication arrêtée (lecture %s, application %s)", yesNo(repl.io), yesNo(repl.sql))
			if repl.err != "" {
				msg += " : " + model.Truncate(repl.err, 300)
			}
		}
		checks = append(checks, model.Check{TS: ts, Name: "mysql.replication", Status: st, Message: msg, Tags: tags(kv...)})
	}
	if now.Sub(m.sizeAt) >= mysqlSizeEvery {
		if sizes, err := databaseSizes(ctx, db); err == nil {
			m.sizes, m.sizeAt = sizes, now
		}
	}
	names := make([]string, 0, len(m.sizes))
	for n := range m.sizes {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		b.Gauge("mysql.database.size", "byte", m.sizes[n], append(append([]string{}, kv...), "db", n)...)
	}
	product := "MySQL"
	if strings.Contains(strings.ToLower(vars["version"]+" "+vars["version_comment"]), "mariadb") {
		product = "MariaDB"
	}
	if strings.EqualFold(vars["read_only"], "ON") && role == "primaire" {
		role = "lecture seule"
	}
	checks = append(checks, model.Check{TS: ts, Name: "mysql.can_connect", Status: model.CheckOK,
		Message: fmt.Sprintf("%s %s joignable (%s)", product, strings.SplitN(vars["version"], "-", 2)[0], role), Tags: tags(kv...)})
	return checks, "ok"
}

func deltaF(cur, prev float64) (float64, bool) {
	if cur < prev {
		return 0, false
	}
	return cur - prev, true
}

func yesNo(b bool) string {
	if b {
		return "oui"
	}
	return "non"
}

func lagText(s float64) string {
	if s < 0 {
		return "inconnu"
	}
	return strconv.FormatFloat(s, 'f', 0, 64) + " s"
}

// keyValues : requête à deux colonnes (Variable_name, Value).
func keyValues(ctx context.Context, db *sql.DB, q string) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k string
		var v sql.NullString
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v.String
	}
	return out, rows.Err()
}

type mysqlRepl struct {
	source  string
	io, sql bool
	lag     float64 // -1 : inconnu
	err     string
}

// replication : SHOW REPLICA STATUS (MySQL 8.0.22+, MariaDB 10.5.1+), sinon SHOW SLAVE STATUS.
// nil : le serveur n'est réplique de personne.
func replication(ctx context.Context, db *sql.DB) (*mysqlRepl, error) {
	row, err := firstRow(ctx, db, "SHOW REPLICA STATUS")
	if err != nil {
		if row, err = firstRow(ctx, db, "SHOW SLAVE STATUS"); err != nil {
			return nil, err
		}
	}
	return parseReplication(row), nil
}

func parseReplication(row map[string]string) *mysqlRepl {
	if row == nil {
		return nil
	}
	pick := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := row[k]; ok {
				return v
			}
		}
		return ""
	}
	r := &mysqlRepl{lag: -1}
	r.source = pick("Source_Host", "Master_Host") + ":" + pick("Source_Port", "Master_Port")
	r.io = strings.EqualFold(pick("Replica_IO_Running", "Slave_IO_Running"), "Yes")
	r.sql = strings.EqualFold(pick("Replica_SQL_Running", "Slave_SQL_Running"), "Yes")
	if v, err := strconv.ParseFloat(pick("Seconds_Behind_Source", "Seconds_Behind_Master"), 64); err == nil {
		r.lag = v
	}
	r.err = strings.TrimSpace(pick("Last_IO_Error") + " " + pick("Last_SQL_Error"))
	return r
}

// firstRow : première ligne d'une requête, colonnes nommées ; nil si aucune ligne.
func firstRow(ctx context.Context, db *sql.DB, q string) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	if !rows.Next() {
		return nil, rows.Err()
	}
	vals := make([]sql.NullString, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(cols))
	for i, c := range cols {
		out[c] = vals[i].String
	}
	return out, nil
}

// databaseSizes : taille (données + index) des bases, hors bases du système.
func databaseSizes(ctx context.Context, db *sql.DB) (map[string]float64, error) {
	rows, err := db.QueryContext(ctx, `SELECT table_schema, COALESCE(SUM(data_length + index_length), 0)
		FROM information_schema.tables
		WHERE table_schema NOT IN ('mysql','information_schema','performance_schema','sys')
		GROUP BY table_schema`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var n string
		var v float64
		if err := rows.Scan(&n, &v); err != nil {
			return nil, err
		}
		out[n] = v
	}
	return out, rows.Err()
}
