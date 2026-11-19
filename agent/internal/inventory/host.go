// Package inventory décrit l'hôte (contrat § 3.1) : envoyé au démarrage puis toutes les
// 5 minutes, il porte aussi l'état des collecteurs.
package inventory

import (
	"context"
	"net"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"

	"s33d.fr/hypha/agent/internal/model"
)

// Collect rassemble l'inventaire ; une information illisible reste vide plutôt que de
// faire échouer l'envoi.
func Collect(ctx context.Context, hostname, version string, tags map[string]string, excludeIfaces []string, st model.HostStatus) (model.Host, []error) {
	var errs []error
	h := model.Host{
		Hostname:     hostname,
		OS:           runtime.GOOS,
		Arch:         runtime.GOARCH,
		Tags:         tags,
		AgentVersion: version,
		Meta:         map[string]string{},
		Status:       st,
		IPAddresses:  []string{},
	}
	if info, err := host.InfoWithContext(ctx); err != nil {
		errs = append(errs, err)
	} else {
		h.Platform = info.Platform
		h.PlatformVersion = info.PlatformVersion
		h.Kernel = info.KernelVersion
		if v := platformVersion(); v != "" {
			// gopsutil laisse ce champ vide sous Windows Server : la version vient du registre.
			h.PlatformVersion = v
		}
		h.BootTime = int64(info.BootTime) * 1000
		if info.VirtualizationSystem != "" {
			h.Meta["virtualization"] = info.VirtualizationSystem
			h.Meta["virtualization_role"] = info.VirtualizationRole
		}
		if info.PlatformFamily != "" {
			h.Meta["platform_family"] = info.PlatformFamily
		}
	}
	h.CPUCores = runtime.NumCPU()
	if infos, err := cpu.InfoWithContext(ctx); err == nil && len(infos) > 0 {
		h.CPUModel = strings.TrimSpace(infos[0].ModelName)
		if h.CPUModel == "" {
			h.CPUModel = strings.TrimSpace(infos[0].VendorID)
		}
	}
	if phys, err := cpu.CountsWithContext(ctx, false); err == nil && phys > 0 {
		h.Meta["cpu_physical_cores"] = strconv.Itoa(phys)
	}
	if v, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		h.MemTotal = v.Total
	} else {
		errs = append(errs, err)
	}
	h.IPAddresses = ipAddresses(excludeIfaces)
	vendor, product := vendorInfo()
	if vendor != "" {
		h.Meta["vendor"] = vendor
	}
	if product != "" {
		h.Meta["product"] = product
	}
	if tz := timezone(); tz != "" {
		h.Meta["timezone"] = tz
	}
	osMeta(h.Meta)
	return h, errs
}

// ipAddresses rend les adresses des interfaces actives, hors boucle locale, adresses de
// lien local (fe80::, 169.254.) et interfaces virtuelles exclues.
func ipAddresses(exclude []string) []string {
	out := []string{}
	ifs, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 || excluded(ifc.Name, exclude) {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.IsLoopback() || ipn.IP.IsLinkLocalUnicast() {
				continue
			}
			out = append(out, ipn.IP.String())
		}
	}
	return out
}

// Timezone rend le nom du fuseau de l'hôte (inventaire § 15).
func Timezone() string { return timezone() }

func excluded(name string, patterns []string) bool {
	for _, p := range patterns {
		if ok, _ := filepath.Match(p, name); ok {
			return true
		}
	}
	return false
}
