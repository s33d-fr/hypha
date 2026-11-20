package integrations

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// run2 fait deux relevés espacés de 31 s (les compteurs ne donnent un écart qu'au second).
func run2(t *testing.T, it integration) ([]model.Check, string, map[string]model.Series) {
	t.Helper()
	var checks []model.Check
	var state string
	var b *model.SeriesBuilder
	start := time.Now()
	for i := 0; i < 2; i++ {
		b = model.NewSeriesBuilder(0, testTags)
		checks, state = it.collect(context.Background(), start.Add(time.Duration(i)*31*time.Second), b, testTags)
	}
	return checks, state, index(b.Series())
}

func checkBy(checks []model.Check, name string) (model.Check, bool) {
	for _, c := range checks {
		if c.Name == name {
			return c, true
		}
	}
	return model.Check{}, false
}

// ── PHP-FPM : faux pool FastCGI ──────────────────────────────────────────────────────────

func fakeFPM(t *testing.T, status func(n int) string) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	n := 0
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			rd := bufio.NewReader(c)
			params := map[string]string{}
			for {
				var h [8]byte
				if _, err := io.ReadFull(rd, h[:]); err != nil {
					break
				}
				l := int(h[4])<<8 | int(h[5])
				buf := make([]byte, l+int(h[6]))
				io.ReadFull(rd, buf)
				buf = buf[:l]
				if h[1] == 4 {
					for len(buf) > 0 {
						rl := func() int {
							if buf[0]&0x80 == 0 {
								v := int(buf[0])
								buf = buf[1:]
								return v
							}
							v := int(binary.BigEndian.Uint32(buf) &^ (1 << 31))
							buf = buf[4:]
							return v
						}
						kl, vl := rl(), rl()
						params[string(buf[:kl])] = string(buf[kl : kl+vl])
						buf = buf[kl+vl:]
					}
				}
				if h[1] == 5 && l == 0 {
					break
				}
			}
			var out string
			switch params["SCRIPT_NAME"] {
			case "/status":
				n++
				out = "Content-type: application/json\r\n\r\n" + status(n)
			case "/ping":
				out = "Content-type: text/plain\r\n\r\npong"
			default:
				out = "Status: 404 Not Found\r\nContent-type: text/html\r\n\r\nFile not found.\n"
			}
			rec := func(typ byte, b []byte) {
				c.Write([]byte{1, typ, 0, 1, byte(len(b) >> 8), byte(len(b)), 0, 0})
				c.Write(b)
			}
			rec(6, []byte(out))
			rec(3, make([]byte, 8))
			c.Close()
		}
	}()
	return ln.Addr().String()
}

func TestPHPFPMFastCGI(t *testing.T) {
	addr := fakeFPM(t, func(n int) string {
		return fmt.Sprintf(`{"pool":"www","process manager":"dynamic","start since":%d,"accepted conn":%d,"listen queue":1,`+
			`"max listen queue":3,"listen queue len":511,"idle processes":3,"active processes":1,"total processes":4,`+
			`"max active processes":2,"max children reached":%d,"slow requests":0}`, 100*n, 50*n, n)
	})
	p := newPHPFPM(config.PHPFPMConfig{Enabled: "auto", Address: addr, StatusPath: "/status", PingPath: "/ping"})
	checks, state, got := run2(t, p)
	if state != "ok" {
		t.Fatalf("état %q, contrôles %+v", state, checks)
	}
	expectValue(t, got, "php_fpm.processes.active{pool=www}", 1)
	expectValue(t, got, "php_fpm.listen_queue.size{pool=www}", 1)
	expectValue(t, got, "php_fpm.processes.usage{pool=www}", 25)
	expectValue(t, got, "php_fpm.requests.accepted{pool=www}", 50)
	expectValue(t, got, "php_fpm.processes.max_reached{pool=www}", 1)
	if c, ok := checkBy(checks, "php_fpm.can_ping"); !ok || c.Status != model.CheckOK {
		t.Fatalf("ping : %+v", checks)
	}
	if c, ok := checkBy(checks, "php_fpm.can_connect"); !ok || !strings.Contains(c.Message, "pool www") {
		t.Fatalf("can_connect : %+v", checks)
	}
	// Page de statut non réglée : pool trouvé mais muet, état « inactif » en mode auto.
	p2 := newPHPFPM(config.PHPFPMConfig{Enabled: "auto", StatusPath: "/autre", PingPath: "/ping"})
	p2.cfg.Address = addr
	p2.det.mode = "auto"
	if c, st := p2.collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags); c != nil || !strings.HasPrefix(st, "inactif") {
		t.Fatalf("pool sans page de statut : %q %+v", st, c)
	}
}

// ── Squid ────────────────────────────────────────────────────────────────────────────────

func TestSquidCounters(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/squid-internal-mgr/counters":
			n++
			fmt.Fprintf(w, "sample_time = 1700000000.1 (Tue)\nclient_http.requests = %d\nclient_http.hits = %d\n"+
				"client_http.errors = 0\nclient_http.kbytes_in = %d\nclient_http.kbytes_out = 10\nserver.all.requests = 5\n", 100*n, 25*n, 3*n)
		case "/squid-internal-mgr/info":
			fmt.Fprint(w, "Squid Object Cache: Version 6.13\nFile descriptor usage for squid:\n\tMaximum number of file descriptors:   1024\n"+
				"\tNumber of file desc currently in use:   12\nConnection information for squid:\n\tNumber of clients accessing cache:\t2\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	s := newSquid(config.ServiceProbe{Enabled: "auto", URL: srv.URL})
	checks, _, got := run2(t, s)
	kv := "{instance=" + srv.URL + "}"
	expectValue(t, got, "squid.cachemgr.client_http.requests"+kv, 100)
	expectValue(t, got, "squid.cachemgr.client_http.hit_rate"+kv, 25)
	expectValue(t, got, "squid.fd.used"+kv, 12)
	expectValue(t, got, "squid.clients"+kv, 2)
	if len(checks) != 1 || checks[0].Message != "Squid 6.13 joignable" {
		t.Fatalf("contrôle : %+v", checks)
	}
}

// ── Lighttpd ─────────────────────────────────────────────────────────────────────────────

func TestLighttpdStatus(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		fmt.Fprintf(w, "Total Accesses: %d\nTotal kBytes: %d\nUptime: 60\nBusyServers: 2\nIdleServers: 0\nScoreboard: __\n", 31*n, 2*n)
	}))
	defer srv.Close()
	l := newLighttpd(config.ServiceProbe{Enabled: "auto", URL: srv.URL + "/server-status"})
	_, state, got := run2(t, l)
	kv := "{instance=" + srv.URL + "/server-status}"
	if state != "ok" {
		t.Fatal(state)
	}
	expectValue(t, got, "lighttpd.performance.busy_servers"+kv, 2)
	expectValue(t, got, "lighttpd.net.requests"+kv, 31)
	expectValue(t, got, "lighttpd.net.request_per_s"+kv, 1)
	if _, err := parseLighttpdStatus("<html>nope</html>"); err == nil {
		t.Fatal("page étrangère acceptée")
	}
	if st := newLighttpd(config.ServiceProbe{Enabled: "auto"}); func() string {
		_, s := st.collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags)
		return s
	}() != "absent" {
		t.Fatal("sans url, lighttpd doit rester absent")
	}
}

// ── Supervisor : faux serveur XML-RPC ────────────────────────────────────────────────────

func TestSupervisordXMLRPC(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		u, p, _ := r.BasicAuth()
		if u != "hypha" || p != "s3cret" {
			w.WriteHeader(401)
			return
		}
		switch {
		case strings.Contains(string(body), "getAllProcessInfo"):
			proc := func(name, state, desc string, start int) string {
				return `<value><struct><member><name>name</name><value><string>` + name + `</string></value></member>` +
					`<member><name>group</name><value><string>` + name + `</string></value></member>` +
					`<member><name>statename</name><value><string>` + state + `</string></value></member>` +
					`<member><name>description</name><value><string>` + desc + `</string></value></member>` +
					`<member><name>start</name><value><int>` + fmt.Sprint(start) + `</int></value></member>` +
					`<member><name>now</name><value><int>1000</int></value></member>` +
					`<member><name>spawnerr</name><value><string></string></value></member></struct></value>`
			}
			fmt.Fprint(w, `<?xml version='1.0'?><methodResponse><params><param><value><array><data>`+
				proc("web", "RUNNING", "pid 12, uptime 0:15:00", 100)+proc("worker", "FATAL", "Exited too quickly", 0)+
				proc("cron", "STOPPED", "Not started", 0)+`</data></array></value></param></params></methodResponse>`)
		case strings.Contains(string(body), "getSupervisorVersion"):
			fmt.Fprint(w, `<?xml version='1.0'?><methodResponse><params><param><value><string>4.2.5</string></value></param></params></methodResponse>`)
		default:
			fmt.Fprint(w, `<?xml version='1.0'?><methodResponse><fault><value><struct><member><name>faultCode</name><value><int>1</int></value></member>`+
				`<member><name>faultString</name><value><string>UNKNOWN_METHOD</string></value></member></struct></value></fault></methodResponse>`)
		}
	}))
	defer srv.Close()
	s := newSupervisord(config.SupervisordConfig{Enabled: "auto", URL: srv.URL + "/RPC2", Username: "hypha", Password: "s3cret"})
	checks, state, got := run2(t, s)
	if state != "ok" {
		t.Fatalf("état %q", state)
	}
	want := map[string]int{"web": model.CheckOK, "worker": model.CheckCrit, "cron": model.CheckWarn}
	for _, c := range checks {
		if c.Name == "supervisord.process.status" && want[c.Tags["supervisord_process"]] != c.Status {
			t.Fatalf("%s : statut %d (%s)", c.Tags["supervisord_process"], c.Status, c.Message)
		}
	}
	if c, ok := checkBy(checks, "supervisord.can_connect"); !ok || !strings.Contains(c.Message, "4.2.5") {
		t.Fatalf("can_connect : %+v", checks)
	}
	expectValue(t, got, "supervisord.process.uptime{supervisord_process=web}", 900)
	expectValue(t, got, "supervisord.process.count{status=fatal}", 1)
	if _, err := xmlrpcCall(context.Background(), newHTTPProbe(srv.URL, "hypha", "s3cret", false), "nope"); err == nil ||
		!strings.Contains(err.Error(), "UNKNOWN_METHOD") {
		t.Fatalf("faute XML-RPC : %v", err)
	}
}

// ── ClickHouse ───────────────────────────────────────────────────────────────────────────

func TestClickHouseHTTP(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-ClickHouse-User") != "lecture" || r.Header.Get("X-ClickHouse-Key") != "pw" {
			w.WriteHeader(516)
			fmt.Fprint(w, "Code: 516. DB::Exception: lecture: Authentication failed. (AUTHENTICATION_FAILED)")
			return
		}
		q, _ := io.ReadAll(r.Body)
		switch s := string(q); {
		case strings.Contains(s, "version()"):
			fmt.Fprint(w, "25.3.2.39\n")
		case strings.Contains(s, "system.metrics"):
			fmt.Fprint(w, "Query\t2\nTCPConnection\t5\nMemoryTracking\t123456\n")
		case strings.Contains(s, "system.events"):
			n++
			fmt.Fprintf(w, "Query\t%d\nFailedQuery\t%d\nQueryTimeMicroseconds\t%d\n", 10*n, n, 50000*n)
		case strings.Contains(s, "asynchronous_metrics"):
			fmt.Fprint(w, "Uptime\t3600\nMaxPartCountForPartition\t12\n")
		}
	}))
	defer srv.Close()
	c := newClickHouse(config.ServiceProbe{Enabled: "auto", URL: srv.URL, Username: "lecture", Password: "pw"})
	checks, state, got := run2(t, c)
	kv := "{instance=" + srv.URL + "}"
	if state != "ok" || checks[0].Message != "ClickHouse 25.3.2.39 joignable" {
		t.Fatalf("%q %+v", state, checks)
	}
	expectValue(t, got, "clickhouse.query.active"+kv, 2)
	expectValue(t, got, "clickhouse.query.count"+kv, 10)
	expectValue(t, got, "clickhouse.query.failed.count"+kv, 1)
	expectValue(t, got, "clickhouse.query.time.avg"+kv, 5)
	expectValue(t, got, "clickhouse.part.max_per_partition"+kv, 12)
	bad := newClickHouse(config.ServiceProbe{Enabled: "true", URL: srv.URL, Username: "autre"})
	if ch, _ := bad.collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags); len(ch) != 1 ||
		ch[0].Status != model.CheckCrit || !strings.Contains(ch[0].Message, "identifiants refusés") {
		t.Fatalf("mauvais compte : %+v", ch)
	}
}

// ── CouchDB ──────────────────────────────────────────────────────────────────────────────

func TestCouchDBStats(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, `{"couchdb":"Welcome","version":"3.4.2"}`)
		case "/_node/_local/_stats":
			n++
			fmt.Fprintf(w, `{"couchdb":{"httpd":{"requests":{"value":%d,"type":"counter","desc":"x"}},`+
				`"open_databases":{"value":7,"type":"counter","desc":"x"},`+
				`"request_time":{"value":{"min":0,"max":40,"arithmetic_mean":2.5,"percentile":[[50,1.0],[95,9.5],[99,30.0]]},"type":"histogram","desc":"x"},`+
				`"httpd_status_codes":{"200":{"value":%d,"type":"counter","desc":"x"},"201":{"value":0,"type":"counter","desc":"x"},"404":{"value":%d,"type":"counter","desc":"x"}}},`+
				`"couch_replicator":{"jobs":{"crashed":{"value":1,"type":"gauge","desc":"x"}}}}`, 100*n, 90*n, 10*n)
		case "/_node/_local/_system":
			fmt.Fprint(w, `{"uptime":600,"memory":{"processes_used":1000,"binary":500},"run_queue":0,"process_count":321}`)
		case "/_active_tasks":
			fmt.Fprint(w, `[{"type":"indexer"}]`)
		}
	}))
	defer srv.Close()
	c := newCouchDB(config.ServiceProbe{Enabled: "auto", URL: srv.URL, Username: "admin", Password: "pw"})
	checks, _, got := run2(t, c)
	kv := "instance=" + srv.URL
	expectValue(t, got, "couchdb.couchdb.httpd.requests{"+kv+"}", 100)
	expectValue(t, got, "couchdb.couchdb.open_databases{"+kv+"}", 7)
	expectValue(t, got, "couchdb.couchdb.request_time.p95{"+kv+"}", 9.5)
	expectValue(t, got, "couchdb.couchdb.httpd_status_codes{"+kv+",status_class=4xx}", 10)
	expectValue(t, got, "couchdb.couch_replicator.jobs.crashed{"+kv+"}", 1)
	expectValue(t, got, "couchdb.active_tasks{"+kv+",task_type=indexer}", 1)
	if checks[0].Message != "CouchDB 3.4.2 joignable" {
		t.Fatalf("%+v", checks)
	}
	if _, st := newCouchDB(config.ServiceProbe{Enabled: "auto", URL: srv.URL}).collect(context.Background(), time.Now(),
		model.NewSeriesBuilder(0, testTags), testTags); st != "absent" {
		t.Fatalf("sans compte : %q", st)
	}
}

// ── Couchbase ────────────────────────────────────────────────────────────────────────────

func TestCouchbaseREST(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/pools/default":
			fmt.Fprint(w, `{"clusterName":"essai","balanced":true,"storageTotals":{"ram":{"total":8000,"quotaTotal":1000,"quotaUsed":500,"used":4000,"usedByData":100},`+
				`"hdd":{"total":10000,"used":2500,"usedByData":200,"free":7500}},"nodes":[`+
				`{"hostname":"192.0.2.1:8091","clusterMembership":"active","status":"healthy","version":"7.6.2-3721-community","thisNode":true,"systemStats":{"cpu_utilization_rate":12.34},"interestingStats":{"ops":5}},`+
				`{"hostname":"192.0.2.2:8091","clusterMembership":"inactiveFailed","status":"unhealthy","version":"7.6.2"}]}`)
		case "/pools/default/buckets":
			fmt.Fprint(w, `[{"name":"seau","bucketType":"membase","basicStats":{"quotaPercentUsed":42.42,"opsPerSec":3,"itemCount":10,"diskUsed":99,"memUsed":77},"quota":{"ram":1000}}]`)
		}
	}))
	defer srv.Close()
	c := newCouchbase(config.ServiceProbe{Enabled: "auto", URL: srv.URL, Username: "ro", Password: "pw"})
	checks, _, got := run2(t, c)
	kv := "couchbase_cluster=essai,instance=" + srv.URL
	expectValue(t, got, "couchbase.ram.quota_usage{"+kv+"}", 50)
	expectValue(t, got, "couchbase.hdd.usage{"+kv+"}", 25)
	expectValue(t, got, "couchbase.by_bucket.quota_percent_used{bucket=seau,"+kv+"}", 42.4)
	expectValue(t, got, "couchbase.nodes.active{"+kv+"}", 1)
	bad := 0
	for _, c := range checks {
		if c.Name == "couchbase.node.health" && c.Status == model.CheckCrit {
			bad++
		}
	}
	if bad != 1 {
		t.Fatalf("un nœud en panne attendu : %+v", checks)
	}
}

// ── PowerDNS ─────────────────────────────────────────────────────────────────────────────

func TestPowerDNSRecursor(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "cle" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/api/v1/servers/localhost":
			fmt.Fprint(w, `{"daemon_type":"recursor","version":"5.2.0"}`)
		case "/api/v1/servers/localhost/statistics":
			n++
			fmt.Fprintf(w, `[{"name":"questions","type":"StatisticItem","value":"%d"},{"name":"cache-hits","type":"StatisticItem","value":"%d"},`+
				`{"name":"cache-misses","type":"StatisticItem","value":"%d"},{"name":"qa-latency","type":"StatisticItem","value":"1500"},`+
				`{"name":"security-status","type":"StatisticItem","value":"3"},{"name":"response-by-qtype","type":"MapStatisticItem","value":[]}]`, 100*n, 75*n, 25*n)
		}
	}))
	defer srv.Close()
	p := newPowerDNS(config.PowerDNSConfig{Enabled: "auto", URL: srv.URL, APIKey: "cle", ServerID: "localhost"}, true)
	checks, _, got := run2(t, p)
	kv := "{instance=" + srv.URL + "}"
	expectValue(t, got, "powerdns.recursor.questions"+kv, 100)
	expectValue(t, got, "powerdns.recursor.cache_hit_rate"+kv, 75)
	expectValue(t, got, "powerdns.recursor.qa_latency"+kv, 1500)
	if c, ok := checkBy(checks, "powerdns.recursor.security_status"); !ok || c.Status != model.CheckCrit {
		t.Fatalf("security-status 3 : %+v", checks)
	}
	// Le serveur faisant autorité visé à l'adresse d'un Recursor : erreur explicite.
	a := newPowerDNS(config.PowerDNSConfig{Enabled: "true", URL: srv.URL, APIKey: "cle", ServerID: "localhost"}, false)
	if ch, _ := a.collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags); len(ch) != 1 ||
		ch[0].Name != "powerdns.authoritative.can_connect" || !strings.Contains(ch[0].Message, "recursor") {
		t.Fatalf("mauvais démon : %+v", ch)
	}
}

// ── Keycloak ─────────────────────────────────────────────────────────────────────────────

func TestKeycloakHealthAndSessions(t *testing.T) {
	tokens := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health/ready":
			fmt.Fprint(w, `{"status":"UP","checks":[{"name":"Keycloak database connections health check","status":"UP"}]}`)
		case "/realms/master/protocol/openid-connect/token":
			r.ParseForm()
			if r.Form.Get("client_secret") != "s3cret" {
				w.WriteHeader(401)
				return
			}
			tokens++
			fmt.Fprint(w, `{"access_token":"jeton","expires_in":300}`)
		case "/admin/realms":
			fmt.Fprint(w, `[{"realm":"master","enabled":true},{"realm":"clients","enabled":true},{"realm":"vieux","enabled":false}]`)
		case "/admin/realms/master/client-session-stats":
			fmt.Fprint(w, `[{"clientId":"admin-cli","active":"2","offline":"0"}]`)
		case "/admin/realms/clients/client-session-stats":
			fmt.Fprint(w, `[{"clientId":"portail","active":"5","offline":"1"},{"clientId":"appli","active":"1","offline":"0"}]`)
		case "/admin/realms/clients/users/count":
			fmt.Fprint(w, `42`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	k := newKeycloak(config.KeycloakConfig{Enabled: "auto", URL: srv.URL, AdminURL: srv.URL, Realm: "master", ClientID: "hypha", ClientSecret: "s3cret"})
	checks, state, got := run2(t, k)
	kv := "instance=" + srv.URL
	if state != "ok" || tokens != 1 {
		t.Fatalf("état %q, %d jetons (un seul attendu, gardé entre deux passages)", state, tokens)
	}
	expectValue(t, got, "keycloak.sessions.active{"+kv+",realm=clients}", 6)
	expectValue(t, got, "keycloak.sessions.offline{"+kv+",realm=clients}", 1)
	expectValue(t, got, "keycloak.users{"+kv+",realm=clients}", 42)
	expectValue(t, got, "keycloak.health.ready{"+kv+"}", 1)
	if _, ok := got["keycloak.sessions.active{"+kv+",realm=vieux}"]; ok {
		t.Fatal("royaume désactivé lu")
	}
	if c, ok := checkBy(checks, "keycloak.admin_api"); !ok || c.Status != model.CheckOK {
		t.Fatalf("%+v", checks)
	}
}

// ── Nexus, Harbor, Fluentd, Wazuh ────────────────────────────────────────────────────────

func TestNexusREST(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/service/rest/v1/status":
		case "/service/rest/v1/status/writable":
			w.WriteHeader(503)
		case "/service/rest/v1/status/check":
			fmt.Fprint(w, `{"Blob Stores":{"healthy":true},"Default Admin Credentials":{"healthy":false,"message":"The default admin credentials have not been changed."}}`)
		case "/service/metrics/data":
			n++
			fmt.Fprintf(w, `{"gauges":{"jvm.memory.heap.used":{"value":500},"jvm.memory.heap.usage":{"value":0.25},"jvm.thread-states.count":{"value":80}},`+
				`"meters":{"org.eclipse.jetty.webapp.WebAppContext.5xx-responses":{"count":%d}},`+
				`"timers":{"org.eclipse.jetty.webapp.WebAppContext.requests":{"count":%d,"mean":0.012,"p95":0.05,"duration_units":"seconds"}}}`, n, 100*n)
		case "/service/rest/v1/blobstores":
			fmt.Fprint(w, `[{"name":"default","type":"File","blobCount":12,"totalSizeInBytes":1000,"availableSpaceInBytes":9000}]`)
		}
	}))
	defer srv.Close()
	x := newNexus(config.ServiceProbe{Enabled: "auto", URL: srv.URL, Username: "hypha", Password: "pw"})
	checks, _, got := run2(t, x)
	kv := "instance=" + srv.URL
	expectValue(t, got, "nexus.jvm.heap.usage{"+kv+"}", 25)
	expectValue(t, got, "nexus.http.requests{"+kv+"}", 100)
	expectValue(t, got, "nexus.http.responses{"+kv+",status_class=5xx}", 1)
	expectValue(t, got, "nexus.http.request.latency.avg{"+kv+"}", 12)
	expectValue(t, got, "nexus.blobstore.available{blobstore=default,blobstore_type=file,"+kv+"}", 9000)
	if c, _ := checkBy(checks, "nexus.writable"); c.Status != model.CheckCrit {
		t.Fatalf("lecture seule : %+v", checks)
	}
	if c, _ := checkBy(checks, "nexus.health"); c.Status != model.CheckWarn || !strings.Contains(c.Message, "Default Admin") {
		t.Fatalf("santé : %+v", checks)
	}
}

func TestHarborREST(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2.0/health":
			fmt.Fprint(w, `{"status":"unhealthy","components":[{"name":"core","status":"healthy"},{"name":"registry","status":"unhealthy","error":"dial tcp: refused"}]}`)
		case "/api/v2.0/systeminfo":
			fmt.Fprint(w, `{"harbor_version":"v2.12.2-73072d0d","read_only":false}`)
		case "/api/v2.0/statistics":
			fmt.Fprint(w, `{"total_project_count":3,"public_project_count":1,"total_repo_count":7,"total_storage_consumption":1234}`)
		case "/api/v2.0/systeminfo/volumes":
			fmt.Fprint(w, `{"storage":[{"total":1000,"free":250}]}`)
		}
	}))
	defer srv.Close()
	h := newHarbor(config.ServiceProbe{Enabled: "auto", URL: srv.URL, Username: "admin", Password: "pw"})
	checks, _, got := run2(t, h)
	kv := "instance=" + srv.URL
	expectValue(t, got, "harbor.disk.usage{"+kv+"}", 75)
	expectValue(t, got, "harbor.repos.count{"+kv+"}", 7)
	expectValue(t, got, "harbor.component.healthy{component=registry,"+kv+"}", 0)
	if c, _ := checkBy(checks, "harbor.status"); c.Status != model.CheckCrit || !strings.Contains(c.Message, "registry") {
		t.Fatalf("%+v", checks)
	}
	if c, _ := checkBy(checks, "harbor.can_connect"); c.Message != "Harbor v2.12.2 joignable" {
		t.Fatalf("%+v", checks)
	}
}

func TestFluentdMonitorAgent(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		fmt.Fprintf(w, `{"plugins":[{"plugin_id":"sortie","plugin_category":"output","type":"forward","output_plugin":true,`+
			`"retry_count":2,"buffer_queue_length":4,"buffer_total_queued_size":2048,"buffer_available_buffer_space_ratios":87.5,"emit_records":%d,"write_count":%d},`+
			`{"plugin_id":"entree","plugin_category":"input","type":"tail","output_plugin":false}]}`, 10*n, n)
	}))
	defer srv.Close()
	f := newFluentd(config.ServiceProbe{Enabled: "auto", URL: srv.URL})
	checks, _, got := run2(t, f)
	kv := "{instance=" + srv.URL + ",plugin_category=output,plugin_id=sortie,type=forward}"
	expectValue(t, got, "fluentd.buffer_queue_length"+kv, 4)
	expectValue(t, got, "fluentd.buffer_available_buffer_space_ratios"+kv, 87.5)
	expectValue(t, got, "fluentd.emit_records"+kv, 10)
	if !strings.Contains(checks[0].Message, "1 sortie(s) en nouvel essai") {
		t.Fatalf("%+v", checks)
	}
}

func TestWazuhAPI(t *testing.T) {
	logins := 0
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/security/user/authenticate" {
			if u, p, _ := r.BasicAuth(); u != "lecteur" || p != "pw" {
				w.WriteHeader(401)
				return
			}
			logins++
			fmt.Fprint(w, `{"data":{"token":"jwt"},"error":0}`)
			return
		}
		if r.Header.Get("Authorization") != "Bearer jwt" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, `{"data":{"title":"Wazuh API REST","api_version":"4.9.2"}}`)
		case "/manager/status":
			fmt.Fprint(w, `{"data":{"affected_items":[{"wazuh-analysisd":"running","wazuh-remoted":"stopped","wazuh-maild":"stopped","wazuh-db":"running"}]}}`)
		case "/agents/summary/status":
			fmt.Fprint(w, `{"data":{"connection":{"active":3,"disconnected":1,"never_connected":0,"pending":0,"total":4},"configuration":{"synced":4}}}`)
		case "/manager/stats/analysisd":
			n++
			fmt.Fprintf(w, `{"data":{"affected_items":[{"events_received":%d,"events_dropped":0,"alerts_written":%d,"event_queue_usage":0.5}]}}`, 1000*n, 7*n)
		case "/manager/stats/remoted":
			fmt.Fprint(w, `{"data":{"affected_items":[{"queue_size":10,"total_queue_size":100,"tcp_sessions":3,"recv_bytes":500}]}}`)
		}
	}))
	defer srv.Close()
	x := newWazuh(config.ServiceProbe{Enabled: "auto", URL: srv.URL, Username: "lecteur", Password: "pw"})
	checks, _, got := run2(t, x)
	kv := "instance=" + srv.URL
	if logins != 1 {
		t.Fatalf("%d connexions à l'API (jeton à garder)", logins)
	}
	expectValue(t, got, "wazuh.agents{"+kv+",status=disconnected}", 1)
	expectValue(t, got, "wazuh.analysisd.events.received{"+kv+"}", 1000)
	expectValue(t, got, "wazuh.analysisd.alerts_written{"+kv+"}", 7)
	expectValue(t, got, "wazuh.analysisd.event_queue.usage{"+kv+"}", 50)
	expectValue(t, got, "wazuh.remoted.queue.usage{"+kv+"}", 10)
	if c, _ := checkBy(checks, "wazuh.manager.daemons"); c.Status != model.CheckCrit || !strings.Contains(c.Message, "wazuh-remoted") ||
		strings.Contains(c.Message, "maild") {
		t.Fatalf("démons : %+v", checks)
	}
}
