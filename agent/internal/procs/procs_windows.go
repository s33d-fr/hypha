//go:build windows

package procs

import (
	"unsafe"

	"github.com/shirou/gopsutil/v4/process"
	"golang.org/x/sys/windows"
)

var (
	modpsapi                 = windows.NewLazySystemDLL("psapi.dll")
	procGetProcessMemoryInfo = modpsapi.NewProc("GetProcessMemoryInfo")
	modkernel32              = windows.NewLazySystemDLL("kernel32.dll")
	procGetProcessIoCounters = modkernel32.NewProc("GetProcessIoCounters")
)

// processMemoryCounters reprend PROCESS_MEMORY_COUNTERS (psapi.h).
type processMemoryCounters struct {
	cb                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

// ioCountersWin reprend IO_COUNTERS (winnt.h). Sous Windows, ces compteurs incluent
// toutes les E/S du processus (fichiers, mais aussi réseau et périphériques).
type ioCountersWin struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

// sysReader : un seul instantané Toolhelp donne pid, parent, nom et nombre de fils de
// tous les processus ; les temps, la mémoire et les E/S demandent ensuite un handle par
// processus, ouvert avec le droit minimal.
type sysReader struct {
	users map[string]string // SID → DOMAINE\compte
}

func newSysReader() *sysReader { return &sysReader{users: map[string]string{}} }

func (r *sysReader) scan(needCmdline bool) ([]sample, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	var out []sample
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		if e.ProcessID == 0 {
			continue // « processus inactif du système » : son « CPU » est le temps libre
		}
		s := sample{
			pid:     int32(e.ProcessID),
			ppid:    int32(e.ParentProcessID),
			name:    windows.UTF16ToString(e.ExeFile[:]),
			threads: int32(e.Threads),
		}
		r.fill(&s)
		if needCmdline {
			s.cmdline = cmdline(s.pid)
		}
		out = append(out, s)
	}
	return out, nil
}

func (r *sysReader) fill(s *sample) {
	// PROCESS_VM_READ est exigé par GetProcessMemoryInfo ; il est refusé pour les processus
	// protégés : on retente alors sans, et la mémoire reste à zéro pour ceux-là.
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_VM_READ, false, uint32(s.pid))
	if err != nil {
		if h, err = windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(s.pid)); err != nil {
			return
		}
	}
	defer windows.CloseHandle(h)
	var created, exited, kernel, user windows.Filetime
	if windows.GetProcessTimes(h, &created, &exited, &kernel, &user) == nil {
		s.cpuSeconds = float64(ft100ns(kernel)+ft100ns(user)) / 1e7
		s.startMs = created.Nanoseconds() / 1e6
	}
	mc := processMemoryCounters{}
	mc.cb = uint32(unsafe.Sizeof(mc))
	if ok, _, _ := procGetProcessMemoryInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&mc)), uintptr(mc.cb)); ok != 0 {
		s.rss = uint64(mc.WorkingSetSize)
	}
	var io ioCountersWin
	if ok, _, _ := procGetProcessIoCounters.Call(uintptr(h), uintptr(unsafe.Pointer(&io))); ok != 0 {
		s.ioRead, s.ioWrite = io.ReadTransferCount, io.WriteTransferCount
	}
}

// ft100ns convertit une durée FILETIME (unités de 100 ns).
func ft100ns(f windows.Filetime) uint64 { return uint64(f.HighDateTime)<<32 | uint64(f.LowDateTime) }

func cmdline(pid int32) string {
	p, err := process.NewProcess(pid)
	if err != nil {
		return ""
	}
	c, err := p.Cmdline()
	if err != nil {
		return ""
	}
	return c
}

// enrich ajoute l'utilisateur (DOMAINE\compte) et la ligne de commande d'un processus retenu.
func (r *sysReader) enrich(s *sample) {
	if s.enriched {
		return
	}
	s.enriched = true
	if s.cmdline == "" {
		s.cmdline = cmdline(s.pid)
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(s.pid))
	if err != nil {
		return
	}
	defer windows.CloseHandle(h)
	var tok windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &tok); err != nil {
		return
	}
	defer tok.Close()
	tu, err := tok.GetTokenUser()
	if err != nil {
		return
	}
	sid := tu.User.Sid.String()
	if name, ok := r.users[sid]; ok {
		s.user = name
		return
	}
	name := sid
	if acc, dom, _, err := tu.User.Sid.LookupAccount(""); err == nil {
		name = acc
		if dom != "" {
			name = dom + `\` + acc
		}
	}
	r.users[sid] = name
	s.user = name
}

// NewNameResolver rend une fonction pid → nom du processus (un seul instantané Toolhelp,
// pris au premier appel).
func NewNameResolver() func(pid int32) string {
	var names map[int32]string
	return func(pid int32) string {
		if names == nil {
			names = map[int32]string{}
			snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
			if err == nil {
				var e windows.ProcessEntry32
				e.Size = uint32(unsafe.Sizeof(e))
				for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
					names[int32(e.ProcessID)] = windows.UTF16ToString(e.ExeFile[:])
				}
				windows.CloseHandle(snap)
			}
		}
		return names[pid]
	}
}
