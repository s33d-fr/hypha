//go:build !windows

package synthetics

import (
	"bufio"
	"os"
	"strings"

	"s33d.fr/hypha/agent/internal/hostfs"
)

// systemNameservers lit les serveurs de /etc/resolv.conf.
func systemNameservers() []string {
	f, err := os.Open(hostfs.Etc("resolv.conf"))
	if err != nil {
		return nil
	}
	defer f.Close()
	return parseResolvConf(bufio.NewScanner(f))
}

func parseResolvConf(sc *bufio.Scanner) []string {
	var out []string
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) >= 2 && f[0] == "nameserver" {
			out = append(out, strings.SplitN(f[1], "%", 2)[0])
		}
	}
	return out
}
