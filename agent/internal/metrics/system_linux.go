//go:build linux

package metrics

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"

	"s33d.fr/hypha/agent/internal/hostfs"
	"s33d.fr/hypha/agent/internal/model"
)

// Ce que Linux sait mesurer et Windows non (contrat § 4, colonnes Linux / Windows).
const (
	hasIowait    = true
	hasInterrupt = false
	hasAwait     = true
	hasInodes    = true
)

type osState struct{}

// readCPU lit /proc/stat. "user" compte aussi nice (temps passé en espace utilisateur),
// "system" compte les interruptions ; guest est déjà inclus dans user par le noyau et
// n'entre pas dans le total (il serait compté deux fois).
func readCPU() (cpuTimes, error) {
	t, err := cpu.Times(false)
	if err != nil {
		return cpuTimes{}, err
	}
	if len(t) == 0 {
		return cpuTimes{}, fmt.Errorf("/proc/stat vide")
	}
	c := t[0]
	user := c.User + c.Nice
	system := c.System + c.Irq + c.Softirq
	return cpuTimes{
		user:   user,
		system: system,
		idle:   c.Idle,
		iowait: c.Iowait,
		steal:  c.Steal,
		total:  user + system + c.Idle + c.Iowait + c.Steal,
	}, nil
}

// collectOS : charge, mémoire, échange, processus, descripteurs.
func (s *System) collectOS(b *model.SeriesBuilder, _ float64, cores int) []error {
	var errs []error

	if l, err := load.Avg(); err != nil {
		errs = append(errs, fmt.Errorf("load : %w", err))
	} else {
		b.Gauge("system.load.1", "count", l.Load1)
		b.Gauge("system.load.5", "count", l.Load5)
		b.Gauge("system.load.15", "count", l.Load15)
		if cores > 0 {
			b.Gauge("system.load.norm.1", "count", l.Load1/float64(cores))
		}
	}

	// « used » = total − disponible : la mémoire réellement occupée, hors cache récupérable.
	// Un seuil sur pct_used signale ainsi un vrai manque, pas un cache bien rempli.
	if v, err := mem.VirtualMemory(); err != nil {
		errs = append(errs, fmt.Errorf("mem : %w", err))
	} else {
		used := v.Total - min(v.Available, v.Total)
		b.Gauge("system.mem.total", "byte", float64(v.Total))
		b.Gauge("system.mem.used", "byte", float64(used))
		b.Gauge("system.mem.free", "byte", float64(v.Free))
		b.Gauge("system.mem.usable", "byte", float64(v.Available))
		b.Gauge("system.mem.cached", "byte", float64(v.Cached))
		b.Gauge("system.mem.buffered", "byte", float64(v.Buffers))
		if v.Total > 0 {
			b.Gauge("system.mem.pct_used", "percent", float64(used)/float64(v.Total)*100)
		}
	}

	if sw, err := mem.SwapMemory(); err != nil {
		errs = append(errs, fmt.Errorf("swap : %w", err))
	} else {
		b.Gauge("system.swap.total", "byte", float64(sw.Total))
		b.Gauge("system.swap.used", "byte", float64(sw.Used))
		b.Gauge("system.swap.free", "byte", float64(sw.Free))
		pct := 0.0
		if sw.Total > 0 {
			pct = float64(sw.Used) / float64(sw.Total) * 100
		}
		b.Gauge("system.swap.pct_used", "percent", pct)
	}

	if total, running, zombie, err := processStates(); err != nil {
		errs = append(errs, fmt.Errorf("processus : %w", err))
	} else {
		b.Gauge("system.processes.count", "count", float64(total))
		b.Gauge("system.processes.running", "count", float64(running))
		b.Gauge("system.processes.zombie", "count", float64(zombie))
	}

	// /proc/sys/fs/file-nr : alloués, alloués mais libres (toujours 0 depuis le noyau 2.6), maximum.
	if data, err := os.ReadFile(hostfs.Proc("sys/fs/file-nr")); err != nil {
		errs = append(errs, fmt.Errorf("file-nr : %w", err))
	} else if f := strings.Fields(string(data)); len(f) >= 2 {
		alloc, _ := strconv.ParseFloat(f[0], 64)
		free, _ := strconv.ParseFloat(f[1], 64)
		b.Gauge("system.file_handles.in_use", "count", alloc-free)
	}
	return errs
}

// processStates compte les processus (répertoires numériques de /proc) et les zombies
// (état Z dans /proc/<pid>/stat). « running » vient de procs_running dans /proc/stat :
// le nombre de tâches exécutables vu par l'ordonnanceur. L'état d'un processus dans
// /proc/<pid>/stat n'est que celui de son fil principal, souvent endormi alors que d'autres
// fils travaillent : le compter sous-estimerait l'activité.
func processStates() (total, running, zombie int, err error) {
	d, err := os.Open(hostfs.Proc())
	if err != nil {
		return 0, 0, 0, err
	}
	names, err := d.Readdirnames(-1)
	d.Close()
	if err != nil {
		return 0, 0, 0, err
	}
	for _, n := range names {
		if n[0] < '0' || n[0] > '9' {
			continue
		}
		data, err := os.ReadFile(hostfs.Proc(n, "stat"))
		if err != nil {
			continue // processus terminé entre-temps
		}
		i := bytes.LastIndexByte(data, ')')
		if i < 0 || i+2 >= len(data) {
			continue
		}
		total++
		if data[i+2] == 'Z' {
			zombie++
		}
	}
	if data, err := os.ReadFile(hostfs.Proc("stat")); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if v, ok := strings.CutPrefix(line, "procs_running "); ok {
				running, _ = strconv.Atoi(strings.TrimSpace(v))
				break
			}
		}
	}
	return total, running, zombie, nil
}

// readDiskIO lit /proc/diskstats. Les secteurs y valent toujours 512 octets, quelle que
// soit la taille réelle des secteurs du disque.
func readDiskIO() (map[string]ioCounters, error) {
	f, err := os.Open(hostfs.Proc("diskstats"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]ioCounters{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fl := strings.Fields(sc.Text())
		if len(fl) < 14 {
			continue
		}
		name := fl[2]
		if ignoredBlockDevice(name) {
			continue
		}
		u := func(i int) uint64 { v, _ := strconv.ParseUint(fl[i], 10, 64); return v }
		c := ioCounters{
			reads:      u(3),
			readBytes:  u(5) * 512,
			readMs:     u(6),
			writes:     u(7),
			writeBytes: u(9) * 512,
			writeMs:    u(10),
			busyMs:     float64(u(12)),
		}
		if c.reads == 0 && c.writes == 0 {
			continue // disque jamais utilisé : rien à suivre
		}
		out[name] = c
	}
	return out, sc.Err()
}

// ignoredBlockDevice écarte les périphériques sans intérêt pour la supervision :
// boucles (snap), disques en mémoire, lecteurs optiques et disquettes.
func ignoredBlockDevice(name string) bool {
	for _, p := range []string{"loop", "ram", "zram", "sr", "fd", "nbd"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// États TCP du noyau (include/net/tcp_states.h), colonne « st » de /proc/net/tcp.
var linuxTCPStates = map[string]string{
	"01": "ESTABLISHED", "02": "SYN_SENT", "03": "SYN_RECV", "04": "FIN_WAIT1",
	"05": "FIN_WAIT2", "06": "TIME_WAIT", "07": "CLOSE", "08": "CLOSE_WAIT",
	"09": "LAST_ACK", "0A": "LISTEN", "0B": "CLOSING", "0C": "SYN_RECV",
}

// readTCPStates compte les sockets TCP (IPv4 et IPv6) par état, sans chercher à quel
// processus elles appartiennent : c'est le relevé léger des 15 s ; l'attribution aux
// processus est faite par l'instantané des connexions, toutes les 60 s.
func readTCPStates() (map[string]int, error) {
	out := map[string]int{}
	found := false
	for _, path := range []string{hostfs.Proc("net", "tcp"), hostfs.Proc("net", "tcp6")} {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		found = true
		sc := bufio.NewScanner(f)
		sc.Scan() // en-tête
		for sc.Scan() {
			fl := strings.Fields(sc.Text())
			if len(fl) < 4 {
				continue
			}
			if st, ok := linuxTCPStates[strings.ToUpper(fl[3])]; ok {
				out[st]++
			}
		}
		f.Close()
	}
	if !found {
		return nil, fmt.Errorf("/proc/net/tcp illisible")
	}
	return out, nil
}
