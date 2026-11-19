package integrations

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/process"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/hostfs"
	"s33d.fr/hypha/agent/internal/model"
)

// VirtualBox : VM déclarées (VirtualBox.xml de chaque compte, fichiers .vbox pour le
// démarrage automatique) et VM en marche (processus VBoxHeadless, VirtualBoxVM…).
//
// VBoxManage n'est pas lancé : sous l'unité de l'agent (PrivateTmp), il ne verrait pas la
// socket du VBoxSVC du propriétaire des VM et en démarrerait un second sur la même conf.
type VirtualBox struct {
	cfg config.VirtualBoxConfig
	det detector
	reg cache[[]vbVM]
}

func newVirtualBox(cfg config.VirtualBoxConfig) *VirtualBox {
	return &VirtualBox{cfg: cfg, det: detector{mode: cfg.Enabled}}
}

func (v *VirtualBox) name() string          { return "virtualbox" }
func (v *VirtualBox) close()                {}
func (v *VirtualBox) period() time.Duration { return 60 * time.Second }

type vbVM struct {
	name, uuid, user string
	autostart        bool
}

// parseVBoxRegistry lit VirtualBox.xml : uuid et chemin de chaque VM déclarée.
func parseVBoxRegistry(data []byte) ([][2]string, error) {
	var doc struct {
		Entries []struct {
			UUID string `xml:"uuid,attr"`
			Src  string `xml:"src,attr"`
		} `xml:"Global>MachineRegistry>MachineEntry"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	var out [][2]string
	for _, e := range doc.Entries {
		out = append(out, [2]string{normUUID(e.UUID), e.Src})
	}
	return out, nil
}

// parseVBoxMachine lit un fichier .vbox : nom et démarrage automatique.
func parseVBoxMachine(data []byte) (string, bool, error) {
	var doc struct {
		Machine struct {
			Name      string `xml:"name,attr"`
			Autostart struct {
				Enabled string `xml:"enabled,attr"`
			} `xml:"Autostart"`
		} `xml:"Machine"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		return "", false, err
	}
	return doc.Machine.Name, doc.Machine.Autostart.Enabled == "true", nil
}

func normUUID(s string) string { return strings.ToLower(strings.Trim(s, "{} ")) }

// vboxHomes rend les VirtualBox.xml des comptes de l'hôte (ou du seul compte de la conf).
func vboxHomes(user string) map[string]string {
	out := map[string]string{}
	var homes [][2]string
	if runtime.GOOS == "windows" {
		dirs, _ := filepath.Glob(filepath.Join(os.Getenv("SystemDrive")+`\`, "Users", "*"))
		for _, d := range dirs {
			homes = append(homes, [2]string{filepath.Base(d), d})
		}
	} else {
		data, _ := os.ReadFile(hostfs.Etc("passwd"))
		for _, l := range strings.Split(string(data), "\n") {
			f := strings.Split(l, ":")
			if len(f) >= 7 && f[5] != "" && f[5] != "/" && !strings.HasSuffix(f[6], "nologin") && !strings.HasSuffix(f[6], "false") {
				homes = append(homes, [2]string{f[0], hostfs.Root(f[5])})
			}
		}
	}
	for _, h := range homes {
		if user != "" && !strings.EqualFold(h[0], user) {
			continue
		}
		for _, rel := range []string{".config/VirtualBox/VirtualBox.xml", ".VirtualBox/VirtualBox.xml"} {
			p := filepath.Join(h[1], filepath.FromSlash(rel))
			if _, err := os.Stat(p); err == nil {
				out[h[0]] = p
				break
			}
		}
	}
	return out
}

func (v *VirtualBox) registry() ([]vbVM, error) {
	homes := vboxHomes(v.cfg.User)
	if len(homes) == 0 {
		return nil, errNoData
	}
	var out []vbVM
	for user, p := range homes {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		entries, err := parseVBoxRegistry(data)
		if err != nil {
			continue
		}
		for _, e := range entries {
			vm := vbVM{uuid: e[0], user: user}
			src := e[1]
			if !filepath.IsAbs(src) {
				src = filepath.Join(filepath.Dir(p), src)
			}
			if md, err := os.ReadFile(src); err == nil {
				vm.name, vm.autostart, _ = parseVBoxMachine(md)
			}
			if vm.name == "" {
				vm.name = strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
			}
			out = append(out, vm)
		}
	}
	if len(out) == 0 {
		return nil, errNoData
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

var vmProcesses = map[string]bool{"vboxheadless": true, "virtualboxvm": true, "virtualbox": true, "vboxsdl": true}

// startvmArg rend la VM visée par « --startvm <uuid|nom> » (ou --startvm=…) d'une ligne de commande.
func startvmArg(args []string) string {
	for i, a := range args {
		switch {
		case a == "--startvm" || a == "-startvm" || a == "-s":
			if i+1 < len(args) {
				return args[i+1]
			}
		case strings.HasPrefix(a, "--startvm="):
			return strings.TrimPrefix(a, "--startvm=")
		}
	}
	return ""
}

// runningVMs rend les VM en marche (uuid ou nom, en minuscules).
func runningVMs(ctx context.Context) map[string]bool {
	out := map[string]bool{}
	ps, err := process.ProcessesWithContext(ctx)
	if err != nil {
		return out
	}
	for _, p := range ps {
		n, err := p.NameWithContext(ctx)
		if err != nil {
			continue
		}
		if !vmProcesses[strings.TrimSuffix(strings.ToLower(n), ".exe")] {
			continue
		}
		args, err := p.CmdlineSliceWithContext(ctx)
		if err != nil {
			continue
		}
		if vm := startvmArg(args); vm != "" {
			out[normUUID(vm)] = true
		}
	}
	return out
}

func (v *VirtualBox) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := v.det.skip(now); skip {
		return nil, st
	}
	vms, err := v.reg.get(now, 10*time.Minute, v.registry)
	if err != nil {
		_, st := v.det.failed("aucune VM VirtualBox déclarée")
		return nil, st
	}
	v.det.ok()
	ts := model.Ms(now)
	up := runningVMs(ctx)
	running := 0
	isUp := func(vm vbVM) bool { return up[vm.uuid] || up[strings.ToLower(vm.name)] }
	for _, vm := range vms {
		if isUp(vm) {
			running++
		}
	}
	b.Gauge("virtualbox.vms.total", "count", float64(len(vms)))
	b.Gauge("virtualbox.vms.running", "count", float64(running))
	checks := []model.Check{canConnect("virtualbox", ts, true, fmt.Sprintf("%d VM déclarée(s), %d en marche", len(vms), running), tags())}
	for _, vm := range vms {
		if !vm.autostart {
			continue
		}
		st, msg := model.CheckOK, "en marche"
		if !isUp(vm) {
			st, msg = model.CheckCrit, "arrêtée alors que son démarrage automatique est actif"
		}
		checks = append(checks, model.Check{TS: ts, Name: "virtualbox.vm.running", Status: st, Message: msg,
			Tags: tags("vm", vm.name, "user", vm.user)})
	}
	return checks, "ok"
}
