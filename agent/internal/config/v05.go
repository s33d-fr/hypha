package config

import (
	"fmt"
	"net"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"s33d.fr/hypha/agent/internal/omprofiles"
)

// Moteurs génériques de la 0.5.0 (vague 1 des intégrations) :
//   - integrations.openmetrics : lecteur OpenMetrics/Prometheus, une entrée par cible, avec ou
//     sans profil intégré (omprofiles) ;
//   - integrations.nagios : plugins Nagios, Monitoring Plugins ou Centreon lancés par l'agent ;
//     les commandes ne viennent que des fichiers de conf locaux (jamais du serveur) ;
//   - syslog : réception syslog UDP et TCP, coupée par défaut.
// Les listes openmetrics et nagios de plusieurs fichiers conf.d s'additionnent (merge.go).

// OpenMetricsInstance : une cible au format Prometheus ou OpenMetrics (texte).
type OpenMetricsInstance struct {
	Name      string   `yaml:"name"`    // étiquette instance_name ; par défaut le nom du profil
	Profile   string   `yaml:"profile"` // profil intégré (node_exporter, prometheus…)
	URL       string   `yaml:"url"`     // sans url : http://127.0.0.1:<port du profil><chemin du profil>
	Interval  Duration `yaml:"interval"`
	Timeout   Duration `yaml:"timeout"`
	Namespace string   `yaml:"namespace"` // préfixe des métriques ; profil, sinon le nom de l'instance
	// Metrics : familles retenues (nom exact, motif glob, ou « re:expression ») ; vide sans profil =
	// toutes, dans la limite de max_series.
	Metrics        []string          `yaml:"metrics"`
	ExcludeMetrics []string          `yaml:"exclude_metrics"`
	Rename         map[string]string `yaml:"rename"`
	// Labels : étiquettes Prometheus gardées comme tags (liste blanche) ; « * » = toutes.
	Labels []string          `yaml:"labels"`
	Tags   map[string]string `yaml:"tags"`

	Headers         map[string]string `yaml:"headers"`
	BearerTokenFile string            `yaml:"bearer_token_file"` // relu à chaque relevé
	Username        string            `yaml:"username"`
	Password        string            `yaml:"password"`
	CAFile          string            `yaml:"ca_file"`
	CertFile        string            `yaml:"cert_file"`
	KeyFile         string            `yaml:"key_file"`
	Insecure        bool              `yaml:"insecure"`

	MaxSeries        int  `yaml:"max_series"`        // garde-fou de cardinalité (2000 par défaut)
	HistogramBuckets bool `yaml:"histogram_buckets"` // envoyer aussi chaque seau des histogrammes
}

// CommandLine : commande d'un plugin, en liste (argv) ou en texte découpé SANS shell
// (guillemets simples et doubles, barre oblique inverse ; ni variables, ni jokers, ni tubes).
type CommandLine []string

// UnmarshalYAML accepte une liste ou une chaîne.
func (c *CommandLine) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.SequenceNode:
		var list []string
		if err := n.Decode(&list); err != nil {
			return err
		}
		*c = list
		return nil
	case yaml.ScalarNode:
		words, err := SplitCommand(n.Value)
		if err != nil {
			return fmt.Errorf("ligne %d : %v", n.Line, err)
		}
		*c = words
		return nil
	}
	return fmt.Errorf("ligne %d : commande attendue en texte ou en liste", n.Line)
}

// SplitCommand découpe une ligne de commande comme un shell POSIX le ferait pour des mots
// simples, sans rien interpréter d'autre : un $, un * ou un | restent des caractères ordinaires.
func SplitCommand(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	inWord := false
	quote := rune(0)
	escaped := false
	for _, r := range s {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case quote == '"':
			switch r {
			case '"':
				quote = 0
			case '\\':
				escaped = true
			default:
				cur.WriteRune(r)
			}
		case r == '\\':
			escaped, inWord = true, true
		case r == '\'' || r == '"':
			quote, inWord = r, true
		case r == ' ' || r == '\t' || r == '\n':
			if inWord {
				out = append(out, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("guillemet non fermé dans %q", s)
	}
	if escaped {
		return nil, fmt.Errorf("barre oblique inverse en fin de commande dans %q", s)
	}
	if inWord {
		out = append(out, cur.String())
	}
	return out, nil
}

// NagiosCheck : un plugin lancé à intervalle régulier ; code de sortie 0/1/2/3 → contrôle
// nagios.check OK/WARN/CRIT/UNKNOWN, données de performance → métriques nagios.perfdata.
type NagiosCheck struct {
	Name     string            `yaml:"name"`
	Command  CommandLine       `yaml:"command"`
	Interval Duration          `yaml:"interval"`
	Timeout  Duration          `yaml:"timeout"`
	User     string            `yaml:"user"`     // compte qui lance le plugin (Linux, agent en root)
	Perfdata *bool             `yaml:"perfdata"` // données de performance en métriques (oui par défaut)
	Env      map[string]string `yaml:"env"`
	Tags     map[string]string `yaml:"tags"`
}

// PerfdataOn dit si les données de performance deviennent des métriques.
func (n NagiosCheck) PerfdataOn() bool { return n.Perfdata == nil || *n.Perfdata }

// NagiosPluginDirs : dossiers où un nom de plugin sans chemin est cherché (jamais le PATH).
var NagiosPluginDirs = []string{
	"/usr/lib/nagios/plugins", "/usr/lib64/nagios/plugins", "/usr/lib/monitoring-plugins",
	"/usr/local/nagios/libexec", "/usr/lib/centreon/plugins", "/usr/lib64/centreon/plugins",
}

// SyslogConfig : réception syslog (RFC 3164 et 5424), coupée par défaut.
type SyslogConfig struct {
	Enabled bool   `yaml:"enabled"`
	UDP     string `yaml:"udp"` // adresse:port ; vide = pas d'UDP
	TCP     string `yaml:"tcp"` // adresse:port ; vide = pas de TCP (octet-counting et fin de ligne)
	// AllowedSources : adresses ou réseaux (CIDR) admis ; vide = tous.
	AllowedSources []string `yaml:"allowed_sources"`
	MaxConnections int      `yaml:"max_connections"`
	RateLimit      int      `yaml:"rate_limit"` // lignes par seconde, au-delà jetées et comptées
	Service        string   `yaml:"service"`    // service des lignes sans APP-NAME
	// Parsers : modèle d'analyse côté serveur par émetteur (attribut syslog.parser).
	Parsers []SyslogParser    `yaml:"parsers"`
	Tags    map[string]string `yaml:"tags"`
}

// SyslogParser : émetteurs (adresse, réseau CIDR ou nom d'hôte, motif glob permis) et modèle
// d'analyse que le serveur leur applique (kv, puis les équipements de la vague 4).
type SyslogParser struct {
	Match  string `yaml:"match"`
	Parser string `yaml:"parser"`
}

const (
	defaultOMInterval  = 30 * time.Second
	defaultOMTimeout   = 10 * time.Second
	defaultOMMaxSeries = 2000
	maxOMMaxSeries     = 50000
	defaultNagiosEvery = 60 * time.Second
	defaultNagiosLimit = 30 * time.Second
)

func defaultsV05(c *Config) {
	c.Syslog = SyslogConfig{
		Enabled: false, UDP: "0.0.0.0:514", TCP: "0.0.0.0:514",
		MaxConnections: 64, RateLimit: 2000, Service: "syslog",
	}
}

// normalizeV05 applique les profils et les valeurs par défaut des instances OpenMetrics et des
// plugins Nagios.
func (c *Config) normalizeV05() {
	for i := range c.Integrations.OpenMetrics {
		normalizeOM(&c.Integrations.OpenMetrics[i])
	}
	for i := range c.Integrations.Nagios {
		n := &c.Integrations.Nagios[i]
		n.Name = strings.TrimSpace(n.Name)
		if n.Interval == 0 {
			n.Interval = Duration(defaultNagiosEvery)
		}
		if n.Timeout == 0 {
			n.Timeout = Duration(min(defaultNagiosLimit, n.Interval.D()))
		}
	}
	s := &c.Syslog
	s.UDP, s.TCP = strings.TrimSpace(s.UDP), strings.TrimSpace(s.TCP)
	if s.Service == "" {
		s.Service = "syslog"
	}
}

func normalizeOM(o *OpenMetricsInstance) {
	o.Name = strings.TrimSpace(o.Name)
	o.Profile = strings.ToLower(strings.TrimSpace(o.Profile))
	o.URL = strings.TrimSpace(o.URL)
	if p, ok := omprofiles.Get(o.Profile); ok {
		if o.Name == "" {
			o.Name = p.Name
		}
		if o.Namespace == "" {
			o.Namespace = p.Namespace
		}
		switch {
		case o.URL == "":
			o.URL = fmt.Sprintf("%s://127.0.0.1:%d%s", p.Scheme, p.Port, p.Path)
		default:
			if u, err := url.Parse(o.URL); err == nil && u.Host != "" && (u.Path == "" || u.Path == "/") {
				// Le chemin d'un profil peut porter des paramètres (Consul, Vault :
				// ?format=prometheus) : chemin et paramètres posés séparément.
				if ref, err := url.Parse(p.Path); err == nil {
					u.Path = ref.Path
					if u.RawQuery == "" {
						u.RawQuery = ref.RawQuery
					}
				}
				o.URL = u.String()
			}
		}
		o.Metrics = union(p.Metrics, o.Metrics)
		rename := map[string]string{}
		for k, v := range p.Rename {
			rename[k] = v
		}
		for k, v := range o.Rename {
			rename[k] = v
		}
		o.Rename = rename
		o.Labels = union(p.Labels, o.Labels)
		tags := map[string]string{}
		for k, v := range p.Tags {
			tags[k] = v
		}
		for k, v := range o.Tags {
			tags[k] = v
		}
		o.Tags = tags
	}
	if o.Namespace == "" {
		o.Namespace = strings.ReplaceAll(o.Name, "-", "_")
	}
	if o.Interval == 0 {
		o.Interval = Duration(defaultOMInterval)
	}
	if o.Timeout == 0 {
		o.Timeout = Duration(min(defaultOMTimeout, o.Interval.D()))
	}
	if o.MaxSeries == 0 {
		o.MaxSeries = defaultOMMaxSeries
	}
}

func union(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range append(append([]string{}, a...), b...) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

var (
	omNamespaceRe   = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.\-]{0,99}$`)
	nagiosNameRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9_\-]{0,63}$`)
	syslogParserRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9_\-]{0,31}$`)
	headerNameRe    = regexp.MustCompile(`^[A-Za-z0-9!#$%&'*+.^_` + "`" + `|~-]{1,100}$`)
	metricPatternRe = regexp.MustCompile(`^[A-Za-z0-9_:*?\[\]\-]{1,200}$`)
)

// ValidMetricPattern vérifie un motif de métriques (nom, glob, ou « re:… »).
func ValidMetricPattern(p string) error {
	if rest, ok := strings.CutPrefix(p, "re:"); ok {
		_, err := regexp.Compile(rest)
		return err
	}
	if !metricPatternRe.MatchString(p) {
		return fmt.Errorf("caractères admis : lettres, chiffres, _ : * ? [ ] -")
	}
	_, err := path.Match(p, "")
	return err
}

func (c *Config) validateV05(add, warn func(string, ...any)) {
	names := map[string]bool{}
	for i, o := range c.Integrations.OpenMetrics {
		at := fmt.Sprintf("integrations.openmetrics[%d]", i)
		if o.Name != "" {
			at += " (" + o.Name + ")"
		}
		switch {
		case o.Name == "":
			add("%s : name manquant (ou profile)", at)
		case !instanceNameRe.MatchString(o.Name):
			add("%s : name invalide (minuscules, chiffres, - et _, 32 au plus)", at)
		case names[o.Name]:
			add("%s : name en double", at)
		}
		names[o.Name] = true
		if o.Profile != "" {
			if _, ok := omprofiles.Get(o.Profile); !ok {
				add("%s : profil %q inconnu (%s)", at, o.Profile, strings.Join(omprofiles.Names(), ", "))
			}
		}
		if u, err := url.Parse(o.URL); err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			add("%s : url invalide %q (http(s)://hôte:port/chemin)", at, o.URL)
		} else {
			if u.User != nil {
				add("%s : pas d'identifiants dans l'url (username et password, ou bearer_token_file)", at)
			}
			if u.Scheme == "http" && (o.Password != "" || o.BearerTokenFile != "" || len(o.Headers) > 0) {
				if h := u.Hostname(); h != "localhost" && !isLoopback(h) {
					warn("%s : identifiants envoyés en clair (http vers %s)", at, h)
				}
			}
		}
		if !omNamespaceRe.MatchString(o.Namespace) {
			add("%s : namespace invalide %q", at, o.Namespace)
		}
		if o.Interval.D() < 10*time.Second || o.Interval.D() > time.Hour {
			add("%s : interval %s hors de 10s à 1h", at, o.Interval.D())
		}
		if o.Timeout.D() < time.Second || o.Timeout.D() > o.Interval.D() {
			add("%s : timeout %s hors de 1s à interval", at, o.Timeout.D())
		}
		if o.MaxSeries < 1 || o.MaxSeries > maxOMMaxSeries {
			add("%s : max_series %d hors de 1 à %d", at, o.MaxSeries, maxOMMaxSeries)
		}
		for _, p := range append(append([]string{}, o.Metrics...), o.ExcludeMetrics...) {
			if err := ValidMetricPattern(p); err != nil {
				add("%s : motif de métrique %q invalide : %v", at, p, err)
			}
		}
		for k, v := range o.Rename {
			if !omNamespaceRe.MatchString(v) {
				add("%s : rename.%s : nom %q invalide", at, k, v)
			}
		}
		for k := range o.Headers {
			if !headerNameRe.MatchString(k) {
				add("%s : en-tête %q invalide", at, k)
			}
		}
		for k := range o.Tags {
			if !tagKeyRe.MatchString(k) {
				add("%s : étiquette %q : clé hors de [a-z0-9_.-/]", at, k)
			}
		}
		for _, f := range []string{o.BearerTokenFile, o.CAFile, o.CertFile, o.KeyFile} {
			if f != "" && !filepath.IsAbs(f) {
				add("%s : chemin absolu attendu, pas %q", at, f)
			}
		}
		if (o.CertFile == "") != (o.KeyFile == "") {
			add("%s : cert_file et key_file vont ensemble", at)
		}
		if o.Insecure {
			warn("%s : insecure actif, certificat de la cible non vérifié", at)
		}
	}

	names = map[string]bool{}
	for i, n := range c.Integrations.Nagios {
		at := fmt.Sprintf("integrations.nagios[%d]", i)
		if n.Name != "" {
			at += " (" + n.Name + ")"
		}
		switch {
		case n.Name == "":
			add("%s : name manquant", at)
		case !nagiosNameRe.MatchString(n.Name):
			add("%s : name invalide (minuscules, chiffres, - et _, 64 au plus)", at)
		case names[n.Name]:
			add("%s : name en double", at)
		}
		names[n.Name] = true
		if len(n.Command) == 0 || n.Command[0] == "" {
			add("%s : command manquante", at)
		} else if p := n.Command[0]; !filepath.IsAbs(p) && strings.ContainsAny(p, `/\`) {
			add("%s : %q : chemin absolu ou simple nom de plugin attendu", at, p)
		}
		if n.Interval.D() < 10*time.Second || n.Interval.D() > 24*time.Hour {
			add("%s : interval %s hors de 10s à 24h", at, n.Interval.D())
		}
		if n.Timeout.D() < time.Second || n.Timeout.D() > 5*time.Minute || n.Timeout.D() > n.Interval.D() {
			add("%s : timeout %s hors de 1s à 5m (et au plus interval)", at, n.Timeout.D())
		}
		for k := range n.Tags {
			if !tagKeyRe.MatchString(k) {
				add("%s : étiquette %q : clé hors de [a-z0-9_.-/]", at, k)
			}
		}
		for k := range n.Env {
			if k == "" || strings.ContainsAny(k, "=\x00") || strings.HasPrefix(strings.ToUpper(k), "HYPHA_") {
				add("%s : variable d'environnement %q refusée", at, k)
			}
		}
	}

	s := c.Syslog
	if !s.Enabled {
		return
	}
	if s.UDP == "" && s.TCP == "" {
		add("syslog : udp et tcp vides, rien à écouter")
	}
	for proto, addr := range map[string]string{"udp": s.UDP, "tcp": s.TCP} {
		if addr == "" {
			continue
		}
		host, port, err := net.SplitHostPort(addr)
		p, perr := strconv.Atoi(port)
		switch {
		case err != nil || perr != nil || p < 1 || p > 65535:
			add("syslog.%s %q attendu sous la forme adresse:port", proto, addr)
		case p == 80:
			add("syslog.%s : le port 80 est interdit", proto)
		case host != "" && net.ParseIP(host) == nil:
			add("syslog.%s : adresse IP attendue, pas %q", proto, host)
		case !isLoopback(host):
			warn("syslog.%s %q : ouvert au réseau ; le pare-feu de l'hôte reste à régler (l'agent n'y touche pas)", proto, addr)
		}
	}
	for _, a := range s.AllowedSources {
		if _, _, err := net.ParseCIDR(a); err != nil && net.ParseIP(a) == nil {
			add("syslog.allowed_sources : %q n'est ni une adresse ni un réseau CIDR", a)
		}
	}
	if s.MaxConnections < 1 || s.MaxConnections > 1000 {
		add("syslog.max_connections : %d hors de 1 à 1000", s.MaxConnections)
	}
	if s.RateLimit < 1 || s.RateLimit > 100000 {
		add("syslog.rate_limit : %d hors de 1 à 100000", s.RateLimit)
	}
	for i, p := range s.Parsers {
		if !syslogParserRe.MatchString(p.Parser) {
			add("syslog.parsers[%d] : parser %q invalide (a-z 0-9 _ -)", i, p.Parser)
		}
		if p.Match == "" {
			add("syslog.parsers[%d] : match manquant", i)
		} else if _, _, err := net.ParseCIDR(p.Match); err != nil && net.ParseIP(p.Match) == nil {
			if _, err := path.Match(p.Match, ""); err != nil {
				add("syslog.parsers[%d] : match %q : ni adresse, ni réseau, ni motif valide", i, p.Match)
			}
		}
	}
	for k := range s.Tags {
		if !tagKeyRe.MatchString(k) {
			add("syslog.tags : clé %q hors de [a-z0-9_.-/]", k)
		}
	}
	if !c.Logs.Enabled {
		warn("syslog actif mais logs.enabled faux : les lignes reçues ne partiront pas")
	}
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
