//go:build windows

package metrics

import (
	"fmt"
	"strings"
	"time"
	"unsafe"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
	gnet "github.com/shirou/gopsutil/v4/net"
	"golang.org/x/sys/windows"

	"s33d.fr/hypha/agent/internal/model"
)

// Ce que Windows sait mesurer et Linux non (contrat § 4).
const (
	hasIowait    = false
	hasInterrupt = true
	hasAwait     = false
	hasInodes    = false
)

type osState struct{}

var (
	modpsapi               = windows.NewLazySystemDLL("psapi.dll")
	procGetPerformanceInfo = modpsapi.NewProc("GetPerformanceInfo")
)

// performanceInformation reprend PERFORMANCE_INFORMATION (psapi.h) : les SIZE_T sont
// des uintptr, les DWORD des uint32.
type performanceInformation struct {
	cb                uint32
	commitTotal       uintptr
	commitLimit       uintptr
	commitPeak        uintptr
	physicalTotal     uintptr
	physicalAvailable uintptr
	systemCache       uintptr
	kernelTotal       uintptr
	kernelPaged       uintptr
	kernelNonpaged    uintptr
	pageSize          uintptr
	handleCount       uint32
	processCount      uint32
	threadCount       uint32
}

func getPerformanceInfo() (*performanceInformation, error) {
	var pi performanceInformation
	pi.cb = uint32(unsafe.Sizeof(pi))
	r, _, err := procGetPerformanceInfo.Call(uintptr(unsafe.Pointer(&pi)), uintptr(pi.cb))
	if r == 0 {
		return nil, fmt.Errorf("GetPerformanceInfo : %w", err)
	}
	return &pi, nil
}

// readCPU additionne les compteurs de tous les processeurs. Le temps noyau de Windows
// contient le temps inactif (ôté par gopsutil) et le temps d'interruption (gardé dans
// "system", comme le « % temps privilégié » de Windows, et rendu à part).
func readCPU() (cpuTimes, error) {
	t, err := cpu.Times(false)
	if err != nil {
		return cpuTimes{}, err
	}
	if len(t) == 0 {
		return cpuTimes{}, fmt.Errorf("aucun compteur processeur")
	}
	c := t[0]
	return cpuTimes{
		user:      c.User,
		system:    c.System,
		idle:      c.Idle,
		interrupt: c.Irq,
		total:     c.User + c.System + c.Idle,
	}, nil
}

// collectOS : mémoire (dont mémoire engagée), fichier d'échange, processus, handles.
func (s *System) collectOS(b *model.SeriesBuilder, _ float64, _ int) []error {
	var errs []error

	if v, err := mem.VirtualMemory(); err != nil {
		errs = append(errs, fmt.Errorf("mem : %w", err))
	} else {
		used := v.Total - min(v.Available, v.Total)
		b.Gauge("system.mem.total", "byte", float64(v.Total))
		b.Gauge("system.mem.used", "byte", float64(used))
		b.Gauge("system.mem.free", "byte", float64(v.Available))
		b.Gauge("system.mem.usable", "byte", float64(v.Available))
		if v.Total > 0 {
			b.Gauge("system.mem.pct_used", "percent", float64(used)/float64(v.Total)*100)
		}
	}

	pi, err := getPerformanceInfo()
	if err != nil {
		errs = append(errs, err)
	} else {
		page := float64(pi.pageSize)
		b.Gauge("system.mem.committed", "byte", float64(pi.commitTotal)*page)
		b.Gauge("system.mem.commit_limit", "byte", float64(pi.commitLimit)*page)
		// Windows n'a pas d'état « en cours d'exécution » par processus : running vaut le
		// nombre de processus vivants (à lire comme tel) ; pas de zombie sous Windows.
		b.Gauge("system.processes.count", "count", float64(pi.processCount))
		b.Gauge("system.processes.running", "count", float64(pi.processCount))
		b.Gauge("system.file_handles.in_use", "count", float64(pi.handleCount))
	}

	// Fichiers d'échange : somme de tous les pagefile.sys.
	if devs, err := mem.SwapDevices(); err != nil {
		errs = append(errs, fmt.Errorf("fichier d'échange : %w", err))
	} else {
		var used, free uint64
		for _, d := range devs {
			used += d.UsedBytes
			free += d.FreeBytes
		}
		total := used + free
		b.Gauge("system.swap.total", "byte", float64(total))
		b.Gauge("system.swap.used", "byte", float64(used))
		b.Gauge("system.swap.free", "byte", float64(free))
		pct := 0.0
		if total > 0 {
			pct = float64(used) / float64(total) * 100
		}
		b.Gauge("system.swap.pct_used", "percent", pct)
	}
	return errs
}

// diskPerformance reprend DISK_PERFORMANCE (winioctl.h). Les temps sont en unités de 100 ns.
type diskPerformance struct {
	BytesRead           int64
	BytesWritten        int64
	ReadTime            int64
	WriteTime           int64
	IdleTime            int64
	ReadCount           uint32
	WriteCount          uint32
	QueueDepth          uint32
	SplitCount          uint32
	QueryTime           int64
	StorageDeviceNumber uint32
	StorageManagerName  [8]uint16
}

const ioctlDiskPerformance = 0x70020

// readDiskIO interroge chaque lecteur fixe (C:, D:…). L'occupation se déduit du temps
// inactif rapporté au temps écoulé entre deux relevés.
func readDiskIO() (map[string]ioCounters, error) {
	buf := make([]uint16, 254)
	n, err := windows.GetLogicalDriveStrings(uint32(len(buf)), &buf[0])
	if err != nil {
		return nil, err
	}
	out := map[string]ioCounters{}
	// La liste est « C:\ », zéro, « D:\ », zéro… : UTF16ToString s'arrêterait au premier
	// zéro, on la découpe donc à la main.
	start := 0
	for i := 0; i < int(n) && i < len(buf); i++ {
		if buf[i] != 0 {
			continue
		}
		if i > start {
			_ = readDrive(windows.UTF16ToString(buf[start:i]), out) // lecteur illisible : ignoré
		}
		start = i + 1
	}
	return out, nil
}

func readDrive(root string, out map[string]ioCounters) error {
	rootp, _ := windows.UTF16PtrFromString(root)
	if windows.GetDriveType(rootp) != windows.DRIVE_FIXED {
		return nil
	}
	letter := strings.TrimSuffix(root, `\`) // "C:"
	dev, _ := windows.UTF16PtrFromString(`\\.\` + letter)
	h, err := windows.CreateFile(dev, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	var p diskPerformance
	var ret uint32
	if err := windows.DeviceIoControl(h, ioctlDiskPerformance, nil, 0, (*byte)(unsafe.Pointer(&p)), uint32(unsafe.Sizeof(p)), &ret, nil); err != nil {
		return err
	}
	out[letter] = ioCounters{
		readBytes:  uint64(p.BytesRead),
		writeBytes: uint64(p.BytesWritten),
		reads:      uint64(p.ReadCount),
		writes:     uint64(p.WriteCount),
		// busyMs = (heure murale − temps inactif cumulé) en ms : entre deux relevés, la
		// différence vaut le temps écoulé moins le temps inactif, soit le temps de travail.
		busyMs: float64(time.Now().UnixMilli()) - float64(p.IdleTime)/10000,
	}
	return nil
}

// readTCPStates compte les connexions TCP (table de pile Windows, IPv4 et IPv6).
func readTCPStates() (map[string]int, error) {
	conns, err := gnet.Connections("tcp")
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, c := range conns {
		out[NormalizeTCPState(c.Status)]++
	}
	return out, nil
}
