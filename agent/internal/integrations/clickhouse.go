package integrations

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── ClickHouse : interface HTTP (8123), tables system.metrics, events, asynchronous_metrics ──
//
// Un compte en lecture seule suffit (readonly = 1) ; sans compte, l'utilisateur « default ».
// L'identité passe par les en-têtes X-ClickHouse-User et X-ClickHouse-Key, jamais par l'URL.

// Valeurs instantanées de system.metrics.
var chMetrics = map[string][2]string{
	"Query":                                {"clickhouse.query.active", "query"},
	"Merge":                                {"clickhouse.merge.active", "merge"},
	"PartMutation":                         {"clickhouse.mutation.active", "mutation"},
	"ReplicatedFetch":                      {"clickhouse.replicated.fetch.active", "fetch"},
	"ReplicatedSend":                       {"clickhouse.replicated.send.active", "send"},
	"TCPConnection":                        {"clickhouse.connection.tcp", "connection"},
	"HTTPConnection":                       {"clickhouse.connection.http", "connection"},
	"MySQLConnection":                      {"clickhouse.connection.mysql", "connection"},
	"PostgreSQLConnection":                 {"clickhouse.connection.postgresql", "connection"},
	"InterserverConnection":                {"clickhouse.connection.interserver", "connection"},
	"ReadonlyReplica":                      {"clickhouse.table.replicated.readonly", "table"},
	"MemoryTracking":                       {"clickhouse.memory.tracked", "byte"},
	"DelayedInserts":                       {"clickhouse.insert.delayed.active", "query"},
	"BackgroundMergesAndMutationsPoolTask": {"clickhouse.background_pool.merges.tasks", "task"},
	"ZooKeeperSession":                     {"clickhouse.zookeeper.session", "session"},
	"OpenFileForRead":                      {"clickhouse.file.open.read", "file"},
	"OpenFileForWrite":                     {"clickhouse.file.open.write", "file"},
}

// Compteurs cumulés de system.events (écart par intervalle).
var chEvents = map[string][2]string{
	"Query":                        {"clickhouse.query.count", "query"},
	"SelectQuery":                  {"clickhouse.query.select.count", "query"},
	"InsertQuery":                  {"clickhouse.query.insert.count", "query"},
	"FailedQuery":                  {"clickhouse.query.failed.count", "query"},
	"FailedSelectQuery":            {"clickhouse.query.select.failed.count", "query"},
	"FailedInsertQuery":            {"clickhouse.query.insert.failed.count", "query"},
	"InsertedRows":                 {"clickhouse.insert.rows", "row"},
	"InsertedBytes":                {"clickhouse.insert.bytes", "byte"},
	"SelectedRows":                 {"clickhouse.select.rows", "row"},
	"SelectedBytes":                {"clickhouse.select.bytes", "byte"},
	"MergedRows":                   {"clickhouse.merge.rows", "row"},
	"DelayedInserts":               {"clickhouse.insert.delayed", "query"},
	"RejectedInserts":              {"clickhouse.insert.rejected", "query"},
	"QueryTimeMicroseconds":        {"clickhouse.query.time", "microsecond"},
	"ZooKeeperHardwareExceptions":  {"clickhouse.zookeeper.exceptions", "error"},
	"ReplicatedPartFailedFetches":  {"clickhouse.replicated.fetch.failed", "fetch"},
	"DistributedConnectionFailTry": {"clickhouse.distributed.connection.failed", "connection"},
}

// Valeurs de system.asynchronous_metrics (relevées en tâche de fond par ClickHouse).
var chAsync = map[string][2]string{
	"Uptime":                      {"clickhouse.uptime", "second"},
	"MaxPartCountForPartition":    {"clickhouse.part.max_per_partition", "part"},
	"ReplicasMaxAbsoluteDelay":    {"clickhouse.replica.delay.max", "second"},
	"ReplicasMaxQueueSize":        {"clickhouse.replica.queue.max", "task"},
	"NumberOfDatabases":           {"clickhouse.database.count", "database"},
	"NumberOfTables":              {"clickhouse.table.count", "table"},
	"TotalBytesOfMergeTreeTables": {"clickhouse.table.mergetree.size", "byte"},
	"TotalRowsOfMergeTreeTables":  {"clickhouse.table.mergetree.rows", "row"},
	"TotalPartsOfMergeTreeTables": {"clickhouse.table.mergetree.parts", "part"},
	"MemoryResident":              {"clickhouse.memory.resident", "byte"},
}

type ClickHouse struct {
	probe
	cfg  config.ServiceProbe
	http *httpProbe
}

func newClickHouse(cfg config.ServiceProbe) *ClickHouse {
	h := newHTTPProbe(cfg.URL, "", "", cfg.Insecure)
	h.headers = map[string]string{}
	if cfg.Username != "" {
		h.headers["X-ClickHouse-User"] = cfg.Username
	}
	if cfg.Password != "" {
		h.headers["X-ClickHouse-Key"] = cfg.Password
	}
	explicit := cfg.Username != "" || (cfg.URL != "" && cfg.URL != "http://127.0.0.1:8123")
	return &ClickHouse{cfg: cfg, http: h,
		probe: newProbe("clickhouse", "clickhouse.can_connect", intent(cfg.Enabled, explicit), "instance", cfg.URL)}
}

func (c *ClickHouse) period() time.Duration { return 30 * time.Second }

// query : une requête SELECT en TSV, lignes découpées en champs.
func (c *ClickHouse) query(ctx context.Context, q string) ([][]string, error) {
	body, code, err := c.http.do(ctx, http.MethodPost, "/?default_format=TabSeparated", strings.NewReader(q), nil)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		msg := strings.TrimSpace(string(body))
		if i := strings.Index(msg, ". ("); i > 0 {
			msg = msg[:i]
		}
		if code == http.StatusUnauthorized || code == http.StatusForbidden || strings.Contains(msg, "Authentication failed") {
			return nil, fmt.Errorf("identifiants refusés (HTTP %d)", code)
		}
		return nil, fmt.Errorf("HTTP %d : %s", code, model.Truncate(msg, 160))
	}
	var out [][]string
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		out = append(out, strings.Split(sc.Text(), "\t"))
	}
	return out, sc.Err()
}

func chList(m map[string][2]string) string {
	var b strings.Builder
	first := true
	for k := range m {
		if !first {
			b.WriteByte(',')
		}
		first = false
		b.WriteString("'" + k + "'")
	}
	return b.String()
}

func (c *ClickHouse) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := c.det.skip(now); skip {
		return nil, st
	}
	ts := model.Ms(now)
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ver, err := c.query(ctx, "SELECT version()")
	if err == nil && (len(ver) != 1 || len(ver[0]) != 1) {
		err = fmt.Errorf("réponse inattendue à SELECT version()")
	}
	if err != nil {
		return c.fail(ts, tags, err)
	}
	c.det.ok()
	c.begin()
	defer c.end()
	kv := c.kvs()
	var errs []string
	read := func(q string, each func(name string, v float64)) {
		rows, err := c.query(ctx, q)
		if err != nil {
			errs = append(errs, err.Error())
			return
		}
		for _, r := range rows {
			if len(r) < 2 {
				continue
			}
			if v, err := strconv.ParseFloat(r[1], 64); err == nil {
				each(r[0], v)
			}
		}
	}
	read("SELECT metric, value FROM system.metrics WHERE metric IN ("+chList(chMetrics)+")", func(n string, v float64) {
		m := chMetrics[n]
		b.Gauge(m[0], m[1], v, kv...)
	})
	read("SELECT event, value FROM system.events WHERE event IN ("+chList(chEvents)+")", func(n string, v float64) {
		m := chEvents[n]
		c.count(b, m[0], m[1], n, v, kv...)
	})
	read("SELECT metric, value FROM system.asynchronous_metrics WHERE metric IN ("+chList(chAsync)+")", func(n string, v float64) {
		m := chAsync[n]
		b.Gauge(m[0], m[1], v, kv...)
	})
	if t, ok := c.diff("QueryTimeMicroseconds"); ok {
		if n, ok := c.diff("Query"); ok && n > 0 {
			b.Gauge("clickhouse.query.time.avg", "millisecond", round1(t/n/1000), kv...)
		}
	}
	checks := []model.Check{c.okCheck(ts, tags, "ClickHouse "+ver[0][0]+" joignable")}
	if len(errs) > 0 {
		return checks, "partiel : " + strings.Join(errs, " ; ")
	}
	return checks, "ok"
}
