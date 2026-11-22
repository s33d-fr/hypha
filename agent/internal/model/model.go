// Package model décrit les corps JSON échangés avec le serveur, champ pour champ
// (docs/CONTRAT.md § 3). Toute modification ici doit d'abord passer par le contrat.
package model

import (
	"math"
	"sort"
	"strconv"
	"time"
	"unicode/utf8"
)

// AgentInfo est l'objet "agent" présent dans chaque corps envoyé.
type AgentInfo struct {
	ID       string `json:"id"`
	Version  string `json:"version"`
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	// Via : pour un équipement SNMP (§ 14), id de l'agent qui l'interroge.
	Via string `json:"via,omitempty"`
}

// Host est l'inventaire de l'hôte (§ 3.1).
type Host struct {
	Hostname        string            `json:"hostname"`
	OS              string            `json:"os"`
	Platform        string            `json:"platform"`
	PlatformVersion string            `json:"platform_version"`
	Kernel          string            `json:"kernel"`
	Arch            string            `json:"arch"`
	CPUCores        int               `json:"cpu_cores"`
	CPUModel        string            `json:"cpu_model"`
	MemTotal        uint64            `json:"mem_total"`
	IPAddresses     []string          `json:"ip_addresses"`
	BootTime        int64             `json:"boot_time"`
	Tags            map[string]string `json:"tags"`
	AgentVersion    string            `json:"agent_version"`
	Meta            map[string]string `json:"meta"`
	Status          HostStatus        `json:"status"`

	// 0.4.0 : kind = "network_device" pour un équipement SNMP (§ 14), avec sa fiche ;
	// capacités de l'agent (§ 27) ; empreinte de la conf fusionnée, secrets masqués.
	Kind         string        `json:"kind,omitempty"`
	Device       *Device       `json:"device,omitempty"`
	Capabilities *Capabilities `json:"capabilities,omitempty"`
	ConfigHash   string        `json:"config_hash,omitempty"`
}

// HostStatus est l'état des collecteurs, rangé par le serveur dans agents.status ;
// update va dans agents.update_status (§ 27).
type HostStatus struct {
	Collectors map[string]string `json:"collectors"`
	Errors     []string          `json:"errors"`
	Update     *UpdateStatus     `json:"update,omitempty"`
}

// UpdateStatus : état de la mise à jour automatique (§ 27).
type UpdateStatus struct {
	State string `json:"state"` // idle, downloading, verifying, installing, failed
	From  string `json:"from,omitempty"`
	To    string `json:"to,omitempty"`
	Error string `json:"error,omitempty"`
	TS    int64  `json:"ts"`
}

// Capabilities : ce que l'agent accepte de faire (§ 27).
type Capabilities struct {
	Actions      ActionCapabilities `json:"actions"`
	Synthetics   bool               `json:"synthetics"`
	AutoUpdate   bool               `json:"auto_update"`
	SNMPDevices  int                `json:"snmp_devices"`
	Integrations []string           `json:"integrations"`
}

// ActionCapabilities : gestes admis et scripts nommés (§ 28). Services et DenyServices
// (motifs de actions.services et actions.deny_services, service de l'agent compris dans le
// refus) : le serveur ne propose que les gestes sur un service que l'agent acceptera.
type ActionCapabilities struct {
	Enabled      bool     `json:"enabled"`
	Kinds        []string `json:"kinds"`
	Scripts      []string `json:"scripts"`
	Services     []string `json:"services"`
	DenyServices []string `json:"deny_services"`
}

// Device : fiche d'un équipement réseau (§ 14, host.device).
type Device struct {
	Address     string `json:"address"`
	SysName     string `json:"sys_name"`
	SysDescr    string `json:"sys_descr"`
	SysObjectID string `json:"sys_object_id"`
	Vendor      string `json:"vendor"`
	Model       string `json:"model"`
	Serial      string `json:"serial"`
	Firmware    string `json:"firmware"`
	Location    string `json:"location"`
	Contact     string `json:"contact"`
	Interfaces  int    `json:"interfaces"`

	// § 34-35 : ressource lue par un connecteur (kind « cloud_resource »).
	Provider    string `json:"provider,omitempty"`
	Type        string `json:"type,omitempty"`
	Service     string `json:"service,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	IP          string `json:"ip,omitempty"`
	Region      string `json:"region,omitempty"`
	Parent      string `json:"parent,omitempty"`
	PowerState  string `json:"power_state,omitempty"`
}

// Point est un couple [horodatage en ms, valeur], écrit en tableau JSON.
type Point struct {
	TS    int64
	Value float64
}

// MarshalJSON écrit [ts,valeur] sans notation exponentielle, pour rester lisible
// et exact sur les grands entiers (octets).
func (p Point) MarshalJSON() ([]byte, error) {
	b := make([]byte, 0, 32)
	b = append(b, '[')
	b = strconv.AppendInt(b, p.TS, 10)
	b = append(b, ',')
	b = strconv.AppendFloat(b, p.Value, 'f', -1, 64)
	b = append(b, ']')
	return b, nil
}

// Series est une série de métriques (§ 3.2).
type Series struct {
	Metric string            `json:"metric"`
	Type   string            `json:"type"`
	Unit   string            `json:"unit,omitempty"`
	Tags   map[string]string `json:"tags"`
	Points []Point           `json:"points"`
}

// Types de séries admis par le contrat.
const (
	TypeGauge        = "gauge"
	TypeCount        = "count"
	TypeRate         = "rate"
	TypeDistribution = "distribution"
)

// Log est une ligne de journal (§ 3.3).
type Log struct {
	TS         int64             `json:"ts"`
	Message    string            `json:"message"`
	Status     string            `json:"status"`
	Service    string            `json:"service"`
	Source     string            `json:"source"`
	Attributes map[string]any    `json:"attributes"`
	Tags       map[string]string `json:"tags"`
	TraceID    *string           `json:"trace_id"`
	SpanID     *string           `json:"span_id"`
}

// Check est le résultat d'un contrôle de service (§ 3.4).
type Check struct {
	TS      int64             `json:"ts"`
	Name    string            `json:"name"`
	Status  int               `json:"status"`
	Message string            `json:"message"`
	Tags    map[string]string `json:"tags"`
}

// Statuts des contrôles.
const (
	CheckOK      = 0
	CheckWarn    = 1
	CheckCrit    = 2
	CheckUnknown = 3
)

// Event est un événement (§ 3.5).
type Event struct {
	TS             int64             `json:"ts"`
	Source         string            `json:"source"`
	Type           string            `json:"type"`
	Title          string            `json:"title"`
	Text           string            `json:"text"`
	AlertType      string            `json:"alert_type"`
	Priority       string            `json:"priority"`
	Tags           map[string]string `json:"tags"`
	AggregationKey string            `json:"aggregation_key"`
}

// Process est une ligne de l'instantané des processus (§ 3.6).
type Process struct {
	PID        int32   `json:"pid"`
	PPID       int32   `json:"ppid"`
	Name       string  `json:"name"`
	User       string  `json:"user"`
	Cmdline    string  `json:"cmdline"`
	State      string  `json:"state"`
	CPUPct     float64 `json:"cpu_pct"`
	MemRSS     uint64  `json:"mem_rss"`
	MemPct     float64 `json:"mem_pct"`
	Threads    int32   `json:"threads"`
	Started    int64   `json:"started"`
	IOReadBps  uint64  `json:"io_read_bps"`
	IOWriteBps uint64  `json:"io_write_bps"`
}

// Connection est un groupe de connexions réseau identiques (§ 3.7).
type Connection struct {
	Proto     string `json:"proto"`
	Direction string `json:"direction"`
	Laddr     string `json:"laddr"`
	Lport     uint32 `json:"lport"`
	Raddr     string `json:"raddr"`
	Rport     uint32 `json:"rport"`
	State     string `json:"state"`
	PID       int32  `json:"pid"`
	Process   string `json:"process"`
	Count     int    `json:"count"`
}

// Container est une ligne de l'instantané des conteneurs (§ 3.8).
type Container struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Image    string            `json:"image"`
	State    string            `json:"state"`
	CPUPct   float64           `json:"cpu_pct"`
	MemUsage uint64            `json:"mem_usage"`
	MemLimit uint64            `json:"mem_limit"`
	NetRxBps uint64            `json:"net_rx_bps"`
	NetTxBps uint64            `json:"net_tx_bps"`
	Labels   map[string]string `json:"labels"`
}

// TagFunc rend les étiquettes communes (conf + host) augmentées de paires clé, valeur.
type TagFunc func(kv ...string) map[string]string

// Ms convertit une date en millisecondes Unix.
func Ms(t time.Time) int64 { return t.UnixMilli() }

// MaxTagValue est la longueur maximale d'une valeur d'étiquette (§ 2).
const MaxTagValue = 200

// Truncate coupe une chaîne à n octets sans casser un caractère UTF-8.
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// SeriesBuilder accumule des séries d'un même cycle, au même horodatage.
type SeriesBuilder struct {
	ts   int64
	tags TagFunc
	out  []Series
	root *SeriesBuilder // With : séries versées dans ce constructeur
}

// NewSeriesBuilder prépare un cycle de collecte.
func NewSeriesBuilder(ts int64, tags TagFunc) *SeriesBuilder {
	return &SeriesBuilder{ts: ts, tags: tags}
}

// Gauge ajoute une valeur instantanée (ou un débit déjà calculé).
func (b *SeriesBuilder) Gauge(name, unit string, v float64, kv ...string) {
	b.Add(name, TypeGauge, unit, v, kv...)
}

// Add ajoute une série d'un type donné ; les valeurs non finies sont ignorées
// (elles casseraient le JSON).
func (b *SeriesBuilder) Add(name, typ, unit string, v float64, kv ...string) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return
	}
	for i := 1; i < len(kv); i += 2 {
		kv[i] = Truncate(kv[i], MaxTagValue)
	}
	s := Series{
		Metric: name,
		Type:   typ,
		Unit:   unit,
		Tags:   b.tags(kv...),
		Points: []Point{{TS: b.ts, Value: v}},
	}
	if b.root != nil {
		b.root.out = append(b.root.out, s)
		return
	}
	b.out = append(b.out, s)
}

// With rend un constructeur au même horodatage, aux étiquettes de tags, qui verse ses séries
// dans celui-ci (instances d'une intégration, § 50).
func (b *SeriesBuilder) With(tags TagFunc) *SeriesBuilder {
	root := b
	if b.root != nil {
		root = b.root
	}
	return &SeriesBuilder{ts: b.ts, tags: tags, root: root}
}

// Series rend les séries accumulées.
func (b *SeriesBuilder) Series() []Series { return b.out }

// SortedKeys rend les clés d'une table, triées (sortie stable pour les journaux et tests).
func SortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
