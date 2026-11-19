package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadDefaultsEnvAndWarnings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hypha.yaml")
	content := "\xef\xbb\xbfapi_key: hph_" + strings.Repeat("a", 40) + `
url: https://exemple.test/
tags: {env: prod, rack: 12}
interval: 30
cle_inconnue: 1
logs:
  files:
    - {path: /var/log/*.log}
checks:
  http: [{name: site, url: "https://exemple.test"}]
containers: {enabled: true}
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HYPHA_URL", "https://autre.test")
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if errs := c.Validate(true); len(errs) != 0 {
		t.Fatalf("erreurs inattendues : %v", errs)
	}
	switch {
	case c.URL != "https://autre.test":
		t.Errorf("HYPHA_URL non appliquée : %q", c.URL)
	case c.Interval.D() != 30*time.Second:
		t.Errorf("intervalle en secondes : %s", c.Interval.D())
	case c.Tags["rack"] != "12":
		t.Errorf("étiquette numérique : %v", c.Tags)
	case !c.Logs.Enabled || !c.Logs.Journald || len(c.Logs.WindowsEventlog) != 2:
		t.Errorf("valeurs par défaut des journaux perdues : %+v", c.Logs)
	case c.Checks.HTTP[0].ExpectStatus != 200 || c.Checks.HTTP[0].Timeout.D() != 5*time.Second:
		t.Errorf("défauts du contrôle HTTP : %+v", c.Checks.HTTP[0])
	case c.Containers.Enabled != "true":
		t.Errorf("containers.enabled : %q", c.Containers.Enabled)
	case !c.StatsD.Enabled || c.StatsD.Listen != "127.0.0.1:8125":
		t.Errorf("statsd par défaut : %+v", c.StatsD)
	}
	if len(c.Warnings) == 0 || !strings.Contains(strings.Join(c.Warnings, " "), "cle_inconnue") {
		t.Errorf("clé inconnue non signalée : %v", c.Warnings)
	}
}

func TestValidateErrors(t *testing.T) {
	c := Default()
	c.URL = "ftp://x"
	c.Tags = map[string]string{"Env": "x"}
	c.Logs.ExcludePatterns = []string{"("}
	c.Checks.TCP = []TCPCheck{{Name: "b", Address: "sans-port"}}
	c.Checks.WindowsServicesExclude = []string{"^Google", "[z-a]"}
	errs := c.Validate(true)
	if len(errs) != 6 { // clé absente, url, étiquette, motif de journal, adresse, motif de service
		t.Fatalf("attendu 6 erreurs : %v", errs)
	}
}
