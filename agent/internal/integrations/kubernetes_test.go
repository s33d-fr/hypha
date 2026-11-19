package integrations

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

func TestKubeQuantity(t *testing.T) {
	for in, want := range map[string]float64{"3800m": 3.8, "8037372Ki": 8037372 * 1024, "1.5G": 1.5e9, "129e6": 129e6, "2": 2, "256Mi": 256 << 20} {
		if got, ok := kubeQuantity(in); !ok || math.Abs(got-want) > 1e-6 {
			t.Fatalf("%s : %v %v", in, got, ok)
		}
	}
	if _, ok := kubeQuantity("n'importe"); ok {
		t.Fatal("quantité invalide acceptée")
	}
}

func TestKubernetesCluster(t *testing.T) {
	var mu sync.Mutex
	pass, forbidSTS := 0, false
	now := time.Now().UTC()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer bon-jeton" {
			w.WriteHeader(401)
			return
		}
		mu.Lock()
		p, forbid := pass, forbidSTS
		mu.Unlock()
		switch r.URL.Path {
		case "/version":
			fmt.Fprint(w, `{"gitVersion":"v1.36.4"}`)
		case "/api/v1/nodes":
			fmt.Fprint(w, `{"items":[
				{"metadata":{"name":"n1"},"status":{"conditions":[{"type":"Ready","status":"True"}],"allocatable":{"cpu":"4","memory":"8Gi","pods":"110"},"nodeInfo":{"kubeletVersion":"v1.36.4"}}},
				{"metadata":{"name":"n2"},"spec":{"unschedulable":true},"status":{"conditions":[{"type":"Ready","status":"Unknown","reason":"NodeStatusUnknown"},{"type":"DiskPressure","status":"True"}],"allocatable":{"cpu":"2"}}}],
				"metadata":{}}`)
		case "/api/v1/pods":
			restarts := 3 + 2*p
			fmt.Fprintf(w, `{"items":[
				{"metadata":{"name":"web-7d9f-abcde","namespace":"app","uid":"u1","labels":{"pod-template-hash":"7d9f"},"ownerReferences":[{"kind":"ReplicaSet","name":"web-7d9f"}],"creationTimestamp":"2026-11-01T00:00:00Z"},
				 "spec":{"nodeName":"n1","containers":[{"resources":{"requests":{"cpu":"500m","memory":"256Mi"}}}]},
				 "status":{"phase":"Running","containerStatuses":[{"name":"web","restartCount":%d,"state":{"waiting":{"reason":"CrashLoopBackOff"}}}]}},
				{"metadata":{"name":"kube-apiserver-n1","namespace":"kube-system","uid":"u2","ownerReferences":[{"kind":"Node","name":"n1"}],"creationTimestamp":"2026-11-01T00:00:00Z"},
				 "spec":{"nodeName":"n1","containers":[{"resources":{"requests":{"cpu":"250m"}}}]},
				 "status":{"phase":"Running","conditions":[{"type":"Ready","status":"True"}],"containerStatuses":[{"name":"kube-apiserver","ready":true,"restartCount":0,"state":{}}]}},
				{"metadata":{"name":"lourd","namespace":"app","uid":"u3","creationTimestamp":"2026-11-01T00:00:00Z"},
				 "status":{"phase":"Pending","conditions":[{"type":"PodScheduled","status":"False","reason":"Unschedulable"}]}}],"metadata":{}}`, restarts)
		case "/apis/apps/v1/deployments":
			fmt.Fprint(w, `{"items":[{"metadata":{"name":"web","namespace":"app"},"spec":{"replicas":2},"status":{"availableReplicas":1}}]}`)
		case "/apis/apps/v1/statefulsets":
			if forbid {
				w.WriteHeader(403)
				return
			}
			fmt.Fprint(w, `{"items":[]}`)
		case "/apis/apps/v1/daemonsets":
			fmt.Fprint(w, `{"items":[{"metadata":{"name":"proxy","namespace":"kube-system"},"status":{"desiredNumberScheduled":2,"numberAvailable":2}}]}`)
		case "/api/v1/events":
			if r.URL.Query().Get("fieldSelector") != "type=Warning" {
				w.WriteHeader(400)
				return
			}
			items := `{"metadata":{"uid":"e1","namespace":"app"},"involvedObject":{"kind":"Pod","name":"web-7d9f-abcde","namespace":"app"},"reason":"BackOff","message":"Back-off restarting failed container","count":4,"lastTimestamp":"2026-11-18T20:00:00Z"}`
			if p > 0 {
				items += fmt.Sprintf(`,{"metadata":{"uid":"e2","namespace":"app"},"involvedObject":{"kind":"Pod","name":"lourd","namespace":"app"},"reason":"FailedScheduling","message":"0/2 nodes are available","eventTime":"%s"}`, now.Format(time.RFC3339Nano))
			}
			fmt.Fprintf(w, `{"items":[%s]}`, items)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	tok := filepath.Join(t.TempDir(), "jeton")
	os.WriteFile(tok, []byte("bon-jeton\n"), 0o600)
	var posted []model.Event
	k := newKubernetes(config.KubernetesConfig{Enabled: "auto", Cluster: "essai", URL: srv.URL, TokenFile: tok, Interval: config.Duration(time.Minute)},
		Deps{Post: func(route string, f map[string]any) {
			if route == routeEvents {
				posted = append(posted, f["events"].([]model.Event)...)
			}
		}})
	var checks []model.Check
	var b *model.SeriesBuilder
	var st string
	for i := 0; i < 2; i++ {
		mu.Lock()
		pass = i
		mu.Unlock()
		b = model.NewSeriesBuilder(0, testTags)
		checks, st = k.collect(context.Background(), time.Now().Add(time.Duration(i)*61*time.Second), b, testTags)
	}
	got := map[string]model.Check{}
	for _, c := range checks {
		got[c.Name+"/"+c.Tags["kube_node"]+c.Tags["kube_kind"]+":"+c.Tags["kube_workload"]] = c
	}
	expect := func(key string, status int, text string) {
		t.Helper()
		c, ok := got[key]
		if !ok || c.Status != status || !strings.Contains(c.Message, text) {
			t.Fatalf("%s : %+v (attendu %d, %q) ; tous : %v", key, c, status, text, got)
		}
	}
	if st != "ok" {
		t.Fatalf("état : %q", st)
	}
	expect("kubernetes.can_connect/:", model.CheckOK, "Kubernetes v1.36.4 joignable (2 nœud(s), 3 pod(s))")
	expect("kubernetes.node/n1:", model.CheckOK, "n1 prêt")
	expect("kubernetes.node/n2:", model.CheckCrit, "kubelet ne répond plus ; DiskPressure ; fermé aux nouveaux pods")
	expect("kubernetes.workload/deployment:web", model.CheckWarn, "1/2 disponible(s) ; web-7d9f-abcde : CrashLoopBackOff")
	expect("kubernetes.workload/static_pod:kube-apiserver-n1", model.CheckOK, "1/1")
	expect("kubernetes.workload/daemonset:proxy", model.CheckOK, "2/2")
	series := map[string]float64{}
	for _, s := range b.Series() {
		series[s.Metric+"/"+s.Tags["kube_node"]+s.Tags["kube_namespace"]] = s.Points[0].Value
	}
	for key, want := range map[string]float64{
		"kubernetes.node.cpu.requests_pct/n1": 18.8, "kubernetes.node.mem.requests_pct/n1": 3.1, "kubernetes.node.pods/n1": 2,
		"kubernetes.containers.restarts/app": 2, "kubernetes.containers.restarts/kube-system": 0, "kubernetes.pods.unschedulable/": 1,
		"kubernetes.pods.pending/app": 1, "kubernetes.nodes.ready/": 1, "kubernetes.workload.available/app": 1,
	} {
		if v, ok := series[key]; !ok || v != want {
			t.Fatalf("%s = %v (attendu %v) ; %v", key, v, want, series)
		}
	}
	if len(posted) != 1 || !strings.Contains(posted[0].Title, "pod app/lourd : FailedScheduling") || posted[0].Tags["kube_cluster"] != "essai" {
		t.Fatalf("événements : %+v", posted)
	}
	// Droit manquant : lecture partielle, dite sur le contrôle de connexion.
	mu.Lock()
	forbidSTS = true
	mu.Unlock()
	checks, st = k.collect(context.Background(), time.Now().Add(3*time.Minute), model.NewSeriesBuilder(0, testTags), testTags)
	if c := byName(checks)["kubernetes.can_connect"]; !strings.HasPrefix(st, "partiel") || c.Status != model.CheckWarn || !strings.Contains(c.Message, "statefulsets : accès refusé (HTTP 403)") {
		t.Fatalf("403 : %q %+v", st, c)
	}
	// Jeton refusé : CRIT, sans le jeton dans le message.
	os.WriteFile(tok, []byte("mauvais-jeton"), 0o600)
	checks, _ = k.collect(context.Background(), time.Now().Add(4*time.Minute), model.NewSeriesBuilder(0, testTags), testTags)
	if len(checks) != 1 || checks[0].Status != model.CheckCrit || !strings.Contains(checks[0].Message, "401") || strings.Contains(checks[0].Message, "mauvais") {
		t.Fatalf("401 : %+v", checks)
	}
}

func TestKubernetesAbsentWithoutConf(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	k := newKubernetes(config.KubernetesConfig{Enabled: "auto", Interval: config.Duration(time.Minute)}, Deps{})
	if checks, st := k.collect(context.Background(), time.Now(), model.NewSeriesBuilder(0, testTags), testTags); len(checks) != 0 || st != "absent" {
		t.Fatalf("sans conf : %q %+v", st, checks)
	}
}

// Essai contre une vraie grappe, lancé à la main sur la machine qui garde le jeton :
// KUBE_REEL=1, KUBE_URL, KUBE_TOKEN_FILE, KUBE_CA_FILE (lecture seule).
func TestKubernetesReel(t *testing.T) {
	if os.Getenv("KUBE_REEL") != "1" {
		t.Skip("KUBE_REEL=1 pour interroger une vraie grappe")
	}
	var events []model.Event
	k := newKubernetes(config.KubernetesConfig{Enabled: "true", Cluster: "s33d", URL: os.Getenv("KUBE_URL"), TokenFile: os.Getenv("KUBE_TOKEN_FILE"),
		CAFile: os.Getenv("KUBE_CA_FILE"), Interval: config.Duration(time.Minute)},
		Deps{Post: func(_ string, f map[string]any) { events = append(events, f["events"].([]model.Event)...) }})
	var checks []model.Check
	var b *model.SeriesBuilder
	var st string
	for i := 0; i < 2; i++ {
		b = model.NewSeriesBuilder(0, testTags)
		checks, st = k.collect(context.Background(), time.Now().Add(time.Duration(i)*61*time.Second), b, testTags)
		time.Sleep(2 * time.Second)
	}
	t.Logf("== %s", st)
	for _, c := range checks {
		t.Logf("[%d] %s : %s", c.Status, c.Name, c.Message)
	}
	for _, s := range b.Series() {
		t.Logf("série %s %s%s%s = %v", s.Metric, s.Tags["kube_node"], s.Tags["kube_namespace"], s.Tags["kube_workload"], s.Points[0].Value)
	}
	t.Logf("%d événement(s) relayé(s)", len(events))
}
