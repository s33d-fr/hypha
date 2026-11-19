package config

import (
	"fmt"
	"net"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Blocs de la 0.4.0 (docs/CONTRAT-04.md §§ 14, 15, 27, 28, 29, 31).

// ControlConfig : canal de contrôle (§ 27), interrogé par l'agent.
type ControlConfig struct {
	Enabled  bool     `yaml:"enabled"`
	Interval Duration `yaml:"interval"`
}

// ActionsConfig : exécuteur d'actions à distance (§ 28), coupé par défaut.
type ActionsConfig struct {
	Enabled      bool              `yaml:"enabled"`
	Allow        []string          `yaml:"allow"`
	Services     []string          `yaml:"services"`
	DenyServices []string          `yaml:"deny_services"`
	Scripts      map[string]string `yaml:"scripts"`
	Timeout      Duration          `yaml:"timeout"`
}

// Gestes que l'agent sait faire (§ 28).
var ActionKinds = []string{"restart_service", "start_service", "stop_service", "run_script", "update_agent"}

// DefaultDenyServices : unités jamais touchées par une action, même si services les admet.
var DefaultDenyServices = []string{"sshd*", "ssh*", "hypha-agent*", "systemd-*"}

// SyntheticsConfig : emplacement privé de tests synthétiques (§ 29).
type SyntheticsConfig struct {
	Enabled       bool `yaml:"enabled"`
	MaxConcurrent int  `yaml:"max_concurrent"`
}

// InventoryConfig : inventaire matériel et logiciel (§ 15).
type InventoryConfig struct {
	Enabled  bool     `yaml:"enabled"`
	Interval Duration `yaml:"interval"`
	Packages bool     `yaml:"packages"`
}

// SNMPConfig : équipements réseau interrogés par l'agent (§ 14).
type SNMPConfig struct {
	Enabled  bool         `yaml:"enabled"`
	Interval Duration     `yaml:"interval"`
	Devices  []SNMPDevice `yaml:"devices"`
	Traps    SNMPTraps    `yaml:"traps"`
}

// SNMPDevice : un équipement. Les secrets (community, clés v3) restent dans la conf de
// l'agent : rien n'en part vers le serveur.
type SNMPDevice struct {
	Name              string         `yaml:"name"`
	Address           string         `yaml:"address"`
	Port              int            `yaml:"port"`
	Version           SNMPVersion    `yaml:"version"`
	Community         string         `yaml:"community"`
	User              string         `yaml:"user"`
	AuthProtocol      string         `yaml:"auth_protocol"`
	AuthKey           string         `yaml:"auth_key"`
	PrivProtocol      string         `yaml:"priv_protocol"`
	PrivKey           string         `yaml:"priv_key"`
	Level             string         `yaml:"level"`
	Context           string         `yaml:"context"`
	Profile           string         `yaml:"profile"`
	RawTags           map[string]any `yaml:"tags"`
	Timeout           Duration       `yaml:"timeout"`
	Retries           int            `yaml:"retries"`
	InterfacesExclude []string       `yaml:"interfaces_exclude"`
	// InterfaceDown : « seen » (défaut) ne met en CRIT une interface active mais coupée que si
	// elle a déjà été vue en service (un port libre d'un commutateur reste OK) ; « always »
	// applique la règle à toutes les interfaces actives.
	InterfaceDown string `yaml:"interface_down"`

	Tags map[string]string `yaml:"-"`
}

// SNMPTraps : réception des traps (UDP), coupée par défaut.
type SNMPTraps struct {
	Enabled   bool   `yaml:"enabled"`
	Listen    string `yaml:"listen"`
	Community string `yaml:"community"`
}

// SNMPVersion vaut "1", "2c" ou "3" ; accepte v1, v2c, 2, v3 et les nombres YAML.
type SNMPVersion string

// UnmarshalYAML lit la version écrite en texte ou en nombre.
func (v *SNMPVersion) UnmarshalYAML(n *yaml.Node) error {
	s := strings.ToLower(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(n.Value)), "v"))
	switch s {
	case "":
		*v = ""
	case "1":
		*v = "1"
	case "2", "2c":
		*v = "2c"
	case "3":
		*v = "3"
	default:
		return fmt.Errorf("ligne %d : version SNMP %q inattendue (1, 2c ou 3)", n.Line, n.Value)
	}
	return nil
}

// ProcessCheck : contrôle de présence d'un processus (checks.processes, § 31).
type ProcessCheck struct {
	Name  string `yaml:"name"`
	Match string `yaml:"match"` // exact (défaut), contains, regex
	Min   *int   `yaml:"min"`
	Max   *int   `yaml:"max"`
}

// IntegrationToggle : intégration sans autre réglage que son activation.
type IntegrationToggle struct {
	Enabled Tristate `yaml:"enabled"`
}

// OVHCloudConfig : connecteur de l'API OVHcloud (§ 34), en lecture seule. Actif (auto) dès que
// les trois identifiants sont posés ; une clé « consumer » restreinte aux GET suffit.
type OVHCloudConfig struct {
	Enabled           Tristate `yaml:"enabled"`
	Endpoint          string   `yaml:"endpoint"`
	ApplicationKey    string   `yaml:"application_key"`
	ApplicationSecret string   `yaml:"application_secret"`
	ConsumerKey       string   `yaml:"consumer_key"`
	Interval          Duration `yaml:"interval"`

	Multi `yaml:",inline"` // name et instances (§ 50)
}

// Configured : les trois identifiants sont présents.
func (o OVHCloudConfig) Configured() bool {
	return o.ApplicationKey != "" && o.ApplicationSecret != "" && o.ConsumerKey != ""
}

// VSphereConfig : ESXi et vCenter interrogés par l'API vSphere (§ 35), en lecture seule. Actif
// (auto) dès qu'une cible est déclarée. Un rôle « ReadOnly » suffit au compte.
type VSphereConfig struct {
	Enabled  Tristate        `yaml:"enabled"`
	Interval Duration        `yaml:"interval"`
	Targets  []VSphereTarget `yaml:"targets"`
}

// VSphereTarget : un ESXi ou un vCenter. Certificat autosigné : thumbprint (SHA-1 du certificat,
// « AA:BB:… ») plutôt qu'insecure, qui accepte n'importe quel certificat.
type VSphereTarget struct {
	Name       string `yaml:"name"`
	URL        string `yaml:"url"`
	Username   string `yaml:"username"`
	Password   string `yaml:"password"`
	Thumbprint string            `yaml:"thumbprint"`
	Insecure   bool              `yaml:"insecure"`
	Tags       map[string]string `yaml:"tags"`
}

// MySQLConfig : MySQL ou MariaDB (§ 36). Sans dsn : socket locale, compte root authentifié par
// la socket (paquets Debian et Ubuntu). Sinon dsn au format du pilote Go, compte de supervision :
// « hypha:motdepasse@tcp(127.0.0.1:3306)/ » (droits PROCESS, REPLICATION CLIENT).
type MySQLConfig struct {
	Enabled Tristate `yaml:"enabled"` // auto = si la socket existe (ou si dsn est renseigné)
	DSN     string   `yaml:"dsn"`
	Socket  string   `yaml:"socket"`

	Multi `yaml:",inline"` // name et instances (§ 50)
}

// RedisConfig : Redis ou Valkey (§ 37), commande INFO. address « hôte:port » ou chemin d'une
// socket Unix ; password (et username, ACL de Redis 6+) si le serveur en demande un.
type RedisConfig struct {
	Enabled  Tristate `yaml:"enabled"` // auto = si 127.0.0.1:6379 répond
	Address  string   `yaml:"address"`
	Username string   `yaml:"username"`
	Password string   `yaml:"password"`

	Multi `yaml:",inline"` // name et instances (§ 50)
}

// MemcachedConfig : Memcached (§ 38), commande stats.
type MemcachedConfig struct {
	Enabled Tristate `yaml:"enabled"` // auto = si 127.0.0.1:11211 répond
	Address string   `yaml:"address"`

	Multi `yaml:",inline"` // name et instances (§ 50)
}

// RabbitMQConfig : RabbitMQ (§ 39) par son API de gestion. Compte au rôle « monitoring » ;
// sans compte, guest/guest (accepté par RabbitMQ depuis la machine elle-même seulement).
type RabbitMQConfig struct {
	Enabled  Tristate `yaml:"enabled"` // auto = si l'API de gestion répond
	URL      string   `yaml:"url"`
	Username string   `yaml:"username"`
	Password string   `yaml:"password"`

	Multi `yaml:",inline"` // name et instances (§ 50)
}

// MongoDBConfig : MongoDB (§ 40), URI de connexion (compte au rôle clusterMonitor si
// l'authentification est active).
type MongoDBConfig struct {
	Enabled Tristate `yaml:"enabled"` // auto = si 127.0.0.1:27017 répond
	URI     string   `yaml:"uri"`

	Multi `yaml:",inline"` // name et instances (§ 50)
}

// OpenSearchConfig : OpenSearch ou Elasticsearch (§ 41). Sans url : l'adresse sur laquelle le
// port 9200 écoute sur cette machine (lue dans /proc/net/tcp), sinon 127.0.0.1.
type OpenSearchConfig struct {
	Enabled  Tristate `yaml:"enabled"` // auto = si le port 9200 écoute sur cette machine
	URL      string   `yaml:"url"`
	Username string   `yaml:"username"`
	Password string   `yaml:"password"`
	Insecure bool     `yaml:"insecure"` // certificat autosigné en https

	Multi `yaml:",inline"` // name et instances (§ 50)
}

// KubernetesConfig : grappe Kubernetes lue par son API (§ 42), avec un compte de service en
// lecture seule (get, list sur nœuds, pods, événements, déploiements, statefulsets, daemonsets,
// replicasets). Un seul agent par grappe la lit. Dans un pod, le compte monté sert sans conf.
type KubernetesConfig struct {
	Enabled   Tristate `yaml:"enabled"` // auto = dans un pod (compte de service monté) ou si url est fixée
	Cluster   string   `yaml:"cluster"` // étiquette kube_cluster, « kubernetes » par défaut
	URL       string   `yaml:"url"`     // https://hôte:6443 ; dans un pod, KUBERNETES_SERVICE_HOST
	Token     string   `yaml:"token"`
	TokenFile string   `yaml:"token_file"` // relu à chaque passage (jeton renouvelé)
	CAFile    string   `yaml:"ca_file"`
	Insecure  bool     `yaml:"insecure"`
	Interval  Duration `yaml:"interval"`

	Multi `yaml:",inline"` // name et instances (§ 50)
}

// LibvirtConfig : KVM et libvirt (§ 43), lus par virsh sur l'hôte.
type LibvirtConfig struct {
	Enabled Tristate `yaml:"enabled"` // auto = si virsh répond
	URI     string   `yaml:"uri"`     // qemu:///system par défaut

	Multi `yaml:",inline"` // name et instances (§ 50)
}

// ProxmoxConfig : Proxmox VE (§ 44). Sur un nœud Proxmox, pvesh en root, sans compte ; à
// distance, l'API avec un jeton d'API au rôle PVEAuditor (token_id « utilisateur@royaume!nom »).
type ProxmoxConfig struct {
	Enabled     Tristate `yaml:"enabled"` // auto = si pvesh existe (nœud Proxmox) ou si url est fixée
	URL         string   `yaml:"url"`     // https://hôte:8006
	TokenID     string   `yaml:"token_id"`
	TokenSecret string   `yaml:"token_secret"`
	CAFile      string   `yaml:"ca_file"`
	Insecure    bool     `yaml:"insecure"`
	Interval    Duration `yaml:"interval"`

	Multi `yaml:",inline"` // name et instances (§ 50)
}

// AWSConfig : connecteur AWS (§ 46), en lecture seule (politique IAM de lecture : EC2, EBS,
// alarmes CloudWatch, identité). Actif (auto) dès que les clés et une région sont données.
// endpoint : une API compatible (essais contre LocalStack), sinon les points d'entrée d'AWS.
// metrics : CPU des instances par GetMetricData, facturé par AWS au compte lu.
type AWSConfig struct {
	Enabled         Tristate          `yaml:"enabled"`
	AccessKeyID     string            `yaml:"access_key_id"`
	SecretAccessKey string            `yaml:"secret_access_key"`
	SessionToken    string            `yaml:"session_token"`
	Regions         []string          `yaml:"regions"`
	Endpoint        string            `yaml:"endpoint"`
	Metrics         bool              `yaml:"metrics"`
	Interval        Duration          `yaml:"interval"`
	Tags            map[string]string `yaml:"tags"`

	Multi `yaml:",inline"` // name et instances (§ 50)
}

// Configured : clés et au moins une région.
func (a AWSConfig) Configured() bool {
	return a.AccessKeyID != "" && a.SecretAccessKey != "" && len(a.Regions) > 0
}

// ScalewayConfig : connecteur Scaleway (§ 47), clé d'API d'un membre IAM en lecture seule
// (InstancesReadOnly, ElasticMetalReadOnly). zones : fr-par-1, nl-ams-1, pl-waw-1…
type ScalewayConfig struct {
	Enabled   Tristate          `yaml:"enabled"`
	SecretKey string            `yaml:"secret_key"`
	Zones     []string          `yaml:"zones"`
	Endpoint  string            `yaml:"endpoint"`
	Interval  Duration          `yaml:"interval"`
	Tags      map[string]string `yaml:"tags"`

	Multi `yaml:",inline"` // name et instances (§ 50)
}

// AzureConfig : connecteur Azure (§ 48), principal de service au rôle « Lecteur » des abonnements.
type AzureConfig struct {
	Enabled       Tristate          `yaml:"enabled"`
	TenantID      string            `yaml:"tenant_id"`
	ClientID      string            `yaml:"client_id"`
	ClientSecret  string            `yaml:"client_secret"`
	Subscriptions []string          `yaml:"subscriptions"`
	Endpoint      string            `yaml:"endpoint"`       // Azure Resource Manager (management.azure.com)
	LoginEndpoint string            `yaml:"login_endpoint"` // Entra ID (login.microsoftonline.com)
	Interval      Duration          `yaml:"interval"`
	Tags          map[string]string `yaml:"tags"`

	Multi `yaml:",inline"` // name et instances (§ 50)
}

// GCPConfig : connecteur Google Cloud (§ 49), compte de service au rôle roles/compute.viewer, clé
// JSON dans credentials_file (fichier 600, hors de la conf).
type GCPConfig struct {
	Enabled         Tristate          `yaml:"enabled"`
	CredentialsFile string            `yaml:"credentials_file"`
	Projects        []string          `yaml:"projects"`
	Endpoint        string            `yaml:"endpoint"`
	TokenEndpoint   string            `yaml:"token_endpoint"`
	Interval        Duration          `yaml:"interval"`
	Tags            map[string]string `yaml:"tags"`

	Multi `yaml:",inline"` // name et instances (§ 50)
}

// URLIntegration : intégration lue sur une page HTTP locale.
type URLIntegration struct {
	Enabled Tristate `yaml:"enabled"`
	URL     string   `yaml:"url"`
}

// OpenVPNConfig : fichiers de statut d'OpenVPN (status-version 1, 2 ou 3).
type OpenVPNConfig struct {
	Enabled     Tristate `yaml:"enabled"`
	StatusFiles []string `yaml:"status_files"`
}

// X509Config : certificats surveillés (motifs glob), plus ceux que déclarent les configurations
// d'Apache, nginx, Postfix, Dovecot et HAProxy (discover) et, sous Windows, le magasin de
// l'ordinateur (stores, « My » par défaut).
type X509Config struct {
	Enabled  Tristate `yaml:"enabled"`
	Paths    []string `yaml:"paths"`
	Discover *bool    `yaml:"discover"`
	Stores   []string `yaml:"stores"`
}

// DiscoverOn : découverte active sauf « discover: false ».
func (x X509Config) DiscoverOn() bool { return x.Discover == nil || *x.Discover }

// VirtualBoxConfig : VM déclarées (VirtualBox.xml des comptes) et VM en marche (processus).
type VirtualBoxConfig struct {
	Enabled Tristate `yaml:"enabled"`
	// User : seul compte dont les VM sont suivies ; vide = tous les comptes de l'hôte.
	User string `yaml:"user"`
}

// DefaultX509Paths : certificats lus par défaut (§ 31).
var DefaultX509Paths = []string{"/etc/letsencrypt/live/*/cert.pem", "/etc/ssl/*/fullchain.pem"}

// DefaultOpenVPNStatus : emplacements habituels des fichiers de statut.
var DefaultOpenVPNStatus = []string{
	"/run/openvpn-server/status-*.log", "/run/openvpn/*.status", "/var/log/openvpn/*status*.log",
	"/var/log/openvpn-status.log", "/etc/openvpn/*status*.log", "/etc/openvpn/server/*status*.log",
}

func defaultsV04(c *Config) {
	c.AutoUpdate = false
	c.Control = ControlConfig{Enabled: true, Interval: Duration(30 * time.Second)}
	c.Actions = ActionsConfig{
		Enabled:      false,
		Allow:        []string{"restart_service", "start_service", "stop_service"},
		Services:     []string{"*"},
		DenyServices: append([]string{}, DefaultDenyServices...),
		Scripts:      map[string]string{},
		Timeout:      Duration(120 * time.Second),
	}
	c.Synthetics = SyntheticsConfig{Enabled: false, MaxConcurrent: 4}
	c.Inventory = InventoryConfig{Enabled: true, Interval: Duration(6 * time.Hour), Packages: true}
	c.SNMP = SNMPConfig{
		Enabled: true, Interval: Duration(60 * time.Second),
		Traps: SNMPTraps{Enabled: false, Listen: "0.0.0.0:162", Community: "public"},
	}
	i := &c.Integrations
	i.Postgres.DBM = "auto"
	i.Apache = URLIntegration{Enabled: "auto", URL: "http://127.0.0.1/server-status?auto"}
	i.Bind = URLIntegration{Enabled: "auto", URL: "http://127.0.0.1:8053/json"}
	i.Postfix = IntegrationToggle{Enabled: "auto"}
	i.Dovecot = IntegrationToggle{Enabled: "auto"}
	i.OpenVPN = OpenVPNConfig{Enabled: "auto", StatusFiles: append([]string{}, DefaultOpenVPNStatus...)}
	i.Fail2ban = IntegrationToggle{Enabled: "auto"}
	i.X509 = X509Config{Enabled: "auto", Paths: append([]string{}, DefaultX509Paths...), Stores: []string{"My"}}
	i.SMART = IntegrationToggle{Enabled: "auto"}
	i.Hwmon = IntegrationToggle{Enabled: "auto"}
	i.NTP = IntegrationToggle{Enabled: "auto"}
	i.VirtualBox = VirtualBoxConfig{Enabled: "auto"}
	i.IIS = IntegrationToggle{Enabled: "auto"}
	i.MSSQL = IntegrationToggle{Enabled: "auto"}
	i.Exchange = IntegrationToggle{Enabled: "auto"}
	i.HyperV = IntegrationToggle{Enabled: "auto"}
	i.Rspamd = URLIntegration{Enabled: "auto", URL: "http://127.0.0.1:11334/stat"}
	i.SystemdTimers = IntegrationToggle{Enabled: "auto"}
	i.Updates = IntegrationToggle{Enabled: "auto"}
	i.OVHCloud = OVHCloudConfig{Enabled: "auto", Endpoint: "https://eu.api.ovh.com/1.0", Interval: Duration(15 * time.Minute)}
	i.VSphere = VSphereConfig{Enabled: "auto", Interval: Duration(60 * time.Second)}
	i.MySQL = MySQLConfig{Enabled: "auto", Socket: "/run/mysqld/mysqld.sock"}
	i.Redis = RedisConfig{Enabled: "auto", Address: "127.0.0.1:6379"}
	i.Memcached = MemcachedConfig{Enabled: "auto", Address: "127.0.0.1:11211"}
	i.RabbitMQ = RabbitMQConfig{Enabled: "auto", URL: "http://127.0.0.1:15672"}
	i.MongoDB = MongoDBConfig{Enabled: "auto", URI: "mongodb://127.0.0.1:27017"}
	i.OpenSearch = OpenSearchConfig{Enabled: "auto"}
	i.Kubernetes = KubernetesConfig{Enabled: "auto", Cluster: "kubernetes", Interval: Duration(60 * time.Second)}
	i.Libvirt = LibvirtConfig{Enabled: "auto", URI: "qemu:///system"}
	i.Proxmox = ProxmoxConfig{Enabled: "auto", Interval: Duration(60 * time.Second)}
	i.AWS = AWSConfig{Enabled: "auto", Interval: Duration(5 * time.Minute)}
	i.Scaleway = ScalewayConfig{Enabled: "auto", Interval: Duration(5 * time.Minute)}
	i.Azure = AzureConfig{Enabled: "auto", Interval: Duration(5 * time.Minute)}
	i.GCP = GCPConfig{Enabled: "auto", Interval: Duration(5 * time.Minute)}
}

func (c *Config) normalizeV04() {
	for i := range c.SNMP.Devices {
		d := &c.SNMP.Devices[i]
		d.Name = strings.TrimSpace(d.Name)
		d.Address = strings.TrimSpace(d.Address)
		if d.Version == "" {
			d.Version = "2c"
		}
		if d.Port == 0 {
			d.Port = 161
		}
		if d.Timeout == 0 {
			d.Timeout = Duration(5 * time.Second)
		}
		if d.Profile == "" {
			d.Profile = "auto"
		}
		d.Profile = strings.ToLower(d.Profile)
		d.AuthProtocol = strings.ToLower(strings.TrimSpace(d.AuthProtocol))
		d.PrivProtocol = strings.ToLower(strings.TrimSpace(d.PrivProtocol))
		if d.Version == "3" {
			if d.Level == "" {
				switch {
				case d.PrivKey != "":
					d.Level = "authPriv"
				case d.AuthKey != "":
					d.Level = "authNoPriv"
				default:
					d.Level = "noAuthNoPriv"
				}
			}
			if d.AuthProtocol == "" && d.AuthKey != "" {
				d.AuthProtocol = "sha"
			}
			if d.PrivProtocol == "" && d.PrivKey != "" {
				d.PrivProtocol = "aes"
			}
		}
		if d.InterfaceDown == "" {
			d.InterfaceDown = "seen"
		}
		d.Tags = map[string]string{}
		for k, v := range d.RawTags {
			if v == nil {
				d.Tags[k] = ""
				continue
			}
			d.Tags[k] = fmt.Sprint(v)
		}
	}
	if c.Actions.Scripts == nil {
		c.Actions.Scripts = map[string]string{}
	}
	for i := range c.Checks.Processes {
		p := &c.Checks.Processes[i]
		p.Match = strings.ToLower(strings.TrimSpace(p.Match))
		if p.Match == "" {
			p.Match = "exact"
		}
		if p.Min == nil && p.Max == nil {
			one := 1
			p.Min = &one
		}
	}
}

var scriptNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.\-]{0,63}$`)

// awsRegionRe : nom de région AWS (eu-west-3, us-gov-west-1, ap-southeast-2).
var awsRegionRe = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-[0-9]$`)

func (c *Config) validateV04(add, warn func(string, ...any)) {
	if c.Control.Interval.D() < 10*time.Second || c.Control.Interval.D() > 10*time.Minute {
		add("control.interval : %s hors de 10s à 10m (30s conseillé)", c.Control.Interval.D())
	}
	if c.AutoUpdate && !c.Control.Enabled {
		warn("auto_update actif mais control.enabled faux : la version cible n'est jamais lue")
	}

	a := c.Actions
	known := map[string]bool{}
	for _, k := range ActionKinds {
		known[k] = true
	}
	for _, k := range a.Allow {
		if !known[k] {
			add("actions.allow : geste %q inconnu (%s)", k, strings.Join(ActionKinds, ", "))
		}
	}
	for _, p := range append(append([]string{}, a.Services...), a.DenyServices...) {
		if _, err := path.Match(p, ""); err != nil {
			add("actions.services / deny_services : motif %q invalide", p)
		}
	}
	for _, name := range SortedScriptNames(a.Scripts) {
		p := a.Scripts[name]
		if !scriptNameRe.MatchString(name) {
			add("actions.scripts : nom %q invalide (a-z 0-9 _ . -, 64 caractères au plus)", name)
		}
		if !filepath.IsAbs(p) {
			add("actions.scripts.%s : chemin absolu attendu, pas %q", name, p)
		}
	}
	if a.Timeout.D() < time.Second || a.Timeout.D() > time.Hour {
		add("actions.timeout : %s hors de 1s à 1h", a.Timeout.D())
	}
	if a.Enabled && !c.Control.Enabled {
		warn("actions.enabled actif mais control.enabled faux : aucune action ne sera reçue")
	}

	if s := c.Synthetics; s.MaxConcurrent < 1 || s.MaxConcurrent > 16 {
		add("synthetics.max_concurrent : %d hors de 1 à 16", s.MaxConcurrent)
	}
	if c.Inventory.Interval.D() < 10*time.Minute {
		add("inventory.interval : %s trop court (10m au moins, 6h conseillé)", c.Inventory.Interval.D())
	}

	sn := c.SNMP
	if sn.Interval.D() < 10*time.Second {
		add("snmp.interval : %s trop court (10s au moins, 60s conseillé)", sn.Interval.D())
	}
	names := map[string]bool{}
	addrs := map[string]bool{}
	for i, d := range sn.Devices {
		at := fmt.Sprintf("snmp.devices[%d] (%s)", i, d.Name)
		switch {
		case d.Name == "":
			add("snmp.devices[%d] : name manquant", i)
		case names[d.Name]:
			add("%s : name en double", at)
		case len(d.Name) > 200:
			add("%s : name de plus de 200 caractères", at)
		}
		names[d.Name] = true
		switch {
		case d.Address == "":
			add("%s : address manquante", at)
		case addrs[d.Address]:
			add("%s : address %q en double (l'identité d'un équipement en dépend)", at, d.Address)
		case strings.ContainsAny(d.Address, " /"):
			add("%s : address %q invalide (nom ou IP, sans port)", at, d.Address)
		}
		addrs[d.Address] = true
		if d.Port < 1 || d.Port > 65535 {
			add("%s : port %d invalide", at, d.Port)
		}
		switch d.Version {
		case "1", "2c":
			if d.Community == "" {
				add("%s : community manquante (SNMP v%s)", at, d.Version)
			}
		case "3":
			validateV3(d, at, add)
		default:
			add("%s : version %q (1, 2c ou 3)", at, d.Version)
		}
		switch d.Profile {
		case "auto", "generic", "mikrotik", "synology", "printer", "ups":
		default:
			add("%s : profile %q (auto, generic, mikrotik, synology, printer, ups)", at, d.Profile)
		}
		switch d.InterfaceDown {
		case "seen", "always":
		default:
			add("%s : interface_down %q (seen ou always)", at, d.InterfaceDown)
		}
		for k, v := range d.Tags {
			if !tagKeyRe.MatchString(k) {
				add("%s : étiquette %q hors de [a-z0-9_.-/]", at, k)
			}
			if len(v) > 200 {
				add("%s : étiquette %q de plus de 200 caractères", at, k)
			}
		}
		if d.Timeout.D() < 500*time.Millisecond || d.Timeout.D() > time.Minute {
			add("%s : timeout %s hors de 500ms à 1m", at, d.Timeout.D())
		}
		if d.Retries < 0 || d.Retries > 5 {
			add("%s : retries %d hors de 0 à 5", at, d.Retries)
		}
		for _, p := range d.InterfacesExclude {
			if _, err := filepath.Match(p, ""); err != nil {
				add("%s : motif interfaces_exclude %q invalide", at, p)
			}
		}
	}
	if t := sn.Traps; t.Enabled {
		if _, _, err := net.SplitHostPort(t.Listen); err != nil {
			add("snmp.traps.listen %q attendu sous la forme adresse:port", t.Listen)
		}
		if t.Community == "" {
			warn("snmp.traps.community vide : tout trap v1/v2c est accepté")
		}
	}

	for i, p := range c.Checks.Processes {
		at := fmt.Sprintf("checks.processes[%d] (%s)", i, p.Name)
		if strings.TrimSpace(p.Name) == "" {
			add("checks.processes[%d] : name manquant", i)
		}
		switch p.Match {
		case "exact", "contains":
		case "regex":
			if _, err := regexp.Compile(p.Name); err != nil {
				add("%s : expression régulière invalide : %v", at, err)
			}
		default:
			add("%s : match %q (exact, contains ou regex)", at, p.Match)
		}
		if p.Min != nil && *p.Min < 0 {
			add("%s : min négatif", at)
		}
		if p.Min != nil && p.Max != nil && *p.Max < *p.Min {
			add("%s : max inférieur à min", at)
		}
	}

	in := c.Integrations
	for name, u := range map[string]URLIntegration{"apache": in.Apache, "bind": in.Bind, "rspamd": in.Rspamd} {
		if u.URL == "" && u.Enabled == "true" {
			add("integrations.%s.url manquante", name)
		}
		if u.URL != "" {
			if pu, err := url.Parse(u.URL); err != nil || pu.Host == "" {
				add("integrations.%s.url invalide : %q", name, u.URL)
			}
		}
	}
	if o := in.OVHCloud; o.Enabled != "false" {
		if o.Enabled == "true" && !o.Configured() {
			add("integrations.ovhcloud : application_key, application_secret et consumer_key sont nécessaires")
		}
		if pu, err := url.Parse(o.Endpoint); o.Configured() && (err != nil || pu.Scheme != "https" || pu.Host == "") {
			add("integrations.ovhcloud.endpoint invalide (https attendu) : %q", o.Endpoint)
		}
		if o.Interval.D() < 5*time.Minute {
			add("integrations.ovhcloud.interval : 5 min au moins (API d'un fournisseur)")
		}
	}
	if v := in.VSphere; v.Enabled != "false" {
		if v.Enabled == "true" && len(v.Targets) == 0 {
			add("integrations.vsphere : aucune cible (targets)")
		}
		if len(v.Targets) > 0 && v.Interval.D() < 20*time.Second {
			add("integrations.vsphere.interval : 20 s au moins")
		}
		names := map[string]bool{}
		for i, t := range v.Targets {
			at := fmt.Sprintf("integrations.vsphere.targets[%d]", i)
			if t.Name == "" || names[t.Name] {
				add("%s : name absent ou en double", at)
			}
			names[t.Name] = true
			if pu, err := url.Parse(t.URL); err != nil || pu.Scheme != "https" || pu.Host == "" {
				add("%s.url invalide (https://hôte/sdk attendu) : %q", at, t.URL)
			}
			if t.Username == "" || t.Password == "" {
				add("%s : username et password sont nécessaires", at)
			}
		}
	}
	for _, c := range []struct {
		name       string
		enabled    Tristate
		configured bool
		missing    string
		interval   time.Duration
		endpoints  []string
	}{
		{"scaleway", in.Scaleway.Enabled, in.Scaleway.SecretKey != "" && len(in.Scaleway.Zones) > 0, "secret_key et zones",
			in.Scaleway.Interval.D(), []string{in.Scaleway.Endpoint}},
		{"azure", in.Azure.Enabled, in.Azure.TenantID != "" && in.Azure.ClientID != "" && in.Azure.ClientSecret != "" && len(in.Azure.Subscriptions) > 0,
			"tenant_id, client_id, client_secret et subscriptions", in.Azure.Interval.D(), []string{in.Azure.Endpoint, in.Azure.LoginEndpoint}},
		{"gcp", in.GCP.Enabled, in.GCP.CredentialsFile != "" && len(in.GCP.Projects) > 0, "credentials_file et projects",
			in.GCP.Interval.D(), []string{in.GCP.Endpoint, in.GCP.TokenEndpoint}},
	} {
		if c.enabled == "false" {
			continue
		}
		if c.enabled == "true" && !c.configured {
			add("integrations.%s : %s sont nécessaires", c.name, c.missing)
		}
		if c.configured && c.interval < time.Minute {
			add("integrations.%s.interval : 1 min au moins (API d'un fournisseur)", c.name)
		}
		for _, e := range c.endpoints {
			if e == "" {
				continue
			}
			if pu, err := url.Parse(e); err != nil || (pu.Scheme != "https" && pu.Scheme != "http") || pu.Host == "" {
				add("integrations.%s : point d'entrée invalide %q", c.name, e)
			}
		}
	}
	if a := in.AWS; a.Enabled != "false" {
		if a.Enabled == "true" && !a.Configured() {
			add("integrations.aws : access_key_id, secret_access_key et regions sont nécessaires")
		}
		for _, r := range a.Regions {
			if !awsRegionRe.MatchString(r) {
				add("integrations.aws.regions : région invalide %q (ex. eu-west-3)", r)
			}
		}
		if a.Endpoint != "" {
			if pu, err := url.Parse(a.Endpoint); err != nil || (pu.Scheme != "https" && pu.Scheme != "http") || pu.Host == "" {
				add("integrations.aws.endpoint invalide : %q", a.Endpoint)
			}
		}
		if a.Configured() && a.Interval.D() < time.Minute {
			add("integrations.aws.interval : 1 min au moins (API d'un fournisseur)")
		}
	}
	if p := in.Proxmox; p.Enabled != "false" {
		if p.URL != "" {
			if pu, err := url.Parse(p.URL); err != nil || pu.Scheme != "https" || pu.Host == "" {
				add("integrations.proxmox.url invalide (https://hôte:8006 attendu) : %q", p.URL)
			}
			if !strings.Contains(p.TokenID, "!") || p.TokenSecret == "" {
				add("integrations.proxmox : token_id (utilisateur@royaume!nom) et token_secret sont nécessaires avec url")
			}
		}
		if p.Interval.D() < 20*time.Second {
			add("integrations.proxmox.interval : 20 s au moins")
		}
	}
	if k := in.Kubernetes; k.Enabled != "false" {
		if k.URL != "" {
			if pu, err := url.Parse(k.URL); err != nil || pu.Scheme != "https" || pu.Host == "" {
				add("integrations.kubernetes.url invalide (https://hôte:6443 attendu) : %q", k.URL)
			}
			if k.Token == "" && k.TokenFile == "" {
				add("integrations.kubernetes : token ou token_file est nécessaire avec url")
			}
		}
		if k.Interval.D() < 20*time.Second {
			add("integrations.kubernetes.interval : 20 s au moins")
		}
	}
	for _, p := range in.X509.Paths {
		if _, err := filepath.Match(p, ""); err != nil {
			add("integrations.x509.paths : motif %q invalide", p)
		}
	}
	for _, p := range in.OpenVPN.StatusFiles {
		if _, err := filepath.Match(p, ""); err != nil {
			add("integrations.openvpn.status_files : motif %q invalide", p)
		}
	}
}

func validateV3(d SNMPDevice, at string, add func(string, ...any)) {
	if d.User == "" {
		add("%s : user manquant (SNMP v3)", at)
	}
	needAuth, needPriv := false, false
	switch strings.ToLower(d.Level) {
	case "noauthnopriv":
	case "authnopriv":
		needAuth = true
	case "authpriv":
		needAuth, needPriv = true, true
	default:
		add("%s : level %q (noAuthNoPriv, authNoPriv ou authPriv)", at, d.Level)
	}
	if needAuth {
		switch d.AuthProtocol {
		case "md5", "sha", "sha224", "sha256", "sha384", "sha512":
		default:
			add("%s : auth_protocol %q (md5, sha, sha224, sha256, sha384, sha512)", at, d.AuthProtocol)
		}
		if len(d.AuthKey) < 8 {
			add("%s : auth_key de 8 caractères au moins (RFC 3414)", at)
		}
	}
	if needPriv {
		switch d.PrivProtocol {
		case "des", "aes", "aes192", "aes256", "aes192c", "aes256c":
		default:
			add("%s : priv_protocol %q (des, aes, aes192, aes256, aes192c, aes256c)", at, d.PrivProtocol)
		}
		if len(d.PrivKey) < 8 {
			add("%s : priv_key de 8 caractères au moins (RFC 3414)", at)
		}
	}
}

// SortedScriptNames rend les noms de scripts triés (affichage, capacités).
func SortedScriptNames(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ProcessBound rend min et max d'un contrôle de processus (-1 = sans borne).
func (p ProcessCheck) ProcessBound() (lo, hi int) {
	lo, hi = 0, -1
	if p.Min != nil {
		lo = *p.Min
	}
	if p.Max != nil {
		hi = *p.Max
	}
	return lo, hi
}

// DeviceAddress rend l'adresse à joindre (hôte:port) d'un équipement.
func (d SNMPDevice) DeviceAddress() string {
	return net.JoinHostPort(d.Address, strconv.Itoa(d.Port))
}
