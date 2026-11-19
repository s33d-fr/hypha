package cmdb

import (
	"bufio"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"s33d.fr/hypha/agent/internal/model"
)

// parseCPUInfo lit /proc/cpuinfo : modèle, sockets (physical id), cœurs (core id distincts
// par socket), fils (processor).
func parseCPUInfo(text string) model.CPUInfo {
	var c model.CPUInfo
	sockets := map[string]bool{}
	cores := map[string]bool{}
	phys := "0"
	hardware := ""
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "processor":
			c.Threads++
		case "model name", "Model":
			if c.Model == "" {
				c.Model = v
			}
		case "Hardware":
			hardware = v
		case "physical id":
			phys = v
			sockets[v] = true
		case "core id":
			cores[phys+"/"+v] = true
		}
	}
	if c.Model == "" {
		c.Model = hardware
	}
	c.Sockets = max(len(sockets), 1)
	c.Cores = len(cores)
	if c.Cores == 0 {
		c.Cores = c.Threads
	}
	return c
}

// parseDmidecodeMemory lit « dmidecode -t 17 » : une barrette par bloc « Memory Device »
// occupé (Size: 16 GB, Type: DDR4, Speed: 2666 MT/s, Locator: DIMM_A1).
func parseDmidecodeMemory(text string) []model.MemoryModule {
	var out []model.MemoryModule
	var cur *model.MemoryModule
	flush := func() {
		if cur != nil && cur.Size > 0 {
			out = append(out, *cur)
		}
		cur = nil
	}
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "Memory Device" {
			flush()
			cur = &model.MemoryModule{}
			continue
		}
		if cur == nil || !strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "\t\t") {
			continue
		}
		k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch k {
		case "Size":
			cur.Size = parseSize(v)
		case "Type":
			if v != "Unknown" && v != "Other" {
				cur.Type = v
			}
		case "Speed":
			if f := strings.Fields(v); len(f) > 0 {
				cur.SpeedMHz, _ = strconv.Atoi(f[0])
			}
		case "Locator":
			cur.Slot = v
		}
	}
	flush()
	return out
}

// parseSize lit « 16 GB », « 16384 MB », « 512 kB » ; 0 si illisible ou « No Module Installed ».
func parseSize(v string) uint64 {
	f := strings.Fields(v)
	if len(f) != 2 {
		return 0
	}
	n, err := strconv.ParseUint(f[0], 10, 64)
	if err != nil {
		return 0
	}
	switch strings.ToUpper(f[1]) {
	case "KB":
		return n << 10
	case "MB":
		return n << 20
	case "GB":
		return n << 30
	case "TB":
		return n << 40
	}
	return 0
}

// parseLsblk lit « lsblk -J -b -d -o NAME,MODEL,SERIAL,SIZE,ROTA,TYPE » ; selon la version de
// lsblk, taille et ROTA sont des nombres, des booléens ou du texte.
func parseLsblk(data []byte) ([]model.Disk, error) {
	var doc struct {
		Blockdevices []map[string]any `json:"blockdevices"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("lsblk : %w", err)
	}
	var out []model.Disk
	for _, d := range doc.Blockdevices {
		if str(d["type"]) != "disk" {
			continue
		}
		name := str(d["name"])
		if strings.HasPrefix(name, "zram") || strings.HasPrefix(name, "loop") {
			continue
		}
		out = append(out, model.Disk{
			Name: name, Model: strings.TrimSpace(str(d["model"])), Serial: strings.TrimSpace(str(d["serial"])),
			Size: num(d["size"]), Rotational: truthy(d["rota"]),
		})
	}
	return out, nil
}

func str(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return ""
}

func num(v any) uint64 {
	switch x := v.(type) {
	case float64:
		return uint64(x)
	case string:
		n, _ := strconv.ParseUint(strings.TrimSpace(x), 10, 64)
		return n
	}
	return 0
}

func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case float64:
		return x != 0
	case string:
		return x == "1" || x == "true"
	}
	return false
}

// parseDpkg lit « dpkg-query -W -f '${Package}\t${Version}\t${Architecture}\t${db:Status-Abbrev}\n' »
// et garde les paquets installés (ii).
func parseDpkg(text string) []model.Package {
	var out []model.Package
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		f := strings.Split(sc.Text(), "\t")
		if len(f) < 4 || !strings.HasPrefix(strings.TrimSpace(f[3]), "ii") {
			continue
		}
		out = append(out, model.Package{Name: f[0], Version: f[1], Arch: f[2], Source: "dpkg"})
	}
	return out
}

// parseRPM lit « rpm -qa --qf '%{NAME}\t%{EPOCHNUM}\t%{VERSION}-%{RELEASE}\t%{ARCH}\n' » ;
// l'époque n'est écrite que si elle n'est pas nulle (1:3.0.7-27.el9).
func parseRPM(text string) []model.Package {
	var out []model.Package
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		f := strings.Split(sc.Text(), "\t")
		if len(f) < 4 || f[0] == "gpg-pubkey" {
			continue
		}
		v := f[2]
		if f[1] != "0" && f[1] != "" && f[1] != "(none)" {
			v = f[1] + ":" + v
		}
		arch := f[3]
		if arch == "(none)" {
			arch = ""
		}
		out = append(out, model.Package{Name: f[0], Version: v, Arch: arch, Source: "rpm"})
	}
	return out
}

// parseOSRelease lit /etc/os-release.
func parseOSRelease(text string) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if !ok || strings.HasPrefix(k, "#") {
			continue
		}
		if u, err := strconv.Unquote(v); err == nil {
			v = u
		} else {
			v = strings.Trim(v, `'"`)
		}
		out[k] = v
	}
	return out
}

// virtualOf ramène le système de virtualisation à un nom court (kvm, virtualbox, vmware,
// hyperv, xen, docker, lxc…) ; none pour une machine physique ou un hôte d'hyperviseur.
func virtualOf(system, role string) string {
	if role != "guest" || system == "" {
		return "none"
	}
	switch s := strings.ToLower(system); {
	case strings.Contains(s, "vbox"), strings.Contains(s, "virtualbox"):
		return "virtualbox"
	case strings.Contains(s, "hyper"):
		return "hyperv"
	case strings.Contains(s, "qemu"):
		return "kvm"
	default:
		return s
	}
}
