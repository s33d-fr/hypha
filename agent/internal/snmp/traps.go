package snmp

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gosnmp/gosnmp"

	"s33d.fr/hypha/agent/internal/model"
)

// Au plus 60 traps par minute deviennent des événements : une tempête de traps (port qui
// bat) ne doit pas noyer le serveur.
const trapsPerMinute = 60

// ListenTraps reçoit les traps v1 et v2c (community de la conf) jusqu'à l'arrêt ; chaque trap
// devient un événement source=snmp, type=snmp_trap, rattaché à l'équipement émetteur s'il
// est dans la conf, sinon à l'hôte de l'agent avec l'étiquette snmp_device.
func (m *Manager) ListenTraps(ctx context.Context) error {
	tl := gosnmp.NewTrapListener()
	tl.Params = &gosnmp.GoSNMP{Version: gosnmp.Version2c, Community: m.cfg.Traps.Community, Timeout: 5 * time.Second}
	var mu sync.Mutex
	window, count, dropped := time.Now(), 0, 0
	tl.OnNewTrap = func(p *gosnmp.SnmpPacket, from *net.UDPAddr) {
		defer func() {
			if r := recover(); r != nil {
				m.o.Log.Error("trap SNMP illisible, ignoré", "de", from, "err", r)
			}
		}()
		if c := m.cfg.Traps.Community; c != "" && p.Version != gosnmp.Version3 && p.Community != c {
			return
		}
		mu.Lock()
		if time.Since(window) > time.Minute {
			if dropped > 0 {
				m.o.Log.Warn("traps SNMP au-delà du plafond, ignorés", "ignorés", dropped)
			}
			window, count, dropped = time.Now(), 0, 0
		}
		count++
		over := count > trapsPerMinute
		if over {
			dropped++
		}
		mu.Unlock()
		if over {
			return
		}
		m.handleTrap(p, from.IP.String(), time.Now())
	}
	errc := make(chan error, 1)
	go func() { errc <- tl.Listen(m.cfg.Traps.Listen) }()
	select {
	case <-tl.Listening():
	case err := <-errc:
		return err
	case <-time.After(5 * time.Second):
	}
	<-ctx.Done()
	tl.Close()
	return nil
}

func (m *Manager) handleTrap(p *gosnmp.SnmpPacket, ip string, now time.Time) {
	ev := trapEvent(p, ip, now)
	d := m.deviceByIP(ip)
	fields := map[string]any{}
	if d != nil {
		ev.Tags = m.tagsFor(d)()
		fields["agent"] = d.info
	} else {
		ev.Tags = m.agentTags("snmp_device", ip)
	}
	fields["events"] = []model.Event{ev}
	m.o.Post(routeEvents, fields)
}

// trapEvent traduit un trap : titre = nom de l'OID du trap (ou l'OID), texte = variables.
func trapEvent(p *gosnmp.SnmpPacket, ip string, now time.Time) model.Event {
	trapOID := ""
	vars := p.Variables
	if p.Version == gosnmp.Version1 {
		// v1 : OID du trap reconstitué (RFC 3584 § 3.1).
		if p.GenericTrap >= 0 && p.GenericTrap < 6 {
			trapOID = "1.3.6.1.6.3.1.1.5." + strconv.Itoa(p.GenericTrap+1)
		} else {
			trapOID = strings.TrimPrefix(p.Enterprise, ".") + ".0." + strconv.Itoa(p.SpecificTrap)
		}
	}
	var lines []string
	for _, v := range vars {
		name := strings.TrimPrefix(v.Name, ".")
		if name == oidSnmpTrapOID {
			if s, ok := v.Value.(string); ok {
				trapOID = strings.TrimPrefix(s, ".")
			}
			continue
		}
		if name == oidSysUpTime || name == "1.3.6.1.2.1.1.3" {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s = %s", oidLabel(name), trapValue(v)))
	}
	title := oidLabel(trapOID)
	if title == "" {
		title = "trap SNMP"
	}
	alert := "info"
	switch title {
	case "linkDown", "authenticationFailure", "upsTrapAlarmEntryAdded":
		alert = "warning"
	case "upsTrapOnBattery":
		alert = "error"
	case "linkUp", "upsTrapAlarmEntryRemoved":
		alert = "success"
	}
	text := strings.Join(lines, "\n")
	if text == "" {
		text = "trap " + trapOID + " reçu de " + ip
	}
	return model.Event{
		TS: model.Ms(now), Source: "snmp", Type: "snmp_trap", Title: model.Truncate(title, 200),
		Text: model.Truncate(text, 4096), AlertType: alert, Priority: "normal",
		AggregationKey: "snmp_trap:" + ip + ":" + trapOID,
	}
}

func trapValue(v gosnmp.SnmpPDU) string {
	switch v.Type {
	case gosnmp.OctetString:
		return asString(v)
	case gosnmp.ObjectIdentifier:
		s, _ := v.Value.(string)
		return oidLabel(s)
	case gosnmp.IPAddress:
		s, _ := v.Value.(string)
		return s
	case gosnmp.TimeTicks:
		if n, ok := asUint(v); ok {
			return (time.Duration(n) * 10 * time.Millisecond).String()
		}
	}
	if n, ok := asInt(v); ok {
		return strconv.FormatInt(n, 10)
	}
	return fmt.Sprint(v.Value)
}
