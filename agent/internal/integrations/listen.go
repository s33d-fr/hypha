package integrations

import (
	"encoding/hex"
	"net"
	"os"
	"strconv"
	"strings"

	"s33d.fr/hypha/agent/internal/hostfs"
)

// listenAddrs : adresses locales sur lesquelles un port TCP est en écoute, lues dans
// /proc/net/tcp et tcp6 (Linux). Lecture locale seule : aucune connexion n'est tentée.
// Une écoute sur toutes les adresses rend 127.0.0.1 ; une adresse IPv4 vue en IPv6
// (::ffff:a.b.c.d) rend l'adresse IPv4.
func listenAddrs(port int) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range []string{"tcp", "tcp6"} {
		data, err := os.ReadFile(hostfs.Proc("net", f))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n")[1:] {
			fields := strings.Fields(line)
			if len(fields) < 4 || fields[3] != "0A" { // 0A : LISTEN
				continue
			}
			ipHex, portHex, ok := strings.Cut(fields[1], ":")
			if !ok {
				continue
			}
			if p, err := strconv.ParseUint(portHex, 16, 16); err != nil || int(p) != port {
				continue
			}
			ip := decodeProcIP(ipHex)
			if ip == nil {
				continue
			}
			addr := ip.String()
			if ip.IsUnspecified() {
				addr = "127.0.0.1"
			} else if v4 := ip.To4(); v4 != nil {
				addr = v4.String()
			} else {
				continue // IPv6 seule : le parc coupe l'IPv6
			}
			if !seen[addr] {
				seen[addr] = true
				out = append(out, addr)
			}
		}
	}
	return out
}

// decodeProcIP : adresse de /proc/net/tcp*, mots de 32 bits en ordre petit-boutiste.
func decodeProcIP(h string) net.IP {
	b, err := hex.DecodeString(h)
	if err != nil || (len(b) != 4 && len(b) != 16) {
		return nil
	}
	for i := 0; i+4 <= len(b); i += 4 {
		b[i], b[i+1], b[i+2], b[i+3] = b[i+3], b[i+2], b[i+1], b[i]
	}
	return net.IP(b)
}
