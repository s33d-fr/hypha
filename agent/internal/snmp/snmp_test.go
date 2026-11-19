package snmp

import (
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gosnmp/gosnmp"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// fakeClient : équipement en mémoire (OID sans point initial).
type fakeClient map[string]gosnmp.SnmpPDU

func (f fakeClient) set(oid string, typ gosnmp.Asn1BER, v any) {
	f[oid] = gosnmp.SnmpPDU{Name: "." + oid, Type: typ, Value: v}
}

func (f fakeClient) Get(oids []string) ([]gosnmp.SnmpPDU, error) {
	out := make([]gosnmp.SnmpPDU, 0, len(oids))
	for _, o := range oids {
		if p, ok := f[o]; ok {
			out = append(out, p)
		} else {
			out = append(out, gosnmp.SnmpPDU{Name: "." + o, Type: gosnmp.NoSuchObject})
		}
	}
	return out, nil
}

func (f fakeClient) Walk(root string) ([]gosnmp.SnmpPDU, error) {
	var keys []string
	for k := range f {
		if strings.HasPrefix(k, root+".") {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return lessIndex(keys[i], keys[j]) })
	var out []gosnmp.SnmpPDU
	for _, k := range keys {
		out = append(out, f[k])
	}
	return out, nil
}

func (f fakeClient) Close() {}

func mikrotik(inOctets uint64, ether2Oper int, uptime uint32) fakeClient {
	f := fakeClient{}
	f.set(oidSysDescr, gosnmp.OctetString, []byte("RouterOS CRS326-24G-2S+\r\n"))
	f.set(oidSysObjectID, gosnmp.ObjectIdentifier, ".1.3.6.1.4.1.14988.1")
	f.set(oidSysUpTime, gosnmp.TimeTicks, uptime)
	f.set(oidSysName, gosnmp.OctetString, []byte("switch-vlan1"))
	for i, name := range []string{"ether1", "ether2", "ether3", "lo"} {
		idx := string(rune('1' + i))
		f.set(oidIfDescr+"."+idx, gosnmp.OctetString, []byte(name))
		f.set(oidIfName+"."+idx, gosnmp.OctetString, []byte(name))
		typ := 6
		if name == "lo" {
			typ = 24
		}
		f.set(oidIfType+"."+idx, gosnmp.Integer, typ)
		f.set(oidIfHighSpeed+"."+idx, gosnmp.Gauge32, uint(1000))
		admin, oper := 1, 1
		switch name {
		case "ether2":
			oper = ether2Oper
		case "ether3":
			admin, oper = 2, 2
		}
		f.set(oidIfAdmin+"."+idx, gosnmp.Integer, admin)
		f.set(oidIfOper+"."+idx, gosnmp.Integer, oper)
		f.set(oidIfHCIn+"."+idx, gosnmp.Counter64, inOctets)
		f.set(oidIfHCOut+"."+idx, gosnmp.Counter64, inOctets/2)
		f.set(oidIfInErrors+"."+idx, gosnmp.Counter32, uint(10))
		f.set(oidIfOutErrors+"."+idx, gosnmp.Counter32, uint(0))
	}
	f.set(oidIfAlias+".1", gosnmp.OctetString, []byte("vers routeur-01"))
	f.set(oidHrProcessorLoad+".1", gosnmp.Integer, 12)
	f.set(oidHrProcessorLoad+".2", gosnmp.Integer, 20)
	f.set(oidHrStorageType+".65536", gosnmp.ObjectIdentifier, "."+hrStorageRAM)
	f.set(oidHrStorageDescr+".65536", gosnmp.OctetString, []byte("main memory"))
	f.set(oidHrStorageUnits+".65536", gosnmp.Integer, 1024)
	f.set(oidHrStorageSize+".65536", gosnmp.Integer, 524288)
	f.set(oidHrStorageUsed+".65536", gosnmp.Integer, 131072)
	f.set(oidMtxrHealth+".10.0", gosnmp.Integer, 365)
	f.set(oidMtxrBoard, gosnmp.OctetString, []byte("CRS326-24G-2S+"))
	f.set(oidMtxrSerial, gosnmp.OctetString, []byte("HD9087ABC"))
	f.set(oidMtxrFirmware, gosnmp.OctetString, []byte("7.16.1"))
	return f
}

func seriesMap(s []model.Series) map[string]float64 {
	out := map[string]float64{}
	for _, x := range s {
		k := x.Metric
		if v := x.Tags["interface"]; v != "" {
			k += "|" + v
		}
		for _, t := range []string{"sensor", "disk", "volume"} {
			if v := x.Tags[t]; v != "" {
				k += "|" + v
			}
		}
		out[k] = x.Points[0].Value
	}
	return out
}

func noTags(kv ...string) map[string]string {
	t := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		t[kv[i]] = kv[i+1]
	}
	return t
}

func TestPollMikroTik(t *testing.T) {
	cfg := config.SNMPDevice{Name: "switch-01", Address: "192.0.2.252", Profile: "auto"}
	d := &devicePoller{cfg: cfg, cl: mikrotik(1_000_000, 2, 50000)}
	t0 := time.Unix(1_800_000_000, 0)
	s1, err := d.poll(t0)
	if err != nil {
		t.Fatal(err)
	}
	if s1.profile != ProfileMikroTik || s1.ident.model != "CRS326-24G-2S+" || s1.ident.firmware != "7.16.1" {
		t.Fatalf("profil ou identité : %q %+v", s1.profile, s1.ident)
	}
	if s1.sys.descr != "RouterOS CRS326-24G-2S+" {
		t.Fatalf("sysDescr nettoyé : %q", s1.sys.descr)
	}
	d.cl = mikrotik(1_000_000+6_000_000, 2, 56000)
	s2, err := d.poll(t0.Add(60 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	b := model.NewSeriesBuilder(0, noTags)
	sampleSeries(b, s2, s1, nil)
	m := seriesMap(b.Series())
	checks := map[string]float64{
		"snmp.ifBandwidthIn|ether1":      100_000,
		"snmp.ifBandwidthOut|ether1":     50_000,
		"snmp.ifBandwidthInUsage|ether1": 0.08,
		"snmp.ifInErrors|ether1":         0,
		"snmp.ifOperStatus|ether2":       2,
		"snmp.ifAdminStatus|ether3":      2,
		"snmp.cpu.usage":                 16,
		"snmp.memory.total":              536870912,
		"snmp.memory.usage":              25,
		"snmp.temperature|system":        36.5,
		"snmp.sysUpTimeInstance":         560,
	}
	for k, want := range checks {
		got, ok := m[k]
		if !ok {
			t.Errorf("%s absente", k)
			continue
		}
		if d := got - want; d > 1e-6 || d < -1e-6 {
			t.Errorf("%s = %v, attendu %v", k, got, want)
		}
	}
	if _, ok := m["snmp.ifOperStatus|lo"]; ok {
		t.Error("boucle locale publiée")
	}

	// Redémarrage de l'équipement : pas de débit calculé sur des compteurs repartis de zéro.
	d.cl = mikrotik(10, 2, 100)
	s3, _ := d.poll(t0.Add(120 * time.Second))
	b = model.NewSeriesBuilder(0, noTags)
	sampleSeries(b, s3, s2, nil)
	if _, ok := seriesMap(b.Series())["snmp.ifBandwidthIn|ether1"]; ok {
		t.Error("débit calculé à travers un redémarrage")
	}
}

func TestInterfaceChecks(t *testing.T) {
	d := &devicePoller{cfg: config.SNMPDevice{Profile: "generic"}, cl: mikrotik(1, 2, 1)}
	s, _ := d.poll(time.Now())
	seen := map[string]bool{}
	st := func(cs []model.Check) map[string]int {
		out := map[string]int{}
		for _, c := range cs {
			out[c.Tags["interface"]] = c.Status
		}
		return out
	}
	got := st(interfaceChecks(s, 0, noTags, nil, false, seen))
	if got["ether1"] != model.CheckOK || got["ether2"] != model.CheckOK {
		t.Fatalf("port libre en alerte : %v", got)
	}
	if _, ok := got["ether3"]; ok {
		t.Fatal("interface désactivée contrôlée")
	}
	if got := st(interfaceChecks(s, 0, noTags, nil, true, map[string]bool{})); got["ether2"] != model.CheckCrit {
		t.Fatalf("interface_down: always : %v", got)
	}
	d.cl = mikrotik(1, 1, 2)
	s, _ = d.poll(time.Now())
	interfaceChecks(s, 0, noTags, nil, false, seen) // ether2 vue en service
	d.cl = mikrotik(1, 2, 3)
	s, _ = d.poll(time.Now())
	if got := st(interfaceChecks(s, 0, noTags, nil, false, seen)); got["ether2"] != model.CheckCrit {
		t.Fatalf("interface tombée non signalée : %v", got)
	}
}

func TestDetectProfile(t *testing.T) {
	none := func(string) bool { return false }
	cases := []struct {
		oid   string
		probe func(string) bool
		want  string
	}{
		{"1.3.6.1.4.1.14988.1", none, ProfileMikroTik},
		{".1.3.6.1.4.1.318.1.3.27", none, ProfileUPS},
		{"1.3.6.1.4.1.2435.2.3.9.1", none, ProfilePrinter},
		{"1.3.6.1.4.1.8072.3.2.10", func(o string) bool { return o == oidSynoModel }, ProfileSynology},
		{"1.3.6.1.4.1.8072.3.2.10", func(o string) bool { return o == oidUpsManufacturer }, ProfileUPS},
		{"1.3.6.1.4.1.9.1.1208", none, ProfileGeneric},
	}
	for _, c := range cases {
		if got := detectProfile(c.oid, c.probe); got != c.want {
			t.Errorf("%s : %s, attendu %s", c.oid, got, c.want)
		}
	}
	if v := vendorOf("1.3.6.1.4.1.14988.1"); v != "MikroTik" {
		t.Error(v)
	}
}

// Valeurs relevées sur un NAS ASUSTOR (ADM 5.1.4, 18/11/2026), un disque et un volume ajoutés en défaut.
func TestAsustor(t *testing.T) {
	if got := detectProfile("1.3.6.1.4.1.8072.3.2.10", func(o string) bool { return o == oidAsusModel }); got != ProfileAsustor {
		t.Fatalf("détection : %s", got)
	}
	f := fakeClient{}
	f.set(oidSysObjectID, gosnmp.ObjectIdentifier, ".1.3.6.1.4.1.8072.3.2.10")
	f.set(oidSysUpTime, gosnmp.TimeTicks, uint32(100))
	f.set(oidAsusModel, gosnmp.OctetString, []byte("AS6712F"))
	f.set(oidAsusSerial, gosnmp.OctetString, []byte("AS23086712XM000A"))
	f.set(oidAsusVersion, gosnmp.OctetString, []byte("5.1.4.RM62"))
	f.set(oidAsusFanSpeed+".1", gosnmp.Integer, 2129)
	f.set(oidAsusCPUTemp+".1", gosnmp.Integer, 66)
	f.set(oidAsusSysTemp+".1", gosnmp.Integer, 39)
	for idx, d := range map[string][3]any{"1": {"Disk32", "Samsung SSD 990 PRO 4TB", 39}, "2": {"Disk33", "CT4000P3PSSD8", 37}} {
		f.set(oidAsusDiskName+"."+idx, gosnmp.OctetString, []byte(d[0].(string)))
		f.set(oidAsusDiskModel+"."+idx, gosnmp.OctetString, []byte(d[1].(string)))
		f.set(oidAsusDiskStatus+"."+idx, gosnmp.OctetString, []byte(map[string]string{"1": "Good", "2": "Bad"}[idx]))
		f.set(oidAsusDiskTemp+"."+idx, gosnmp.Integer, d[2])
	}
	for idx, v := range map[string][4]any{"1": {"Volume1", "healthy", 3662, 3648}, "7": {"Volume7", "degraded", 1872, 1015}} {
		f.set(oidAsusVolName+"."+idx, gosnmp.OctetString, []byte(v[0].(string)))
		f.set(oidAsusVolStatus+"."+idx, gosnmp.OctetString, []byte(v[1].(string)))
		f.set(oidAsusVolTotal+"."+idx, gosnmp.Integer, v[2])
		f.set(oidAsusVolFree+"."+idx, gosnmp.Integer, v[3])
	}
	d := &devicePoller{cfg: config.SNMPDevice{Profile: "auto"}, cl: f}
	s, err := d.poll(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if s.profile != ProfileAsustor || s.ident.model != "AS6712F" || s.ident.firmware != "ADM 5.1.4.RM62" || s.ident.serial == "" {
		t.Fatalf("identité : %s %+v", s.profile, s.ident)
	}
	b := model.NewSeriesBuilder(0, noTags)
	sampleSeries(b, s, nil, nil)
	m := seriesMap(b.Series())
	if m["snmp.temperature|cpu"] != 66 || m["snmp.temperature|system"] != 39 || m["snmp.fan.speed|fan1"] != 2129 ||
		m["snmp.temperature|disk:Disk32"] != 39 || m["snmp.disk.status|Disk33"] != 3 {
		t.Fatalf("capteurs : %v", m)
	}
	if u := m["snmp.volume.usage|Volume7"]; u < 45.7 || u > 45.8 { // (1872 − 1015) / 1872
		t.Fatalf("volume : %v", m)
	}
	got := map[string]int{}
	for _, c := range healthChecks(s, 0, noTags) {
		got[c.Name+"|"+c.Message] = c.Status
	}
	want := map[string]int{
		"snmp.disk.health|état : Good (Samsung SSD 990 PRO 4TB)": model.CheckOK,
		"snmp.disk.health|état : Bad (CT4000P3PSSD8)":            model.CheckCrit,
		"snmp.volume.health|état : healthy":                      model.CheckOK,
		"snmp.volume.health|état : degraded":                     model.CheckWarn,
	}
	for k, v := range want {
		if st, ok := got[k]; !ok || st != v {
			t.Fatalf("contrôles : %v", got)
		}
	}
}

func TestSynologyAndUPS(t *testing.T) {
	f := fakeClient{}
	f.set(oidSysObjectID, gosnmp.ObjectIdentifier, ".1.3.6.1.4.1.8072.3.2.10")
	f.set(oidSysUpTime, gosnmp.TimeTicks, uint32(100))
	f.set(oidSynoModel, gosnmp.OctetString, []byte("DS920+"))
	f.set(oidSynoTemperature, gosnmp.Integer, 41)
	f.set(oidSynoPower, gosnmp.Integer, 1)
	f.set(oidSynoDiskID+".0", gosnmp.OctetString, []byte("Disk 1"))
	f.set(oidSynoDiskTemp+".0", gosnmp.Integer, 35)
	f.set(oidSynoDiskStatus+".0", gosnmp.Integer, 1)
	// Net-SNMP : tampons et cache retirés de la mémoire occupée.
	for idx, v := range map[string][3]any{
		"1": {"." + hrStorageRAM, "Physical memory", 4000},
		"6": {".1.3.6.1.2.1.25.2.1.1", "Memory buffers", 500},
		"7": {".1.3.6.1.2.1.25.2.1.1", "Cached memory", 1500},
		"31": {"." + hrStorageFixedDisk, "/volume1", 0},
	} {
		f.set(oidHrStorageType+"."+idx, gosnmp.ObjectIdentifier, v[0])
		f.set(oidHrStorageDescr+"."+idx, gosnmp.OctetString, []byte(v[1].(string)))
		f.set(oidHrStorageUnits+"."+idx, gosnmp.Integer, 1024)
		f.set(oidHrStorageSize+"."+idx, gosnmp.Integer, 8000)
		f.set(oidHrStorageUsed+"."+idx, gosnmp.Integer, v[2])
	}
	f.set(oidHrStorageUsed+".31", gosnmp.Integer, 2000)
	d := &devicePoller{cfg: config.SNMPDevice{Profile: "auto"}, cl: f}
	s, err := d.poll(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	b := model.NewSeriesBuilder(0, noTags)
	sampleSeries(b, s, nil, nil)
	m := seriesMap(b.Series())
	if s.profile != ProfileSynology || m["snmp.temperature|disk:Disk 1"] != 35 || m["snmp.psu.status|power"] != 1 {
		t.Fatalf("Synology : %s %v", s.profile, m)
	}
	if m["snmp.memory.usage"] != 25 { // (4000 − 500 − 1500) / 8000
		t.Fatalf("mémoire Net-SNMP : %v", m["snmp.memory.usage"])
	}
	if m["snmp.storage.usage"] != 25 {
		t.Fatalf("stockage : %v", m)
	}

	u := fakeClient{}
	u.set(oidSysObjectID, gosnmp.ObjectIdentifier, ".1.3.6.1.4.1.318.1.3.27")
	u.set(oidSysUpTime, gosnmp.TimeTicks, uint32(100))
	u.set(oidUpsCharge, gosnmp.Integer, 97)
	u.set(oidUpsMinutesLeft, gosnmp.Integer, 42)
	u.set(oidUpsOutputLoad+".1", gosnmp.Integer, 31)
	u.set(oidUpsOutputSource, gosnmp.Integer, 5)
	d = &devicePoller{cfg: config.SNMPDevice{Profile: "auto"}, cl: u}
	s, _ = d.poll(time.Now())
	b = model.NewSeriesBuilder(0, noTags)
	sampleSeries(b, s, nil, nil)
	m = seriesMap(b.Series())
	if m["snmp.ups.battery.charge"] != 97 || m["snmp.ups.battery.runtime"] != 2520 || m["snmp.ups.battery.load"] != 31 || m["snmp.ups.on_battery"] != 1 {
		t.Fatalf("onduleur : %v", m)
	}
}

func TestCounterDeltaAndIdentity(t *testing.T) {
	if d, ok := counterDelta(10, 1<<32-10, false); !ok || d != 20 {
		t.Fatalf("passage par zéro d'un compteur 32 bits : %d %v", d, ok)
	}
	if _, ok := counterDelta(10, 20, true); ok {
		t.Fatal("compteur 64 bits remis à zéro accepté")
	}
	agent := "6f1c2b3a-0000-4000-8000-000000000001"
	a, b := DeviceID(agent, "192.0.2.252"), DeviceID(agent, "192.0.2.40")
	if a == b || a != DeviceID(agent, "192.0.2.252") {
		t.Fatal("identité d'équipement instable")
	}
	if u := uuid.MustParse(a); u.Version() != 5 {
		t.Fatalf("UUID v%d", u.Version())
	}
	if sensorValue(4250, 9, 2) != 42.5 || sensorValue(42, 8, 0) != 0.042 {
		t.Fatal("échelle ENTITY-SENSOR")
	}
}

func TestTrapEvent(t *testing.T) {
	p := &gosnmp.SnmpPacket{Version: gosnmp.Version2c, Variables: []gosnmp.SnmpPDU{
		{Name: ".1.3.6.1.2.1.1.3.0", Type: gosnmp.TimeTicks, Value: uint32(100)},
		{Name: "." + oidSnmpTrapOID, Type: gosnmp.ObjectIdentifier, Value: ".1.3.6.1.6.3.1.1.5.3"},
		{Name: ".1.3.6.1.2.1.31.1.1.1.1.3", Type: gosnmp.OctetString, Value: []byte("ether3")},
		{Name: ".1.3.6.1.2.1.2.2.1.8.3", Type: gosnmp.Integer, Value: 2},
	}}
	ev := trapEvent(p, "192.0.2.252", time.Now())
	if ev.Title != "linkDown" || ev.AlertType != "warning" || ev.Source != "snmp" || ev.Type != "snmp_trap" {
		t.Fatalf("événement : %+v", ev)
	}
	if !strings.Contains(ev.Text, "ifName.3 = ether3") || !strings.Contains(ev.Text, "ifOperStatus.3 = 2") {
		t.Fatalf("variables : %q", ev.Text)
	}
	v1 := &gosnmp.SnmpPacket{Version: gosnmp.Version1}
	v1.GenericTrap, v1.Enterprise = 6, ".1.3.6.1.4.1.318"
	v1.SpecificTrap = 5
	if ev := trapEvent(v1, "203.0.113.9", time.Now()); ev.Title != "1.3.6.1.4.1.318.0.5" {
		t.Fatalf("trap v1 propre au fabricant : %q", ev.Title)
	}
	v1.GenericTrap = 0
	if ev := trapEvent(v1, "203.0.113.9", time.Now()); ev.Title != "coldStart" {
		t.Fatalf("trap v1 générique : %q", ev.Title)
	}
}
