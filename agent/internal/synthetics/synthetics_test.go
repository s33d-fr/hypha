package synthetics

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"s33d.fr/hypha/agent/internal/model"
)

// cfgOf décode une config comme elle arrive du serveur (nombres en float64).
func cfgOf(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func testServer(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Version", "0.4.0")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok","data":{"items":[{"id":7}]}}`))
	})
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/ok", http.StatusFound) })
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		b, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(b), `"user":"alice"`) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"token":"jeton-42"}`))
	})
	mux.HandleFunc("/me", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer jeton-42" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"name":"Alice"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestHTTPProbe(t *testing.T) {
	srv := testServer(t)
	ctx := context.Background()
	r := Execute(ctx, Test{ID: 1, Kind: "http", Target: srv.URL + "/redirect",
		Config: cfgOf(t, `{"expect_status":200,"expect_text":"\"ok\"","timeout_s":5,
		 "assertions":[{"type":"json_path","path":"$.data.items[0].id","op":"eq","value":7},
		               {"type":"header","path":"X-Version","op":"matches","value":"^0\\.4"},
		               {"type":"latency_ms","op":"lt","value":5000}]}`)})
	if !r.OK || *r.StatusCode != 200 || r.Details["redirects"] != 1 {
		t.Fatalf("test http : %+v", r)
	}
	r = Execute(ctx, Test{Kind: "http", Target: srv.URL + "/absent", Config: cfgOf(t, `{"expect_status":200}`)})
	if r.OK || !strings.Contains(r.Error, "statut 404 (attendu 200)") {
		t.Fatalf("statut inattendu : %+v", r)
	}
	r = Execute(ctx, Test{Kind: "http", Target: srv.URL + "/ok", Config: cfgOf(t, `{"assertions":[{"type":"json_path","path":"status","op":"eq","value":"ko"}]}`)})
	if r.OK || !strings.Contains(r.Error, "assertion json_path status eq ko (obtenu ok)") {
		t.Fatalf("assertion fausse : %+v", r.Error)
	}
	r = Execute(ctx, Test{Kind: "http", Target: "http://127.0.0.1:1/", Config: cfgOf(t, `{"timeout_s":2}`)})
	if r.OK || !strings.Contains(r.Error, "connexion impossible") {
		t.Fatalf("port fermé : %+v", r)
	}
}

func TestMultistep(t *testing.T) {
	srv := testServer(t)
	steps := []Step{
		{Name: "connexion", Method: "POST", URL: "{{base}}/login", Body: `{"user":"{{user}}"}`,
			Assertions: []Assertion{{Type: "status", Op: "eq", Value: float64(200)}},
			Extract:    []Extract{{Name: "jeton", From: "json_path", Path: "$.token"}}},
		{Name: "profil", URL: "{{base}}/me", Headers: map[string]string{"Authorization": "Bearer {{jeton}}"},
			Assertions: []Assertion{{Type: "body_contains", Value: "Alice"}}},
	}
	cfg := map[string]any{"variables": map[string]any{"base": srv.URL, "user": "alice"}}
	r := Execute(context.Background(), Test{Kind: "multistep", Steps: steps, Config: cfg})
	if !r.OK {
		t.Fatalf("multistep : %s %+v", r.Error, r.Details)
	}
	sd := r.Details["steps"].([]map[string]any)
	if len(sd) != 2 || sd[1]["status_code"] != 200 {
		t.Fatalf("détail des étapes : %+v", sd)
	}
	cfg["variables"].(map[string]any)["user"] = "intrus"
	r = Execute(context.Background(), Test{Kind: "multistep", Steps: steps, Config: cfg})
	sd = r.Details["steps"].([]map[string]any)
	if r.OK || !strings.HasPrefix(r.Error, "connexion") || sd[1]["skipped"] != true {
		t.Fatalf("échec de la première étape : %s %+v", r.Error, sd)
	}
}

func TestSSLProbe(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	r := Execute(context.Background(), Test{Kind: "ssl", Target: strings.TrimPrefix(srv.URL, "https://"), Config: cfgOf(t, `{"timeout_s":5}`)})
	if r.OK || !strings.Contains(r.Error, "certificat refusé") {
		t.Fatalf("certificat auto-signé accepté : %+v", r)
	}
	if _, ok := r.Details["ssl_days_left"]; !ok {
		t.Fatalf("échéance lue sans vérification absente : %+v", r.Details)
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	if r := Execute(context.Background(), Test{Kind: "tcp", Target: ln.Addr().String()}); !r.OK {
		t.Fatalf("tcp : %+v", r)
	}
}

// fakeDNS répond aux questions A et MX de exemple.test ; le reste en NXDOMAIN.
func fakeDNS(t *testing.T) string {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 1500)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			var p dnsmessage.Parser
			h, err := p.Start(buf[:n])
			if err != nil {
				continue
			}
			q, _ := p.Question()
			resp := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: h.ID, Response: true, RCode: dnsmessage.RCodeSuccess})
			hdr := dnsmessage.ResourceHeader{Name: q.Name, Class: dnsmessage.ClassINET, TTL: 300}
			known := q.Name.String() == "exemple.test."
			if !known {
				resp = dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: h.ID, Response: true, RCode: dnsmessage.RCodeNameError})
			}
			resp.StartQuestions()
			resp.Question(q)
			resp.StartAnswers()
			if known {
				switch q.Type {
				case dnsmessage.TypeA:
					resp.AResource(hdr, dnsmessage.AResource{A: [4]byte{192, 0, 2, 63}})
				case dnsmessage.TypeMX:
					mx, _ := dnsmessage.NewName("mx.exemple.test.")
					resp.MXResource(hdr, dnsmessage.MXResource{Pref: 10, MX: mx})
				}
			}
			out, _ := resp.Finish()
			pc.WriteTo(out, addr)
		}
	}()
	return pc.LocalAddr().String()
}

func TestDNSProbe(t *testing.T) {
	ns := fakeDNS(t)
	ctx := context.Background()
	r := Execute(ctx, Test{Kind: "dns", Target: "exemple.test", Config: map[string]any{"nameserver": ns, "expect": "192.0.2.63", "timeout_s": 3.0}})
	if !r.OK || r.Details["ttl"] != uint32(300) {
		t.Fatalf("dns A : %+v", r)
	}
	r = Execute(ctx, Test{Kind: "dns", Target: "exemple.test", Config: map[string]any{"nameserver": ns, "record_type": "MX", "timeout_s": 3.0}})
	if !r.OK || r.Details["records"].([]string)[0] != "10 mx.exemple.test." {
		t.Fatalf("dns MX : %+v", r)
	}
	r = Execute(ctx, Test{Kind: "dns", Target: "absent.test", Config: map[string]any{"nameserver": ns, "timeout_s": 3.0}})
	if r.OK || r.Error != "NXDOMAIN : nom inexistant" {
		t.Fatalf("dns NXDOMAIN : %+v", r)
	}
	r = Execute(ctx, Test{Kind: "dns", Target: "exemple.test", Config: map[string]any{"nameserver": ns, "record_type": "TXT", "timeout_s": 3.0}})
	if r.OK || r.Error != "aucun enregistrement TXT" {
		t.Fatalf("dns sans réponse : %+v", r)
	}
}

func TestICMP(t *testing.T) {
	r := Execute(context.Background(), Test{Kind: "icmp", Target: "127.0.0.1", Config: map[string]any{"count": 2.0, "timeout_s": 2.0}})
	if r.Skip != "" {
		t.Skipf("écho ICMP indisponible ici : %s", r.Skip)
	}
	if !r.OK || r.Details["packets_received"] != 2 {
		t.Fatalf("icmp : %+v", r)
	}
}

func TestHelpers(t *testing.T) {
	var doc any
	json.Unmarshal([]byte(`{"a":{"b":[{"c":"x"},{"c":5}]}}`), &doc)
	for p, want := range map[string]any{"$.a.b[1].c": 5.0, "a.b.0.c": "x", "$['a'].b.length": 2.0, "a.b[-1].c": 5.0} {
		if v, ok := jsonPath(doc, p); !ok || v != want {
			t.Errorf("%s : %v %v", p, v, ok)
		}
	}
	if _, ok := jsonPath(doc, "a.z"); ok {
		t.Error("chemin absent trouvé")
	}
	if ok, _ := compare(200.0, "lt", "300"); !ok {
		t.Error("comparaison numérique de texte")
	}
	if ok, why := compare("abc", "gt", 1.0); ok || why == "" {
		t.Error("comparaison numérique impossible acceptée")
	}
	if render("{{a}}-{{ b }}-{{c}}", map[string]string{"a": "1", "b": "2"}) != "1-2-{{c}}" {
		t.Error("gabarit")
	}
	if h, p, err := splitHostPort("[2001:db8::1]:8443", 443); err != nil || h != "2001:db8::1" || p != 8443 {
		t.Error("ipv6", h, p, err)
	}
}

func TestRunnerSchedule(t *testing.T) {
	var got []model.SyntheticResult
	r := New("web-01", 2, func(res []model.SyntheticResult) { got = append(got, res...) }, func(string) {}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t1 := Test{ID: 1, Kind: "tcp", Target: "127.0.0.1:1", IntervalS: 60}
	r.Update([]Test{t1, {ID: 2, Kind: "tcp", Target: "127.0.0.1:2", IntervalS: 60}})
	next := r.tests[1].next
	r.Update([]Test{t1})
	if r.Count() != 1 || !r.tests[1].next.Equal(next) {
		t.Fatal("test inchangé replanifié, ou test retiré gardé")
	}
	r.tests[1].next = time.Now().Add(-time.Second)
	e := r.due(time.Now())[0]
	r.runOne(context.Background(), e)
	r.flush()
	if len(got) != 1 || got[0].Location != "agent:web-01" || got[0].OK || got[0].Error == nil {
		t.Fatalf("résultat : %+v", got)
	}
}
