package integrations

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

func TestMemcachedStats(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	n := 0
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			n++
			rd := bufio.NewReader(c)
			rd.ReadString('\n')
			fmt.Fprintf(c, "STAT version 1.6.40\r\nSTAT uptime 50\r\nSTAT curr_connections 2\r\nSTAT bytes 256\r\nSTAT limit_maxbytes 1024\r\n"+
				"STAT get_hits %d\r\nSTAT get_misses %d\r\nSTAT evictions 0\r\nEND\r\n", 80*n, 20*n)
			c.Close()
		}
	}()
	m := newMemcached(config.MemcachedConfig{Enabled: "auto", Address: ln.Addr().String()})
	var checks []model.Check
	var b *model.SeriesBuilder
	for i := 0; i < 2; i++ {
		b = model.NewSeriesBuilder(0, testTags)
		checks, _ = m.collect(context.Background(), time.Now().Add(time.Duration(i)*31*time.Second), b, testTags)
	}
	got := map[string]float64{}
	for _, s := range b.Series() {
		got[s.Metric] = s.Points[0].Value
	}
	if len(checks) != 1 || checks[0].Message != "Memcached 1.6.40 joignable" || got["memcached.bytes.usage"] != 25 ||
		got["memcached.get.hit_rate"] != 80 || got["memcached.get.hits"] != 80 {
		t.Fatalf("memcached : %+v %v", checks, got)
	}
}

func TestRabbitMQAlarmsAndQueues(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "hypha" || p != "bon" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/api/overview":
			fmt.Fprint(w, `{"rabbitmq_version":"4.0.9","cluster_name":"rabbit@essai","object_totals":{"queues":2,"connections":1,"channels":1,"consumers":0},
				"queue_totals":{"messages":7,"messages_ready":5,"messages_unacknowledged":2},"message_stats":{"publish_details":{"rate":1.5}}}`)
		case "/api/nodes":
			fmt.Fprint(w, `[{"name":"rabbit@essai","running":true,"mem_used":50,"mem_limit":100,"mem_alarm":false,"disk_free":10,"disk_free_limit":50,"disk_free_alarm":true,"fd_used":10,"fd_total":100,"partitions":[]}]`)
		case "/api/queues":
			fmt.Fprint(w, `[{"name":"commandes","vhost":"/","messages":7,"messages_ready":5,"messages_unacknowledged":2,"consumers":0,"state":"running"}]`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	r := newRabbitMQ(config.RabbitMQConfig{Enabled: "auto", URL: srv.URL, Username: "hypha", Password: "bon"})
	b := model.NewSeriesBuilder(0, testTags)
	checks, st := r.collect(context.Background(), time.Now(), b, testTags)
	c := byName(checks)
	if st != "ok" || c["rabbitmq.node"].Status != model.CheckCrit || !strings.Contains(c["rabbitmq.node"].Message, "alarme disque") ||
		!strings.Contains(c["rabbitmq.can_connect"].Message, "RabbitMQ 4.0.9") {
		t.Fatalf("rabbitmq : %q %+v", st, checks)
	}
	got := map[string]float64{}
	for _, s := range b.Series() {
		got[s.Metric+"/"+s.Tags["queue"]] = s.Points[0].Value
	}
	if got["rabbitmq.queue.messages.ready/commandes"] != 5 || got["rabbitmq.messages.publish_rate/"] != 1.5 || got["rabbitmq.node.mem.usage/"] != 50 {
		t.Fatalf("mesures : %v", got)
	}
	// Mauvais mot de passe : 401 dit, sans le mot de passe.
	r = newRabbitMQ(config.RabbitMQConfig{Enabled: "auto", URL: srv.URL, Username: "hypha", Password: "mauvais"})
	checks, _ = r.collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags)
	if len(checks) != 1 || checks[0].Status != model.CheckCrit || !strings.Contains(checks[0].Message, "401") || strings.Contains(checks[0].Message, "mauvais") {
		t.Fatalf("401 : %+v", checks)
	}
}

func TestMongoReplicaSetState(t *testing.T) {
	now := time.Now()
	dt := func(t time.Time) bson.DateTime { return bson.NewDateTimeFromTime(t) }
	rs := bson.M{"set": "rs0", "members": bson.A{
		bson.M{"name": "a:27017", "stateStr": "PRIMARY", "health": float64(1), "optimeDate": dt(now), "self": true},
		bson.M{"name": "b:27017", "stateStr": "SECONDARY", "health": float64(1), "optimeDate": dt(now.Add(-30 * time.Second))},
		bson.D{{Key: "name", Value: "c:27017"}, {Key: "stateStr", Value: "(not reachable/healthy)"}, {Key: "health", Value: float64(0)}},
	}} // le pilote rend les sous-documents en bson.D : les deux formes sont lues
	r := replicaSetState(rs)
	if r.role != "primaire" || r.status != model.CheckCrit || !strings.Contains(r.msg, "c:27017") || r.lags["b:27017"] < 29 || r.lags["b:27017"] > 31 {
		t.Fatalf("jeu : %+v", r)
	}
	if got := redactURIHost("mongodb://hypha:secret@db1:27017,db2:27017/?replicaSet=rs0"); got != "db1:27017,db2:27017" {
		t.Fatalf("URI : %q", got)
	}
}

// Essai contre de vrais serveurs d'essai, lancé à la main : PRODUITS_REEL=1, avec
// MEMCACHED_ADDR, RABBITMQ_URL, RABBITMQ_USER, RABBITMQ_PASS, MONGODB_URI.
func TestProduitsReel(t *testing.T) {
	if os.Getenv("PRODUITS_REEL") != "1" {
		t.Skip("PRODUITS_REEL=1 pour interroger les vrais serveurs")
	}
	list := []integration{
		newMemcached(config.MemcachedConfig{Enabled: "true", Address: os.Getenv("MEMCACHED_ADDR")}),
		newRabbitMQ(config.RabbitMQConfig{Enabled: "true", URL: os.Getenv("RABBITMQ_URL"), Username: os.Getenv("RABBITMQ_USER"), Password: os.Getenv("RABBITMQ_PASS")}),
		newMongoDB(config.MongoDBConfig{Enabled: "true", URI: os.Getenv("MONGODB_URI")}),
	}
	for _, it := range list {
		var checks []model.Check
		var b *model.SeriesBuilder
		var st string
		for i := 0; i < 2; i++ {
			b = model.NewSeriesBuilder(0, testTags)
			checks, st = it.collect(context.Background(), time.Now().Add(time.Duration(i)*31*time.Second), b, testTags)
			time.Sleep(time.Second)
		}
		t.Logf("== %s : %s", it.name(), st)
		for _, c := range checks {
			t.Logf("[%d] %s : %s", c.Status, c.Name, c.Message)
		}
		for _, s := range b.Series() {
			t.Logf("série %s %s%s%s = %v", s.Metric, s.Tags["queue"], s.Tags["db"], s.Tags["op"], s.Points[0].Value)
		}
		it.close()
	}
}
