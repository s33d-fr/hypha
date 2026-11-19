//go:build linux

package procs

import (
	"bytes"
	"os"
	"strconv"
	"strings"

	"s33d.fr/hypha/agent/internal/hostfs"
)

// clkTck : unité des temps de /proc/<pid>/stat. USER_HZ vaut 100 sur toutes les
// architectures Linux courantes (x86_64, arm64), quelle que soit la fréquence du noyau.
const clkTck = 100.0

// sysReader lit /proc directement : une lecture de /proc/<pid>/stat donne d'un coup
// l'état, le parent, les temps CPU, les fils d'exécution, la date de démarrage et la RSS.
type sysReader struct {
	pageSize uint64
	bootMs   int64
	users    map[string]string // uid → nom, lu dans le /etc/passwd de l'hôte (sans cgo : pas de NSS)
}

func newSysReader() *sysReader {
	r := &sysReader{pageSize: uint64(os.Getpagesize()), users: hostfs.Users()}
	if data, err := os.ReadFile(hostfs.Proc("stat")); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "btime ") {
				v, _ := strconv.ParseInt(strings.TrimSpace(line[6:]), 10, 64)
				r.bootMs = v * 1000
			}
		}
	}
	return r
}

func (r *sysReader) scan(needCmdline bool) ([]sample, error) {
	d, err := os.Open(hostfs.Proc())
	if err != nil {
		return nil, err
	}
	names, err := d.Readdirnames(-1)
	d.Close()
	if err != nil {
		return nil, err
	}
	out := make([]sample, 0, len(names))
	for _, n := range names {
		if n[0] < '0' || n[0] > '9' {
			continue
		}
		pid, err := strconv.Atoi(n)
		if err != nil {
			continue
		}
		s, ok := r.readStat(int32(pid))
		if !ok {
			continue // processus terminé entre-temps
		}
		r.readIO(&s)
		if needCmdline {
			s.cmdline = r.cmdline(&s)
		}
		out = append(out, s)
	}
	return out, nil
}

// readStat découpe /proc/<pid>/stat. Le nom (2e champ) est entre parenthèses et peut
// contenir espaces et parenthèses : on coupe donc à la DERNIÈRE parenthèse fermante.
func (r *sysReader) readStat(pid int32) (sample, bool) {
	data, err := os.ReadFile(hostfs.Proc(strconv.Itoa(int(pid)), "stat"))
	if err != nil {
		return sample{}, false
	}
	open := bytes.IndexByte(data, '(')
	end := bytes.LastIndexByte(data, ')')
	if open < 0 || end < open || end+2 > len(data) {
		return sample{}, false
	}
	f := strings.Fields(string(data[end+2:]))
	// f[0] = état (champ 3 de proc(5)), f[1] = ppid, f[11] = utime, f[12] = stime,
	// f[17] = num_threads, f[19] = starttime, f[21] = rss (pages).
	if len(f) < 22 {
		return sample{}, false
	}
	num := func(i int) float64 { v, _ := strconv.ParseFloat(f[i], 64); return v }
	ppid, _ := strconv.Atoi(f[1])
	rss, _ := strconv.ParseUint(f[21], 10, 64)
	return sample{
		pid:        pid,
		ppid:       int32(ppid),
		name:       string(data[open+1 : end]),
		state:      f[0],
		cpuSeconds: (num(11) + num(12)) / clkTck,
		threads:    int32(num(17)),
		startMs:    r.bootMs + int64(num(19)/clkTck*1000),
		rss:        rss * r.pageSize,
	}, true
}

// readIO lit les octets réellement lus et écrits sur le stockage (/proc/<pid>/io).
func (r *sysReader) readIO(s *sample) {
	data, err := os.ReadFile(hostfs.Proc(strconv.Itoa(int(s.pid)), "io"))
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		k, v, ok := strings.Cut(line, ": ")
		if !ok {
			continue
		}
		n, _ := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
		switch k {
		case "read_bytes":
			s.ioRead = n
		case "write_bytes":
			s.ioWrite = n
		}
	}
}

func (r *sysReader) cmdline(s *sample) string {
	data, err := os.ReadFile(hostfs.Proc(strconv.Itoa(int(s.pid)), "cmdline"))
	if err != nil || len(data) == 0 {
		// Fil du noyau (kworker…) : pas de ligne de commande, on montre le nom comme ps.
		return "[" + s.name + "]"
	}
	return strings.TrimSpace(string(bytes.ReplaceAll(data, []byte{0}, []byte{' '})))
}

// enrich ajoute l'utilisateur et la ligne de commande d'un processus retenu.
func (r *sysReader) enrich(s *sample) {
	if s.enriched {
		return
	}
	s.enriched = true
	if s.cmdline == "" {
		s.cmdline = r.cmdline(s)
	}
	data, err := os.ReadFile(hostfs.Proc(strconv.Itoa(int(s.pid)), "status"))
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "Uid:") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 2 {
			return
		}
		uid := f[1]
		name, ok := r.users[uid]
		if !ok {
			// Compte absent de /etc/passwd (annuaire LDAP par exemple) : l'uid seul.
			name = uid
		}
		s.user = name
		return
	}
}

// NewNameResolver rend une fonction pid → nom du processus, pour l'instantané des connexions.
func NewNameResolver() func(pid int32) string {
	cache := map[int32]string{}
	return func(pid int32) string {
		if pid <= 0 {
			return ""
		}
		if n, ok := cache[pid]; ok {
			return n
		}
		data, _ := os.ReadFile(hostfs.Proc(strconv.Itoa(int(pid)), "comm"))
		n := strings.TrimSpace(string(data))
		cache[pid] = n
		return n
	}
}
