package integrations

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/hostfs"
	"s33d.fr/hypha/agent/internal/model"
)

// ── Btrfs : /sys/fs/btrfs/<uuid> (allocation, disques, compteurs d'erreurs) ─────────────────
//
// Par système de fichiers monté : espace alloué et occupé par type (data, metadata, system),
// profil de redondance (single, dup, raid1…), espace non alloué (sans lui, un volume « plein »
// de métadonnées refuse l'écriture alors que df montre de la place), disques manquants, et
// compteurs d'erreurs par disque (noyau 5.14 et plus ; sinon « btrfs device stats »).

type Btrfs struct {
	det  detector
	seen map[string]bool
}

func newBtrfs(cfg config.IntegrationToggle) *Btrfs {
	return &Btrfs{det: detector{mode: cfg.Enabled}, seen: map[string]bool{}}
}

func (x *Btrfs) name() string          { return "btrfs" }
func (x *Btrfs) period() time.Duration { return 60 * time.Second }
func (x *Btrfs) close()                {}

// btrfsMounts : point de montage le plus court par périphérique source (sous-volumes compris).
func btrfsMounts() map[string]string {
	out := map[string]string{}
	f, err := os.Open(hostfs.Proc("1", "mountinfo"))
	if err != nil {
		if f, err = os.Open(hostfs.Proc("self", "mountinfo")); err != nil {
			return out
		}
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		// … point_de_montage options… - type source options
		l := sc.Text()
		i := strings.Index(l, " - ")
		if i < 0 {
			continue
		}
		pre, post := strings.Fields(l[:i]), strings.Fields(l[i+3:])
		if len(pre) < 5 || len(post) < 2 || post[0] != "btrfs" {
			continue
		}
		mnt, src := unescapeMount(pre[4]), post[1]
		if cur, ok := out[src]; !ok || len(mnt) < len(cur) {
			out[src] = mnt
		}
	}
	return out
}

func unescapeMount(s string) string {
	return strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(s)
}

func readNum(path string) (float64, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
	return v, err == nil
}

type btrfsFS struct {
	uuid, label, mount string
	devices            []string // noms des périphériques (sda1, loop0, dm-0)
}

// btrfsList : systèmes de fichiers Btrfs montés, avec leur point de montage.
func btrfsList() ([]btrfsFS, error) {
	root := hostfs.Sys("fs", "btrfs")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	mounts := btrfsMounts()
	byDev := map[string]string{}
	for src, mnt := range mounts {
		dev := filepath.Base(src)
		if r, err := filepath.EvalSymlinks(hostfs.Root(src)); err == nil {
			dev = filepath.Base(r) // /dev/mapper/x → dm-0
		}
		byDev[dev] = mnt
		byDev[filepath.Base(src)] = mnt
	}
	var out []btrfsFS
	for _, e := range entries {
		if len(e.Name()) != 36 || strings.Count(e.Name(), "-") != 4 {
			continue // « features », etc.
		}
		fs := btrfsFS{uuid: e.Name()}
		if b, err := os.ReadFile(filepath.Join(root, e.Name(), "label")); err == nil {
			fs.label = strings.TrimSpace(string(b))
		}
		devs, _ := os.ReadDir(filepath.Join(root, e.Name(), "devices"))
		for _, d := range devs {
			fs.devices = append(fs.devices, d.Name())
			if m, ok := byDev[d.Name()]; ok && (fs.mount == "" || len(m) < len(fs.mount)) {
				fs.mount = m
			}
		}
		if fs.mount == "" {
			continue // connu du noyau (disques analysés) mais démonté : rien à mesurer
		}
		out = append(out, fs)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].uuid < out[j].uuid })
	return out, nil
}

// btrfsErrors : compteurs d'erreurs par identifiant de disque (devinfo/<id>/error_stats).
func btrfsErrors(dir string) map[string]map[string]float64 {
	out := map[string]map[string]float64{}
	ids, _ := os.ReadDir(filepath.Join(dir, "devinfo"))
	for _, id := range ids {
		b, err := os.ReadFile(filepath.Join(dir, "devinfo", id.Name(), "error_stats"))
		if err != nil {
			continue
		}
		m := map[string]float64{}
		for _, l := range strings.Split(string(b), "\n") {
			f := strings.Fields(l)
			if len(f) == 2 {
				v, _ := strconv.ParseFloat(f[1], 64)
				m[strings.TrimSuffix(f[0], "_errs")] = v
			}
		}
		out[id.Name()] = m
	}
	return out
}

// btrfsDevStats : repli par l'outil (« [/dev/sda1].write_io_errs   0 »), par chemin de disque.
func btrfsDevStats(ctx context.Context, mount string) map[string]map[string]float64 {
	tool, ok := findTool("btrfs")
	if !ok || mount == "" {
		return nil
	}
	out, err := runTool(ctx, 10*time.Second, tool, "device", "stats", hostfs.Root(mount))
	if err != nil && len(out) == 0 {
		return nil
	}
	res := map[string]map[string]float64{}
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Fields(l)
		if len(f) != 2 || !strings.HasPrefix(f[0], "[") {
			continue
		}
		dev, kind, ok := strings.Cut(strings.TrimPrefix(f[0], "["), "].")
		if !ok {
			continue
		}
		kind = strings.TrimSuffix(strings.TrimSuffix(kind, "_errs"), "_io")
		v, _ := strconv.ParseFloat(f[1], 64)
		if res[dev] == nil {
			res[dev] = map[string]float64{}
		}
		res[dev][kind] = v
	}
	return res
}

func (x *Btrfs) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := x.det.skip(now); skip {
		return nil, st
	}
	ts := model.Ms(now)
	list, err := btrfsList()
	if err == nil && len(list) == 0 {
		err = fmt.Errorf("aucun système de fichiers Btrfs monté")
	}
	if err != nil {
		if silent, st := x.det.failed(err.Error()); silent {
			return nil, st
		}
		if len(x.seen) == 0 {
			return nil, "erreur : " + err.Error()
		}
		list = nil
	}
	x.det.ok()
	var checks []model.Check
	for _, fs := range list {
		x.seen[fs.uuid] = true
		dir := hostfs.Sys("fs", "btrfs", fs.uuid)
		mount := fs.mount
		if mount == "" {
			mount = fs.uuid
		}
		kv := []string{"mount", mount, "btrfs_uuid", fs.uuid}
		if fs.label != "" {
			kv = append(kv, "btrfs_label", fs.label)
		}
		var allocated float64
		for _, typ := range []string{"data", "metadata", "system", "mixed"} {
			ad := filepath.Join(dir, "allocation", typ)
			total, ok := readNum(filepath.Join(ad, "total_bytes"))
			if !ok {
				continue
			}
			used, _ := readNum(filepath.Join(ad, "bytes_used"))
			diskTotal, _ := readNum(filepath.Join(ad, "disk_total"))
			allocated += diskTotal
			profile := "single"
			if subs, err := os.ReadDir(ad); err == nil {
				for _, s := range subs {
					if s.IsDir() {
						profile = s.Name()
					}
				}
			}
			tkv := append(append([]string{}, kv...), "usage_type", typ, "replication_type", profile)
			b.Gauge("btrfs.allocated", "byte", total, tkv...)
			b.Gauge("btrfs.used", "byte", used, tkv...)
			b.Gauge("btrfs.free", "byte", total-used, tkv...)
			if total > 0 {
				b.Gauge("btrfs.usage", "percent", round1(100*used/total), tkv...)
			}
		}
		// Taille des disques (secteurs de 512 octets du périphérique bloc).
		var size float64
		for _, d := range fs.devices {
			if v, ok := readNum(filepath.Join(dir, "devices", d, "size")); ok {
				size += v * 512
			}
		}
		if size > 0 {
			b.Gauge("btrfs.size", "byte", size, kv...)
			if allocated > 0 && size >= allocated {
				b.Gauge("btrfs.unallocated", "byte", size-allocated, kv...)
				b.Gauge("btrfs.unallocated.pct", "percent", round1(100*(size-allocated)/size), kv...)
			}
		}
		// Disques manquants et compteurs d'erreurs.
		missing := 0
		ids, _ := os.ReadDir(filepath.Join(dir, "devinfo"))
		for _, id := range ids {
			if v, ok := readNum(filepath.Join(dir, "devinfo", id.Name(), "missing")); ok && v > 0 {
				missing++
			}
		}
		b.Gauge("btrfs.devices.missing", "device", float64(missing), kv...)
		b.Gauge("btrfs.devices", "device", float64(len(fs.devices)), kv...)
		errs := btrfsErrors(dir)
		if len(errs) == 0 {
			errs = btrfsDevStats(ctx, fs.mount)
		}
		var withErrors []string
		devs := make([]string, 0, len(errs))
		for d := range errs {
			devs = append(devs, d)
		}
		sort.Strings(devs)
		for _, d := range devs {
			var sum float64
			for kind, v := range errs[d] {
				b.Gauge("btrfs.device.errors", "error", v, append(append([]string{}, kv...), "device", d, "error_type", kind)...)
				sum += v
			}
			if sum > 0 {
				withErrors = append(withErrors, fmt.Sprintf("disque %s : %g", d, sum))
			}
		}
		st, msg := model.CheckOK, fmt.Sprintf("%d disque(s), aucune erreur", len(fs.devices))
		switch {
		case missing > 0:
			st, msg = model.CheckCrit, fmt.Sprintf("%d disque(s) manquant(s) : volume dégradé", missing)
		case len(withErrors) > 0:
			st, msg = model.CheckWarn, "compteurs d'erreurs non nuls ("+strings.Join(withErrors, ", ")+
				") ; après examen : btrfs device stats -z "+fs.mount
		}
		checks = append(checks, model.Check{TS: ts, Name: "btrfs.health", Status: st, Message: msg, Tags: tags(kv...)})
	}
	return checks, "ok"
}
