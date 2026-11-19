// Package snmp interroge les équipements réseau (switchs, box, NAS, imprimantes, onduleurs)
// et les publie comme des hôtes à part entière (docs/CONTRAT-04.md § 14) : chaque équipement
// a son identité (UUID v5 tiré de l'agent et de son adresse), ses métriques snmp.* et ses
// contrôles snmp.can_poll et snmp.interface.status. Les traps reçus deviennent des
// événements. Bibliothèque github.com/gosnmp/gosnmp ; v1, v2c et v3.
package snmp

import (
	"math/big"
	"strconv"
	"strings"
	"unicode"

	"github.com/gosnmp/gosnmp"
)

// OID utilisés, sans point initial (gosnmp les rend avec).
const (
	oidSysDescr    = "1.3.6.1.2.1.1.1.0"
	oidSysObjectID = "1.3.6.1.2.1.1.2.0"
	oidSysUpTime   = "1.3.6.1.2.1.1.3.0"
	oidSysContact  = "1.3.6.1.2.1.1.4.0"
	oidSysName     = "1.3.6.1.2.1.1.5.0"
	oidSysLocation = "1.3.6.1.2.1.1.6.0"

	// IF-MIB : ifTable et ifXTable (colonnes).
	oidIfDescr      = "1.3.6.1.2.1.2.2.1.2"
	oidIfType       = "1.3.6.1.2.1.2.2.1.3"
	oidIfSpeed      = "1.3.6.1.2.1.2.2.1.5"
	oidIfAdmin      = "1.3.6.1.2.1.2.2.1.7"
	oidIfOper       = "1.3.6.1.2.1.2.2.1.8"
	oidIfInOctets   = "1.3.6.1.2.1.2.2.1.10"
	oidIfInDiscards = "1.3.6.1.2.1.2.2.1.13"
	oidIfInErrors   = "1.3.6.1.2.1.2.2.1.14"
	oidIfOutOctets  = "1.3.6.1.2.1.2.2.1.16"
	oidIfOutDisc    = "1.3.6.1.2.1.2.2.1.19"
	oidIfOutErrors  = "1.3.6.1.2.1.2.2.1.20"
	oidIfName       = "1.3.6.1.2.1.31.1.1.1.1"
	oidIfHCIn       = "1.3.6.1.2.1.31.1.1.1.6"
	oidIfHCOut      = "1.3.6.1.2.1.31.1.1.1.10"
	oidIfHighSpeed  = "1.3.6.1.2.1.31.1.1.1.15"
	oidIfAlias      = "1.3.6.1.2.1.31.1.1.1.18"

	// HOST-RESOURCES-MIB.
	oidHrProcessorLoad = "1.3.6.1.2.1.25.3.3.1.2"
	oidHrStorageType   = "1.3.6.1.2.1.25.2.3.1.2"
	oidHrStorageDescr  = "1.3.6.1.2.1.25.2.3.1.3"
	oidHrStorageUnits  = "1.3.6.1.2.1.25.2.3.1.4"
	oidHrStorageSize   = "1.3.6.1.2.1.25.2.3.1.5"
	oidHrStorageUsed   = "1.3.6.1.2.1.25.2.3.1.6"
	oidHrDeviceDescr1  = "1.3.6.1.2.1.25.3.2.1.3.1"
	oidHrPrinterStatus = "1.3.6.1.2.1.25.3.5.1.1"

	hrStorageRAM       = "1.3.6.1.2.1.25.2.1.2"
	hrStorageFixedDisk = "1.3.6.1.2.1.25.2.1.4"
	hrStorageFlash     = "1.3.6.1.2.1.25.2.1.9"
	hrStorageNetwork   = "1.3.6.1.2.1.25.2.1.10"

	// ENTITY-MIB et ENTITY-SENSOR-MIB.
	oidEntName        = "1.3.6.1.2.1.47.1.1.1.1.7"
	oidEntFirmware    = "1.3.6.1.2.1.47.1.1.1.1.9"
	oidEntSoftware    = "1.3.6.1.2.1.47.1.1.1.1.10"
	oidEntSerial      = "1.3.6.1.2.1.47.1.1.1.1.11"
	oidEntModel       = "1.3.6.1.2.1.47.1.1.1.1.13"
	oidEntSensorType  = "1.3.6.1.2.1.99.1.1.1.1"
	oidEntSensorScale = "1.3.6.1.2.1.99.1.1.1.2"
	oidEntSensorPrec  = "1.3.6.1.2.1.99.1.1.1.3"
	oidEntSensorValue = "1.3.6.1.2.1.99.1.1.1.4"

	// MIKROTIK-MIB.
	oidMtxrHealth     = "1.3.6.1.4.1.14988.1.1.3"
	oidMtxrSerial     = "1.3.6.1.4.1.14988.1.1.7.3.0"
	oidMtxrFirmware   = "1.3.6.1.4.1.14988.1.1.7.4.0"
	oidMtxrBoard      = "1.3.6.1.4.1.14988.1.1.7.8.0"
	oidMtxrGaugeName  = "1.3.6.1.4.1.14988.1.1.3.100.1.2"
	oidMtxrGaugeValue = "1.3.6.1.4.1.14988.1.1.3.100.1.3"
	oidMtxrGaugeUnit  = "1.3.6.1.4.1.14988.1.1.3.100.1.4"

	// SYNOLOGY-SYSTEM-MIB, -DISK-MIB, -RAID-MIB.
	oidSynoSystemStatus = "1.3.6.1.4.1.6574.1.1.0"
	oidSynoTemperature  = "1.3.6.1.4.1.6574.1.2.0"
	oidSynoPower        = "1.3.6.1.4.1.6574.1.3.0"
	oidSynoSystemFan    = "1.3.6.1.4.1.6574.1.4.1.0"
	oidSynoCPUFan       = "1.3.6.1.4.1.6574.1.4.2.0"
	oidSynoModel        = "1.3.6.1.4.1.6574.1.5.1.0"
	oidSynoSerial       = "1.3.6.1.4.1.6574.1.5.2.0"
	oidSynoVersion      = "1.3.6.1.4.1.6574.1.5.3.0"
	oidSynoDiskID       = "1.3.6.1.4.1.6574.2.1.1.2"
	oidSynoDiskStatus   = "1.3.6.1.4.1.6574.2.1.1.5"
	oidSynoDiskTemp     = "1.3.6.1.4.1.6574.2.1.1.6"
	oidSynoRaidName     = "1.3.6.1.4.1.6574.3.1.1.2"
	oidSynoRaidStatus   = "1.3.6.1.4.1.6574.3.1.1.3"

	// ASUSTOR (ADM, sous-agent snmpcd, entreprise 44738) ; colonnes relevées sur ADM 5.1.4 :
	// températures en °C, ventilateurs en tr/min, volumes en Gio (total, disponible).
	oidAsusSerial     = "1.3.6.1.4.1.44738.1.1.0"
	oidAsusVersion    = "1.3.6.1.4.1.44738.1.2.0"
	oidAsusModel      = "1.3.6.1.4.1.44738.2.1.0"
	oidAsusFanSpeed   = "1.3.6.1.4.1.44738.2.4.1.2"
	oidAsusCPUTemp    = "1.3.6.1.4.1.44738.2.5.1.2"
	oidAsusSysTemp    = "1.3.6.1.4.1.44738.2.5.1.3"
	oidAsusDiskName   = "1.3.6.1.4.1.44738.4.1.1.2"
	oidAsusDiskModel  = "1.3.6.1.4.1.44738.4.1.1.3"
	oidAsusDiskStatus = "1.3.6.1.4.1.44738.4.1.1.5"
	oidAsusDiskTemp   = "1.3.6.1.4.1.44738.4.1.1.7"
	oidAsusVolName    = "1.3.6.1.4.1.44738.5.1.1.2"
	oidAsusVolStatus  = "1.3.6.1.4.1.44738.5.1.1.4"
	oidAsusVolTotal   = "1.3.6.1.4.1.44738.5.1.1.6"
	oidAsusVolFree    = "1.3.6.1.4.1.44738.5.1.1.7"

	// UPS-MIB (RFC 1628) et PowerNet-MIB (APC).
	oidUpsManufacturer  = "1.3.6.1.2.1.33.1.1.1.0"
	oidUpsModel         = "1.3.6.1.2.1.33.1.1.2.0"
	oidUpsSoftware      = "1.3.6.1.2.1.33.1.1.3.0"
	oidUpsBatteryStatus = "1.3.6.1.2.1.33.1.2.1.0"
	oidUpsMinutesLeft   = "1.3.6.1.2.1.33.1.2.3.0"
	oidUpsCharge        = "1.3.6.1.2.1.33.1.2.4.0"
	oidUpsBatteryTemp   = "1.3.6.1.2.1.33.1.2.7.0"
	oidUpsOutputSource  = "1.3.6.1.2.1.33.1.4.1.0"
	oidUpsOutputLoad    = "1.3.6.1.2.1.33.1.4.4.1.5"
	oidApcModel         = "1.3.6.1.4.1.318.1.1.1.1.1.1.0"
	oidApcSerial        = "1.3.6.1.4.1.318.1.1.1.1.2.3.0"
	oidApcFirmware      = "1.3.6.1.4.1.318.1.1.1.1.2.1.0"
	oidApcOutputStatus  = "1.3.6.1.4.1.318.1.1.1.4.1.1.0"
	oidApcCapacity      = "1.3.6.1.4.1.318.1.1.1.2.2.1.0"
	oidApcBatteryTemp   = "1.3.6.1.4.1.318.1.1.1.2.2.2.0"
	oidApcRunTime       = "1.3.6.1.4.1.318.1.1.1.2.2.3.0"
	oidApcLoad          = "1.3.6.1.4.1.318.1.1.1.4.2.3.0"

	// Printer-MIB.
	oidPrtGeneral       = "1.3.6.1.2.1.43.5.1.1.1.1"
	oidPrtSupplyDescr   = "1.3.6.1.2.1.43.11.1.1.6"
	oidPrtSupplyMax     = "1.3.6.1.2.1.43.11.1.1.8"
	oidPrtSupplyLevel   = "1.3.6.1.2.1.43.11.1.1.9"
	oidSnmpTrapOID      = "1.3.6.1.6.3.1.1.4.1.0"
	oidEnterprisePrefix = "1.3.6.1.4.1."
)

// Profils (§ 14) : generic = MIB-II + IF-MIB + HOST-RESOURCES-MIB, les autres s'y ajoutent.
const (
	ProfileGeneric  = "generic"
	ProfileMikroTik = "mikrotik"
	ProfileSynology = "synology"
	ProfileAsustor  = "asustor"
	ProfilePrinter  = "printer"
	ProfileUPS      = "ups"
)

// Numéros d'entreprise (IANA) des fabricants courants.
var vendors = map[int]string{
	2: "IBM", 9: "Cisco", 11: "HP", 43: "3Com", 171: "D-Link", 253: "Xerox", 311: "Microsoft",
	318: "APC", 367: "Ricoh", 534: "Eaton", 641: "Lexmark", 674: "Dell", 789: "NetApp", 1248: "Epson",
	1347: "Kyocera", 1602: "Canon", 1916: "Extreme Networks", 2011: "Huawei", 2435: "Brother",
	2620: "Check Point", 2636: "Juniper", 3375: "F5", 4526: "Netgear", 6574: "Synology", 6876: "VMware",
	8072: "Net-SNMP", 8741: "SonicWall", 9303: "PacketFront", 11863: "TP-Link", 12356: "Fortinet",
	14823: "Aruba", 14988: "MikroTik", 18334: "Konica Minolta", 24681: "QNAP", 25461: "Palo Alto Networks",
	25506: "H3C", 30065: "Arista", 41112: "Ubiquiti", 44738: "ASUSTOR",
}

// Fabricants qui ne font que des imprimantes (HP, 11, fait aussi des switchs : reconnu par sonde).
var printerVendors = map[int]bool{253: true, 367: true, 641: true, 1248: true, 1347: true, 1602: true, 2435: true, 18334: true}

// enterprise rend le numéro d'entreprise d'un sysObjectID (1.3.6.1.4.1.<n>…), ou 0.
func enterprise(oid string) int {
	oid = strings.TrimPrefix(oid, ".")
	rest, ok := strings.CutPrefix(oid, oidEnterprisePrefix)
	if !ok {
		return 0
	}
	n, _, _ := strings.Cut(rest, ".")
	v, _ := strconv.Atoi(n)
	return v
}

// vendorOf rend le fabricant d'un sysObjectID.
func vendorOf(oid string) string {
	n := enterprise(oid)
	if n == 0 {
		return ""
	}
	if v, ok := vendors[n]; ok {
		return v
	}
	return "entreprise " + strconv.Itoa(n)
}

// detectProfile choisit le profil d'après sysObjectID, puis par sondes (probe rend vrai si
// l'OID existe) pour les équipements qui s'annoncent comme Net-SNMP générique.
func detectProfile(sysObjectID string, probe func(oid string) bool) string {
	oid := strings.TrimPrefix(sysObjectID, ".")
	ent := enterprise(oid)
	switch {
	case ent == 14988:
		return ProfileMikroTik
	case ent == 6574:
		return ProfileSynology
	case ent == 44738:
		return ProfileAsustor
	case ent == 318 || ent == 534 || ent == 935 || strings.HasPrefix(oid, "1.3.6.1.2.1.33"):
		return ProfileUPS
	case printerVendors[ent]:
		return ProfilePrinter
	}
	if probe == nil {
		return ProfileGeneric
	}
	switch {
	case probe(oidSynoModel):
		return ProfileSynology
	case probe(oidAsusModel):
		return ProfileAsustor
	case probe(oidUpsManufacturer):
		return ProfileUPS
	case probe(oidPrtGeneral):
		return ProfilePrinter
	}
	return ProfileGeneric
}

// ── lecture des valeurs ─────────────────────────────────────────────────────────────────

func present(p gosnmp.SnmpPDU) bool {
	switch p.Type {
	case gosnmp.NoSuchObject, gosnmp.NoSuchInstance, gosnmp.EndOfMibView, gosnmp.Null:
		return false
	}
	return p.Value != nil
}

// asUint lit un nombre (Counter32, Counter64, Gauge32, TimeTicks, Integer).
func asUint(p gosnmp.SnmpPDU) (uint64, bool) {
	if !present(p) {
		return 0, false
	}
	switch p.Type {
	case gosnmp.Counter32, gosnmp.Counter64, gosnmp.Gauge32, gosnmp.TimeTicks, gosnmp.Integer, gosnmp.Uinteger32:
		b := gosnmp.ToBigInt(p.Value)
		if b.Sign() < 0 {
			return 0, false
		}
		return b.Uint64(), true
	case gosnmp.OctetString:
		v, err := strconv.ParseUint(strings.TrimSpace(asString(p)), 10, 64)
		return v, err == nil
	}
	return 0, false
}

// asInt lit un entier signé.
func asInt(p gosnmp.SnmpPDU) (int64, bool) {
	if !present(p) {
		return 0, false
	}
	switch p.Type {
	case gosnmp.Counter32, gosnmp.Counter64, gosnmp.Gauge32, gosnmp.TimeTicks, gosnmp.Integer, gosnmp.Uinteger32:
		b := gosnmp.ToBigInt(p.Value)
		if !b.IsInt64() {
			return 0, false
		}
		return b.Int64(), true
	case gosnmp.OctetString:
		v, err := strconv.ParseFloat(strings.TrimSpace(asString(p)), 64)
		if err != nil {
			return 0, false
		}
		return int64(v), true
	}
	return 0, false
}

// asString lit un texte ; les caractères de contrôle deviennent des espaces.
func asString(p gosnmp.SnmpPDU) string {
	if !present(p) {
		return ""
	}
	var s string
	switch v := p.Value.(type) {
	case []byte:
		s = string(v)
	case string:
		s = v
	case *big.Int:
		s = v.String()
	default:
		if n, ok := asInt(p); ok && p.Type != gosnmp.OctetString {
			return strconv.FormatInt(n, 10)
		}
		return ""
	}
	s = strings.Map(func(r rune) rune {
		if r == unicode.ReplacementChar || r == 0 {
			return -1
		}
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// indexOf rend le suffixe d'index d'un OID de colonne (« 1.3.6.1.2.1.2.2.1.2.5 » → « 5 »).
func indexOf(name, column string) (string, bool) {
	name = strings.TrimPrefix(name, ".")
	rest, ok := strings.CutPrefix(name, column+".")
	return rest, ok && rest != ""
}

// column range une colonne parcourue par index.
func column(pdus []gosnmp.SnmpPDU, col string) map[string]gosnmp.SnmpPDU {
	out := make(map[string]gosnmp.SnmpPDU, len(pdus))
	for _, p := range pdus {
		if idx, ok := indexOf(p.Name, col); ok && present(p) {
			out[idx] = p
		}
	}
	return out
}

// Noms des OID de traps et de variables courants.
var oidNames = map[string]string{
	"1.3.6.1.6.3.1.1.5.1": "coldStart", "1.3.6.1.6.3.1.1.5.2": "warmStart", "1.3.6.1.6.3.1.1.5.3": "linkDown",
	"1.3.6.1.6.3.1.1.5.4": "linkUp", "1.3.6.1.6.3.1.1.5.5": "authenticationFailure", "1.3.6.1.6.3.1.1.5.6": "egpNeighborLoss",
	"1.3.6.1.2.1.33.2.1": "upsTrapOnBattery", "1.3.6.1.2.1.33.2.2": "upsTrapTestCompleted",
	"1.3.6.1.2.1.33.2.3": "upsTrapAlarmEntryAdded", "1.3.6.1.2.1.33.2.4": "upsTrapAlarmEntryRemoved",
	"1.3.6.1.2.1.47.2.0.1": "entConfigChange",
	"1.3.6.1.2.1.1.3":      "sysUpTime", "1.3.6.1.6.3.1.1.4.1": "snmpTrapOID", "1.3.6.1.6.3.1.1.4.3": "snmpTrapEnterprise",
	"1.3.6.1.2.1.2.2.1.1": "ifIndex", "1.3.6.1.2.1.2.2.1.2": "ifDescr", "1.3.6.1.2.1.2.2.1.7": "ifAdminStatus",
	"1.3.6.1.2.1.2.2.1.8": "ifOperStatus", "1.3.6.1.2.1.31.1.1.1.1": "ifName", "1.3.6.1.2.1.31.1.1.1.18": "ifAlias",
	"1.3.6.1.2.1.1.5": "sysName",
}

// oidLabel rend « nom.index » pour un OID connu, sinon l'OID lui-même.
func oidLabel(oid string) string {
	oid = strings.TrimPrefix(oid, ".")
	if n, ok := oidNames[oid]; ok {
		return n
	}
	for base := oid; ; {
		i := strings.LastIndex(base, ".")
		if i < 0 {
			return oid
		}
		base = base[:i]
		if n, ok := oidNames[base]; ok {
			return n + oid[len(base):]
		}
	}
}
