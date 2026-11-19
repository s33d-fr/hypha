package snmp

import (
	"context"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
	"s33d.fr/hypha/agent/internal/state"
)

// Routes d'envoi (contrat § 3), reprises ici pour éviter une dépendance au paquet sender.
const (
	routeHost    = "/api/v1/intake/host"
	routeMetrics = "/api/v1/intake/metrics"
	routeChecks  = "/api/v1/intake/checks"
	routeEvents  = "/api/v1/intake/events"
	hostEvery    = 5 * time.Minute
	seenFile     = "snmp_seen_up.json"
)

// Options : ce que le gestionnaire demande à l'agent.
type Options struct {
	AgentID      string
	AgentVersion string
	Hostname     string            // hôte de l'agent (snmp.devices.unreachable, traps inconnus)
	Tags         map[string]string // étiquettes de la conf de l'agent
	Store        *state.Store
	Log          *slog.Logger
	Post         func(route string, fields map[string]any)
	SetStatus    func(state string)
}

// Manager interroge tous les équipements de la conf.
type Manager struct {
	cfg     config.SNMPConfig
	o       Options
	devices []*device
	seenMu  sync.Mutex
	seen    map[string][]string // adresse → interfaces déjà vues en service
	dirty   atomic.Bool
}

type device struct {
	cfg      config.SNMPDevice
	info     model.AgentInfo
	poller   *devicePoller
	prev     *sample
	seen     map[string]bool
	lastHost time.Time
	ident    identity
	up       atomic.Bool
	polled   atomic.Bool
}

// DeviceID : identité d'un équipement, UUID v5 (espace = id de l'agent, nom = snmp:<adresse>).
func DeviceID(agentID, address string) string {
	ns, err := uuid.Parse(agentID)
	if err != nil {
		ns = uuid.Nil
	}
	return uuid.NewSHA1(ns, []byte("snmp:"+address)).String()
}

// New prépare le gestionnaire.
func New(cfg config.SNMPConfig, o Options) *Manager {
	m := &Manager{cfg: cfg, o: o, seen: map[string][]string{}}
	_ = o.Store.ReadJSON(seenFile, &m.seen)
	for _, dc := range cfg.Devices {
		d := &device{
			cfg:    dc,
			info:   model.AgentInfo{ID: DeviceID(o.AgentID, dc.Address), Version: o.AgentVersion, Hostname: dc.Name, OS: "network", Via: o.AgentID},
			poller: &devicePoller{cfg: dc},
			seen:   map[string]bool{},
		}
		for _, n := range m.seen[dc.Address] {
			d.seen[n] = true
		}
		m.devices = append(m.devices, d)
	}
	return m
}

// Count rend le nombre d'équipements de la conf (capacités).
func (m *Manager) Count() int { return len(m.devices) }

func (m *Manager) tagsFor(d *device) model.TagFunc {
	return func(kv ...string) map[string]string {
		t := make(map[string]string, len(m.o.Tags)+len(d.cfg.Tags)+2+len(kv)/2)
		for k, v := range m.o.Tags {
			t[k] = v
		}
		for k, v := range d.cfg.Tags {
			t[k] = v
		}
		t["host"] = d.cfg.Name
		t["snmp_device"] = d.cfg.Address
		for i := 0; i+1 < len(kv); i += 2 {
			t[kv[i]] = kv[i+1]
		}
		return t
	}
}

func (m *Manager) agentTags(kv ...string) map[string]string {
	t := make(map[string]string, len(m.o.Tags)+1+len(kv)/2)
	for k, v := range m.o.Tags {
		t[k] = v
	}
	t["host"] = m.o.Hostname
	for i := 0; i+1 < len(kv); i += 2 {
		t[kv[i]] = kv[i+1]
	}
	return t
}

// Run interroge chaque équipement à son tour, à l'intervalle de la conf, jusqu'à l'arrêt.
func (m *Manager) Run(ctx context.Context) {
	if len(m.devices) == 0 {
		return
	}
	every := m.cfg.Interval.D()
	var wg sync.WaitGroup
	for i, d := range m.devices {
		wg.Add(1)
		go func(i int, d *device) {
			defer wg.Done()
			// Départs étalés sur l'intervalle : un seul équipement interrogé à la fois en général.
			first := every * time.Duration(i) / time.Duration(len(m.devices))
			t := time.NewTimer(first)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					d.close()
					return
				case now := <-t.C:
					m.pollOne(ctx, d, now)
					t.Reset(max(time.Second, every-time.Since(now)))
				}
			}
		}(i, d)
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			m.saveSeen()
			return
		case now := <-tick.C:
			m.summary(now)
		}
	}
}

// PollAll fait un relevé de chaque équipement (hypha-agent run --once).
func (m *Manager) PollAll(ctx context.Context) {
	now := time.Now()
	for _, d := range m.devices {
		m.pollOne(ctx, d, now)
		d.close()
	}
	m.summary(now)
}

func (d *device) close() {
	if d.poller.cl != nil {
		d.poller.cl.Close()
		d.poller.cl = nil
	}
}

// summary : nombre d'équipements injoignables, sur l'hôte de l'agent ; état du collecteur.
func (m *Manager) summary(now time.Time) {
	down, polled := 0, 0
	for _, d := range m.devices {
		if d.polled.Load() {
			polled++
			if !d.up.Load() {
				down++
			}
		}
	}
	if polled == 0 {
		return
	}
	b := model.NewSeriesBuilder(model.Ms(now), m.agentTags)
	b.Gauge("snmp.devices.unreachable", "count", float64(down))
	m.o.Post(routeMetrics, map[string]any{"series": b.Series()})
	if down == 0 {
		m.o.SetStatus("ok")
	} else {
		m.o.SetStatus("partiel : " + strconv.Itoa(down) + " équipement(s) injoignable(s)")
	}
	if m.dirty.Swap(false) {
		m.saveSeen()
	}
}

func (m *Manager) pollOne(ctx context.Context, d *device, now time.Time) {
	defer func() {
		if p := recover(); p != nil {
			d.close()
			m.o.Log.Error("relevé SNMP en panique, abandonné", "équipement", d.cfg.Name, "err", p)
		}
	}()
	tags := m.tagsFor(d)
	ts := model.Ms(now)
	start := time.Now()
	var s *sample
	var err error
	if d.poller.cl == nil {
		var c *gsClient
		if c, err = newClient(d.cfg); err == nil {
			c.g.Context = ctx
			d.poller.cl = c
		}
	}
	if err == nil {
		s, err = d.poller.poll(now)
	}
	dur := time.Since(start)
	d.polled.Store(true)
	agent := d.info
	if err != nil {
		d.close()
		wasUp := d.up.Swap(false)
		if wasUp || d.lastHost.IsZero() {
			m.postHost(d, nil, now, "erreur : "+err.Error())
		}
		m.o.Post(routeChecks, map[string]any{"agent": agent, "checks": []model.Check{{TS: ts, Name: "snmp.can_poll",
			Status: model.CheckCrit, Message: "équipement injoignable : " + err.Error(), Tags: tags()}}})
		if wasUp {
			m.o.Log.Warn("équipement SNMP injoignable", "équipement", d.cfg.Name, "adresse", d.cfg.Address, "err", err)
		}
		return
	}
	d.up.Store(true)
	b := model.NewSeriesBuilder(ts, tags)
	sampleSeries(b, s, d.prev, d.cfg.InterfacesExclude)
	b.Gauge("snmp.poll.duration", "millisecond", float64(dur.Microseconds())/1000)
	d.prev = s
	before := len(d.seen)
	checks := []model.Check{{TS: ts, Name: "snmp.can_poll", Status: model.CheckOK,
		Message: "relevé en " + strconv.Itoa(int(dur.Milliseconds())) + " ms (profil " + s.profile + ")", Tags: tags()}}
	checks = append(checks, interfaceChecks(s, ts, tags, d.cfg.InterfacesExclude, d.cfg.InterfaceDown == "always", d.seen)...)
	checks = append(checks, healthChecks(s, ts, tags)...)
	if len(d.seen) != before {
		m.rememberSeen(d)
	}
	m.o.Post(routeMetrics, map[string]any{"agent": agent, "series": b.Series()})
	m.o.Post(routeChecks, map[string]any{"agent": agent, "checks": checks})
	if now.Sub(d.lastHost) >= hostEvery || s.ident != d.ident {
		d.ident = s.ident
		state := "ok"
		if len(s.warnings) > 0 {
			state = "partiel : " + s.warnings[0]
		}
		m.postHost(d, s, now, state)
	}
}

func (m *Manager) rememberSeen(d *device) {
	names := make([]string, 0, len(d.seen))
	for n := range d.seen {
		names = append(names, n)
	}
	m.seenMu.Lock()
	m.seen[d.cfg.Address] = names
	m.seenMu.Unlock()
	m.dirty.Store(true)
}

func (m *Manager) saveSeen() {
	m.seenMu.Lock()
	defer m.seenMu.Unlock()
	if err := m.o.Store.WriteJSON(seenFile, m.seen); err != nil {
		m.o.Log.Warn("interfaces SNMP vues non écrites", "err", err)
	}
}

// deviceHost : corps /intake/host d'un équipement (kind network_device, fiche device).
func (m *Manager) deviceHost(d *device, s *sample, now time.Time, state string) model.Host {
	tags := map[string]string{}
	for k, v := range m.tagsFor(d)() {
		if k != "host" {
			tags[k] = v
		}
	}
	h := model.Host{
		Hostname: d.cfg.Name, OS: "network", Arch: "", AgentVersion: m.o.AgentVersion, Tags: tags,
		IPAddresses: []string{}, Kind: "network_device",
		Meta:   map[string]string{"snmp_version": string(d.cfg.Version), "snmp_device": d.cfg.Address},
		Status: model.HostStatus{Collectors: map[string]string{"snmp": state}, Errors: []string{}},
		Device: &model.Device{Address: d.cfg.Address},
	}
	if ip := net.ParseIP(d.cfg.Address); ip != nil {
		h.IPAddresses = []string{ip.String()}
	}
	if s == nil {
		return h
	}
	vendor := vendorOf(s.sys.objectID)
	switch s.profile {
	case ProfileSynology:
		vendor = "Synology"
	case ProfileAsustor:
		vendor = "ASUSTOR"
	}
	h.Platform, h.PlatformVersion = vendor, s.ident.firmware
	h.Meta["profile"] = s.profile
	if s.sys.upTime > 0 {
		h.BootTime = now.Add(-time.Duration(s.sys.upTime * float64(time.Second))).UnixMilli()
	}
	n := 0
	for _, x := range s.ifaces {
		if x.typ != 24 {
			n++
		}
	}
	h.Device = &model.Device{
		Address: d.cfg.Address, SysName: s.sys.name, SysDescr: model.Truncate(s.sys.descr, 1024),
		SysObjectID: s.sys.objectID, Vendor: vendor, Model: s.ident.model, Serial: s.ident.serial,
		Firmware: s.ident.firmware, Location: s.sys.location, Contact: s.sys.contact, Interfaces: n,
	}
	return h
}

func (m *Manager) postHost(d *device, s *sample, now time.Time, state string) {
	d.lastHost = now
	m.o.Post(routeHost, map[string]any{"agent": d.info, "host": m.deviceHost(d, s, now, state)})
}

// deviceByIP rend l'équipement dont l'adresse (ou le nom résolu) est ip.
func (m *Manager) deviceByIP(ip string) *device {
	for _, d := range m.devices {
		if d.cfg.Address == ip {
			return d
		}
	}
	for _, d := range m.devices {
		if net.ParseIP(d.cfg.Address) != nil {
			continue
		}
		addrs, err := net.LookupHost(d.cfg.Address)
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if strings.EqualFold(a, ip) {
				return d
			}
		}
	}
	return nil
}
