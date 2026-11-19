package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConf(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "conf.d"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "hypha.yaml")
}

var testKey = "hph_" + strings.Repeat("a", 40)

func TestConfDMerge(t *testing.T) {
	dir := t.TempDir()
	path := writeConf(t, dir, map[string]string{
		"hypha.yaml": "api_key: " + testKey + `
tags: {env: prod, role: web}
interval: 20s
logs:
  files:
    - {path: /var/log/a.log}
    - {path: /var/log/b.log}
integrations:
  postgres: {port: 5433}
`,
		"conf.d/10-parc.yaml": `auto_update: true
actions:
  enabled: true
  allow: [restart_service]
integrations:
  postgres: {user: pg}
logs:
  files: [{path: /var/log/c.log}]
tags: {role: base}
`,
		"conf.d/20-local.yaml":   "tags: {rack: 7}\nactions: {timeout: 30s}\n",
		"conf.d/.cache.yaml":     "interval: [ceci n'est pas lu\n",
		"conf.d/30-ancien.yml":   "interval: 99s\n",
		"conf.d/40-x.yaml.bak":   "interval: 99s\n",
	})
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if errs := c.Validate(true); len(errs) != 0 {
		t.Fatalf("erreurs : %v", errs)
	}
	switch {
	case !c.AutoUpdate || !c.Actions.Enabled:
		t.Errorf("conf.d non appliquée : auto_update=%v actions=%v", c.AutoUpdate, c.Actions.Enabled)
	case c.Integrations.Postgres.Port != 5433 || c.Integrations.Postgres.User != "pg":
		t.Errorf("fusion récursive des objets : %+v", c.Integrations.Postgres)
	case len(c.Logs.Files) != 1 || c.Logs.Files[0].Path != "/var/log/c.log":
		t.Errorf("une liste doit remplacer la liste : %+v", c.Logs.Files)
	case c.Tags["env"] != "prod" || c.Tags["role"] != "base" || c.Tags["rack"] != "7":
		t.Errorf("étiquettes fusionnées : %v", c.Tags)
	case c.Interval.D() != 20*time.Second:
		t.Errorf("fichier ignoré lu : interval=%s", c.Interval.D())
	case c.Actions.Timeout.D() != 30*time.Second || len(c.Actions.Allow) != 1:
		t.Errorf("ordre alphabétique : %+v", c.Actions)
	case len(c.Actions.DenyServices) != 4:
		t.Errorf("clé absente : valeur par défaut perdue : %v", c.Actions.DenyServices)
	case len(c.Files) != 3:
		t.Errorf("fichiers lus : %v", c.Files)
	}
}

func TestConfDInvalidFileStopsLoad(t *testing.T) {
	for name, content := range map[string]string{
		"yaml":  "actions: {enabled: true\n",
		"type":  "interval: abc\n",
		"liste": "- a\n- b\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeConf(t, dir, map[string]string{"hypha.yaml": "api_key: " + testKey + "\n", "conf.d/10-parc.yaml": content})
			_, err := Load(path)
			if err == nil || !strings.Contains(err.Error(), "10-parc.yaml") {
				t.Fatalf("erreur attendue nommant le fichier, obtenue %v", err)
			}
		})
	}
}

func TestConfDErrorOrigin(t *testing.T) {
	dir := t.TempDir()
	path := writeConf(t, dir, map[string]string{
		"hypha.yaml":          "api_key: " + testKey + "\n",
		"conf.d/10-parc.yaml": "snmp:\n  devices:\n    - {name: sw, address: 192.0.2.1, version: 2c}\n",
	})
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	errs := c.Validate(true)
	if len(errs) != 1 || !strings.Contains(errs[0], "community") || !strings.Contains(errs[0], "10-parc.yaml") {
		t.Fatalf("erreur sans son fichier d'origine : %v", errs)
	}
}

func TestRedactedAndHash(t *testing.T) {
	dir := t.TempDir()
	path := writeConf(t, dir, map[string]string{"hypha.yaml": "api_key: " + testKey + `
integrations:
  postgres: {dsn: "postgres://hypha:s3cret@127.0.0.1:5432/postgres"}
snmp:
  devices:
    - {name: nas, address: 192.0.2.40, version: 3, user: hypha, auth_key: authsecret1, priv_key: privsecret1}
    - {name: sw, address: 192.0.2.1, community: c0mmunity}
`})
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if errs := c.Validate(true); len(errs) != 0 {
		t.Fatalf("erreurs : %v", errs)
	}
	y, err := c.RedactedYAML()
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{testKey, "s3cret", "authsecret1", "privsecret1", "c0mmunity"} {
		if strings.Contains(y, secret) {
			t.Errorf("secret %q affiché", secret)
		}
	}
	if !strings.Contains(y, "hypha:****@127.0.0.1") || !strings.Contains(y, "auth_protocol: sha") {
		t.Errorf("conf masquée : %s", y)
	}
	h := c.Hash()
	if h == "" || h != c.Hash() {
		t.Fatal("empreinte instable")
	}
	c.AutoUpdate = true
	if c.Hash() == h {
		t.Fatal("empreinte inchangée après un changement de conf")
	}
}

func TestValidateV04(t *testing.T) {
	c := Default()
	c.APIKey = testKey
	c.Actions.Allow = []string{"restart_service", "rm_rf"}
	c.Actions.Scripts = map[string]string{"vider": "relatif.sh", "Mauvais Nom": "/x.sh"}
	c.SNMP.Devices = []SNMPDevice{{Name: "a", Address: "192.0.2.1", Version: "3", User: "u", AuthKey: "court", PrivKey: "assezlongue"}}
	c.Checks.Processes = []ProcessCheck{{Name: "(", Match: "regex"}}
	c.normalize()
	errs := strings.Join(c.Validate(true), "\n")
	for _, want := range []string{"rm_rf", "chemin absolu", "Mauvais Nom", "auth_key de 8", "expression régulière invalide"} {
		if !strings.Contains(errs, want) {
			t.Errorf("erreur attendue « %s » absente :\n%s", want, errs)
		}
	}
	d := Default()
	d.APIKey = testKey
	d.normalize()
	if errs := d.Validate(true); len(errs) != 0 {
		t.Fatalf("défauts invalides : %v", errs)
	}
	if d.Actions.Enabled || d.AutoUpdate || d.Synthetics.Enabled || d.SNMP.Traps.Enabled {
		t.Fatal("un défaut n'est pas sûr (actions, mise à jour, synthétiques ou traps actifs)")
	}
}
