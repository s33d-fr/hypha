package metrics

import (
	"math"
	"testing"

	"github.com/shirou/gopsutil/v4/disk"
)

func TestExcludedMountOptique(t *testing.T) {
	s := &System{}
	for _, p := range []disk.PartitionStat{
		{Device: "D:", Mountpoint: "D:", Fstype: "UDF"},
		{Device: "E:", Mountpoint: "E:", Fstype: "CDFS"},
		{Device: "/dev/sr0", Mountpoint: "/media/cdrom", Fstype: "iso9660"},
	} {
		if !s.excludedMount(p) {
			t.Errorf("support optique compté comme disque : %+v", p)
		}
	}
	if s.excludedMount(disk.PartitionStat{Device: "C:", Mountpoint: "C:", Fstype: "NTFS"}) {
		t.Error("C: en NTFS exclu")
	}
}

// Profil relevé sur un serveur de base de données le 19/11 : 14 % d'inactivité, 69 % d'attente disque.
func TestCPUBusySansAttenteDisque(t *testing.T) {
	prev := cpuTimes{total: 1000}
	cur := cpuTimes{user: 5, system: 12, idle: 14, iowait: 69, total: 1100}
	proche := func(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
	c := cpuPercents(cur, prev, true)
	if !proche(c.usage, 86) || !proche(c.iowait, 69) || !proche(c.busy, 17) || !proche(c.idle, 14) {
		t.Fatalf("Linux : %+v", c)
	}
	// Windows : pas d'attente disque séparée, busy = usage.
	c = cpuPercents(cpuTimes{user: 30, system: 10, idle: 60, total: 1100}, prev, false)
	if !proche(c.usage, 40) || !proche(c.busy, 40) || c.iowait != 0 {
		t.Fatalf("Windows : %+v", c)
	}
	if c := cpuPercents(prev, prev, true); c != (cpuPct{}) {
		t.Fatalf("intervalle nul : %+v", c)
	}
}
