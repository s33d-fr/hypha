// Package metrics collecte les métriques système du contrat (§ 4), noms et unités exacts.
//
// Les débits (octets/s, opérations/s, paquets/s) sont calculés ici : différence entre deux
// relevés divisée par le temps écoulé. Le premier cycle ne rend donc que les valeurs
// instantanées ; les débits apparaissent au cycle suivant.
package metrics

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	gnet "github.com/shirou/gopsutil/v4/net"

	"s33d.fr/hypha/agent/internal/hostfs"
	"s33d.fr/hypha/agent/internal/model"
)

// Seuils de l'événement « disque plein » : alerte au-dessus de 90 %, retour à la normale
// sous 85 % (l'écart évite une rafale d'événements autour de 90 %).
const (
	diskAlertPct   = 90.0
	diskRecoverPct = 85.0
)

// Systèmes de fichiers ignorés : liste du contrat, plus les pseudo-systèmes qui n'ont pas
// d'espace disque réel (leur taille est nulle de toute façon, mais autant ne pas les interroger).
var excludedFstypes = map[string]bool{
	"tmpfs": true, "devtmpfs": true, "overlay": true, "squashfs": true, "proc": true, "sysfs": true,
	"cgroup": true, "cgroup2": true, "devpts": true, "mqueue": true, "debugfs": true, "tracefs": true,
	"securityfs": true, "pstore": true, "bpf": true, "configfs": true, "fusectl": true, "hugetlbfs": true,
	"autofs": true, "binfmt_misc": true, "rpc_pipefs": true, "nsfs": true, "efivarfs": true, "ramfs": true,
	"selinuxfs": true, "nfsd": true, "fuse.lxcfs": true, "fuse.gvfsd-fuse": true, "fuse.portal": true,
	// Supports optiques (ISO, CD, DVD) : pleins par construction, jamais une alerte de place.
	"iso9660": true, "udf": true, "UDF": true, "CDFS": true, "cdfs": true,
}

// Montages ignorés (contrat § 4).
var excludedMountPrefixes = []string{"/snap/", "/run/", "/sys/", "/proc/", "/dev/"}

// États TCP rendus, toujours tous (une série à zéro reste lisible sur un graphique).
var tcpStates = []string{
	"ESTABLISHED", "SYN_SENT", "SYN_RECV", "FIN_WAIT1", "FIN_WAIT2", "TIME_WAIT",
	"CLOSE", "CLOSE_WAIT", "LAST_ACK", "LISTEN", "CLOSING",
}

// System garde les relevés précédents pour calculer les débits.
type System struct {
	excludeIfaces []string
	log           *slog.Logger

	prevAt  time.Time
	prevCPU *cpuTimes
	prevIO  map[string]ioCounters
	prevNet map[string]gnet.IOCountersStat
	osPrev  osState

	diskAlerted map[string]bool
	hungMounts  map[string]time.Time
}

// Result est la sortie d'un cycle.
type Result struct {
	Series []model.Series
	Events []model.Event
	Errors []error
}

// cpuTimes : temps cumulés (secondes) depuis le démarrage, tous cœurs confondus.
type cpuTimes struct {
	user, system, idle, iowait, steal, interrupt, total float64
}

// cpuPct : parts du temps processeur entre deux relevés. usage = 100 − idle compte l'attente
// disque comme du temps occupé (sens historique, gardé pour les tableaux) ; busy l'en retire : un
// serveur qui attend son disque n'est pas un processeur saturé (serveur de base de données relevé
// le 19/11 : usage 86 %, iowait 69 %, busy 17 %).
type cpuPct struct {
	idle, iowait, usage, busy float64
}

func cpuPercents(cur, prev cpuTimes, withIowait bool) cpuPct {
	span := cur.total - prev.total
	if span <= 0 {
		return cpuPct{}
	}
	pct := func(c, o float64) float64 { return clampPct((c - o) / span * 100) }
	r := cpuPct{idle: pct(cur.idle, prev.idle)}
	r.usage = clampPct(100 - r.idle)
	r.busy = r.usage
	if withIowait {
		r.iowait = pct(cur.iowait, prev.iowait)
		r.busy = clampPct(r.usage - r.iowait)
	}
	return r
}

// ioCounters : compteurs cumulés d'un disque.
type ioCounters struct {
	readBytes, writeBytes uint64
	reads, writes         uint64
	busyMs                float64 // temps où le disque a travaillé (ms)
	readMs, writeMs       uint64  // temps cumulé des lectures et écritures (Linux, pour await)
}

// New crée le collecteur.
func New(excludeIfaces []string, log *slog.Logger) *System {
	return &System{
		excludeIfaces: excludeIfaces,
		log:           log,
		diskAlerted:   map[string]bool{},
		hungMounts:    map[string]time.Time{},
	}
}

// Collect fait un cycle complet.
func (s *System) Collect(ctx context.Context, now time.Time, tags model.TagFunc) Result {
	var res Result
	b := model.NewSeriesBuilder(model.Ms(now), tags)
	dt := 0.0
	if !s.prevAt.IsZero() {
		dt = now.Sub(s.prevAt).Seconds()
	}
	fail := func(what string, err error) {
		res.Errors = append(res.Errors, fmt.Errorf("%s : %w", what, err))
	}

	// CPU
	if cur, err := readCPU(); err != nil {
		fail("cpu", err)
	} else {
		if p := s.prevCPU; p != nil && cur.total > p.total {
			span := cur.total - p.total
			pct := func(c, o float64) float64 { return clampPct((c - o) / span * 100) }
			c := cpuPercents(cur, *p, hasIowait)
			b.Gauge("system.cpu.user", "percent", pct(cur.user, p.user))
			b.Gauge("system.cpu.system", "percent", pct(cur.system, p.system))
			b.Gauge("system.cpu.idle", "percent", c.idle)
			b.Gauge("system.cpu.usage", "percent", c.usage)
			b.Gauge("system.cpu.busy", "percent", c.busy)
			if hasIowait {
				b.Gauge("system.cpu.iowait", "percent", c.iowait)
				b.Gauge("system.cpu.steal", "percent", pct(cur.steal, p.steal))
			}
			if hasInterrupt {
				b.Gauge("system.cpu.interrupt", "percent", pct(cur.interrupt, p.interrupt))
			}
		}
		s.prevCPU = &cur
	}
	cores := runtime.NumCPU()
	b.Gauge("system.cpu.num_cores", "count", float64(cores))

	// Mémoire, fichier d'échange, charge, processus, descripteurs, TCP : propres au système.
	for _, err := range s.collectOS(b, dt, cores) {
		res.Errors = append(res.Errors, err)
	}

	// Espace disque
	res.Events = append(res.Events, s.collectDisks(ctx, b, now, tags, fail)...)

	// E/S disque
	if cur, err := readDiskIO(); err != nil {
		fail("disk io", err)
	} else {
		if dt > 0 {
			for dev, c := range cur {
				p, ok := s.prevIO[dev]
				if !ok {
					continue
				}
				r := func(a, o uint64) (float64, bool) { return rate(a, o, dt) }
				if v, ok := r(c.readBytes, p.readBytes); ok {
					b.Gauge("system.io.read_bytes", "byte_per_second", v, "device", dev)
				}
				if v, ok := r(c.writeBytes, p.writeBytes); ok {
					b.Gauge("system.io.write_bytes", "byte_per_second", v, "device", dev)
				}
				if v, ok := r(c.reads, p.reads); ok {
					b.Gauge("system.io.r_s", "operation_per_second", v, "device", dev)
				}
				if v, ok := r(c.writes, p.writes); ok {
					b.Gauge("system.io.w_s", "operation_per_second", v, "device", dev)
				}
				if c.busyMs >= p.busyMs {
					b.Gauge("system.io.util", "percent", clampPct((c.busyMs-p.busyMs)/(dt*1000)*100), "device", dev)
				}
				if hasAwait && c.reads >= p.reads && c.writes >= p.writes && c.readMs >= p.readMs && c.writeMs >= p.writeMs {
					ops := float64((c.reads - p.reads) + (c.writes - p.writes))
					await := 0.0
					if ops > 0 {
						await = float64((c.readMs-p.readMs)+(c.writeMs-p.writeMs)) / ops
					}
					b.Gauge("system.io.await", "millisecond", await, "device", dev)
				}
			}
		}
		s.prevIO = cur
	}

	// Réseau
	if cur, err := gnet.IOCountersWithContext(ctx, true); err != nil {
		fail("net", err)
	} else {
		next := make(map[string]gnet.IOCountersStat, len(cur))
		for _, c := range cur {
			if s.excludedIface(c.Name) {
				continue
			}
			next[c.Name] = c
			p, ok := s.prevNet[c.Name]
			if !ok || dt <= 0 {
				continue
			}
			pairs := []struct {
				name, unit string
				c, o       uint64
			}{
				{"system.net.bytes_rcvd", "byte_per_second", c.BytesRecv, p.BytesRecv},
				{"system.net.bytes_sent", "byte_per_second", c.BytesSent, p.BytesSent},
				{"system.net.packets_in", "packet_per_second", c.PacketsRecv, p.PacketsRecv},
				{"system.net.packets_out", "packet_per_second", c.PacketsSent, p.PacketsSent},
				{"system.net.errors_in", "packet_per_second", c.Errin, p.Errin},
				{"system.net.errors_out", "packet_per_second", c.Errout, p.Errout},
				{"system.net.drops_in", "packet_per_second", c.Dropin, p.Dropin},
			}
			for _, x := range pairs {
				if v, ok := rate(x.c, x.o, dt); ok {
					b.Gauge(x.name, x.unit, v, "interface", c.Name)
				}
			}
		}
		s.prevNet = next
	}

	// Connexions TCP par état
	if states, err := readTCPStates(); err != nil {
		fail("tcp", err)
	} else {
		for _, st := range tcpStates {
			b.Gauge("system.net.tcp.connections", "count", float64(states[st]), "state", st)
		}
	}

	// Durée de fonctionnement
	if up, err := host.UptimeWithContext(ctx); err != nil {
		fail("uptime", err)
	} else {
		b.Gauge("system.uptime", "second", float64(up))
	}

	s.prevAt = now
	res.Series = b.Series()
	return res
}

// collectDisks relève l'espace de chaque système de fichiers et rend les événements
// « disque plein ».
func (s *System) collectDisks(ctx context.Context, b *model.SeriesBuilder, now time.Time, tags model.TagFunc, fail func(string, error)) []model.Event {
	parts, err := disk.PartitionsWithContext(ctx, true)
	if err != nil && len(parts) == 0 {
		fail("disk", err)
		return nil
	}
	var events []model.Event
	seen := map[string]bool{}
	for _, p := range parts {
		if seen[p.Mountpoint] || s.excludedMount(p) {
			continue
		}
		seen[p.Mountpoint] = true
		if t, hung := s.hungMounts[p.Mountpoint]; hung && now.Sub(t) < 5*time.Minute {
			continue
		}
		u, err := usageWithTimeout(p.Mountpoint, 2*time.Second)
		if err == errUsageTimeout {
			// Montage réseau figé : statfs ne rend pas la main. On le saute 5 minutes pour
			// ne pas accumuler les appels bloqués.
			s.hungMounts[p.Mountpoint] = now
			fail("disk "+p.Mountpoint, err)
			continue
		}
		delete(s.hungMounts, p.Mountpoint)
		if err != nil || u.Total == 0 {
			continue
		}
		kv := []string{"device", p.Device, "mount", p.Mountpoint, "fstype", p.Fstype}
		b.Gauge("system.disk.total", "byte", float64(u.Total), kv...)
		b.Gauge("system.disk.used", "byte", float64(u.Used), kv...)
		b.Gauge("system.disk.free", "byte", float64(u.Free), kv...)
		b.Gauge("system.disk.in_use", "percent", u.UsedPercent, kv...)
		if hasInodes && u.InodesTotal > 0 {
			b.Gauge("system.fs.inodes.in_use", "percent", u.InodesUsedPercent, "mount", p.Mountpoint)
		}

		switch {
		case u.UsedPercent > diskAlertPct && !s.diskAlerted[p.Mountpoint]:
			s.diskAlerted[p.Mountpoint] = true
			events = append(events, model.Event{
				TS: model.Ms(now), Source: "agent", Type: "disk_full",
				Title:     fmt.Sprintf("Disque %s rempli à %.0f %%", p.Mountpoint, u.UsedPercent),
				Text:      fmt.Sprintf("%s (%s) : %s libres sur %s.", p.Mountpoint, p.Device, humanBytes(u.Free), humanBytes(u.Total)),
				AlertType: "warning", Priority: "normal",
				Tags:           tags("mount", p.Mountpoint, "device", p.Device),
				AggregationKey: "disk:" + p.Mountpoint,
			})
		case u.UsedPercent < diskRecoverPct && s.diskAlerted[p.Mountpoint]:
			delete(s.diskAlerted, p.Mountpoint)
			events = append(events, model.Event{
				TS: model.Ms(now), Source: "agent", Type: "disk_full",
				Title:     fmt.Sprintf("Disque %s revenu à %.0f %%", p.Mountpoint, u.UsedPercent),
				Text:      fmt.Sprintf("%s (%s) : %s libres sur %s.", p.Mountpoint, p.Device, humanBytes(u.Free), humanBytes(u.Total)),
				AlertType: "success", Priority: "normal",
				Tags:           tags("mount", p.Mountpoint, "device", p.Device),
				AggregationKey: "disk:" + p.Mountpoint,
			})
		}
	}
	return events
}

func (s *System) excludedMount(p disk.PartitionStat) bool {
	if excludedFstypes[p.Fstype] {
		return true
	}
	m := p.Mountpoint
	for _, pre := range excludedMountPrefixes {
		if strings.HasPrefix(m, pre) || m == strings.TrimSuffix(pre, "/") {
			return true
		}
	}
	return false
}

func (s *System) excludedIface(name string) bool {
	for _, pat := range s.excludeIfaces {
		if ok, _ := filepath.Match(pat, name); ok {
			return true
		}
	}
	return false
}

var errUsageTimeout = fmt.Errorf("statfs sans réponse en 2 s (montage figé ?)")

// usageWithTimeout borne l'appel statfs, qui peut rester bloqué sur un montage NFS mort.
func usageWithTimeout(path string, d time.Duration) (*disk.UsageStat, error) {
	type res struct {
		u   *disk.UsageStat
		err error
	}
	ch := make(chan res, 1)
	go func() {
		u, err := disk.Usage(hostfs.Root(path))
		ch <- res{u, err}
	}()
	select {
	case r := <-ch:
		return r.u, r.err
	case <-time.After(d):
		return nil, errUsageTimeout
	}
}

// rate rend (cur − prev) / dt ; faux si le compteur est reparti de zéro.
func rate(cur, prev uint64, dt float64) (float64, bool) {
	if cur < prev || dt <= 0 {
		return 0, false
	}
	return float64(cur-prev) / dt, true
}

func clampPct(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 100:
		return 100
	}
	return v
}

func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d o", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cio", float64(n)/float64(div), "KMGTPE"[exp])
}

// NormalizeTCPState ramène les noms d'états de Windows et de Linux à une seule forme
// (FIN_WAIT_1 → FIN_WAIT1, SYN_RECEIVED → SYN_RECV, CLOSED → CLOSE).
func NormalizeTCPState(s string) string {
	switch s {
	case "SYN_RECEIVED", "NEW_SYN_RECV":
		return "SYN_RECV"
	case "FIN_WAIT_1":
		return "FIN_WAIT1"
	case "FIN_WAIT_2":
		return "FIN_WAIT2"
	case "CLOSED", "DELETE":
		return "CLOSE"
	}
	return s
}
