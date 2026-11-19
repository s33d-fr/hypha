// Package hostfs rend les chemins des fichiers de l'hôte. Dans l'image Docker de l'agent,
// ceux de l'hôte sont montés sous /host et désignés par HOST_PROC, HOST_SYS, HOST_ETC et
// HOST_ROOT, les mêmes variables que lit gopsutil ; ailleurs, ce sont les chemins habituels.
package hostfs

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func base(env, def string) string {
	if v := os.Getenv(env); v != "" {
		return v
	}
	return def
}

// Proc rend un chemin sous /proc de l'hôte (HOST_PROC).
func Proc(parts ...string) string {
	return filepath.Join(append([]string{base("HOST_PROC", "/proc")}, parts...)...)
}

// Sys rend un chemin sous /sys de l'hôte (HOST_SYS).
func Sys(parts ...string) string {
	return filepath.Join(append([]string{base("HOST_SYS", "/sys")}, parts...)...)
}

// Etc rend un chemin sous /etc de l'hôte (HOST_ETC).
func Etc(parts ...string) string {
	return filepath.Join(append([]string{base("HOST_ETC", "/etc")}, parts...)...)
}

// Root préfixe un chemin absolu de l'hôte (un point de montage lu dans /proc/1/mountinfo)
// par HOST_ROOT ; sans la variable, le chemin est rendu tel quel (y compris « C: » sous
// Windows). statfs sur le chemin brut mesurerait, dans un conteneur, le disque du
// conteneur et non celui de l'hôte.
func Root(path string) string {
	r := os.Getenv("HOST_ROOT")
	if r == "" {
		return path
	}
	return filepath.Join(r, path)
}

// Users lit le /etc/passwd de l'hôte : uid → nom. os/user lirait celui du conteneur.
func Users() map[string]string {
	out := map[string]string{}
	f, err := os.Open(Etc("passwd"))
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		// nom:mot_de_passe:uid:gid:…
		fields := strings.SplitN(sc.Text(), ":", 4)
		if len(fields) >= 3 && fields[0] != "" && !strings.HasPrefix(fields[0], "#") {
			if _, seen := out[fields[2]]; !seen {
				out[fields[2]] = fields[0]
			}
		}
	}
	return out
}

// LookupUser rend l'uid et le gid d'un compte du /etc/passwd de l'hôte.
func LookupUser(name string) (uid, gid int, ok bool) {
	f, err := os.Open(Etc("passwd"))
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Split(sc.Text(), ":")
		if len(fields) < 4 || fields[0] != name {
			continue
		}
		u, err1 := strconv.Atoi(fields[2])
		g, err2 := strconv.Atoi(fields[3])
		if err1 != nil || err2 != nil {
			return 0, 0, false
		}
		return u, g, true
	}
	return 0, 0, false
}
