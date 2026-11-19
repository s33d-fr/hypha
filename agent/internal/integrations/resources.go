package integrations

import (
	"sync"
	"time"

	"github.com/google/uuid"

	"s33d.fr/hypha/agent/internal/model"
)

// ── Ressources de connecteurs (§ 34-35) ────────────────────────────────────────────────────
//
// Serveur dédié OVH, hôte ESXi, VM vSphere : publiés comme des hôtes sans agent (kind
// « cloud_resource »), rattachés à l'agent qui les interroge (`via`), comme un équipement SNMP.
// Leurs contrôles et mesures partent sous leur identité à chaque collecte ; leur fiche (hôte)
// toutes les 5 minutes.

const (
	routeHost         = "/api/v1/intake/host"
	routeMetrics      = "/api/v1/intake/metrics"
	routeChecks       = "/api/v1/intake/checks"
	resourceHostEvery = 5 * time.Minute
)

type cloudResource struct {
	key      string // identité stable, ex. « ovh:dedicated_server:ns1234567.ip-203-0-113.eu »
	name     string // nom d'hôte affiché
	device   model.Device
	tags     map[string]string // étiquettes propres à la ressource
	checks   []ovhCheck
	gauges   []ovhGauge
	ips      []string
	platform string
	version  string
	cpuCores int
	memTotal uint64
	bootTime int64
}

type resourcePublisher struct {
	d    Deps
	mu   sync.Mutex
	last map[string]time.Time
}

func newResourcePublisher(d Deps) *resourcePublisher {
	return &resourcePublisher{d: d, last: map[string]time.Time{}}
}

// resourceID : UUID v5 (espace = id de l'agent, nom = res:<clé>), stable d'un passage à l'autre.
func resourceID(agentID, key string) string {
	ns, err := uuid.Parse(agentID)
	if err != nil {
		ns = uuid.Nil
	}
	return uuid.NewSHA1(ns, []byte("res:"+key)).String()
}

func (p *resourcePublisher) tagsFor(r *cloudResource) model.TagFunc {
	return func(kv ...string) map[string]string {
		t := make(map[string]string, len(p.d.Tags)+len(r.tags)+1+len(kv)/2)
		for k, v := range p.d.Tags {
			if k != "role" { // le rôle décrit la machine qui interroge, pas la ressource
				t[k] = v
			}
		}
		for k, v := range r.tags {
			t[k] = v
		}
		t["host"] = r.name
		for i := 0; i+1 < len(kv); i += 2 {
			if kv[i+1] != "" {
				t[kv[i]] = kv[i+1]
			}
		}
		return t
	}
}

// publish : contrôles et mesures de chaque ressource (horodatés maintenant), fiche si elle est due.
func (p *resourcePublisher) publish(now time.Time, list []*cloudResource, collector, state string) {
	if p.d.Post == nil || p.d.AgentID == "" {
		return
	}
	ts := model.Ms(now)
	for _, r := range list {
		info := model.AgentInfo{ID: resourceID(p.d.AgentID, r.key), Version: p.d.AgentVersion, Hostname: r.name,
			OS: "cloud", Via: p.d.AgentID}
		tf := p.tagsFor(r)
		p.mu.Lock()
		due := now.Sub(p.last[r.key]) >= resourceHostEvery
		if due {
			p.last[r.key] = now
		}
		p.mu.Unlock()
		if due {
			tags := map[string]string{}
			for k, v := range tf() {
				if k != "host" {
					tags[k] = v
				}
			}
			dev := r.device
			ips := r.ips
			if ips == nil {
				ips = []string{}
			}
			h := model.Host{Hostname: r.name, OS: "cloud", Platform: r.platform, PlatformVersion: r.version,
				CPUCores: r.cpuCores, MemTotal: r.memTotal, IPAddresses: ips, BootTime: r.bootTime, Tags: tags,
				AgentVersion: p.d.AgentVersion, Meta: map[string]string{"connector": collector, "provider": r.device.Provider},
				Status: model.HostStatus{Collectors: map[string]string{collector: state}, Errors: []string{}},
				Kind:   "cloud_resource", Device: &dev}
			p.d.Post(routeHost, map[string]any{"agent": info, "host": h})
		}
		if len(r.gauges) > 0 {
			b := model.NewSeriesBuilder(ts, tf)
			for _, g := range r.gauges {
				b.Gauge(g.name, g.unit, g.v, g.kv...)
			}
			p.d.Post(routeMetrics, map[string]any{"agent": info, "series": b.Series()})
		}
		if len(r.checks) > 0 {
			checks := make([]model.Check, 0, len(r.checks))
			for _, c := range r.checks {
				checks = append(checks, model.Check{TS: ts, Name: c.name, Status: c.status, Message: c.msg, Tags: tf(c.kv...)})
			}
			p.d.Post(routeChecks, map[string]any{"agent": info, "checks": checks})
		}
	}
}
