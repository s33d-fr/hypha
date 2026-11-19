// Package netconn fait l'instantané des connexions réseau (contrat § 3.7), regroupées
// pour que 2 000 connexions d'un serveur web vers sa base tiennent en une ligne.
package netconn

import (
	"context"
	"sort"
	"strconv"
	"syscall"

	gnet "github.com/shirou/gopsutil/v4/net"

	"s33d.fr/hypha/agent/internal/metrics"
	"s33d.fr/hypha/agent/internal/model"
)

const maxGroups = 300

// Collect relève les sockets TCP et UDP (IPv4 et IPv6) et les regroupe.
//
// Direction : listening = socket en écoute (TCP LISTEN, UDP sans pair) ; incoming = port
// local sur lequel l'hôte écoute ; outgoing = le reste.
//
// Regroupement : pour une connexion sortante, le port local (éphémère) ne compte pas ;
// pour une connexion entrante, c'est le port distant (éphémère, côté client) qui ne compte
// pas, sinon chaque client ferait son propre groupe.
func Collect(ctx context.Context, nameOf func(int32) string) ([]model.Connection, error) {
	conns, err := gnet.ConnectionsWithContext(ctx, "inet")
	if err != nil {
		return nil, err
	}

	listenTCP := map[uint32]bool{}
	listenUDP := map[uint32]bool{}
	for _, c := range conns {
		switch {
		case c.Type == syscall.SOCK_STREAM && c.Status == "LISTEN":
			listenTCP[c.Laddr.Port] = true
		case c.Type == syscall.SOCK_DGRAM && c.Raddr.Port == 0:
			listenUDP[c.Laddr.Port] = true
		}
	}

	groups := map[string]*model.Connection{}
	for _, c := range conns {
		proto := "tcp"
		listening := listenTCP
		if c.Type == syscall.SOCK_DGRAM {
			proto, listening = "udp", listenUDP
		} else if c.Type != syscall.SOCK_STREAM {
			continue
		}
		if c.Family == syscall.AF_INET6 {
			proto += "6"
		}
		state := metrics.NormalizeTCPState(c.Status)
		if proto[0] == 'u' {
			state = ""
		}
		g := model.Connection{
			Proto: proto, Laddr: c.Laddr.IP, Lport: c.Laddr.Port,
			Raddr: c.Raddr.IP, Rport: c.Raddr.Port, State: state, PID: c.Pid,
		}
		switch {
		case state == "LISTEN" || (proto[0] == 'u' && c.Raddr.Port == 0):
			g.Direction, g.Raddr, g.Rport = "listening", "", 0
		case listening[c.Laddr.Port]:
			g.Direction, g.Rport = "incoming", 0
		default:
			g.Direction, g.Lport = "outgoing", 0
		}
		g.Process = nameOf(c.Pid)
		key := g.Proto + "|" + g.Direction + "|" + g.Raddr + "|" + strconv.FormatUint(uint64(g.Rport), 10) + "|" +
			strconv.FormatUint(uint64(g.Lport), 10) + "|" + g.State + "|" + g.Process
		if prev, ok := groups[key]; ok {
			prev.Count++
			continue
		}
		g.Count = 1
		groups[key] = &g
	}

	out := make([]model.Connection, 0, len(groups))
	for _, g := range groups {
		out = append(out, *g)
	}
	// Les écoutes d'abord (peu nombreuses, et ce sont elles qui disent ce que sert l'hôte),
	// puis les groupes les plus fournis.
	sort.Slice(out, func(i, j int) bool {
		li, lj := out[i].Direction == "listening", out[j].Direction == "listening"
		if li != lj {
			return li
		}
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Raddr < out[j].Raddr
	})
	if len(out) > maxGroups {
		out = out[:maxGroups]
	}
	return out, nil
}
