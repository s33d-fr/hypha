package integrations

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── ZooKeeper ────────────────────────────────────────────────────────────────────────────

func TestZooKeeperMntr(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	n := 0
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			buf := make([]byte, 4)
			c.Read(buf)
			switch string(buf) {
			case "mntr":
				n++
				fmt.Fprintf(c, "zk_version\t3.9.3-c26634f34490bb0ea7a09cc51e05ede3b4e320ee, built on 2024-10-17\nzk_server_state\tstandalone\n"+
					"zk_avg_latency\t0.5\nzk_num_alive_connections\t3\nzk_znode_count\t42\nzk_packets_received\t%d\n", 100*n)
			case "ruok":
				fmt.Fprint(c, "imok")
			}
			c.Close()
		}
	}()
	z := newZooKeeper(config.AddressProbe{Enabled: "auto", Address: ln.Addr().String()})
	checks, _, got := run2(t, z)
	kv := "instance=" + ln.Addr().String()
	expectValue(t, got, "zookeeper.znode_count{"+kv+"}", 42)
	expectValue(t, got, "zookeeper.packets.received{"+kv+"}", 100)
	expectValue(t, got, "zookeeper.server_state{"+kv+",mode=standalone}", 1)
	if c, _ := checkBy(checks, "zookeeper.can_connect"); c.Message != "ZooKeeper 3.9.3 joignable (standalone)" {
		t.Fatalf("%+v", checks)
	}
	if c, ok := checkBy(checks, "zookeeper.ruok"); !ok || c.Status != model.CheckOK {
		t.Fatalf("%+v", checks)
	}
}

// ── Varnish et Ceph : commandes simulées par un script ───────────────────────────────────

func script(t *testing.T, body string) string {
	if runtime.GOOS == "windows" {
		t.Skip("script shell")
	}
	p := filepath.Join(t.TempDir(), "outil")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestVarnishstat(t *testing.T) {
	// Forme 7.x (« counters ») ; varnishadm absent du dossier : santé des serveurs non lue.
	dir := t.TempDir()
	cnt := filepath.Join(dir, "n")
	os.WriteFile(cnt, []byte("0"), 0o644)
	stat := filepath.Join(dir, "varnishstat")
	os.WriteFile(stat, []byte(`#!/bin/sh
n=$(cat `+cnt+`); n=$((n+1)); echo $n > `+cnt+`
cat <<EOF
{"version":1,"timestamp":"2026-11-20T04:00:00","counters":{
"MAIN.uptime":{"flag":"c","format":"d","value":120},
"MAIN.cache_hit":{"flag":"c","format":"i","value":$((90*n))},
"MAIN.cache_miss":{"flag":"c","format":"i","value":$((10*n))},
"MAIN.n_object":{"flag":"g","format":"i","value":33},
"SMA.s0.g_bytes":{"flag":"g","format":"B","value":2048},
"VBE.boot.default.req":{"flag":"c","format":"i","value":$((5*n))}}}
EOF
`), 0o755)
	os.WriteFile(filepath.Join(dir, "varnishadm"), []byte(`#!/bin/sh
echo '[2, ["backend.list", "-j"], 1700000000.0, {"boot.default": {"type": "backend", "admin_health": "probe", "probe_message": [3, 8, "sick"]}}]'
`), 0o755)
	v := newVarnish(config.CommandProbe{Enabled: "auto", Command: config.CommandLine{stat}, Timeout: config.Duration(5 * time.Second)})
	checks, state, got := run2(t, v)
	if state != "ok" {
		t.Fatalf("état %q", state)
	}
	expectValue(t, got, "varnish.cache_hit{}", 90)
	expectValue(t, got, "varnish.cache_hit_rate{}", 90)
	expectValue(t, got, "varnish.n_object{}", 33)
	expectValue(t, got, "varnish.storage.used{storage=s0}", 2048)
	expectValue(t, got, "varnish.backend.requests{backend=boot.default}", 5)
	if c, ok := checkBy(checks, "varnish.backend_healthy"); !ok || c.Status != model.CheckCrit || !strings.Contains(c.Message, "3/8") {
		t.Fatalf("serveur malade : %+v", checks)
	}
	// Forme 6.x et plus ancienne : compteurs au premier niveau.
	if c, err := parseVarnishstat([]byte(`{"timestamp":"x","MAIN.uptime":{"flag":"c","value":5}}`)); err != nil || c["MAIN.uptime"].Value != 5 {
		t.Fatalf("forme ancienne : %v %v", c, err)
	}
	if a := adminCommand([]string{"docker", "exec", "v", "varnishstat", "-n", "x"}); strings.Join(a, " ") != "docker exec v varnishadm -n x" {
		t.Fatalf("varnishadm : %v", a)
	}
}

func TestCephStatus(t *testing.T) {
	cmd := script(t, `case "$1" in
status) cat <<'EOF'
{"fsid":"aaaa-bbbb","health":{"status":"HEALTH_WARN","checks":{"OSD_DOWN":{"severity":"HEALTH_WARN","summary":{"message":"1 osds down"},"muted":false},
"POOL_NO_REDUNDANCY":{"severity":"HEALTH_WARN","summary":{"message":"1 pool(s) have no replicas"},"muted":true}}},
"monmap":{"num_mons":1},"quorum_names":["a"],"osdmap":{"num_osds":3,"num_up_osds":2,"num_in_osds":3},
"pgmap":{"pgs_by_state":[{"state_name":"active+clean","count":30},{"state_name":"active+undersized","count":10}],"num_pgs":40,"num_pools":2,
"num_objects":100,"bytes_used":250,"bytes_avail":750,"bytes_total":1000,"read_bytes_sec":12}}
EOF
;;
df) echo '{"stats":{"total_bytes":1000},"pools":[{"name":"rbd","stats":{"stored":10,"objects":5,"percent_used":0.123,"max_avail":500}}]}' ;;
esac
`)
	c := newCeph(config.CommandProbe{Enabled: "auto", Command: config.CommandLine{cmd}, Timeout: config.Duration(5 * time.Second)})
	checks, state, got := run2(t, c)
	if state != "ok" {
		t.Fatalf("état %q %+v", state, checks)
	}
	kv := "ceph_fsid=aaaa-bbbb"
	expectValue(t, got, "ceph.num_up_osds{"+kv+"}", 2)
	expectValue(t, got, "ceph.aggregate_pct_used{"+kv+"}", 25)
	expectValue(t, got, "ceph.pgs.active_clean_pct{"+kv+"}", 75)
	expectValue(t, got, "ceph.pool.pct_used{"+kv+",ceph_pool=rbd}", 12.3)
	st, _ := checkBy(checks, "ceph.overall_status")
	if st.Status != model.CheckWarn || !strings.Contains(st.Message, "1 osds down") || strings.Contains(st.Message, "replicas") {
		t.Fatalf("santé : %+v", st)
	}
	// Ancien format (Luminous) : osdmap imbriqué.
	if m := parseOSDMap([]byte(`{"osdmap":{"num_osds":4,"num_up_osds":4}}`)); m.NumOSDs != 4 {
		t.Fatalf("osdmap imbriqué : %+v", m)
	}
}

// ── OpenLDAP : faux slapd ────────────────────────────────────────────────────────────────

func fakeSlapd(t *testing.T, password string) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	entry := func(dn string, attrs map[string]string) []byte {
		var al [][]byte
		for k, v := range attrs {
			al = append(al, ber(0x30, berStr(0x04, k), ber(0x31, berStr(0x04, v))))
		}
		return ber(0x64, berStr(0x04, dn), ber(0x30, al...))
	}
	done := func(code int) []byte { return ber(0x65, berInt(0x0a, code), berStr(0x04, ""), berStr(0x04, "")) }
	tree := map[string][][]byte{
		"cn=Monitor":                   {entry("cn=Monitor", map[string]string{"monitoredInfo": "OpenLDAP: slapd 2.6.9 (Jan  1 2025)"})},
		"cn=Connections,cn=Monitor":    {entry("cn=Total,cn=Connections,cn=Monitor", map[string]string{"monitorCounter": "1000"}), entry("cn=Current,cn=Connections,cn=Monitor", map[string]string{"monitorCounter": "4"})},
		"cn=Operations,cn=Monitor":     {entry("cn=Bind,cn=Operations,cn=Monitor", map[string]string{"monitorOpInitiated": "10", "monitorOpCompleted": "9"})},
		"cn=Statistics,cn=Monitor":     {entry("cn=Bytes,cn=Statistics,cn=Monitor", map[string]string{"monitorCounter": "5000"})},
		"cn=Threads,cn=Monitor":        {entry("cn=Active,cn=Threads,cn=Monitor", map[string]string{"monitoredInfo": "2"})},
		"cn=Waiters,cn=Monitor":        {entry("cn=Read,cn=Waiters,cn=Monitor", map[string]string{"monitorCounter": "3"})},
		"cn=Uptime,cn=Time,cn=Monitor": {entry("cn=Uptime,cn=Time,cn=Monitor", map[string]string{"monitoredInfo": "3600"})},
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				rd := bufio.NewReader(c)
				for {
					msg, err := berRead(rd)
					if err != nil {
						return
					}
					parts, _ := berSplit(msg.data)
					id := berToInt(parts[0].data)
					op := parts[1]
					reply := func(op []byte) { c.Write(ber(0x30, berInt(0x02, id), op)) }
					switch op.tag {
					case 0x60:
						f, _ := berSplit(op.data)
						code := 0
						if string(f[2].data) != password {
							code = 49
						}
						reply(ber(0x61, berInt(0x0a, code), berStr(0x04, ""), berStr(0x04, "")))
					case 0x63:
						f, _ := berSplit(op.data)
						base := string(f[0].data)
						for _, e := range tree[base] {
							reply(e)
						}
						if _, ok := tree[base]; !ok {
							reply(done(32))
						} else {
							reply(done(0))
						}
					case 0x42:
						return
					}
				}
			}(c)
		}
	}()
	return ln.Addr().String()
}

func TestOpenLDAPMonitor(t *testing.T) {
	addr := fakeSlapd(t, "pw")
	o := newOpenLDAP(config.OpenLDAPConfig{Enabled: "auto", URL: "ldap://" + addr, BindDN: "cn=hypha,dc=exemple", Password: "pw"})
	checks, state, got := run2(t, o)
	if state != "ok" {
		t.Fatalf("état %q %+v", state, checks)
	}
	kv := "instance=ldap://" + addr
	expectValue(t, got, "openldap.connections.current{"+kv+"}", 4)
	expectValue(t, got, "openldap.threads{"+kv+",status=active}", 2)
	expectValue(t, got, "openldap.waiter.read{"+kv+"}", 3)
	expectValue(t, got, "openldap.uptime{"+kv+"}", 3600)
	if checks[0].Message != "slapd 2.6.9 joignable" {
		t.Fatalf("%+v", checks)
	}
	bad := newOpenLDAP(config.OpenLDAPConfig{Enabled: "true", URL: "ldap://" + addr, BindDN: "cn=x", Password: "faux"})
	if ch, _ := bad.collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags); len(ch) != 1 ||
		!strings.Contains(ch[0].Message, "code 49") {
		t.Fatalf("bind refusé : %+v", ch)
	}
	for _, n := range []int{0, 3, 127, 128, 255, 256, 70000} {
		if v := berToInt(berInt(0x02, n)[2:]); v != n {
			t.Fatalf("entier BER %d relu %d", n, v)
		}
	}
}

// ── Gunicorn : /proc simulé ──────────────────────────────────────────────────────────────

func fakeProc(t *testing.T, procs map[int][2]string) string {
	dir := t.TempDir()
	for pid, p := range procs {
		d := filepath.Join(dir, fmt.Sprint(pid))
		os.MkdirAll(d, 0o755)
		os.WriteFile(filepath.Join(d, "cmdline"), []byte(p[0]), 0o644)
		os.WriteFile(filepath.Join(d, "stat"), []byte(fmt.Sprintf("%d (python3) S %s", pid, p[1])), 0o644)
	}
	return dir
}

func TestGunicornProcesses(t *testing.T) {
	stat := func(ppid, ticks int) string { return fmt.Sprintf("%d 0 0 0 0 0 0 0 0 0 %d 0 0", ppid, ticks) }
	proc := fakeProc(t, map[int][2]string{
		10: {"gunicorn: master [boutique]\x00\x00", stat(1, 5)},
		11: {"gunicorn: worker [boutique]\x00", stat(10, 7)},
		12: {"gunicorn: worker [boutique]\x00", stat(10, 9)},
		20: {"/usr/bin/python3\x00/usr/local/bin/gunicorn\x00-w\x002\x00api.main:app\x00", stat(1, 1)},
		21: {"/usr/bin/python3\x00/usr/local/bin/gunicorn\x00-w\x002\x00api.main:app\x00", stat(20, 1)},
		30: {"tail\x00-f\x00/var/log/gunicorn\x00", stat(1, 1)},
	})
	t.Setenv("HOST_PROC", proc)
	g := newGunicorn(config.GunicornConfig{Enabled: "auto", Apps: []string{"boutique", "api.main:app", "absente"}})
	b := model.NewSeriesBuilder(0, testTags)
	g.collect(context.Background(), time.Now(), b, testTags)
	// Second passage : le worker 11 a travaillé.
	os.WriteFile(filepath.Join(proc, "11", "stat"), []byte("11 (python3) S "+stat(10, 20)), 0o644)
	b = model.NewSeriesBuilder(0, testTags)
	checks, state := g.collect(context.Background(), time.Now().Add(31*time.Second), b, testTags)
	got := index(b.Series())
	expectValue(t, got, "gunicorn.worker_processes{app=boutique,state=working}", 1)
	expectValue(t, got, "gunicorn.worker_processes{app=boutique,state=idle}", 1)
	expectValue(t, got, "gunicorn.worker_processes{app=api.main:app,state=idle}", 1)
	if _, ok := got["gunicorn.worker_processes{app=gunicorn,state=idle}"]; ok {
		t.Fatal("« tail -f /var/log/gunicorn » pris pour Gunicorn")
	}
	if c, _ := checkBy(checks, "gunicorn.is_running"); c.Status == model.CheckOK && c.Tags["app"] == "absente" {
		t.Fatal("application absente non signalée")
	}
	crit := 0
	for _, c := range checks {
		if c.Status == model.CheckCrit {
			crit++
		}
	}
	if crit != 1 || !strings.HasPrefix(state, "partiel") {
		t.Fatalf("une application absente attendue : %q %+v", state, checks)
	}
	for args, want := range map[string]string{
		"gunicorn\x00-b\x000.0.0.0:8000\x00monapp.wsgi": "monapp.wsgi",
		"python3\x00-m\x00gunicorn\x00--name\x00site":   "site",
		"python3\x00/opt/x/bin/gunicorn\x00--name=ops":  "ops",
	} {
		if got, ok := gunicornArgs(strings.Split(args, "\x00")); !ok || got != want {
			t.Fatalf("%q : %q", args, got)
		}
	}
}

// ── Btrfs : /sys et /proc simulés ────────────────────────────────────────────────────────

func TestBtrfsSysfs(t *testing.T) {
	sys, proc := t.TempDir(), t.TempDir()
	uuid := "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0"
	fs := filepath.Join(sys, "fs", "btrfs", uuid)
	w := func(p, v string) {
		os.MkdirAll(filepath.Dir(filepath.Join(fs, p)), 0o755)
		os.WriteFile(filepath.Join(fs, p), []byte(v+"\n"), 0o644)
	}
	w("label", "donnees")
	w("allocation/data/total_bytes", "1000")
	w("allocation/data/bytes_used", "900")
	w("allocation/data/disk_total", "2000")
	os.MkdirAll(filepath.Join(fs, "allocation", "data", "raid1"), 0o755)
	w("allocation/metadata/total_bytes", "100")
	w("allocation/metadata/bytes_used", "50")
	w("allocation/metadata/disk_total", "200")
	w("devices/loop0/size", "10") // 10 secteurs de 512 octets
	w("devices/loop1/size", "10")
	w("devinfo/1/error_stats", "write_errs 0\nread_errs 2\nflush_errs 0\ncorruption_errs 0\ngeneration_errs 0")
	w("devinfo/1/missing", "0")
	w("devinfo/2/error_stats", "write_errs 0\nread_errs 0\nflush_errs 0\ncorruption_errs 0\ngeneration_errs 0")
	w("devinfo/2/missing", "0")
	os.MkdirAll(filepath.Join(sys, "fs", "btrfs", "features"), 0o755)
	os.MkdirAll(filepath.Join(proc, "1"), 0o755)
	os.WriteFile(filepath.Join(proc, "1", "mountinfo"), []byte(
		"30 1 7:0 / /srv/donnees rw,relatime shared:1 - btrfs /dev/loop0 rw,space_cache=v2\n"+
			"31 1 7:0 /@sauv /srv/donnees/sauv rw shared:2 - btrfs /dev/loop0 rw\n"+
			"32 1 8:1 / / rw - ext4 /dev/sda1 rw\n"), 0o644)
	t.Setenv("HOST_SYS", sys)
	t.Setenv("HOST_PROC", proc)
	x := newBtrfs(config.IntegrationToggle{Enabled: "auto"})
	b := model.NewSeriesBuilder(0, testTags)
	checks, state := x.collect(context.Background(), time.Now(), b, testTags)
	got := index(b.Series())
	kv := "btrfs_label=donnees,btrfs_uuid=" + uuid + ",mount=/srv/donnees"
	if state != "ok" {
		t.Fatal(state)
	}
	expectValue(t, got, "btrfs.usage{"+kv+",replication_type=raid1,usage_type=data}", 90)
	expectValue(t, got, "btrfs.size{"+kv+"}", 10240)
	expectValue(t, got, "btrfs.unallocated{"+kv+"}", 10240-2200)
	expectValue(t, got, "btrfs.device.errors{btrfs_label=donnees,btrfs_uuid="+uuid+",device=1,error_type=read,mount=/srv/donnees}", 2)
	if len(checks) != 1 || checks[0].Status != model.CheckWarn || !strings.Contains(checks[0].Message, "disque 1 : 2") {
		t.Fatalf("%+v", checks)
	}
	w("devinfo/2/missing", "1")
	checks, _ = x.collect(context.Background(), time.Now().Add(time.Minute), model.NewSeriesBuilder(0, testTags), testTags)
	if checks[0].Status != model.CheckCrit {
		t.Fatalf("disque manquant : %+v", checks)
	}
}

// ── NFS : /proc simulé ───────────────────────────────────────────────────────────────────

func TestNFSClientAndServer(t *testing.T) {
	proc := t.TempDir()
	mnt := t.TempDir() // statfs réel sur un dossier local
	stats := func(reads, rtt int) string {
		return "device sysfs mounted on /sys with fstype sysfs\n" +
			"device nfsd mounted on /proc/fs/nfsd with fstype nfsd\n" +
			"device 192.0.2.5:/export mounted on " + mnt + " with fstype nfs4 statvers=1.1\n" +
			"\topts:\trw,vers=4.2\n\tper-op statistics\n" +
			fmt.Sprintf("\t        READ: %d %d 0 1000 %d 0 %d %d 0\n", reads, reads+2, 4096*reads, rtt, rtt+reads) +
			"\t       WRITE: 5 5 0 2000 100 0 10 12 0\n\t     GETATTR: 50 50 0 0 0 0 5 6 0\n"
	}
	os.MkdirAll(filepath.Join(proc, "1"), 0o755)
	os.MkdirAll(filepath.Join(proc, "net", "rpc"), 0o755)
	os.MkdirAll(filepath.Join(proc, "fs", "nfs"), 0o755)
	os.WriteFile(filepath.Join(proc, "1", "mountstats"), []byte(stats(10, 20)), 0o644)
	nfsd := func(calls int) string {
		return fmt.Sprintf("rc 0 30 400\nfh 0 0 0 0 0\nio %d 200\nth 8 0 0.000 0.000\nnet 500 0 500 12\nrpc %d 1 0 0 0\n"+
			"proc3 22 0 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21\nproc4ops 3 0 %d 5\n", 100*calls, calls, calls)
	}
	os.WriteFile(filepath.Join(proc, "net", "rpc", "nfsd"), []byte(nfsd(10)), 0o644)
	os.WriteFile(filepath.Join(proc, "fs", "nfs", "exports"), []byte("# Version 1.2\n/srv/export\t*(rw)\n"), 0o644)
	t.Setenv("HOST_PROC", proc)
	t.Setenv("HOST_ROOT", "")
	n := newNFS(config.NFSConfig{Enabled: "auto", Timeout: config.Duration(2 * time.Second)})
	n.collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags)
	os.WriteFile(filepath.Join(proc, "1", "mountstats"), []byte(stats(20, 70)), 0o644)
	os.WriteFile(filepath.Join(proc, "net", "rpc", "nfsd"), []byte(nfsd(30)), 0o644)
	b := model.NewSeriesBuilder(0, testTags)
	checks, state := n.collect(context.Background(), time.Now().Add(31*time.Second), b, testTags)
	got := index(b.Series())
	kv := "mount=" + mnt + ",nfs_server=192.0.2.5:/export,nfs_version=nfs4"
	if state != "ok" {
		t.Fatal(state)
	}
	expectValue(t, got, "nfs.client.read.ops{"+kv+"}", 10)
	expectValue(t, got, "nfs.client.read.bytes{"+kv+"}", 40960)
	expectValue(t, got, "nfs.client.read.rtt{"+kv+"}", 5)
	expectValue(t, got, "nfs.server.threads{}", 8)
	expectValue(t, got, "nfs.server.rpc.calls{}", 20)
	expectValue(t, got, "nfs.server.ops{nfs_version=4}", 20)
	expectValue(t, got, "nfs.server.exports{}", 1)
	mounts := 0
	for _, c := range checks {
		if c.Name == "nfs.mount" {
			mounts++
		}
	}
	if mounts != 1 {
		t.Fatalf("le système de fichiers nfsd n'est pas un montage NFS : %+v", checks)
	}
	if c, ok := checkBy(checks, "nfs.mount"); !ok || c.Status != model.CheckOK {
		t.Fatalf("%+v", checks)
	}
	if c, ok := checkBy(checks, "nfs.server"); !ok || c.Status != model.CheckOK || !strings.Contains(c.Message, "8 fils") {
		t.Fatalf("%+v", checks)
	}
}

// ── SSH ──────────────────────────────────────────────────────────────────────────────────

func TestSSHBanner(t *testing.T) {
	listen := func(banner string) string {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { ln.Close() })
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				fmt.Fprint(c, banner)
				buf := make([]byte, 64)
				c.Read(buf)
				c.Close()
			}
		}()
		return ln.Addr().String()
	}
	ok := listen("SSH-2.0-OpenSSH_9.6p1 Ubuntu-3ubuntu13.5\r\n")
	old := listen("SSH-1.5-Ancien\r\n")
	http := listen("HTTP/1.1 400 Bad Request\r\n\r\n")
	dead, _ := net.Listen("tcp", "127.0.0.1:0")
	deadAddr := dead.Addr().String()
	dead.Close()
	tgt := func(name, addr, expect string) config.SSHTarget {
		return config.SSHTarget{Name: name, Address: addr, ExpectBanner: expect, Timeout: config.Duration(2 * time.Second)}
	}
	s := newSSHProbe(config.SSHConfig{Enabled: "auto", Targets: []config.SSHTarget{
		tgt("ok", ok, "^SSH-2.0-OpenSSH_9"), tgt("attendu", ok, "OpenSSH_10"), tgt("ancien", old, ""),
		tgt("web", http, ""), tgt("mort", deadAddr, ""),
	}})
	b := model.NewSeriesBuilder(0, testTags)
	checks, state := s.collect(context.Background(), time.Now(), b, testTags)
	want := map[string]int{"ok": model.CheckOK, "attendu": model.CheckWarn, "ancien": model.CheckWarn, "web": model.CheckCrit, "mort": model.CheckCrit}
	for _, c := range checks {
		if c.Status != want[c.Tags["target"]] {
			t.Fatalf("%s : %d (%s)", c.Tags["target"], c.Status, c.Message)
		}
	}
	if !strings.HasPrefix(state, "partiel : 2") {
		t.Fatal(state)
	}
	if c, _ := checkBy(checks, "ssh.can_connect"); !strings.HasPrefix(c.Message, "OpenSSH_9.6p1 Ubuntu-3ubuntu13.5 en ") {
		t.Fatalf("%+v", c)
	}
}

// ── Suricata : eve.json ──────────────────────────────────────────────────────────────────

func TestSuricataEve(t *testing.T) {
	dir := t.TempDir()
	eve := filepath.Join(dir, "eve.json")
	ts := func(d time.Duration) string { return time.Now().Add(d).Format("2006-01-02T15:04:05.000000-0700") }
	stats := func(up, pkts, drops, alerts int, at string) string {
		return fmt.Sprintf(`{"timestamp":"%s","event_type":"stats","stats":{"uptime":%d,"capture":{"kernel_packets":%d,"kernel_drops":%d},`+
			`"decoder":{"pkts":%d,"bytes":1000},"flow":{"memuse":4096},"detect":{"alert":%d},"app_layer":{"flow":{"http":%d,"tls":2}}}}`+"\n",
			at, up, pkts, drops, pkts, alerts, alerts)
	}
	alert := `{"timestamp":"x","event_type":"alert","alert":{"severity":1,"action":"allowed","signature":"ET SCAN"}}` + "\n"
	os.WriteFile(eve, []byte(alert+stats(10, 1000, 0, 1, ts(-time.Minute))), 0o644)
	s := newSuricata(config.SuricataConfig{Enabled: "auto", EveFile: eve, StaleAfter: config.Duration(5 * time.Minute)})
	b := model.NewSeriesBuilder(0, testTags)
	checks, _ := s.collect(context.Background(), time.Now(), b, testTags)
	if _, ok := index(b.Series())["suricata.alerts{action=allowed,eve_file="+eve+",severity=1}"]; ok {
		t.Fatal("alerte d'avant le premier passage comptée")
	}
	f, _ := os.OpenFile(eve, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(alert + alert + stats(40, 2000, 100, 3, ts(0)) + `{"event_type":"stats","stats":{"uptim`) // dernière ligne en cours
	f.Close()
	b = model.NewSeriesBuilder(0, testTags)
	checks, _ = s.collect(context.Background(), time.Now().Add(31*time.Second), b, testTags)
	got := index(b.Series())
	kv := "eve_file=" + eve
	expectValue(t, got, "suricata.capture.kernel_packets{"+kv+"}", 1000)
	expectValue(t, got, "suricata.capture.drop_rate{"+kv+"}", 9.1)
	expectValue(t, got, "suricata.alerts{action=allowed,"+kv+",severity=1}", 2)
	expectValue(t, got, "suricata.app_layer.flow{app_proto=http,"+kv+"}", 2)
	expectValue(t, got, "suricata.uptime{"+kv+"}", 40)
	if len(checks) != 1 || checks[0].Status != model.CheckOK {
		t.Fatalf("%+v", checks)
	}
	// Plus rien d'écrit depuis 10 minutes : Suricata arrêté.
	checks, _ = s.collect(context.Background(), time.Now().Add(10*time.Minute), model.NewSeriesBuilder(0, testTags), testTags)
	if checks[0].Status != model.CheckCrit {
		t.Fatalf("relevé périmé : %+v", checks)
	}
	// Rotation : nouveau fichier, relu depuis le début.
	os.Rename(eve, eve+".1")
	os.WriteFile(eve, []byte(stats(5, 10, 0, 0, ts(10*time.Minute))), 0o644)
	checks, _ = s.collect(context.Background(), time.Now().Add(11*time.Minute), model.NewSeriesBuilder(0, testTags), testTags)
	if checks[0].Status != model.CheckOK {
		t.Fatalf("après rotation : %+v", checks)
	}
}
