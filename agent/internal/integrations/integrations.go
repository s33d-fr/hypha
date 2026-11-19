// Package integrations relève les métriques des logiciels de l'hôte, sous des noms
// compatibles Datadog : PostgreSQL (et ses requêtes, DBM), HAProxy et nginx à chaque
// intervalle de l'agent ; Apache, BIND, Postfix, Dovecot, OpenVPN, fail2ban, certificats,
// SMART, capteurs, horloge, VirtualBox, timers systemd, mises à jour, IIS et SQL Server
// à leur propre cadence (docs/CONTRAT-04.md § 31).
//
// Chaque intégration rend son état pour status.collectors : « ok », « absent » (logiciel
// non détecté en mode auto), « disabled », ou « erreur : … ». En mode auto, une intégration
// ne s'active qu'après une première lecture réussie ; elle ne casse jamais l'agent si
// l'outil manque, et la détection d'un logiciel absent n'est retentée que toutes les 10 min.
package integrations

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
	"s33d.fr/hypha/agent/internal/updates"
)

type integration interface {
	name() string
	collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string)
	close()
}

// periodic : intégration relevée moins souvent que l'intervalle de l'agent.
type periodic interface {
	period() time.Duration
}

// logObserver : intégration qui lit les journaux collectés (postfix).
type logObserver interface {
	observe(l *model.Log)
}

// Deps : ce que les intégrations demandent à l'agent.
type Deps struct {
	Log       *slog.Logger
	SetStatus func(name, state string)
	// Post envoie un corps hors métriques et contrôles (DBM).
	Post    func(route string, fields map[string]any)
	Updates *updates.Checker

	// § 34-35 : identité de l'agent, pour publier les ressources des connecteurs sous la leur.
	AgentID      string
	AgentVersion string
	Tags         map[string]string
}

// Runner fait tourner les intégrations configurées.
type Runner struct {
	list      []integration
	setStatus func(name, state string)

	mu    sync.Mutex
	next  map[string]time.Time
	state map[string]string
}

// New prépare les intégrations.
func New(cfg config.IntegrationsConfig, d Deps) *Runner {
	if d.Post == nil {
		d.Post = func(string, map[string]any) {}
	}
	if d.Updates == nil {
		d.Updates = &updates.Checker{}
	}
	list := []integration{
		newPostgres(cfg.Postgres, d), newHAProxy(cfg.HAProxy), newNginx(cfg.Nginx),
		newApache(cfg.Apache), newBind(cfg.Bind), newPostfix(cfg.Postfix), newDovecot(cfg.Dovecot),
		newOpenVPN(cfg.OpenVPN), newFail2ban(cfg.Fail2ban), newX509(cfg.X509), newSMART(cfg.SMART),
		newHwmon(cfg.Hwmon), newNTP(cfg.NTP), newVirtualBox(cfg.VirtualBox),
		newSystemdTimers(cfg.SystemdTimers), newUpdatesIntegration(cfg.Updates, d.Updates), newRspamd(cfg.Rspamd),
		newVSphere(cfg.VSphere, d),
		// § 50 : entrée principale nommée = étiquetée comme ses instances.
		named("ovhcloud", cfg.OVHCloud.Name, d, func(d Deps) integration { return newOVHCloud(cfg.OVHCloud, d) }),
		named("mysql", cfg.MySQL.Name, d, func(Deps) integration { return newMySQL(cfg.MySQL) }),
		named("redis", cfg.Redis.Name, d, func(Deps) integration { return newRedis(cfg.Redis) }),
		named("memcached", cfg.Memcached.Name, d, func(Deps) integration { return newMemcached(cfg.Memcached) }),
		named("rabbitmq", cfg.RabbitMQ.Name, d, func(Deps) integration { return newRabbitMQ(cfg.RabbitMQ) }),
		named("mongodb", cfg.MongoDB.Name, d, func(Deps) integration { return newMongoDB(cfg.MongoDB) }),
		named("opensearch", cfg.OpenSearch.Name, d, func(Deps) integration { return newOpenSearch(cfg.OpenSearch) }),
		named("kubernetes", cfg.Kubernetes.Name, d, func(d Deps) integration { return newKubernetes(cfg.Kubernetes, d) }),
		named("libvirt", cfg.Libvirt.Name, d, func(Deps) integration { return newLibvirt(cfg.Libvirt) }),
		named("proxmox", cfg.Proxmox.Name, d, func(d Deps) integration { return newProxmox(cfg.Proxmox, d) }),
		named("aws", cfg.AWS.Name, d, func(d Deps) integration { return newAWS(cfg.AWS, d) }),
		named("scaleway", cfg.Scaleway.Name, d, func(d Deps) integration { return newScaleway(cfg.Scaleway, d) }),
		named("azure", cfg.Azure.Name, d, func(d Deps) integration { return newAzure(cfg.Azure, d) }),
		named("gcp", cfg.GCP.Name, d, func(d Deps) integration { return newGCP(cfg.GCP, d) }),
	}
	list = append(list, instanceIntegrations(cfg, d)...)
	list = append(list, platformIntegrations(cfg)...)
	return &Runner{list: list, setStatus: d.SetStatus, next: map[string]time.Time{}, state: map[string]string{}}
}

// Collect fait un cycle des intégrations dues ; une intégration en panne ne gêne pas les
// autres (chacune borne ses propres délais).
func (r *Runner) Collect(ctx context.Context, now time.Time, tags model.TagFunc) ([]model.Series, []model.Check) {
	b := model.NewSeriesBuilder(model.Ms(now), tags)
	var checks []model.Check
	for _, it := range r.list {
		if p, ok := it.(periodic); ok {
			r.mu.Lock()
			due := !now.Before(r.next[it.name()])
			if due {
				r.next[it.name()] = now.Add(p.period() - time.Second)
			}
			r.mu.Unlock()
			if !due {
				continue
			}
		}
		c, state := r.safeCollect(ctx, it, now, b, tags)
		checks = append(checks, c...)
		r.mu.Lock()
		r.state[it.name()] = state
		r.mu.Unlock()
		r.setStatus(it.name(), state)
	}
	return b.Series(), checks
}

func (r *Runner) safeCollect(ctx context.Context, it integration, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) (c []model.Check, state string) {
	defer func() {
		if p := recover(); p != nil {
			c, state = nil, "erreur : panique de l'intégration"
		}
	}()
	return it.collect(ctx, now, b, tags)
}

// ResetSchedule rend toutes les intégrations dues au prochain passage (run --once : le relevé
// de rodage ne doit pas repousser les intégrations périodiques).
func (r *Runner) ResetSchedule() {
	r.mu.Lock()
	r.next = map[string]time.Time{}
	r.mu.Unlock()
}

// Active rend les intégrations en service (capacités de l'agent).
func (r *Runner) Active() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []string{}
	for _, it := range r.list {
		st := r.state[it.name()]
		if st == "ok" || strings.HasPrefix(st, "partiel") || strings.HasPrefix(st, "erreur") {
			out = append(out, it.name())
		}
	}
	return out
}

// ObserveLog passe une ligne de journal aux intégrations qui en tirent des métriques.
func (r *Runner) ObserveLog(l *model.Log) {
	for _, it := range r.list {
		if o, ok := it.(logObserver); ok {
			o.observe(l)
		}
	}
}

// Close ferme les connexions gardées ouvertes entre deux cycles.
func (r *Runner) Close() {
	for _, it := range r.list {
		it.close()
	}
}

// rateOf rend (cur − prev) / dt ; faux si le compteur a été remis à zéro (rechargement
// d'HAProxy, réinitialisation des statistiques PostgreSQL).
func rateOf(cur, prev int64, dt float64) (float64, bool) {
	if dt <= 0 || cur < prev {
		return 0, false
	}
	return float64(cur-prev) / dt, true
}

// detector : en mode auto, l'intégration n'est active qu'après une lecture réussie ; tant
// qu'elle ne l'a jamais été, l'essai n'est refait que toutes les 10 minutes.
type detector struct {
	mode    config.Tristate
	seen    bool
	triedAt time.Time
}

// skip dit s'il faut sauter ce cycle, et l'état à rendre alors.
func (d *detector) skip(now time.Time) (bool, string) {
	switch {
	case d.mode == "false":
		return true, "disabled"
	case d.mode == "auto" && !d.seen && !d.triedAt.IsZero() && now.Sub(d.triedAt) < 10*time.Minute:
		return true, "absent"
	}
	d.triedAt = now
	return false, ""
}

// failed : lecture en échec. En mode auto, un logiciel jamais lu reste « absent » sans
// contrôle ; une fois vu, l'échec devient un contrôle CRIT.
func (d *detector) failed(err string) (silent bool, state string) {
	if d.mode == "auto" && !d.seen {
		return true, "absent"
	}
	return false, "erreur : " + err
}

func (d *detector) ok() { d.seen = true }

func canConnect(name string, ts int64, ok bool, msg string, tags map[string]string) model.Check {
	st := model.CheckOK
	if !ok {
		st = model.CheckCrit
	}
	return model.Check{TS: ts, Name: name + ".can_connect", Status: st, Message: msg, Tags: tags}
}
