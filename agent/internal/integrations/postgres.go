package integrations

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/hostfs"
	"s33d.fr/hypha/agent/internal/model"
)

// Requêtes de chaque cycle : vues de statistiques seulement, envoyées en un seul lot,
// dans une session en lecture seule avec un délai d'exécution de 5 s.
const (
	pgQuerySettings = `SELECT current_setting('server_version'), current_setting('server_version_num')::int,
       current_setting('max_connections')::int, pg_is_in_recovery()`
	pgQueryConnections = `SELECT coalesce(state, 'unknown'), count(*) FROM pg_stat_activity
WHERE backend_type = 'client backend' GROUP BY 1`
	pgQueryDatabases = `SELECT datname, xact_commit, xact_rollback, deadlocks, temp_bytes, tup_fetched,
       tup_inserted, tup_updated, tup_deleted, blks_hit, blks_read
FROM pg_stat_database WHERE datname IS NOT NULL AND datname NOT IN ('template0', 'template1')`
	pgQueryLocks = `SELECT mode, count(*) FROM pg_locks GROUP BY mode`
	// Activité (§ 30) : requêtes en attente d'un verrou, plus longue requête active,
	// transactions ouvertes et inactives.
	pgQueryActivity = `SELECT count(*) FILTER (WHERE wait_event_type = 'Lock'),
       coalesce(max(EXTRACT(EPOCH FROM now() - query_start)) FILTER (WHERE state = 'active' AND pid <> pg_backend_pid()), 0)::float8,
       count(*) FILTER (WHERE state IN ('idle in transaction', 'idle in transaction (aborted)'))
FROM pg_stat_activity WHERE backend_type = 'client backend'`
	// Retard d'un secondaire : nul s'il a rejoué tout ce qu'il a reçu, sinon l'âge de la
	// dernière transaction rejouée (formule de l'intégration Datadog).
	pgQueryStandbyDelay = `SELECT (CASE WHEN pg_last_wal_receive_lsn() = pg_last_wal_replay_lsn() THEN 0
       ELSE GREATEST(0, EXTRACT(EPOCH FROM now() - pg_last_xact_replay_timestamp())) END)::float8`
	pgQueryReplicas = `SELECT coalesce(application_name, ''), coalesce(host(client_addr), ''),
       coalesce(pg_wal_lsn_diff(pg_current_wal_lsn(), replay_lsn), 0)::float8 FROM pg_stat_replication`
	// PostgreSQL 17 a déplacé les points de contrôle dans pg_stat_checkpointer et retiré
	// buffers_backend de pg_stat_bgwriter.
	pgQueryBgwriter17 = `SELECT b.buffers_clean, b.maxwritten_clean, b.buffers_alloc, c.num_timed, c.num_requested,
       c.buffers_written, c.write_time::float8, c.sync_time::float8
FROM pg_stat_bgwriter b, pg_stat_checkpointer c`
	pgQueryBgwriterOld = `SELECT buffers_clean, maxwritten_clean, buffers_alloc, checkpoints_timed, checkpoints_req,
       buffers_checkpoint, checkpoint_write_time::float8, checkpoint_sync_time::float8, buffers_backend
FROM pg_stat_bgwriter`
	// La taille des bases demande de parcourir leurs fichiers : une fois par minute seulement.
	pgQuerySizes   = `SELECT datname, pg_database_size(datname) FROM pg_database WHERE datallowconn AND NOT datistemplate`
	pgSizesEvery   = time.Minute
	pgCycleTimeout = 10 * time.Second
)

// Postgres garde une connexion ouverte entre les cycles (rouverte après une erreur).
type Postgres struct {
	cfg      config.PostgresConfig
	log      *slog.Logger
	conn     *pgx.Conn
	instance string
	prev     *pgSnapshot
	prevAt   time.Time
	sizesAt  time.Time
	dbm      *dbmState
	deps     Deps
}

func newPostgres(cfg config.PostgresConfig, d Deps) *Postgres {
	p := &Postgres{cfg: cfg, log: d.Log, deps: d, dbm: &dbmState{}}
	p.instance = p.socketPath()
	if cfg.DSN != "" {
		// Étiquette sans mot de passe : hôte, port et base seulement.
		if c, err := pgx.ParseConfig(cfg.DSN); err == nil {
			p.instance = fmt.Sprintf("%s:%d/%s", c.Host, c.Port, c.Database)
		} else {
			p.instance = "dsn"
		}
	}
	return p
}

func (p *Postgres) name() string { return "postgres" }

func (p *Postgres) socketPath() string {
	return filepath.Join(hostfs.Root(p.cfg.SocketDir), fmt.Sprintf(".s.PGSQL.%d", p.cfg.Port))
}

func (p *Postgres) close() {
	if p.conn != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		p.conn.Close(ctx)
		cancel()
		p.conn = nil
	}
}

func (p *Postgres) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	switch {
	case p.cfg.Enabled == "false":
		return nil, "disabled"
	case p.cfg.DSN == "" && p.cfg.Enabled == "auto":
		if _, err := os.Stat(p.socketPath()); err != nil {
			return nil, "absent"
		}
	}
	ctx, cancel := context.WithTimeout(ctx, pgCycleTimeout)
	defer cancel()
	ts := model.Ms(now)
	snap, err := p.snapshot(ctx, now)
	if err != nil {
		p.close()
		return []model.Check{{TS: ts, Name: "postgres.can_connect", Status: model.CheckCrit,
			Message: err.Error(), Tags: tags("instance", p.instance)}}, "erreur : " + err.Error()
	}
	dt := 0.0
	if p.prev != nil {
		dt = now.Sub(p.prevAt).Seconds()
	}
	pgSeries(b, snap, p.prev, dt)
	p.prev, p.prevAt = snap, now
	p.collectDBM(ctx, now)
	role := "primaire"
	if snap.inRecovery {
		role = "secondaire"
	}
	return []model.Check{{TS: ts, Name: "postgres.can_connect", Status: model.CheckOK,
		Message: fmt.Sprintf("PostgreSQL %s joignable (%s)", snap.versionText, role), Tags: tags("instance", p.instance)}}, "ok"
}

func (p *Postgres) connect(ctx context.Context) (*pgx.Conn, error) {
	var cfg *pgx.ConnConfig
	var err error
	if p.cfg.DSN != "" {
		if cfg, err = pgx.ParseConfig(p.cfg.DSN); err != nil {
			return nil, fmt.Errorf("dsn : %w", err)
		}
	} else {
		cfg, err = pgx.ParseConfig(fmt.Sprintf("host=%s port=%d user=%s dbname=postgres sslmode=disable",
			hostfs.Root(p.cfg.SocketDir), p.cfg.Port, p.cfg.User))
		if err != nil {
			return nil, err
		}
		// Authentification peer : PostgreSQL vérifie l'utilisateur système qui ouvre la
		// socket. L'agent tourne en root : la socket est ouverte par un court sous-processus
		// lancé sous l'identité de p.cfg.User, qui passe la connexion ouverte à l'agent.
		dial, err := peerDialer(p.cfg.User)
		if err != nil {
			return nil, err
		}
		cfg.DialFunc = dial
	}
	cfg.ConnectTimeout = 5 * time.Second
	cfg.RuntimeParams["application_name"] = "hypha-agent"
	cfg.RuntimeParams["statement_timeout"] = "5000"
	cfg.RuntimeParams["default_transaction_read_only"] = "on"
	// Protocole simple : aucune requête préparée laissée sur le serveur (compatible pgbouncer).
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	return pgx.ConnectConfig(ctx, cfg)
}

// pgSnapshot : un relevé complet. Les compteurs sont cumulés depuis le démarrage de
// PostgreSQL (ou la dernière remise à zéro des statistiques).
type pgSnapshot struct {
	versionText  string
	version      int
	maxConn      int
	inRecovery   bool
	connByState  map[string]int64
	dbs          map[string]pgDB
	locks        map[string]int64
	standbyDelay *float64 // secondaire seulement ; nil si rien n'a encore été rejoué
	replicas     []pgReplica
	bgw          *pgBgwriter
	sizes        map[string]int64 // nil hors relevé de taille
	activity     *pgActivity
}

type pgActivity struct {
	waiting, idleInTx int64
	longest           float64
}

type pgDB struct {
	commits, rollbacks, deadlocks, tempBytes        int64
	fetched, inserted, updated, deleted, hit, reads int64
}

type pgReplica struct {
	name, addr string
	lagBytes   float64
}

type pgBgwriter struct {
	checkpointsTimed, checkpointsRequested, buffersCheckpoint   int64
	buffersClean, maxwrittenClean, buffersAlloc, buffersBackend int64
	writeTime, syncTime                                         float64
	hasBackend                                                  bool
}

func (p *Postgres) snapshot(ctx context.Context, now time.Time) (*pgSnapshot, error) {
	if p.conn == nil || p.conn.IsClosed() {
		conn, err := p.connect(ctx)
		if err != nil {
			return nil, fmt.Errorf("connexion impossible : %w", err)
		}
		p.conn = conn
	}
	s := &pgSnapshot{connByState: map[string]int64{}, dbs: map[string]pgDB{}, locks: map[string]int64{}}
	if err := p.conn.QueryRow(ctx, pgQuerySettings).Scan(&s.versionText, &s.version, &s.maxConn, &s.inRecovery); err != nil {
		return nil, err
	}
	if s.version < 100000 {
		return nil, fmt.Errorf("PostgreSQL %s non pris en charge (version 10 au moins)", s.versionText)
	}

	batch := &pgx.Batch{}
	batch.Queue(pgQueryConnections).Query(func(rows pgx.Rows) error {
		var st string
		var n int64
		_, err := pgx.ForEachRow(rows, []any{&st, &n}, func() error { s.connByState[st] = n; return nil })
		return err
	})
	batch.Queue(pgQueryDatabases).Query(func(rows pgx.Rows) error {
		var name string
		var d pgDB
		_, err := pgx.ForEachRow(rows, []any{&name, &d.commits, &d.rollbacks, &d.deadlocks, &d.tempBytes, &d.fetched,
			&d.inserted, &d.updated, &d.deleted, &d.hit, &d.reads}, func() error { s.dbs[name] = d; return nil })
		return err
	})
	batch.Queue(pgQueryLocks).Query(func(rows pgx.Rows) error {
		var mode string
		var n int64
		_, err := pgx.ForEachRow(rows, []any{&mode, &n}, func() error { s.locks[mode] = n; return nil })
		return err
	})
	act := &pgActivity{}
	batch.Queue(pgQueryActivity).QueryRow(func(row pgx.Row) error {
		return row.Scan(&act.waiting, &act.longest, &act.idleInTx)
	})
	s.activity = act
	if s.inRecovery {
		batch.Queue(pgQueryStandbyDelay).QueryRow(func(row pgx.Row) error { return row.Scan(&s.standbyDelay) })
	} else {
		batch.Queue(pgQueryReplicas).Query(func(rows pgx.Rows) error {
			var r pgReplica
			_, err := pgx.ForEachRow(rows, []any{&r.name, &r.addr, &r.lagBytes}, func() error {
				s.replicas = append(s.replicas, r)
				return nil
			})
			return err
		})
	}
	bgw := &pgBgwriter{}
	if s.version >= 170000 {
		batch.Queue(pgQueryBgwriter17).QueryRow(func(row pgx.Row) error {
			return row.Scan(&bgw.buffersClean, &bgw.maxwrittenClean, &bgw.buffersAlloc, &bgw.checkpointsTimed,
				&bgw.checkpointsRequested, &bgw.buffersCheckpoint, &bgw.writeTime, &bgw.syncTime)
		})
	} else {
		bgw.hasBackend = true
		batch.Queue(pgQueryBgwriterOld).QueryRow(func(row pgx.Row) error {
			return row.Scan(&bgw.buffersClean, &bgw.maxwrittenClean, &bgw.buffersAlloc, &bgw.checkpointsTimed,
				&bgw.checkpointsRequested, &bgw.buffersCheckpoint, &bgw.writeTime, &bgw.syncTime, &bgw.buffersBackend)
		})
	}
	s.bgw = bgw
	measureSizes := now.Sub(p.sizesAt) >= pgSizesEvery
	if measureSizes {
		s.sizes = map[string]int64{}
		batch.Queue(pgQuerySizes).Query(func(rows pgx.Rows) error {
			var name string
			var n int64
			_, err := pgx.ForEachRow(rows, []any{&name, &n}, func() error { s.sizes[name] = n; return nil })
			return err
		})
	}
	if err := p.conn.SendBatch(ctx, batch).Close(); err != nil {
		return nil, err
	}
	if measureSizes {
		p.sizesAt = now
	}
	return s, nil
}

// pgSeries construit les séries d'un relevé ; les débits demandent le relevé précédent.
func pgSeries(b *model.SeriesBuilder, cur, prev *pgSnapshot, dt float64) {
	var total int64
	for _, st := range model.SortedKeys(cur.connByState) {
		n := cur.connByState[st]
		total += n
		b.Gauge("postgresql.connections", "count", float64(n), "state", st)
	}
	b.Gauge("postgresql.max_connections", "count", float64(cur.maxConn))
	if cur.maxConn > 0 {
		b.Gauge("postgresql.percent_usage_connections", "percent", float64(total)/float64(cur.maxConn)*100)
	}
	primary := 1.0
	if cur.inRecovery {
		primary = 0
	}
	b.Gauge("postgresql.is_primary", "", primary)
	for _, mode := range model.SortedKeys(cur.locks) {
		b.Gauge("postgresql.locks", "count", float64(cur.locks[mode]), "lock_mode", mode)
	}
	if a := cur.activity; a != nil {
		b.Gauge("postgresql.waiting_queries", "count", float64(a.waiting))
		b.Gauge("postgresql.longest_query_s", "second", a.longest)
		b.Gauge("postgresql.idle_in_transaction", "count", float64(a.idleInTx))
	}
	for _, db := range model.SortedKeys(cur.sizes) {
		b.Gauge("postgresql.database_size", "byte", float64(cur.sizes[db]), "db", db)
	}
	if cur.inRecovery && cur.standbyDelay != nil {
		b.Gauge("postgresql.replication_delay", "second", *cur.standbyDelay)
	}
	for _, r := range cur.replicas {
		b.Gauge("postgresql.replication.wal_lag_bytes", "byte", r.lagBytes, "replica", r.name, "client_addr", r.addr)
	}

	if prev == nil || dt <= 0 {
		return
	}
	for _, db := range model.SortedKeys(cur.dbs) {
		c := cur.dbs[db]
		p, had := prev.dbs[db]
		if !had {
			continue
		}
		for _, m := range []struct {
			name, unit string
			c, p       int64
		}{
			{"postgresql.commits", "operation_per_second", c.commits, p.commits},
			{"postgresql.rollbacks", "operation_per_second", c.rollbacks, p.rollbacks},
			{"postgresql.deadlocks", "operation_per_second", c.deadlocks, p.deadlocks},
			{"postgresql.temp_bytes", "byte_per_second", c.tempBytes, p.tempBytes},
			{"postgresql.rows_fetched", "operation_per_second", c.fetched, p.fetched},
			{"postgresql.rows_inserted", "operation_per_second", c.inserted, p.inserted},
			{"postgresql.rows_updated", "operation_per_second", c.updated, p.updated},
			{"postgresql.rows_deleted", "operation_per_second", c.deleted, p.deleted},
		} {
			if v, ok := rateOf(m.c, m.p, dt); ok {
				b.Gauge(m.name, m.unit, v, "db", db)
			}
		}
		// Part des blocs demandés trouvés en mémoire sur l'intervalle (et non depuis le
		// démarrage, où une chute récente serait noyée).
		hits, reads := c.hit-p.hit, c.reads-p.reads
		if hits >= 0 && reads >= 0 && hits+reads > 0 {
			b.Gauge("postgresql.buffer_hit", "percent", float64(hits)/float64(hits+reads)*100, "db", db)
		}
	}
	if cur.bgw == nil || prev.bgw == nil {
		return
	}
	c, p := cur.bgw, prev.bgw
	type counter struct {
		name string
		c, p int64
	}
	counts := []counter{
		{"postgresql.bgwriter.checkpoints_timed", c.checkpointsTimed, p.checkpointsTimed},
		{"postgresql.bgwriter.checkpoints_requested", c.checkpointsRequested, p.checkpointsRequested},
		{"postgresql.bgwriter.buffers_checkpoint", c.buffersCheckpoint, p.buffersCheckpoint},
		{"postgresql.bgwriter.buffers_clean", c.buffersClean, p.buffersClean},
		{"postgresql.bgwriter.maxwritten_clean", c.maxwrittenClean, p.maxwrittenClean},
		{"postgresql.bgwriter.buffers_alloc", c.buffersAlloc, p.buffersAlloc},
	}
	if c.hasBackend && p.hasBackend {
		counts = append(counts, counter{"postgresql.bgwriter.buffers_backend", c.buffersBackend, p.buffersBackend})
	}
	// Compteurs : la somme sur l'intervalle (type count), comme l'agent Datadog.
	for _, m := range counts {
		if m.c >= m.p {
			b.Add(m.name, model.TypeCount, "count", float64(m.c-m.p))
		}
	}
	if c.writeTime >= p.writeTime {
		b.Add("postgresql.bgwriter.write_time", model.TypeCount, "millisecond", c.writeTime-p.writeTime)
	}
	if c.syncTime >= p.syncTime {
		b.Add("postgresql.bgwriter.sync_time", model.TypeCount, "millisecond", c.syncTime-p.syncTime)
	}
}
