//go:build !windows

package synthetics

import (
	"context"
	"errors"
	"net"
	"os"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

// ping : socket ICMP « datagramme » sans privilège (net.ipv4.ping_group_range) d'abord,
// socket brute ensuite (l'agent tourne en root sous Linux).
func ping(ctx context.Context, ip net.IP, count int, timeout time.Duration) ([]float64, error) {
	v4 := ip.To4() != nil
	nets := []string{"udp6", "ip6:ipv6-icmp"}
	echo, reply, proto := icmp.Type(ipv6.ICMPTypeEchoRequest), icmp.Type(ipv6.ICMPTypeEchoReply), 58
	if v4 {
		nets = []string{"udp4", "ip4:icmp"}
		echo, reply, proto = ipv4.ICMPTypeEcho, ipv4.ICMPTypeEchoReply, 1
	}
	var conn *icmp.PacketConn
	var network string
	var lastErr error
	local := "::"
	if v4 {
		local = "0.0.0.0"
	}
	for _, n := range nets {
		c, err := icmp.ListenPacket(n, local)
		if err == nil {
			conn, network = c, n
			break
		}
		lastErr = err
	}
	if conn == nil {
		if errors.Is(lastErr, os.ErrPermission) {
			return nil, unavailable("le noyau refuse l'écho ICMP sans privilège et l'agent n'a pas le droit des sockets brutes")
		}
		return nil, unavailable(lastErr.Error())
	}
	defer conn.Close()
	id := os.Getpid() & 0xffff
	var dst net.Addr = &net.IPAddr{IP: ip}
	if network == "udp4" || network == "udp6" {
		dst = &net.UDPAddr{IP: ip}
	}
	var rtts []float64
	buf := make([]byte, 1500)
	for seq := 0; seq < count; seq++ {
		msg := icmp.Message{Type: echo, Body: &icmp.Echo{ID: id, Seq: seq, Data: []byte("hypha-synthetics")}}
		wb, err := msg.Marshal(nil)
		if err != nil {
			return nil, err
		}
		t0 := time.Now()
		if _, err := conn.WriteTo(wb, dst); err != nil {
			return rtts, err
		}
		deadline := t0.Add(timeout)
		if cd, ok := ctx.Deadline(); ok && cd.Before(deadline) {
			deadline = cd
		}
		conn.SetReadDeadline(deadline)
		for {
			n, from, err := conn.ReadFrom(buf)
			if err != nil {
				break // délai dépassé : écho perdu
			}
			m, err := icmp.ParseMessage(proto, buf[:n])
			if err != nil || m.Type != reply {
				continue
			}
			e, ok := m.Body.(*icmp.Echo)
			if !ok || e.Seq != seq || !sameIP(from, ip) {
				continue
			}
			// Sur une socket datagramme, le noyau remplace l'identifiant : seule la socket brute le vérifie.
			if (network == "ip4:icmp" || network == "ip6:ipv6-icmp") && e.ID != id {
				continue
			}
			rtts = append(rtts, float64(time.Since(t0).Microseconds())/1000)
			break
		}
		if seq < count-1 && !sleepCtx(ctx, 200*time.Millisecond) {
			break
		}
	}
	return rtts, nil
}

func sameIP(a net.Addr, ip net.IP) bool {
	switch x := a.(type) {
	case *net.UDPAddr:
		return x.IP.Equal(ip)
	case *net.IPAddr:
		return x.IP.Equal(ip)
	}
	return false
}
