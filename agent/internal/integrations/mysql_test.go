package integrations

import (
	"context"
	"os"
	"testing"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

func TestMySQLReplicationParsing(t *testing.T) {
	if parseReplication(nil) != nil {
		t.Fatal("pas de ligne : pas de réplication")
	}
	// MySQL 8.0.22+ (SHOW REPLICA STATUS).
	r := parseReplication(map[string]string{"Source_Host": "db1", "Source_Port": "3306", "Replica_IO_Running": "Yes",
		"Replica_SQL_Running": "Yes", "Seconds_Behind_Source": "4"})
	if r.source != "db1:3306" || !r.io || !r.sql || r.lag != 4 {
		t.Fatalf("MySQL : %+v", r)
	}
	// MariaDB et MySQL anciens (SHOW SLAVE STATUS), fil d'application arrêté, retard inconnu.
	r = parseReplication(map[string]string{"Master_Host": "db2", "Master_Port": "3306", "Slave_IO_Running": "Yes",
		"Slave_SQL_Running": "No", "Seconds_Behind_Master": "", "Last_SQL_Error": "Duplicate entry '7'"})
	if r.source != "db2:3306" || !r.io || r.sql || r.lag != -1 || r.err != "Duplicate entry '7'" {
		t.Fatalf("MariaDB : %+v", r)
	}
}

func TestMySQLAbsentAndInstance(t *testing.T) {
	m := newMySQL(config.MySQLConfig{Enabled: "auto", Socket: "/nexiste/pas.sock"})
	if c, st := m.collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags); c != nil || st != "absent" {
		t.Fatalf("sans socket : %q", st)
	}
	m = newMySQL(config.MySQLConfig{Enabled: "auto", DSN: "hypha:secret@tcp(127.0.0.1:1)/"})
	if m.instance() != "tcp:127.0.0.1:1" || m.det.mode != "true" {
		t.Fatalf("instance %q, mode %q", m.instance(), m.det.mode)
	}
	// Un dsn posé : l'échec se signale tout de suite, en CRIT, sans le mot de passe.
	c, st := m.collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags)
	if len(c) != 1 || c[0].Status != model.CheckCrit || st[:6] != "erreur" {
		t.Fatalf("dsn injoignable : %q %+v", st, c)
	}
	for _, x := range []string{c[0].Message, st} {
		if containsStr(x, "secret") {
			t.Fatalf("mot de passe dans le message : %q", x)
		}
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// Essai contre de vrais serveurs, lancé à la main : MYSQL_REEL=1 et MYSQL_DSN ou MYSQL_SOCKET.
func TestMySQLReel(t *testing.T) {
	if os.Getenv("MYSQL_REEL") != "1" {
		t.Skip("MYSQL_REEL=1 pour interroger un vrai MySQL ou MariaDB")
	}
	for _, cfg := range []config.MySQLConfig{
		{Enabled: "auto", Socket: os.Getenv("MYSQL_SOCKET")},
		{Enabled: "auto", DSN: os.Getenv("MYSQL_DSN")},
	} {
		if cfg.Socket == "" && cfg.DSN == "" {
			continue
		}
		m := newMySQL(cfg)
		var checks []model.Check
		var state string
		var b *model.SeriesBuilder
		for i := 0; i < 2; i++ { // deux passages : les débits demandent un relevé précédent
			b = model.NewSeriesBuilder(0, testTags)
			checks, state = m.collect(context.Background(), time.Now().Add(time.Duration(i)*31*time.Second), b, testTags)
			time.Sleep(time.Second)
		}
		t.Logf("== %s : état %s", m.instance(), state)
		for _, c := range checks {
			t.Logf("[%d] %s : %s", c.Status, c.Name, c.Message)
		}
		for _, s := range b.Series() {
			t.Logf("série %s %s = %v", s.Metric, s.Tags["db"], s.Points[0].Value)
		}
		m.close()
	}
}
