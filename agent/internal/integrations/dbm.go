package integrations

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"s33d.fr/hypha/agent/internal/model"
)

// DBM (docs/CONTRAT-04.md § 30) : toutes les 5 minutes, les écarts de pg_stat_statements
// depuis l'échantillon précédent, les 50 premières requêtes par durée totale, texte
// normalisé tel que pg_stat_statements le rend, tronqué à 4 Kio.
const (
	dbmEvery     = 5 * time.Minute
	dbmTop       = 50
	dbmMaxQuery  = 4 << 10
	dbmCheckEach = time.Hour
	routeDBM     = "/api/v1/intake/dbm"
)

type dbmState struct {
	version   string // extversion de pg_stat_statements ; vide = absente
	checkedAt time.Time
	prev      map[string]dbmRow
	at        time.Time
}

// dbmRow : une ligne de pg_stat_statements (compteurs cumulés).
type dbmRow struct {
	queryID, db                  string
	calls, rows, hit, read       int64
	totalMS                      float64
}

func (r dbmRow) key(userid, toplevel string) string {
	return r.queryID + "|" + r.db + "|" + userid + "|" + toplevel
}

func (p *Postgres) collectDBM(ctx context.Context, now time.Time) {
	st := p.dbm
	setStatus := func(s string) {
		if p.deps.SetStatus != nil {
			p.deps.SetStatus("postgres.dbm", s)
		}
	}
	if p.cfg.DBM == "false" {
		setStatus("disabled")
		return
	}
	if !st.at.IsZero() && now.Sub(st.at) < dbmEvery-time.Second {
		return
	}
	if st.checkedAt.IsZero() || now.Sub(st.checkedAt) > dbmCheckEach {
		st.version = ""
		_ = p.conn.QueryRow(ctx, `SELECT extversion FROM pg_extension WHERE extname = 'pg_stat_statements'`).Scan(&st.version)
		st.checkedAt = now
	}
	if st.version == "" {
		setStatus("absent : extension pg_stat_statements absente de la base postgres")
		return
	}
	rows, err := p.readStatements(ctx, st.version)
	if err != nil {
		setStatus("erreur : " + err.Error())
		return
	}
	prev, prevAt := st.prev, st.at
	st.prev, st.at = rows, now
	if prev == nil || prevAt.IsZero() {
		setStatus("ok")
		return // premier échantillon : base des écarts
	}
	top := dbmDeltas(rows, prev, dbmTop)
	if len(top) == 0 {
		setStatus("ok")
		return
	}
	ids := make([]int64, 0, len(top))
	for _, q := range top {
		if n, err := strconv.ParseInt(q.QueryID, 10, 64); err == nil {
			ids = append(ids, n)
		}
	}
	texts := map[string]string{}
	trows, err := p.conn.Query(ctx, `SELECT queryid::text, query FROM pg_stat_statements(true) WHERE queryid = ANY($1::bigint[])`, ids)
	if err == nil {
		var id, q string
		_, _ = pgx.ForEachRow(trows, []any{&id, &q}, func() error {
			if _, ok := texts[id]; !ok {
				texts[id] = q
			}
			return nil
		})
	}
	for i := range top {
		top[i].Engine, top[i].Instance = "postgres", p.instance
		top[i].Query = model.Truncate(strings.TrimSpace(texts[top[i].QueryID]), dbmMaxQuery)
	}
	p.deps.Post(routeDBM, map[string]any{"ts": now.UnixMilli(), "queries": top})
	setStatus("ok")
}

// readStatements lit les compteurs sans les textes (pg_stat_statements(false) ne lit pas le
// fichier des textes) ; les colonnes dépendent de la version de l'extension.
func (p *Postgres) readStatements(ctx context.Context, ext string) (map[string]dbmRow, error) {
	total := "s.total_time"
	if versionAtLeast(ext, 1, 8) {
		total = "s.total_exec_time"
	}
	top := "true"
	if versionAtLeast(ext, 1, 9) {
		top = "s.toplevel"
	}
	q := fmt.Sprintf(`SELECT s.queryid::text, s.userid::text, %s::text, d.datname, s.calls, %s::float8, s.rows,
       s.shared_blks_hit, s.shared_blks_read
FROM pg_stat_statements(false) s JOIN pg_database d ON d.oid = s.dbid WHERE s.queryid IS NOT NULL`, top, total)
	rows, err := p.conn.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	out := map[string]dbmRow{}
	var r dbmRow
	var user, tl string
	_, err = pgx.ForEachRow(rows, []any{&r.queryID, &user, &tl, &r.db, &r.calls, &r.totalMS, &r.rows, &r.hit, &r.read}, func() error {
		out[r.key(user, tl)] = r
		return nil
	})
	return out, err
}

// versionAtLeast compare « 1.10 » à majeure.mineure.
func versionAtLeast(v string, major, minor int) bool {
	a, b, _ := strings.Cut(v, ".")
	x, _ := strconv.Atoi(a)
	y, _ := strconv.Atoi(b)
	return x > major || (x == major && y >= minor)
}

// dbmDeltas : écarts par (requête, base), toutes identités confondues, pour les lignes dont
// les appels ont augmenté ; une ligne remise à zéro (pg_stat_statements_reset) est ignorée.
// Les n premières par durée totale.
func dbmDeltas(cur, prev map[string]dbmRow, n int) []model.DBMQuery {
	agg := map[string]*model.DBMQuery{}
	for k, c := range cur {
		p, ok := prev[k]
		if !ok {
			p = dbmRow{} // nouvelle ligne : tout ce qu'elle porte date de l'intervalle
		}
		if c.calls < p.calls || c.totalMS < p.totalMS || c.calls == p.calls {
			continue
		}
		id := c.queryID + "|" + c.db
		q := agg[id]
		if q == nil {
			q = &model.DBMQuery{DB: c.db, QueryID: c.queryID}
			agg[id] = q
		}
		q.Calls += c.calls - p.calls
		q.TotalMS += c.totalMS - p.totalMS
		q.Rows += max(c.rows-p.rows, 0)
		q.SharedBlksHit += max(c.hit-p.hit, 0)
		q.SharedBlksRead += max(c.read-p.read, 0)
	}
	out := make([]model.DBMQuery, 0, len(agg))
	for _, q := range agg {
		q.TotalMS = float64(int64(q.TotalMS*1000)) / 1000
		out = append(out, *q)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TotalMS != out[j].TotalMS {
			return out[i].TotalMS > out[j].TotalMS
		}
		return out[i].QueryID < out[j].QueryID
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}
