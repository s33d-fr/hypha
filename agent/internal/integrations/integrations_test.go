package integrations

import (
	"sort"
	"strings"
	"testing"

	"s33d.fr/hypha/agent/internal/model"
)

func testTags(kv ...string) map[string]string {
	m := map[string]string{"host": "h"}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}

// index rend les séries par « métrique{étiquettes hors host} ».
func index(series []model.Series) map[string]model.Series {
	out := map[string]model.Series{}
	for _, s := range series {
		var kv []string
		for k, v := range s.Tags {
			if k != "host" {
				kv = append(kv, k+"="+v)
			}
		}
		sort.Strings(kv)
		out[s.Metric+"{"+strings.Join(kv, ",")+"}"] = s
	}
	return out
}

func expectValue(t *testing.T, got map[string]model.Series, key string, want float64) {
	t.Helper()
	s, ok := got[key]
	if !ok {
		t.Errorf("%s absente", key)
		return
	}
	if v := s.Points[0].Value; v < want-1e-9 || v > want+1e-9 {
		t.Errorf("%s = %v, attendu %v", key, v, want)
	}
}

func expectAbsent(t *testing.T, got map[string]model.Series, key string) {
	t.Helper()
	if _, ok := got[key]; ok {
		t.Errorf("%s ne devrait pas être émise", key)
	}
}

func TestPostgresSeries(t *testing.T) {
	prev := &pgSnapshot{
		version: 160004, maxConn: 100,
		connByState: map[string]int64{"active": 2, "idle": 8},
		dbs: map[string]pgDB{"app": {commits: 1000, rollbacks: 10, deadlocks: 1, tempBytes: 0, fetched: 5000,
			inserted: 100, updated: 50, deleted: 5, hit: 9000, reads: 1000}},
		bgw: &pgBgwriter{checkpointsTimed: 10, checkpointsRequested: 2, buffersCheckpoint: 500, buffersClean: 40,
			maxwrittenClean: 0, buffersAlloc: 9000, buffersBackend: 100, writeTime: 1000, syncTime: 100, hasBackend: true},
	}
	cur := &pgSnapshot{
		version: 160004, maxConn: 100,
		connByState: map[string]int64{"active": 3, "idle": 9},
		dbs: map[string]pgDB{
			// rollbacks repart de 5 : statistiques remises à zéro, pas de débit négatif.
			"app":   {commits: 1150, rollbacks: 5, deadlocks: 1, tempBytes: 15 << 20, fetched: 5300, inserted: 130, updated: 50, deleted: 5, hit: 9900, reads: 1100},
			"neuve": {commits: 10},
		},
		locks:    map[string]int64{"AccessShareLock": 3, "RowExclusiveLock": 1},
		sizes:    map[string]int64{"app": 1 << 30},
		replicas: []pgReplica{{name: "db-02", addr: "192.0.2.6", lagBytes: 4096}},
		bgw: &pgBgwriter{checkpointsTimed: 11, checkpointsRequested: 2, buffersCheckpoint: 650, buffersClean: 40,
			maxwrittenClean: 0, buffersAlloc: 9500, buffersBackend: 130, writeTime: 1300, syncTime: 120, hasBackend: true},
	}
	b := model.NewSeriesBuilder(1000, testTags)
	pgSeries(b, cur, prev, 15)
	got := index(b.Series())

	expectValue(t, got, "postgresql.connections{state=active}", 3)
	expectValue(t, got, "postgresql.connections{state=idle}", 9)
	expectValue(t, got, "postgresql.max_connections{}", 100)
	expectValue(t, got, "postgresql.percent_usage_connections{}", 12)
	expectValue(t, got, "postgresql.is_primary{}", 1)
	expectValue(t, got, "postgresql.locks{lock_mode=AccessShareLock}", 3)
	expectValue(t, got, "postgresql.database_size{db=app}", 1<<30)
	expectValue(t, got, "postgresql.replication.wal_lag_bytes{client_addr=192.0.2.6,replica=db-02}", 4096)
	expectValue(t, got, "postgresql.commits{db=app}", 10)       // 150 en 15 s
	expectValue(t, got, "postgresql.rows_fetched{db=app}", 20)  // 300 en 15 s
	expectValue(t, got, "postgresql.rows_inserted{db=app}", 2)  // 30 en 15 s
	expectValue(t, got, "postgresql.temp_bytes{db=app}", 1<<20) // 15 Mio en 15 s
	expectValue(t, got, "postgresql.deadlocks{db=app}", 0)      // inchangé : débit nul, émis
	expectValue(t, got, "postgresql.buffer_hit{db=app}", 90)    // 900 trouvés sur 1 000 demandés
	expectAbsent(t, got, "postgresql.rollbacks{db=app}")        // remis à zéro
	expectAbsent(t, got, "postgresql.commits{db=neuve}")        // base apparue : pas de relevé précédent
	expectAbsent(t, got, "postgresql.replication_delay{}")      // primaire
	expectValue(t, got, "postgresql.bgwriter.checkpoints_timed{}", 1)
	expectValue(t, got, "postgresql.bgwriter.buffers_checkpoint{}", 150)
	expectValue(t, got, "postgresql.bgwriter.buffers_backend{}", 30)
	expectValue(t, got, "postgresql.bgwriter.write_time{}", 300)
	if s := got["postgresql.bgwriter.checkpoints_timed{}"]; s.Type != model.TypeCount {
		t.Errorf("bgwriter : type %q, attendu count", s.Type)
	}

	// Secondaire, premier relevé : retard de réplication, pas de débits.
	delay := 2.5
	standby := &pgSnapshot{version: 170002, maxConn: 50, inRecovery: true, connByState: map[string]int64{"active": 1},
		dbs: map[string]pgDB{"app": {}}, standbyDelay: &delay, bgw: &pgBgwriter{}}
	b = model.NewSeriesBuilder(1000, testTags)
	pgSeries(b, standby, nil, 0)
	got = index(b.Series())
	expectValue(t, got, "postgresql.is_primary{}", 0)
	expectValue(t, got, "postgresql.replication_delay{}", 2.5)
	expectAbsent(t, got, "postgresql.commits{db=app}")
	expectAbsent(t, got, "postgresql.bgwriter.buffers_backend{}")
}
