package openmetrics

import (
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"s33d.fr/hypha/agent/internal/omprofiles"
)

// Profils de la vague 2 : chaque jeu d'essai (testdata/vague2/<profil>.prom) est un extrait d'une
// exposition relevée sur le vrai logiciel, en conteneur dans un environnement d'essai (versions dans la
// documentation ; adresses des pods remplacées par des adresses de documentation, RFC 5737). Le
// second relevé ajoute 10 à chaque valeur, pour que les compteurs et les histogrammes donnent
// leurs débits.

// bump ajoute d à chaque valeur finie d'une exposition texte.
func bump(text string, d float64) string {
	var b strings.Builder
	for _, line := range strings.Split(text, "\n") {
		if line == "" || line[0] == '#' {
			b.WriteString(line + "\n")
			continue
		}
		rest := line
		head := ""
		if i := strings.LastIndexByte(line, '}'); i >= 0 {
			head, rest = line[:i+1], line[i+1:]
		} else if i := strings.IndexByte(line, ' '); i >= 0 {
			head, rest = line[:i], line[i:]
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			b.WriteString(line + "\n")
			continue
		}
		v, err := strconv.ParseFloat(f[0], 64)
		if err == nil && !math.IsNaN(v) && !math.IsInf(v, 0) {
			f[0] = strconv.FormatFloat(v+d, 'g', -1, 64)
		}
		b.WriteString(head + " " + strings.Join(f, " ") + "\n")
	}
	return b.String()
}

func vague2Points(t *testing.T, profile, file string) map[string][]point {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "vague2", file))
	if err != nil {
		t.Fatal(err)
	}
	cfg := profileInstance(t, "integrations:\n  openmetrics:\n    - profile: "+profile+"\n")
	c := newConverter(cfg)
	fams, bad, err := Parse(strings.NewReader(string(raw)))
	if err != nil || bad > 0 || len(fams) == 0 {
		t.Fatalf("%s : exposition illisible (%d lignes refusées, %v)", file, bad, err)
	}
	t0 := time.Unix(1_800_000_000, 0)
	c.Convert(fams, t0)
	fams, _, _ = Parse(strings.NewReader(bump(string(raw), 10)))
	pts := c.Convert(fams, t0.Add(10*time.Second))
	by := map[string][]point{}
	for _, p := range pts {
		by[p.name] = append(by[p.name], p)
	}
	return by
}

func TestVague2ProfilesOnRealExpositions(t *testing.T) {
	cases := []struct {
		profile, file string
		want          []string
	}{
		{"consul", "consul.prom", []string{"consul.server.is_leader", "consul.autopilot.healthy", "consul.catalog.nodes",
			"consul.raft.apply", "consul.raft.commit_time.p99", "consul.http.request.count", "consul.runtime.goroutines"}},
		{"vault", "vault.prom", []string{"vault.core.unsealed", "vault.core.active", "vault.core.check_token.sum",
			"vault.core.handle_request.count", "vault.expire.num_leases", "vault.barrier.put.count", "vault.runtime.alloc"}},
		{"etcd", "etcd.prom", []string{"etcd.server.has_leader", "etcd.server.leader_changes", "etcd.db.size",
			"etcd.disk.wal_fsync.p99", "etcd.disk.backend_commit.count", "etcd.mvcc.put", "etcd.grpc.handled"}},
		{"coredns", "coredns.prom", []string{"coredns.requests", "coredns.responses", "coredns.request_duration.p95",
			"coredns.cache.hits", "coredns.cache.misses", "coredns.panics"}},
		{"traefik", "traefik.prom", []string{"traefik.entrypoint.requests", "traefik.entrypoint.request_duration.p95",
			"traefik.service.requests", "traefik.service.server_up", "traefik.open_connections", "traefik.config.reloads"}},
		{"envoy", "envoy.prom", []string{"envoy.server.live", "envoy.cluster.upstream.rq_total", "envoy.cluster.upstream.rq_xx",
			"envoy.cluster.upstream.rq_time.p99", "envoy.cluster.membership.healthy", "envoy.http.downstream.rq_xx"}},
		{"kong", "kong.prom", []string{"kong.http.requests", "kong.request_latency.p95", "kong.upstream_latency.count",
			"kong.bandwidth", "kong.nginx.connections", "kong.datastore.reachable"}},
		{"cockroachdb", "cockroachdb.prom", []string{"cockroachdb.liveness.live_nodes", "cockroachdb.ranges.unavailable",
			"cockroachdb.ranges.underreplicated", "cockroachdb.capacity.available", "cockroachdb.sql.select",
			"cockroachdb.sql.service_latency.p99", "cockroachdb.sql.conns"}},
		{"n8n", "n8n.prom", []string{"n8n.workflows.active", "n8n.workflow.started", "n8n.workflow.failed",
			"n8n.workflow.execution_duration.p95", "n8n.process.resident_memory"}},
		{"temporal", "temporal.prom", []string{"temporal.service.requests", "temporal.service.latency.p99",
			"temporal.persistence.requests", "temporal.persistence.latency.p95", "temporal.task_queue.backlog",
			"temporal.workflow.terminate"}},
		{"celery", "celery.prom", []string{"celery.events", "celery.task.runtime.p95", "celery.worker.online",
			"celery.worker.executing_tasks"}},
		{"tomcat", "tomcat.prom", []string{"tomcat.requests", "tomcat.errors", "tomcat.threads.busy", "tomcat.sessions.active",
			"tomcat.jvm.memory.used", "tomcat.jvm.gc.collection.count", "tomcat.jmx.scrape_error"}},
		{"kafka", "kafka.prom", []string{"kafka.broker.messages_in", "kafka.broker.bytes_in",
			"kafka.replication.under_replicated_partitions", "kafka.controller.active", "kafka.controller.offline_partitions",
			"kafka.request.total_time.p99", "kafka.request.count", "kafka.jvm.memory.used"}},
		{"activemq", "activemq.prom", []string{"activemq.broker.memory_usage", "activemq.broker.store_usage",
			"activemq.destination.size", "activemq.destination.enqueued", "activemq.destination.consumers"}},
		{"cassandra", "cassandra.prom", []string{"cassandra.client_request.count", "cassandra.client_request.latency.p99",
			"cassandra.client_request.timeouts", "cassandra.storage.load", "cassandra.compaction.pending_tasks",
			"cassandra.thread_pools.pending_tasks", "cassandra.dropped_messages"}},
		{"solr", "solr.prom", []string{"solr.core.requests", "solr.core.request_errors", "solr.core.request_time.p95",
			"solr.core.num_docs", "solr.core.index_size_mb", "solr.jvm.memory.used"}},
		{"solr", "solr9.prom", []string{"solr.metrics.core.requests", "solr.metrics.core.request_time.p99",
			"solr.metrics.core.index_size", "solr.metrics.jetty.responses"}},
		{"wildfly", "wildfly.prom", []string{"wildfly.undertow.requests", "wildfly.undertow.errors",
			"wildfly.datasource.pool.in_use", "wildfly.transactions.committed", "wildfly.jvm.memory.heap_used"}},
		{"pulsar", "pulsar.prom", []string{"pulsar.broker.topics", "pulsar.broker.msg_backlog", "pulsar.broker.rate_in",
			"pulsar.broker.publish_latency.p99", "pulsar.namespace.msg_backlog", "pulsar.jvm.memory.used"}},
		{"scylla", "scylla.prom", []string{"scylla.reactor.utilization", "scylla.coordinator.read_latency.p99",
			"scylla.coordinator.write_latency.count", "scylla.cql.requests_served", "scylla.cache.row_hits",
			"scylla.database.writes_failed"}},
		{"sonarqube", "sonarqube.prom", []string{"sonarqube.health.web", "sonarqube.health.compute_engine",
			"sonarqube.health.elasticsearch", "sonarqube.compute_engine.pending_tasks", "sonarqube.elasticsearch.disk_usage"}},
		{"gitlab", "gitlab.prom", []string{"gitlab.http.requests", "gitlab.http.request_duration.count", "gitlab.db.pool.busy",
			"gitlab.redis.requests", "gitlab.sli.rails.requests", "gitlab.puma.running", "gitlab.ruby.resident_memory"}},
		{"gitlab_sidekiq", "gitlab_sidekiq.prom", []string{"gitlab_sidekiq.jobs.completion.count", "gitlab_sidekiq.jobs.running",
			"gitlab_sidekiq.concurrency"}},
		{"gitaly", "gitaly.prom", []string{"gitaly.grpc.handled", "gitaly.pack_objects.queued", "gitaly.commands.running"}},
		{"cert_manager", "cert_manager.prom", []string{"cert_manager.certificate.ready", "cert_manager.certificate.expiration_timestamp",
			"cert_manager.clusterissuer.ready", "cert_manager.controller.sync_calls"}},
		{"velero", "velero.prom", []string{"velero.backup.attempts", "velero.backup.validation_failure", "velero.backup.last_status",
			"velero.backup_location.available"}},
		{"argocd", "argocd.prom", []string{"argocd.app.info", "argocd.app.syncs", "argocd.app.reconcile.count",
			"argocd.cluster.connection_status"}},
		{"argocd_repo_server", "argocd_repo_server.prom", []string{"argocd_repo_server.git.requests",
			"argocd_repo_server.git.request_duration.count"}},
		{"linkerd", "linkerd.prom", []string{"linkerd.requests", "linkerd.responses", "linkerd.response_latency.p95",
			"linkerd.tcp.open_connections", "linkerd.identity.cert_expiration_timestamp"}},
		{"keda", "keda.prom", []string{"keda.scaler.errors", "keda.scaled_object.errors", "keda.scaler.active",
			"keda.scaler.metrics_latency"}},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			prof, _ := omprofiles.Get(tc.profile)
			by := vague2Points(t, tc.profile, tc.file)
			for _, w := range tc.want {
				if len(by[w]) == 0 {
					t.Errorf("%s absente ; reçues : %v", w, names(by))
				}
			}
			renamed := map[string]bool{}
			for _, v := range prof.Rename {
				renamed[v] = true
			}
			for n, ps := range by {
				base, ok := strings.CutPrefix(n, prof.Namespace+".")
				if !ok {
					t.Errorf("%s hors de l'espace de noms %s", n, prof.Namespace)
				}
				if i := strings.LastIndexByte(base, '.'); !renamed[base] && i > 0 && suffixRe.MatchString(base[i+1:]) {
					base = base[:i]
				}
				if !renamed[base] {
					t.Errorf("nom Prometheus non renommé : %s", n)
				}
				for _, p := range ps {
					if math.IsNaN(p.value) || math.IsInf(p.value, 0) {
						t.Errorf("%s : valeur %v", n, p.value)
					}
				}
			}
		})
	}
}

// suffixRe : suffixes ajoutés aux histogrammes et aux résumés (.count, .sum, .avg, .p99, .p99_9…).
var suffixRe = regexp.MustCompile(`^(count|sum|avg|p[0-9_]+)$`)

func names(by map[string][]point) []string {
	out := make([]string, 0, len(by))
	for n := range by {
		out = append(out, n)
	}
	return out
}

// Chaque famille retenue par un profil (nom exact) a son nom HYPHA : aucune ne part sous son nom
// Prometheus brut.
func TestProfilesRenameEverySelectedFamily(t *testing.T) {
	for _, name := range omprofiles.Names() {
		p, _ := omprofiles.Get(name)
		for _, m := range p.Metrics {
			if strings.ContainsAny(m, "*?[") || strings.HasPrefix(m, "re:") {
				continue
			}
			_, ok := p.Rename[m]
			if !ok {
				if base, cut := strings.CutSuffix(m, "_total"); cut {
					_, ok = p.Rename[base]
				} else {
					_, ok = p.Rename[m+"_total"]
				}
			}
			if !ok {
				t.Errorf("profil %s : %s sans renommage", name, m)
			}
		}
		if len(p.Rename) > len(p.Metrics) {
			t.Errorf("profil %s : renommages en trop (%d pour %d familles)", name, len(p.Rename), len(p.Metrics))
		}
	}
}

// Envoy publie envoy_cluster_upstream_rq (par code) et envoy_cluster_upstream_rq_total : la
// seconde ne doit pas capturer la première (même nom HYPHA, séries mêlées).
func TestCounterAndTotalFamiliesStayApart(t *testing.T) {
	const expo = `# TYPE envoy_cluster_upstream_rq counter
envoy_cluster_upstream_rq{envoy_response_code="200",envoy_cluster_name="a"} 20
envoy_cluster_upstream_rq{envoy_response_code="404",envoy_cluster_name="a"} 20
# TYPE envoy_cluster_upstream_rq_total counter
envoy_cluster_upstream_rq_total{envoy_cluster_name="a"} 40
`
	cfg := profileInstance(t, "integrations:\n  openmetrics:\n    - profile: envoy\n")
	c := newConverter(cfg)
	fams, _, _ := Parse(strings.NewReader(expo))
	c.Convert(fams, time.Unix(0, 0))
	fams, _, _ = Parse(strings.NewReader(bump(expo, 10)))
	pts := c.Convert(fams, time.Unix(10, 0))
	got := 0
	for _, p := range pts {
		if p.name == "envoy.cluster.upstream.rq_total" {
			got++
			if p.value != 1 {
				t.Fatalf("débit de rq_total : %v (attendu 1/s)", p.value)
			}
		}
	}
	if got != 1 || len(pts) != 1 {
		t.Fatalf("séries : %+v", pts)
	}
}

// Chemin de profil avec paramètres (Consul, Vault) : une url sans chemin les reçoit tels quels.
func TestProfilePathWithQuery(t *testing.T) {
	cfg := profileInstance(t, "integrations:\n  openmetrics:\n    - {profile: consul, url: \"http://consul.lan:8500\"}\n")
	if cfg.URL != "http://consul.lan:8500/v1/agent/metrics?format=prometheus" {
		t.Fatalf("url : %s", cfg.URL)
	}
	cfg = profileInstance(t, "integrations:\n  openmetrics:\n    - profile: vault\n")
	if cfg.URL != "https://127.0.0.1:8200/v1/sys/metrics?format=prometheus" {
		t.Fatalf("url par défaut : %s", cfg.URL)
	}
}
