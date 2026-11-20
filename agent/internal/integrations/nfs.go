package integrations

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/disk"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/hostfs"
	"s33d.fr/hypha/agent/internal/model"
)

// ── NFS : client (/proc/<pid>/mountstats) et serveur (/proc/net/rpc/nfsd) ───────────────────
//
// Client : par point de montage NFS, opérations, octets lus et écrits, retransmissions,
// délais dépassés, temps aller-retour et d'exécution moyens des lectures et écritures, et un
// contrôle nfs.mount : statfs dans le délai, sinon CRIT (serveur muet, montage figé). Un statfs
// figé n'est jamais relancé tant que le précédent n'a pas rendu la main.
// Serveur : fils nfsd, appels RPC (et mauvais appels), octets lus et écrits, opérations par
// version, cache de réponses, exports.

type nfsOp struct{ ops, trans, timeouts, sent, recv, queue, rtt, exe float64 }

type nfsMount struct {
	device, mount, fstype string
	ops                   map[string]nfsOp
}

// parseMountstats lit /proc/<pid>/mountstats : blocs « device … mounted on … with fstype nfs… ».
func parseMountstats(r io.Reader) []nfsMount {
	var out []nfsMount
	var cur *nfsMount
	inOps := false
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		l := sc.Text()
		if strings.HasPrefix(l, "device ") {
			cur, inOps = nil, false
			f := strings.Fields(l)
			// device SRC mounted on MNT with fstype TYPE …
			// nfs et nfs4 seulement : « nfsd » est le système de fichiers de contrôle du serveur.
			if len(f) >= 8 && f[2] == "mounted" && f[3] == "on" && f[5] == "with" && f[6] == "fstype" &&
				(f[7] == "nfs" || f[7] == "nfs4") {
				out = append(out, nfsMount{device: f[1], mount: unescapeMount(f[4]), fstype: f[7], ops: map[string]nfsOp{}})
				cur = &out[len(out)-1]
			}
			continue
		}
		if cur == nil {
			continue
		}
		t := strings.TrimSpace(l)
		if t == "per-op statistics" {
			inOps = true
			continue
		}
		if !inOps {
			continue
		}
		name, rest, ok := strings.Cut(t, ":")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 8 {
			continue
		}
		v := make([]float64, 8)
		for i := range v {
			v[i], _ = strconv.ParseFloat(f[i], 64)
		}
		cur.ops[name] = nfsOp{v[0], v[1], v[2], v[3], v[4], v[5], v[6], v[7]}
	}
	return out
}

type NFS struct {
	cfg     config.NFSConfig
	det     detector
	prev    map[string]float64
	mu      sync.Mutex
	pending map[string]bool // statfs en cours (montage peut-être figé)
	server  bool
}

func newNFS(cfg config.NFSConfig) *NFS {
	return &NFS{cfg: cfg, det: detector{mode: intent(cfg.Enabled, len(cfg.Mounts) > 0 || cfg.ServerStats != "")}, pending: map[string]bool{}}
}

func (n *NFS) name() string          { return "nfs" }
func (n *NFS) period() time.Duration { return 30 * time.Second }
func (n *NFS) close()                {}

func (n *NFS) wanted(mount string) bool {
	if len(n.cfg.Mounts) == 0 {
		return true
	}
	for _, p := range n.cfg.Mounts {
		if ok, _ := filepath.Match(p, mount); ok || p == mount {
			return true
		}
	}
	return false
}

// statfsTimeout : statfs borné ; rend (taille, libre, réussi). Un appel figé reste en cours
// et marque le montage, qui n'est plus interrogé avant son retour.
func (n *NFS) statfsTimeout(mount string, timeout time.Duration) (total, free float64, ok bool, hung bool) {
	n.mu.Lock()
	if n.pending[mount] {
		n.mu.Unlock()
		return 0, 0, false, true
	}
	n.pending[mount] = true
	n.mu.Unlock()
	type res struct {
		total, free float64
		err         error
	}
	ch := make(chan res, 1)
	go func() {
		var t, f float64
		u, err := disk.Usage(hostfs.Root(mount))
		if err == nil {
			t, f = float64(u.Total), float64(u.Free)
		}
		n.mu.Lock()
		delete(n.pending, mount)
		n.mu.Unlock()
		ch <- res{t, f, err}
	}()
	select {
	case r := <-ch:
		return r.total, r.free, r.err == nil, false
	case <-time.After(timeout):
		return 0, 0, false, true
	}
}

// parseNfsd lit /proc/net/rpc/nfsd : ligne → valeurs.
func parseNfsd(r io.Reader) map[string][]float64 {
	out := map[string][]float64{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 {
			continue
		}
		v := make([]float64, 0, len(f)-1)
		for _, x := range f[1:] {
			n, _ := strconv.ParseFloat(x, 64)
			v = append(v, n)
		}
		out[f[0]] = v
	}
	return out
}

func (n *NFS) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := n.det.skip(now); skip {
		return nil, st
	}
	ts := model.Ms(now)
	var mounts []nfsMount
	if f, err := os.Open(hostfs.Proc("1", "mountstats")); err == nil {
		mounts = parseMountstats(f)
		f.Close()
	} else if f, err := os.Open(hostfs.Proc("self", "mountstats")); err == nil {
		mounts = parseMountstats(f)
		f.Close()
	}
	var nfsd map[string][]float64
	statsPath := hostfs.Proc("net", "rpc", "nfsd")
	if p := n.cfg.ServerStats; p != "" {
		statsPath = hostfs.Root(p)
		if rest, ok := strings.CutPrefix(p, "/proc/"); ok {
			statsPath = hostfs.Proc(rest) // agent en conteneur : /proc de l'hôte monté à part
		}
	}
	if f, err := os.Open(statsPath); err == nil {
		nfsd = parseNfsd(f)
		f.Close()
	}
	threads := 0.0
	if th := nfsd["th"]; len(th) > 0 {
		threads = th[0]
	}
	var kept []nfsMount
	for _, m := range mounts {
		if n.wanted(m.mount) {
			kept = append(kept, m)
		}
	}
	isServer := nfsd != nil && (threads > 0 || n.server)
	if len(kept) == 0 && !isServer {
		if silent, st := n.det.failed("ni montage NFS, ni serveur NFS"); silent {
			return nil, st
		}
		return nil, "ok" // plus de montage : rien à mesurer, rien d'anormal
	}
	n.det.ok()
	cur := map[string]float64{}
	count := func(metric, unit, key string, v float64, kv ...string) {
		cur[key] = v
		if n.prev == nil {
			return
		}
		if old, ok := n.prev[key]; ok {
			if d, ok := deltaF(v, old); ok {
				b.Add(metric, model.TypeCount, unit, d, kv...)
			}
		}
	}
	diff := func(key string) (float64, bool) {
		old, ok := n.prev[key]
		if !ok || n.prev == nil {
			return 0, false
		}
		return deltaF(cur[key], old)
	}
	var checks []model.Check
	sort.Slice(kept, func(i, j int) bool { return kept[i].mount < kept[j].mount })
	for _, m := range kept {
		kv := []string{"mount", m.mount, "nfs_server", m.device, "nfs_version", m.fstype}
		var ops, retrans, timeouts float64
		for _, o := range m.ops {
			ops += o.ops
			retrans += o.trans - o.ops
			timeouts += o.timeouts
		}
		count("nfs.client.ops", "operation", m.mount+"/ops", ops, kv...)
		count("nfs.client.retrans", "operation", m.mount+"/retrans", retrans, kv...)
		count("nfs.client.timeouts", "operation", m.mount+"/timeouts", timeouts, kv...)
		for _, dir := range []struct{ op, ops, bytes, rtt, exe string }{
			{"READ", "nfs.client.read.ops", "nfs.client.read.bytes", "nfs.client.read.rtt", "nfs.client.read.exe"},
			{"WRITE", "nfs.client.write.ops", "nfs.client.write.bytes", "nfs.client.write.rtt", "nfs.client.write.exe"},
		} {
			o, ok := m.ops[dir.op]
			if !ok {
				continue
			}
			bytes := o.recv
			if dir.op == "WRITE" {
				bytes = o.sent
			}
			p := m.mount + "/" + dir.op
			count(dir.ops, "operation", p+"/ops", o.ops, kv...)
			count(dir.bytes, "byte", p+"/bytes", bytes, kv...)
			cur[p+"/rtt"], cur[p+"/exe"] = o.rtt, o.exe
			if d, ok := diff(p + "/ops"); ok && d > 0 {
				if r, ok := diff(p + "/rtt"); ok {
					b.Gauge(dir.rtt, "millisecond", round1(r/d), kv...)
				}
				if e, ok := diff(p + "/exe"); ok {
					b.Gauge(dir.exe, "millisecond", round1(e/d), kv...)
				}
			}
		}
		total, free, ok, hung := n.statfsTimeout(m.mount, n.cfg.Timeout.D())
		st, msg := model.CheckOK, "montage "+m.device+" répond"
		switch {
		case hung:
			st, msg = model.CheckCrit, fmt.Sprintf("montage %s figé : statfs sans réponse en %s (serveur %s)", m.mount, n.cfg.Timeout.D(), m.device)
		case !ok:
			st, msg = model.CheckCrit, "montage "+m.mount+" en erreur"
		default:
			b.Gauge("nfs.client.disk.total", "byte", total, kv...)
			b.Gauge("nfs.client.disk.free", "byte", free, kv...)
			if total > 0 {
				b.Gauge("nfs.client.disk.usage", "percent", round1(100*(total-free)/total), kv...)
			}
		}
		checks = append(checks, model.Check{TS: ts, Name: "nfs.mount", Status: st, Message: msg, Tags: tags(kv...)})
	}
	if nfsd != nil && (threads > 0 || n.server) {
		n.server = true
		b.Gauge("nfs.server.threads", "thread", threads, nil...)
		if v := nfsd["io"]; len(v) >= 2 {
			count("nfs.server.read.bytes", "byte", "srv/read", v[0])
			count("nfs.server.write.bytes", "byte", "srv/write", v[1])
		}
		if v := nfsd["rpc"]; len(v) >= 2 {
			count("nfs.server.rpc.calls", "call", "srv/calls", v[0])
			count("nfs.server.rpc.badcalls", "call", "srv/badcalls", v[1])
		}
		if v := nfsd["net"]; len(v) >= 4 {
			count("nfs.server.net.packets", "packet", "srv/packets", v[0])
			count("nfs.server.net.tcp_connections", "connection", "srv/tcpconn", v[3])
		}
		if v := nfsd["rc"]; len(v) >= 2 {
			count("nfs.server.reply_cache.hits", "hit", "srv/rc_hits", v[0])
			count("nfs.server.reply_cache.misses", "miss", "srv/rc_misses", v[1])
		}
		// « proc3 22 v0 … » et « proc4ops 72 v0 … » : le premier nombre est le nombre de valeurs.
		for _, ver := range []struct{ key, tag string }{{"proc3", "3"}, {"proc4ops", "4"}} {
			if v := nfsd[ver.key]; len(v) > 1 {
				var sum float64
				for _, x := range v[1:] {
					sum += x
				}
				count("nfs.server.ops", "operation", "srv/"+ver.key, sum, "nfs_version", ver.tag)
			}
		}
		exports := 0.0
		data, err := os.ReadFile(hostfs.Proc("fs", "nfsd", "exports")) // nfsd monté (cas courant)
		if err != nil {
			data, err = os.ReadFile(hostfs.Proc("fs", "nfs", "exports"))
		}
		if err == nil {
			for _, l := range strings.Split(string(data), "\n") {
				if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
					exports++
				}
			}
			b.Gauge("nfs.server.exports", "export", exports)
		}
		st, msg := model.CheckOK, fmt.Sprintf("serveur NFS en marche (%g fils, %g export(s))", threads, exports)
		if threads == 0 {
			st, msg = model.CheckCrit, "serveur NFS arrêté (aucun fil nfsd)"
		}
		checks = append(checks, model.Check{TS: ts, Name: "nfs.server", Status: st, Message: msg, Tags: tags()})
	}
	n.prev = cur
	return checks, "ok"
}
