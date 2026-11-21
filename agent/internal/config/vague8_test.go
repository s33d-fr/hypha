package config

import (
	"strings"
	"testing"
	"time"
)

func TestV8DefaultsInstancesAndRedaction(t *testing.T) {
	dir := t.TempDir()
	path := writeConf(t, dir, map[string]string{
		"hypha.yaml": "api_key: " + testKey + `
integrations:
  pihole: {password: mdp-pihole, tags: {site: lab}}
  adguard: {username: hypha, password: mdp-adguard}
  unbound: {command: "docker exec unbound unbound-control"}
  mosquitto: {url: "mqtts://192.0.2.5:8883", username: hypha, password: mdp-mqtt, ca_file: /etc/hypha/mqtt-ca.pem}
  proxmox_backup:
    url: https://192.0.2.7:8007
    token_id: "hypha@pbs!lecture"
    token_secret: secret-pbs
    fingerprint: "af:c8:fd:ab:6c:15:b3:47:03:10:22:14:6e:87:d9:f4:ad:d7:bd:2b:fe:d7:38:f2:e8:cd:d0:4c:5e:7e:fe:49"
  borg:
    repositories:
      - {name: serveur, repository: "ssh://borg@192.0.2.20/./depot", passphrase_file: /etc/hypha/borg.pass, rsh: "ssh -i /root/.ssh/borg"}
  restic:
    stats: true
    repositories:
      - {name: nas, repository_file: /etc/hypha/restic.repo, password_file: /etc/hypha/restic.pass, env: {AWS_SHARED_CREDENTIALS_FILE: /etc/hypha/aws}}
`,
		"conf.d/50-v8.yaml": `integrations:
  adguard:
    instances:
      - {name: dns2, url: "http://192.0.2.2:3000", username: hypha, password: mdp-2}
  caddy:
    instances:
      - {name: front, url: "http://192.0.2.3:2019"}
`,
	})
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if errs := c.Validate(true); len(errs) != 0 {
		t.Fatalf("erreurs : %v", errs)
	}
	in := c.Integrations.IntegrationsV8
	if in.PiHole.URL != "http://127.0.0.1" || in.PiHole.Tags["site"] != "lab" || in.Caddy.URL != "http://127.0.0.1:2019" {
		t.Fatalf("défauts : %+v %+v", in.PiHole, in.Caddy)
	}
	if strings.Join(in.Unbound.Command, "|") != "docker|exec|unbound|unbound-control" || in.Unbound.Timeout.D() != 10*time.Second {
		t.Fatalf("unbound : %+v", in.Unbound)
	}
	if in.PBS.Interval.D() != time.Minute || in.PBS.MaxGroups != DefaultPBSMaxGroups || in.Mosquitto.Timeout.D() != 5*time.Second {
		t.Fatalf("pbs, mosquitto : %+v %+v", in.PBS, in.Mosquitto)
	}
	if in.Borg.Interval.D() != 15*time.Minute || in.Borg.Timeout.D() != 2*time.Minute || len(in.Borg.Repositories) != 1 || !in.Restic.Stats {
		t.Fatalf("borg, restic : %+v %+v", in.Borg, in.Restic)
	}
	x := in.V8Extra
	if len(x.AdGuard) != 1 || x.AdGuard[0].Enabled != "true" || x.AdGuard[0].URL != "http://192.0.2.2:3000" || len(x.Caddy) != 1 {
		t.Fatalf("instances : %+v %+v", x.AdGuard, x.Caddy)
	}
	red, err := c.RedactedYAML()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"mdp-pihole", "mdp-adguard", "mdp-mqtt", "secret-pbs", "mdp-2"} {
		if strings.Contains(red, s) {
			t.Fatalf("secret %s affiché par check-config", s)
		}
	}
}

func TestV8Validation(t *testing.T) {
	dir := t.TempDir()
	path := writeConf(t, dir, map[string]string{
		"hypha.yaml": "api_key: " + testKey + `
integrations:
  adguard: {enabled: true}
  pihole: {url: "ftp://127.0.0.1", tags: {"Mauvaise Clé": x}}
  unbound: {command: "./unbound-control", timeout: 5m, config_file: relatif.conf}
  mosquitto: {url: "http://127.0.0.1:1883", password: p, timeout: 2m}
  proxmox_backup: {url: "http://127.0.0.1:8007", token_id: "hypha", fingerprint: "zz", interval: 5s}
  borg:
    repositories:
      - {name: a, passphrase_file: relatif.pass}
      - {name: a, repository: /srv/borg, password_file: /etc/x}
      - {name: "Pas Bon", repository: "-x"}
  restic:
    interval: 1m
    repositories:
      - {name: r1, repository: "rest:http://u:motdepasse@192.0.2.9:8000/r/"}
      - {name: r2, repository: /srv/r, repository_file: /etc/r, password_file: /etc/p, env: {BORG_PASSPHRASE: x, "minuscule": y}}
`,
	})
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	errs := strings.Join(c.Validate(true), "\n")
	for _, want := range []string{
		"integrations.adguard : username et password attendus", "integrations.pihole.url : schéma", "integrations.pihole.tags",
		"integrations.unbound.command", "integrations.unbound.timeout", "integrations.unbound.config_file",
		"integrations.mosquitto.url invalide", "integrations.mosquitto : password sans username", "integrations.mosquitto.timeout",
		"integrations.proxmox_backup.url : schéma", "token_id et token_secret vont ensemble", "integrations.proxmox_backup.token_id",
		"integrations.proxmox_backup.fingerprint", "integrations.proxmox_backup.interval",
		"integrations.borg.repositories[0] (a).repository manquant", "integrations.borg.repositories[0] (a).passphrase_file",
		`name "a" en double`, "integrations.borg.repositories[1] (a) : repository_file et password_file",
		"integrations.borg.repositories[2] : name invalide", "integrations.borg.repositories[2] (Pas Bon).repository invalide",
		"integrations.restic.interval", "integrations.restic.repositories[0] (r1).password_file manquant",
		"integrations.restic.repositories[0] (r1).repository : mot de passe dans l'adresse",
		"integrations.restic.repositories[1] (r2) : repository ou repository_file", "BORG_PASSPHRASE est posé par l'agent",
		`nom de variable "minuscule" invalide`,
	} {
		if !strings.Contains(errs, want) {
			t.Errorf("erreur attendue « %s » absente de :\n%s", want, errs)
		}
	}
}

// La conf par défaut, sans aucun réglage, reste valide sur tous les systèmes (leçon de la 0.8.0).
func TestV8DefaultsValid(t *testing.T) {
	c := Default()
	c.APIKey = testKey
	c.normalize()
	var errs []string
	c.validateV8(func(f string, a ...any) { errs = append(errs, f) }, func(string, ...any) {})
	if len(errs) != 0 {
		t.Fatalf("conf par défaut refusée : %v", errs)
	}
}
