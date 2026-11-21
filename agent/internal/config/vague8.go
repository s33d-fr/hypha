package config

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Vague 8 des intégrations : réseau, stockage et sauvegardes auto-hébergés. Pi-hole (API v6),
// AdGuard Home (API /control), Unbound (unbound-control), Caddy (API d'administration), Mosquitto
// (sujets $SYS en MQTT), Proxmox Backup Server (API, jeton en lecture seule), Borg et restic
// (dernière sauvegarde de chaque dépôt). Les mesures Prometheus de Caddy et de MinIO passent par le
// lecteur OpenMetrics (profils de omprofiles/vague8.go). Chaque sonde accepte des « instances »
// (§ 50) et des étiquettes fixes (tags) posées sur toutes ses mesures et contrôles.

// IntegrationsV8 : intégrations de la vague 8, à plat dans « integrations » (inline).
type IntegrationsV8 struct {
	PiHole    PiHoleConfig     `yaml:"pihole"`
	AdGuard   AdGuardConfig    `yaml:"adguard"`
	Unbound   UnboundConfig    `yaml:"unbound"`
	Caddy     CaddyConfig      `yaml:"caddy"`
	Mosquitto MosquittoConfig  `yaml:"mosquitto"`
	PBS       PBSConfig        `yaml:"proxmox_backup"`
	Borg      BackupToolConfig `yaml:"borg"`
	Restic    BackupToolConfig `yaml:"restic"`

	// V8Extra : entrées « instances » décodées (§ 50), remplies par normalizeV8.
	V8Extra V8Instances `yaml:"-"`
}

// V8Instances : instances décodées des sondes de la vague 8.
type V8Instances struct {
	PiHole    []PiHoleConfig
	AdGuard   []AdGuardConfig
	Unbound   []UnboundConfig
	Caddy     []CaddyConfig
	Mosquitto []MosquittoConfig
	PBS       []PBSConfig
}

// PiHoleConfig : API REST de Pi-hole 6 (/api). password : mot de passe de l'interface ou, mieux, mot
// de passe d'application (Réglages › API, sans double authentification) ; vide pour un Pi-hole sans
// mot de passe. La session ouverte est gardée d'un relevé à l'autre et fermée à l'arrêt de l'agent.
// En mode auto : active si /etc/pihole existe ou si password (ou url) est posé.
type PiHoleConfig struct {
	Enabled  Tristate          `yaml:"enabled"`
	URL      string            `yaml:"url"`
	Password string            `yaml:"password"`
	Insecure bool              `yaml:"insecure"`
	Tags     map[string]string `yaml:"tags"`

	Multi `yaml:",inline"`
}

// AdGuardConfig : API d'AdGuard Home (/control), compte de l'interface en authentification de base.
// En mode auto : active dès que username et password sont posés (l'API exige un compte).
type AdGuardConfig struct {
	Enabled  Tristate          `yaml:"enabled"`
	URL      string            `yaml:"url"`
	Username string            `yaml:"username"`
	Password string            `yaml:"password"`
	Insecure bool              `yaml:"insecure"`
	Tags     map[string]string `yaml:"tags"`

	Multi `yaml:",inline"`
}

// UnboundConfig : statistiques d'Unbound par « unbound-control stats_noreset » (sans remise à zéro
// des compteurs), lancé sans shell. command : la commande de base (« docker exec unbound
// unbound-control » pour un Unbound en conteneur) ; config_file : passé par -c. En mode auto : active
// si /etc/unbound/unbound.conf existe (remote-control activé : control-enable: yes).
type UnboundConfig struct {
	Enabled    Tristate          `yaml:"enabled"`
	Command    CommandLine       `yaml:"command"`
	ConfigFile string            `yaml:"config_file"`
	Timeout    Duration          `yaml:"timeout"`
	Tags       map[string]string `yaml:"tags"`

	Multi `yaml:",inline"`
}

// CaddyConfig : API d'administration de Caddy (http://127.0.0.1:2019 par défaut), en lecture
// seule : serveurs amont du mandataire (/reverse_proxy/upstreams) et serveurs HTTP configurés. Les
// mesures Prometheus du même point passent par le profil OpenMetrics « caddy ».
type CaddyConfig struct {
	Enabled  Tristate          `yaml:"enabled"`
	URL      string            `yaml:"url"`
	Insecure bool              `yaml:"insecure"`
	Tags     map[string]string `yaml:"tags"`

	Multi `yaml:",inline"`
}

// MosquittoConfig : sujets $SYS du courtier Mosquitto, lus en MQTT 3.1.1 par un client qui ne fait
// que s'abonner (jamais de publication). url : mqtt://hôte:1883 ou mqtts://hôte:8883 ; compte en
// lecture de $SYS/# (ACL « topic read $SYS/# »). timeout : durée maximale d'un relevé. En mode auto :
// active si /etc/mosquitto existe, ou si username (ou url) est posé.
type MosquittoConfig struct {
	Enabled  Tristate          `yaml:"enabled"`
	URL      string            `yaml:"url"`
	Username string            `yaml:"username"`
	Password string            `yaml:"password"`
	CAFile   string            `yaml:"ca_file"`
	Insecure bool              `yaml:"insecure"`
	Timeout  Duration          `yaml:"timeout"`
	Tags     map[string]string `yaml:"tags"`

	Multi `yaml:",inline"`
}

// PBSConfig : API de Proxmox Backup Server (https://hôte:8007), jeton d'API au rôle Audit
// (« proxmox-backup-manager user generate-token », ACL Audit sur / pour l'utilisateur ET le
// jeton). Vérification du certificat : fingerprint (empreinte SHA-256, comme PBS_FINGERPRINT),
// ca_file, ou insecure. L'abonnement n'est jamais lu. En mode auto : active dès que le jeton est posé.
type PBSConfig struct {
	Enabled     Tristate          `yaml:"enabled"`
	URL         string            `yaml:"url"`
	TokenID     string            `yaml:"token_id"` // utilisateur@domaine!jeton
	TokenSecret string            `yaml:"token_secret"`
	Fingerprint string            `yaml:"fingerprint"`
	CAFile      string            `yaml:"ca_file"`
	Insecure    bool              `yaml:"insecure"`
	Interval    Duration          `yaml:"interval"`
	MaxGroups   int               `yaml:"max_groups"` // groupes de sauvegarde suivis par magasin (500)
	Tags        map[string]string `yaml:"tags"`

	Multi `yaml:",inline"`
}

// BackupToolConfig : Borg ou restic, dépôts relus à cadence lente (15 min par défaut) par l'outil
// installé, sans shell, en lecture seule et sans verrou (--bypass-lock, --no-lock : une sauvegarde
// ou un élagage en cours ne sont jamais gênés). stats : taille du dépôt (borg info, restic stats),
// plus coûteuse, relue toutes les 6 h. En mode auto : actif dès qu'un dépôt est déclaré.
type BackupToolConfig struct {
	Enabled      Tristate     `yaml:"enabled"`
	Command      CommandLine  `yaml:"command"`
	Interval     Duration     `yaml:"interval"`
	Timeout      Duration     `yaml:"timeout"`
	Stats        bool         `yaml:"stats"`
	Repositories []BackupRepo `yaml:"repositories"`
}

// BackupRepo : un dépôt. Borg : repository (chemin, ssh://…), passphrase_file (fichier réservé à
// root, mode 600 ou 400), rsh (BORG_RSH : clé ssh, options). restic : repository ou repository_file
// (adresse avec identifiants, dans un fichier réservé à root), password_file (idem). env : variables
// sans secret passées à l'outil (AWS_SHARED_CREDENTIALS_FILE…). Un secret ne va jamais dans env.
type BackupRepo struct {
	Name           string            `yaml:"name"`
	Repository     string            `yaml:"repository"`
	RepositoryFile string            `yaml:"repository_file"`
	PassphraseFile string            `yaml:"passphrase_file"`
	PasswordFile   string            `yaml:"password_file"`
	RSH            string            `yaml:"rsh"`
	Env            map[string]string `yaml:"env"`
	Tags           map[string]string `yaml:"tags"`
}

// DefaultPBSMaxGroups : groupes de sauvegarde suivis par magasin.
const DefaultPBSMaxGroups = 500

func defaultsV8(c *Config) {
	in := &c.Integrations.IntegrationsV8
	in.PiHole = PiHoleConfig{Enabled: "auto", URL: "http://127.0.0.1"}
	in.AdGuard = AdGuardConfig{Enabled: "auto", URL: "http://127.0.0.1:3000"}
	in.Unbound = UnboundConfig{Enabled: "auto", Command: CommandLine{"unbound-control"}, Timeout: Duration(10 * time.Second)}
	in.Caddy = CaddyConfig{Enabled: "auto", URL: "http://127.0.0.1:2019"}
	in.Mosquitto = MosquittoConfig{Enabled: "auto", URL: "mqtt://127.0.0.1:1883", Timeout: Duration(5 * time.Second)}
	in.PBS = PBSConfig{Enabled: "auto", URL: "https://127.0.0.1:8007", Interval: Duration(time.Minute), MaxGroups: DefaultPBSMaxGroups}
	in.Borg = BackupToolConfig{Enabled: "auto", Command: CommandLine{"borg"}, Interval: Duration(15 * time.Minute), Timeout: Duration(2 * time.Minute)}
	in.Restic = BackupToolConfig{Enabled: "auto", Command: CommandLine{"restic"}, Interval: Duration(15 * time.Minute), Timeout: Duration(2 * time.Minute)}
}

func (c *Config) normalizeV8() {
	in := &c.Integrations.IntegrationsV8
	d := func() IntegrationsV8 { return Default().Integrations.IntegrationsV8 }
	warn := func(s string) { c.Warnings = append(c.Warnings, s) }
	fail := func(s string) { c.instErrs = append(c.instErrs, s) }
	x := &in.V8Extra
	x.PiHole = decodeInstances("pihole", in.PiHole.Raw, func() PiHoleConfig { v := d().PiHole; v.Enabled = "true"; return v },
		func(v *PiHoleConfig) *Multi { return &v.Multi }, warn, fail)
	x.AdGuard = decodeInstances("adguard", in.AdGuard.Raw, func() AdGuardConfig { v := d().AdGuard; v.Enabled = "true"; return v },
		func(v *AdGuardConfig) *Multi { return &v.Multi }, warn, fail)
	x.Unbound = decodeInstances("unbound", in.Unbound.Raw, func() UnboundConfig { v := d().Unbound; v.Enabled = "true"; return v },
		func(v *UnboundConfig) *Multi { return &v.Multi }, warn, fail)
	x.Caddy = decodeInstances("caddy", in.Caddy.Raw, func() CaddyConfig { v := d().Caddy; v.Enabled = "true"; return v },
		func(v *CaddyConfig) *Multi { return &v.Multi }, warn, fail)
	x.Mosquitto = decodeInstances("mosquitto", in.Mosquitto.Raw, func() MosquittoConfig { v := d().Mosquitto; v.Enabled = "true"; return v },
		func(v *MosquittoConfig) *Multi { return &v.Multi }, warn, fail)
	x.PBS = decodeInstances("proxmox_backup", in.PBS.Raw, func() PBSConfig { v := d().PBS; v.Enabled = "true"; return v },
		func(v *PBSConfig) *Multi { return &v.Multi }, warn, fail)
	trim := func(s *string) { *s = strings.TrimRight(strings.TrimSpace(*s), "/") }
	trim(&in.PiHole.URL)
	trim(&in.AdGuard.URL)
	trim(&in.Caddy.URL)
	trim(&in.PBS.URL)
	in.Mosquitto.URL = strings.TrimSpace(in.Mosquitto.URL)
	for i := range x.PiHole {
		trim(&x.PiHole[i].URL)
	}
	for i := range x.AdGuard {
		trim(&x.AdGuard[i].URL)
	}
	for i := range x.Caddy {
		trim(&x.Caddy[i].URL)
	}
	for i := range x.PBS {
		trim(&x.PBS[i].URL)
	}
	for _, t := range []*BackupToolConfig{&in.Borg, &in.Restic} {
		for i := range t.Repositories {
			r := &t.Repositories[i]
			r.Name, r.Repository = strings.TrimSpace(r.Name), strings.TrimSpace(r.Repository)
		}
	}
	if in.PBS.MaxGroups == 0 {
		in.PBS.MaxGroups = DefaultPBSMaxGroups
	}
}

// ── Validation ─────────────────────────────────────────────────────────────────────────────

var (
	pbsTokenRe   = regexp.MustCompile(`^[A-Za-z0-9._\-]+@[A-Za-z0-9._\-]+![A-Za-z0-9._\-]+$`)
	sha256FprRe  = regexp.MustCompile(`^([0-9a-fA-F]{2}:){31}[0-9a-fA-F]{2}$|^[0-9a-fA-F]{64}$`)
	backupEnvRe  = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)
	urlPassRe    = regexp.MustCompile(`://[^/\s@:]+:[^/\s@]+@`) // rest:http://u:p@…, s3:https://u:p@…
	secretEnvRe  = regexp.MustCompile(`(PASS|SECRET|TOKEN|KEY)`)
	reservedEnvs = map[string]bool{"BORG_PASSPHRASE": true, "BORG_PASSCOMMAND": true, "BORG_PASSPHRASE_FD": true,
		"RESTIC_PASSWORD": true, "RESTIC_PASSWORD_COMMAND": true, "BORG_RSH": true, "BORG_BASE_DIR": true, "PATH": true}
)

func validTags(add vfn, at string, tags map[string]string) {
	for k := range tags {
		if !tagKeyRe.MatchString(k) {
			add("%s.tags : clé %q hors de [a-z0-9_.-/]", at, k)
		}
	}
}

func validCommand(add vfn, at string, cmd CommandLine, t Duration, minT, maxT time.Duration) {
	if len(cmd) == 0 || cmd[0] == "" {
		add("%s.command manquante", at)
	} else if c := cmd[0]; !absPath(c) && strings.ContainsAny(c, `/\`) {
		add("%s.command : %q : chemin absolu ou simple nom de commande attendu", at, c)
	}
	if d := t.D(); d < minT || d > maxT {
		add("%s.timeout %s hors de %s à %s", at, d, minT, maxT)
	}
}

func (p PiHoleConfig) validate(at string, add, warn vfn) {
	validURL(add, at, "url", p.URL, "http", "https")
	clearSecret(warn, at, p.URL, p.Password != "")
	if p.Insecure {
		warn("%s : insecure actif, certificat non vérifié", at)
	}
	validTags(add, at, p.Tags)
}

func (p AdGuardConfig) validate(at string, add, warn vfn) {
	validURL(add, at, "url", p.URL, "http", "https")
	clearSecret(warn, at, p.URL, p.Password != "")
	if (p.Username == "") != (p.Password == "") {
		add("%s : username et password vont ensemble", at)
	}
	if p.Enabled == "true" && p.Username == "" {
		add("%s : username et password attendus (l'API d'AdGuard Home exige un compte)", at)
	}
	if p.Insecure {
		warn("%s : insecure actif, certificat non vérifié", at)
	}
	validTags(add, at, p.Tags)
}

func (p UnboundConfig) validate(at string, add, _ vfn) {
	validCommand(add, at, p.Command, p.Timeout, time.Second, time.Minute)
	if p.ConfigFile != "" && !absPath(p.ConfigFile) {
		add("%s.config_file : chemin absolu attendu, pas %q", at, p.ConfigFile)
	}
	validTags(add, at, p.Tags)
}

func (p CaddyConfig) validate(at string, add, warn vfn) {
	validURL(add, at, "url", p.URL, "http", "https")
	if p.Insecure {
		warn("%s : insecure actif, certificat non vérifié", at)
	}
	validTags(add, at, p.Tags)
}

func (p MosquittoConfig) validate(at string, add, warn vfn) {
	u, err := url.Parse(p.URL)
	switch {
	case err != nil || u.Host == "" || (u.Scheme != "mqtt" && u.Scheme != "mqtts"):
		add("%s.url invalide : %q (mqtt://hôte:1883 ou mqtts://hôte:8883)", at, p.URL)
	case u.User != nil:
		add("%s.url : pas d'identifiants dans l'url (username et password)", at)
	case u.Scheme == "mqtt" && p.Password != "" && u.Hostname() != "localhost" && !isLoopback(u.Hostname()):
		warn("%s : mot de passe envoyé en clair (mqtt:// vers %s ; préférer mqtts://)", at, u.Hostname())
	}
	if p.Password != "" && p.Username == "" {
		add("%s : password sans username", at)
	}
	if p.CAFile != "" && !absPath(p.CAFile) {
		add("%s.ca_file : chemin absolu attendu, pas %q", at, p.CAFile)
	}
	if d := p.Timeout.D(); d < time.Second || d > 30*time.Second {
		add("%s.timeout %s hors de 1s à 30s", at, d)
	}
	if p.Insecure {
		warn("%s : insecure actif, certificat non vérifié", at)
	}
	validTags(add, at, p.Tags)
}

func (p PBSConfig) validate(at string, add, warn vfn) {
	validURL(add, at, "url", p.URL, "https")
	if (p.TokenID == "") != (p.TokenSecret == "") {
		add("%s : token_id et token_secret vont ensemble", at)
	}
	if p.TokenID != "" && !pbsTokenRe.MatchString(p.TokenID) {
		add("%s.token_id : forme utilisateur@domaine!jeton attendue, pas %q", at, p.TokenID)
	}
	if p.Enabled == "true" && p.TokenID == "" {
		add("%s : token_id et token_secret attendus (jeton d'API au rôle Audit)", at)
	}
	if p.Fingerprint != "" && !sha256FprRe.MatchString(p.Fingerprint) {
		add("%s.fingerprint : empreinte SHA-256 attendue (64 chiffres hexadécimaux, avec ou sans « : »)", at)
	}
	if p.CAFile != "" && !absPath(p.CAFile) {
		add("%s.ca_file : chemin absolu attendu, pas %q", at, p.CAFile)
	}
	if p.Insecure {
		warn("%s : insecure actif, certificat non vérifié", at)
	} else if p.Fingerprint == "" && p.CAFile == "" && p.TokenID != "" {
		if u, err := url.Parse(p.URL); err == nil && (u.Hostname() == "localhost" || isLoopback(u.Hostname())) {
			warn("%s : certificat autosigné de PBS non vérifiable sans fingerprint ni ca_file", at)
		}
	}
	if d := p.Interval.D(); d < 30*time.Second || d > time.Hour {
		add("%s.interval %s hors de 30s à 1h", at, d)
	}
	if p.MaxGroups < 1 || p.MaxGroups > 5000 {
		add("%s.max_groups %d hors de 1 à 5000", at, p.MaxGroups)
	}
	validTags(add, at, p.Tags)
}

func (t BackupToolConfig) validate(at, tool string, add, warn vfn) {
	validCommand(add, at, t.Command, t.Timeout, 10*time.Second, 30*time.Minute)
	if d := t.Interval.D(); d < 5*time.Minute || d > 24*time.Hour {
		add("%s.interval %s hors de 5m à 24h", at, d)
	}
	if t.Enabled == "true" && len(t.Repositories) == 0 {
		add("%s : enabled sans repositories", at)
	}
	if len(t.Repositories) > 50 {
		add("%s.repositories : %d dépôts, 50 au plus", at, len(t.Repositories))
	}
	seen := map[string]bool{}
	for i, r := range t.Repositories {
		ra := fmt.Sprintf("%s.repositories[%d]", at, i)
		switch {
		case r.Name == "":
			add("%s : name manquant", ra)
		case !instanceNameRe.MatchString(r.Name):
			add("%s : name invalide %q (minuscules, chiffres, - et _, 32 au plus)", ra, r.Name)
		case seen[r.Name]:
			add("%s : name %q en double", ra, r.Name)
		}
		seen[r.Name] = true
		if r.Name != "" {
			ra += " (" + r.Name + ")"
		}
		if strings.HasPrefix(r.Repository, "-") || strings.ContainsAny(r.Repository, "\n\r\x00") {
			add("%s.repository invalide", ra)
		}
		for k, v := range map[string]string{"repository_file": r.RepositoryFile, "passphrase_file": r.PassphraseFile, "password_file": r.PasswordFile} {
			if v != "" && !absPath(v) {
				add("%s.%s : chemin absolu attendu, pas %q", ra, k, v)
			}
		}
		if urlPassRe.MatchString(r.Repository) {
			add("%s.repository : mot de passe dans l'adresse ; le mettre dans un fichier (repository_file)", ra)
		}
		switch tool {
		case "borg":
			if r.Repository == "" {
				add("%s.repository manquant", ra)
			}
			if r.RepositoryFile != "" || r.PasswordFile != "" {
				add("%s : repository_file et password_file sont des réglages de restic (borg : passphrase_file)", ra)
			}
		case "restic":
			if (r.Repository == "") == (r.RepositoryFile == "") {
				add("%s : repository ou repository_file attendu (un seul des deux)", ra)
			}
			if r.PasswordFile == "" {
				add("%s.password_file manquant (fichier réservé à root)", ra)
			}
			if r.PassphraseFile != "" || r.RSH != "" {
				add("%s : passphrase_file et rsh sont des réglages de borg (restic : password_file)", ra)
			}
		}
		if strings.ContainsAny(r.RSH, "\n\r\x00") {
			add("%s.rsh invalide", ra)
		}
		for k := range r.Env {
			switch {
			case !backupEnvRe.MatchString(k):
				add("%s.env : nom de variable %q invalide", ra, k)
			case reservedEnvs[k]:
				add("%s.env : %s est posé par l'agent (passphrase_file, password_file, rsh)", ra, k)
			case secretEnvRe.MatchString(k) && !strings.HasSuffix(k, "_FILE"):
				warn("%s.env : %s ressemble à un secret ; préférer un fichier réservé à root (variable *_FILE)", ra, k)
			}
		}
		validTags(add, ra, r.Tags)
	}
}

func (c *Config) validateV8(add, warn vfn) {
	in := c.Integrations.IntegrationsV8
	x := in.V8Extra
	const p = "integrations."
	in.PiHole.validate(p+"pihole", add, warn)
	in.AdGuard.validate(p+"adguard", add, warn)
	in.Unbound.validate(p+"unbound", add, warn)
	in.Caddy.validate(p+"caddy", add, warn)
	in.Mosquitto.validate(p+"mosquitto", add, warn)
	in.PBS.validate(p+"proxmox_backup", add, warn)
	in.Borg.validate(p+"borg", "borg", add, warn)
	in.Restic.validate(p+"restic", "restic", add, warn)

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
	ms := func(n int, get func(int) Multi) []Multi {
		out := make([]Multi, n)
		for i := range out {
			out[i] = get(i)
		}
		return out
	}
	names("pihole", in.PiHole.Name, ms(len(x.PiHole), func(i int) Multi { return x.PiHole[i].Multi }))
	for _, v := range x.PiHole {
		v.validate(at("pihole", v.Multi), add, warn)
	}
	names("adguard", in.AdGuard.Name, ms(len(x.AdGuard), func(i int) Multi { return x.AdGuard[i].Multi }))
	for _, v := range x.AdGuard {
		v.validate(at("adguard", v.Multi), add, warn)
	}
	names("unbound", in.Unbound.Name, ms(len(x.Unbound), func(i int) Multi { return x.Unbound[i].Multi }))
	for _, v := range x.Unbound {
		v.validate(at("unbound", v.Multi), add, warn)
	}
	names("caddy", in.Caddy.Name, ms(len(x.Caddy), func(i int) Multi { return x.Caddy[i].Multi }))
	for _, v := range x.Caddy {
		v.validate(at("caddy", v.Multi), add, warn)
	}
	names("mosquitto", in.Mosquitto.Name, ms(len(x.Mosquitto), func(i int) Multi { return x.Mosquitto[i].Multi }))
	for _, v := range x.Mosquitto {
		v.validate(at("mosquitto", v.Multi), add, warn)
	}
	names("proxmox_backup", in.PBS.Name, ms(len(x.PBS), func(i int) Multi { return x.PBS[i].Multi }))
	for _, v := range x.PBS {
		v.validate(at("proxmox_backup", v.Multi), add, warn)
	}
}
