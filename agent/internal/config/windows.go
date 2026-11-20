package config

import (
	"fmt"
	"net"
	"path"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Sondes Windows (vague 3 des intégrations) :
//   - logs.windows_eventlog : canaux nommés, en texte (« System ») ou en objet avec filtres par
//     niveau, source (fournisseur) et identifiant d'événement, service et étiquettes propres ;
//   - integrations.perf_counters : compteurs de performance (PDH) choisis, devenus métriques ;
//   - integrations.wmi : requêtes WMI génériques (classe, propriétés, filtre), devenues métriques ;
//   - integrations.active_directory et integrations.windows_dns : rôles AD DS et DNS de Windows
//     Server, avec leurs journaux (Directory Service, DFS Replication, DNS Server) ajoutés d'office.
// Les listes perf_counters et wmi de plusieurs fichiers conf.d s'additionnent (merge.go).

// EventlogChannel : un canal des journaux d'événements Windows.
type EventlogChannel struct {
	Channel string `yaml:"channel"`
	// Levels : niveaux retenus (critical, error, warning, information, verbose, audit_success,
	// audit_failure) ; vide = tous.
	Levels []string `yaml:"levels"`
	// EventIDs : identifiants retenus (« 4625 » ou plage « 4624-4634 ») ; vide = tous.
	EventIDs        []string `yaml:"event_ids"`
	ExcludeEventIDs []string `yaml:"exclude_event_ids"`
	// Providers : sources (noms de fournisseurs, sans casse, motif glob permis) ; vide = toutes.
	Providers        []string          `yaml:"providers"`
	ExcludeProviders []string          `yaml:"exclude_providers"`
	Service          string            `yaml:"service"` // remplace le fournisseur comme service
	Tags             map[string]string `yaml:"tags"`

	unknown []string // clés inconnues, signalées par Validate (pas de refus de démarrer)
}

var eventlogKeys = map[string]bool{"channel": true, "levels": true, "event_ids": true, "exclude_event_ids": true,
	"providers": true, "exclude_providers": true, "service": true, "tags": true}

// UnmarshalYAML accepte un nom de canal seul ou un objet.
func (e *EventlogChannel) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		*e = EventlogChannel{Channel: strings.TrimSpace(n.Value)}
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("ligne %d : canal attendu en texte (System) ou en objet (channel: …)", n.Line)
	}
	type plain EventlogChannel
	var p plain
	if err := n.Decode(&p); err != nil {
		return err
	}
	*e = EventlogChannel(p)
	for i := 0; i+1 < len(n.Content); i += 2 {
		if k := n.Content[i].Value; !eventlogKeys[k] {
			e.unknown = append(e.unknown, k)
		}
	}
	return nil
}

// MarshalYAML écrit un canal sans filtre en texte (check-config lisible).
func (e EventlogChannel) MarshalYAML() (any, error) {
	if e.Plain() {
		return e.Channel, nil
	}
	type plain EventlogChannel
	return plain(e), nil
}

// Plain dit si le canal n'a aucun filtre ni réglage.
func (e EventlogChannel) Plain() bool {
	return len(e.Levels)+len(e.EventIDs)+len(e.ExcludeEventIDs)+len(e.Providers)+len(e.ExcludeProviders)+len(e.Tags) == 0 &&
		e.Service == ""
}

// EventlogLevels : niveaux admis dans levels (information et verbose ont pour alias info et debug).
var EventlogLevels = []string{"critical", "error", "warning", "information", "verbose", "audit_success", "audit_failure"}

// NormalizeEventlogLevel rend le nom canonique d'un niveau, ou "" s'il est inconnu.
func NormalizeEventlogLevel(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "info":
		return "information"
	case "debug":
		return "verbose"
	case "audit-success":
		return "audit_success"
	case "audit-failure":
		return "audit_failure"
	}
	for _, l := range EventlogLevels {
		if s == l {
			return s
		}
	}
	return ""
}

// IDRange : plage d'identifiants d'événement, bornes comprises.
type IDRange struct{ Lo, Hi uint32 }

// ParseEventIDs lit « 4625 » ou « 4624-4634 » (0 à 65535).
func ParseEventIDs(list []string) ([]IDRange, error) {
	var out []IDRange
	for _, s := range list {
		s = strings.TrimSpace(s)
		lo, hi, isRange := strings.Cut(s, "-")
		a, err1 := strconv.ParseUint(strings.TrimSpace(lo), 10, 16)
		b := a
		var err2 error
		if isRange {
			b, err2 = strconv.ParseUint(strings.TrimSpace(hi), 10, 16)
		}
		if err1 != nil || err2 != nil || b < a {
			return nil, fmt.Errorf("identifiant %q : nombre de 0 à 65535, ou plage « début-fin »", s)
		}
		out = append(out, IDRange{uint32(a), uint32(b)})
	}
	return out, nil
}

// PerfCounterSet : un jeu de compteurs de performance relevés ensemble (étiquette instance_name).
type PerfCounterSet struct {
	Name      string            `yaml:"name"`
	Namespace string            `yaml:"namespace"` // préfixe des métriques ; par défaut le nom du jeu
	Interval  Duration          `yaml:"interval"`
	Timeout   Duration          `yaml:"timeout"`
	MaxSeries int               `yaml:"max_series"`
	Counters  []PerfCounter     `yaml:"counters"`
	Tags      map[string]string `yaml:"tags"`
}

// PerfCounter : un compteur, nommé en anglais (\Objet(instance)\Compteur), quelle que soit la
// langue de Windows ; (*) relève toutes les instances.
type PerfCounter struct {
	Path             string   `yaml:"path"`
	Metric           string   `yaml:"metric"` // sous l'espace de noms ; par défaut tiré du compteur
	Unit             string   `yaml:"unit"`
	InstanceTag      string   `yaml:"instance_tag"`      // étiquette des instances de (*) ; « instance »
	Instances        []string `yaml:"instances"`         // motifs glob retenus ; vide = toutes
	ExcludeInstances []string `yaml:"exclude_instances"` // motifs glob écartés (_Total…)
}

// WMIQuery : une requête WMI (SELECT propriétés FROM classe WHERE filtre), en lecture seule.
type WMIQuery struct {
	Name         string            `yaml:"name"`
	Namespace    string            `yaml:"namespace"`     // préfixe des métriques ; par défaut le nom
	WMINamespace string            `yaml:"wmi_namespace"` // espace WMI ; root\cimv2 par défaut
	Class        string            `yaml:"class"`
	Properties   []WMIProperty     `yaml:"properties"`
	Filter       string            `yaml:"filter"` // clause WHERE, sans le mot WHERE
	TagBy        []string          `yaml:"tag_by"` // propriétés texte devenues étiquettes
	Interval     Duration          `yaml:"interval"`
	Timeout      Duration          `yaml:"timeout"`
	MaxSeries    int               `yaml:"max_series"`
	Tags         map[string]string `yaml:"tags"`
}

// WMIProperty : propriété numérique devenue métrique ; en texte (« FreePhysicalMemory ») ou en
// objet (property, metric, unit).
type WMIProperty struct {
	Property string `yaml:"property"`
	Metric   string `yaml:"metric"`
	Unit     string `yaml:"unit"`
}

// UnmarshalYAML accepte un nom de propriété seul ou un objet.
func (p *WMIProperty) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		*p = WMIProperty{Property: strings.TrimSpace(n.Value)}
		return nil
	}
	type plain WMIProperty
	var v plain
	if err := n.Decode(&v); err != nil {
		return err
	}
	*p = WMIProperty(v)
	return nil
}

// ActiveDirectoryConfig : contrôleur de domaine (service NTDS présent).
type ActiveDirectoryConfig struct {
	Enabled Tristate `yaml:"enabled"`
	// Logs : canaux Directory Service et DFS Replication ajoutés à la collecte (oui par défaut).
	Logs *bool `yaml:"logs"`
}

// LogsOn dit si les journaux du rôle sont collectés.
func (a ActiveDirectoryConfig) LogsOn() bool {
	return a.Enabled != "false" && (a.Logs == nil || *a.Logs)
}

// WindowsDNSConfig : serveur DNS de Windows (service DNS présent).
type WindowsDNSConfig struct {
	Enabled Tristate `yaml:"enabled"`
	Logs    *bool    `yaml:"logs"` // canal DNS Server ajouté à la collecte (oui par défaut)
	// ExcludeZones : zones (motifs glob, sans casse) ni contrôlées ni mesurées.
	ExcludeZones []string `yaml:"exclude_zones"`
	// TestZone : zone dont le SOA est demandé au serveur local à chaque relevé (preuve qu'il
	// répond) ; par défaut la première zone principale.
	TestZone string `yaml:"test_zone"`
	// Address : adresse:port interrogée (127.0.0.1:53 par défaut ; l'adresse du serveur s'il
	// n'écoute pas sur la boucle locale).
	Address string `yaml:"address"`
}

// LogsOn dit si le journal du rôle est collecté.
func (d WindowsDNSConfig) LogsOn() bool { return d.Enabled != "false" && (d.Logs == nil || *d.Logs) }

const (
	defaultPerfInterval = 30 * time.Second
	defaultWMIInterval  = 60 * time.Second
	defaultProbeTimeout = 10 * time.Second
	defaultProbeSeries  = 500
	maxProbeSeries      = 10000
)

func defaultsWindows(c *Config) {
	c.Integrations.ActiveDirectory = ActiveDirectoryConfig{Enabled: "auto"}
	c.Integrations.WindowsDNS = WindowsDNSConfig{Enabled: "auto", Address: "127.0.0.1:53"}
}

func (c *Config) normalizeWindows() {
	for i := range c.Logs.WindowsEventlog {
		e := &c.Logs.WindowsEventlog[i]
		e.Channel = strings.TrimSpace(e.Channel)
		e.Service = strings.TrimSpace(e.Service)
		for j, l := range e.Levels {
			if n := NormalizeEventlogLevel(l); n != "" {
				e.Levels[j] = n
			}
		}
	}
	for i := range c.Integrations.PerfCounters {
		p := &c.Integrations.PerfCounters[i]
		p.Name = strings.TrimSpace(p.Name)
		if p.Namespace == "" {
			p.Namespace = strings.ReplaceAll(p.Name, "-", "_")
		}
		if p.Interval == 0 {
			p.Interval = Duration(defaultPerfInterval)
		}
		if p.Timeout == 0 {
			p.Timeout = Duration(min(defaultProbeTimeout, p.Interval.D()))
		}
		if p.MaxSeries == 0 {
			p.MaxSeries = defaultProbeSeries
		}
		for j := range p.Counters {
			k := &p.Counters[j]
			k.Path = strings.TrimSpace(k.Path)
			if k.Metric == "" {
				_, _, counter, _ := SplitCounterPath(k.Path)
				k.Metric = MetricName(counter)
			}
			if k.InstanceTag == "" {
				k.InstanceTag = "instance"
			}
		}
	}
	for i := range c.Integrations.WMI {
		w := &c.Integrations.WMI[i]
		w.Name = strings.TrimSpace(w.Name)
		w.Class = strings.TrimSpace(w.Class)
		w.WMINamespace = strings.ReplaceAll(strings.TrimSpace(w.WMINamespace), "/", `\`)
		if w.WMINamespace == "" {
			w.WMINamespace = `root\cimv2`
		}
		if w.Namespace == "" {
			w.Namespace = strings.ReplaceAll(w.Name, "-", "_")
		}
		if w.Interval == 0 {
			w.Interval = Duration(defaultWMIInterval)
		}
		if w.Timeout == 0 {
			w.Timeout = Duration(min(defaultProbeTimeout, w.Interval.D()))
		}
		if w.MaxSeries == 0 {
			w.MaxSeries = defaultProbeSeries
		}
		for j := range w.Properties {
			p := &w.Properties[j]
			if p.Metric == "" {
				p.Metric = MetricName(p.Property)
			}
		}
	}
}

// SplitCounterPath découpe \Objet(instance)\Compteur ; instance vide sans parenthèses.
func SplitCounterPath(p string) (object, instance, counter string, ok bool) {
	if !strings.HasPrefix(p, `\`) || strings.HasPrefix(p, `\\`) {
		return "", "", "", false
	}
	rest := p[1:]
	i := strings.LastIndex(rest, `\`)
	if i <= 0 || i == len(rest)-1 {
		return "", "", "", false
	}
	object, counter = rest[:i], rest[i+1:]
	if j := strings.Index(object, "("); j >= 0 {
		if !strings.HasSuffix(object, ")") {
			return "", "", "", false
		}
		object, instance = object[:j], object[j+1:len(object)-1]
		if instance == "" {
			return "", "", "", false
		}
	}
	return object, instance, counter, object != ""
}

var (
	camelRe      = regexp.MustCompile(`([a-z0-9])([A-Z])`)
	acronymRe    = regexp.MustCompile(`([A-Z]+)([A-Z][a-z])`)
	nonMetricRe  = regexp.MustCompile(`[^a-z0-9]+`)
	probeMetric  = regexp.MustCompile(`^[a-z0-9_][a-z0-9_.]{0,99}$`)
	probeUnitRe  = regexp.MustCompile(`^[a-z_]{0,40}$`)
	wmiNameRe    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	wmiNSRe      = regexp.MustCompile(`^[A-Za-z0-9_]+(\\[A-Za-z0-9_]+)*$`)
	evtServiceRe = regexp.MustCompile(`^[A-Za-z0-9@._:\-]{1,200}$`)
)

// MetricName tire un nom de métrique d'un nom de compteur ou de propriété :
// « % Processor Time » → processor_time_pct, « Total Query Received/sec » →
// total_query_received_per_s, « FreePhysicalMemory » → free_physical_memory.
func MetricName(s string) string {
	pct := strings.Contains(s, "%")
	s = strings.ReplaceAll(s, "%", " ")
	s = strings.ReplaceAll(s, "/sec", " per_s")
	s = acronymRe.ReplaceAllString(s, "${1}_${2}")
	s = camelRe.ReplaceAllString(s, "${1}_${2}")
	s = strings.Trim(nonMetricRe.ReplaceAllString(strings.ToLower(s), "_"), "_")
	if s == "" {
		s = "value"
	}
	if pct {
		s += "_pct"
	}
	return s
}

func (c *Config) validateWindows(add, warn func(string, ...any)) {
	seen := map[string]bool{}
	for i, e := range c.Logs.WindowsEventlog {
		at := fmt.Sprintf("logs.windows_eventlog[%d]", i)
		if e.Channel != "" {
			at += " (" + e.Channel + ")"
		}
		key := strings.ToLower(e.Channel)
		switch {
		case e.Channel == "":
			add("%s : channel manquant", at)
		case strings.ContainsAny(e.Channel, "\"'<>&*"):
			add("%s : nom de canal invalide", at)
		case seen[key]:
			add("%s : canal en double (regrouper ses filtres dans une seule entrée)", at)
		}
		seen[key] = true
		for _, k := range e.unknown {
			warn("%s : clé inconnue ignorée : %s", at, k)
		}
		for _, l := range e.Levels {
			if NormalizeEventlogLevel(l) == "" {
				add("%s : niveau %q inconnu (%s)", at, l, strings.Join(EventlogLevels, ", "))
			}
		}
		for _, list := range [][]string{e.EventIDs, e.ExcludeEventIDs} {
			if _, err := ParseEventIDs(list); err != nil {
				add("%s : %v", at, err)
			}
		}
		for _, p := range append(append([]string{}, e.Providers...), e.ExcludeProviders...) {
			if _, err := path.Match(strings.ToLower(p), ""); err != nil || strings.TrimSpace(p) == "" {
				add("%s : source %q invalide", at, p)
			}
		}
		if e.Service != "" && !evtServiceRe.MatchString(e.Service) {
			add("%s : service %q invalide (lettres, chiffres, @ . _ : -)", at, e.Service)
		}
		for k := range e.Tags {
			if !tagKeyRe.MatchString(k) {
				add("%s : étiquette %q : clé hors de [a-z0-9_.-/]", at, k)
			}
		}
	}

	notWindows := runtime.GOOS != "windows"
	names := map[string]bool{}
	for i, p := range c.Integrations.PerfCounters {
		at := fmt.Sprintf("integrations.perf_counters[%d]", i)
		if p.Name != "" {
			at += " (" + p.Name + ")"
		}
		switch {
		case p.Name == "":
			add("%s : name manquant", at)
		case !instanceNameRe.MatchString(p.Name):
			add("%s : name invalide (minuscules, chiffres, - et _, 32 au plus)", at)
		case names[p.Name]:
			add("%s : name en double", at)
		}
		names[p.Name] = true
		c.validateProbeCommon(at, p.Namespace, p.Interval, p.Timeout, p.MaxSeries, p.Tags, 10*time.Second, add)
		if len(p.Counters) == 0 {
			add("%s : counters vide", at)
		}
		metrics := map[string]bool{}
		for j, k := range p.Counters {
			kat := fmt.Sprintf("%s.counters[%d]", at, j)
			_, inst, _, ok := SplitCounterPath(k.Path)
			switch {
			case !ok:
				add(`%s : path %q attendu sous la forme \Objet\Compteur ou \Objet(instance)\Compteur (local, noms anglais)`, kat, k.Path)
			case strings.Contains(inst, "*") && inst != "*":
				add("%s : instance %q : (*) entier seulement ; filtrer par instances (motifs glob)", kat, inst)
			}
			if !probeMetric.MatchString(k.Metric) {
				add("%s : metric %q invalide (minuscules, chiffres, _ et .)", kat, k.Metric)
			}
			if metrics[k.Metric+"|"+k.Path] {
				add("%s : compteur en double", kat)
			}
			metrics[k.Metric+"|"+k.Path] = true
			if !probeUnitRe.MatchString(k.Unit) {
				add("%s : unit %q invalide", kat, k.Unit)
			}
			if !tagKeyRe.MatchString(k.InstanceTag) {
				add("%s : instance_tag %q invalide", kat, k.InstanceTag)
			}
			for _, g := range append(append([]string{}, k.Instances...), k.ExcludeInstances...) {
				if _, err := path.Match(g, ""); err != nil {
					add("%s : motif d'instance %q invalide", kat, g)
				}
			}
		}
	}
	if notWindows && len(c.Integrations.PerfCounters) > 0 {
		warn("integrations.perf_counters : compteurs de performance de Windows, ignorés sur cet hôte")
	}

	names = map[string]bool{}
	for i, w := range c.Integrations.WMI {
		at := fmt.Sprintf("integrations.wmi[%d]", i)
		if w.Name != "" {
			at += " (" + w.Name + ")"
		}
		switch {
		case w.Name == "":
			add("%s : name manquant", at)
		case !instanceNameRe.MatchString(w.Name):
			add("%s : name invalide (minuscules, chiffres, - et _, 32 au plus)", at)
		case names[w.Name]:
			add("%s : name en double", at)
		}
		names[w.Name] = true
		c.validateProbeCommon(at, w.Namespace, w.Interval, w.Timeout, w.MaxSeries, w.Tags, 15*time.Second, add)
		if !wmiNameRe.MatchString(w.Class) {
			add("%s : class %q invalide (ex. Win32_OperatingSystem)", at, w.Class)
		}
		if !wmiNSRe.MatchString(w.WMINamespace) {
			add(`%s : wmi_namespace %q invalide (ex. root\cimv2)`, at, w.WMINamespace)
		}
		if len(w.Properties) == 0 {
			add("%s : properties vide", at)
		}
		for j, p := range w.Properties {
			if !wmiNameRe.MatchString(p.Property) {
				add("%s.properties[%d] : propriété %q invalide", at, j, p.Property)
			}
			if !probeMetric.MatchString(p.Metric) {
				add("%s.properties[%d] : metric %q invalide", at, j, p.Metric)
			}
			if !probeUnitRe.MatchString(p.Unit) {
				add("%s.properties[%d] : unit %q invalide", at, j, p.Unit)
			}
		}
		for _, t := range w.TagBy {
			if !wmiNameRe.MatchString(t) {
				add("%s : tag_by : propriété %q invalide", at, t)
			}
		}
		if len(w.Filter) > 1000 || strings.ContainsAny(w.Filter, "\x00\r\n;") {
			add("%s : filter invalide (une clause WHERE sur une ligne, sans « ; », 1000 caractères au plus)", at)
		}
	}
	if notWindows && len(c.Integrations.WMI) > 0 {
		warn("integrations.wmi : requêtes WMI de Windows, ignorées sur cet hôte")
	}
	for _, z := range c.Integrations.WindowsDNS.ExcludeZones {
		if _, err := path.Match(strings.ToLower(z), ""); err != nil {
			add("integrations.windows_dns.exclude_zones : motif %q invalide", z)
		}
	}
	if z := c.Integrations.WindowsDNS.TestZone; z != "" && (len(z) > 253 || strings.ContainsAny(z, " \t/\\'\"")) {
		add("integrations.windows_dns.test_zone : %q n'est pas un nom de zone", z)
	}
	if a := c.Integrations.WindowsDNS.Address; a != "" {
		if _, port, err := net.SplitHostPort(a); err != nil || port == "" {
			add("integrations.windows_dns.address %q attendue sous la forme adresse:port", a)
		}
	}
}

func (c *Config) validateProbeCommon(at, ns string, interval, timeout Duration, maxSeries int, tags map[string]string,
	minInterval time.Duration, add func(string, ...any)) {
	if !omNamespaceRe.MatchString(ns) {
		add("%s : namespace invalide %q", at, ns)
	}
	if interval.D() < minInterval || interval.D() > time.Hour {
		add("%s : interval %s hors de %s à 1h", at, interval.D(), minInterval)
	}
	if timeout.D() < time.Second || timeout.D() > time.Minute || timeout.D() > interval.D() {
		add("%s : timeout %s hors de 1s à 1m (et au plus interval)", at, timeout.D())
	}
	if maxSeries < 1 || maxSeries > maxProbeSeries {
		add("%s : max_series %d hors de 1 à %d", at, maxSeries, maxProbeSeries)
	}
	for k := range tags {
		if !tagKeyRe.MatchString(k) {
			add("%s : étiquette %q : clé hors de [a-z0-9_.-/]", at, k)
		}
	}
}

// EventlogNames rend les noms des canaux, filtrés marqués d'un astérisque (check-config).
func EventlogNames(list []EventlogChannel) []string {
	out := make([]string, 0, len(list))
	for _, e := range list {
		n := e.Channel
		if !e.Plain() {
			n += "*"
		}
		out = append(out, n)
	}
	return out
}
