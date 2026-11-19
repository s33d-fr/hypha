package integrations

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

const redisInfoReplica = "# Server\r\nredis_version:7.4.12\r\nuptime_in_seconds:120\r\n# Clients\r\nconnected_clients:3\r\nblocked_clients:0\r\n" +
	"# Memory\r\nused_memory:2000000\r\nused_memory_rss:4000000\r\nmaxmemory:4000000\r\nmem_fragmentation_ratio:2.0\r\n" +
	"# Persistence\r\nrdb_changes_since_last_save:5\r\nrdb_last_bgsave_status:err\r\naof_enabled:0\r\n" +
	"# Stats\r\ntotal_commands_processed:%d\r\nkeyspace_hits:%d\r\nkeyspace_misses:%d\r\nevicted_keys:0\r\n" +
	"# Replication\r\nrole:slave\r\nmaster_host:redis\r\nmaster_port:6379\r\nmaster_link_status:down\r\nmaster_link_down_since_seconds:42\r\nmaster_last_io_seconds_ago:-1\r\n" +
	"# Keyspace\r\ndb0:keys=12,expires=3,avg_ttl=0\r\ndb2:keys=1,expires=0,avg_ttl=0\r\n"

// fakeRedis : répond à AUTH (mot de passe « bon ») et à INFO ; compte les appels INFO.
func fakeRedis(t *testing.T, password string) (string, *int) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	n := new(int)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				rd := bufio.NewReader(c)
				for {
					line, err := rd.ReadString('\n')
					if err != nil {
						return
					}
					var argc int
					fmt.Sscanf(line, "*%d", &argc)
					args := make([]string, argc)
					for i := range args {
						rd.ReadString('\n')
						a, _ := rd.ReadString('\n')
						args[i] = strings.TrimRight(a, "\r\n")
					}
					switch {
					case len(args) > 0 && args[0] == "AUTH":
						if args[len(args)-1] == password {
							c.Write([]byte("+OK\r\n"))
						} else {
							c.Write([]byte("-WRONGPASS invalid username-password pair\r\n"))
						}
					case len(args) > 0 && args[0] == "INFO":
						*n++
						body := fmt.Sprintf(redisInfoReplica, 1000**n, 90**n, 10**n)
						fmt.Fprintf(c, "$%d\r\n%s\r\n", len(body), body)
					}
				}
			}(c)
		}
	}()
	return ln.Addr().String(), n
}

func TestRedisReplicaAndPersistence(t *testing.T) {
	addr, _ := fakeRedis(t, "bon")
	r := newRedis(config.RedisConfig{Enabled: "auto", Address: addr, Password: "bon"})
	var checks []model.Check
	var b *model.SeriesBuilder
	for i := 0; i < 2; i++ {
		b = model.NewSeriesBuilder(0, testTags)
		checks, _ = r.collect(context.Background(), time.Now().Add(time.Duration(i)*31*time.Second), b, testTags)
	}
	c := byName(checks)
	if c["redis.can_connect"].Status != model.CheckOK || !strings.Contains(c["redis.can_connect"].Message, "Redis 7.4.12 joignable (réplique)") {
		t.Fatalf("connexion : %+v", c["redis.can_connect"])
	}
	if s := c["redis.replication"]; s.Status != model.CheckCrit || !strings.Contains(s.Message, "lien down depuis 42s") {
		t.Fatalf("réplication : %+v", s)
	}
	if s := c["redis.persistence"]; s.Status != model.CheckCrit || !strings.Contains(s.Message, "RDB") {
		t.Fatalf("persistance : %+v", s)
	}
	got := map[string]float64{}
	for _, s := range b.Series() {
		got[s.Metric+"/"+s.Tags["db"]] = s.Points[0].Value
	}
	if got["redis.mem.usage/"] != 50 || got["redis.keys/db0"] != 12 || got["redis.keys.expires/db0"] != 3 ||
		got["redis.keys/db2"] != 1 || got["redis.commands/"] != 1000 || got["redis.keyspace.hit_rate/"] != 90 {
		t.Fatalf("mesures : %v", got)
	}
}

func TestRedisAuthAndAbsent(t *testing.T) {
	addr, n := fakeRedis(t, "bon")
	r := newRedis(config.RedisConfig{Enabled: "auto", Address: addr, Password: "mauvais"})
	c, st := r.collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags)
	if len(c) != 1 || c[0].Status != model.CheckCrit || !strings.Contains(c[0].Message, "WRONGPASS") || *n != 0 ||
		strings.Contains(c[0].Message+st, "mauvais") {
		t.Fatalf("mot de passe refusé : %q %+v (INFO lus : %d)", st, c, *n)
	}
	// Auto, adresse par défaut fermée : absent, sans alerte.
	r = newRedis(config.RedisConfig{Enabled: "auto", Address: "127.0.0.1:6379"})
	r.cfg.Address = "127.0.0.1:1"
	if c, st := r.collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags); c != nil || st != "absent" {
		t.Fatalf("absent : %q %+v", st, c)
	}
}

// Essai contre de vrais serveurs, lancé à la main : REDIS_REEL=1 REDIS_ADDRS="127.0.0.1:6379,127.0.0.1:6380".
func TestRedisReel(t *testing.T) {
	if os.Getenv("REDIS_REEL") != "1" {
		t.Skip("REDIS_REEL=1 pour interroger un vrai Redis")
	}
	for _, a := range strings.Split(os.Getenv("REDIS_ADDRS"), ",") {
		r := newRedis(config.RedisConfig{Enabled: "true", Address: a})
		var checks []model.Check
		var b *model.SeriesBuilder
		for i := 0; i < 2; i++ {
			b = model.NewSeriesBuilder(0, testTags)
			checks, _ = r.collect(context.Background(), time.Now().Add(time.Duration(i)*31*time.Second), b, testTags)
			time.Sleep(time.Second)
		}
		t.Logf("== %s", a)
		for _, c := range checks {
			t.Logf("[%d] %s : %s", c.Status, c.Name, c.Message)
		}
		for _, s := range b.Series() {
			t.Logf("série %s %s = %v", s.Metric, s.Tags["db"], s.Points[0].Value)
		}
	}
}
