package integrations

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// Faux serveur OVH : vérifie la signature de chaque appel, ne répond qu'aux GET attendus.
type fakeOVH struct {
	mu        sync.Mutex
	paths     []string
	methods   map[string]int
	routes    map[string]string
	badSig    int
	refuseAll bool
}

func (f *fakeOVH) handler(t *testing.T, secret, consumer string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.methods[r.Method]++
		p := strings.TrimPrefix(r.URL.EscapedPath(), "/1.0")
		if p == "/auth/time" {
			fmt.Fprint(w, time.Now().Unix()+42)
			return
		}
		f.paths = append(f.paths, p)
		ts := r.Header.Get("X-Ovh-Timestamp")
		var tsv int64
		fmt.Sscan(ts, &tsv)
		want := ovhSignature(secret, consumer, r.Method, "http://"+r.Host+r.URL.RequestURI(), "", tsv)
		if r.Header.Get("X-Ovh-Signature") != want || r.Header.Get("X-Ovh-Application") != "AK" {
			f.badSig++
			w.WriteHeader(400)
			fmt.Fprint(w, `{"message":"Invalid signature"}`)
			return
		}
		if tsv < time.Now().Unix()+30 {
			t.Errorf("horodatage non recalé sur l'heure d'OVH : %d", tsv)
		}
		if f.refuseAll {
			w.WriteHeader(403)
			fmt.Fprint(w, `{"message":"This call has not been granted"}`)
			return
		}
		body, ok := f.routes[p]
		if !ok {
			w.WriteHeader(404)
			fmt.Fprint(w, `{"message":"not found"}`)
			return
		}
		fmt.Fprint(w, body)
	}
}

func ovhRoutes(now time.Time) map[string]string {
	in := func(d int) string { return now.AddDate(0, 0, d).Format("2006-01-02") }
	return map[string]string{
		"/dedicated/server":                            `["ns1.example"]`,
		"/dedicated/server/ns1.example":                `{"name":"ns1.example","state":"ok","powerState":"poweron","datacenter":"gra1","ip":"192.0.2.10","reverse":"mx.example.org.","commercialRange":"KS-13","monitoring":true,"iam":{"displayName":"mx.example.org"}}`,
		"/dedicated/server/ns1.example/intervention":   `[1,2]`,
		"/dedicated/server/ns1.example/task":           `[]`,
		"/dedicated/server/ns1.example/serviceInfos":   `{"expiration":"` + in(5) + `","status":"ok","renew":{"automatic":false}}`,
		"/ip":                                          `["192.0.2.10/32","2001:db8::/64"]`,
		"/ip/192.0.2.10%2F32":                          `{"routedTo":{"serviceName":"ns1.example"}}`,
		"/ip/192.0.2.10%2F32/spam":                     `["192.0.2.10"]`,
		"/ip/192.0.2.10%2F32/spam/192.0.2.10":          `{"state":"blockedForSpam","time":1795000000}`,
		"/ip/192.0.2.10%2F32/antihack":                 `[]`,
		"/ip/192.0.2.10%2F32/mitigation":               `["192.0.2.10"]`,
		"/ip/192.0.2.10%2F32/mitigation/192.0.2.10":    `{"auto":true,"permanent":false,"state":"ok"}`,
		"/domain":                                      `["example.org"]`,
		"/domain/example.org/serviceInfos":             `{"expiration":"` + in(200) + `","status":"ok","renew":{"automatic":true}}`,
		"/dedicated/server/ns1.example/intervention/3": `{"interventionId":3,"type":"hardware : disque remplacé","date":"2026-11-18T10:00:00+01:00"}`,
	}
}

const testAgentID = "6f1c2b8e-0000-4000-8000-000000000001"

// sent : envois de l'agent rangés par route (événements, fiches, mesures et contrôles des ressources).
type sent map[string][]map[string]any

func newTestOVH(t *testing.T, f *fakeOVH) (*OVHCloud, *[]map[string]any) {
	srv := httptest.NewServer(f.handler(t, "AS", "CK"))
	t.Cleanup(srv.Close)
	var posted []map[string]any
	o := newOVHCloud(config.OVHCloudConfig{Enabled: "auto", Endpoint: srv.URL + "/1.0", ApplicationKey: "AK",
		ApplicationSecret: "AS", ConsumerKey: "CK", Interval: config.Duration(15 * time.Minute)},
		Deps{Post: func(route string, fields map[string]any) {
			if route == routeEvents {
				posted = append(posted, fields)
			}
		}})
	o.pace = 0
	o.tags = testTags
	o.once.Do(func() {}) // pas de tâche de fond : les passages sont lancés par le test
	return o, &posted
}

func byName(checks []model.Check) map[string]model.Check {
	out := map[string]model.Check{}
	for _, c := range checks {
		out[c.Name] = c
	}
	return out
}

func TestOVHCloudPoll(t *testing.T) {
	f := &fakeOVH{methods: map[string]int{}, routes: ovhRoutes(time.Now())}
	o, posted := newTestOVH(t, f)
	o.poll(context.Background())
	b := model.NewSeriesBuilder(0, testTags)
	checks, state := o.collect(context.Background(), time.Now(), b, testTags)
	if f.badSig != 0 || f.methods["GET"] == 0 || len(f.methods) != 1 {
		t.Fatalf("signatures refusées %d, méthodes %v", f.badSig, f.methods)
	}
	if state != "ok" {
		t.Fatalf("état %q", state)
	}
	c := byName(checks)
	if _, ok := c["ovh.dedicated_server.state"]; ok {
		t.Fatal("l'état du serveur part sous l'identité du serveur, pas de l'agent")
	}
	if s := c["ovh.ip.spam"]; s.Status != model.CheckCrit || !strings.Contains(s.Message, "bloquée pour spam") || s.Tags["ovh_service"] != "" {
		t.Fatalf("spam (routage pas encore connu au premier passage) : %+v", s)
	}
	if s := c["ovh.ip.mitigation"]; s.Status != model.CheckWarn {
		t.Fatalf("mitigation : %+v", s)
	}
	if s := c["ovh.ip.antihack"]; s.Status != model.CheckOK {
		t.Fatalf("antihack : %+v", s)
	}
	if s := c["ovhcloud.can_connect"]; s.Status != model.CheckOK {
		t.Fatalf("connexion : %+v", s)
	}
	// Échéances : serveur sans renouvellement automatique à 5 jours (critique), domaine automatique.
	var renew []model.Check
	for _, x := range checks {
		if x.Name == "ovh.service.renewal" {
			renew = append(renew, x)
		}
	}
	if len(renew) != 1 || renew[0].Status != model.CheckOK || renew[0].Tags["ovh_type"] != "domain" {
		t.Fatalf("échéances restées sur l'agent (domaine seul) : %+v", renew)
	}
	if len(b.Series()) != 1 || b.Series()[0].Tags["ovh_service"] != "example.org" {
		t.Fatalf("séries de l'agent : %+v", b.Series())
	}
	for _, p := range f.paths {
		if strings.Contains(p, "2001:db8") {
			t.Fatalf("bloc IPv6 interrogé : %s", p)
		}
	}
	if len(*posted) != 0 {
		t.Fatalf("premier passage : l'historique ne se rejoue pas, %d envoi(s)", len(*posted))
	}

	// Deuxième passage : une intervention nouvelle devient un événement ; le routage est connu.
	f.mu.Lock()
	f.routes["/dedicated/server/ns1.example/intervention"] = `[1,2,3]`
	f.mu.Unlock()
	o.poll(context.Background())
	if len(*posted) != 1 {
		t.Fatalf("événements : %d envoi(s)", len(*posted))
	}
	evs := (*posted)[0]["events"].([]model.Event)
	if len(evs) != 1 || evs[0].Type != "ovh_intervention" || !strings.Contains(evs[0].Title, "mx.example.org") ||
		!strings.Contains(evs[0].Text, "disque remplacé") {
		t.Fatalf("événement : %+v", evs)
	}
	checks, _ = o.collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags)
	if s := byName(checks)["ovh.ip.spam"]; s.Tags["ovh_service"] != "ns1.example" {
		t.Fatalf("routage du bloc : %+v", s)
	}
	o.poll(context.Background())
	if len(*posted) != 1 {
		t.Fatalf("intervention déjà vue rejouée : %d envoi(s)", len(*posted))
	}
}

func TestOVHCloudRefusedAndStale(t *testing.T) {
	f := &fakeOVH{methods: map[string]int{}, routes: ovhRoutes(time.Now()), refuseAll: true}
	o, _ := newTestOVH(t, f)
	o.poll(context.Background())
	checks, state := o.collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags)
	if len(checks) != 1 || checks[0].Status != model.CheckCrit || !strings.HasPrefix(state, "erreur") ||
		!strings.Contains(checks[0].Message, "identifiants refusés") {
		t.Fatalf("refus : %q %+v", state, checks)
	}
	if len(f.paths) != 1 {
		t.Fatalf("après un refus d'identifiants, le relevé s'arrête : %v", f.paths)
	}
	// Relevé réussi mais vieux de plus de 3 intervalles : plus rien n'est affirmé.
	f.refuseAll = false
	o.poll(context.Background())
	later := time.Now().Add(50 * time.Minute)
	checks, _ = o.collect(context.Background(), later, model.NewSeriesBuilder(0, testTags), testTags)
	if len(checks) != 1 || checks[0].Name != "ovhcloud.can_connect" || checks[0].Status != model.CheckCrit {
		t.Fatalf("relevé périmé : %+v", checks)
	}
}

func TestOVHCloudConfig(t *testing.T) {
	o := newOVHCloud(config.OVHCloudConfig{Enabled: "auto"}, Deps{})
	if c, st := o.collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags); c != nil || st != "absent" {
		t.Fatalf("sans identifiants : %q", st)
	}
	o = newOVHCloud(config.OVHCloudConfig{Enabled: "true"}, Deps{})
	if _, st := o.collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags); !strings.HasPrefix(st, "erreur") {
		t.Fatalf("activé sans identifiants : %q", st)
	}
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	var si ovhServiceInfos
	si.Expiration, si.Status = "2026-12-10", "ok"
	if c, _ := renewalCheck("s", "s", "domain", si, now); c.status != model.CheckWarn {
		t.Fatalf("manuel à 21 j : %+v", c)
	}
	si.Renew.Automatic = true
	if c, _ := renewalCheck("s", "s", "domain", si, now); c.status != model.CheckOK {
		t.Fatalf("automatique : %+v", c)
	}
	si.Status = "expired"
	if c, _ := renewalCheck("s", "s", "domain", si, now); c.status != model.CheckCrit {
		t.Fatalf("expiré : %+v", c)
	}
}

// Le serveur dédié est publié comme ressource : fiche « cloud_resource », état, échéance, jours restants.
func TestOVHCloudResource(t *testing.T) {
	f := &fakeOVH{methods: map[string]int{}, routes: ovhRoutes(time.Now())}
	srv := httptest.NewServer(f.handler(t, "AS", "CK"))
	defer srv.Close()
	got := sent{}
	o := newOVHCloud(config.OVHCloudConfig{Enabled: "auto", Endpoint: srv.URL + "/1.0", ApplicationKey: "AK",
		ApplicationSecret: "AS", ConsumerKey: "CK", Interval: config.Duration(15 * time.Minute)},
		Deps{AgentID: testAgentID, AgentVersion: "0.4.8", Tags: map[string]string{"env": "prod", "role": "ia"},
			Post: func(route string, fields map[string]any) { got[route] = append(got[route], fields) }})
	o.pace, o.tags = 0, testTags
	o.once.Do(func() {})
	o.poll(context.Background())
	o.collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags)
	if len(got[routeHost]) != 1 || len(got[routeChecks]) != 1 || len(got[routeMetrics]) != 1 {
		t.Fatalf("envois : %d fiche(s), %d contrôle(s), %d mesure(s)", len(got[routeHost]), len(got[routeChecks]), len(got[routeMetrics]))
	}
	info := got[routeHost][0]["agent"].(model.AgentInfo)
	host := got[routeHost][0]["host"].(model.Host)
	if info.Via != testAgentID || info.OS != "cloud" || info.Hostname != "ns1.example" || info.ID != resourceID(testAgentID, "ovh:dedicated_server:ns1.example") {
		t.Fatalf("identité : %+v", info)
	}
	if host.Kind != "cloud_resource" || host.Device.DisplayName != "mx.example.org" || host.Device.IP != "192.0.2.10" ||
		host.Tags["provider"] != "ovh" || host.Tags["env"] != "prod" || host.Tags["role"] != "" || len(host.IPAddresses) != 1 {
		t.Fatalf("fiche : %+v %+v", host, host.Device)
	}
	checks := byName(got[routeChecks][0]["checks"].([]model.Check))
	if checks["ovh.dedicated_server.state"].Status != model.CheckOK || checks["ovh.service.renewal"].Status != model.CheckCrit ||
		checks["ovh.dedicated_server.state"].Tags["host"] != "ns1.example" {
		t.Fatalf("contrôles du serveur : %+v", checks)
	}
	series := got[routeMetrics][0]["series"].([]model.Series)
	if len(series) != 1 || series[0].Metric != "ovh.service.days_left" || series[0].Tags["host"] != "ns1.example" {
		t.Fatalf("mesures du serveur : %+v", series)
	}
	// Collecte suivante : contrôles et mesures repartent, la fiche attend ses 5 minutes.
	o.collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags)
	if len(got[routeHost]) != 1 || len(got[routeChecks]) != 2 {
		t.Fatalf("deuxième collecte : %d fiche(s), %d envois de contrôles", len(got[routeHost]), len(got[routeChecks]))
	}
}
