package integrations

import (
	"strings"
	"testing"
	"time"

	"s33d.fr/hypha/agent/internal/model"
)

// En-tête réel de « show stat » (HAProxy 2.x et 3.x), tronqué après hrsp_other : les
// colonnes sont retrouvées par leur nom.
const hapHeader = "# pxname,svname,qcur,qmax,scur,smax,slim,stot,bin,bout,dreq,dresp,ereq,econ,eresp,wretr,wredis,status,weight,act,bck,chkfail,chkdown,lastchg,downtime,qlimit,pid,iid,sid,throttle,lbtot,tracked,type,rate,rate_lim,rate_max,check_status,check_code,check_duration,hrsp_1xx,hrsp_2xx,hrsp_3xx,hrsp_4xx,hrsp_5xx,hrsp_other,"

// csvLine construit une ligne dans l'ordre de l'en-tête à partir des colonnes données.
func csvLine(values map[string]string) string {
	cols := strings.Split(strings.TrimPrefix(hapHeader, "# "), ",")
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = values[c]
	}
	return strings.Join(out, ",")
}

func hapStat(bin, h2xx, ereq, econ string) []byte {
	lines := []string{
		hapHeader,
		csvLine(map[string]string{"pxname": "http_front", "svname": "FRONTEND", "scur": "5", "bin": bin, "bout": "5000", "ereq": ereq, "status": "OPEN", "type": "0", "hrsp_2xx": h2xx, "hrsp_3xx": "5", "hrsp_4xx": "3", "hrsp_5xx": "2"}),
		csvLine(map[string]string{"pxname": "http_front", "svname": "sock-1", "scur": "0", "bin": "0", "bout": "0", "status": "OPEN", "type": "3"}),
		csvLine(map[string]string{"pxname": "bk_web", "svname": "web1", "qcur": "0", "scur": "2", "bin": "500", "bout": "2500", "econ": "0", "eresp": "0", "status": "UP", "type": "2"}),
		csvLine(map[string]string{"pxname": "bk_web", "svname": "web2", "qcur": "0", "scur": "0", "bin": "500", "bout": "2500", "econ": "0", "eresp": "0", "status": "DOWN", "type": "2"}),
		csvLine(map[string]string{"pxname": "bk_web", "svname": "BACKEND", "qcur": "1", "scur": "2", "bin": bin, "bout": "5000", "econ": econ, "eresp": "1", "status": "UP", "type": "1", "hrsp_2xx": h2xx}),
		csvLine(map[string]string{"pxname": "bk_db", "svname": "db1", "qcur": "0", "scur": "0", "bin": "0", "bout": "0", "status": "MAINT", "type": "2"}),
		csvLine(map[string]string{"pxname": "bk_db", "svname": "db2", "qcur": "0", "scur": "0", "bin": "0", "bout": "0", "status": "DOWN 1/2", "type": "2"}),
		csvLine(map[string]string{"pxname": "bk_db", "svname": "BACKEND", "qcur": "0", "scur": "0", "bin": "0", "bout": "0", "econ": "0", "eresp": "0", "status": "DOWN", "type": "1"}),
		csvLine(map[string]string{"pxname": "tcp_front", "svname": "FRONTEND", "scur": "1", "bin": "300", "bout": "400", "ereq": "0", "status": "OPEN", "type": "0"}),
	}
	return []byte(strings.Join(lines, "\n") + "\n\n")
}

func TestHAProxyParseAndSeries(t *testing.T) {
	first, err := parseHAProxyStat(hapStat("1000", "90", "2", "1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 8 { // l'écouteur (type 3) est écarté
		t.Fatalf("%d lignes lues, attendu 8", len(first))
	}
	prev := map[string]hapRow{}
	for _, r := range first {
		prev[r.key()] = r
	}
	second, err := parseHAProxyStat(hapStat("2000", "190", "3", "2"))
	if err != nil {
		t.Fatal(err)
	}
	b := model.NewSeriesBuilder(1000, testTags)
	checks := hapSeries(b, time.Now(), second, prev, 10, testTags)
	got := index(b.Series())

	expectValue(t, got, "haproxy.frontend.sessions.current{frontend=http_front}", 5)
	expectValue(t, got, "haproxy.frontend.bytes.in{frontend=http_front}", 100) // 1 000 o en 10 s
	expectValue(t, got, "haproxy.frontend.bytes.out{frontend=http_front}", 0)
	expectValue(t, got, "haproxy.frontend.response.2xx{frontend=http_front}", 10) // 100 réponses en 10 s
	expectValue(t, got, "haproxy.frontend.errors{frontend=http_front}", 0.1)
	expectAbsent(t, got, "haproxy.frontend.response.2xx{frontend=tcp_front}") // mode tcp : pas de codes HTTP
	expectAbsent(t, got, "haproxy.frontend.queue.current{frontend=http_front}")
	expectValue(t, got, "haproxy.backend.queue.current{backend=bk_web}", 1)
	expectValue(t, got, "haproxy.backend.errors{backend=bk_web}", 0.1) // econ+eresp : 2 → 3
	expectValue(t, got, "haproxy.server.up{backend=bk_web,server=web1}", 1)
	expectValue(t, got, "haproxy.server.up{backend=bk_web,server=web2}", 0)
	expectValue(t, got, "haproxy.server.up{backend=bk_db,server=db1}", 0)

	byBackend := map[string]model.Check{}
	for _, c := range checks {
		byBackend[c.Tags["backend"]] = c
	}
	if c := byBackend["bk_web"]; c.Name != "haproxy.backend_up" || c.Status != model.CheckOK || c.Message != "1/2 serveurs en service" {
		t.Errorf("bk_web : %+v", c)
	}
	if c := byBackend["bk_db"]; c.Status != model.CheckCrit || c.Message != "0/2 serveurs en service" {
		t.Errorf("bk_db : %+v", c)
	}

	// Rechargement d'HAProxy : compteurs repartis de zéro, aucun débit négatif émis.
	third, _ := parseHAProxyStat(hapStat("10", "1", "0", "0"))
	prev = map[string]hapRow{}
	for _, r := range second {
		prev[r.key()] = r
	}
	b = model.NewSeriesBuilder(1000, testTags)
	hapSeries(b, time.Now(), third, prev, 10, testTags)
	got = index(b.Series())
	expectAbsent(t, got, "haproxy.frontend.bytes.in{frontend=http_front}")
	expectAbsent(t, got, "haproxy.frontend.response.2xx{frontend=http_front}")

	if _, err := parseHAProxyStat([]byte("Unknown command\n")); err == nil {
		t.Error("une réponse qui n'est pas du CSV de stats doit être refusée")
	}
}

func TestServerUp(t *testing.T) {
	for st, want := range map[string]bool{"UP": true, "UP 1/3": true, "no check": true, "DRAIN": true, "NOLB": true,
		"DOWN": false, "DOWN 1/2": false, "MAINT": false, "MAINT(via)": false} {
		if serverUp(st) != want {
			t.Errorf("serverUp(%q) = %v", st, !want)
		}
	}
}
