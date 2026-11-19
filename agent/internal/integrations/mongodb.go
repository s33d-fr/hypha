package integrations

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── MongoDB (§ 40) : serverStatus, replSetGetStatus, listDatabases ─────────────────────────
//
// Connexion directe au nœud de l'URI (même membre d'un jeu de réplicas : c'est lui qu'on
// supervise). Compte au rôle clusterMonitor si l'authentification est active. Connexions,
// opérations par type, mémoire, cache WiredTiger, files d'attente, réseau, taille des bases
// (10 min), état du jeu de réplicas et retard des secondaires.

const mongoSizeEvery = 10 * time.Minute

type MongoDB struct {
	cfg    config.MongoDBConfig
	det    detector
	client *mongo.Client
	prev   map[string]float64
	sizes  map[string]float64
	sizeAt time.Time
}

func newMongoDB(cfg config.MongoDBConfig) *MongoDB {
	mode := cfg.Enabled
	if mode == "auto" && cfg.URI != "" && cfg.URI != "mongodb://127.0.0.1:27017" {
		mode = "true"
	}
	return &MongoDB{cfg: cfg, det: detector{mode: mode}}
}

func (m *MongoDB) name() string          { return "mongodb" }
func (m *MongoDB) period() time.Duration { return 30 * time.Second }
func (m *MongoDB) close() {
	if m.client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = m.client.Disconnect(ctx)
		cancel()
		m.client = nil
	}
}

func (m *MongoDB) open() (*mongo.Client, error) {
	if m.client != nil {
		return m.client, nil
	}
	opts := options.Client().ApplyURI(m.cfg.URI).SetDirect(true).SetAppName("hypha-agent").
		SetServerSelectionTimeout(5 * time.Second).SetConnectTimeout(5 * time.Second).SetMaxPoolSize(2)
	c, err := mongo.Connect(opts)
	if err != nil {
		return nil, err
	}
	m.client = c
	return c, nil
}

// numF : nombre d'un document BSON (int32, int64, double), sinon faux.
func numF(v any) (float64, bool) {
	switch x := v.(type) {
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case float64:
		return x, true
	case int:
		return float64(x), true
	}
	return 0, false
}

// toM : un sous-document, que le pilote rend en bson.M ou en bson.D selon le cas.
func toM(v any) (bson.M, bool) {
	switch d := v.(type) {
	case bson.M:
		return d, true
	case bson.D:
		m := bson.M{}
		for _, e := range d {
			m[e.Key] = e.Value
		}
		return m, true
	}
	return nil, false
}

// path : valeur d'un document imbriqué, « a.b.c ».
func path(d bson.M, keys ...string) any {
	var cur any = d
	for _, k := range keys {
		m, ok := toM(cur)
		if !ok {
			return nil
		}
		cur = m[k]
	}
	return cur
}

func (m *MongoDB) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := m.det.skip(now); skip {
		return nil, st
	}
	ts := model.Ms(now)
	kv := []string{"instance", redactURIHost(m.cfg.URI)}
	fail := func(err error) ([]model.Check, string) {
		m.close()
		if silent, st := m.det.failed(err.Error()); silent {
			return nil, st
		}
		return []model.Check{{TS: ts, Name: "mongodb.can_connect", Status: model.CheckCrit, Message: model.Truncate(err.Error(), 400),
			Tags: tags(kv...)}}, "erreur : " + model.Truncate(err.Error(), 200)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	c, err := m.open()
	if err != nil {
		return fail(err)
	}
	admin := c.Database("admin")
	var ss bson.M
	if err := admin.RunCommand(ctx, bson.D{{Key: "serverStatus", Value: 1}}).Decode(&ss); err != nil {
		return fail(err)
	}
	m.det.ok()
	g := func(name, unit string, v any, extra ...string) {
		if f, ok := numF(v); ok {
			b.Gauge(name, unit, f, append(append([]string{}, kv...), extra...)...)
		}
	}
	g("mongodb.uptime", "second", path(ss, "uptime"))
	g("mongodb.connections.current", "connection", path(ss, "connections", "current"))
	g("mongodb.connections.available", "connection", path(ss, "connections", "available"))
	g("mongodb.mem.resident", "megabyte", path(ss, "mem", "resident"))
	g("mongodb.queue.readers", "operation", path(ss, "globalLock", "currentQueue", "readers"))
	g("mongodb.queue.writers", "operation", path(ss, "globalLock", "currentQueue", "writers"))
	if used, ok := numF(path(ss, "wiredTiger", "cache", "bytes currently in the cache")); ok {
		b.Gauge("mongodb.cache.bytes", "byte", used, kv...)
		if mx, ok := numF(path(ss, "wiredTiger", "cache", "maximum bytes configured")); ok && mx > 0 {
			b.Gauge("mongodb.cache.usage", "percent", round1(100*used/mx), kv...)
		}
	}
	cur := map[string]float64{}
	for _, op := range []string{"insert", "query", "update", "delete", "getmore", "command"} {
		if v, ok := numF(path(ss, "opcounters", op)); ok {
			cur["op."+op] = v
		}
	}
	for _, f := range []string{"bytesIn", "bytesOut"} {
		if v, ok := numF(path(ss, "network", f)); ok {
			cur["net."+f] = v
		}
	}
	if m.prev != nil {
		for k, v := range cur {
			d, ok := deltaF(v, m.prev[k])
			if !ok {
				continue
			}
			if strings.HasPrefix(k, "op.") {
				b.Add("mongodb.operations", model.TypeCount, "operation", d, append(append([]string{}, kv...), "op", k[3:])...)
			} else {
				b.Add("mongodb.network."+strings.ToLower(k[4:]), model.TypeCount, "byte", d, kv...)
			}
		}
	}
	m.prev = cur

	checks := []model.Check{}
	role := "autonome"
	var rs bson.M
	err = admin.RunCommand(ctx, bson.D{{Key: "replSetGetStatus", Value: 1}}).Decode(&rs)
	var ce mongo.CommandError
	switch {
	case err == nil:
		r := replicaSetState(rs)
		role = r.role
		for name, lag := range r.lags {
			b.Gauge("mongodb.replication.lag", "second", lag, append(append([]string{}, kv...), "member", name)...)
		}
		checks = append(checks, model.Check{TS: ts, Name: "mongodb.replication", Status: r.status, Message: r.msg, Tags: tags(kv...)})
	case errors.As(err, &ce) && (ce.Code == 76 || ce.Code == 94): // pas de jeu de réplicas, ou pas encore initialisé
	default:
		checks = append(checks, model.Check{TS: ts, Name: "mongodb.replication", Status: model.CheckUnknown,
			Message: "état du jeu de réplicas illisible : " + model.Truncate(err.Error(), 200), Tags: tags(kv...)})
	}
	if now.Sub(m.sizeAt) >= mongoSizeEvery {
		var ld bson.M
		if err := admin.RunCommand(ctx, bson.D{{Key: "listDatabases", Value: 1}}).Decode(&ld); err == nil {
			sizes := map[string]float64{}
			if dbs, ok := path(ld, "databases").(bson.A); ok {
				for _, x := range dbs {
					if d, ok := toM(x); ok {
						name, _ := d["name"].(string)
						if v, ok := numF(d["sizeOnDisk"]); ok && name != "" && name != "local" && name != "config" {
							sizes[name] = v
						}
					}
				}
			}
			m.sizes, m.sizeAt = sizes, now
		}
	}
	names := make([]string, 0, len(m.sizes))
	for n := range m.sizes {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		b.Gauge("mongodb.database.size", "byte", m.sizes[n], append(append([]string{}, kv...), "db", n)...)
	}
	version, _ := ss["version"].(string)
	checks = append(checks, model.Check{TS: ts, Name: "mongodb.can_connect", Status: model.CheckOK,
		Message: fmt.Sprintf("MongoDB %s joignable (%s)", version, role), Tags: tags(kv...)})
	return checks, "ok"
}

type mongoRS struct {
	role   string
	status int
	msg    string
	lags   map[string]float64
}

// replicaSetState : rôle du nœud supervisé, membres en panne, retard des secondaires sur le primaire.
func replicaSetState(rs bson.M) mongoRS {
	out := mongoRS{role: "membre", status: model.CheckOK, lags: map[string]float64{}}
	set, _ := rs["set"].(string)
	members, _ := rs["members"].(bson.A)
	var primary time.Time
	type mem struct {
		name, state string
		health      float64
		optime      time.Time
		self        bool
	}
	var list []mem
	for _, x := range members {
		d, ok := toM(x)
		if !ok {
			continue
		}
		var mm mem
		mm.name, _ = d["name"].(string)
		mm.state, _ = d["stateStr"].(string)
		mm.health, _ = numF(d["health"])
		mm.self, _ = d["self"].(bool)
		if dt, ok := d["optimeDate"].(bson.DateTime); ok {
			mm.optime = dt.Time()
		}
		if mm.state == "PRIMARY" {
			primary = mm.optime
		}
		if mm.self {
			out.role = strings.ToLower(mm.state)
			switch mm.state {
			case "PRIMARY":
				out.role = "primaire"
			case "SECONDARY":
				out.role = "secondaire"
			}
		}
		list = append(list, mm)
	}
	var down, odd []string
	for _, mm := range list {
		if mm.health == 0 {
			down = append(down, mm.name)
		} else if mm.state != "PRIMARY" && mm.state != "SECONDARY" && mm.state != "ARBITER" {
			odd = append(odd, mm.name+" ("+mm.state+")")
		}
		if mm.state == "SECONDARY" && !primary.IsZero() && !mm.optime.IsZero() {
			lag := primary.Sub(mm.optime).Seconds()
			if lag < 0 {
				lag = 0
			}
			out.lags[mm.name] = lag
		}
	}
	out.msg = fmt.Sprintf("jeu %s : %d membre(s), ce nœud %s", set, len(list), out.role)
	switch {
	case primary.IsZero():
		out.status, out.msg = model.CheckCrit, out.msg+" ; aucun primaire"
	case len(down) > 0:
		out.status, out.msg = model.CheckCrit, out.msg+" ; injoignable(s) : "+strings.Join(down, ", ")
	case len(odd) > 0:
		out.status, out.msg = model.CheckWarn, out.msg+" ; "+strings.Join(odd, ", ")
	}
	return out
}

// redactURIHost : l'URI réduite à ses hôtes (ni compte ni mot de passe dans les étiquettes).
func redactURIHost(uri string) string {
	s := uri
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.LastIndex(s, "@"); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.IndexAny(s, "/?"); i >= 0 {
		s = s[:i]
	}
	return s
}
