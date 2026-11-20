package config

import (
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Sondes dédiées (vague 3 des intégrations) : chaque logiciel est lu par son interface native
// (page de statut, socket d'administration, commande de statistiques, API REST). Toutes sont en
// mode auto : un logiciel à l'adresse habituelle est détecté seul ; celles qui exigent un compte
// ne s'activent que si leurs identifiants (ou leur url) sont posés. Les sondes de service
// acceptent des « instances » (§ 50), comme les intégrations de produits.

// DedicatedIntegrations : intégrées à IntegrationsConfig (champ inline), déclarées ici seules.
type DedicatedIntegrations struct {
	PHPFPM           PHPFPMConfig      `yaml:"phpfpm"`
	Varnish          CommandProbe      `yaml:"varnish"`
	Squid            ServiceProbe      `yaml:"squid"`
	Lighttpd         ServiceProbe      `yaml:"lighttpd"`
	Gunicorn         GunicornConfig    `yaml:"gunicorn"`
	Supervisord      SupervisordConfig `yaml:"supervisord"`
	PgBouncer        DSNProbe          `yaml:"pgbouncer"`
	ProxySQL         DSNProbe          `yaml:"proxysql"`
	ClickHouse       ServiceProbe      `yaml:"clickhouse"`
	CouchDB          ServiceProbe      `yaml:"couchdb"`
	Couchbase        ServiceProbe      `yaml:"couchbase"`
	ZooKeeper        AddressProbe      `yaml:"zookeeper"`
	Ceph             CommandProbe      `yaml:"ceph"`
	OpenLDAP         OpenLDAPConfig    `yaml:"openldap"`
	PowerDNS         PowerDNSConfig    `yaml:"powerdns"`
	PowerDNSRecursor PowerDNSConfig    `yaml:"powerdns_recursor"`
	Keycloak         KeycloakConfig    `yaml:"keycloak"`
	Nexus            ServiceProbe      `yaml:"nexus"`
	Harbor           ServiceProbe      `yaml:"harbor"`
	Fluentd          ServiceProbe      `yaml:"fluentd"`
	Btrfs            IntegrationToggle `yaml:"btrfs"`
	NFS              NFSConfig         `yaml:"nfs"`
	SSH              SSHConfig         `yaml:"ssh"`
	Wazuh            ServiceProbe      `yaml:"wazuh"`
	Suricata         SuricataConfig    `yaml:"suricata"`

	// DedicatedExtra : entrées « instances » décodées, remplies par normalizeDedicated.
	DedicatedExtra DedicatedInstances `yaml:"-"`
}

// DedicatedInstances : instances décodées des sondes dédiées.
type DedicatedInstances struct {
	PHPFPM           []PHPFPMConfig
	Varnish          []CommandProbe
	Squid            []ServiceProbe
	Lighttpd         []ServiceProbe
	Supervisord      []SupervisordConfig
	PgBouncer        []DSNProbe
	ProxySQL         []DSNProbe
	ClickHouse       []ServiceProbe
	CouchDB          []ServiceProbe
	Couchbase        []ServiceProbe
	ZooKeeper        []AddressProbe
	Ceph             []CommandProbe
	OpenLDAP         []OpenLDAPConfig
	PowerDNS         []PowerDNSConfig
	PowerDNSRecursor []PowerDNSConfig
	Keycloak         []KeycloakConfig
	Nexus            []ServiceProbe
	Harbor           []ServiceProbe
	Fluentd          []ServiceProbe
	Wazuh            []ServiceProbe
	Suricata         []SuricataConfig
}

// ServiceProbe : un logiciel lu par HTTP (page de statut ou API REST).
type ServiceProbe struct {
	Enabled  Tristate `yaml:"enabled"`
	URL      string   `yaml:"url"`
	Username string   `yaml:"username"`
	Password string   `yaml:"password"`
	Insecure bool     `yaml:"insecure"` // certificat autosigné en https

	Multi `yaml:",inline"`
}

// CommandProbe : un logiciel lu par sa commande de statistiques (varnishstat, ceph). command :
// la commande de base, en liste ou en texte découpé sans shell (« docker exec varnish varnishstat »
// pour un logiciel en conteneur) ; l'agent y ajoute ses propres arguments.
type CommandProbe struct {
	Enabled Tristate    `yaml:"enabled"`
	Command CommandLine `yaml:"command"`
	Timeout Duration    `yaml:"timeout"`

	Multi `yaml:",inline"`
}

// DSNProbe : console d'administration lue en SQL (PgBouncer, ProxySQL).
type DSNProbe struct {
	Enabled Tristate `yaml:"enabled"`
	DSN     string   `yaml:"dsn"`

	Multi `yaml:",inline"`
}

// AddressProbe : protocole texte sur TCP (ZooKeeper).
type AddressProbe struct {
	Enabled Tristate `yaml:"enabled"`
	Address string   `yaml:"address"`

	Multi `yaml:",inline"`
}

// PHPFPMConfig : page de statut de PHP-FPM (pm.status_path), lue en FastCGI directement sur la
// socket du pool (address : chemin d'une socket Unix ou hôte:port), ou en HTTP à travers le
// serveur web (url). Sans address ni url : les sockets /run/php/php*-fpm.sock et
// /run/php-fpm/*.sock trouvées sur l'hôte.
type PHPFPMConfig struct {
	Enabled    Tristate `yaml:"enabled"`
	Address    string   `yaml:"address"`
	URL        string   `yaml:"url"`
	StatusPath string   `yaml:"status_path"`
	PingPath   string   `yaml:"ping_path"`

	Multi `yaml:",inline"`
}

// GunicornConfig : processus maîtres « gunicorn: master [app] » et leurs workers ; apps : noms
// attendus (un maître absent passe gunicorn.is_running en CRIT) ; vide = ceux qui tournent.
// Les requêtes viennent du StatsD natif de Gunicorn (--statsd-host 127.0.0.1:8125).
type GunicornConfig struct {
	Enabled Tristate `yaml:"enabled"`
	Apps    []string `yaml:"apps"`
}

// SupervisordConfig : API XML-RPC de Supervisor, par la socket Unix (socket) ou le serveur
// HTTP (url, http://127.0.0.1:9001/RPC2).
type SupervisordConfig struct {
	Enabled  Tristate `yaml:"enabled"`
	Socket   string   `yaml:"socket"`
	URL      string   `yaml:"url"`
	Username string   `yaml:"username"`
	Password string   `yaml:"password"`

	Multi `yaml:",inline"`
}

// OpenLDAPConfig : base cn=Monitor de slapd. url : ldap://hôte:389, ldaps://hôte:636 ou
// ldapi:// (socket Unix, chemin encodé : ldapi://%2Frun%2Fslapd%2Fldapi) ; bind_dn et password
// d'un compte qui lit cn=Monitor (sans bind_dn : connexion anonyme).
type OpenLDAPConfig struct {
	Enabled  Tristate `yaml:"enabled"`
	URL      string   `yaml:"url"`
	BindDN   string   `yaml:"bind_dn"`
	Password string   `yaml:"password"`
	Insecure bool     `yaml:"insecure"`

	Multi `yaml:",inline"`
}

// PowerDNSConfig : API du serveur web intégré de PowerDNS (serveur faisant autorité ou
// Recursor), clé X-API-Key.
type PowerDNSConfig struct {
	Enabled  Tristate `yaml:"enabled"`
	URL      string   `yaml:"url"`
	APIKey   string   `yaml:"api_key"`
	ServerID string   `yaml:"server_id"`
	Insecure bool     `yaml:"insecure"`

	Multi `yaml:",inline"`
}

// KeycloakConfig : santé (url de gestion, /health/ready) et sessions par royaume (API
// d'administration, compte de service : client_id et client_secret d'un client à
// identifiants de client, rôles view-realm et view-users du realm-management de chaque royaume).
type KeycloakConfig struct {
	Enabled      Tristate `yaml:"enabled"`
	URL          string   `yaml:"url"`       // http://127.0.0.1:9000 (port de gestion)
	AdminURL     string   `yaml:"admin_url"` // http://127.0.0.1:8080
	Realm        string   `yaml:"realm"`     // royaume du compte de service
	Realms       []string `yaml:"realms"`    // royaumes lus ; vide = tous ceux visibles
	ClientID     string   `yaml:"client_id"`
	ClientSecret string   `yaml:"client_secret"`
	Insecure     bool     `yaml:"insecure"`

	Multi `yaml:",inline"`
}

// NFSConfig : client (montages NFS, /proc/self/mountstats) et serveur (/proc/net/rpc/nfsd).
// mounts : motifs des points de montage suivis ; vide = tous. Un montage qui ne répond pas à
// statfs dans timeout passe nfs.mount en CRIT. server_stats : fichier des statistiques de nfsd,
// propres à chaque espace réseau (serveur NFS en conteneur : /proc/<pid du conteneur>/net/rpc/nfsd).
type NFSConfig struct {
	Enabled     Tristate `yaml:"enabled"`
	Mounts      []string `yaml:"mounts"`
	Timeout     Duration `yaml:"timeout"`
	ServerStats string   `yaml:"server_stats"`
}

// SSHConfig : disponibilité et bannière de serveurs SSH (aucune authentification).
type SSHConfig struct {
	Enabled Tristate    `yaml:"enabled"`
	Targets []SSHTarget `yaml:"targets"`
}

// SSHTarget : une cible ; expect_banner : expression régulière que la bannière doit vérifier.
type SSHTarget struct {
	Name         string            `yaml:"name"`
	Address      string            `yaml:"address"`
	ExpectBanner string            `yaml:"expect_banner"`
	Timeout      Duration          `yaml:"timeout"`
	Tags         map[string]string `yaml:"tags"`
}

// SuricataConfig : fichier eve.json (statistiques « stats » et alertes).
type SuricataConfig struct {
	Enabled    Tristate `yaml:"enabled"`
	EveFile    string   `yaml:"eve_file"`
	StaleAfter Duration `yaml:"stale_after"`

	Multi `yaml:",inline"`
}

func defaultsDedicated(c *Config) {
	d := &c.Integrations.DedicatedIntegrations
	d.PHPFPM = PHPFPMConfig{Enabled: "auto", StatusPath: "/status", PingPath: "/ping"}
	d.Varnish = CommandProbe{Enabled: "auto", Command: CommandLine{"varnishstat"}, Timeout: Duration(10 * time.Second)}
	d.Squid = ServiceProbe{Enabled: "auto", URL: "http://127.0.0.1:3128"}
	d.Lighttpd = ServiceProbe{Enabled: "auto"}
	d.Gunicorn = GunicornConfig{Enabled: "auto"}
	d.Supervisord = SupervisordConfig{Enabled: "auto"}
	d.PgBouncer = DSNProbe{Enabled: "auto"}
	d.ProxySQL = DSNProbe{Enabled: "auto"}
	d.ClickHouse = ServiceProbe{Enabled: "auto", URL: "http://127.0.0.1:8123"}
	d.CouchDB = ServiceProbe{Enabled: "auto", URL: "http://127.0.0.1:5984"}
	d.Couchbase = ServiceProbe{Enabled: "auto", URL: "http://127.0.0.1:8091"}
	d.ZooKeeper = AddressProbe{Enabled: "auto", Address: "127.0.0.1:2181"}
	d.Ceph = CommandProbe{Enabled: "auto", Command: CommandLine{"ceph"}, Timeout: Duration(20 * time.Second)}
	d.OpenLDAP = OpenLDAPConfig{Enabled: "auto"}
	d.PowerDNS = PowerDNSConfig{Enabled: "auto", URL: "http://127.0.0.1:8081", ServerID: "localhost"}
	d.PowerDNSRecursor = PowerDNSConfig{Enabled: "auto", URL: "http://127.0.0.1:8082", ServerID: "localhost"}
	d.Keycloak = KeycloakConfig{Enabled: "auto", Realm: "master"}
	d.Nexus = ServiceProbe{Enabled: "auto"}
	d.Harbor = ServiceProbe{Enabled: "auto"}
	d.Fluentd = ServiceProbe{Enabled: "auto", URL: "http://127.0.0.1:24220"}
	d.Btrfs = IntegrationToggle{Enabled: "auto"}
	d.NFS = NFSConfig{Enabled: "auto", Timeout: Duration(5 * time.Second)}
	d.SSH = SSHConfig{Enabled: "auto"}
	d.Wazuh = ServiceProbe{Enabled: "auto", URL: "https://127.0.0.1:55000"}
	d.Suricata = SuricataConfig{Enabled: "auto", EveFile: "/var/log/suricata/eve.json", StaleAfter: Duration(5 * time.Minute)}
}

func (c *Config) normalizeDedicated() {
	in := &c.Integrations.DedicatedIntegrations
	for i := range in.SSH.Targets {
		t := &in.SSH.Targets[i]
		t.Name, t.Address = strings.TrimSpace(t.Name), strings.TrimSpace(t.Address)
		if t.Address != "" && !strings.Contains(t.Address, ":") {
			t.Address = net.JoinHostPort(t.Address, "22")
		}
		if t.Name == "" {
			t.Name = t.Address
		}
		if t.Timeout == 0 {
			t.Timeout = Duration(5 * time.Second)
		}
	}
	d := func() DedicatedIntegrations { return Default().Integrations.DedicatedIntegrations }
	warn := func(s string) { c.Warnings = append(c.Warnings, s) }
	fail := func(s string) { c.instErrs = append(c.instErrs, s) }
	x := &in.DedicatedExtra
	x.PHPFPM = decodeInstances("phpfpm", in.PHPFPM.Raw, func() PHPFPMConfig { v := d().PHPFPM; v.Enabled = "true"; return v },
		func(v *PHPFPMConfig) *Multi { return &v.Multi }, warn, fail)
	x.Varnish = decodeInstances("varnish", in.Varnish.Raw, func() CommandProbe { v := d().Varnish; v.Enabled = "true"; return v },
		func(v *CommandProbe) *Multi { return &v.Multi }, warn, fail)
	x.Squid = decodeInstances("squid", in.Squid.Raw, func() ServiceProbe { v := d().Squid; v.Enabled = "true"; return v },
		func(v *ServiceProbe) *Multi { return &v.Multi }, warn, fail)
	x.Lighttpd = decodeInstances("lighttpd", in.Lighttpd.Raw, func() ServiceProbe { v := d().Lighttpd; v.Enabled = "true"; return v },
		func(v *ServiceProbe) *Multi { return &v.Multi }, warn, fail)
	x.Supervisord = decodeInstances("supervisord", in.Supervisord.Raw, func() SupervisordConfig { v := d().Supervisord; v.Enabled = "true"; return v },
		func(v *SupervisordConfig) *Multi { return &v.Multi }, warn, fail)
	x.PgBouncer = decodeInstances("pgbouncer", in.PgBouncer.Raw, func() DSNProbe { v := d().PgBouncer; v.Enabled = "true"; return v },
		func(v *DSNProbe) *Multi { return &v.Multi }, warn, fail)
	x.ProxySQL = decodeInstances("proxysql", in.ProxySQL.Raw, func() DSNProbe { v := d().ProxySQL; v.Enabled = "true"; return v },
		func(v *DSNProbe) *Multi { return &v.Multi }, warn, fail)
	x.ClickHouse = decodeInstances("clickhouse", in.ClickHouse.Raw, func() ServiceProbe { v := d().ClickHouse; v.Enabled = "true"; return v },
		func(v *ServiceProbe) *Multi { return &v.Multi }, warn, fail)
	x.CouchDB = decodeInstances("couchdb", in.CouchDB.Raw, func() ServiceProbe { v := d().CouchDB; v.Enabled = "true"; return v },
		func(v *ServiceProbe) *Multi { return &v.Multi }, warn, fail)
	x.Couchbase = decodeInstances("couchbase", in.Couchbase.Raw, func() ServiceProbe { v := d().Couchbase; v.Enabled = "true"; return v },
		func(v *ServiceProbe) *Multi { return &v.Multi }, warn, fail)
	x.ZooKeeper = decodeInstances("zookeeper", in.ZooKeeper.Raw, func() AddressProbe { v := d().ZooKeeper; v.Enabled = "true"; return v },
		func(v *AddressProbe) *Multi { return &v.Multi }, warn, fail)
	x.Ceph = decodeInstances("ceph", in.Ceph.Raw, func() CommandProbe { v := d().Ceph; v.Enabled = "true"; return v },
		func(v *CommandProbe) *Multi { return &v.Multi }, warn, fail)
	x.OpenLDAP = decodeInstances("openldap", in.OpenLDAP.Raw, func() OpenLDAPConfig { v := d().OpenLDAP; v.Enabled = "true"; return v },
		func(v *OpenLDAPConfig) *Multi { return &v.Multi }, warn, fail)
	x.PowerDNS = decodeInstances("powerdns", in.PowerDNS.Raw, func() PowerDNSConfig { v := d().PowerDNS; v.Enabled = "true"; return v },
		func(v *PowerDNSConfig) *Multi { return &v.Multi }, warn, fail)
	x.PowerDNSRecursor = decodeInstances("powerdns_recursor", in.PowerDNSRecursor.Raw, func() PowerDNSConfig { v := d().PowerDNSRecursor; v.Enabled = "true"; return v },
		func(v *PowerDNSConfig) *Multi { return &v.Multi }, warn, fail)
	x.Keycloak = decodeInstances("keycloak", in.Keycloak.Raw, func() KeycloakConfig { v := d().Keycloak; v.Enabled = "true"; return v },
		func(v *KeycloakConfig) *Multi { return &v.Multi }, warn, fail)
	x.Nexus = decodeInstances("nexus", in.Nexus.Raw, func() ServiceProbe { v := d().Nexus; v.Enabled = "true"; return v },
		func(v *ServiceProbe) *Multi { return &v.Multi }, warn, fail)
	x.Harbor = decodeInstances("harbor", in.Harbor.Raw, func() ServiceProbe { v := d().Harbor; v.Enabled = "true"; return v },
		func(v *ServiceProbe) *Multi { return &v.Multi }, warn, fail)
	x.Fluentd = decodeInstances("fluentd", in.Fluentd.Raw, func() ServiceProbe { v := d().Fluentd; v.Enabled = "true"; return v },
		func(v *ServiceProbe) *Multi { return &v.Multi }, warn, fail)
	x.Wazuh = decodeInstances("wazuh", in.Wazuh.Raw, func() ServiceProbe { v := d().Wazuh; v.Enabled = "true"; return v },
		func(v *ServiceProbe) *Multi { return &v.Multi }, warn, fail)
	x.Suricata = decodeInstances("suricata", in.Suricata.Raw, func() SuricataConfig { v := d().Suricata; v.Enabled = "true"; return v },
		func(v *SuricataConfig) *Multi { return &v.Multi }, warn, fail)
}

// ── Validation ─────────────────────────────────────────────────────────────────────────────

// absPath : chemin absolu sous Linux (« /var/log/… ») ou sous Windows (« C:\… »). Les valeurs par
// défaut des sondes sont des chemins Linux : filepath.IsAbs seul les refuse sous Windows, et une
// conf par défaut refusée bloque la mise à jour des agents Windows (0.8.0, corrigé en 0.8.1).
func absPath(p string) bool { return strings.HasPrefix(p, "/") || filepath.IsAbs(p) }

type vfn = func(string, ...any)

func validURL(add vfn, at, key, raw string, schemes ...string) {
	if raw == "" {
		return
	}
	if strings.HasPrefix(raw, "ldapi://") {
		for _, s := range schemes {
			if s == "ldapi" {
				return // chemin de socket encodé (%2F) que url.Parse refuse
			}
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		add("%s.%s invalide : %q", at, key, raw)
		return
	}
	for _, s := range schemes {
		if u.Scheme == s {
			if u.User != nil {
				add("%s.%s : pas d'identifiants dans l'url (username et password)", at, key)
			}
			return
		}
	}
	add("%s.%s : schéma %q inattendu (%s)", at, key, u.Scheme, strings.Join(schemes, ", "))
}

func clearSecret(warn vfn, at, rawURL string, hasSecret bool) {
	if !hasSecret || rawURL == "" {
		return
	}
	if u, err := url.Parse(rawURL); err == nil && u.Scheme == "http" {
		if h := u.Hostname(); h != "localhost" && !isLoopback(h) {
			warn("%s : identifiants envoyés en clair (http vers %s)", at, h)
		}
	}
}

func (p ServiceProbe) validate(at string, add, warn vfn) {
	validURL(add, at, "url", p.URL, "http", "https")
	clearSecret(warn, at, p.URL, p.Password != "")
	if p.Insecure {
		warn("%s : insecure actif, certificat non vérifié", at)
	}
}

func (p CommandProbe) validate(at string, add, _ vfn) {
	if len(p.Command) == 0 || p.Command[0] == "" {
		add("%s.command manquante", at)
	} else if c := p.Command[0]; !absPath(c) && strings.ContainsAny(c, `/\`) {
		add("%s.command : %q : chemin absolu ou simple nom de commande attendu", at, c)
	}
	if t := p.Timeout.D(); t < time.Second || t > 2*time.Minute {
		add("%s.timeout %s hors de 1s à 2m", at, t)
	}
}

func (p DSNProbe) validate(at string, add, _ vfn, kind string) {
	if p.DSN == "" {
		if p.Enabled == "true" {
			add("%s.dsn manquant", at)
		}
		return
	}
	switch kind {
	case "postgres":
		if !strings.HasPrefix(p.DSN, "postgres://") && !strings.HasPrefix(p.DSN, "postgresql://") && !strings.Contains(p.DSN, "=") {
			add("%s.dsn : attendu postgres://utilisateur:motdepasse@hôte:6432/pgbouncer", at)
		}
	case "mysql":
		if !strings.Contains(p.DSN, "@") || !strings.Contains(p.DSN, "/") {
			add("%s.dsn : attendu utilisateur:motdepasse@tcp(hôte:6032)/", at)
		}
	}
}

func (p AddressProbe) validate(at string, add, _ vfn) {
	if _, _, err := net.SplitHostPort(p.Address); err != nil {
		add("%s.address %q attendue sous la forme hôte:port", at, p.Address)
	}
}

func (p PHPFPMConfig) validate(at string, add, _ vfn) {
	if p.Address != "" && !absPath(p.Address) {
		if _, _, err := net.SplitHostPort(p.Address); err != nil {
			add("%s.address %q : chemin d'une socket Unix ou hôte:port attendu", at, p.Address)
		}
	}
	validURL(add, at, "url", p.URL, "http", "https")
	for k, v := range map[string]string{"status_path": p.StatusPath, "ping_path": p.PingPath} {
		if v != "" && !strings.HasPrefix(v, "/") {
			add("%s.%s %q : chemin commençant par / attendu", at, k, v)
		}
	}
}

func (p SupervisordConfig) validate(at string, add, warn vfn) {
	if p.Socket != "" && !absPath(p.Socket) {
		add("%s.socket : chemin absolu attendu, pas %q", at, p.Socket)
	}
	validURL(add, at, "url", p.URL, "http", "https")
	clearSecret(warn, at, p.URL, p.Password != "")
	if strings.HasPrefix(p.Socket, "/tmp/") {
		warn("%s.socket %q : /tmp de l'agent est privé (PrivateTmp de l'unité systemd), socket invisible ; préférer /run/supervisor.sock", at, p.Socket)
	}
}

func (p OpenLDAPConfig) validate(at string, add, warn vfn) {
	validURL(add, at, "url", p.URL, "ldap", "ldaps", "ldapi")
	if p.Password != "" && p.BindDN == "" {
		add("%s : password sans bind_dn", at)
	}
	if u, err := url.Parse(p.URL); err == nil && u.Scheme == "ldap" && p.Password != "" {
		if h := u.Hostname(); h != "localhost" && !isLoopback(h) {
			warn("%s : mot de passe envoyé en clair (ldap:// vers %s ; préférer ldaps://)", at, h)
		}
	}
	if p.Insecure {
		warn("%s : insecure actif, certificat non vérifié", at)
	}
}

func (p PowerDNSConfig) validate(at string, add, warn vfn) {
	validURL(add, at, "url", p.URL, "http", "https")
	clearSecret(warn, at, p.URL, p.APIKey != "")
	if p.ServerID == "" || strings.ContainsAny(p.ServerID, "/?#") {
		add("%s.server_id invalide : %q", at, p.ServerID)
	}
	if p.Enabled == "true" && p.APIKey == "" {
		add("%s.api_key manquante (api-key de la conf de PowerDNS)", at)
	}
}

var realmRe = regexp.MustCompile(`^[A-Za-z0-9._\-]{1,100}$`)

func (p KeycloakConfig) validate(at string, add, warn vfn) {
	validURL(add, at, "url", p.URL, "http", "https")
	validURL(add, at, "admin_url", p.AdminURL, "http", "https")
	clearSecret(warn, at, p.AdminURL, p.ClientSecret != "")
	if (p.ClientID == "") != (p.ClientSecret == "") {
		add("%s : client_id et client_secret vont ensemble", at)
	}
	if p.ClientID != "" && p.AdminURL == "" {
		add("%s : admin_url manquante pour lire les sessions", at)
	}
	for _, r := range append([]string{p.Realm}, p.Realms...) {
		if !realmRe.MatchString(r) {
			add("%s : royaume %q invalide", at, r)
		}
	}
	if p.Enabled == "true" && p.URL == "" && p.AdminURL == "" {
		add("%s : url (gestion, /health) ou admin_url attendue", at)
	}
}

func (p SuricataConfig) validate(at string, add, _ vfn) {
	if !absPath(p.EveFile) {
		add("%s.eve_file : chemin absolu attendu, pas %q", at, p.EveFile)
	}
	if s := p.StaleAfter.D(); s < 30*time.Second || s > 24*time.Hour {
		add("%s.stale_after %s hors de 30s à 24h", at, s)
	}
}

var gunicornAppRe = regexp.MustCompile(`^[^\s\[\]]{1,100}$`)

func (c *Config) validateDedicated(add, warn vfn) {
	in := c.Integrations.DedicatedIntegrations
	x := in.DedicatedExtra
	const p = "integrations."
	in.PHPFPM.validate(p+"phpfpm", add, warn)
	in.Varnish.validate(p+"varnish", add, warn)
	in.Squid.validate(p+"squid", add, warn)
	in.Lighttpd.validate(p+"lighttpd", add, warn)
	in.Supervisord.validate(p+"supervisord", add, warn)
	in.PgBouncer.validate(p+"pgbouncer", add, warn, "postgres")
	in.ProxySQL.validate(p+"proxysql", add, warn, "mysql")
	in.ClickHouse.validate(p+"clickhouse", add, warn)
	in.CouchDB.validate(p+"couchdb", add, warn)
	in.Couchbase.validate(p+"couchbase", add, warn)
	in.ZooKeeper.validate(p+"zookeeper", add, warn)
	in.Ceph.validate(p+"ceph", add, warn)
	in.OpenLDAP.validate(p+"openldap", add, warn)
	in.PowerDNS.validate(p+"powerdns", add, warn)
	in.PowerDNSRecursor.validate(p+"powerdns_recursor", add, warn)
	in.Keycloak.validate(p+"keycloak", add, warn)
	in.Nexus.validate(p+"nexus", add, warn)
	in.Harbor.validate(p+"harbor", add, warn)
	in.Fluentd.validate(p+"fluentd", add, warn)
	in.Wazuh.validate(p+"wazuh", add, warn)
	in.Suricata.validate(p+"suricata", add, warn)
	for _, s := range []struct {
		key string
		p   ServiceProbe
	}{{"lighttpd", in.Lighttpd}, {"nexus", in.Nexus}, {"harbor", in.Harbor}} {
		if s.p.Enabled == "true" && s.p.URL == "" {
			add("integrations.%s : enabled sans url", s.key)
		}
	}
	if in.OpenLDAP.Enabled == "true" && in.OpenLDAP.URL == "" {
		add("integrations.openldap : enabled sans url")
	}
	for _, a := range in.Gunicorn.Apps {
		if !gunicornAppRe.MatchString(a) {
			add("integrations.gunicorn.apps : nom %q invalide", a)
		}
	}
	for _, m := range in.NFS.Mounts {
		if _, err := filepath.Match(m, ""); err != nil {
			add("integrations.nfs.mounts : motif %q invalide", m)
		}
	}
	if p := in.NFS.ServerStats; p != "" && !absPath(p) {
		add("integrations.nfs.server_stats : chemin absolu attendu, pas %q", p)
	}
	if t := in.NFS.Timeout.D(); t < time.Second || t > time.Minute {
		add("integrations.nfs.timeout %s hors de 1s à 1m", t)
	}
	seen := map[string]bool{}
	for i, t := range in.SSH.Targets {
		at := fmt.Sprintf("integrations.ssh.targets[%d]", i)
		if _, port, err := net.SplitHostPort(t.Address); err != nil || port == "" {
			add("%s : address %q attendue sous la forme hôte[:port]", at, t.Address)
		}
		if seen[t.Name] {
			add("%s : name %q en double", at, t.Name)
		}
		seen[t.Name] = true
		if t.ExpectBanner != "" {
			if _, err := regexp.Compile(t.ExpectBanner); err != nil {
				add("%s : expect_banner n'est pas une expression régulière valide : %v", at, err)
			}
		}
		if d := t.Timeout.D(); d < 500*time.Millisecond || d > 30*time.Second {
			add("%s : timeout %s hors de 0,5s à 30s", at, d)
		}
		for k := range t.Tags {
			if !tagKeyRe.MatchString(k) {
				add("%s : étiquette %q : clé hors de [a-z0-9_.-/]", at, k)
			}
		}
	}
	if in.SSH.Enabled == "true" && len(in.SSH.Targets) == 0 {
		add("integrations.ssh : enabled sans targets")
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
	multis := func(n int, get func(int) Multi) []Multi {
		out := make([]Multi, n)
		for i := range out {
			out[i] = get(i)
		}
		return out
	}
	names("phpfpm", in.PHPFPM.Name, multis(len(x.PHPFPM), func(i int) Multi { return x.PHPFPM[i].Multi }))
	for _, v := range x.PHPFPM {
		v.validate(at("phpfpm", v.Multi), add, warn)
	}
	names("varnish", in.Varnish.Name, multis(len(x.Varnish), func(i int) Multi { return x.Varnish[i].Multi }))
	for _, v := range x.Varnish {
		v.validate(at("varnish", v.Multi), add, warn)
	}
	names("ceph", in.Ceph.Name, multis(len(x.Ceph), func(i int) Multi { return x.Ceph[i].Multi }))
	for _, v := range x.Ceph {
		v.validate(at("ceph", v.Multi), add, warn)
	}
	for _, s := range []struct {
		key, main string
		list      []ServiceProbe
	}{
		{"squid", in.Squid.Name, x.Squid}, {"lighttpd", in.Lighttpd.Name, x.Lighttpd},
		{"clickhouse", in.ClickHouse.Name, x.ClickHouse}, {"couchdb", in.CouchDB.Name, x.CouchDB},
		{"couchbase", in.Couchbase.Name, x.Couchbase}, {"nexus", in.Nexus.Name, x.Nexus},
		{"harbor", in.Harbor.Name, x.Harbor}, {"fluentd", in.Fluentd.Name, x.Fluentd}, {"wazuh", in.Wazuh.Name, x.Wazuh},
	} {
		l := s.list
		names(s.key, s.main, multis(len(l), func(i int) Multi { return l[i].Multi }))
		for _, v := range l {
			v.validate(at(s.key, v.Multi), add, warn)
			if v.URL == "" {
				add("%s : url manquante", at(s.key, v.Multi))
			}
		}
	}
	names("supervisord", in.Supervisord.Name, multis(len(x.Supervisord), func(i int) Multi { return x.Supervisord[i].Multi }))
	for _, v := range x.Supervisord {
		v.validate(at("supervisord", v.Multi), add, warn)
	}
	names("pgbouncer", in.PgBouncer.Name, multis(len(x.PgBouncer), func(i int) Multi { return x.PgBouncer[i].Multi }))
	for _, v := range x.PgBouncer {
		v.validate(at("pgbouncer", v.Multi), add, warn, "postgres")
	}
	names("proxysql", in.ProxySQL.Name, multis(len(x.ProxySQL), func(i int) Multi { return x.ProxySQL[i].Multi }))
	for _, v := range x.ProxySQL {
		v.validate(at("proxysql", v.Multi), add, warn, "mysql")
	}
	names("zookeeper", in.ZooKeeper.Name, multis(len(x.ZooKeeper), func(i int) Multi { return x.ZooKeeper[i].Multi }))
	for _, v := range x.ZooKeeper {
		v.validate(at("zookeeper", v.Multi), add, warn)
	}
	names("openldap", in.OpenLDAP.Name, multis(len(x.OpenLDAP), func(i int) Multi { return x.OpenLDAP[i].Multi }))
	for _, v := range x.OpenLDAP {
		v.validate(at("openldap", v.Multi), add, warn)
		if v.URL == "" {
			add("%s : url manquante", at("openldap", v.Multi))
		}
	}
	names("powerdns", in.PowerDNS.Name, multis(len(x.PowerDNS), func(i int) Multi { return x.PowerDNS[i].Multi }))
	for _, v := range x.PowerDNS {
		v.validate(at("powerdns", v.Multi), add, warn)
	}
	names("powerdns_recursor", in.PowerDNSRecursor.Name, multis(len(x.PowerDNSRecursor), func(i int) Multi { return x.PowerDNSRecursor[i].Multi }))
	for _, v := range x.PowerDNSRecursor {
		v.validate(at("powerdns_recursor", v.Multi), add, warn)
	}
	names("keycloak", in.Keycloak.Name, multis(len(x.Keycloak), func(i int) Multi { return x.Keycloak[i].Multi }))
	for _, v := range x.Keycloak {
		v.validate(at("keycloak", v.Multi), add, warn)
	}
	names("suricata", in.Suricata.Name, multis(len(x.Suricata), func(i int) Multi { return x.Suricata[i].Multi }))
	for _, v := range x.Suricata {
		v.validate(at("suricata", v.Multi), add, warn)
	}
}
