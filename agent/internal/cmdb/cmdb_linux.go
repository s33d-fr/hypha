//go:build linux

package cmdb

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/host"

	"s33d.fr/hypha/agent/internal/hostfs"
	"s33d.fr/hypha/agent/internal/inventory"
	"s33d.fr/hypha/agent/internal/model"
)

func readTrim(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if _, err := exec.LookPath(name); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	return cmd.Output()
}

// Collect : /sys/class/dmi/id, /proc/cpuinfo, dmidecode -t 17, lsblk, os-release,
// dpkg-query ou rpm. Une source illisible laisse sa partie vide.
func Collect(ctx context.Context, packages bool) (model.Inventory, []error) {
	var errs []error
	dmi := func(f string) string { return readTrim(hostfs.Sys("class/dmi/id", f)) }
	inv := model.Inventory{
		Hardware: model.Hardware{
			Vendor: dmi("sys_vendor"), Product: dmi("product_name"), Serial: dmi("product_serial"),
			BIOSVendor: dmi("bios_vendor"), BIOSVersion: dmi("bios_version"), BIOSDate: dmi("bios_date"),
			MemoryModules: []model.MemoryModule{}, Disks: []model.Disk{},
		},
		Packages: []model.Package{},
	}
	if b, err := os.ReadFile(hostfs.Proc("cpuinfo")); err == nil {
		inv.Hardware.CPU = parseCPUInfo(string(b))
	} else {
		errs = append(errs, err)
	}
	if inv.Hardware.CPU.Model == "" {
		if infos, err := cpu.InfoWithContext(ctx); err == nil && len(infos) > 0 {
			inv.Hardware.CPU.Model = strings.TrimSpace(infos[0].ModelName)
		}
	}
	if out, err := run(ctx, "dmidecode", "-t", "17"); err == nil {
		inv.Hardware.MemoryModules = parseDmidecodeMemory(string(out))
	}
	if out, err := run(ctx, "lsblk", "-J", "-b", "-d", "-o", "NAME,MODEL,SERIAL,SIZE,ROTA,TYPE"); err == nil {
		if disks, err := parseLsblk(out); err == nil {
			inv.Hardware.Disks = disks
		} else {
			errs = append(errs, err)
		}
	}
	if _, err := exec.LookPath("smartctl"); err == nil {
		for i := range inv.Hardware.Disks {
			inv.Hardware.Disks[i].SMART = smartHealth(ctx, "/dev/"+inv.Hardware.Disks[i].Name)
		}
	}
	info, err := host.InfoWithContext(ctx)
	if err != nil {
		errs = append(errs, err)
	} else {
		inv.Hardware.Virtual = virtualOf(info.VirtualizationSystem, info.VirtualizationRole)
		inv.OS.Kernel = info.KernelVersion
		inv.OS.LastBoot = int64(info.BootTime) * 1000
	}
	rel := parseOSRelease(readTrim(hostfs.Etc("os-release")))
	inv.OS.Name = rel["NAME"]
	inv.OS.Version = rel["VERSION_ID"]
	inv.OS.Codename = rel["VERSION_CODENAME"]
	inv.OS.Timezone = inventory.Timezone()
	inv.OS.InstalledAt = installedAt()
	if packages {
		pk, err := listPackages(ctx)
		if err != nil {
			errs = append(errs, err)
		}
		inv.Packages = pk
	}
	return inv, errs
}

// smartHealth : verdict global SMART, sans réveiller un disque en veille (-n standby).
func smartHealth(ctx context.Context, dev string) string {
	out, _ := run(ctx, "smartctl", "-n", "standby", "-H", "-j", dev)
	var r struct {
		SmartStatus *struct {
			Passed bool `json:"passed"`
		} `json:"smart_status"`
	}
	if json.Unmarshal(out, &r) != nil || r.SmartStatus == nil {
		return ""
	}
	if r.SmartStatus.Passed {
		return "PASSED"
	}
	return "FAILED"
}

// installedAt : date de pose du système, approchée par le plus ancien de ces témoins.
func installedAt() int64 {
	var best time.Time
	for _, p := range []string{"/var/log/installer", "/etc/machine-id", "/lost+found", "/root/anaconda-ks.cfg"} {
		fi, err := os.Stat(hostfs.Root(p))
		if err != nil {
			continue
		}
		if best.IsZero() || fi.ModTime().Before(best) {
			best = fi.ModTime()
		}
	}
	if best.IsZero() {
		return 0
	}
	return best.UnixMilli()
}

func listPackages(ctx context.Context) ([]model.Package, error) {
	if out, err := run(ctx, "dpkg-query", "-W", "-f", "${Package}\t${Version}\t${Architecture}\t${db:Status-Abbrev}\n"); err == nil {
		return parseDpkg(string(out)), nil
	}
	if out, err := run(ctx, "rpm", "-qa", "--qf", "%{NAME}\t%{EPOCHNUM}\t%{VERSION}-%{RELEASE}\t%{ARCH}\n"); err == nil {
		return parseRPM(string(out)), nil
	}
	return []model.Package{}, errors.New("ni dpkg-query ni rpm : paquets non listés")
}
