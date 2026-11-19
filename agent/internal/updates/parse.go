package updates

import (
	"bufio"
	"strings"

	"s33d.fr/hypha/agent/internal/model"
)

// parseAptUpgradable lit « apt list --upgradable » :
//
//	openssl/noble-updates,noble-security 3.0.13-0ubuntu3.5 amd64 [upgradable from: 3.0.13-0ubuntu3.4]
//
// une mise à jour est de sécurité si l'une de ses suites porte « -security ».
func parseAptUpgradable(out string) []model.PendingUpdate {
	var list []model.PendingUpdate
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		name, rest, ok := strings.Cut(line, "/")
		if !ok || strings.HasPrefix(line, "Listing") || strings.HasPrefix(line, "WARNING") || strings.Contains(name, " ") {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 2 {
			continue
		}
		u := model.PendingUpdate{Name: name, Candidate: f[1], Security: strings.Contains(f[0], "-security")}
		if i := strings.Index(rest, "[upgradable from: "); i >= 0 {
			u.Current = strings.TrimSuffix(strings.TrimSpace(rest[i+len("[upgradable from: "):]), "]")
		}
		list = append(list, u)
	}
	return list
}

// parseDnfCheckUpdate lit « dnf -q --cacheonly check-update » (nom.arch version dépôt) ;
// la section « Obsoleting Packages » est ignorée.
func parseDnfCheckUpdate(out string) []model.PendingUpdate {
	var list []model.PendingUpdate
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "Obsoleting") {
			break
		}
		f := strings.Fields(line)
		if len(f) != 3 || strings.HasPrefix(line, " ") {
			continue
		}
		name := f[0]
		if i := strings.LastIndex(name, "."); i > 0 {
			name = name[:i]
		}
		list = append(list, model.PendingUpdate{Name: name, Candidate: f[1]})
	}
	return list
}

// parseDnfSecurity lit « dnf -q --cacheonly updateinfo list --security » et rend les noms
// de paquets visés (RHSA-… Important/Sec. openssl-1:3.0.7-27.el9.x86_64).
func parseDnfSecurity(out string) map[string]bool {
	set := map[string]bool{}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 3 {
			continue
		}
		set[rpmName(f[len(f)-1])] = true
	}
	return set
}

// rpmName ôte version, publication et architecture d'un NEVRA (openssl-1:3.0.7-27.el9.x86_64).
func rpmName(nevra string) string {
	s := nevra
	if i := strings.LastIndex(s, "."); i > 0 {
		s = s[:i] // architecture
	}
	for range 2 { // publication, puis version
		if i := strings.LastIndex(s, "-"); i > 0 {
			s = s[:i]
		}
	}
	return s
}

func countSecurity(list []model.PendingUpdate) int {
	n := 0
	for _, u := range list {
		if u.Security {
			n++
		}
	}
	return n
}
