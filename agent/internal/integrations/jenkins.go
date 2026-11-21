package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	pathpkg "path"
	"sort"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── Jenkins (vague 6) : API JSON, en lecture seule ──────────────────────────────────────────
//
// Toutes les 60 s : /api/json (mode, arrêt préparé, tâches et leurs derniers builds, dossiers et
// projets multibranches sur trois niveaux), /queue/api/json (file d'attente), /computer/api/json
// (exécuteurs, nœuds hors ligne, place libre et temps de réponse des nœuds), et, si le plugin
// Metrics est installé, ses contrôles de santé (/metrics/currentUser/healthcheck). Compte : un
// utilisateur aux droits Overall/Read, Job/Read et Agent/ExtendedRead (ou anonyme si Jenkins le
// permet), son jeton d'API en mot de passe. Aucune écriture, aucun build lancé.

type Jenkins struct {
	probe
	cfg  config.JenkinsConfig
	http *httpProbe
}

func newJenkins(cfg config.JenkinsConfig) *Jenkins {
	explicit := cfg.URL != config.DefaultJenkinsURL || cfg.Username != ""
	return &Jenkins{cfg: cfg, http: newHTTPProbe(cfg.URL, cfg.Username, cfg.Password, cfg.Insecure),
		probe: newProbe("jenkins", "jenkins.can_connect", intent(cfg.Enabled, explicit), "instance", cfg.URL)}
}

func (j *Jenkins) period() time.Duration { return 60 * time.Second }

// jenkinsJobFields : champs lus par tâche ; « jobs » se répète pour les dossiers (trois niveaux).
const jenkinsJobFields = "name,fullName,color,buildable,inQueue,lastBuild[number,building,timestamp]," +
	"lastCompletedBuild[number,result,timestamp,duration,url],lastSuccessfulBuild[timestamp],healthReport[score]"

func jenkinsTree() string {
	t := jenkinsJobFields
	for i := 0; i < 3; i++ {
		t = jenkinsJobFields + ",jobs[" + t + "]"
	}
	return "mode,quietingDown,useSecurity,jobs[" + t + "]"
}

type jenkinsBuild struct {
	Number    int64   `json:"number"`
	Result    string  `json:"result"`
	Building  bool    `json:"building"`
	Timestamp float64 `json:"timestamp"` // ms
	Duration  float64 `json:"duration"`  // ms
	URL       string  `json:"url"`
}

type jenkinsJob struct {
	Class      string        `json:"_class"`
	Name       string        `json:"name"`
	FullName   string        `json:"fullName"`
	Color      string        `json:"color"`
	Buildable  *bool         `json:"buildable"`
	InQueue    bool          `json:"inQueue"`
	Last       *jenkinsBuild `json:"lastBuild"`
	Completed  *jenkinsBuild `json:"lastCompletedBuild"`
	Success    *jenkinsBuild `json:"lastSuccessfulBuild"`
	Health     []struct {
		Score float64 `json:"score"`
	} `json:"healthReport"`
	Jobs []jenkinsJob `json:"jobs"`
}

type jenkinsRoot struct {
	Mode         string       `json:"mode"`
	QuietingDown bool         `json:"quietingDown"`
	UseSecurity  bool         `json:"useSecurity"`
	Jobs         []jenkinsJob `json:"jobs"`
}

type jenkinsQueue struct {
	Items []struct {
		ID           int64   `json:"id"`
		Blocked      bool    `json:"blocked"`
		Buildable    bool    `json:"buildable"`
		Stuck        bool    `json:"stuck"`
		InQueueSince float64 `json:"inQueueSince"`
		Why          string  `json:"why"`
		Task         struct {
			Name     string `json:"name"`
			FullName string `json:"fullName"`
		} `json:"task"`
	} `json:"items"`
}

type jenkinsComputers struct {
	Busy     float64 `json:"busyExecutors"`
	Total    float64 `json:"totalExecutors"`
	Computer []struct {
		Name          string                     `json:"displayName"`
		Offline       bool                       `json:"offline"`
		TempOffline   bool                       `json:"temporarilyOffline"`
		OfflineReason string                     `json:"offlineCauseReason"`
		Executors     float64                    `json:"numExecutors"`
		Idle          bool                       `json:"idle"`
		Monitor       map[string]json.RawMessage `json:"monitorData"`
	} `json:"computer"`
}

// doHeader : comme do, avec les en-têtes de la réponse (X-Jenkins porte la version).
func (h *httpProbe) doHeader(ctx context.Context, p string) ([]byte, int, http.Header, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.base+p, nil)
	if err != nil {
		return nil, 0, nil, err
	}
	req.Header.Set("User-Agent", "hypha-agent")
	req.Header.Set("Accept", "application/json")
	if h.user != "" || h.pass != "" {
		req.SetBasicAuth(h.user, h.pass)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, 0, nil, cleanNetErr(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, h.maxBytes))
	return data, resp.StatusCode, resp.Header, err
}

// jenkinsFlatten : tâches (pas les dossiers), noms complets ; un dossier n'a pas de lastBuild.
func jenkinsFlatten(list []jenkinsJob, out *[]jenkinsJob) {
	for _, jb := range list {
		if len(jb.Jobs) > 0 || strings.HasSuffix(jb.Class, "Folder") || strings.HasSuffix(jb.Class, "MultiBranchProject") ||
			strings.HasSuffix(jb.Class, "OrganizationFolder") {
			jenkinsFlatten(jb.Jobs, out)
			continue
		}
		if jb.FullName == "" {
			jb.FullName = jb.Name
		}
		*out = append(*out, jb)
	}
}

func (j *Jenkins) wanted(full string) bool {
	if len(j.cfg.Jobs) == 0 {
		return true
	}
	for _, m := range j.cfg.Jobs {
		if ok, _ := pathpkg.Match(m, full); ok {
			return true
		}
	}
	return false
}

func (j *Jenkins) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := j.det.skip(now); skip {
		return nil, st
	}
	ts := model.Ms(now)
	data, code, hdr, err := j.http.doHeader(ctx, "/api/json?tree="+jenkinsTree())
	if err == nil {
		err = httpStatusErr("/api/json", code, data)
	}
	var root jenkinsRoot
	if err == nil && (hdr.Get("X-Jenkins") == "" && hdr.Get("X-Hudson") == "") {
		err = fmt.Errorf("/api/json : pas de réponse de Jenkins à cette adresse (en-tête X-Jenkins absent)")
	}
	if err == nil {
		if e := json.Unmarshal(data, &root); e != nil {
			err = fmt.Errorf("/api/json : réponse illisible (%v)", e)
		}
	}
	if err != nil {
		return j.fail(ts, tags, err)
	}
	j.det.ok()
	version := hdr.Get("X-Jenkins")
	kv := j.kvs()
	var checks []model.Check
	var errs []string

	// Tâches et derniers builds.
	var jobs []jenkinsJob
	jenkinsFlatten(root.Jobs, &jobs)
	sort.Slice(jobs, func(a, c int) bool { return jobs[a].FullName < jobs[c].FullName })
	var total, failing, unstable, disabled, building float64
	kept := 0
	for _, jb := range jobs {
		if !j.wanted(jb.FullName) {
			continue
		}
		total++
		jkv := j.kvs("job", jb.FullName)
		isDisabled := jb.Color == "disabled" || (jb.Buildable != nil && !*jb.Buildable)
		if isDisabled {
			disabled++
		}
		if jb.Last != nil && jb.Last.Building {
			building++
		}
		c := jb.Completed
		if c != nil {
			switch c.Result {
			case "FAILURE":
				failing++
			case "UNSTABLE":
				unstable++
			}
		}
		if kept >= j.cfg.MaxJobs {
			continue
		}
		kept++
		if len(jb.Health) > 0 {
			b.Gauge("jenkins.job.health", "percent", jb.Health[0].Score, jkv...)
		}
		if jb.Success != nil && jb.Success.Timestamp > 0 {
			b.Gauge("jenkins.job.last_success.age", "second", round1(float64(ts)/1000-jb.Success.Timestamp/1000), jkv...)
		}
		if c == nil || isDisabled {
			continue // jamais construite, ou désactivée : aucun état à juger
		}
		b.Gauge("jenkins.job.last_build.duration", "second", round1(c.Duration/1000), jkv...)
		st, word := model.CheckOK, "réussi"
		switch c.Result {
		case "FAILURE":
			st, word = model.CheckCrit, "en échec"
		case "UNSTABLE":
			st, word = model.CheckWarn, "instable"
		case "ABORTED":
			st, word = model.CheckWarn, "interrompu"
		case "NOT_BUILT":
			st, word = model.CheckUnknown, "non construit"
		}
		msg := fmt.Sprintf("%s : build n° %d %s", jb.FullName, c.Number, word)
		if c.Timestamp > 0 {
			msg += " (" + time.UnixMilli(int64(c.Timestamp)).UTC().Format("02/01/2006 15:04 UTC") + ")"
		}
		if c.URL != "" && st != model.CheckOK {
			msg += " " + c.URL
		}
		checks = append(checks, model.Check{TS: ts, Name: "jenkins.job.status", Status: st, Message: model.Truncate(msg, 300), Tags: tags(jkv...)})
	}
	b.Gauge("jenkins.jobs.total", "count", total, kv...)
	b.Gauge("jenkins.jobs.failing", "count", failing, kv...)
	b.Gauge("jenkins.jobs.unstable", "count", unstable, kv...)
	b.Gauge("jenkins.jobs.disabled", "count", disabled, kv...)
	b.Gauge("jenkins.jobs.building", "count", building, kv...)

	// File d'attente.
	var q jenkinsQueue
	if err := j.http.getJSON(ctx, "/queue/api/json?tree=items[id,blocked,buildable,stuck,inQueueSince,why,task[name,fullName]]", &q); err == nil {
		var blocked, buildable, stuck, oldest float64
		var stuckNames []string
		for _, it := range q.Items {
			switch {
			case it.Blocked:
				blocked++
			case it.Buildable:
				buildable++
			}
			if it.Stuck {
				stuck++
				n := firstNonEmpty(it.Task.FullName, it.Task.Name)
				if it.Why != "" {
					n += " (" + model.Truncate(it.Why, 120) + ")"
				}
				stuckNames = append(stuckNames, n)
			}
			if it.InQueueSince > 0 {
				oldest = max(oldest, float64(ts)/1000-it.InQueueSince/1000)
			}
		}
		b.Gauge("jenkins.queue.size", "count", float64(len(q.Items)), kv...)
		b.Gauge("jenkins.queue.blocked", "count", blocked, kv...)
		b.Gauge("jenkins.queue.buildable", "count", buildable, kv...)
		b.Gauge("jenkins.queue.stuck", "count", stuck, kv...)
		b.Gauge("jenkins.queue.oldest_age", "second", round1(oldest), kv...)
		st, msg := model.CheckOK, fmt.Sprintf("%d build(s) en attente, aucun bloqué", len(q.Items))
		if stuck > 0 {
			sort.Strings(stuckNames)
			st, msg = model.CheckWarn, fmt.Sprintf("%g build(s) bloqué(s) dans la file : %s", stuck, strings.Join(stuckNames, " ; "))
		}
		checks = append(checks, model.Check{TS: ts, Name: "jenkins.queue", Status: st, Message: model.Truncate(msg, 500), Tags: tags(kv...)})
	} else {
		errs = append(errs, err.Error())
	}

	// Exécuteurs et nœuds.
	var cp jenkinsComputers
	if err := j.http.getJSON(ctx, "/computer/api/json?depth=1&tree=busyExecutors,totalExecutors,computer[displayName,offline,"+
		"temporarilyOffline,offlineCauseReason,numExecutors,idle,monitorData[*]]", &cp); err == nil {
		b.Gauge("jenkins.executors.total", "count", cp.Total, kv...)
		b.Gauge("jenkins.executors.busy", "count", cp.Busy, kv...)
		b.Gauge("jenkins.executors.idle", "count", cp.Total-cp.Busy, kv...)
		if cp.Total > 0 {
			b.Gauge("jenkins.executors.usage", "percent", round1(100*cp.Busy/cp.Total), kv...)
		}
		offline := 0.0
		for _, n := range cp.Computer {
			nkv := j.kvs("node", n.Name)
			v := 0.0
			st, msg := model.CheckOK, n.Name+" en ligne"
			switch {
			case n.Offline && n.TempOffline:
				v, st, msg = 1, model.CheckWarn, n.Name+" mis hors ligne par un administrateur"
			case n.Offline:
				v, st, msg = 1, model.CheckCrit, n.Name+" hors ligne"
			}
			if n.Offline {
				offline++
				if r := strings.TrimSpace(n.OfflineReason); r != "" {
					msg += " : " + model.Truncate(r, 200)
				}
			}
			b.Gauge("jenkins.node.offline", "", v, nkv...)
			b.Gauge("jenkins.node.executors", "count", n.Executors, nkv...)
			jenkinsMonitors(b, n.Monitor, nkv)
			checks = append(checks, model.Check{TS: ts, Name: "jenkins.node.status", Status: st, Message: model.Truncate(msg, 300), Tags: tags(nkv...)})
		}
		b.Gauge("jenkins.nodes.total", "count", float64(len(cp.Computer)), kv...)
		b.Gauge("jenkins.nodes.offline", "count", offline, kv...)
	} else {
		errs = append(errs, err.Error())
	}

	// Santé : plugin Metrics (absent = rien), arrêt préparé.
	var bad []string
	hc := map[string]struct {
		Healthy bool   `json:"healthy"`
		Message string `json:"message"`
	}{}
	data, code, err = j.http.do(ctx, http.MethodGet, "/metrics/currentUser/healthcheck", nil, nil)
	plugin := err == nil && (code == http.StatusOK || code == http.StatusInternalServerError) && json.Unmarshal(data, &hc) == nil && len(hc) > 0
	if plugin {
		names := make([]string, 0, len(hc))
		for k := range hc {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			v := 1.0
			if !hc[k].Healthy {
				v = 0
				m := k
				if hc[k].Message != "" {
					m += " (" + model.Truncate(hc[k].Message, 120) + ")"
				}
				bad = append(bad, m)
			}
			b.Gauge("jenkins.health_check", "", v, j.kvs("check", k)...)
		}
	}
	st, msg := model.CheckOK, "Jenkins "+version+" en service"
	if plugin {
		msg += fmt.Sprintf(", %d contrôles de santé au vert", len(hc))
	}
	switch {
	case len(bad) > 0:
		st, msg = model.CheckCrit, "contrôles de santé en défaut : "+strings.Join(bad, " ; ")
	case root.QuietingDown:
		st, msg = model.CheckWarn, "arrêt préparé (quietingDown) : aucun nouveau build ne démarre"
	}
	checks = append(checks, model.Check{TS: ts, Name: "jenkins.health", Status: st, Message: model.Truncate(msg, 500), Tags: tags(kv...)})
	quiet := 0.0
	if root.QuietingDown {
		quiet = 1
	}
	b.Gauge("jenkins.quieting_down", "", quiet, kv...)

	ok := fmt.Sprintf("Jenkins %s : %g tâche(s), %g en échec", version, total, failing)
	checks = append(checks, j.okCheck(ts, tags, ok))
	if len(errs) > 0 {
		return checks, "partiel : " + strings.Join(errs, " ; ")
	}
	return checks, "ok"
}

// jenkinsMonitors : place libre (DiskSpaceMonitor, TemporarySpaceMonitor) et temps de réponse
// (ResponseTimeMonitor) d'un nœud ; un moniteur désactivé ou pas encore relevé vaut null. Jenkins ne
// rend monitorData qu'au compte qui a le droit Agent/Connect (vide sinon : rien n'est publié).
func jenkinsMonitors(b *model.SeriesBuilder, m map[string]json.RawMessage, kv []string) {
	type space struct {
		Size  *float64 `json:"size"`
		Total *float64 `json:"totalSize"` // depuis Jenkins 2.4xx
	}
	var disk, tmp space
	if raw, ok := m["hudson.node_monitors.DiskSpaceMonitor"]; ok && json.Unmarshal(raw, &disk) == nil && disk.Size != nil {
		b.Gauge("jenkins.node.disk_free", "byte", *disk.Size, kv...)
		if disk.Total != nil && *disk.Total > 0 {
			b.Gauge("jenkins.node.disk_free_percent", "percent", round1(100**disk.Size / *disk.Total), kv...)
		}
	}
	if raw, ok := m["hudson.node_monitors.TemporarySpaceMonitor"]; ok && json.Unmarshal(raw, &tmp) == nil && tmp.Size != nil {
		b.Gauge("jenkins.node.temp_free", "byte", *tmp.Size, kv...)
	}
	var rt struct {
		Average *float64 `json:"average"`
	}
	if raw, ok := m["hudson.node_monitors.ResponseTimeMonitor"]; ok && json.Unmarshal(raw, &rt) == nil && rt.Average != nil {
		b.Gauge("jenkins.node.response_time", "millisecond", *rt.Average, kv...)
	}
}
