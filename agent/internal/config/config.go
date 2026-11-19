// Package config lit et valide hypha.yaml (docs/CONTRAT.md § 5).
//
// Principe : on part des valeurs par défaut, puis le fichier ne remplace que les clés
// qu'il contient. Les variables d'environnement HYPHA_API_KEY et HYPHA_URL l'emportent
// sur le fichier (pose par Ansible ou conteneur sans réécrire la conf).
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config reprend hypha.yaml clé pour clé.
type Config struct {
	APIKey        string             `yaml:"api_key"`
	URL           string             `yaml:"url"`
	Hostname      string             `yaml:"hostname"`
	RawTags       map[string]any     `yaml:"tags"`
	Interval      Duration           `yaml:"interval"`
	TLSSkipVerify bool               `yaml:"tls_skip_verify"`
	Proxy         string             `yaml:"proxy"`
	Buffer        BufferConfig       `yaml:"buffer"`
	Logs          LogsConfig         `yaml:"logs"`
	Processes     ProcessesConfig    `yaml:"processes"`
	Network       NetworkConfig      `yaml:"network"`
	Containers    ContainersConfig   `yaml:"containers"`
	Checks        ChecksConfig       `yaml:"checks"`
	StatsD        ListenerConfig     `yaml:"statsd"`
	OTLP          ListenerConfig     `yaml:"otlp"`
	Integrations  IntegrationsConfig `yaml:"integrations"`

	// 0.4.0 (CONTRAT-04) : mise à jour signée, canal de contrôle, actions à distance,
	// emplacement privé de synthétiques, équipements SNMP, inventaire.
	AutoUpdate bool             `yaml:"auto_update"`
	Control    ControlConfig    `yaml:"control"`
	Actions    ActionsConfig    `yaml:"actions"`
	Synthetics SyntheticsConfig `yaml:"synthetics"`
	SNMP       SNMPConfig       `yaml:"snmp"`
	Inventory  InventoryConfig  `yaml:"inventory"`

	// Tags : étiquettes de la conf, valeurs converties en texte (un `rack: 12` YAML est un entier).
	Tags map[string]string `yaml:"-"`
	// Path : fichier principal.
	Path string `yaml:"-"`
	// Files : fichiers effectivement lus, dans l'ordre de fusion (principal, puis conf.d).
	Files []string `yaml:"-"`
	// origins : chemin de clé (« snmp.devices ») → fichier qui l'a posé en dernier.
	origins map[string]string
	// instErrs : erreurs de décodage des instances (§ 50), rendues par Validate.
	instErrs []string
	// Warnings : remarques non bloquantes (clé inconnue, URL en clair…).
	Warnings []string `yaml:"-"`
	// Sources des valeurs surchargées par l'environnement, pour check-config.
	FromEnv []string `yaml:"-"`
}

// BufferConfig : tampon disque des envois en échec.
type BufferConfig struct {
	Dir   string `yaml:"dir"`
	MaxMB int    `yaml:"max_mb"`
}

// LogsConfig : collecte des journaux.
type LogsConfig struct {
	Enabled         bool         `yaml:"enabled"`
	Journald        bool         `yaml:"journald"`
	WindowsEventlog []string     `yaml:"windows_eventlog"`
	Files           []FileSource `yaml:"files"`
	ExcludePatterns []string     `yaml:"exclude_patterns"`
}

// FileSource : un motif glob de fichiers journaux.
type FileSource struct {
	Path    string `yaml:"path"`
	Service string `yaml:"service"`
	Source  string `yaml:"source"`
}

// ProcessesConfig : instantané des processus.
type ProcessesConfig struct {
	Enabled bool     `yaml:"enabled"`
	Include []string `yaml:"include"` // expressions régulières sur le nom ou la ligne de commande
}

// NetworkConfig : métriques réseau et instantané des connexions.
type NetworkConfig struct {
	Enabled bool `yaml:"enabled"`
	// ExcludeInterfaces : motifs glob d'interfaces ignorées (le contrat la dit « réglable »).
	ExcludeInterfaces []string `yaml:"exclude_interfaces"`
}

// ContainersConfig : conteneurs Docker.
type ContainersConfig struct {
	Enabled Tristate `yaml:"enabled"`
}

// ChecksConfig : contrôles de service.
type ChecksConfig struct {
	SystemdUnits    []string `yaml:"systemd_units"`
	WindowsServices []string `yaml:"windows_services"`
	// WindowsServicesExclude : motifs (expressions régulières, sans distinction de casse)
	// de services automatiques à ne pas signaler quand ils sont arrêtés, en plus de la
	// liste intégrée (services de mise à jour, services sur déclencheur…).
	WindowsServicesExclude []string `yaml:"windows_services_exclude"`
	// WindowsAutoServices : signaler les services à démarrage automatique arrêtés ?
	// auto = oui sur Windows Server, non sur une édition de poste de travail (où les
	// logiciels de constructeur en posent beaucoup) ; always ; never.
	WindowsAutoServices string         `yaml:"windows_auto_services"`
	HTTP                []HTTPCheck    `yaml:"http"`
	TCP                 []TCPCheck     `yaml:"tcp"`
	Processes           []ProcessCheck `yaml:"processes"`
}

// HTTPCheck : contrôle HTTP déclaré dans la conf.
type HTTPCheck struct {
	Name         string   `yaml:"name"`
	URL          string   `yaml:"url"`
	ExpectStatus int      `yaml:"expect_status"`
	Timeout      Duration `yaml:"timeout"`
}

// TCPCheck : contrôle d'ouverture de port TCP.
type TCPCheck struct {
	Name    string   `yaml:"name"`
	Address string   `yaml:"address"`
	Timeout Duration `yaml:"timeout"`
}

// IntegrationsConfig : intégrations avec les logiciels de l'hôte.
type IntegrationsConfig struct {
	Postgres PostgresConfig `yaml:"postgres"`
	HAProxy  HAProxyConfig  `yaml:"haproxy"`
	Nginx    NginxConfig    `yaml:"nginx"`

	// § 31 : chacune s'active seule (auto) si son logiciel est détecté.
	Apache        URLIntegration    `yaml:"apache"`
	Bind          URLIntegration    `yaml:"bind"`
	Postfix       IntegrationToggle `yaml:"postfix"`
	Dovecot       IntegrationToggle `yaml:"dovecot"`
	OpenVPN       OpenVPNConfig     `yaml:"openvpn"`
	Fail2ban      IntegrationToggle `yaml:"fail2ban"`
	X509          X509Config        `yaml:"x509"`
	SMART         IntegrationToggle `yaml:"smart"`
	Hwmon         IntegrationToggle `yaml:"hwmon"`
	NTP           IntegrationToggle `yaml:"ntp"`
	VirtualBox    VirtualBoxConfig  `yaml:"virtualbox"`
	IIS           IntegrationToggle `yaml:"iis"`
	MSSQL         IntegrationToggle `yaml:"mssql"`
	Exchange      IntegrationToggle `yaml:"exchange"`
	HyperV        IntegrationToggle `yaml:"hyperv"`
	Rspamd        URLIntegration    `yaml:"rspamd"`
	SystemdTimers IntegrationToggle `yaml:"systemd_timers"`
	Updates       IntegrationToggle `yaml:"updates"`

	// § 34 : connecteurs cloud, en lecture seule, actifs dès que leurs identifiants sont posés.
	OVHCloud OVHCloudConfig `yaml:"ovhcloud"`
	MySQL    MySQLConfig    `yaml:"mysql"`
	Redis    RedisConfig    `yaml:"redis"`
	Memcached MemcachedConfig `yaml:"memcached"`
	RabbitMQ  RabbitMQConfig  `yaml:"rabbitmq"`
	MongoDB   MongoDBConfig   `yaml:"mongodb"`
	OpenSearch OpenSearchConfig `yaml:"opensearch"`
	Kubernetes KubernetesConfig `yaml:"kubernetes"`
	Libvirt    LibvirtConfig    `yaml:"libvirt"`
	Proxmox    ProxmoxConfig    `yaml:"proxmox"`
	AWS        AWSConfig        `yaml:"aws"`
	Scaleway   ScalewayConfig   `yaml:"scaleway"`
	Azure      AzureConfig      `yaml:"azure"`
	GCP        GCPConfig        `yaml:"gcp"`
	VSphere  VSphereConfig  `yaml:"vsphere"`

	// Extra : entrées « instances » décodées (§ 50), remplies par normalize.
	Extra InstanceLists `yaml:"-"`
}

// PostgresConfig : serveur PostgreSQL local. Sans dsn, connexion par la socket Unix en
// tant qu'utilisateur système user (authentification peer, sans mot de passe).
type PostgresConfig struct {
	Enabled   Tristate `yaml:"enabled"` // auto = si la socket <socket_dir>/.s.PGSQL.<port> existe
	DSN       string   `yaml:"dsn"`
	SocketDir string   `yaml:"socket_dir"`
	Port      int      `yaml:"port"`
	User      string   `yaml:"user"`
	// DBM : requêtes les plus coûteuses (pg_stat_statements) ; auto = si l'extension existe.
	DBM Tristate `yaml:"dbm"`
}

// HAProxyConfig : statistiques HAProxy par la socket d'administration, ou par l'URL CSV.
type HAProxyConfig struct {
	Enabled Tristate `yaml:"enabled"` // auto = si la socket existe (ou si url est renseignée)
	Socket  string   `yaml:"socket"`
	URL     string   `yaml:"url"`
}

// NginxConfig : page stub_status de nginx.
type NginxConfig struct {
	Enabled bool   `yaml:"enabled"`
	URL     string `yaml:"url"`
}

// ListenerConfig : écoute locale (StatsD, OTLP).
type ListenerConfig struct {
	Enabled bool   `yaml:"enabled"`
	Listen  string `yaml:"listen"`
}

// Duration accepte "15s", "1m30s" ou un nombre de secondes.
type Duration time.Duration

// D rend la durée Go.
func (d Duration) D() time.Duration { return time.Duration(d) }

// UnmarshalYAML lit une durée écrite en texte ou en secondes.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	s := strings.TrimSpace(n.Value)
	if s == "" {
		*d = 0
		return nil
	}
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		*d = Duration(v * float64(time.Second))
		return nil
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("ligne %d : durée invalide %q (exemples : 15s, 1m)", n.Line, s)
	}
	*d = Duration(v)
	return nil
}

// MarshalYAML écrit la durée en texte (affichage par check-config).
func (d Duration) MarshalYAML() (any, error) { return time.Duration(d).String(), nil }

// Tristate vaut "auto", "true" ou "false" (containers.enabled).
type Tristate string

// UnmarshalYAML accepte auto, true/false, yes/no, on/off.
func (t *Tristate) UnmarshalYAML(n *yaml.Node) error {
	switch strings.ToLower(strings.TrimSpace(n.Value)) {
	case "", "auto":
		*t = "auto"
	case "true", "yes", "on", "1":
		*t = "true"
	case "false", "no", "off", "0":
		*t = "false"
	default:
		return fmt.Errorf("ligne %d : valeur %q inattendue (auto, true ou false)", n.Line, n.Value)
	}
	return nil
}

// DefaultExcludeInterfaces : interfaces virtuelles ignorées (contrat § 4), plus la
// boucle locale de Windows.
var DefaultExcludeInterfaces = []string{
	"lo", "docker*", "veth*", "br-*", "virbr*", "cali*", "flannel*", "cni*",
	"Loopback Pseudo-Interface*",
}

// Default rend la configuration par défaut : sûre (rien n'écoute hors de 127.0.0.1,
// TLS vérifié) et utile sans réglage.
func Default() *Config {
	c := defaultsV03()
	defaultsV04(c)
	return c
}

func defaultsV03() *Config {
	return &Config{
		URL:      "https://hypha.s33d.fr",
		Interval: Duration(15 * time.Second),
		Buffer:   BufferConfig{Dir: filepath.Join(StateDir(), "buffer"), MaxMB: 256},
		Logs: LogsConfig{
			Enabled:         true,
			Journald:        true,
			WindowsEventlog: []string{"System", "Application"},
		},
		Processes:  ProcessesConfig{Enabled: true},
		Network:    NetworkConfig{Enabled: true, ExcludeInterfaces: DefaultExcludeInterfaces},
		Containers: ContainersConfig{Enabled: "auto"},
		StatsD:     ListenerConfig{Enabled: true, Listen: "127.0.0.1:8125"},
		OTLP:       ListenerConfig{Enabled: false, Listen: "127.0.0.1:4318"},
		Checks:     ChecksConfig{WindowsAutoServices: "auto"},
		Integrations: IntegrationsConfig{
			Postgres: PostgresConfig{Enabled: "auto", SocketDir: "/var/run/postgresql", Port: 5432, User: "postgres"},
			HAProxy:  HAProxyConfig{Enabled: "auto", Socket: "/run/haproxy/admin.sock"},
			Nginx:    NginxConfig{Enabled: false, URL: "http://127.0.0.1/nginx_status"},
		},
		Tags: map[string]string{},
	}
}

// Load lit le fichier principal puis les fichiers conf.d/*.yaml de son dossier (ordre
// alphabétique), les fusionne (merge.go), applique les valeurs par défaut et l'environnement.
// Un fichier principal absent n'est pas une erreur si HYPHA_API_KEY est fournie (conteneur,
// essai). Un fichier illisible ou invalide arrête tout : jamais de conf à moitié lue.
func Load(path string) (*Config, error) {
	c := Default()
	c.Path = path
	var docs []fileDoc
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		d, err := parseDoc(path, data, &c.Warnings)
		if err != nil {
			return nil, err
		}
		docs = append(docs, d)
	case errors.Is(err, os.ErrNotExist) && os.Getenv("HYPHA_API_KEY") != "":
		c.Warnings = append(c.Warnings, fmt.Sprintf("%s absent : configuration par défaut et variables d'environnement", path))
	default:
		return nil, err
	}
	extra, err := readConfD(filepath.Join(filepath.Dir(path), "conf.d"), &c.Warnings)
	if err != nil {
		return nil, err
	}
	docs = append(docs, extra...)
	if err := c.applyDocs(docs); err != nil {
		return nil, err
	}
	c.applyEnv()
	c.normalize()
	return c, nil
}

// decode lit le YAML ; une clé inconnue (faute de frappe, conf plus récente que l'agent)
// devient un avertissement plutôt qu'un refus de démarrer.
// Un fichier vide (io.EOF) vaut une conf sans clé.
func decode(data []byte, c *Config) error {
	strict := *c
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	err := dec.Decode(&strict)
	if err == nil || errors.Is(err, io.EOF) {
		*c = strict
		return nil
	}
	if !strings.Contains(err.Error(), "not found in type") {
		return err
	}
	c.Warnings = append(c.Warnings, "clé inconnue ignorée : "+strings.TrimPrefix(err.Error(), "yaml: "))
	dec = yaml.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(c); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func (c *Config) applyEnv() {
	if v := strings.TrimSpace(os.Getenv("HYPHA_API_KEY")); v != "" {
		c.APIKey = v
		c.FromEnv = append(c.FromEnv, "api_key (HYPHA_API_KEY)")
	}
	if v := strings.TrimSpace(os.Getenv("HYPHA_URL")); v != "" {
		c.URL = v
		c.FromEnv = append(c.FromEnv, "url (HYPHA_URL)")
	}
}

func (c *Config) normalize() {
	c.URL = strings.TrimRight(strings.TrimSpace(c.URL), "/")
	c.APIKey = strings.TrimSpace(c.APIKey)
	c.Tags = map[string]string{}
	for k, v := range c.RawTags {
		if v == nil {
			c.Tags[k] = ""
			continue
		}
		c.Tags[k] = fmt.Sprint(v)
	}
	if c.Buffer.Dir == "" {
		c.Buffer.Dir = filepath.Join(StateDir(), "buffer")
	}
	for i := range c.Checks.HTTP {
		if c.Checks.HTTP[i].ExpectStatus == 0 {
			c.Checks.HTTP[i].ExpectStatus = 200
		}
		if c.Checks.HTTP[i].Timeout == 0 {
			c.Checks.HTTP[i].Timeout = Duration(5 * time.Second)
		}
	}
	for i := range c.Checks.TCP {
		if c.Checks.TCP[i].Timeout == 0 {
			c.Checks.TCP[i].Timeout = Duration(3 * time.Second)
		}
	}
	c.normalizeV04()
	c.normalizeInstances()
}

var (
	tagKeyRe = regexp.MustCompile(`^[a-z0-9_.\-/]+$`)
	apiKeyRe = regexp.MustCompile(`^hph_[0-9A-Za-z]{40}$`)
)

// Validate rend les erreurs bloquantes ; les remarques vont dans c.Warnings.
// requireKey est faux en --dry-run : rien n'est envoyé, la clé n'est pas nécessaire.
func (c *Config) Validate(requireKey bool) []string {
	var errs []string
	add := func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) }
	warn := func(f string, a ...any) { c.Warnings = append(c.Warnings, fmt.Sprintf(f, a...)) }

	switch {
	case c.APIKey == "" && requireKey:
		add("api_key absente (dans le fichier ou par la variable HYPHA_API_KEY)")
	case c.APIKey != "" && !apiKeyRe.MatchString(c.APIKey):
		warn("api_key n'a pas la forme attendue (hph_ suivi de 40 caractères)")
	}

	if u, err := url.Parse(c.URL); err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		add("url invalide : %q (exemple : https://hypha.s33d.fr)", c.URL)
	} else if u.Scheme == "http" {
		warn("url en http : la clé d'API circule en clair")
	}
	if c.Proxy != "" {
		if u, err := url.Parse(c.Proxy); err != nil || u.Host == "" {
			add("proxy invalide : %q (exemple : http://proxy:3128)", c.Proxy)
		}
	}
	if c.TLSSkipVerify {
		warn("tls_skip_verify actif : le certificat du serveur n'est pas vérifié")
	}
	if c.Interval.D() < time.Second {
		add("interval trop court : %s (1s au moins, 15s conseillé)", c.Interval.D())
	}
	for k, v := range c.Tags {
		if !tagKeyRe.MatchString(k) {
			add("étiquette %q : clé hors de [a-z0-9_.-/]", k)
		}
		if len(v) > 200 {
			add("étiquette %q : valeur de plus de 200 caractères", k)
		}
	}
	if c.Buffer.MaxMB < 1 {
		add("buffer.max_mb doit valoir au moins 1")
	}
	for _, p := range c.Logs.ExcludePatterns {
		if _, err := regexp.Compile(p); err != nil {
			add("logs.exclude_patterns : %q n'est pas une expression régulière valide : %v", p, err)
		}
	}
	for i, f := range c.Logs.Files {
		if f.Path == "" {
			add("logs.files[%d] : path manquant", i)
		} else if _, err := filepath.Match(f.Path, ""); err != nil {
			add("logs.files[%d] : motif %q invalide", i, f.Path)
		}
	}
	for _, p := range c.Processes.Include {
		if _, err := regexp.Compile(p); err != nil {
			add("processes.include : %q n'est pas une expression régulière valide : %v", p, err)
		}
	}
	for _, p := range c.Checks.WindowsServicesExclude {
		if _, err := regexp.Compile("(?i)" + p); err != nil {
			add("checks.windows_services_exclude : %q n'est pas une expression régulière valide : %v", p, err)
		}
	}
	switch c.Checks.WindowsAutoServices {
	case "", "auto", "always", "never":
	default:
		add("checks.windows_auto_services : %q (auto, always ou never)", c.Checks.WindowsAutoServices)
	}
	pg := c.Integrations.Postgres
	if pg.DSN != "" && !strings.HasPrefix(pg.DSN, "postgres://") && !strings.HasPrefix(pg.DSN, "postgresql://") && !strings.Contains(pg.DSN, "=") {
		add("integrations.postgres.dsn : attendu postgres://… ou « host=… user=… »")
	}
	if pg.DSN == "" && (pg.Port < 1 || pg.Port > 65535) {
		add("integrations.postgres.port invalide : %d", pg.Port)
	}
	if u := c.Integrations.HAProxy.URL; u != "" {
		if pu, err := url.Parse(u); err != nil || pu.Host == "" {
			add("integrations.haproxy.url invalide : %q", u)
		}
	}
	if n := c.Integrations.Nginx; n.Enabled {
		if pu, err := url.Parse(n.URL); err != nil || pu.Host == "" {
			add("integrations.nginx.url invalide : %q", n.URL)
		}
	}
	for _, p := range c.Network.ExcludeInterfaces {
		if _, err := filepath.Match(p, ""); err != nil {
			add("network.exclude_interfaces : motif %q invalide", p)
		}
	}
	for i, h := range c.Checks.HTTP {
		if h.Name == "" {
			add("checks.http[%d] : name manquant", i)
		}
		if u, err := url.Parse(h.URL); err != nil || u.Host == "" {
			add("checks.http[%d] (%s) : url invalide %q", i, h.Name, h.URL)
		}
		if h.ExpectStatus < 100 || h.ExpectStatus > 599 {
			add("checks.http[%d] (%s) : expect_status hors de 100-599", i, h.Name)
		}
		if h.Timeout.D() > c.Interval.D() {
			warn("checks.http[%d] (%s) : timeout supérieur à interval", i, h.Name)
		}
	}
	for i, t := range c.Checks.TCP {
		if t.Name == "" {
			add("checks.tcp[%d] : name manquant", i)
		}
		if _, _, err := net.SplitHostPort(t.Address); err != nil {
			add("checks.tcp[%d] (%s) : address %q attendue sous la forme hôte:port", i, t.Name, t.Address)
		}
	}
	for name, l := range map[string]ListenerConfig{"statsd": c.StatsD, "otlp": c.OTLP} {
		if !l.Enabled {
			continue
		}
		host, _, err := net.SplitHostPort(l.Listen)
		if err != nil {
			add("%s.listen %q attendu sous la forme adresse:port", name, l.Listen)
			continue
		}
		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			warn("%s.listen %q : écoute hors de la boucle locale, ouverte au réseau", name, l.Listen)
		}
	}
	c.validateV04(add, warn)
	c.validateInstances(add, warn)
	// Avec plusieurs fichiers, chaque erreur nomme celui qui a posé la clé fautive.
	if len(c.Files) > 1 {
		for i, e := range errs {
			if f := c.originOf(e); f != "" {
				errs[i] = e + " (fichier " + f + ")"
			}
		}
	}
	return errs
}

// MaskedKey rend la clé tronquée, pour affichage.
func (c *Config) MaskedKey() string {
	if len(c.APIKey) <= 8 {
		if c.APIKey == "" {
			return "(absente)"
		}
		return "****"
	}
	return c.APIKey[:8] + "…"
}
