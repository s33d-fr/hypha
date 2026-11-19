// Package procs fait l'instantané des processus (contrat § 3.6) : les 30 premiers par
// CPU et les 30 premiers par mémoire, dédoublonnés, plus ceux que désigne
// processes.include.
//
// Deux passes, pour rester léger : une lecture rapide de tous les processus (CPU, mémoire,
// E/S), puis les détails coûteux (utilisateur, ligne de commande) pour les seuls retenus.
package procs

import (
	"regexp"
	"sort"
	"time"

	"s33d.fr/hypha/agent/internal/model"
)

const topN = 30

// sample : relevé brut d'un processus.
type sample struct {
	pid, ppid  int32
	name       string
	state      string
	cpuSeconds float64 // temps CPU cumulé (utilisateur + noyau)
	rss        uint64
	threads    int32
	startMs    int64 // date de démarrage : sert aussi à reconnaître un pid réutilisé
	ioRead     uint64
	ioWrite    uint64
	cmdline    string // rempli par la passe rapide seulement si processes.include le demande
	user       string
	enriched   bool
}

type prevSample struct {
	startMs    int64
	cpuSeconds float64
	ioRead     uint64
	ioWrite    uint64
}

// Collector garde le relevé précédent pour les pourcentages CPU et les débits d'E/S.
type Collector struct {
	include []*regexp.Regexp
	prev    map[int32]prevSample
	prevAt  time.Time
	sys     *sysReader
}

// New crée le collecteur ; include sont les motifs de processes.include.
func New(include []string) *Collector {
	c := &Collector{prev: map[int32]prevSample{}, sys: newSysReader()}
	for _, p := range include {
		if re, err := regexp.Compile(p); err == nil {
			c.include = append(c.include, re)
		}
	}
	return c
}

// Collect rend l'instantané ; totalMem sert au pourcentage de mémoire.
func (c *Collector) Collect(now time.Time, totalMem uint64) ([]model.Process, error) {
	all, err := c.sys.scan(len(c.include) > 0)
	if err != nil {
		return nil, err
	}
	dt := 0.0
	if !c.prevAt.IsZero() {
		dt = now.Sub(c.prevAt).Seconds()
	}

	type scored struct {
		s        *sample
		cpu      float64
		rBps     uint64
		wBps     uint64
		selected bool
	}
	list := make([]scored, len(all))
	next := make(map[int32]prevSample, len(all))
	for i := range all {
		s := &all[i]
		sc := scored{s: s}
		if p, ok := c.prev[s.pid]; ok && p.startMs == s.startMs && dt > 0 {
			if s.cpuSeconds >= p.cpuSeconds {
				sc.cpu = (s.cpuSeconds - p.cpuSeconds) / dt * 100
			}
			if s.ioRead >= p.ioRead {
				sc.rBps = uint64(float64(s.ioRead-p.ioRead) / dt)
			}
			if s.ioWrite >= p.ioWrite {
				sc.wBps = uint64(float64(s.ioWrite-p.ioWrite) / dt)
			}
		}
		next[s.pid] = prevSample{startMs: s.startMs, cpuSeconds: s.cpuSeconds, ioRead: s.ioRead, ioWrite: s.ioWrite}
		list[i] = sc
	}
	c.prev, c.prevAt = next, now

	idx := make([]int, len(list))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool { return list[idx[a]].cpu > list[idx[b]].cpu })
	for i := 0; i < len(idx) && i < topN; i++ {
		list[idx[i]].selected = true
	}
	sort.Slice(idx, func(a, b int) bool { return list[idx[a]].s.rss > list[idx[b]].s.rss })
	for i := 0; i < len(idx) && i < topN; i++ {
		list[idx[i]].selected = true
	}
	if len(c.include) > 0 {
		for i := range list {
			if list[i].selected {
				continue
			}
			for _, re := range c.include {
				if re.MatchString(list[i].s.name) || re.MatchString(list[i].s.cmdline) {
					list[i].selected = true
					break
				}
			}
		}
	}

	var out []model.Process
	for _, sc := range list {
		if !sc.selected {
			continue
		}
		c.sys.enrich(sc.s)
		memPct := 0.0
		if totalMem > 0 {
			memPct = float64(sc.s.rss) / float64(totalMem) * 100
		}
		out = append(out, model.Process{
			PID:        sc.s.pid,
			PPID:       sc.s.ppid,
			Name:       sc.s.name,
			User:       sc.s.user,
			Cmdline:    model.Truncate(sc.s.cmdline, 2048),
			State:      sc.s.state,
			CPUPct:     round2(sc.cpu),
			MemRSS:     sc.s.rss,
			MemPct:     round2(memPct),
			Threads:    sc.s.threads,
			Started:    sc.s.startMs,
			IOReadBps:  sc.rBps,
			IOWriteBps: sc.wBps,
		})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].CPUPct > out[b].CPUPct })
	return out, nil
}

func round2(v float64) float64 { return float64(int64(v*100+0.5)) / 100 }
