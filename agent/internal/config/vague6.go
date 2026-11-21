package config

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"
)

// Vague 6 des intégrations : Jenkins (API JSON), GitHub (connecteur en lecture seule vers
// github.com ou GitHub Enterprise Cloud), Odoo (santé, version, processus) et WSUS (sous Windows,
// coupé par défaut). Zot passe par le lecteur OpenMetrics (profil de omprofiles/vague6.go).

// IntegrationsV6 : intégrations de la vague 6, à plat dans « integrations » (inline).
type IntegrationsV6 struct {
	Jenkins JenkinsConfig `yaml:"jenkins"`
	GitHub  GitHubConfig  `yaml:"github"`
	Odoo    OdooConfig    `yaml:"odoo"`
	WSUS    WSUSConfig    `yaml:"wsus"`

	// V6Extra : entrées « instances » décodées (§ 50), remplies par normalizeV6.
	V6Extra V6Instances `yaml:"-"`
}

// V6Instances : instances décodées des intégrations de la vague 6.
type V6Instances struct {
	Jenkins []JenkinsConfig
	GitHub  []GitHubConfig
	Odoo    []OdooConfig
}

// DefaultJenkinsURL : adresse d'écoute par défaut de Jenkins.
const DefaultJenkinsURL = "http://127.0.0.1:8080"

// DefaultOdooURL : adresse d'écoute par défaut d'Odoo.
const DefaultOdooURL = "http://127.0.0.1:8069"

// DefaultGitHubAPI : API de github.com ; GitHub Enterprise Cloud avec résidence des données :
// https://api.<sous-domaine>.ghe.com.
const DefaultGitHubAPI = "https://api.github.com"

// JenkinsConfig : API JSON de Jenkins (/api/json, file d'attente, nœuds). username et password :
// un compte de lecture et son jeton d'API (droits Overall/Read, Job/Read, Agent/Read suffisent) ;
// sans compte, lecture anonyme si Jenkins la permet. jobs : motifs des noms complets suivis
// (« dossier/* », glob), vide = toutes ; max_jobs borne le nombre de tâches publiées.
type JenkinsConfig struct {
	Enabled  Tristate `yaml:"enabled"`
	URL      string   `yaml:"url"`
	Username string   `yaml:"username"`
	Password string   `yaml:"password"` // jeton d'API de l'utilisateur (ou mot de passe)
	Insecure bool     `yaml:"insecure"`
	Jobs     []string `yaml:"jobs"`
	MaxJobs  int      `yaml:"max_jobs"`

	Multi `yaml:",inline"`
}

// GitHubConfig : connecteur GitHub en lecture seule. repositories : « propriétaire/dépôt ».
// token : jeton fin (fine-grained) en lecture seule, facultatif pour un dépôt public (60 requêtes
// par heure sans jeton, 5 000 avec) ; les alertes de sécurité demandent les droits « Dependabot
// alerts », « Code scanning alerts » et « Secret scanning alerts » en lecture. security : auto =
// lues si le jeton le permet.
type GitHubConfig struct {
	Enabled      Tristate          `yaml:"enabled"`
	Token        string            `yaml:"token"`
	Repositories []string          `yaml:"repositories"`
	Endpoint     string            `yaml:"endpoint"`
	Interval     Duration          `yaml:"interval"`
	Security     Tristate          `yaml:"security"`
	Tags         map[string]string `yaml:"tags"`

	Multi `yaml:",inline"`
}

// Configured : au moins un dépôt nommé (le jeton est facultatif).
func (g GitHubConfig) Configured() bool { return len(g.Repositories) > 0 }

// OdooConfig : serveur Odoo (17 et suivants) : /web/health, version par XML-RPC (common.version,
// repli sur /web/webclient/version_info), temps de réponse ; processus locaux (maître, workers HTTP
// et cron, gevent) lus dans /proc quand l'url vise la machine (processes: false pour s'en passer).
type OdooConfig struct {
	Enabled   Tristate `yaml:"enabled"`
	URL       string   `yaml:"url"`
	Insecure  bool     `yaml:"insecure"`
	Processes *bool    `yaml:"processes"`

	Multi `yaml:",inline"`
}

// ProcessesOn : processus relevés (oui par défaut quand l'url est locale).
func (o OdooConfig) ProcessesOn() bool {
	if o.Processes != nil {
		return *o.Processes
	}
	u, err := url.Parse(o.URL)
	return err == nil && isLoopback(u.Hostname())
}

// WSUSConfig : serveur WSUS (Windows Server Update Services), lu par son API d'administration
// (PowerShell, module UpdateServices). COUPÉ par défaut : une sonde de mises à jour ne s'active
// que sur décision explicite. stale_days : ordinateurs sans rapport depuis ce nombre de jours ;
// sync_max_age : dernière synchronisation réussie plus ancienne = avertissement.
type WSUSConfig struct {
	Enabled    Tristate `yaml:"enabled"`
	StaleDays  int      `yaml:"stale_days"`
	SyncMaxAge Duration `yaml:"sync_max_age"`
}

func defaultsV6(c *Config) {
	in := &c.Integrations
	in.Jenkins = JenkinsConfig{Enabled: "auto", URL: DefaultJenkinsURL, MaxJobs: 500}
	in.GitHub = GitHubConfig{Enabled: "auto", Endpoint: DefaultGitHubAPI, Interval: Duration(5 * time.Minute), Security: "auto"}
	in.Odoo = OdooConfig{Enabled: "auto", URL: DefaultOdooURL}
	in.WSUS = WSUSConfig{Enabled: "false", StaleDays: 30, SyncMaxAge: Duration(48 * time.Hour)}
}

func (c *Config) normalizeV6() {
	in := &c.Integrations
	trim := func(s *string, def string) {
		*s = strings.TrimRight(strings.TrimSpace(*s), "/")
		if *s == "" {
			*s = def
		}
	}
	normRepos := func(g *GitHubConfig) {
		trim(&g.Endpoint, DefaultGitHubAPI)
		for i, r := range g.Repositories {
			r = strings.TrimSpace(r)
			r = strings.TrimPrefix(strings.TrimPrefix(r, "https://github.com/"), "github.com/")
			g.Repositories[i] = strings.TrimSuffix(strings.Trim(r, "/"), ".git")
		}
	}
	trim(&in.Jenkins.URL, DefaultJenkinsURL)
	trim(&in.Odoo.URL, DefaultOdooURL)
	normRepos(&in.GitHub)
	warn := func(s string) { c.Warnings = append(c.Warnings, s) }
	fail := func(s string) { c.instErrs = append(c.instErrs, s) }
	d := func() IntegrationsConfig { return Default().Integrations }
	x := &in.V6Extra
	x.Jenkins = decodeInstances("jenkins", in.Jenkins.Raw, func() JenkinsConfig { v := d().Jenkins; v.Enabled = "true"; return v },
		func(v *JenkinsConfig) *Multi { return &v.Multi }, warn, fail)
	x.GitHub = decodeInstances("github", in.GitHub.Raw, func() GitHubConfig { v := d().GitHub; v.Enabled = "true"; return v },
		func(v *GitHubConfig) *Multi { return &v.Multi }, warn, fail)
	x.Odoo = decodeInstances("odoo", in.Odoo.Raw, func() OdooConfig { v := d().Odoo; v.Enabled = "true"; return v },
		func(v *OdooConfig) *Multi { return &v.Multi }, warn, fail)
	for i := range x.Jenkins {
		trim(&x.Jenkins[i].URL, DefaultJenkinsURL)
	}
	for i := range x.Odoo {
		trim(&x.Odoo[i].URL, DefaultOdooURL)
	}
	for i := range x.GitHub {
		normRepos(&x.GitHub[i])
	}
}

var githubRepoRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9._-]{1,100}$`)

func (j JenkinsConfig) validate(at string, add, warn vfn) {
	validURL(add, at, "url", j.URL, "http", "https")
	clearSecret(warn, at, j.URL, j.Password != "")
	if (j.Username == "") != (j.Password == "") {
		add("%s : username et password (jeton d'API) vont ensemble", at)
	}
	for _, m := range j.Jobs {
		if _, err := path.Match(m, ""); err != nil || m == "" {
			add("%s.jobs : motif %q invalide", at, m)
		}
	}
	if j.MaxJobs < 1 || j.MaxJobs > 5000 {
		add("%s.max_jobs %d hors de 1 à 5000", at, j.MaxJobs)
	}
	if j.Insecure {
		warn("%s : insecure actif, certificat non vérifié", at)
	}
}

func (g GitHubConfig) validate(at string, add, warn vfn) {
	if g.Enabled == "false" {
		return
	}
	if g.Enabled == "true" && !g.Configured() {
		add("%s : repositories (propriétaire/dépôt) est nécessaire", at)
	}
	seen := map[string]bool{}
	for _, r := range g.Repositories {
		if !githubRepoRe.MatchString(r) {
			add("%s.repositories : %q attendu sous la forme propriétaire/dépôt", at, r)
		}
		if seen[strings.ToLower(r)] {
			add("%s.repositories : %q en double", at, r)
		}
		seen[strings.ToLower(r)] = true
	}
	if pu, err := url.Parse(g.Endpoint); err != nil || pu.Host == "" || (pu.Scheme != "https" && pu.Scheme != "http") {
		add("%s.endpoint invalide : %q (https://api.github.com ou https://api.<sous-domaine>.ghe.com)", at, g.Endpoint)
	} else if pu.Scheme == "http" && !isLoopback(pu.Hostname()) {
		add("%s.endpoint : https attendu (jeton envoyé à chaque appel)", at)
	}
	if g.Configured() {
		min, why := time.Minute, "API de GitHub"
		if g.Token == "" {
			// 60 requêtes par heure pour l'adresse publique, sans jeton : 2 par dépôt et par relevé.
			min, why = 5*time.Minute, "sans jeton, 60 requêtes par heure"
			if iv := g.Interval.D(); iv > 0 {
				if per := float64(time.Hour) / float64(iv) * 2 * float64(len(g.Repositories)); per > 50 {
					warn("%s : sans jeton, %.0f requêtes par heure pour %d dépôt(s) (60 permises) : allonger interval ou poser un jeton", at, per, len(g.Repositories))
				}
			}
		}
		if g.Interval.D() < min {
			add("%s.interval : %s au moins (%s)", at, min, why)
		}
	}
	if g.Security == "true" && g.Token == "" {
		add("%s.security : les alertes de sécurité demandent un jeton", at)
	}
	for k := range g.Tags {
		if !tagKeyRe.MatchString(k) {
			add("%s.tags : clé %q hors de [a-z0-9_.-/]", at, k)
		}
	}
}

func (o OdooConfig) validate(at string, add, warn vfn) {
	validURL(add, at, "url", o.URL, "http", "https")
	if o.Insecure {
		warn("%s : insecure actif, certificat non vérifié", at)
	}
}

func (c *Config) validateV6(add, warn vfn) {
	in := c.Integrations
	x := in.V6Extra
	in.Jenkins.validate("integrations.jenkins", add, warn)
	in.GitHub.validate("integrations.github", add, warn)
	in.Odoo.validate("integrations.odoo", add, warn)
	if w := in.WSUS; w.Enabled != "false" {
		if w.StaleDays < 1 || w.StaleDays > 365 {
			add("integrations.wsus.stale_days %d hors de 1 à 365", w.StaleDays)
		}
		if a := w.SyncMaxAge.D(); a < time.Hour || a > 30*24*time.Hour {
			add("integrations.wsus.sync_max_age %s hors de 1h à 30 jours", a)
		}
	}
	names := func(key, main string, list []Multi) {
		seen := map[string]bool{}
		if main != "" {
			if !instanceNameRe.MatchString(main) {
				add("integrations.%s.name invalide : %q (minuscules, chiffres, - et _, 32 au plus)", key, main)
			}
			seen[main] = true
		}
		for _, m := range list {
			at := fmt.Sprintf("integrations.%s.instances[%d]", key, m.idx)
			switch {
			case m.Name == "":
				add("%s : name manquant", at)
			case !instanceNameRe.MatchString(m.Name):
				add("%s : name invalide %q (minuscules, chiffres, - et _, 32 au plus)", at, m.Name)
			case seen[m.Name]:
				add("%s : name %q en double", at, m.Name)
			}
			seen[m.Name] = true
		}
	}
	at := func(key string, m Multi) string {
		return fmt.Sprintf("integrations.%s.instances[%d] (%s)", key, m.idx, m.Name)
	}
	var ms []Multi
	for _, v := range x.Jenkins {
		ms = append(ms, v.Multi)
		v.validate(at("jenkins", v.Multi), add, warn)
	}
	names("jenkins", in.Jenkins.Name, ms)
	ms = nil
	for _, v := range x.GitHub {
		ms = append(ms, v.Multi)
		v.validate(at("github", v.Multi), add, warn)
	}
	names("github", in.GitHub.Name, ms)
	ms = nil
	for _, v := range x.Odoo {
		ms = append(ms, v.Multi)
		v.validate(at("odoo", v.Multi), add, warn)
	}
	names("odoo", in.Odoo.Name, ms)
}
