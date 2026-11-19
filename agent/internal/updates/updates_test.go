package updates

import "testing"

func TestParseApt(t *testing.T) {
	out := `Listing...
WARNING: apt does not have a stable CLI interface. Use with caution in scripts.
openssl/noble-updates,noble-security 3.0.13-0ubuntu3.5 amd64 [upgradable from: 3.0.13-0ubuntu3.4]
tzdata/noble-updates 2026b-0ubuntu0.24.04 all [upgradable from: 2026a-0ubuntu0.24.04]
`
	l := parseAptUpgradable(out)
	if len(l) != 2 || countSecurity(l) != 1 {
		t.Fatalf("apt : %+v", l)
	}
	if u := l[0]; u.Name != "openssl" || u.Candidate != "3.0.13-0ubuntu3.5" || u.Current != "3.0.13-0ubuntu3.4" || !u.Security {
		t.Fatalf("ligne : %+v", u)
	}
}

func TestParseDnf(t *testing.T) {
	out := `
openssl.x86_64                1:3.0.7-28.el9_5          baseos
kernel.x86_64                 5.14.0-503.15.1.el9_5     baseos
Obsoleting Packages
grub2-tools.x86_64            1:2.06-92.el9             baseos
`
	l := parseDnfCheckUpdate(out)
	if len(l) != 2 || l[0].Name != "openssl" || l[0].Candidate != "1:3.0.7-28.el9_5" {
		t.Fatalf("dnf : %+v", l)
	}
	sec := parseDnfSecurity("RHSA-2026:1234 Important/Sec. openssl-1:3.0.7-28.el9_5.x86_64\nRHSA-2026:1300 Moderate/Sec.  kernel-5.14.0-503.15.1.el9_5.x86_64\n")
	if !sec["openssl"] || !sec["kernel"] || len(sec) != 2 {
		t.Fatalf("dnf sécurité : %v", sec)
	}
	if rpmName("python3-libs-3.9.21-1.el9.x86_64") != "python3-libs" {
		t.Fatal(rpmName("python3-libs-3.9.21-1.el9.x86_64"))
	}
}
