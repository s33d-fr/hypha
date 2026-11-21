package integrations

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── Proxmox Backup Server : API (port 8007), jeton au rôle Audit ────────────────────────────
//
// Par relevé (1 min) : occupation de chaque magasin (/status/datastore-usage : capacité, prévision
// de remplissage, état du dernier ramasse-miettes), groupes de sauvegarde de chaque espace de noms
// (âge de la dernière sauvegarde, nombre d'instantanés), dernier passage des travaux planifiés
// (vérification, élagage, synchronisation, ramasse-miettes : /admin/verify, /admin/prune,
// /admin/sync, /admin/gc), tâches en échec depuis le relevé précédent (événements), état du nœud.
// Toutes les 5 min : dernière tâche de sauvegarde de chaque groupe sur 7 jours (contrôle levé dès
// que la suivante réussit). Toutes les 15 min : état de vérification des instantanés. L'abonnement
// n'est jamais lu.

const (
	pbsMaxStores     = 50
	pbsMaxNamespaces = 50
	pbsBackupWindow  = 7 * 24 * time.Hour
)

type PBS struct {
	cfg  config.PBSConfig
	det  detector
	http *http.Client
	base string
	auth string
	post func(route string, fields map[string]any)
	err  error // réglage TLS illisible (autorité, empreinte)

	since     int64 // fin de la dernière tâche en échec relayée (secondes)
	version   string
	versionAt time.Time

	backupsAt time.Time
	backups   []model.Check // dernier état de la dernière sauvegarde de chaque groupe

	snapsAt time.Time
	snaps   map[string]pbsSnapStats // magasin → états de vérification
}

type pbsSnapStats struct{ total, failed, unverified float64 }

func newPBS(cfg config.PBSConfig, d Deps) *PBS {
	p := &PBS{cfg: cfg, post: d.Post, base: strings.TrimRight(cfg.URL, "/") + "/api2/json",
		auth: "PBSAPIToken=" + cfg.TokenID + ":" + cfg.TokenSecret}
	tc := &tls.Config{}
	switch {
	case cfg.Insecure:
		tc.InsecureSkipVerify = true //nolint:gosec // choisi dans la conf
	case cfg.Fingerprint != "":
		want := strings.ToLower(strings.ReplaceAll(cfg.Fingerprint, ":", ""))
		tc.InsecureSkipVerify = true //nolint:gosec // remplacé par la comparaison d'empreinte ci-dessous
		tc.VerifyPeerCertificate = func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return fmt.Errorf("aucun certificat présenté")
			}
			sum := sha256.Sum256(raw[0])
			if hex.EncodeToString(sum[:]) != want {
				return fmt.Errorf("empreinte du certificat différente de fingerprint")
			}
			return nil
		}
	case cfg.CAFile != "":
		pool := x509.NewCertPool()
		if pem, err := os.ReadFile(cfg.CAFile); err != nil || !pool.AppendCertsFromPEM(pem) {
			p.err = fmt.Errorf("autorité de certification illisible : %s", cfg.CAFile)
		}
		tc.RootCAs = pool
	}
	tr := &http.Transport{TLSClientConfig: tc, MaxIdleConns: 2, IdleConnTimeout: 90 * time.Second}
	p.http = &http.Client{Timeout: 20 * time.Second, Transport: tr}
	p.det = detector{mode: intent(cfg.Enabled, cfg.TokenID != "")}
	return p
}

func (p *PBS) name() string           { return "proxmox_backup" }
func (p *PBS) close()                 {}
func (p *PBS) period() time.Duration  { return p.cfg.Interval.D() }
func (p *PBS) where() []string        { return []string{"instance", p.cfg.URL} }
func (p *PBS) kvs(kv ...string) []string { return append(p.where(), kv...) }

// get lit une ressource ; out reçoit le champ « data » de la réponse.
func (p *PBS) get(ctx context.Context, path string, out any) error {
	if p.err != nil {
		return p.err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", p.auth)
	req.Header.Set("User-Agent", "hypha-agent")
	resp, err := p.http.Do(req)
	if err != nil {
		return cleanNetErr(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return fmt.Errorf("jeton refusé (HTTP 401)")
	case http.StatusForbidden:
		return fmt.Errorf("%s : accès refusé (HTTP 403), rôle Audit attendu pour l'utilisateur et le jeton", path)
	default:
		msg := strings.TrimSpace(string(bytes.TrimSpace(body)))
		if len(msg) > 120 || strings.ContainsAny(msg, "<{") {
			msg = ""
		}
		if msg != "" {
			return fmt.Errorf("%s : HTTP %d (%s)", path, resp.StatusCode, msg)
		}
		return fmt.Errorf("%s : HTTP %d", path, resp.StatusCode)
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("%s : réponse illisible", path)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(env.Data, out)
}

type pbsUsage struct {
	Store       string   `json:"store"`
	Total       *float64 `json:"total"`
	Used        float64  `json:"used"`
	Avail       float64  `json:"avail"`
	EstFull     *float64 `json:"estimated-full-date"`
	Error       string   `json:"error"`
	MountStatus string   `json:"mount-status"`
	GC          *struct {
		DiskBytes  float64 `json:"disk-bytes"`
		DiskChunks float64 `json:"disk-chunks"`
		IndexBytes float64 `json:"index-data-bytes"`
		StillBad   float64 `json:"still-bad"`
		Pending    float64 `json:"pending-bytes"`
	} `json:"gc-status"`
}

type pbsGroup struct {
	Type        string  `json:"backup-type"`
	ID          string  `json:"backup-id"`
	LastBackup  float64 `json:"last-backup"`
	BackupCount float64 `json:"backup-count"`
}

type pbsTask struct {
	UPID       string  `json:"upid"`
	WorkerType string  `json:"worker_type"`
	WorkerID   string  `json:"worker_id"`
	User       string  `json:"user"`
	StartTime  float64 `json:"starttime"`
	EndTime    float64 `json:"endtime"`
	Status     string  `json:"status"`
}

type pbsJob struct {
	ID           string  `json:"id"`
	Store        string  `json:"store"`
	LastRunState string  `json:"last-run-state"`
	LastRunEnd   float64 `json:"last-run-endtime"`
}

func (p *PBS) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := p.det.skip(now); skip {
		return nil, st
	}
	if p.cfg.TokenID == "" && p.det.mode == "auto" {
		return nil, "absent" // l'API exige un jeton
	}
	// Budget du relevé entier : un serveur qui répond puis se fige ne retient pas la boucle des intégrations.
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	ts := model.Ms(now)
	var usage []pbsUsage
	if err := p.get(ctx, "/status/datastore-usage", &usage); err != nil {
		if silent, st := p.det.failed(err.Error()); silent {
			return nil, st
		}
		return []model.Check{{TS: ts, Name: "proxmox_backup.can_connect", Status: model.CheckCrit, Message: err.Error(),
			Tags: tags(p.where()...)}}, "erreur : " + err.Error()
	}
	p.det.ok()
	kv := p.where()
	var checks []model.Check
	var problems []string
	stores := 0
	for i, u := range usage {
		if i >= pbsMaxStores {
			break
		}
		stores++
		skv := p.kvs("datastore", u.Store)
		if u.Error != "" || u.Total == nil {
			msg := firstNonEmpty(u.Error, "occupation inconnue")
			checks = append(checks, model.Check{TS: ts, Name: "proxmox_backup.datastore", Status: model.CheckCrit,
				Message: model.Truncate("magasin "+u.Store+" : "+msg, 300), Tags: tags(skv...)})
			continue
		}
		total := *u.Total
		b.Gauge("proxmox_backup.datastore.total", "byte", total, skv...)
		b.Gauge("proxmox_backup.datastore.used", "byte", u.Used, skv...)
		b.Gauge("proxmox_backup.datastore.avail", "byte", u.Avail, skv...)
		if total > 0 {
			b.Gauge("proxmox_backup.datastore.usage", "percent", round1(100*u.Used/total), skv...)
		}
		if u.EstFull != nil && *u.EstFull > 0 {
			days := (*u.EstFull - float64(now.Unix())) / 86400
			if days < 0 {
				days = 0
			}
			b.Gauge("proxmox_backup.datastore.days_until_full", "day", round1(days), skv...)
		}
		if g := u.GC; g != nil {
			b.Gauge("proxmox_backup.datastore.chunks", "object", g.DiskChunks, skv...)
			b.Gauge("proxmox_backup.datastore.bad_chunks", "object", g.StillBad, skv...)
			b.Gauge("proxmox_backup.datastore.gc_pending", "byte", g.Pending, skv...)
			if g.DiskBytes > 0 {
				b.Gauge("proxmox_backup.datastore.dedup_factor", "", round1(g.IndexBytes/g.DiskBytes), skv...)
			}
		}
		msg := fmt.Sprintf("magasin %s : %.1f %% occupés", u.Store, pct(u.Used, total))
		if u.MountStatus != "" && u.MountStatus != "nonremovable" {
			msg += " (" + u.MountStatus + ")"
		}
		checks = append(checks, model.Check{TS: ts, Name: "proxmox_backup.datastore", Status: model.CheckOK, Message: msg, Tags: tags(skv...)})
		if err := p.groups(ctx, now, b, u.Store); err != nil {
			problems = append(problems, "groupes de "+u.Store+" : "+err.Error())
		}
	}
	b.Gauge("proxmox_backup.datastores", "", float64(stores), kv...)

	jobs, err := p.jobs(ctx, ts, tags)
	if err != nil {
		problems = append(problems, "travaux : "+err.Error())
	}
	checks = append(checks, jobs...)
	if err := p.failedTasks(ctx, now, b, tags); err != nil {
		problems = append(problems, "tâches : "+err.Error())
	}
	if p.backupsAt.IsZero() || now.Sub(p.backupsAt) >= 5*time.Minute-time.Second {
		if bk, err := p.lastBackups(ctx, now, ts, tags); err != nil {
			problems = append(problems, "sauvegardes : "+err.Error())
		} else {
			p.backups, p.backupsAt = bk, now
		}
	}
	for _, c := range p.backups {
		c.TS = ts
		checks = append(checks, c)
	}
	if p.snaps == nil || now.Sub(p.snapsAt) >= 15*time.Minute-time.Second {
		p.snaps, p.snapsAt = p.snapshots(ctx, usage), now
	}
	for store, s := range p.snaps {
		skv := p.kvs("datastore", store)
		b.Gauge("proxmox_backup.datastore.snapshots", "object", s.total, skv...)
		b.Gauge("proxmox_backup.datastore.snapshots.verify_failed", "object", s.failed, skv...)
		b.Gauge("proxmox_backup.datastore.snapshots.unverified", "object", s.unverified, skv...)
	}
	p.node(ctx, b)
	if p.version == "" || now.Sub(p.versionAt) >= time.Hour {
		var v struct {
			Version string `json:"version"`
			Release string `json:"release"`
		}
		if p.get(ctx, "/version", &v) == nil && v.Version != "" {
			p.version, p.versionAt = v.Version+"."+v.Release, now
		}
	}
	conn := model.Check{TS: ts, Name: "proxmox_backup.can_connect", Status: model.CheckOK,
		Message: fmt.Sprintf("Proxmox Backup Server %s : %d magasin(s)", firstNonEmpty(p.version, "?"), stores), Tags: tags(kv...)}
	if stores == 0 {
		// Un jeton sans Datastore.Audit voit une liste vide, sans erreur : à dire.
		problems = append(problems, "aucun magasin visible (rôle Audit sur / attendu pour l'utilisateur et le jeton)")
	}
	if len(problems) > 0 {
		conn.Status, conn.Message = model.CheckWarn, conn.Message+" ; lecture partielle : "+model.Truncate(strings.Join(problems, " ; "), 300)
		return append(checks, conn), "partiel : " + model.Truncate(problems[0], 200)
	}
	return append(checks, conn), "ok"
}

// namespaces : espaces de noms d'un magasin (« » = racine), bornés.
func (p *PBS) namespaces(ctx context.Context, store string) []string {
	var list []struct {
		NS string `json:"ns"`
	}
	if p.get(ctx, "/admin/datastore/"+url.PathEscape(store)+"/namespace", &list) != nil || len(list) == 0 {
		return []string{""}
	}
	out := make([]string, 0, len(list))
	for i, n := range list {
		if i >= pbsMaxNamespaces {
			break
		}
		out = append(out, n.NS)
	}
	return out
}

func nsQuery(ns string) string {
	if ns == "" {
		return ""
	}
	return "?ns=" + url.QueryEscape(ns)
}

// groups : âge de la dernière sauvegarde et nombre d'instantanés de chaque groupe d'un magasin.
func (p *PBS) groups(ctx context.Context, now time.Time, b *model.SeriesBuilder, store string) error {
	n, newest := 0, -1.0
	for _, ns := range p.namespaces(ctx, store) {
		var list []pbsGroup
		if err := p.get(ctx, "/admin/datastore/"+url.PathEscape(store)+"/groups"+nsQuery(ns), &list); err != nil {
			return err
		}
		sort.Slice(list, func(i, j int) bool { return list[i].LastBackup > list[j].LastBackup })
		for _, g := range list {
			if n >= p.cfg.MaxGroups {
				break
			}
			n++
			gkv := p.kvs("datastore", store, "backup_type", g.Type, "backup_id", model.Truncate(g.ID, 100))
			if ns != "" {
				gkv = append(gkv, "pbs_namespace", model.Truncate(ns, 100))
			}
			b.Gauge("proxmox_backup.group.snapshots", "object", g.BackupCount, gkv...)
			if g.LastBackup > 0 {
				age := ageSeconds(now, g.LastBackup)
				b.Gauge("proxmox_backup.group.last_backup.age", "second", age, gkv...)
				if newest < 0 || age < newest {
					newest = age
				}
			}
		}
	}
	skv := p.kvs("datastore", store)
	b.Gauge("proxmox_backup.datastore.groups", "object", float64(n), skv...)
	if newest >= 0 {
		b.Gauge("proxmox_backup.datastore.last_backup.age", "second", newest, skv...)
	}
	return nil
}

// jobs : dernier passage de chaque travail planifié ; un travail jamais lancé n'a pas de contrôle.
func (p *PBS) jobs(ctx context.Context, ts int64, tags model.TagFunc) ([]model.Check, error) {
	var out []model.Check
	var firstErr error
	for _, j := range []struct{ path, kind, label string }{
		{"/admin/verify", "verify", "vérification"}, {"/admin/prune", "prune", "élagage"},
		{"/admin/sync", "sync", "synchronisation"}, {"/admin/gc", "garbage_collection", "ramasse-miettes"},
	} {
		var list []pbsJob
		if err := p.get(ctx, j.path, &list); err != nil {
			if m := err.Error(); strings.Contains(m, "HTTP 404") || strings.Contains(m, "HTTP 501") {
				continue // travaux absents de cette version de PBS
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, job := range list {
			if job.LastRunState == "" {
				continue
			}
			id := firstNonEmpty(job.ID, job.Store)
			when := ""
			if job.LastRunEnd > 0 {
				when = " le " + time.Unix(int64(job.LastRunEnd), 0).Format("02/01 15:04")
			}
			st, msg := model.CheckOK, fmt.Sprintf("%s %s réussi%s", j.label, id, when)
			switch s := job.LastRunState; {
			case s == "OK":
			case strings.HasPrefix(s, "WARNINGS"):
				st, msg = model.CheckWarn, fmt.Sprintf("%s %s%s avec avertissements (%s)", j.label, id, when, s)
			default:
				st, msg = model.CheckCrit, fmt.Sprintf("%s %s%s en échec : %s", j.label, id, when, model.Truncate(s, 200))
			}
			jkv := p.kvs("job_type", j.kind, "job_id", model.Truncate(id, 64))
			if job.Store != "" {
				jkv = append(jkv, "datastore", job.Store)
			}
			out = append(out, model.Check{TS: ts, Name: "proxmox_backup.job", Status: st, Message: msg, Tags: tags(jkv...)})
		}
	}
	return out, firstErr
}

// failedTasks : tâches en échec terminées depuis le relevé précédent → événements et compte par type.
func (p *PBS) failedTasks(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) error {
	// since filtre sur le début des tâches : un jour de marge pour les longues, dédoublonnées par leur fin.
	since := p.since - 86400
	if p.since == 0 {
		since = now.Add(-time.Hour).Unix()
	}
	var list []pbsTask
	if err := p.get(ctx, "/nodes/localhost/tasks?errors=1&limit=200&since="+strconv.FormatInt(since, 10), &list); err != nil {
		return err
	}
	first := p.since == 0
	maxEnd := p.since
	counts := map[string]float64{}
	var events []model.Event
	for _, t := range list {
		if t.EndTime == 0 || t.Status == "" || t.Status == "OK" {
			continue
		}
		end := int64(t.EndTime)
		if end > maxEnd {
			maxEnd = end
		}
		if first || end <= p.since {
			continue // au premier relevé, l'historique n'est pas relayé
		}
		counts[t.WorkerType]++
		alert := "error"
		if strings.HasPrefix(t.Status, "WARNINGS") {
			alert = "warning"
		}
		title := fmt.Sprintf("tâche %s %s : %s", t.WorkerType, t.WorkerID, t.Status)
		ev := model.Event{TS: end * 1000, Source: "proxmox_backup", Type: "proxmox_backup_task_" + t.WorkerType,
			Title: model.Truncate(title, 200), Text: model.Truncate(fmt.Sprintf("%s, lancée par %s", t.UPID, t.User), 1000),
			AlertType: alert, Priority: "normal", AggregationKey: "proxmox_backup:" + t.UPID}
		if tags != nil {
			ev.Tags = tags(p.kvs("pbs_task", t.WorkerType)...)
		}
		events = append(events, ev)
	}
	if first && maxEnd == 0 {
		maxEnd = now.Unix()
	}
	p.since = maxEnd
	for typ, n := range counts {
		b.Add("proxmox_backup.tasks.failed", model.TypeCount, "task", n, p.kvs("pbs_task", typ)...)
	}
	if len(events) > 0 && p.post != nil {
		p.post(routeEvents, map[string]any{"events": events})
	}
	return nil
}

// lastBackups : dernière tâche de sauvegarde (terminée) de chaque groupe sur 7 jours.
func (p *PBS) lastBackups(ctx context.Context, now time.Time, ts int64, tags model.TagFunc) ([]model.Check, error) {
	since := now.Add(-pbsBackupWindow).Unix()
	var list []pbsTask
	if err := p.get(ctx, "/nodes/localhost/tasks?typefilter=backup&limit=1000&since="+strconv.FormatInt(since, 10), &list); err != nil {
		return nil, err
	}
	last := map[string]pbsTask{}
	for _, t := range list {
		if t.WorkerType != "backup" || t.EndTime == 0 || t.Status == "" {
			continue
		}
		if cur, ok := last[t.WorkerID]; !ok || t.EndTime > cur.EndTime {
			last[t.WorkerID] = t
		}
	}
	ids := make([]string, 0, len(last))
	for id := range last {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []model.Check
	for i, id := range ids {
		if i >= p.cfg.MaxGroups {
			break
		}
		t := last[id]
		store, ns, typ, bid := pbsWorkerID(id)
		when := time.Unix(int64(t.EndTime), 0).Format("02/01 15:04")
		name := typ + "/" + bid
		st, msg := model.CheckOK, fmt.Sprintf("dernière sauvegarde de %s réussie le %s", name, when)
		switch s := t.Status; {
		case s == "OK":
		case strings.HasPrefix(s, "WARNINGS"):
			st, msg = model.CheckWarn, fmt.Sprintf("dernière sauvegarde de %s du %s avec avertissements (%s)", name, when, s)
		default:
			st, msg = model.CheckCrit, fmt.Sprintf("dernière sauvegarde de %s du %s en échec : %s", name, when, model.Truncate(s, 200))
		}
		kv := p.kvs("datastore", store, "backup_type", typ, "backup_id", model.Truncate(bid, 100))
		if ns != "" {
			kv = append(kv, "pbs_namespace", model.Truncate(ns, 100))
		}
		out = append(out, model.Check{TS: ts, Name: "proxmox_backup.backup", Status: st, Message: msg, Tags: tags(kv...)})
	}
	return out, nil
}

// pbsWorkerID : « magasin:[espace/]type/identifiant » d'une tâche de sauvegarde.
func pbsWorkerID(id string) (store, ns, typ, bid string) {
	store, rest, _ := strings.Cut(id, ":")
	parts := strings.Split(rest, "/")
	if len(parts) >= 2 {
		typ, bid = parts[len(parts)-2], parts[len(parts)-1]
		ns = strings.Join(parts[:len(parts)-2], "/")
	} else {
		bid = rest
	}
	return store, ns, typ, bid
}

// snapshots : états de vérification des instantanés de chaque magasin (tous espaces de noms).
func (p *PBS) snapshots(ctx context.Context, usage []pbsUsage) map[string]pbsSnapStats {
	out := map[string]pbsSnapStats{}
	for i, u := range usage {
		if i >= pbsMaxStores || u.Error != "" {
			continue
		}
		var s pbsSnapStats
		ok := false
		for _, ns := range p.namespaces(ctx, u.Store) {
			var list []struct {
				Verification *struct {
					State string `json:"state"`
				} `json:"verification"`
			}
			if p.get(ctx, "/admin/datastore/"+url.PathEscape(u.Store)+"/snapshots"+nsQuery(ns), &list) != nil {
				continue
			}
			ok = true
			for _, sn := range list {
				s.total++
				switch {
				case sn.Verification == nil:
					s.unverified++
				case sn.Verification.State == "failed":
					s.failed++
				}
			}
		}
		if ok {
			out[u.Store] = s
		}
	}
	return out
}

// node : processeur, mémoire, disque système et durée de service du serveur de sauvegarde.
func (p *PBS) node(ctx context.Context, b *model.SeriesBuilder) {
	var st struct {
		CPU    float64 `json:"cpu"`
		Uptime float64 `json:"uptime"`
		Memory struct {
			Used  float64 `json:"used"`
			Total float64 `json:"total"`
		} `json:"memory"`
		Root struct {
			Used  float64 `json:"used"`
			Total float64 `json:"total"`
			Avail float64 `json:"avail"`
		} `json:"root"`
		Load []float64 `json:"loadavg"`
	}
	if p.get(ctx, "/nodes/localhost/status", &st) != nil {
		return
	}
	kv := p.where()
	b.Gauge("proxmox_backup.node.cpu", "percent", round1(100*st.CPU), kv...)
	b.Gauge("proxmox_backup.node.uptime", "second", st.Uptime, kv...)
	b.Gauge("proxmox_backup.node.memory.used", "byte", st.Memory.Used, kv...)
	b.Gauge("proxmox_backup.node.memory.total", "byte", st.Memory.Total, kv...)
	b.Gauge("proxmox_backup.node.root.used", "byte", st.Root.Used, kv...)
	b.Gauge("proxmox_backup.node.root.total", "byte", st.Root.Total, kv...)
	if len(st.Load) > 0 {
		b.Gauge("proxmox_backup.node.load.1", "", st.Load[0], kv...)
	}
}
