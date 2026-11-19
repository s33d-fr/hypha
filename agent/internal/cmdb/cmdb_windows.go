//go:build windows

package cmdb

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/yusufpapurcu/wmi"
	"golang.org/x/sys/windows/registry"

	"s33d.fr/hypha/agent/internal/inventory"
	"s33d.fr/hypha/agent/internal/model"
)

type win32ComputerSystem struct {
	Manufacturer string
	Model        string
}

type win32BIOS struct {
	Manufacturer      string
	SMBIOSBIOSVersion string
	SerialNumber      string
	ReleaseDate       time.Time
}

type win32Processor struct {
	Name                      string
	NumberOfCores             uint32
	NumberOfLogicalProcessors uint32
}

type win32PhysicalMemory struct {
	Capacity         uint64
	SMBIOSMemoryType uint32
	Speed            uint32
	DeviceLocator    string
	BankLabel        string
}

type win32DiskDrive struct {
	Index        uint32
	Model        string
	SerialNumber string
	Size         uint64
	Status       string
}

type msftPhysicalDisk struct {
	DeviceId  string
	MediaType uint16 // 3 = HDD, 4 = SSD, 5 = SCM
}

type win32OperatingSystem struct {
	Caption        string
	Version        string
	InstallDate    time.Time
	LastBootUpTime time.Time
}

// Types de barrette SMBIOS (DSP0134, tableau 76).
var memTypes = map[uint32]string{18: "DDR", 19: "DDR2", 20: "DDR2 FB-DIMM", 24: "DDR3", 26: "DDR4", 27: "LPDDR", 28: "LPDDR2",
	29: "LPDDR3", 30: "LPDDR4", 34: "DDR5", 35: "LPDDR5"}

// Collect : WMI (Win32_ComputerSystem, Win32_BIOS, Win32_Processor, Win32_PhysicalMemory,
// Win32_DiskDrive, Win32_OperatingSystem) et registre Uninstall (64 et 32 bits).
func Collect(ctx context.Context, packages bool) (model.Inventory, []error) {
	var errs []error
	inv := model.Inventory{Hardware: model.Hardware{MemoryModules: []model.MemoryModule{}, Disks: []model.Disk{}}, Packages: []model.Package{}}
	query := func(q string, dst any) bool {
		if err := wmi.Query(q, dst); err != nil {
			errs = append(errs, fmt.Errorf("WMI %s : %w", q, err))
			return false
		}
		return true
	}
	var cs []win32ComputerSystem
	if query("SELECT Manufacturer, Model FROM Win32_ComputerSystem", &cs) && len(cs) > 0 {
		inv.Hardware.Vendor, inv.Hardware.Product = strings.TrimSpace(cs[0].Manufacturer), strings.TrimSpace(cs[0].Model)
		inv.Hardware.Virtual = windowsVirtual(cs[0].Manufacturer, cs[0].Model)
	}
	var bios []win32BIOS
	if query("SELECT Manufacturer, SMBIOSBIOSVersion, SerialNumber, ReleaseDate FROM Win32_BIOS", &bios) && len(bios) > 0 {
		b := bios[0]
		inv.Hardware.BIOSVendor, inv.Hardware.BIOSVersion, inv.Hardware.Serial = b.Manufacturer, b.SMBIOSBIOSVersion, strings.TrimSpace(b.SerialNumber)
		if !b.ReleaseDate.IsZero() {
			inv.Hardware.BIOSDate = b.ReleaseDate.Format("01/02/2006")
		}
	}
	var cpus []win32Processor
	if query("SELECT Name, NumberOfCores, NumberOfLogicalProcessors FROM Win32_Processor", &cpus) && len(cpus) > 0 {
		inv.Hardware.CPU.Model = strings.TrimSpace(cpus[0].Name)
		inv.Hardware.CPU.Sockets = len(cpus)
		for _, c := range cpus {
			inv.Hardware.CPU.Cores += int(c.NumberOfCores)
			inv.Hardware.CPU.Threads += int(c.NumberOfLogicalProcessors)
		}
	}
	var mem []win32PhysicalMemory
	if query("SELECT Capacity, SMBIOSMemoryType, Speed, DeviceLocator, BankLabel FROM Win32_PhysicalMemory", &mem) {
		for _, m := range mem {
			slot := strings.TrimSpace(m.DeviceLocator)
			if slot == "" {
				slot = strings.TrimSpace(m.BankLabel)
			}
			inv.Hardware.MemoryModules = append(inv.Hardware.MemoryModules, model.MemoryModule{
				Size: m.Capacity, Type: memTypes[m.SMBIOSMemoryType], SpeedMHz: int(m.Speed), Slot: slot})
		}
	}
	var disks []win32DiskDrive
	if query("SELECT Index, Model, SerialNumber, Size, Status FROM Win32_DiskDrive", &disks) {
		media := map[string]uint16{}
		var pd []msftPhysicalDisk
		if err := wmi.QueryNamespace("SELECT DeviceId, MediaType FROM MSFT_PhysicalDisk", &pd, `root\Microsoft\Windows\Storage`); err == nil {
			for _, p := range pd {
				media[p.DeviceId] = p.MediaType
			}
		}
		for _, d := range disks {
			disk := model.Disk{Name: "PhysicalDrive" + strconv.Itoa(int(d.Index)), Model: strings.TrimSpace(d.Model),
				Serial: strings.TrimSpace(d.SerialNumber), Size: d.Size, Rotational: media[strconv.Itoa(int(d.Index))] == 3}
			switch d.Status {
			case "OK":
				disk.SMART = "PASSED"
			case "Pred Fail":
				disk.SMART = "FAILED"
			}
			inv.Hardware.Disks = append(inv.Hardware.Disks, disk)
		}
	}
	var osInfo []win32OperatingSystem
	if query("SELECT Caption, Version, InstallDate, LastBootUpTime FROM Win32_OperatingSystem", &osInfo) && len(osInfo) > 0 {
		o := osInfo[0]
		inv.OS.Name, inv.OS.Version = strings.TrimSpace(o.Caption), o.Version
		if !o.InstallDate.IsZero() {
			inv.OS.InstalledAt = o.InstallDate.UnixMilli()
		}
		if !o.LastBootUpTime.IsZero() {
			inv.OS.LastBoot = o.LastBootUpTime.UnixMilli()
		}
	}
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE); err == nil {
		inv.OS.Codename, _, _ = k.GetStringValue("DisplayVersion")
		build, _, _ := k.GetStringValue("CurrentBuildNumber")
		if ubr, _, err := k.GetIntegerValue("UBR"); err == nil && build != "" {
			build += "." + strconv.FormatUint(ubr, 10)
		}
		inv.OS.Kernel = build
		k.Close()
	}
	inv.OS.Timezone = inventory.Timezone()
	if packages {
		inv.Packages = uninstallEntries()
	}
	return inv, errs
}

// windowsVirtual reconnaît une machine virtuelle à son fabricant ou modèle.
func windowsVirtual(vendor, product string) string {
	s := strings.ToLower(vendor + " " + product)
	switch {
	case strings.Contains(s, "virtualbox") || strings.Contains(s, "innotek"):
		return "virtualbox"
	case strings.Contains(s, "vmware"):
		return "vmware"
	case strings.Contains(s, "microsoft") && strings.Contains(s, "virtual"):
		return "hyperv"
	case strings.Contains(s, "kvm") || strings.Contains(s, "qemu") || strings.Contains(s, "standard pc"):
		return "kvm"
	case strings.Contains(s, "xen"):
		return "xen"
	}
	return "none"
}

// uninstallEntries lit les logiciels installés (Uninstall, vues 64 et 32 bits), sans les
// composants système ni les mises à jour rattachées à un produit.
func uninstallEntries() []model.Package {
	out := []model.Package{}
	seen := map[string]bool{}
	for _, v := range []struct {
		path, arch string
	}{
		{`SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`, "x64"},
		{`SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`, "x86"},
	} {
		k, err := registry.OpenKey(registry.LOCAL_MACHINE, v.path, registry.ENUMERATE_SUB_KEYS)
		if err != nil {
			continue
		}
		names, _ := k.ReadSubKeyNames(-1)
		k.Close()
		for _, n := range names {
			sk, err := registry.OpenKey(registry.LOCAL_MACHINE, v.path+`\`+n, registry.QUERY_VALUE)
			if err != nil {
				continue
			}
			name, _, _ := sk.GetStringValue("DisplayName")
			ver, _, _ := sk.GetStringValue("DisplayVersion")
			sys, _, _ := sk.GetIntegerValue("SystemComponent")
			parent, _, _ := sk.GetStringValue("ParentKeyName")
			sk.Close()
			name = strings.TrimSpace(name)
			if name == "" || sys == 1 || parent != "" || seen[name+"|"+v.arch] {
				continue
			}
			seen[name+"|"+v.arch] = true
			out = append(out, model.Package{Name: name, Version: strings.TrimSpace(ver), Arch: v.arch, Source: "windows"})
		}
	}
	return out
}
