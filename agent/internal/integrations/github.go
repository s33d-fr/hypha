package integrations

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── GitHub (vague 6) : connecteur en lecture seule, github.com ou GitHub Enterprise Cloud ────
//
// Toutes les interval (5 min par défaut), pour chaque dépôt nommé : branche par défaut (relue
// toutes les 6 h), 100 dernières exécutions de GitHub Actions (dernière exécution terminée de
// chaque workflow sur la branche par défaut, hors demandes de fusion ; exécutions en file et en
// cours), et, avec un jeton qui le permet, alertes ouvertes de Dependabot, du code scanning et du
// secret scanning ; puis le quota de l'API (/rate_limit, qui ne compte pas dans le quota). Sans
// jeton : dépôts publics seulement, 60 requêtes par heure pour l'adresse publique de la machine.
// Aucune écriture, aucun workflow relancé.

type ghRepoInfo struct {
	at            time.Time
	DefaultBranch string `json:"default_branch"`
	Archived      bool   `json:"archived"`
	Disabled      bool   `json:"disabled"`
}

type ghRun struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Path         string `json:"path"`
	WorkflowID   int64  `json:"workflow_id"`
	HeadBranch   string `json:"head_branch"`
	Event        string `json:"event"`
	Status       string `json:"status"`
	Conclusion   string `json:"conclusion"`
	CreatedAt    string `json:"created_at"`
	RunStartedAt string `json:"run_started_at"`
	UpdatedAt    string `json:"updated_at"`
	HTMLURL      string `json:"html_url"`
	RunNumber    int64  `json:"run_number"`
}

// ghWorkflowName : nom stable d'un workflow ; les workflows dynamiques de GitHub (Dependabot,
// graphe des dépendances) portent un nom différent à chaque exécution : leur chemin les nomme.
func ghWorkflowName(r ghRun) string {
	if p, ok := strings.CutPrefix(r.Path, "dynamic/"); ok {
		return p
	}
	if r.Name != "" {
		return r.Name
	}
	p := r.Path
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		p = p[i+1:]
	}
	return strings.TrimSuffix(strings.TrimSuffix(p, ".yml"), ".yaml")
}

func ghTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

// ghRunState : état d'une exécution terminée.
func ghRunState(conclusion string) (int, string) {
	switch conclusion {
	case "success", "neutral", "skipped":
		return model.CheckOK, "réussie"
	case "failure", "startup_failure":
		return model.CheckCrit, "en échec"
	case "timed_out":
		return model.CheckCrit, "arrêtée par le délai"
	case "cancelled":
		return model.CheckWarn, "annulée"
	case "action_required":
		return model.CheckWarn, "en attente d'une action"
	}
	return model.CheckUnknown, conclusion
}

var ghQueued = map[string]bool{"queued": true, "waiting": true, "requested": true, "pending": true}

type github struct {
	cfg    config.GitHubConfig
	client *http.Client
	base   string
	hdr    map[string]string
	extra  []string // étiquettes de la conf (tags), triées

	mu    sync.Mutex
	repos map[string]ghRepoInfo
}

func newGitHub(cfg config.GitHubConfig, d Deps) *cloudConn {
	g := &github{cfg: cfg, client: &http.Client{Timeout: 30 * time.Second}, base: strings.TrimRight(cfg.Endpoint, "/"),
		hdr: map[string]string{"X-GitHub-Api-Version": "2022-11-28"}, repos: map[string]ghRepoInfo{}}
	if g.base == "" {
		g.base = config.DefaultGitHubAPI
	}
	if cfg.Token != "" {
		g.hdr["Authorization"] = "Bearer " + cfg.Token
	}
	keys := make([]string, 0, len(cfg.Tags))
	for k := range cfg.Tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		g.extra = append(g.extra, k, cfg.Tags[k])
	}
	return &cloudConn{id: "github", enabled: cfg.Enabled, configured: cfg.Configured(),
		missing: "repositories (propriétaire/dépôt) est nécessaire", interval: cfg.Interval.D(),
		pub: newResourcePublisher(d), poll: g.poll}
}

func (g *github) kv(kv ...string) []string { return append(append([]string{}, kv...), g.extra...) }

func (g *github) get(ctx context.Context, p string, out any) (int, error) {
	return getJSON(ctx, g.client, g.base+p, g.hdr, out)
}

func (g *github) poll(ctx context.Context) vsSnapshot {
	start := time.Now()
	var s vsSnapshot
	read, failing := 0, 0
	var notes []string
	for _, repo := range g.cfg.Repositories {
		n, note, err := g.repo(ctx, repo, start, &s)
		if err != nil {
			s.errs = append(s.errs, repo+" : "+err.Error())
			continue
		}
		read++
		failing += n
		if note != "" {
			notes = append(notes, repo+" : "+note)
		}
	}
	// Quota de l'API (/rate_limit ne compte pas dans le quota).
	quota := ""
	var rl struct {
		Resources struct {
			Core struct {
				Limit     float64 `json:"limit"`
				Remaining float64 `json:"remaining"`
				Reset     int64   `json:"reset"`
				Used      float64 `json:"used"`
			} `json:"core"`
		} `json:"resources"`
	}
	if _, err := g.get(ctx, "/rate_limit", &rl); err == nil && rl.Resources.Core.Limit > 0 {
		c := rl.Resources.Core
		kv := g.kv("resource", "core")
		s.gauges = append(s.gauges, ovhGauge{"github.rate_limit.limit", "count", c.Limit, kv},
			ovhGauge{"github.rate_limit.remaining", "count", c.Remaining, kv},
			ovhGauge{"github.rate_limit.used_percent", "percent", round1(100 * c.Used / c.Limit), kv})
		quota = fmt.Sprintf("quota %g/%g", c.Remaining, c.Limit)
		st, msg := model.CheckOK, fmt.Sprintf("%g requêtes restantes sur %g", c.Remaining, c.Limit)
		reset := time.Unix(c.Reset, 0).UTC().Format("15:04 UTC")
		switch {
		case c.Remaining == 0:
			st, msg = model.CheckCrit, fmt.Sprintf("quota de l'API épuisé (%g requêtes), retour à %s", c.Limit, reset)
		case c.Remaining < c.Limit/10:
			st, msg = model.CheckWarn, fmt.Sprintf("%g requêtes restantes sur %g, retour à %s", c.Remaining, c.Limit, reset)
		}
		s.checks = append(s.checks, ovhCheck{"github.rate_limit", st, msg, g.kv()})
	} else if err != nil {
		s.errs = append(s.errs, "rate_limit : "+err.Error())
	}
	token := "sans jeton"
	if g.cfg.Token != "" {
		token = "avec jeton"
	}
	conn := ovhCheck{"github.can_connect", model.CheckOK,
		fmt.Sprintf("%d dépôt(s) lu(s) %s, %d workflow(s) en échec", read, token, failing), g.kv()}
	if quota != "" {
		conn.msg += ", " + quota
	}
	if len(notes) > 0 {
		conn.msg += " ; " + strings.Join(notes, " ; ")
	}
	switch {
	case read == 0 && len(g.cfg.Repositories) > 0:
		conn.status, conn.msg = model.CheckCrit, model.Truncate(strings.Join(s.errs, " ; "), 400)
	case len(s.errs) > 0:
		conn.status, conn.msg = model.CheckWarn, conn.msg+" ; lecture partielle : "+model.Truncate(strings.Join(s.errs, " ; "), 300)
	}
	conn.msg = model.Truncate(conn.msg, 500)
	s.checks = append(s.checks, conn)
	s.at, s.took = time.Now(), time.Since(start)
	return s
}

// repo lit un dépôt ; rend le nombre de workflows en échec et une remarque (alertes non lisibles).
func (g *github) repo(ctx context.Context, repo string, now time.Time, s *vsSnapshot) (int, string, error) {
	esc := url.PathEscape(strings.SplitN(repo, "/", 2)[0]) + "/" + url.PathEscape(strings.SplitN(repo, "/", 2)[1])
	g.mu.Lock()
	info, ok := g.repos[repo]
	g.mu.Unlock()
	if !ok || now.Sub(info.at) > 6*time.Hour {
		var r ghRepoInfo
		code, err := g.get(ctx, "/repos/"+esc, &r)
		if err != nil {
			if code == http.StatusNotFound {
				return 0, "", fmt.Errorf("dépôt introuvable (ou privé et invisible pour ce jeton)")
			}
			return 0, "", err
		}
		r.at = now
		info = r
		g.mu.Lock()
		g.repos[repo] = info
		g.mu.Unlock()
	}
	rkv := g.kv("repo", repo)

	var runs struct {
		Runs []ghRun `json:"workflow_runs"`
	}
	code, err := g.get(ctx, "/repos/"+esc+"/actions/runs?per_page=100&exclude_pull_requests=true", &runs)
	if err != nil && code != http.StatusNotFound {
		return 0, "", fmt.Errorf("exécutions : %v", err)
	}
	queued, running, oldest := 0.0, 0.0, 0.0
	latest := map[int64]ghRun{}
	for _, r := range runs.Runs {
		switch {
		case ghQueued[r.Status]:
			queued++
			if t := ghTime(r.CreatedAt); !t.IsZero() {
				oldest = max(oldest, now.Sub(t).Seconds())
			}
		case r.Status == "in_progress":
			running++
		case r.Status == "completed":
			if r.HeadBranch != info.DefaultBranch || r.Event == "pull_request" || r.Event == "pull_request_target" {
				continue
			}
			if cur, ok := latest[r.WorkflowID]; !ok || ghTime(r.CreatedAt).After(ghTime(cur.CreatedAt)) {
				latest[r.WorkflowID] = r
			}
		}
	}
	s.gauges = append(s.gauges, ovhGauge{"github.runs.queued", "count", queued, rkv},
		ovhGauge{"github.runs.in_progress", "count", running, rkv},
		ovhGauge{"github.runs.queued.oldest_age", "second", round1(oldest), rkv})
	ids := make([]int64, 0, len(latest))
	for id := range latest {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(a, b int) bool { return ghWorkflowName(latest[ids[a]]) < ghWorkflowName(latest[ids[b]]) })
	failing := 0
	for _, id := range ids {
		r := latest[id]
		name := ghWorkflowName(r)
		wkv := g.kv("repo", repo, "workflow", name)
		st, word := ghRunState(r.Conclusion)
		if st == model.CheckCrit {
			failing++
		}
		if d := ghTime(r.UpdatedAt).Sub(ghTime(r.RunStartedAt)); d > 0 && !ghTime(r.RunStartedAt).IsZero() {
			s.gauges = append(s.gauges, ovhGauge{"github.workflow.run.duration", "second", round1(d.Seconds()), wkv})
		}
		msg := fmt.Sprintf("%s, %s n° %d sur %s : %s (%s)", repo, name, r.RunNumber, info.DefaultBranch, word,
			ghTime(r.CreatedAt).UTC().Format("02/01/2006 15:04 UTC"))
		if st != model.CheckOK && r.HTMLURL != "" {
			msg += " " + r.HTMLURL
		}
		s.checks = append(s.checks, ovhCheck{"github.workflow.status", st, model.Truncate(msg, 300), wkv})
	}
	s.gauges = append(s.gauges, ovhGauge{"github.workflows.failing", "count", float64(failing), rkv})

	// Alertes de sécurité : seulement avec un jeton qui le permet.
	note := ""
	if g.cfg.Token != "" && g.cfg.Security != "false" {
		note = g.security(ctx, repo, esc, rkv, s)
	}
	return failing, note, nil
}

// security : alertes ouvertes (100 au plus par type) ; un type non permis par le jeton est sauté.
func (g *github) security(ctx context.Context, repo, esc string, rkv []string, s *vsSnapshot) string {
	var denied []string
	counts := map[string]float64{}
	readable := false
	var dep []struct {
		Advisory struct {
			Severity string `json:"severity"`
		} `json:"security_advisory"`
	}
	code, err := g.get(ctx, "/repos/"+esc+"/dependabot/alerts?state=open&per_page=100", &dep)
	switch {
	case err == nil:
		readable = true
		sev := map[string]float64{"critical": 0, "high": 0, "medium": 0, "low": 0}
		for _, a := range dep {
			sev[strings.ToLower(a.Advisory.Severity)]++
		}
		for k, v := range sev {
			s.gauges = append(s.gauges, ovhGauge{"github.dependabot.alerts", "count", v, append(append([]string{}, rkv...), "severity", k)})
			counts["dependabot:"+k] = v
		}
	case code == http.StatusForbidden || code == http.StatusNotFound || code == http.StatusUnauthorized:
		denied = append(denied, "Dependabot")
	default:
		s.errs = append(s.errs, repo+" (Dependabot) : "+err.Error())
	}
	var cs []struct {
		Rule struct {
			Severity         string `json:"severity"`
			SecuritySeverity string `json:"security_severity_level"`
		} `json:"rule"`
	}
	code, err = g.get(ctx, "/repos/"+esc+"/code-scanning/alerts?state=open&per_page=100", &cs)
	switch {
	case err == nil:
		readable = true
		sev := map[string]float64{"critical": 0, "high": 0, "medium": 0, "low": 0}
		for _, a := range cs {
			k := strings.ToLower(a.Rule.SecuritySeverity)
			if k == "" {
				k = map[string]string{"error": "high", "warning": "medium"}[strings.ToLower(a.Rule.Severity)]
				if k == "" {
					k = "low"
				}
			}
			sev[k]++
		}
		for k, v := range sev {
			s.gauges = append(s.gauges, ovhGauge{"github.code_scanning.alerts", "count", v, append(append([]string{}, rkv...), "severity", k)})
			counts["code:"+k] = v
		}
	case code == http.StatusForbidden || code == http.StatusNotFound || code == http.StatusUnauthorized:
		denied = append(denied, "code scanning") // 404 : aucune analyse configurée
	default:
		s.errs = append(s.errs, repo+" (code scanning) : "+err.Error())
	}
	var ss []struct {
		Number int64 `json:"number"`
	}
	code, err = g.get(ctx, "/repos/"+esc+"/secret-scanning/alerts?state=open&per_page=100", &ss)
	switch {
	case err == nil:
		readable = true
		s.gauges = append(s.gauges, ovhGauge{"github.secret_scanning.alerts", "count", float64(len(ss)), rkv})
		counts["secret"] = float64(len(ss))
	case code == http.StatusForbidden || code == http.StatusNotFound || code == http.StatusUnauthorized:
		denied = append(denied, "secret scanning")
	default:
		s.errs = append(s.errs, repo+" (secret scanning) : "+err.Error())
	}
	if readable {
		crit := counts["dependabot:critical"] + counts["code:critical"]
		high := counts["dependabot:high"] + counts["code:high"]
		st, msg := model.CheckOK, fmt.Sprintf("%s : aucune alerte critique ni haute ouverte", repo)
		switch {
		case crit > 0 || counts["secret"] > 0:
			st, msg = model.CheckCrit, fmt.Sprintf("%s : %g alerte(s) critique(s), %g haute(s), %g secret(s) exposé(s)", repo, crit, high, counts["secret"])
		case high > 0:
			st, msg = model.CheckWarn, fmt.Sprintf("%s : %g alerte(s) de gravité haute ouverte(s)", repo, high)
		}
		s.checks = append(s.checks, ovhCheck{"github.security", st, msg, rkv})
	}
	if len(denied) > 0 {
		return "alertes non lisibles avec ce jeton : " + strings.Join(denied, ", ")
	}
	return ""
}
