//go:build linux

package inventory

import (
	"os"
	"path/filepath"
	"strings"

	"s33d.fr/hypha/agent/internal/hostfs"
)

// vendorInfo lit le fabricant et le modèle exposés par le microprogramme (DMI).
func vendorInfo() (vendor, product string) {
	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(b))
	}
	return read(hostfs.Sys("class/dmi/id/sys_vendor")), read(hostfs.Sys("class/dmi/id/product_name"))
}

// timezone rend le nom du fuseau (Europe/Paris) : variable TZ, /etc/timezone, ou cible
// du lien /etc/localtime.
func timezone() string {
	if tz := os.Getenv("TZ"); tz != "" {
		return strings.TrimPrefix(tz, ":")
	}
	if b, err := os.ReadFile(hostfs.Etc("timezone")); err == nil {
		if tz := strings.TrimSpace(string(b)); tz != "" {
			return tz
		}
	}
	if target, err := os.Readlink(hostfs.Etc("localtime")); err == nil {
		if i := strings.Index(target, "zoneinfo/"); i >= 0 {
			return filepath.ToSlash(target[i+len("zoneinfo/"):])
		}
	}
	return ""
}

// platformVersion : sous Linux, celle de gopsutil (/etc/os-release) suffit.
func platformVersion() string { return "" }

// osMeta : rien de plus sous Linux.
func osMeta(map[string]string) {}
