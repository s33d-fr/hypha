package config

import (
	"strings"
	"testing"
	"time"
)

func TestInstancesDecodeMerge(t *testing.T) {
	dir := t.TempDir()
	path := writeConf(t, dir, map[string]string{
		"hypha.yaml": "api_key: " + testKey + `
integrations:
  redis:
    name: local
    instances:
      - {name: cache, address: "192.0.2.5:6379", password: s3cret}
  proxmox:
    interval: 2m
`,
		"conf.d/30-pve-a.yaml": `integrations:
  proxmox:
    instances:
      - name: grappe-a
        url: https://pve-a:8006
        token_id: hypha@pve!lecture
        token_secret: aaa
`,
		"conf.d/31-pve-b.yaml": `integrations:
  proxmox:
    instances:
      - name: grappe-b
        url: https://pve-b:8006
        token_id: hypha@pve!lecture
        token_secret: bbb
        enabled: false
`,
	})
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if errs := c.Validate(true); len(errs) != 0 {
		t.Fatalf("erreurs : %v", errs)
	}
	x := c.Integrations.Extra
	if len(x.Redis) != 1 || x.Redis[0].Name != "cache" || x.Redis[0].Address != "192.0.2.5:6379" || x.Redis[0].Enabled != "true" {
		t.Fatalf("redis : %+v", x.Redis)
	}
	if c.Integrations.Redis.Name != "local" || c.Integrations.Redis.Address != "127.0.0.1:6379" {
		t.Fatalf("entrée principale redis : %+v", c.Integrations.Redis)
	}
	if len(x.Proxmox) != 2 || x.Proxmox[0].Name != "grappe-a" || x.Proxmox[1].Name != "grappe-b" {
		t.Fatalf("les instances des deux fichiers doivent s'additionner : %+v", x.Proxmox)
	}
	// Valeurs par défaut de l'intégration, pas celles de l'entrée principale.
	if x.Proxmox[0].Interval.D() != 60*time.Second || x.Proxmox[0].Enabled != "true" || x.Proxmox[1].Enabled != "false" {
		t.Fatalf("défauts des instances : %+v", x.Proxmox)
	}
	red, err := c.RedactedYAML()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"s3cret", "aaa", "bbb"} {
		if strings.Contains(red, s) {
			t.Fatalf("secret %q affiché par check-config :\n%s", s, red)
		}
	}
}

func TestInstancesValidate(t *testing.T) {
	dir := t.TempDir()
	path := writeConf(t, dir, map[string]string{
		"hypha.yaml": "api_key: " + testKey + `
integrations:
  aws:
    instances:
      - {name: prod, access_key_id: AK, secret_access_key: SK, regions: [eu-west-3]}
      - {name: prod, access_key_id: AK, secret_access_key: SK, regions: [paris]}
      - {access_key_id: AK}
      - {name: Bad Name, access_key_id: AK, secret_access_key: SK, regions: [eu-west-1], instances: [{name: x}]}
  kubernetes:
    instances:
      - {name: k1, url: "http://k1:6443", tokn: x}
`,
	})
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(c.Validate(true), "\n")
	for _, want := range []string{
		`integrations.aws.instances[1] : name "prod" en double`,
		`integrations.aws.instances[1] (prod).regions : région invalide "paris"`,
		`integrations.aws.instances[2] : name manquant`,
		`integrations.aws.instances[2] : access_key_id, secret_access_key et regions sont nécessaires`,
		`integrations.aws.instances[3] : une instance ne peut pas avoir ses propres instances`,
		`integrations.kubernetes.instances[0] (k1).url invalide`,
		`integrations.kubernetes.instances[0] (k1) : token ou token_file est nécessaire avec url`,
	} {
		if !strings.Contains(all, want) {
			t.Errorf("message attendu absent : %s\n--- obtenus :\n%s", want, all)
		}
	}
	if !strings.Contains(strings.Join(c.Warnings, "\n"), "integrations.kubernetes.instances[0] : clé inconnue ignorée") {
		t.Errorf("clé inconnue d'une instance non signalée : %v", c.Warnings)
	}
}
