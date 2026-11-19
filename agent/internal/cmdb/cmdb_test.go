package cmdb

import (
	"testing"

	"s33d.fr/hypha/agent/internal/model"
)

func TestParseCPUInfo(t *testing.T) {
	var text string
	for s := 0; s < 2; s++ {
		for c := 0; c < 2; c++ {
			for th := 0; th < 2; th++ {
				text += "processor\t: x\nmodel name\t: Intel(R) Xeon(R) Gold 6130\nphysical id\t: " + string(rune('0'+s)) +
					"\ncore id\t\t: " + string(rune('0'+c)) + "\n\n"
			}
		}
	}
	c := parseCPUInfo(text)
	if c.Model != "Intel(R) Xeon(R) Gold 6130" || c.Sockets != 2 || c.Cores != 4 || c.Threads != 8 {
		t.Fatalf("x86 : %+v", c)
	}
	arm := parseCPUInfo("processor\t: 0\nBogoMIPS\t: 50.00\n\nprocessor\t: 1\n\nHardware\t: BCM2835\nModel\t\t: Raspberry Pi 4 Model B Rev 1.4\n")
	if arm.Model != "Raspberry Pi 4 Model B Rev 1.4" || arm.Sockets != 1 || arm.Threads != 2 || arm.Cores != 2 {
		t.Fatalf("arm : %+v", arm)
	}
}

func TestParseDmidecode(t *testing.T) {
	out := `# dmidecode 3.5
Handle 0x1100, DMI type 17, 92 bytes
Memory Device
	Array Handle: 0x1000
	Size: 16 GB
	Form Factor: DIMM
	Locator: A1
	Bank Locator: Not Specified
	Type: DDR4
	Speed: 2666 MT/s

Handle 0x1101, DMI type 17, 92 bytes
Memory Device
	Size: No Module Installed
	Locator: A2
	Type: Unknown

Handle 0x1102, DMI type 17, 92 bytes
Memory Device
	Size: 8192 MB
	Locator: B1
	Type: DDR4
	Speed: 2400 MHz
`
	m := parseDmidecodeMemory(out)
	if len(m) != 2 || m[0].Size != 16<<30 || m[0].Slot != "A1" || m[0].SpeedMHz != 2666 || m[1].Size != 8<<30 || m[1].Type != "DDR4" {
		t.Fatalf("barrettes : %+v", m)
	}
}

func TestParseLsblk(t *testing.T) {
	recent := `{"blockdevices":[{"name":"sda","model":"Samsung SSD 870","serial":"S6P","size":1000204886016,"rota":false,"type":"disk"},
	{"name":"sr0","model":"DVD","serial":null,"size":1073741312,"rota":true,"type":"rom"},
	{"name":"loop0","model":null,"serial":null,"size":4096,"rota":false,"type":"loop"}]}`
	old := `{"blockdevices":[{"name":"vda","model":null,"serial":null,"size":"53687091200","rota":"1","type":"disk"}]}`
	d, err := parseLsblk([]byte(recent))
	if err != nil || len(d) != 1 || d[0].Size != 1000204886016 || d[0].Rotational || d[0].Model != "Samsung SSD 870" {
		t.Fatalf("lsblk récent : %+v %v", d, err)
	}
	d, err = parseLsblk([]byte(old))
	if err != nil || len(d) != 1 || d[0].Size != 53687091200 || !d[0].Rotational {
		t.Fatalf("lsblk ancien : %+v %v", d, err)
	}
}

func TestParsePackages(t *testing.T) {
	deb := parseDpkg("openssl\t3.0.13-0ubuntu3.4\tamd64\tii \nrc-pkg\t1.0\tall\trc \nlibc6\t2.39-0ubuntu8\tamd64\tii \n")
	if len(deb) != 2 || deb[0].Name != "openssl" || deb[0].Source != "dpkg" {
		t.Fatalf("dpkg : %+v", deb)
	}
	rpm := parseRPM("openssl\t1\t3.0.7-27.el9\tx86_64\nbash\t0\t5.1.8-9.el9\tx86_64\ngpg-pubkey\t0\tfd431d51-4ae0493b\t(none)\n")
	if len(rpm) != 2 || rpm[0].Version != "1:3.0.7-27.el9" || rpm[1].Version != "5.1.8-9.el9" {
		t.Fatalf("rpm : %+v", rpm)
	}
	rel := parseOSRelease("NAME=\"Ubuntu\"\nVERSION_ID=\"26.04\"\nVERSION_CODENAME=resolute\n# x\n")
	if rel["NAME"] != "Ubuntu" || rel["VERSION_ID"] != "26.04" || rel["VERSION_CODENAME"] != "resolute" {
		t.Fatalf("os-release : %v", rel)
	}
	if virtualOf("kvm", "guest") != "kvm" || virtualOf("vbox", "guest") != "virtualbox" || virtualOf("kvm", "host") != "none" {
		t.Fatal("virtualisation")
	}
}

func TestFingerprint(t *testing.T) {
	a := model.Inventory{Packages: []model.Package{{Name: "b"}, {Name: "a"}}}
	b := model.Inventory{Packages: []model.Package{{Name: "a"}, {Name: "b"}}}
	if Fingerprint(a) != Fingerprint(b) {
		t.Fatal("l'empreinte dépend de l'ordre des paquets")
	}
	b.Packages[0].Version = "2"
	if Fingerprint(a) == Fingerprint(b) {
		t.Fatal("empreinte identique après un changement de version")
	}
}
