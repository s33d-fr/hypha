package integrations

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// Vague 6 : les réponses de testdata/jenkins, testdata/github et testdata/odoo ont été relevées sur
// les vrais logiciels le 21/11/2026 (Jenkins 2.580.2 LTS, Odoo 19.0-20261112 en conteneur ; API
// publique de github.com pour s33d-fr/hypha, réduite aux champs lus).

func readTestdata(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", p))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func fakeJenkins(t *testing.T, header bool, health int) *httptest.Server {
	root, queue := readTestdata(t, "jenkins/root.json"), readTestdata(t, "jenkins/queue.json")
	computer, hc := readTestdata(t, "jenkins/computer.json"), readTestdata(t, "jenkins/health.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, _ := r.BasicAuth(); u != "hypha" || p != "jeton" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if header {
			w.Header().Set("X-Jenkins", "2.580.2")
		}
		switch r.URL.Path {
		case "/api/json":
			if !strings.Contains(r.URL.RawQuery, "lastCompletedBuild") {
				t.Errorf("arbre de champs absent : %s", r.URL.RawQuery)
			}
			w.Write(root)
		case "/queue/api/json":
			w.Write(queue)
		case "/computer/api/json":
			w.Write(computer)
		case "/metrics/currentUser/healthcheck":
			w.WriteHeader(health)
			if health == http.StatusForbidden {
				fmt.Fprint(w, "<html>Access Denied</html>")
				return
			}
			w.Write(hc)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestJenkinsRealResponses(t *testing.T) {
	srv := fakeJenkins(t, true, http.StatusOK)
	j := newJenkins(config.JenkinsConfig{Enabled: "auto", URL: srv.URL, Username: "hypha", Password: "jeton", MaxJobs: 500})
	checks, state, got := run2(t, j)
	if state != "ok" {
		t.Fatalf("état %q", state)
	}
	kv := "instance=" + srv.URL
	expectValue(t, got, "jenkins.jobs.total{"+kv+"}", 5)
	expectValue(t, got, "jenkins.jobs.failing{"+kv+"}", 1)
	expectValue(t, got, "jenkins.jobs.unstable{"+kv+"}", 1)
	expectValue(t, got, "jenkins.queue.size{"+kv+"}", 1)
	expectValue(t, got, "jenkins.queue.stuck{"+kv+"}", 1)
	expectValue(t, got, "jenkins.executors.total{"+kv+"}", 2)
	expectValue(t, got, "jenkins.nodes.offline{"+kv+"}", 1)
	expectValue(t, got, "jenkins.node.disk_free{"+kv+",node=Built-In Node}", 72538464256)
	expectValue(t, got, "jenkins.node.disk_free_percent{"+kv+",node=Built-In Node}", 70.5)
	expectValue(t, got, "jenkins.job.health{"+kv+",job=essai-echec}", 0)
	expectValue(t, got, "jenkins.health_check{check=disk-space,"+kv+"}", 1)
	jobs := checkMap(checks, "job")
	for job, want := range map[string]int{"essai-ok": model.CheckOK, "essai-echec": model.CheckCrit,
		"essai-instable": model.CheckWarn, "dossier/essai-imbrique": model.CheckOK} {
		if c := jobs["jenkins.job.status/"+job]; c.Status != want {
			t.Errorf("%s : %+v", job, c)
		}
	}
	if _, ok := jobs["jenkins.job.status/essai-bloque"]; ok {
		t.Error("tâche jamais construite jugée")
	}
	if c := checkMap(checks, "node")["jenkins.node.status/agent-hors-ligne"]; c.Status != model.CheckCrit {
		t.Errorf("nœud hors ligne : %+v", c)
	}
	if c, _ := checkBy(checks, "jenkins.queue"); c.Status != model.CheckWarn || !strings.Contains(c.Message, "etiquette-absente") {
		t.Errorf("file : %+v", c)
	}
	if c, _ := checkBy(checks, "jenkins.health"); c.Status != model.CheckOK || !strings.Contains(c.Message, "4 contrôles") {
		t.Errorf("santé : %+v", c)
	}
	if c, _ := checkBy(checks, "jenkins.can_connect"); c.Status != model.CheckOK || !strings.Contains(c.Message, "2.580.2") {
		t.Errorf("joignable : %+v", c)
	}

	// Filtre des tâches, plugin Metrics sans droit (403, page HTML) : rien d'inventé.
	srv = fakeJenkins(t, true, http.StatusForbidden)
	j = newJenkins(config.JenkinsConfig{Enabled: "true", URL: srv.URL, Username: "hypha", Password: "jeton", Jobs: []string{"dossier/*"}, MaxJobs: 500})
	checks, _, got = run2(t, j)
	expectValue(t, got, "jenkins.jobs.total{instance="+srv.URL+"}", 1)
	if _, ok := got["jenkins.health_check{check=disk-space,instance="+srv.URL+"}"]; ok {
		t.Error("contrôle de santé inventé")
	}
	if c, _ := checkBy(checks, "jenkins.health"); c.Status != model.CheckOK {
		t.Errorf("santé sans plugin : %+v", c)
	}
}

func TestJenkinsNotJenkins(t *testing.T) {
	srv := fakeJenkins(t, false, http.StatusOK)
	j := newJenkins(config.JenkinsConfig{Enabled: "auto", URL: config.DefaultJenkinsURL, MaxJobs: 500})
	j.http = newHTTPProbe(srv.URL, "hypha", "jeton", false)
	if checks, state := j.collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags); state != "absent" || len(checks) > 0 {
		t.Fatalf("auto, autre logiciel sur 8080 : %q %+v", state, checks)
	}
	j = newJenkins(config.JenkinsConfig{Enabled: "auto", URL: srv.URL, Username: "hypha", Password: "faux", MaxJobs: 500})
	checks, state := j.collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags)
	if c, _ := checkBy(checks, "jenkins.can_connect"); c.Status != model.CheckCrit || !strings.Contains(state, "403") {
		t.Fatalf("compte refusé : %q %+v", state, checks)
	}
}

func fakeGitHub(t *testing.T, token string) *httptest.Server {
	repo, rate := readTestdata(t, "github/repo.json"), readTestdata(t, "github/rate_limit.json")
	runs := string(readTestdata(t, "github/runs.json"))
	// Dernière exécution d'« agent » sur main passée en échec, une exécution en file depuis 20 min.
	runs = strings.Replace(runs, `"conclusion": "success"`, `"conclusion": "failure"`, 1)
	queued := fmt.Sprintf(`"workflow_runs": [{"id": 1, "name": "agent", "path": ".github/workflows/agent.yml", "workflow_id": 424430016, `+
		`"head_branch": "fix", "event": "push", "status": "queued", "conclusion": null, "created_at": %q, "run_number": 9},`,
		time.Now().Add(-20*time.Minute).UTC().Format(time.RFC3339))
	runs = strings.Replace(runs, `"workflow_runs": [`, queued, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token != "" && r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"message":"Bad credentials"}`)
			return
		}
		if r.Header.Get("X-GitHub-Api-Version") == "" {
			t.Error("version de l'API absente")
		}
		switch r.URL.Path {
		case "/repos/s33d-fr/hypha":
			w.Write(repo)
		case "/repos/s33d-fr/hypha/actions/runs":
			fmt.Fprint(w, runs)
		case "/rate_limit":
			w.Write(rate)
		case "/repos/s33d-fr/hypha/dependabot/alerts":
			fmt.Fprint(w, `[{"number":3,"state":"open","security_advisory":{"severity":"critical"}},{"number":4,"state":"open","security_advisory":{"severity":"medium"}}]`)
		case "/repos/s33d-fr/hypha/code-scanning/alerts":
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"no analysis found"}`)
		case "/repos/s33d-fr/hypha/secret-scanning/alerts":
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"message":"Resource not accessible by personal access token"}`)
		case "/repos/s33d-fr/absent":
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"Not Found"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func snapChecks(s vsSnapshot, name string) []ovhCheck {
	var out []ovhCheck
	for _, c := range s.checks {
		if c.name == name {
			out = append(out, c)
		}
	}
	return out
}

func snapGauge(s vsSnapshot, name string, kv ...string) (float64, bool) {
	for _, g := range s.gauges {
		if g.name != name {
			continue
		}
		ok := true
		for i := 0; i+1 < len(kv); i += 2 {
			found := false
			for j := 0; j+1 < len(g.kv); j += 2 {
				found = found || (g.kv[j] == kv[i] && g.kv[j+1] == kv[i+1])
			}
			ok = ok && found
		}
		if ok {
			return g.v, true
		}
	}
	return 0, false
}

func TestGitHubConnector(t *testing.T) {
	srv := fakeGitHub(t, "")
	c := newGitHub(config.GitHubConfig{Enabled: "auto", Endpoint: srv.URL, Repositories: []string{"s33d-fr/hypha"},
		Interval: config.Duration(5 * time.Minute), Security: "auto", Tags: map[string]string{"forge": "github"}}, Deps{})
	s := c.poll(context.Background())
	wf := map[string]ovhCheck{}
	for _, ch := range snapChecks(s, "github.workflow.status") {
		wf[ch.kv[3]] = ch
	}
	if wf["agent"].status != model.CheckCrit || !strings.Contains(wf["agent"].msg, "en échec") {
		t.Fatalf("workflow agent : %+v", wf)
	}
	if wf["dependabot/update-graph"].status != model.CheckOK {
		t.Errorf("workflow dynamique : %+v", wf)
	}
	if v, ok := snapGauge(s, "github.runs.queued", "repo", "s33d-fr/hypha"); !ok || v != 1 {
		t.Errorf("en file : %v", v)
	}
	if v, _ := snapGauge(s, "github.runs.queued.oldest_age", "repo", "s33d-fr/hypha"); v < 1100 || v > 1300 {
		t.Errorf("attente : %v", v)
	}
	if v, _ := snapGauge(s, "github.workflows.failing", "forge", "github"); v != 1 {
		t.Errorf("en échec : %v", v)
	}
	if v, _ := snapGauge(s, "github.workflow.run.duration", "workflow", "agent"); v != 161 {
		t.Errorf("durée : %v", v)
	}
	if v, _ := snapGauge(s, "github.rate_limit.remaining"); v != 58 {
		t.Errorf("quota : %v", v)
	}
	if len(snapChecks(s, "github.security")) != 0 {
		t.Error("alertes lues sans jeton")
	}
	if conn := snapChecks(s, "github.can_connect"); len(conn) != 1 || conn[0].status != model.CheckOK || !strings.Contains(conn[0].msg, "sans jeton") {
		t.Fatalf("connexion : %+v", conn)
	}

	// Avec jeton : alertes lues quand le jeton le permet ; dépôt introuvable = lecture partielle.
	srv = fakeGitHub(t, "jeton")
	c = newGitHub(config.GitHubConfig{Enabled: "true", Token: "jeton", Endpoint: srv.URL, Repositories: []string{"s33d-fr/hypha", "s33d-fr/absent"},
		Interval: config.Duration(time.Minute), Security: "auto"}, Deps{})
	s = c.poll(context.Background())
	sec := snapChecks(s, "github.security")
	if len(sec) != 1 || sec[0].status != model.CheckCrit || !strings.Contains(sec[0].msg, "1 alerte(s) critique") {
		t.Fatalf("sécurité : %+v", sec)
	}
	if v, _ := snapGauge(s, "github.dependabot.alerts", "severity", "medium"); v != 1 {
		t.Errorf("dependabot moyen : %v", v)
	}
	conn := snapChecks(s, "github.can_connect")[0]
	if conn.status != model.CheckWarn || !strings.Contains(conn.msg, "introuvable") || !strings.Contains(conn.msg, "code scanning, secret scanning") {
		t.Fatalf("connexion partielle : %+v", conn)
	}
	if strings.Contains(fmt.Sprint(s), "Bearer") {
		t.Error("jeton dans l'instantané")
	}

	// Jeton refusé : tout en échec.
	c = newGitHub(config.GitHubConfig{Enabled: "true", Token: "faux", Endpoint: srv.URL, Repositories: []string{"s33d-fr/hypha"},
		Interval: config.Duration(time.Minute)}, Deps{})
	s = c.poll(context.Background())
	if conn := snapChecks(s, "github.can_connect")[0]; conn.status != model.CheckCrit || !strings.Contains(conn.msg, "Bad credentials") {
		t.Fatalf("jeton refusé : %+v", conn)
	}
}

func fakeOdoo(t *testing.T, dbUp, xmlrpc bool) *httptest.Server {
	ver, info := readTestdata(t, "odoo/xmlrpc-version.xml"), readTestdata(t, "odoo/version_info.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/web/health":
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Query().Get("db_server_status") != "" && !dbUp {
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprint(w, `{"status": "fail", "db_server_status": false}`)
				return
			}
			if r.URL.Query().Get("db_server_status") != "" {
				fmt.Fprint(w, `{"status": "pass", "db_server_status": true}`)
				return
			}
			fmt.Fprint(w, `{"status": "pass"}`)
		case "/xmlrpc/2/common":
			if !xmlrpc {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Write(ver)
		case "/web/webclient/version_info":
			w.Write(info)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestOdooHealthVersionProcesses(t *testing.T) {
	srv := fakeOdoo(t, true, true)
	on := true
	o := newOdoo(config.OdooConfig{Enabled: "auto", URL: srv.URL, Processes: &on})
	// Image officielle (sans setproctitle) : maître, 3 workers à la même ligne de commande, gevent ;
	// la base PostgreSQL et un shell d'administration ne comptent pas.
	args := "\x00--workers=2\x00--max-cron-threads=1\x00-d\x00essai\x00"
	o.proc = fakeProc(t, map[int][2]string{
		10: {"/usr/bin/python3\x00/usr/bin/odoo" + args, "1 0 0"},
		11: {"/usr/bin/python3\x00/usr/bin/odoo" + args, "10 0 0"},
		12: {"/usr/bin/python3\x00/usr/bin/odoo" + args, "10 0 0"},
		13: {"/usr/bin/python3\x00/usr/bin/odoo" + args, "10 0 0"},
		14: {"/usr/bin/python3\x00/usr/bin/odoo\x00gevent" + args, "10 0 0"},
		20: {"postgres: odoo essai 172.20.0.3(54724) idle\x00", "1 0 0"},
		21: {"/usr/bin/python3\x00/usr/bin/odoo\x00shell\x00-d\x00essai\x00", "1 0 0"},
		30: {"odoo: WorkerCron 30 \x00", "40 0 0"},
		40: {"/opt/odoo/odoo-bin\x00-c\x00/etc/odoo.conf\x00", "1 0 0"},
	})
	checks, state, got := run2(t, o)
	if state != "ok" {
		t.Fatalf("état %q", state)
	}
	kv := "instance=" + srv.URL
	expectValue(t, got, "odoo.processes{"+kv+",role=main}", 2)
	expectValue(t, got, "odoo.processes{"+kv+",role=worker}", 3)
	expectValue(t, got, "odoo.processes{"+kv+",role=gevent}", 1)
	expectValue(t, got, "odoo.processes{"+kv+",role=cron}", 1)
	expectValue(t, got, "odoo.workers{"+kv+"}", 4)
	expectValue(t, got, "odoo.database.reachable{"+kv+"}", 1)
	if _, ok := got["odoo.response_time{endpoint=xmlrpc,"+kv+"}"]; !ok {
		t.Error("temps de réponse XML-RPC absent")
	}
	if c, _ := checkBy(checks, "odoo.can_connect"); c.Status != model.CheckOK || !strings.Contains(c.Message, "19.0-20261112") {
		t.Fatalf("joignable : %+v", c)
	}
	if c, _ := checkBy(checks, "odoo.health"); c.Status != model.CheckOK {
		t.Fatalf("santé : %+v", c)
	}

	// Base injoignable : /web/health rend 500 « fail » ; XML-RPC retiré : repli sur version_info.
	srv = fakeOdoo(t, false, false)
	off := false
	o = newOdoo(config.OdooConfig{Enabled: "auto", URL: srv.URL, Processes: &off})
	checks, _, got = run2(t, o)
	if c, _ := checkBy(checks, "odoo.health"); c.Status != model.CheckCrit || !strings.Contains(c.Message, "PostgreSQL") {
		t.Fatalf("base injoignable : %+v", checks)
	}
	if _, ok := got["odoo.response_time{endpoint=version_info,instance="+srv.URL+"}"]; !ok {
		t.Error("repli version_info absent")
	}
	if _, ok := got["odoo.processes{instance="+srv.URL+",role=main}"]; ok {
		t.Error("processus lus malgré processes: false")
	}
	// Rien n'écoute : en auto, muet.
	o = newOdoo(config.OdooConfig{Enabled: "auto", URL: config.DefaultOdooURL})
	o.http = newHTTPProbe("http://127.0.0.1:1", "", "", false)
	if checks, state := o.collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags); state != "absent" || len(checks) > 0 {
		t.Fatalf("absent : %q %+v", state, checks)
	}
}

func TestWSUS(t *testing.T) {
	svc := allRunning("WsusService", "W3SVC", `MSSQL$MICROSOFT##WID`)
	f := &fakeRole{present: true, svc: svc, ps: wsusJSONEssai}
	w := newWSUS(config.WSUSConfig{Enabled: "true", StaleDays: 30, SyncMaxAge: config.Duration(48 * time.Hour)}, f.deps())
	b := model.NewSeriesBuilder(0, testTags)
	checks, state := w.collect(context.Background(), time.Now(), b, testTags)
	if state != "ok" {
		t.Fatalf("état %q %+v", state, checks)
	}
	got := index(b.Series())
	expectValue(t, got, "wsus.computers{}", 1)
	expectValue(t, got, "wsus.updates.not_approved{}", 12)
	expectValue(t, got, "wsus.computers.stale{}", 1)
	if c, _ := checkBy(checks, "wsus.sync"); c.Status != model.CheckOK {
		t.Fatalf("synchronisation : %+v", c)
	}
	if c, _ := checkBy(checks, "wsus.can_connect"); c.Status != model.CheckOK || !strings.Contains(c.Message, "base interne") {
		t.Fatalf("joignable : %+v", c)
	}
	// Panne : synchronisation en échec, pool arrêté, service arrêté.
	f.ps = strings.Replace(strings.Replace(wsusJSONEssai, `"result":"Succeeded"`, `"result":"Failed"`, 1), `"pool":"Started"`, `"pool":"Stopped"`, 1)
	svc["WsusService"] = svcState{State: "stopped"}
	w.info.at = time.Time{}
	checks, _ = w.collect(context.Background(), time.Now().Add(time.Minute), model.NewSeriesBuilder(0, testTags), testTags)
	for name, want := range map[string]int{"wsus.sync": model.CheckCrit, "wsus.app_pool": model.CheckCrit, "wsus.can_connect": model.CheckCrit} {
		if c, _ := checkBy(checks, name); c.Status != want {
			t.Errorf("%s : %+v", name, c)
		}
	}
	if c := checkMap(checks, "windows_service")["wsus.service/WsusService"]; c.Status != model.CheckCrit {
		t.Errorf("service : %+v", c)
	}
	// Relevés réels : jamais synchronisé (WARN), synchronisation en cours (OK), en échec (CRIT).
	svc["WsusService"] = svcState{State: "running"}
	for ps, want := range map[string]int{wsusJSONAvant: model.CheckWarn, wsusJSONEnCours: model.CheckOK, wsusJSONEchec: model.CheckCrit} {
		f.ps = ps
		w := newWSUS(config.WSUSConfig{Enabled: "true", StaleDays: 30, SyncMaxAge: config.Duration(48 * time.Hour)}, f.deps())
		b := model.NewSeriesBuilder(0, testTags)
		checks, state := w.collect(context.Background(), time.Now(), b, testTags)
		if c, _ := checkBy(checks, "wsus.sync"); c.Status != want || state != "ok" {
			t.Errorf("relevé réel : %q %+v", state, c)
		}
		got := index(b.Series())
		expectValue(t, got, "wsus.database.size{}", map[string]float64{wsusJSONAvant: 10682368, wsusJSONEnCours: 20119552, wsusJSONEchec: 33751040}[ps])
		if c, _ := checkBy(checks, "wsus.sync"); ps == wsusJSONEchec && !strings.Contains(c.Message, "proxy-inexistant") {
			t.Errorf("message de WSUS absent : %+v", c)
		}
		if _, ok := got["wsus.sync.age{}"]; ok {
			t.Error("âge d'une réussite inventé")
		}
	}
	// Coupée par défaut ; rôle absent en auto : muet.
	if _, st := newWSUS(config.WSUSConfig{Enabled: "false"}, f.deps()).collect(context.Background(), time.Now(), b, testTags); st != "disabled" {
		t.Errorf("défaut : %q", st)
	}
	f.present = false
	if c, st := newWSUS(config.WSUSConfig{Enabled: "auto"}, f.deps()).collect(context.Background(), time.Now(), b, testTags); st != "absent" || len(c) > 0 {
		t.Errorf("rôle absent : %q", st)
	}
	if s := wsusScriptFor(14); !strings.Contains(s, "AddDays(-14)") || strings.Contains(s, "__STALE__") {
		t.Error("script : stale_days non posé")
	}
}
