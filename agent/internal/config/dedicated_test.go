package config

import (
	"strings"
	"testing"
	"time"
)

func TestDedicatedDefaultsAndInstances(t *testing.T) {
	dir := t.TempDir()
	path := writeConf(t, dir, map[string]string{
		"hypha.yaml": "api_key: " + testKey + `
integrations:
  phpfpm:
    address: /run/php/php8.3-fpm.sock
  varnish:
    command: "docker exec varnish varnishstat"
  pgbouncer:
    dsn: postgres://hypha:pw@127.0.0.1:6432/pgbouncer
  ssh:
    targets:
      - {address: 192.0.2.10}
      - {name: bastion, address: "bastion.exemple:2222", expect_banner: "^SSH-2.0-OpenSSH_9"}
  openldap:
    url: "ldapi://%2Frun%2Fslapd%2Fldapi"
`,
		"conf.d/40-pdns.yaml": `integrations:
  powerdns:
    instances:
      - {name: ns-a, url: "http://192.0.2.1:8081", api_key: cle-a}
  zookeeper:
    instances:
      - {name: zk2, address: "192.0.2.2:2181"}
`,
	})
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if errs := c.Validate(true); len(errs) != 0 {
		t.Fatalf("erreurs : %v", errs)
	}
	in := c.Integrations
	if in.PHPFPM.StatusPath != "/status" || in.PHPFPM.Address != "/run/php/php8.3-fpm.sock" || in.Squid.URL != "http://127.0.0.1:3128" {
		t.Fatalf("défauts : %+v %+v", in.PHPFPM, in.Squid)
	}
	if strings.Join(in.Varnish.Command, "|") != "docker|exec|varnish|varnishstat" || in.Varnish.Timeout.D() != 10*time.Second {
		t.Fatalf("varnish : %+v", in.Varnish)
	}
	if ts := in.SSH.Targets; len(ts) != 2 || ts[0].Name != "192.0.2.10:22" || ts[0].Address != "192.0.2.10:22" || ts[1].Timeout.D() != 5*time.Second {
		t.Fatalf("ssh : %+v", ts)
	}
	x := in.DedicatedExtra
	if len(x.PowerDNS) != 1 || x.PowerDNS[0].Enabled != "true" || x.PowerDNS[0].ServerID != "localhost" || x.PowerDNS[0].APIKey != "cle-a" {
		t.Fatalf("instances powerdns : %+v", x.PowerDNS)
	}
	if len(x.ZooKeeper) != 1 || x.ZooKeeper[0].Address != "192.0.2.2:2181" {
		t.Fatalf("instances zookeeper : %+v", x.ZooKeeper)
	}
	red, err := c.RedactedYAML()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(red, "cle-a") || strings.Contains(red, "hypha:pw@") {
		t.Fatalf("secret affiché par check-config :\n%s", red)
	}
}

func TestDedicatedValidation(t *testing.T) {
	dir := t.TempDir()
	path := writeConf(t, dir, map[string]string{
		"hypha.yaml": "api_key: " + testKey + `
integrations:
  phpfpm: {address: "pas une adresse", status_path: status}
  varnish: {command: "./varnishstat"}
  pgbouncer: {enabled: true}
  proxysql: {dsn: "admin"}
  keycloak: {client_id: hypha, url: "http://127.0.0.1:9000"}
  powerdns: {enabled: true}
  supervisord: {socket: /tmp/supervisor.sock}
  lighttpd: {enabled: true}
  nfs: {timeout: 5m}
  ssh:
    targets:
      - {name: a, address: "h:22", expect_banner: "("}
      - {name: a, address: "h2"}
  zookeeper:
    instances:
      - {address: "192.0.2.1:2181"}
      - {name: "Mauvais Nom", address: x}
`,
	})
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	errs := strings.Join(c.Validate(true), "\n")
	for _, want := range []string{
		"integrations.phpfpm.address", "integrations.phpfpm.status_path", "integrations.varnish.command",
		"integrations.pgbouncer.dsn manquant", "integrations.proxysql.dsn", "client_id et client_secret vont ensemble",
		"integrations.powerdns.api_key manquante", "integrations.lighttpd : enabled sans url", "integrations.nfs.timeout",
		"expect_banner", `name "a" en double`, "integrations.zookeeper.instances[0] : name manquant",
		"integrations.zookeeper.instances[1] : name invalide", "integrations.zookeeper.instances[1] (Mauvais Nom).address",
	} {
		if !strings.Contains(errs, want) {
			t.Errorf("erreur attendue « %s » absente de :\n%s", want, errs)
		}
	}
	if !strings.Contains(strings.Join(c.Warnings, "\n"), "PrivateTmp") {
		t.Errorf("avertissement /tmp attendu : %v", c.Warnings)
	}
}

// La conf par défaut, sans aucun réglage, doit rester valide sur tous les systèmes : un agent
// Windows refuse sa mise à jour si la nouvelle version rejette sa conf (0.8.0).
func TestDedicatedDefaultsValid(t *testing.T) {
	c := Default()
	c.APIKey = testKey
	c.normalize()
	var errs []string
	c.validateDedicated(func(f string, a ...any) { errs = append(errs, f) }, func(string, ...any) {})
	if len(errs) != 0 {
		t.Fatalf("conf par défaut refusée : %v", errs)
	}
	for p, want := range map[string]bool{"/var/log/suricata/eve.json": true, "/run/php/x.sock": true, "relatif/eve.json": false, "": false} {
		if absPath(p) != want {
			t.Fatalf("absPath(%q) = %v", p, !want)
		}
	}
}
