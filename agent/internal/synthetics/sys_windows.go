//go:build windows

package synthetics

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// systemNameservers lit les serveurs DNS des cartes actives (GetAdaptersAddresses).
func systemNameservers() []string {
	size := uint32(16 << 10)
	var buf []byte
	for range 3 {
		buf = make([]byte, size)
		err := windows.GetAdaptersAddresses(windows.AF_UNSPEC, windows.GAA_FLAG_SKIP_ANYCAST|windows.GAA_FLAG_SKIP_MULTICAST,
			0, (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])), &size)
		if err == nil {
			break
		}
		if err != windows.ERROR_BUFFER_OVERFLOW {
			return nil
		}
	}
	var out []string
	seen := map[string]bool{}
	for a := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])); a != nil; a = a.Next {
		if a.OperStatus != windows.IfOperStatusUp {
			continue
		}
		for d := a.FirstDnsServerAddress; d != nil; d = d.Next {
			ip := d.Address.IP()
			if ip == nil || ip.IsLinkLocalUnicast() || (ip.To4() == nil && ip[0] == 0xfe && ip[1] == 0xc0) {
				continue // fec0::/10 : adresses de site obsolètes posées par défaut par Windows
			}
			if !seen[ip.String()] {
				seen[ip.String()] = true
				out = append(out, ip.String())
			}
		}
	}
	return out
}
