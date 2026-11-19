package integrations

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/event"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/session"
	"github.com/vmware/govmomi/view"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/soap"
	"github.com/vmware/govmomi/vim25/types"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── vSphere (§ 35) : ESXi et vCenter par leur API, en lecture seule ─────────────────────────
//
// L'agent ne s'installe pas sur un ESXi : un agent du parc interroge l'API vSphere (rôle
// ReadOnly). Par cible : hôtes (état, alarmes, maintenance, CPU, mémoire), datastores
// (accessibilité, occupation), VM (alimentation, état, cœur du système invité, CPU, mémoire,
// ancienneté des instantanés) et événements de vSphere. Une tâche de fond par passage ; la
// collecte relit le dernier relevé, comme le connecteur OVHcloud.

const vsphereMaxEvents = 200

type vsTarget struct {
	cfg    config.VSphereTarget
	client *govmomi.Client
	since  time.Time // date du dernier événement relayé
	evErr  string    // dernière lecture des événements en échec
}

type vsSnapshot struct {
	at        time.Time
	checks    []ovhCheck
	resources []*cloudResource // hôtes ESXi et VM, publiés comme hôtes (§ 35)
	gauges []ovhGauge
	errs   []string
	took   time.Duration
}

type VSphere struct {
	cfg  config.VSphereConfig
	post func(route string, fields map[string]any)
	pub  *resourcePublisher

	once sync.Once
	stop context.CancelFunc

	mu      sync.Mutex
	tags    model.TagFunc
	targets []*vsTarget
	snap    vsSnapshot
}

func newVSphere(cfg config.VSphereConfig, d Deps) *VSphere {
	v := &VSphere{cfg: cfg, post: d.Post, pub: newResourcePublisher(d)}
	for _, t := range cfg.Targets {
		v.targets = append(v.targets, &vsTarget{cfg: t})
	}
	return v
}

func (v *VSphere) name() string          { return "vsphere" }
func (v *VSphere) period() time.Duration { return 30 * time.Second }
func (v *VSphere) close() {
	if v.stop != nil {
		v.stop()
	}
}

func (v *VSphere) collect(_ context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if v.cfg.Enabled == "false" {
		return nil, "disabled"
	}
	if len(v.targets) == 0 {
		if v.cfg.Enabled == "true" {
			return nil, "erreur : aucune cible vSphere"
		}
		return nil, "absent"
	}
	v.mu.Lock()
	v.tags = tags
	s := v.snap
	v.mu.Unlock()
	v.once.Do(v.start)
	if s.at.IsZero() {
		return nil, "partiel : premier relevé en cours"
	}
	ts := model.Ms(now)
	if now.Sub(s.at) > 3*v.cfg.Interval.D()+time.Minute {
		var out []model.Check
		for _, t := range v.targets {
			out = append(out, canConnect("vsphere", ts, false, "aucun relevé depuis "+now.Sub(s.at).Round(time.Second).String(),
				tags("vsphere_target", t.cfg.Name)))
		}
		return out, "erreur : relevé périmé"
	}
	out := make([]model.Check, 0, len(s.checks))
	for _, c := range s.checks {
		out = append(out, model.Check{TS: ts, Name: c.name, Status: c.status, Message: c.msg, Tags: tags(c.kv...)})
	}
	for _, g := range s.gauges {
		b.Gauge(g.name, g.unit, g.v, g.kv...)
	}
	v.pub.publish(now, s.resources, "vsphere", "ok")
	if len(s.errs) > 0 {
		return out, fmt.Sprintf("partiel : %d cible(s) en échec", len(s.errs))
	}
	return out, "ok"
}

func (v *VSphere) start() {
	ctx, cancel := context.WithCancel(context.Background())
	v.stop = cancel
	go func() {
		for {
			v.poll(ctx)
			select {
			case <-ctx.Done():
				for _, t := range v.targets {
					if t.client != nil {
						lctx, c := context.WithTimeout(context.Background(), 5*time.Second)
						_ = t.client.Logout(lctx)
						c()
					}
				}
				return
			case <-time.After(v.cfg.Interval.D()):
			}
		}
	}()
}

func (v *VSphere) poll(parent context.Context) {
	start := time.Now()
	var s vsSnapshot
	for _, t := range v.targets {
		ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
		checks, gauges, resources, err := v.pollTarget(ctx, t)
		cancel()
		kv := []string{"vsphere_target", t.cfg.Name}
		if err != nil {
			if t.client != nil { // session perdue ou serveur redémarré : nouvelle connexion au passage suivant
				lctx, c := context.WithTimeout(context.Background(), 5*time.Second)
				_ = t.client.Logout(lctx)
				c()
				t.client = nil
			}
			s.errs = append(s.errs, t.cfg.Name+" : "+err.Error())
			s.checks = append(s.checks, ovhCheck{"vsphere.can_connect", model.CheckCrit, err.Error(), kv})
			continue
		}
		s.checks = append(s.checks, checks...)
		conn := ovhCheck{"vsphere.can_connect", model.CheckOK, fmt.Sprintf("%s lu (%d contrôles)", t.cfg.URL, len(checks)), kv}
		if t.evErr != "" {
			conn.status, conn.msg = model.CheckWarn, conn.msg+" ; événements illisibles : "+t.evErr
		}
		s.checks = append(s.checks, conn)
		s.gauges = append(s.gauges, gauges...)
		s.resources = append(s.resources, resources...)
	}
	s.at, s.took = time.Now(), time.Since(start)
	v.mu.Lock()
	v.snap = s
	v.mu.Unlock()
}

func (t *vsTarget) connect(ctx context.Context) (*govmomi.Client, error) {
	if t.client != nil {
		if ok, err := t.client.SessionManager.SessionIsActive(ctx); err == nil && ok {
			return t.client, nil
		}
		t.client = nil
	}
	u, err := soap.ParseURL(t.cfg.URL)
	if err != nil || u == nil {
		return nil, fmt.Errorf("url invalide : %q", t.cfg.URL)
	}
	sc := soap.NewClient(u, t.cfg.Insecure && t.cfg.Thumbprint == "")
	if t.cfg.Thumbprint != "" {
		sc.SetThumbprint(u.Host, strings.ToUpper(t.cfg.Thumbprint))
	}
	vc, err := vim25.NewClient(ctx, sc)
	if err != nil {
		return nil, fmt.Errorf("connexion : %w", err)
	}
	c := &govmomi.Client{Client: vc, SessionManager: session.NewManager(vc)}
	if err := c.Login(ctx, url.UserPassword(t.cfg.Username, t.cfg.Password)); err != nil {
		return nil, fmt.Errorf("authentification : %w", err)
	}
	t.client = c
	return c, nil
}

func statusOf(s types.ManagedEntityStatus) int {
	switch s {
	case types.ManagedEntityStatusRed:
		return model.CheckCrit
	case types.ManagedEntityStatusYellow:
		return model.CheckWarn
	case types.ManagedEntityStatusGreen:
		return model.CheckOK
	}
	return model.CheckUnknown
}

func (v *VSphere) pollTarget(ctx context.Context, t *vsTarget) ([]ovhCheck, []ovhGauge, []*cloudResource, error) {
	c, err := t.connect(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	m := view.NewManager(c.Client)
	cv, err := m.CreateContainerView(ctx, c.ServiceContent.RootFolder, []string{"HostSystem", "Datastore", "VirtualMachine"}, true)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("vue de l'inventaire : %w", err)
	}
	defer cv.Destroy(context.Background())
	var hosts []mo.HostSystem
	if err := cv.Retrieve(ctx, []string{"HostSystem"}, []string{"name", "summary.hardware", "summary.quickStats",
		"summary.config.product", "runtime.connectionState", "runtime.inMaintenanceMode", "overallStatus",
		"triggeredAlarmState"}, &hosts); err != nil {
		return nil, nil, nil, fmt.Errorf("hôtes : %w", err)
	}
	var dss []mo.Datastore
	if err := cv.Retrieve(ctx, []string{"Datastore"}, []string{"name", "summary", "overallStatus"}, &dss); err != nil {
		return nil, nil, nil, fmt.Errorf("datastores : %w", err)
	}
	var vms []mo.VirtualMachine
	if err := cv.Retrieve(ctx, []string{"VirtualMachine"}, []string{"name", "runtime.powerState", "runtime.host",
		"summary.quickStats", "summary.config", "guest.toolsRunningStatus", "guest.ipAddress", "guestHeartbeatStatus",
		"overallStatus", "snapshot", "triggeredAlarmState"}, &vms); err != nil {
		return nil, nil, nil, fmt.Errorf("VM : %w", err)
	}
	alarms := alarmNames(ctx, c.Client, hosts, vms)
	now := time.Now()
	target := t.cfg.Name
	var checks []ovhCheck
	var gauges []ovhGauge
	var resources []*cloudResource
	g := func(name, unit string, val float64, kv ...string) {
		gauges = append(gauges, ovhGauge{name: name, unit: unit, v: val, kv: kv})
	}
	newRes := func(key, name string, dev model.Device) *cloudResource {
		tags := map[string]string{"provider": "vsphere", "vsphere_target": target}
		for k, val := range t.cfg.Tags {
			tags[k] = val
		}
		dev.Provider, dev.Service = "vsphere", target
		r := &cloudResource{key: "vsphere:" + target + ":" + key, name: name, device: dev, tags: tags}
		resources = append(resources, r)
		return r
	}

	hostName := map[string]string{}
	running := map[string]int{}
	for _, h := range hosts {
		hostName[h.Self.Value] = h.Name
	}
	for _, vm := range vms {
		if vm.Runtime.PowerState == types.VirtualMachinePowerStatePoweredOn && vm.Runtime.Host != nil {
			running[vm.Runtime.Host.Value]++
		}
	}
	sort.Slice(hosts, func(i, j int) bool { return hosts[i].Name < hosts[j].Name })
	for _, h := range hosts {
		kv := []string{"vsphere_target", target, "vsphere_host", h.Name}
		st, msg := statusOf(h.OverallStatus), "état « "+string(h.OverallStatus)+" »"
		if h.Runtime.ConnectionState != types.HostSystemConnectionStateConnected {
			st, msg = model.CheckCrit, "hôte « "+string(h.Runtime.ConnectionState)+" »"
		} else if h.Runtime.InMaintenanceMode && st == model.CheckOK {
			st, msg = model.CheckWarn, "en maintenance"
		}
		if names := alarmList(h.TriggeredAlarmState, alarms); names != "" {
			msg += " ; alarmes : " + names
		}
		if p := h.Summary.Config.Product; p != nil {
			msg += " ; " + p.FullName
		}
		r := newRes("host:"+h.Self.Value, h.Name, model.Device{Type: "esxi_host", DisplayName: h.Name, Vendor: "VMware",
			PowerState: string(h.Runtime.ConnectionState)})
		r.platform = "VMware ESXi"
		if p := h.Summary.Config.Product; p != nil {
			r.version = p.Version + " (" + p.Build + ")"
		}
		r.checks = append(r.checks, ovhCheck{"vsphere.host.status", st, h.Name + " : " + msg, kv})
		rg := func(name, unit string, val float64) {
			r.gauges = append(r.gauges, ovhGauge{name: name, unit: unit, v: val, kv: kv})
		}
		if hw := h.Summary.Hardware; hw != nil && hw.CpuMhz > 0 && hw.NumCpuCores > 0 {
			q := h.Summary.QuickStats
			r.cpuCores, r.memTotal = int(hw.NumCpuCores), uint64(hw.MemorySize)
			r.device.Model = strings.TrimSpace(hw.Vendor + " " + hw.Model)
			r.bootTime = now.Add(-time.Duration(q.Uptime) * time.Second).UnixMilli()
			rg("vsphere.host.cpu.usage", "percent", round1(100*float64(q.OverallCpuUsage)/(float64(hw.CpuMhz)*float64(hw.NumCpuCores))))
			if hw.MemorySize > 0 {
				rg("vsphere.host.mem.usage", "percent", round1(100*float64(q.OverallMemoryUsage)*1024*1024/float64(hw.MemorySize)))
			}
			rg("vsphere.host.uptime", "second", float64(q.Uptime))
		}
		rg("vsphere.host.vms.running", "count", float64(running[h.Self.Value]))
	}

	sort.Slice(dss, func(i, j int) bool { return dss[i].Name < dss[j].Name })
	for _, d := range dss {
		kv := []string{"vsphere_target", target, "vsphere_datastore", d.Name}
		st, msg := statusOf(d.OverallStatus), fmt.Sprintf("%s : %s, état « %s »", d.Name, d.Summary.Type, d.OverallStatus)
		if !d.Summary.Accessible {
			st, msg = model.CheckCrit, d.Name+" : inaccessible"
		}
		checks = append(checks, ovhCheck{"vsphere.datastore.status", st, msg, kv})
		if cap := d.Summary.Capacity; cap > 0 {
			g("vsphere.datastore.capacity", "byte", float64(cap), kv...)
			g("vsphere.datastore.free", "byte", float64(d.Summary.FreeSpace), kv...)
			g("vsphere.datastore.in_use", "percent", round1(100*float64(cap-d.Summary.FreeSpace)/float64(cap)), kv...)
		}
	}

	sort.Slice(vms, func(i, j int) bool { return vms[i].Name < vms[j].Name })
	byPower := map[string]int{}
	for _, vm := range vms {
		if vm.Summary.Config.Template {
			continue
		}
		power := string(vm.Runtime.PowerState)
		byPower[power]++
		host := ""
		if vm.Runtime.Host != nil {
			host = hostName[vm.Runtime.Host.Value]
		}
		kv := []string{"vsphere_target", target, "vsphere_vm", vm.Name, "vsphere_host", host}
		ip := ""
		if vm.Guest != nil {
			ip = vm.Guest.IpAddress
		}
		r := newRes("vm:"+vm.Self.Value, vm.Name, model.Device{Type: "virtual_machine", DisplayName: vm.Name, Parent: host,
			PowerState: power, IP: ip, Serial: vmwareSerial(vm.Summary.Config.Uuid)})
		r.platform = vm.Summary.Config.GuestFullName
		r.cpuCores, r.memTotal = int(vm.Summary.Config.NumCpu), uint64(vm.Summary.Config.MemorySizeMB)*1024*1024
		if ip != "" {
			r.ips = []string{ip}
		}
		rg := func(name, unit string, val float64) {
			r.gauges = append(r.gauges, ovhGauge{name: name, unit: unit, v: val, kv: kv})
		}
		if snap := vm.Snapshot; snap != nil {
			if oldest := oldestSnapshot(snap.RootSnapshotList); !oldest.IsZero() {
				rg("vsphere.vm.snapshot.age", "day", math.Floor(now.Sub(oldest).Hours()/24))
			}
		}
		if vm.Runtime.PowerState != types.VirtualMachinePowerStatePoweredOn {
			continue // une VM éteinte n'est pas une panne ; sa fiche dit son état, le décompte la montre
		}
		q := vm.Summary.QuickStats
		r.bootTime = now.Add(-time.Duration(q.UptimeSeconds) * time.Second).UnixMilli()
		rg("vsphere.vm.cpu.usage", "megahertz", float64(q.OverallCpuUsage))
		rg("vsphere.vm.mem.guest", "megabyte", float64(q.GuestMemoryUsage))
		rg("vsphere.vm.uptime", "second", float64(q.UptimeSeconds))
		st, msg := statusOf(vm.OverallStatus), "état « "+string(vm.OverallStatus)+" »"
		tools := ""
		if vm.Guest != nil {
			tools = vm.Guest.ToolsRunningStatus
		}
		if tools == string(types.VirtualMachineToolsRunningStatusGuestToolsRunning) && vm.GuestHeartbeatStatus == types.ManagedEntityStatusRed {
			st, msg = model.CheckCrit, "le système invité ne répond plus (VMware Tools)"
		}
		if names := alarmList(vm.TriggeredAlarmState, alarms); names != "" {
			msg += " ; alarmes : " + names
		}
		if vm.Guest != nil && vm.Guest.IpAddress != "" {
			msg += " ; " + vm.Guest.IpAddress
		}
		if st == model.CheckUnknown { // gris : rien de connu, pas une panne
			st = model.CheckOK
		}
		r.checks = append(r.checks, ovhCheck{"vsphere.vm.status", st, vm.Name + " : " + msg, kv})
	}
	for _, p := range []string{"poweredOn", "poweredOff", "suspended"} {
		g("vsphere.vm.count", "count", float64(byPower[p]), "vsphere_target", target, "power_state", p)
	}
	v.relayEvents(ctx, c.Client, t)
	return checks, gauges, resources, nil
}

func round1(x float64) float64 { return math.Round(x*10) / 10 }

func oldestSnapshot(list []types.VirtualMachineSnapshotTree) time.Time {
	var oldest time.Time
	for _, s := range list {
		if oldest.IsZero() || s.CreateTime.Before(oldest) {
			oldest = s.CreateTime
		}
		if o := oldestSnapshot(s.ChildSnapshotList); !o.IsZero() && o.Before(oldest) {
			oldest = o
		}
	}
	return oldest
}

// alarmNames : noms des alarmes déclenchées sur les hôtes et les VM, lus en un appel.
func alarmNames(ctx context.Context, c *vim25.Client, hosts []mo.HostSystem, vms []mo.VirtualMachine) map[string]string {
	refs := map[string]types.ManagedObjectReference{}
	for _, h := range hosts {
		for _, a := range h.TriggeredAlarmState {
			refs[a.Alarm.Value] = a.Alarm
		}
	}
	for _, vm := range vms {
		for _, a := range vm.TriggeredAlarmState {
			refs[a.Alarm.Value] = a.Alarm
		}
	}
	out := map[string]string{}
	if len(refs) == 0 {
		return out
	}
	list := make([]types.ManagedObjectReference, 0, len(refs))
	for _, r := range refs {
		list = append(list, r)
	}
	var alarms []mo.Alarm
	if err := property.DefaultCollector(c).Retrieve(ctx, list, []string{"info.name"}, &alarms); err == nil {
		for _, a := range alarms {
			out[a.Self.Value] = a.Info.Name
		}
	}
	return out
}

func alarmList(states []types.AlarmState, names map[string]string) string {
	var out []string
	for _, s := range states {
		if s.OverallStatus == types.ManagedEntityStatusGreen {
			continue
		}
		n := names[s.Alarm.Value]
		if n == "" {
			n = s.Alarm.Value
		}
		out = append(out, fmt.Sprintf("%s (%s)", n, s.OverallStatus))
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// relayEvents : événements de vSphere survenus depuis le passage précédent ; au premier passage,
// rien n'est rejoué.
func (v *VSphere) relayEvents(ctx context.Context, c *vim25.Client, t *vsTarget) {
	now := time.Now()
	if t.since.IsZero() {
		t.since = now
		return
	}
	begin := t.since.Add(time.Millisecond)
	evs, err := readEvents(ctx, c, begin)
	t.evErr = ""
	if err != nil {
		t.evErr = model.Truncate(err.Error(), 200)
		return
	}
	if len(evs) == 0 {
		return
	}
	sort.Slice(evs, func(i, j int) bool { return evs[i].GetEvent().CreatedTime.Before(evs[j].GetEvent().CreatedTime) })
	if len(evs) > vsphereMaxEvents {
		evs = evs[len(evs)-vsphereMaxEvents:]
	}
	v.mu.Lock()
	tags := v.tags
	v.mu.Unlock()
	var out []model.Event
	for _, be := range evs {
		e := be.GetEvent()
		if e.CreatedTime.After(t.since) {
			t.since = e.CreatedTime
		}
		kv := []string{"vsphere_target", t.cfg.Name}
		if e.Vm != nil {
			kv = append(kv, "vsphere_vm", e.Vm.Name)
		}
		if e.Host != nil {
			kv = append(kv, "vsphere_host", e.Host.Name)
		}
		typ := fmt.Sprintf("%T", be)
		typ = strings.TrimPrefix(typ, "*types.")
		ev := model.Event{TS: e.CreatedTime.UnixMilli(), Source: "vsphere", Type: "vsphere_" + typ,
			Title: model.Truncate(firstNonEmpty(e.FullFormattedMessage, typ), 200), Text: typ,
			AlertType: vsphereSeverity(be), Priority: "normal", AggregationKey: fmt.Sprintf("vsphere:%s:%d", t.cfg.Name, e.Key)}
		if tags != nil {
			ev.Tags = tags(kv...)
		}
		out = append(out, ev)
	}
	if len(out) > 0 && v.post != nil {
		v.post(routeEvents, map[string]any{"events": out})
	}
}

// readEvents : collecteur d'historique (EventHistoryCollector), lu par pages puis détruit (un
// serveur vSphere limite le nombre de collecteurs ouverts par session).
func readEvents(ctx context.Context, c *vim25.Client, begin time.Time) ([]types.BaseEvent, error) {
	col, err := event.NewManager(c).CreateCollectorForEvents(ctx, types.EventFilterSpec{Time: &types.EventFilterSpecByTime{BeginTime: &begin}})
	if err != nil {
		return nil, err
	}
	defer func() {
		dctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = col.Destroy(dctx)
		cancel()
	}()
	var out []types.BaseEvent
	for len(out) < 5*vsphereMaxEvents {
		page, err := col.ReadNextEvents(ctx, 100)
		if err != nil {
			return out, err
		}
		if len(page) == 0 {
			break
		}
		out = append(out, page...)
	}
	keep := out[:0]
	for _, e := range out { // filtre de date redit ici : tous les serveurs ne l'appliquent pas
		if e.GetEvent().CreatedTime.After(begin.Add(-time.Millisecond)) {
			keep = append(keep, e)
		}
	}
	return keep, nil
}

func vsphereSeverity(be types.BaseEvent) string {
	switch e := be.(type) {
	case *types.AlarmStatusChangedEvent:
		switch types.ManagedEntityStatus(e.To) {
		case types.ManagedEntityStatusRed:
			return "error"
		case types.ManagedEntityStatusYellow:
			return "warning"
		case types.ManagedEntityStatusGreen:
			return "success"
		}
	case *types.EventEx:
		switch e.Severity {
		case "error":
			return "error"
		case "warning":
			return "warning"
		}
	case *types.HostConnectionLostEvent, *types.HostDisconnectedEvent, *types.VmFailedToPowerOnEvent,
		*types.DatastoreRemovedOnHostEvent, *types.VmDisconnectedEvent:
		return "error"
	}
	return "info"
}

// vmwareSerial : numéro de série SMBIOS que VMware donne à l'invité, tiré de l'UUID du BIOS de la
// VM (« 421a2b3c-… » → « VMware-42 1a 2b 3c 4d 5e 6f 70-81 92 a3 b4 c5 d6 e7 f8 ») ; l'agent de l'invité
// lit le même (dmi product_serial, Win32_BIOS) : le serveur rapproche ainsi la VM de son agent (§ 51).
func vmwareSerial(uuid string) string {
	hx := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(uuid)), "-", "")
	if len(hx) != 32 {
		return ""
	}
	var b strings.Builder
	b.WriteString("VMware-")
	for i := 0; i < 16; i++ {
		switch {
		case i == 8:
			b.WriteByte('-')
		case i > 0:
			b.WriteByte(' ')
		}
		b.WriteString(hx[2*i : 2*i+2])
	}
	return b.String()
}
