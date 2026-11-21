package config

import (
	"strings"
	"testing"
	"time"
)

func TestV6Defaults(t *testing.T) {
	c, errs := loadV5(t, "")
	if errs != "" {
		t.Fatalf("erreurs : %s", errs)
	}
	in := c.Integrations
	if in.Jenkins.Enabled != "auto" || in.Jenkins.URL != DefaultJenkinsURL || in.Jenkins.MaxJobs != 500 ||
		in.Odoo.URL != DefaultOdooURL || !in.Odoo.ProcessesOn() || in.GitHub.Configured() ||
		in.GitHub.Endpoint != DefaultGitHubAPI || in.GitHub.Interval.D() != 5*time.Minute {
		t.Fatalf("valeurs par défaut : %+v %+v %+v", in.Jenkins, in.Odoo, in.GitHub)
	}
	// WSUS : coupé par défaut, jamais actif sans décision explicite.
	if in.WSUS.Enabled != "false" || in.WSUS.StaleDays != 30 || in.WSUS.SyncMaxAge.D() != 48*time.Hour {
		t.Fatalf("wsus : %+v", in.WSUS)
	}
	y, err := c.RedactedYAML()
	if err != nil || !strings.Contains(y, "jenkins:") || !strings.Contains(y, "wsus:") {
		t.Fatalf("conf affichée : %v", err)
	}
}

func TestV6ConfigAndValidation(t *testing.T) {
	c, errs := loadV5(t, `integrations:
  jenkins:
    url: https://ci.exemple.fr/
    username: hypha
    password: jeton-secret
    jobs: ["prod/*"]
    instances:
      - name: ci2
        url: http://192.0.2.10:8080
  github:
    token: github_pat_secret
    repositories: [s33d-fr/hypha, "https://github.com/s33d-fr/autre.git"]
    endpoint: https://api.s33d.ghe.com
    interval: 2m
    tags: {forge: ghe}
  odoo:
    url: https://compta.exemple.fr
  wsus:
    enabled: true
    stale_days: 14
`)
	if errs != "" {
		t.Fatalf("erreurs : %s", errs)
	}
	in := c.Integrations
	if in.Jenkins.URL != "https://ci.exemple.fr" || len(in.V6Extra.Jenkins) != 1 || in.V6Extra.Jenkins[0].MaxJobs != 500 {
		t.Fatalf("jenkins : %+v %+v", in.Jenkins, in.V6Extra.Jenkins)
	}
	if got := in.GitHub.Repositories; got[1] != "s33d-fr/autre" || !in.GitHub.Configured() {
		t.Fatalf("dépôts : %v", got)
	}
	if in.Odoo.ProcessesOn() {
		t.Error("processus lus pour une url distante")
	}
	y, _ := c.RedactedYAML()
	if strings.Contains(y, "jeton-secret") || strings.Contains(y, "github_pat_secret") {
		t.Fatal("secret affiché par check-config")
	}
	for yaml, want := range map[string]string{
		"integrations:\n  jenkins:\n    username: hypha\n":                                            "vont ensemble",
		"integrations:\n  jenkins:\n    jobs: [\"[\"]\n":                                              "motif",
		"integrations:\n  github:\n    repositories: [pas-un-depot]\n":                               "propriétaire/dépôt",
		"integrations:\n  github:\n    repositories: [a/b]\n    interval: 1m\n":                        "sans jeton",
		"integrations:\n  github:\n    enabled: true\n":                                              "repositories",
		"integrations:\n  github:\n    repositories: [a/b]\n    endpoint: http://api.github.com\n":     "https attendu",
		"integrations:\n  github:\n    repositories: [a/b]\n    security: true\n":                       "demandent un jeton",
		"integrations:\n  odoo:\n    url: ftp://odoo\n":                                              "inattendu",
		"integrations:\n  wsus:\n    enabled: auto\n    stale_days: 0\n":                                "stale_days",
		"integrations:\n  jenkins:\n    instances:\n      - url: http://a:8080\n":                       "name manquant",
	} {
		if _, errs := loadV5(t, yaml); !strings.Contains(errs, want) {
			t.Errorf("%q : attendu %q, reçu %q", yaml, want, errs)
		}
	}
	// Sans jeton, au-delà du quota de 60 requêtes par heure : avertissement.
	c, _ = loadV5(t, "integrations:\n  github:\n    repositories: [a/b, a/c, a/d]\n")
	if !strings.Contains(strings.Join(c.Warnings, "\n"), "requêtes par heure") {
		t.Errorf("avertissement de quota absent : %v", c.Warnings)
	}
}
