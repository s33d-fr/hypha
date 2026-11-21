package config

import (
	"fmt"
	"strings"
)

// Vague 7 des intégrations (applications auto-hébergées) : Nextcloud, Jellyfin, Syncthing et Immich
// par leur API REST (sondes dédiées) ; Gitea/Forgejo, Synapse, Home Assistant et Mastodon passent
// par le lecteur OpenMetrics (profils de omprofiles/vague7.go).

// IntegrationsV7 : intégrations de la vague 7, à plat dans « integrations » (inline).
type IntegrationsV7 struct {
	Nextcloud NextcloudConfig `yaml:"nextcloud"`
	Jellyfin  AppAPIConfig    `yaml:"jellyfin"`
	Syncthing AppAPIConfig    `yaml:"syncthing"`
	Immich    AppAPIConfig    `yaml:"immich"`

	// V7Extra : entrées « instances » décodées (§ 50), remplies par normalizeV7.
	V7Extra V7Instances `yaml:"-"`
}

// V7Instances : instances décodées des sondes de la vague 7.
type V7Instances struct {
	Nextcloud []NextcloudConfig
	Jellyfin  []AppAPIConfig
	Syncthing []AppAPIConfig
	Immich    []AppAPIConfig
}

// AppAPIConfig : application lue par son API REST avec une clé (Jellyfin, Syncthing, Immich).
// api_key_file : fichier qui porte la clé, relu à chaque relevé (préférable à api_key en clair).
type AppAPIConfig struct {
	Enabled    Tristate `yaml:"enabled"`
	URL        string   `yaml:"url"`
	APIKey     string   `yaml:"api_key"`
	APIKeyFile string   `yaml:"api_key_file"`
	Insecure   bool     `yaml:"insecure"`

	Multi `yaml:",inline"`
}

// HasKey dit si une clé (en clair ou par fichier) est posée.
func (a AppAPIConfig) HasKey() bool { return a.APIKey != "" || a.APIKeyFile != "" }

// NextcloudConfig : status.php (sans compte) et l'API serverinfo, avec le jeton de l'application
// serverinfo (occ config:app:set serverinfo token --value …, en-tête NC-Token) ou un compte
// administrateur (username et mot de passe d'application).
type NextcloudConfig struct {
	Enabled   Tristate `yaml:"enabled"`
	URL       string   `yaml:"url"`
	Token     string   `yaml:"token"`
	TokenFile string   `yaml:"token_file"`
	Username  string   `yaml:"username"`
	Password  string   `yaml:"password"`
	Insecure  bool     `yaml:"insecure"`

	Multi `yaml:",inline"`
}

// Adresses par défaut : Jellyfin et Syncthing écoutent sur un port qui leur est propre (détection
// automatique sans risque) ; Nextcloud et Immich n'ont pas d'adresse propre, ils attendent leur url.
const (
	DefaultJellyfinURL  = "http://127.0.0.1:8096"
	DefaultSyncthingURL = "http://127.0.0.1:8384"
)

func defaultsV7(c *Config) {
	in := &c.Integrations.IntegrationsV7
	in.Nextcloud = NextcloudConfig{Enabled: "auto"}
	in.Jellyfin = AppAPIConfig{Enabled: "auto", URL: DefaultJellyfinURL}
	in.Syncthing = AppAPIConfig{Enabled: "auto", URL: DefaultSyncthingURL}
	in.Immich = AppAPIConfig{Enabled: "auto"}
}

func (c *Config) normalizeV7() {
	in := &c.Integrations.IntegrationsV7
	d := func() IntegrationsV7 { return Default().Integrations.IntegrationsV7 }
	warn := func(s string) { c.Warnings = append(c.Warnings, s) }
	fail := func(s string) { c.instErrs = append(c.instErrs, s) }
	trim := func(a *AppAPIConfig) {
		a.URL, a.APIKey, a.APIKeyFile = strings.TrimSpace(a.URL), strings.TrimSpace(a.APIKey), strings.TrimSpace(a.APIKeyFile)
	}
	trim(&in.Jellyfin)
	trim(&in.Syncthing)
	trim(&in.Immich)
	in.Nextcloud.URL, in.Nextcloud.Token = strings.TrimSpace(in.Nextcloud.URL), strings.TrimSpace(in.Nextcloud.Token)
	in.Nextcloud.TokenFile = strings.TrimSpace(in.Nextcloud.TokenFile)
	x := &in.V7Extra
	x.Nextcloud = decodeInstances("nextcloud", in.Nextcloud.Raw, func() NextcloudConfig { v := d().Nextcloud; v.Enabled = "true"; return v },
		func(v *NextcloudConfig) *Multi { return &v.Multi }, warn, fail)
	x.Jellyfin = decodeInstances("jellyfin", in.Jellyfin.Raw, func() AppAPIConfig { v := d().Jellyfin; v.Enabled = "true"; return v },
		func(v *AppAPIConfig) *Multi { return &v.Multi }, warn, fail)
	x.Syncthing = decodeInstances("syncthing", in.Syncthing.Raw, func() AppAPIConfig { v := d().Syncthing; v.Enabled = "true"; return v },
		func(v *AppAPIConfig) *Multi { return &v.Multi }, warn, fail)
	x.Immich = decodeInstances("immich", in.Immich.Raw, func() AppAPIConfig { v := d().Immich; v.Enabled = "true"; return v },
		func(v *AppAPIConfig) *Multi { return &v.Multi }, warn, fail)
	for _, l := range [][]AppAPIConfig{x.Jellyfin, x.Syncthing, x.Immich} {
		for i := range l {
			trim(&l[i])
		}
	}
	for i := range x.Nextcloud {
		n := &x.Nextcloud[i]
		n.URL, n.Token, n.TokenFile = strings.TrimSpace(n.URL), strings.TrimSpace(n.Token), strings.TrimSpace(n.TokenFile)
	}
}

func (a AppAPIConfig) validate(at string, add, warn vfn) {
	validURL(add, at, "url", a.URL, "http", "https")
	clearSecret(warn, at, a.URL, a.HasKey())
	if a.APIKey != "" && a.APIKeyFile != "" {
		add("%s : api_key et api_key_file s'excluent", at)
	}
	if a.APIKeyFile != "" && !absPath(a.APIKeyFile) {
		add("%s.api_key_file : chemin absolu attendu, pas %q", at, a.APIKeyFile)
	}
	if a.Enabled == "true" && a.URL == "" {
		add("%s : enabled sans url", at)
	}
	if a.Insecure {
		warn("%s : insecure actif, certificat non vérifié", at)
	}
}

func (n NextcloudConfig) validate(at string, add, warn vfn) {
	validURL(add, at, "url", n.URL, "http", "https")
	clearSecret(warn, at, n.URL, n.Token != "" || n.TokenFile != "" || n.Password != "")
	if n.Token != "" && n.TokenFile != "" {
		add("%s : token et token_file s'excluent", at)
	}
	if n.TokenFile != "" && !absPath(n.TokenFile) {
		add("%s.token_file : chemin absolu attendu, pas %q", at, n.TokenFile)
	}
	if (n.Username == "") != (n.Password == "") {
		add("%s : username et password vont ensemble", at)
	}
	if n.Enabled == "true" && n.URL == "" {
		add("%s : enabled sans url", at)
	}
	if n.Insecure {
		warn("%s : insecure actif, certificat non vérifié", at)
	}
}

func (c *Config) validateV7(add, warn vfn) {
	in := c.Integrations.IntegrationsV7
	x := in.V7Extra
	const p = "integrations."
	in.Nextcloud.validate(p+"nextcloud", add, warn)
	in.Jellyfin.validate(p+"jellyfin", add, warn)
	in.Syncthing.validate(p+"syncthing", add, warn)
	in.Immich.validate(p+"immich", add, warn)
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
	nm := make([]Multi, len(x.Nextcloud))
	for i, v := range x.Nextcloud {
		nm[i] = v.Multi
		v.validate(at("nextcloud", v.Multi), add, warn)
		if v.URL == "" {
			add("%s : url manquante", at("nextcloud", v.Multi))
		}
	}
	names("nextcloud", in.Nextcloud.Name, nm)
	for _, s := range []struct {
		key  string
		main AppAPIConfig
		list []AppAPIConfig
	}{{"jellyfin", in.Jellyfin, x.Jellyfin}, {"syncthing", in.Syncthing, x.Syncthing}, {"immich", in.Immich, x.Immich}} {
		ms := make([]Multi, len(s.list))
		for i, v := range s.list {
			ms[i] = v.Multi
			v.validate(at(s.key, v.Multi), add, warn)
			if v.URL == "" {
				add("%s : url manquante", at(s.key, v.Multi))
			}
		}
		names(s.key, s.main.Name, ms)
	}
}
