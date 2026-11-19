//go:build windows

package synthetics

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Écho ICMP par l'API IcmpSendEcho d'iphlpapi : sans droit particulier, IPv4 seulement.
var (
	iphlpapi            = windows.NewLazySystemDLL("iphlpapi.dll")
	procIcmpCreateFile  = iphlpapi.NewProc("IcmpCreateFile")
	procIcmpCloseHandle = iphlpapi.NewProc("IcmpCloseHandle")
	procIcmpSendEcho    = iphlpapi.NewProc("IcmpSendEcho")
)

// icmpEchoReply reprend ICMP_ECHO_REPLY (ipexport.h) pour une plateforme 64 bits.
type icmpEchoReply struct {
	Address       uint32
	Status        uint32
	RoundTripTime uint32
	DataSize      uint16
	Reserved      uint16
	Data          uintptr
	Options       [16]byte
}

func ping(ctx context.Context, ip net.IP, count int, timeout time.Duration) ([]float64, error) {
	v4 := ip.To4()
	if v4 == nil {
		return nil, unavailable("écho ICMP IPv6 non pris en charge par l'agent Windows")
	}
	if err := procIcmpSendEcho.Find(); err != nil {
		return nil, unavailable("iphlpapi.dll : " + err.Error())
	}
	h, _, err := procIcmpCreateFile.Call()
	if windows.Handle(h) == windows.InvalidHandle {
		return nil, unavailable(fmt.Sprintf("IcmpCreateFile : %v", err))
	}
	defer procIcmpCloseHandle.Call(h)
	dst := binary.LittleEndian.Uint32(v4) // IPAddr : octets dans l'ordre du réseau
	data := []byte("hypha-synthetics")
	reply := make([]byte, int(unsafe.Sizeof(icmpEchoReply{}))+len(data)+64)
	var rtts []float64
	for seq := 0; seq < count; seq++ {
		t0 := time.Now()
		n, _, _ := procIcmpSendEcho.Call(h, uintptr(dst), uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)), 0,
			uintptr(unsafe.Pointer(&reply[0])), uintptr(len(reply)), uintptr(timeout.Milliseconds()))
		if n > 0 {
			r := (*icmpEchoReply)(unsafe.Pointer(&reply[0]))
			if r.Status == 0 {
				rt := float64(r.RoundTripTime)
				if rt == 0 { // résolution de la milliseconde : mesure locale plus fine
					rt = float64(time.Since(t0).Microseconds()) / 1000
				}
				rtts = append(rtts, rt)
			}
		}
		if seq < count-1 && !sleepCtx(ctx, 200*time.Millisecond) {
			break
		}
	}
	return rtts, nil
}
