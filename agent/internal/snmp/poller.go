package snmp

import (
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gosnmp/gosnmp"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// client : ce que le relevé demande à SNMP (remplacé par un faux équipement dans les tests).
type client interface {
	Get(oids []string) ([]gosnmp.SnmpPDU, error)
	Walk(root string) ([]gosnmp.SnmpPDU, error)
	Close()
}

type gsClient struct {
	g  *gosnmp.GoSNMP
	v1 bool
}

// newClient ouvre la session SNMP d'un équipement (les clés v3 ne quittent pas l'agent).
func newClient(d config.SNMPDevice) (*gsClient, error) {
	g := &gosnmp.GoSNMP{
		Target: d.Address, Port: uint16(d.Port), Timeout: d.Timeout.D(), Retries: d.Retries,
		MaxRepetitions: 20, MaxOids: 30, ExponentialTimeout: false, Community: d.Community,
	}
	switch d.Version {
	case "1":
		g.Version = gosnmp.Version1
	case "2c":
		g.Version = gosnmp.Version2c
	case "3":
		g.Version = gosnmp.Version3
		g.SecurityModel = gosnmp.UserSecurityModel
		g.ContextName = d.Context
		usm := &gosnmp.UsmSecurityParameters{UserName: d.User}
		switch strings.ToLower(d.Level) {
		case "noauthnopriv":
			g.MsgFlags = gosnmp.NoAuthNoPriv
		case "authnopriv":
			g.MsgFlags = gosnmp.AuthNoPriv
		default:
			g.MsgFlags = gosnmp.AuthPriv
		}
		if g.MsgFlags != gosnmp.NoAuthNoPriv {
			usm.AuthenticationProtocol = map[string]gosnmp.SnmpV3AuthProtocol{
				"md5": gosnmp.MD5, "sha": gosnmp.SHA, "sha224": gosnmp.SHA224, "sha256": gosnmp.SHA256,
				"sha384": gosnmp.SHA384, "sha512": gosnmp.SHA512}[d.AuthProtocol]
			usm.AuthenticationPassphrase = d.AuthKey
		}
		if g.MsgFlags == gosnmp.AuthPriv {
			usm.PrivacyProtocol = map[string]gosnmp.SnmpV3PrivProtocol{
				"des": gosnmp.DES, "aes": gosnmp.AES, "aes192": gosnmp.AES192, "aes256": gosnmp.AES256,
				"aes192c": gosnmp.AES192C, "aes256c": gosnmp.AES256C}[d.PrivProtocol]
			usm.PrivacyPassphrase = d.PrivKey
		}
		g.SecurityParameters = usm
	default:
		return nil, fmt.Errorf("version SNMP %q", d.Version)
	}
	if err := g.Connect(); err != nil {
		return nil, err
	}
	return &gsClient{g: g, v1: d.Version == "1"}, nil
}

func (c *gsClient) Get(oids []string) ([]gosnmp.SnmpPDU, error) {
	var out []gosnmp.SnmpPDU
	for len(oids) > 0 {
		n := min(len(oids), c.g.MaxOids)
		pkt, err := c.g.Get(oids[:n])
		if err != nil {
			return nil, err
		}
		if pkt.Error != gosnmp.NoError {
			if !c.v1 || n == 1 {
				return nil, fmt.Errorf("réponse SNMP en erreur : %v", pkt.Error)
			}
			// v1 : un seul OID absent fait échouer toute la requête ; un par un.
			for _, o := range oids[:n] {
				if p, err := c.g.Get([]string{o}); err == nil && p.Error == gosnmp.NoError {
					out = append(out, p.Variables...)
				}
			}
		} else {
			out = append(out, pkt.Variables...)
		}
		oids = oids[n:]
	}
	return out, nil
}

func (c *gsClient) Walk(root string) ([]gosnmp.SnmpPDU, error) {
	if c.v1 {
		return c.g.WalkAll(root)
	}
	return c.g.BulkWalkAll(root)
}

func (c *gsClient) Close() {
	if c.g.Conn != nil {
		c.g.Conn.Close()
	}
}

// ── relevé ──────────────────────────────────────────────────────────────────────────────

type sysInfo struct {
	descr, objectID, contact, name, location string
	upTime                                   float64 // secondes
}

type iface struct {
	index, name, descr, alias string
	typ                       int64
	admin, oper               int64
	speedMbps                 float64
	in, out                   uint64 // octets (64 bits si hc)
	hc                        bool
	inErr, outErr             uint64
	inDisc, outDisc           uint64
	hasErr, hasDisc           bool
}

type storage struct {
	typ, descr             string
	units, size, used      uint64
}

type sensor struct {
	kind  string // temperature, fan, psu
	name  string
	value float64
}

type extra struct {
	name, unit string
	value      float64
	kv         []string
}

type identity struct{ model, serial, firmware string }

type sample struct {
	at       time.Time
	sys      sysInfo
	profile  string
	ifaces   map[string]*iface
	cpu      []float64
	storages []storage
	sensors  []sensor
	extras   []extra
	health   []health
	ident    identity
	warnings []string
}

// health : état d'un composant que l'équipement rend en clair (disque, volume) ; le manager
// en fait un contrôle (check = nom du contrôle, tag = étiquette du composant).
type health struct {
	check, tag, name, state string
}

// ifStatic : colonnes qui ne changent guère (relues toutes les 10 minutes).
type ifStatic struct {
	name, descr, alias string
	typ                int64
	speedMbps          float64
}

type devicePoller struct {
	cfg config.SNMPDevice
	cl  client

	profile   string
	profileAt time.Time
	objectID  string
	static    map[string]ifStatic
	staticAt  time.Time
	ident     identity
	identAt   time.Time
	sensorsOn bool
	sensorsAt time.Time
}

func (d *devicePoller) walk(s *sample, root string) []gosnmp.SnmpPDU {
	pdus, err := d.cl.Walk(root)
	if err != nil {
		s.warnings = append(s.warnings, fmt.Sprintf("%s : %v", root, err))
		return nil
	}
	return pdus
}

func (d *devicePoller) get(oids ...string) map[string]gosnmp.SnmpPDU {
	out := map[string]gosnmp.SnmpPDU{}
	pdus, err := d.cl.Get(oids)
	if err != nil {
		return out
	}
	for _, p := range pdus {
		if present(p) {
			out[strings.TrimPrefix(p.Name, ".")] = p
		}
	}
	return out
}

func (d *devicePoller) probe(oid string) bool {
	_, ok := d.get(oid)[oid]
	return ok
}

// poll fait un relevé complet ; une erreur sur le groupe système = équipement injoignable.
func (d *devicePoller) poll(now time.Time) (*sample, error) {
	s := &sample{at: now, ifaces: map[string]*iface{}}
	pdus, err := d.cl.Get([]string{oidSysDescr, oidSysObjectID, oidSysUpTime, oidSysContact, oidSysName, oidSysLocation})
	if err != nil {
		return nil, err
	}
	sys := map[string]gosnmp.SnmpPDU{}
	for _, p := range pdus {
		sys[strings.TrimPrefix(p.Name, ".")] = p
	}
	if !present(sys[oidSysObjectID]) && !present(sys[oidSysDescr]) && !present(sys[oidSysUpTime]) {
		return nil, errors.New("aucune valeur du groupe system (MIB-II)")
	}
	s.sys = sysInfo{descr: asString(sys[oidSysDescr]), contact: asString(sys[oidSysContact]),
		name: asString(sys[oidSysName]), location: asString(sys[oidSysLocation])}
	if p := sys[oidSysObjectID]; present(p) {
		if v, ok := p.Value.(string); ok {
			s.sys.objectID = strings.TrimPrefix(v, ".")
		}
	}
	if t, ok := asUint(sys[oidSysUpTime]); ok {
		s.sys.upTime = float64(t) / 100
	}

	if d.cfg.Profile != "auto" {
		d.profile = d.cfg.Profile
	} else if d.profile == "" || s.sys.objectID != d.objectID || now.Sub(d.profileAt) > time.Hour {
		d.profile = detectProfile(s.sys.objectID, d.probe)
		d.profileAt = now
	}
	d.objectID = s.sys.objectID
	s.profile = d.profile

	d.interfaces(s, now)
	d.hostResources(s)
	d.entitySensors(s, now)
	switch d.profile {
	case ProfileMikroTik:
		d.mikrotik(s)
	case ProfileSynology:
		d.synology(s)
	case ProfileAsustor:
		d.asustor(s)
	case ProfileUPS:
		d.ups(s)
	case ProfilePrinter:
		d.printer(s)
	}
	if d.identAt.IsZero() || now.Sub(d.identAt) > time.Hour {
		d.ident = d.identify()
		d.identAt = now
	}
	s.ident = d.ident
	return s, nil
}

func (d *devicePoller) interfaces(s *sample, now time.Time) {
	if d.static == nil || now.Sub(d.staticAt) > 10*time.Minute {
		descr := column(d.walk(s, oidIfDescr), oidIfDescr)
		if len(descr) > 0 || d.static == nil {
			st := map[string]ifStatic{}
			typ := column(d.walk(s, oidIfType), oidIfType)
			name := column(d.walk(s, oidIfName), oidIfName)
			alias := column(d.walk(s, oidIfAlias), oidIfAlias)
			speed := column(d.walk(s, oidIfSpeed), oidIfSpeed)
			high := column(d.walk(s, oidIfHighSpeed), oidIfHighSpeed)
			for idx, p := range descr {
				x := ifStatic{descr: asString(p), name: asString(name[idx]), alias: asString(alias[idx])}
				x.typ, _ = asInt(typ[idx])
				if h, ok := asUint(high[idx]); ok && h > 0 {
					x.speedMbps = float64(h)
				} else if v, ok := asUint(speed[idx]); ok {
					x.speedMbps = float64(v) / 1e6
				}
				st[idx] = x
			}
			d.static, d.staticAt = st, now
		}
	}
	admin := column(d.walk(s, oidIfAdmin), oidIfAdmin)
	oper := column(d.walk(s, oidIfOper), oidIfOper)
	hcIn := column(d.walk(s, oidIfHCIn), oidIfHCIn)
	hcOut := column(d.walk(s, oidIfHCOut), oidIfHCOut)
	var in32, out32 map[string]gosnmp.SnmpPDU
	if len(hcIn) == 0 {
		in32 = column(d.walk(s, oidIfInOctets), oidIfInOctets)
		out32 = column(d.walk(s, oidIfOutOctets), oidIfOutOctets)
	}
	inErr := column(d.walk(s, oidIfInErrors), oidIfInErrors)
	outErr := column(d.walk(s, oidIfOutErrors), oidIfOutErrors)
	inDisc := column(d.walk(s, oidIfInDiscards), oidIfInDiscards)
	outDisc := column(d.walk(s, oidIfOutDisc), oidIfOutDisc)
	for idx, p := range oper {
		st := d.static[idx]
		x := &iface{index: idx, name: st.name, descr: st.descr, alias: st.alias, typ: st.typ, speedMbps: st.speedMbps}
		x.oper, _ = asInt(p)
		x.admin, _ = asInt(admin[idx])
		if v, ok := asUint(hcIn[idx]); ok {
			x.in, x.hc = v, true
			x.out, _ = asUint(hcOut[idx])
		} else if v, ok := asUint(in32[idx]); ok {
			x.in = v
			x.out, _ = asUint(out32[idx])
		}
		var ok1, ok2 bool
		x.inErr, ok1 = asUint(inErr[idx])
		x.outErr, ok2 = asUint(outErr[idx])
		x.hasErr = ok1 && ok2
		x.inDisc, ok1 = asUint(inDisc[idx])
		x.outDisc, ok2 = asUint(outDisc[idx])
		x.hasDisc = ok1 && ok2
		s.ifaces[idx] = x
	}
}

func (d *devicePoller) hostResources(s *sample) {
	for _, p := range sortedPDUs(column(d.walk(s, oidHrProcessorLoad), oidHrProcessorLoad)) {
		if v, ok := asInt(p); ok && v >= 0 && v <= 100 {
			s.cpu = append(s.cpu, float64(v))
		}
	}
	typ := column(d.walk(s, oidHrStorageType), oidHrStorageType)
	if len(typ) == 0 {
		return
	}
	descr := column(d.walk(s, oidHrStorageDescr), oidHrStorageDescr)
	units := column(d.walk(s, oidHrStorageUnits), oidHrStorageUnits)
	size := column(d.walk(s, oidHrStorageSize), oidHrStorageSize)
	used := column(d.walk(s, oidHrStorageUsed), oidHrStorageUsed)
	for _, idx := range sortedKeys(typ) {
		t, _ := typ[idx].Value.(string)
		st := storage{typ: strings.TrimPrefix(t, "."), descr: asString(descr[idx])}
		st.units, _ = asUint(units[idx])
		st.size, _ = asUint(size[idx])
		st.used, _ = asUint(used[idx])
		s.storages = append(s.storages, st)
	}
}

// entitySensors : ENTITY-SENSOR-MIB (températures et ventilateurs), si l'équipement le publie
// (vérifié une fois par heure).
func (d *devicePoller) entitySensors(s *sample, now time.Time) {
	if d.sensorsAt.IsZero() || now.Sub(d.sensorsAt) > time.Hour {
		d.sensorsOn = len(d.walk(s, oidEntSensorType)) > 0
		d.sensorsAt = now
	}
	if !d.sensorsOn {
		return
	}
	typ := column(d.walk(s, oidEntSensorType), oidEntSensorType)
	scale := column(d.walk(s, oidEntSensorScale), oidEntSensorScale)
	prec := column(d.walk(s, oidEntSensorPrec), oidEntSensorPrec)
	val := column(d.walk(s, oidEntSensorValue), oidEntSensorValue)
	names := column(d.walk(s, oidEntName), oidEntName)
	for _, idx := range sortedKeys(typ) {
		t, _ := asInt(typ[idx])
		kind := map[int64]string{8: "temperature", 10: "fan"}[t]
		if kind == "" {
			continue
		}
		raw, ok := asInt(val[idx])
		if !ok {
			continue
		}
		sc, _ := asInt(scale[idx])
		pr, _ := asInt(prec[idx])
		name := asString(names[idx])
		if name == "" {
			name = "capteur " + idx
		}
		s.sensors = append(s.sensors, sensor{kind: kind, name: name, value: sensorValue(raw, sc, pr)})
	}
}

// sensorValue applique l'échelle (9 = unités, 8 = milli…, 10 = kilo…) et la précision.
func sensorValue(raw, scale, precision int64) float64 {
	v := float64(raw)
	if scale >= 1 && scale <= 17 {
		v *= math.Pow(10, float64(3*(scale-9)))
	}
	if precision > 0 && precision <= 9 {
		v /= math.Pow(10, float64(precision))
	}
	return v
}

func (d *devicePoller) mikrotik(s *sample) {
	// Ancien groupe santé (RouterOS 6, valeurs en dixièmes de degré), puis table des jauges
	// de RouterOS 7 (degrés, tr/min).
	h := d.get(oidMtxrHealth+".6.0", oidMtxrHealth+".7.0", oidMtxrHealth+".10.0", oidMtxrHealth+".11.0",
		oidMtxrHealth+".15.0", oidMtxrHealth+".16.0", oidMtxrHealth+".17.0", oidMtxrHealth+".18.0")
	for oid, name := range map[string]string{".6.0": "cpu", ".7.0": "board", ".10.0": "system", ".11.0": "processor"} {
		if v, ok := asInt(h[oidMtxrHealth+oid]); ok && v > 0 {
			s.sensors = append(s.sensors, sensor{kind: "temperature", name: name, value: float64(v) / 10})
		}
	}
	for oid, name := range map[string]string{".17.0": "fan1", ".18.0": "fan2"} {
		if v, ok := asInt(h[oidMtxrHealth+oid]); ok && v >= 0 {
			s.sensors = append(s.sensors, sensor{kind: "fan", name: name, value: float64(v)})
		}
	}
	for oid, name := range map[string]string{".15.0": "psu1", ".16.0": "psu2"} {
		if v, ok := asInt(h[oidMtxrHealth+oid]); ok {
			s.sensors = append(s.sensors, sensor{kind: "psu", name: name, value: psuState(v == 1)})
		}
	}
	names := column(d.walk(s, oidMtxrGaugeName), oidMtxrGaugeName)
	if len(names) == 0 {
		return
	}
	vals := column(d.walk(s, oidMtxrGaugeValue), oidMtxrGaugeValue)
	units := column(d.walk(s, oidMtxrGaugeUnit), oidMtxrGaugeUnit)
	for _, idx := range sortedKeys(names) {
		v, ok := asInt(vals[idx])
		if !ok {
			continue
		}
		u, _ := asInt(units[idx])
		n := asString(names[idx])
		switch u {
		case 1:
			s.sensors = append(s.sensors, sensor{kind: "temperature", name: n, value: float64(v)})
		case 2:
			s.sensors = append(s.sensors, sensor{kind: "fan", name: n, value: float64(v)})
		case 6:
			if strings.Contains(n, "psu") {
				s.sensors = append(s.sensors, sensor{kind: "psu", name: n, value: psuState(v == 1)})
			}
		}
	}
}

// psuState : 1 = en service, 2 = en panne (même lecture que Synology).
func psuState(ok bool) float64 {
	if ok {
		return 1
	}
	return 2
}

func (d *devicePoller) synology(s *sample) {
	g := d.get(oidSynoSystemStatus, oidSynoTemperature, oidSynoPower, oidSynoSystemFan, oidSynoCPUFan)
	if v, ok := asInt(g[oidSynoTemperature]); ok {
		s.sensors = append(s.sensors, sensor{kind: "temperature", name: "system", value: float64(v)})
	}
	if v, ok := asInt(g[oidSynoPower]); ok {
		s.sensors = append(s.sensors, sensor{kind: "psu", name: "power", value: float64(v)})
	}
	for oid, name := range map[string]string{oidSynoSystemFan: "system", oidSynoCPUFan: "cpu"} {
		if v, ok := asInt(g[oid]); ok {
			s.extras = append(s.extras, extra{name: "snmp.fan.status", unit: "count", value: float64(v), kv: []string{"sensor", name}})
		}
	}
	if v, ok := asInt(g[oidSynoSystemStatus]); ok {
		s.extras = append(s.extras, extra{name: "snmp.system.status", unit: "count", value: float64(v)})
	}
	ids := column(d.walk(s, oidSynoDiskID), oidSynoDiskID)
	status := column(d.walk(s, oidSynoDiskStatus), oidSynoDiskStatus)
	temp := column(d.walk(s, oidSynoDiskTemp), oidSynoDiskTemp)
	for _, idx := range sortedKeys(ids) {
		n := asString(ids[idx])
		if v, ok := asInt(temp[idx]); ok {
			s.sensors = append(s.sensors, sensor{kind: "temperature", name: "disk:" + n, value: float64(v)})
		}
		if v, ok := asInt(status[idx]); ok {
			s.extras = append(s.extras, extra{name: "snmp.disk.status", unit: "count", value: float64(v), kv: []string{"disk", n}})
		}
	}
	rn := column(d.walk(s, oidSynoRaidName), oidSynoRaidName)
	rs := column(d.walk(s, oidSynoRaidStatus), oidSynoRaidStatus)
	for _, idx := range sortedKeys(rn) {
		if v, ok := asInt(rs[idx]); ok {
			s.extras = append(s.extras, extra{name: "snmp.raid.status", unit: "count", value: float64(v), kv: []string{"raid", asString(rn[idx])}})
		}
	}
}

// asustor : températures (processeur, système), ventilateurs, disques (état, température) et
// volumes (état, occupation) d'un NAS ASUSTOR. La table des onduleurs du sous-agent n'est pas
// lue : sans onduleur, ADM y range un concentrateur USB avec l'état « OB » (sur batterie).
func (d *devicePoller) asustor(s *sample) {
	for _, c := range []struct{ oid, name string }{{oidAsusCPUTemp, "cpu"}, {oidAsusSysTemp, "system"}} {
		col := column(d.walk(s, c.oid), c.oid)
		for _, idx := range sortedKeys(col) {
			if v, ok := asInt(col[idx]); ok && v > 0 {
				s.sensors = append(s.sensors, sensor{kind: "temperature", name: c.name, value: float64(v)})
				break
			}
		}
	}
	fans := column(d.walk(s, oidAsusFanSpeed), oidAsusFanSpeed)
	for _, idx := range sortedKeys(fans) {
		if v, ok := asInt(fans[idx]); ok {
			s.sensors = append(s.sensors, sensor{kind: "fan", name: "fan" + idx, value: float64(v)})
		}
	}
	names := column(d.walk(s, oidAsusDiskName), oidAsusDiskName)
	models := column(d.walk(s, oidAsusDiskModel), oidAsusDiskModel)
	status := column(d.walk(s, oidAsusDiskStatus), oidAsusDiskStatus)
	temp := column(d.walk(s, oidAsusDiskTemp), oidAsusDiskTemp)
	for _, idx := range sortedKeys(names) {
		n := asString(names[idx])
		if n == "" {
			continue
		}
		if v, ok := asInt(temp[idx]); ok && v > 0 {
			s.sensors = append(s.sensors, sensor{kind: "temperature", name: "disk:" + n, value: float64(v)})
		}
		if p, ok := status[idx]; ok {
			st := asString(p)
			s.extras = append(s.extras, extra{name: "snmp.disk.status", unit: "count", value: stateValue(st), kv: []string{"disk", n}})
			label := st
			if m := asString(models[idx]); m != "" {
				label = st + " (" + m + ")"
			}
			s.health = append(s.health, health{check: "snmp.disk.health", tag: "disk", name: n, state: label})
		}
	}
	vn := column(d.walk(s, oidAsusVolName), oidAsusVolName)
	vs := column(d.walk(s, oidAsusVolStatus), oidAsusVolStatus)
	vt := column(d.walk(s, oidAsusVolTotal), oidAsusVolTotal)
	vf := column(d.walk(s, oidAsusVolFree), oidAsusVolFree)
	for _, idx := range sortedKeys(vn) {
		n := asString(vn[idx])
		if n == "" {
			continue
		}
		total, okT := asInt(vt[idx])
		free, okF := asInt(vf[idx])
		if okT && okF && total > 0 && free >= 0 && free <= total {
			kv := []string{"volume", n}
			s.extras = append(s.extras,
				extra{name: "snmp.volume.total", unit: "byte", value: float64(total) * (1 << 30), kv: kv},
				extra{name: "snmp.volume.free", unit: "byte", value: float64(free) * (1 << 30), kv: kv},
				extra{name: "snmp.volume.usage", unit: "percent", value: float64(total-free) / float64(total) * 100, kv: kv})
		}
		if p, ok := vs[idx]; ok {
			st := asString(p)
			s.extras = append(s.extras, extra{name: "snmp.volume.status", unit: "count", value: stateValue(st), kv: []string{"volume", n}})
			s.health = append(s.health, health{check: "snmp.volume.health", tag: "volume", name: n, state: st})
		}
	}
}

// healthStatus lit un état rendu en clair : « Good », « healthy », « normal » = OK ;
// reconstruction, synchronisation, vérification, dégradé ou alerte = WARN ; vide = UNKNOWN ;
// tout autre état (« Bad », « crashed », « failed »…) = CRIT.
func healthStatus(state string) int {
	s := strings.ToLower(strings.TrimSpace(state))
	if i := strings.Index(s, " ("); i >= 0 {
		s = s[:i]
	}
	switch {
	case s == "":
		return model.CheckUnknown
	case s == "good" || s == "healthy" || s == "normal" || s == "ok":
		return model.CheckOK
	}
	for _, w := range []string{"degrad", "warn", "rebuild", "resync", "sync", "recover", "check", "migrat", "expand"} {
		if strings.Contains(s, w) {
			return model.CheckWarn
		}
	}
	return model.CheckCrit
}

// stateValue : 1 = sain, 2 = à surveiller, 3 = en défaut, 0 = inconnu (métriques d'état).
func stateValue(state string) float64 {
	return map[int]float64{model.CheckOK: 1, model.CheckWarn: 2, model.CheckCrit: 3, model.CheckUnknown: 0}[healthStatus(state)]
}

// healthChecks : un contrôle par composant relevé (snmp.disk.health, snmp.volume.health).
func healthChecks(s *sample, ts int64, tags model.TagFunc) []model.Check {
	out := make([]model.Check, 0, len(s.health))
	for _, h := range s.health {
		st := healthStatus(h.state)
		msg := "état : " + h.state
		if h.state == "" {
			msg = "état non rendu par l'équipement"
		}
		out = append(out, model.Check{TS: ts, Name: h.check, Status: st, Message: msg, Tags: tags(h.tag, h.name)})
	}
	return out
}

func (d *devicePoller) ups(s *sample) {
	g := d.get(oidUpsCharge, oidUpsMinutesLeft, oidUpsBatteryStatus, oidUpsBatteryTemp, oidUpsOutputSource,
		oidApcCapacity, oidApcRunTime, oidApcLoad, oidApcBatteryTemp, oidApcOutputStatus)
	add := func(name, unit string, v float64) { s.extras = append(s.extras, extra{name: name, unit: unit, value: v}) }
	if v, ok := asInt(g[oidUpsCharge]); ok {
		add("snmp.ups.battery.charge", "percent", float64(v))
	} else if v, ok := asInt(g[oidApcCapacity]); ok {
		add("snmp.ups.battery.charge", "percent", float64(v))
	}
	if v, ok := asInt(g[oidUpsMinutesLeft]); ok {
		add("snmp.ups.battery.runtime", "second", float64(v)*60)
	} else if v, ok := asUint(g[oidApcRunTime]); ok {
		add("snmp.ups.battery.runtime", "second", float64(v)/100)
	}
	loads := column(d.walk(s, oidUpsOutputLoad), oidUpsOutputLoad)
	if len(loads) > 0 {
		hi := -1.0
		for _, p := range loads {
			if v, ok := asInt(p); ok && float64(v) > hi {
				hi = float64(v)
			}
		}
		if hi >= 0 {
			add("snmp.ups.battery.load", "percent", hi)
		}
	} else if v, ok := asInt(g[oidApcLoad]); ok {
		add("snmp.ups.battery.load", "percent", float64(v))
	}
	if v, ok := asInt(g[oidUpsBatteryTemp]); ok {
		s.sensors = append(s.sensors, sensor{kind: "temperature", name: "battery", value: float64(v)})
	} else if v, ok := asInt(g[oidApcBatteryTemp]); ok {
		s.sensors = append(s.sensors, sensor{kind: "temperature", name: "battery", value: float64(v)})
	}
	if v, ok := asInt(g[oidUpsOutputSource]); ok {
		add("snmp.ups.on_battery", "count", b2f(v == 5))
	} else if v, ok := asInt(g[oidApcOutputStatus]); ok {
		add("snmp.ups.on_battery", "count", b2f(v == 3))
	}
	if v, ok := asInt(g[oidUpsBatteryStatus]); ok {
		add("snmp.ups.battery.status", "count", float64(v))
	}
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func (d *devicePoller) printer(s *sample) {
	descr := column(d.walk(s, oidPrtSupplyDescr), oidPrtSupplyDescr)
	maxs := column(d.walk(s, oidPrtSupplyMax), oidPrtSupplyMax)
	lvls := column(d.walk(s, oidPrtSupplyLevel), oidPrtSupplyLevel)
	for _, idx := range sortedKeys(descr) {
		mx, ok1 := asInt(maxs[idx])
		lv, ok2 := asInt(lvls[idx])
		if !ok1 || !ok2 || mx <= 0 || lv < 0 { // -2 inconnu, -3 « il en reste »
			continue
		}
		s.extras = append(s.extras, extra{name: "snmp.printer.supply.level", unit: "percent",
			value: math.Min(100, float64(lv)/float64(mx)*100), kv: []string{"supply", asString(descr[idx])}})
	}
	st := column(d.walk(s, oidHrPrinterStatus), oidHrPrinterStatus)
	for _, idx := range sortedKeys(st) {
		if v, ok := asInt(st[idx]); ok {
			s.extras = append(s.extras, extra{name: "snmp.printer.status", unit: "count", value: float64(v)})
			break
		}
	}
}

// identify : modèle, n° de série, micrologiciel (une fois par heure).
func (d *devicePoller) identify() identity {
	var id identity
	switch d.profile {
	case ProfileMikroTik:
		g := d.get(oidMtxrBoard, oidMtxrSerial, oidMtxrFirmware)
		id = identity{asString(g[oidMtxrBoard]), asString(g[oidMtxrSerial]), asString(g[oidMtxrFirmware])}
	case ProfileSynology:
		g := d.get(oidSynoModel, oidSynoSerial, oidSynoVersion)
		id = identity{asString(g[oidSynoModel]), asString(g[oidSynoSerial]), asString(g[oidSynoVersion])}
	case ProfileAsustor:
		g := d.get(oidAsusModel, oidAsusSerial, oidAsusVersion)
		id = identity{asString(g[oidAsusModel]), asString(g[oidAsusSerial]), asString(g[oidAsusVersion])}
		if id.firmware != "" {
			id.firmware = "ADM " + id.firmware
		}
	case ProfileUPS:
		g := d.get(oidUpsModel, oidUpsSoftware, oidApcModel, oidApcSerial, oidApcFirmware)
		id = identity{firstNonEmpty(asString(g[oidApcModel]), asString(g[oidUpsModel])), asString(g[oidApcSerial]),
			firstNonEmpty(asString(g[oidApcFirmware]), asString(g[oidUpsSoftware]))}
	case ProfilePrinter:
		g := d.get(oidHrDeviceDescr1)
		id.model = asString(g[oidHrDeviceDescr1])
	}
	if id.model == "" || id.serial == "" || id.firmware == "" {
		var s sample
		model := firstValue(column(d.walk(&s, oidEntModel), oidEntModel))
		serial := firstValue(column(d.walk(&s, oidEntSerial), oidEntSerial))
		fw := firstValue(column(d.walk(&s, oidEntFirmware), oidEntFirmware))
		if fw == "" {
			fw = firstValue(column(d.walk(&s, oidEntSoftware), oidEntSoftware))
		}
		id.model = firstNonEmpty(id.model, model)
		id.serial = firstNonEmpty(id.serial, serial)
		id.firmware = firstNonEmpty(id.firmware, fw)
	}
	return id
}

func firstValue(m map[string]gosnmp.SnmpPDU) string {
	for _, k := range sortedKeys(m) {
		if v := asString(m[k]); v != "" {
			return v
		}
	}
	return ""
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// sortedKeys trie les index numériquement (« 2 » avant « 10 »).
func sortedKeys(m map[string]gosnmp.SnmpPDU) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return lessIndex(keys[i], keys[j]) })
	return keys
}

func lessIndex(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		x, e1 := strconv.Atoi(pa[i])
		y, e2 := strconv.Atoi(pb[i])
		if e1 != nil || e2 != nil {
			if pa[i] != pb[i] {
				return pa[i] < pb[i]
			}
			continue
		}
		if x != y {
			return x < y
		}
	}
	return len(pa) < len(pb)
}

func sortedPDUs(m map[string]gosnmp.SnmpPDU) []gosnmp.SnmpPDU {
	out := make([]gosnmp.SnmpPDU, 0, len(m))
	for _, k := range sortedKeys(m) {
		out = append(out, m[k])
	}
	return out
}

// ── séries et contrôles ─────────────────────────────────────────────────────────────────

// ifName : nom affiché d'une interface (ifName, sinon ifDescr, sinon son index).
func (x *iface) label() string {
	return firstNonEmpty(x.name, x.descr, "if"+x.index)
}

func excludedIface(name string, patterns []string) bool {
	for _, p := range patterns {
		if ok, _ := filepath.Match(p, name); ok {
			return true
		}
	}
	return false
}

// counterDelta : écart d'un compteur ; un compteur 32 bits qui repasse par zéro est
// rattrapé, un compteur 64 bits qui recule (remise à zéro) ne donne rien.
func counterDelta(cur, prev uint64, wide bool) (uint64, bool) {
	if cur >= prev {
		return cur - prev, true
	}
	if !wide && prev <= math.MaxUint32 {
		return cur + (1 << 32) - prev, true
	}
	return 0, false
}

// sampleSeries écrit les séries d'un relevé ; les débits demandent le relevé précédent
// (et sont omis si l'équipement a redémarré entre les deux).
func sampleSeries(b *model.SeriesBuilder, cur, prev *sample, excl []string) {
	if cur.sys.upTime > 0 {
		b.Gauge("snmp.sysUpTimeInstance", "second", cur.sys.upTime)
	}
	dt := 0.0
	if prev != nil {
		dt = cur.at.Sub(prev.at).Seconds()
		if cur.sys.upTime > 0 && prev.sys.upTime > 0 && cur.sys.upTime < prev.sys.upTime {
			dt = 0 // redémarrage : compteurs repartis de zéro
		}
	}
	for _, idx := range sortedIfaces(cur.ifaces) {
		x := cur.ifaces[idx]
		if x.typ == 24 || excludedIface(x.label(), excl) { // 24 = softwareLoopback
			continue
		}
		kv := []string{"interface", x.label()}
		if x.alias != "" {
			kv = append(kv, "interface_alias", x.alias)
		}
		tag := func() []string { return append([]string{}, kv...) }
		b.Gauge("snmp.ifOperStatus", "count", float64(x.oper), tag()...)
		b.Gauge("snmp.ifAdminStatus", "count", float64(x.admin), tag()...)
		if prev == nil || dt <= 0 {
			continue
		}
		p, ok := prev.ifaces[idx]
		if !ok {
			continue
		}
		rate := func(c, o uint64, wide bool) (float64, bool) {
			d, ok := counterDelta(c, o, wide)
			if !ok {
				return 0, false
			}
			return float64(d) / dt, true
		}
		if x.hc == p.hc {
			if in, ok := rate(x.in, p.in, x.hc); ok {
				b.Gauge("snmp.ifBandwidthIn", "byte_per_second", in, tag()...)
				if x.speedMbps > 0 {
					b.Gauge("snmp.ifBandwidthInUsage", "percent", math.Min(100, in*8/(x.speedMbps*1e6)*100), tag()...)
				}
			}
			if out, ok := rate(x.out, p.out, x.hc); ok {
				b.Gauge("snmp.ifBandwidthOut", "byte_per_second", out, tag()...)
				if x.speedMbps > 0 {
					b.Gauge("snmp.ifBandwidthOutUsage", "percent", math.Min(100, out*8/(x.speedMbps*1e6)*100), tag()...)
				}
			}
		}
		if x.hasErr && p.hasErr {
			if v, ok := rate(x.inErr, p.inErr, false); ok {
				b.Gauge("snmp.ifInErrors", "packet_per_second", v, tag()...)
			}
			if v, ok := rate(x.outErr, p.outErr, false); ok {
				b.Gauge("snmp.ifOutErrors", "packet_per_second", v, tag()...)
			}
		}
		if x.hasDisc && p.hasDisc {
			if v, ok := rate(x.inDisc, p.inDisc, false); ok {
				b.Gauge("snmp.ifInDiscards", "packet_per_second", v, tag()...)
			}
			if v, ok := rate(x.outDisc, p.outDisc, false); ok {
				b.Gauge("snmp.ifOutDiscards", "packet_per_second", v, tag()...)
			}
		}
	}
	if len(cur.cpu) > 0 {
		var sum float64
		for _, v := range cur.cpu {
			sum += v
		}
		b.Gauge("snmp.cpu.usage", "percent", sum/float64(len(cur.cpu)))
	}
	memSeries(b, cur.storages)
	for _, st := range cur.storages {
		switch st.typ {
		case hrStorageFixedDisk, hrStorageNetwork, hrStorageFlash:
			if st.size > 0 {
				b.Gauge("snmp.storage.usage", "percent", float64(st.used)/float64(st.size)*100, "storage", st.descr)
			}
		}
	}
	for _, s := range cur.sensors {
		switch s.kind {
		case "temperature":
			b.Gauge("snmp.temperature", "celsius", s.value, "sensor", s.name)
		case "fan":
			b.Gauge("snmp.fan.speed", "rpm", s.value, "sensor", s.name)
		case "psu":
			b.Gauge("snmp.psu.status", "count", s.value, "sensor", s.name)
		}
	}
	for _, e := range cur.extras {
		b.Gauge(e.name, e.unit, e.value, append([]string{}, e.kv...)...)
	}
}

// memSeries : mémoire vive (hrStorageRam) ; chez Net-SNMP (Linux, NAS), les tampons et le
// cache comptés dans « Physical memory » sont retirés de l'occupé, comme le ferait free.
func memSeries(b *model.SeriesBuilder, storages []storage) {
	var ram *storage
	var buffers, cached uint64
	for i := range storages {
		st := &storages[i]
		switch {
		case st.typ == hrStorageRAM && ram == nil:
			ram = st
		case st.descr == "Memory buffers":
			buffers = st.used * st.units
		case st.descr == "Cached memory":
			cached = st.used * st.units
		}
	}
	if ram == nil || ram.size == 0 || ram.units == 0 {
		return
	}
	total := ram.size * ram.units
	used := ram.used * ram.units
	if buffers+cached < used {
		used -= buffers + cached
	}
	b.Gauge("snmp.memory.total", "byte", float64(total))
	b.Gauge("snmp.memory.used", "byte", float64(used))
	b.Gauge("snmp.memory.usage", "percent", float64(used)/float64(total)*100)
}

func sortedIfaces(m map[string]*iface) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return lessIndex(keys[i], keys[j]) })
	return keys
}

// interfaceChecks : snmp.interface.status des interfaces actives (admin up). Une interface
// coupée (oper down) est CRIT si elle a déjà été vue en service (ou toujours, si la conf
// le demande) ; un port jamais utilisé reste OK. seen est mis à jour.
func interfaceChecks(s *sample, ts int64, tags model.TagFunc, excl []string, always bool, seen map[string]bool) []model.Check {
	var out []model.Check
	for _, idx := range sortedIfaces(s.ifaces) {
		x := s.ifaces[idx]
		if x.typ == 24 || x.admin != 1 || excludedIface(x.label(), excl) {
			continue
		}
		name := x.label()
		if x.oper == 1 {
			seen[name] = true
		}
		st, msg := model.CheckOK, "up"
		switch x.oper {
		case 1:
		case 2, 7:
			msg = map[int64]string{2: "down", 7: "lowerLayerDown"}[x.oper]
			if always || seen[name] {
				st = model.CheckCrit
				msg += " alors que l'interface est activée (ifAdminStatus up)"
			} else {
				msg += ", jamais vue active (port libre ?)"
			}
		case 3:
			st, msg = model.CheckWarn, "testing"
		case 5:
			msg = "dormant"
		case 6:
			continue // notPresent : composant absent
		default:
			st, msg = model.CheckUnknown, fmt.Sprintf("état %d", x.oper)
		}
		kv := []string{"interface", name}
		if x.alias != "" {
			kv = append(kv, "interface_alias", x.alias)
		}
		out = append(out, model.Check{TS: ts, Name: "snmp.interface.status", Status: st, Message: msg, Tags: tags(kv...)})
	}
	return out
}
