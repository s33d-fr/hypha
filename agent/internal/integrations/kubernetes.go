package integrations

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
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

// ── Kubernetes (§ 42) : l'API de la grappe, en lecture seule ────────────────────────────────
//
// Un agent par grappe (celui du plan de contrôle, ou un pod au compte de service lié au rôle de
// lecture) lit les nœuds, les pods, les déploiements, statefulsets et daemonsets, et les
// événements Warning. Contrôles : nœud prêt (pressions, cordon), charge de travail disponible
// (pods en CrashLoopBackOff cités), pods statiques du plan de contrôle. Mesures : pods par espace
// de noms, redémarrages de conteneurs, réservations CPU et mémoire par nœud. Sans metrics-server
// requis.

const (
	kubeSADir     = "/var/run/secrets/kubernetes.io/serviceaccount"
	kubeMaxEvents = 200
)

// Attentes de conteneur qui ne se résolvent pas seules.
var kubeBadWaiting = map[string]bool{
	"CrashLoopBackOff": true, "ImagePullBackOff": true, "ErrImagePull": true, "InvalidImageName": true,
	"CreateContainerConfigError": true, "CreateContainerError": true, "RunContainerError": true,
}

type Kubernetes struct {
	cfg     config.KubernetesConfig
	det     detector
	url     string
	token   string // fichier du jeton (relu à chaque passage) ou jeton de la conf
	tokFile bool
	client  *http.Client
	initErr string
	post    func(route string, fields map[string]any)

	restarts map[string]float64 // uid du pod → redémarrages cumulés au passage précédent
	events   map[string]bool    // événements déjà vus (uid, compte, date)
}

func newKubernetes(cfg config.KubernetesConfig, d Deps) *Kubernetes {
	k := &Kubernetes{cfg: cfg, post: d.Post}
	mode := cfg.Enabled
	caFile := cfg.CAFile
	switch {
	case cfg.URL != "":
		k.url = strings.TrimRight(cfg.URL, "/")
		k.token, k.tokFile = cfg.Token, false
		if cfg.Token == "" {
			k.token, k.tokFile = cfg.TokenFile, true
		}
		if mode == "auto" {
			mode = "true"
		}
	case os.Getenv("KUBERNETES_SERVICE_HOST") != "" && fileExists(kubeSADir+"/token"):
		// Dans un pod : le compte de service monté. En auto, un compte sans droit reste muet.
		k.url = "https://" + net.JoinHostPort(os.Getenv("KUBERNETES_SERVICE_HOST"), firstNonEmpty(os.Getenv("KUBERNETES_SERVICE_PORT"), "443"))
		k.token, k.tokFile = kubeSADir+"/token", true
		if caFile == "" {
			caFile = kubeSADir + "/ca.crt"
		}
	}
	k.det = detector{mode: mode}
	tc := &tls.Config{InsecureSkipVerify: cfg.Insecure} //nolint:gosec // choisi dans la conf
	if caFile != "" && !cfg.Insecure {
		pem, err := os.ReadFile(caFile)
		pool := x509.NewCertPool()
		if err != nil || !pool.AppendCertsFromPEM(pem) {
			k.initErr = "autorité de certification illisible : " + caFile
		}
		tc.RootCAs = pool
	}
	k.client = &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{TLSClientConfig: tc}}
	return k
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func (k *Kubernetes) name() string { return "kubernetes" }
func (k *Kubernetes) period() time.Duration {
	if d := k.cfg.Interval.D(); d > 0 {
		return d
	}
	return time.Minute
}
func (k *Kubernetes) close() {}

var errKubeAuth = errors.New("jeton refusé (HTTP 401)")

func (k *Kubernetes) bearer() (string, error) {
	if !k.tokFile {
		return k.token, nil
	}
	b, err := os.ReadFile(k.token)
	if err != nil {
		return "", fmt.Errorf("jeton illisible : %s", k.token)
	}
	return strings.TrimSpace(string(b)), nil
}

func (k *Kubernetes) get(ctx context.Context, tok, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, k.url+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "hypha-agent")
	resp, err := k.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return errKubeAuth
	case resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%s : accès refusé (HTTP 403), droits du compte à revoir", strings.SplitN(path, "?", 2)[0])
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("%s : HTTP %d", strings.SplitN(path, "?", 2)[0], resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(out)
}

// kubeList : liste complète, page par page (limit/continue).
func kubeList[T any](ctx context.Context, k *Kubernetes, tok, path string) ([]T, error) {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	var all []T
	cont := ""
	for page := 0; page < 100; page++ {
		p := path + sep + "limit=500"
		if cont != "" {
			p += "&continue=" + url.QueryEscape(cont)
		}
		var r struct {
			Items    []T `json:"items"`
			Metadata struct {
				Continue string `json:"continue"`
			} `json:"metadata"`
		}
		if err := k.get(ctx, tok, p, &r); err != nil {
			return all, err
		}
		all = append(all, r.Items...)
		if r.Metadata.Continue == "" {
			break
		}
		cont = r.Metadata.Continue
	}
	return all, nil
}

type kubeMeta struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace"`
	UID       string            `json:"uid"`
	Labels    map[string]string `json:"labels"`
	Owners    []struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
	} `json:"ownerReferences"`
	Created time.Time `json:"creationTimestamp"`
}

type kubeCond struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

type kubeNode struct {
	Metadata kubeMeta `json:"metadata"`
	Spec     struct {
		Unschedulable bool `json:"unschedulable"`
	} `json:"spec"`
	Status struct {
		Conditions  []kubeCond        `json:"conditions"`
		Allocatable map[string]string `json:"allocatable"`
		NodeInfo    struct {
			Kubelet string `json:"kubeletVersion"`
		} `json:"nodeInfo"`
	} `json:"status"`
}

type kubeContainerStatus struct {
	Name     string  `json:"name"`
	Ready    bool    `json:"ready"`
	Restarts float64 `json:"restartCount"`
	State    struct {
		Waiting *struct {
			Reason string `json:"reason"`
		} `json:"waiting"`
	} `json:"state"`
}

type kubePod struct {
	Metadata kubeMeta `json:"metadata"`
	Spec     struct {
		NodeName   string `json:"nodeName"`
		Containers []struct {
			Resources struct {
				Requests map[string]string `json:"requests"`
			} `json:"resources"`
		} `json:"containers"`
	} `json:"spec"`
	Status struct {
		Phase      string                `json:"phase"`
		Conditions []kubeCond            `json:"conditions"`
		Containers []kubeContainerStatus `json:"containerStatuses"`
		Inits      []kubeContainerStatus `json:"initContainerStatuses"`
	} `json:"status"`
}

type kubeDeployment struct {
	Metadata kubeMeta `json:"metadata"`
	Spec     struct {
		Replicas *float64 `json:"replicas"`
	} `json:"spec"`
	Status struct {
		Available  float64    `json:"availableReplicas"`
		Conditions []kubeCond `json:"conditions"`
	} `json:"status"`
}

type kubeStatefulSet struct {
	Metadata kubeMeta `json:"metadata"`
	Spec     struct {
		Replicas *float64 `json:"replicas"`
	} `json:"spec"`
	Status struct {
		Available *float64 `json:"availableReplicas"`
		Ready     float64  `json:"readyReplicas"`
	} `json:"status"`
}

type kubeDaemonSet struct {
	Metadata kubeMeta `json:"metadata"`
	Status   struct {
		Desired   float64 `json:"desiredNumberScheduled"`
		Available float64 `json:"numberAvailable"`
	} `json:"status"`
}

type kubeEvent struct {
	Metadata kubeMeta `json:"metadata"`
	Involved struct {
		Kind      string `json:"kind"`
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"involvedObject"`
	Reason    string    `json:"reason"`
	Message   string    `json:"message"`
	Count     float64   `json:"count"`
	Last      time.Time `json:"lastTimestamp"`
	EventTime time.Time `json:"eventTime"`
	Series    *struct {
		Count float64   `json:"count"`
		Last  time.Time `json:"lastObservedTime"`
	} `json:"series"`
}

func (e kubeEvent) when() time.Time {
	switch {
	case e.Series != nil && !e.Series.Last.IsZero():
		return e.Series.Last
	case !e.Last.IsZero():
		return e.Last
	case !e.EventTime.IsZero():
		return e.EventTime
	}
	return e.Metadata.Created
}

// kubeQuantity : quantité Kubernetes (« 3800m », « 8037372Ki », « 1.5G », « 129e6 »).
func kubeQuantity(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f, true
	}
	mult := map[string]float64{"Ki": 1 << 10, "Mi": 1 << 20, "Gi": 1 << 30, "Ti": 1 << 40, "Pi": 1 << 50, "Ei": 1 << 60}
	if len(s) > 2 {
		if m, ok := mult[s[len(s)-2:]]; ok {
			f, err := strconv.ParseFloat(s[:len(s)-2], 64)
			return f * m, err == nil
		}
	}
	dec := map[byte]float64{'n': 1e-9, 'u': 1e-6, 'm': 1e-3, 'k': 1e3, 'M': 1e6, 'G': 1e9, 'T': 1e12, 'P': 1e15, 'E': 1e18}
	if m, ok := dec[s[len(s)-1]]; ok {
		f, err := strconv.ParseFloat(s[:len(s)-1], 64)
		return f * m, err == nil
	}
	return 0, false
}

func condOf(cs []kubeCond, typ string) *kubeCond {
	for i := range cs {
		if cs[i].Type == typ {
			return &cs[i]
		}
	}
	return nil
}

// owner : charge de travail d'un pod (le ReplicaSet d'un déploiement est ramené au déploiement).
func (p kubePod) owner() (kind, name string) {
	if len(p.Metadata.Owners) == 0 {
		return "", ""
	}
	o := p.Metadata.Owners[0]
	switch o.Kind {
	case "ReplicaSet":
		if h := p.Metadata.Labels["pod-template-hash"]; h != "" {
			return "deployment", strings.TrimSuffix(o.Name, "-"+h)
		}
		return "replicaset", o.Name
	case "StatefulSet":
		return "statefulset", o.Name
	case "DaemonSet":
		return "daemonset", o.Name
	case "Node":
		return "static_pod", p.Metadata.Name
	}
	return strings.ToLower(o.Kind), o.Name
}

func (k *Kubernetes) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if k.url == "" && k.cfg.Enabled != "true" {
		if k.cfg.Enabled == "false" {
			return nil, "disabled"
		}
		return nil, "absent"
	}
	if skip, st := k.det.skip(now); skip {
		return nil, st
	}
	ts := model.Ms(now)
	cluster := firstNonEmpty(k.cfg.Cluster, "kubernetes")
	base := []string{"kube_cluster", cluster}
	fail := func(msg string) ([]model.Check, string) {
		if silent, st := k.det.failed(msg); silent {
			return nil, st
		}
		return []model.Check{{TS: ts, Name: "kubernetes.can_connect", Status: model.CheckCrit, Message: msg, Tags: tags(base...)}}, "erreur : " + msg
	}
	switch {
	case k.url == "":
		return fail("url de l'API absente (integrations.kubernetes.url)")
	case k.initErr != "":
		return fail(k.initErr)
	}
	tok, err := k.bearer()
	if err != nil {
		return fail(err.Error())
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	var ver struct {
		GitVersion string `json:"gitVersion"`
	}
	if err := k.get(ctx, tok, "/version", &ver); err != nil {
		return fail(err.Error())
	}
	nodes, err := kubeList[kubeNode](ctx, k, tok, "/api/v1/nodes")
	if err != nil {
		return fail(err.Error())
	}
	k.det.ok()
	var problems []string
	pods, err := kubeList[kubePod](ctx, k, tok, "/api/v1/pods")
	if err != nil {
		problems = append(problems, err.Error())
	}
	checks := []model.Check{}
	checks = append(checks, k.nodeChecks(ts, b, tags, base, nodes, pods)...)
	checks = append(checks, k.podSeries(ts, b, tags, base, pods, err == nil)...)

	// Charges de travail : disponibles sur demandées, et pods bloqués de chacune.
	stuck := map[string][]string{} // namespace/kind/name → « pod : raison »
	for _, p := range pods {
		kind, name := p.owner()
		if kind == "" {
			continue
		}
		for _, cs := range append(append([]kubeContainerStatus{}, p.Status.Inits...), p.Status.Containers...) {
			if cs.State.Waiting != nil && kubeBadWaiting[cs.State.Waiting.Reason] {
				key := p.Metadata.Namespace + "/" + kind + "/" + name
				stuck[key] = append(stuck[key], fmt.Sprintf("%s : %s", p.Metadata.Name, cs.State.Waiting.Reason))
				break
			}
		}
	}
	workload := func(ns, kind, name string, desired, avail float64, extra string, floor int) {
		kv := append(append([]string{}, base...), "kube_namespace", ns, "kube_kind", kind, "kube_workload", name)
		b.Gauge("kubernetes.workload.desired", "pod", desired, kv...)
		b.Gauge("kubernetes.workload.available", "pod", avail, kv...)
		st, msg := model.CheckOK, fmt.Sprintf("%g/%g disponible(s)", avail, desired)
		switch {
		case desired == 0:
			msg = "à l'arrêt (0 réplique demandée)"
		case avail == 0:
			st, msg = model.CheckCrit, fmt.Sprintf("aucune réplique disponible sur %g", desired)
		case avail < desired:
			st = model.CheckWarn
		}
		if s := stuck[ns+"/"+kind+"/"+name]; len(s) > 0 {
			sort.Strings(s)
			if len(s) > 3 {
				s = append(s[:3], fmt.Sprintf("et %d autre(s)", len(s)-3))
			}
			msg += " ; " + strings.Join(s, ", ")
			if st == model.CheckOK {
				st = model.CheckWarn
			}
		}
		if extra != "" {
			msg += " ; " + extra
		}
		if floor > st && st != model.CheckCrit {
			st = floor
		}
		checks = append(checks, model.Check{TS: ts, Name: "kubernetes.workload", Status: st, Message: fmt.Sprintf("%s %s/%s : %s", kind, ns, name, msg), Tags: tags(kv...)})
	}
	if ds, err := kubeList[kubeDeployment](ctx, k, tok, "/apis/apps/v1/deployments"); err == nil {
		for _, d := range ds {
			desired := 1.0
			if d.Spec.Replicas != nil {
				desired = *d.Spec.Replicas
			}
			extra, floor := "", model.CheckOK
			if c := condOf(d.Status.Conditions, "Progressing"); c != nil && c.Status == "False" && c.Reason == "ProgressDeadlineExceeded" {
				extra, floor = "déploiement bloqué (ProgressDeadlineExceeded)", model.CheckWarn
			}
			workload(d.Metadata.Namespace, "deployment", d.Metadata.Name, desired, d.Status.Available, extra, floor)
		}
	} else {
		problems = append(problems, err.Error())
	}
	if ss, err := kubeList[kubeStatefulSet](ctx, k, tok, "/apis/apps/v1/statefulsets"); err == nil {
		for _, s := range ss {
			desired := 1.0
			if s.Spec.Replicas != nil {
				desired = *s.Spec.Replicas
			}
			avail := s.Status.Ready
			if s.Status.Available != nil {
				avail = *s.Status.Available
			}
			workload(s.Metadata.Namespace, "statefulset", s.Metadata.Name, desired, avail, "", model.CheckOK)
		}
	} else {
		problems = append(problems, err.Error())
	}
	if ds, err := kubeList[kubeDaemonSet](ctx, k, tok, "/apis/apps/v1/daemonsets"); err == nil {
		for _, d := range ds {
			workload(d.Metadata.Namespace, "daemonset", d.Metadata.Name, d.Status.Desired, d.Status.Available, "", model.CheckOK)
		}
	} else {
		problems = append(problems, err.Error())
	}
	// Pods statiques (plan de contrôle d'un kubeadm : apiserver, etcd, ordonnanceur, contrôleurs).
	for _, p := range pods {
		if kind, name := p.owner(); kind == "static_pod" {
			ready := 0.0
			if c := condOf(p.Status.Conditions, "Ready"); p.Status.Phase == "Running" && c != nil && c.Status == "True" {
				ready = 1
			}
			workload(p.Metadata.Namespace, kind, name, 1, ready, "nœud "+p.Spec.NodeName, model.CheckOK)
		}
	}
	if err := k.relayEvents(ctx, tok, tags, base); err != nil {
		problems = append(problems, "événements : "+err.Error())
	}
	conn := model.Check{TS: ts, Name: "kubernetes.can_connect", Status: model.CheckOK,
		Message: fmt.Sprintf("Kubernetes %s joignable (%d nœud(s), %d pod(s))", ver.GitVersion, len(nodes), len(pods)), Tags: tags(base...)}
	if len(problems) > 0 {
		conn.Status, conn.Message = model.CheckWarn, conn.Message+" ; lecture partielle : "+model.Truncate(strings.Join(problems, " ; "), 400)
		checks = append(checks, conn)
		return checks, "partiel : " + model.Truncate(problems[0], 200)
	}
	return append(checks, conn), "ok"
}

func (k *Kubernetes) nodeChecks(ts int64, b *model.SeriesBuilder, tags model.TagFunc, base []string, nodes []kubeNode, pods []kubePod) []model.Check {
	type req struct{ cpu, mem, pods float64 }
	used := map[string]*req{}
	for _, p := range pods {
		if p.Spec.NodeName == "" || p.Status.Phase == "Succeeded" || p.Status.Phase == "Failed" {
			continue
		}
		r := used[p.Spec.NodeName]
		if r == nil {
			r = &req{}
			used[p.Spec.NodeName] = r
		}
		r.pods++
		for _, c := range p.Spec.Containers {
			if v, ok := kubeQuantity(c.Resources.Requests["cpu"]); ok {
				r.cpu += v
			}
			if v, ok := kubeQuantity(c.Resources.Requests["memory"]); ok {
				r.mem += v
			}
		}
	}
	var out []model.Check
	ready := 0.0
	for _, n := range nodes {
		kv := append(append([]string{}, base...), "kube_node", n.Metadata.Name)
		r := used[n.Metadata.Name]
		if r == nil {
			r = &req{}
		}
		b.Gauge("kubernetes.node.pods", "pod", r.pods, kv...)
		if v, ok := kubeQuantity(n.Status.Allocatable["pods"]); ok && v > 0 {
			b.Gauge("kubernetes.node.pods.capacity", "pod", v, kv...)
		}
		if v, ok := kubeQuantity(n.Status.Allocatable["cpu"]); ok && v > 0 {
			b.Gauge("kubernetes.node.cpu.allocatable", "core", v, kv...)
			b.Gauge("kubernetes.node.cpu.requests", "core", round1(r.cpu*100)/100, kv...)
			b.Gauge("kubernetes.node.cpu.requests_pct", "percent", round1(100*r.cpu/v), kv...)
		}
		if v, ok := kubeQuantity(n.Status.Allocatable["memory"]); ok && v > 0 {
			b.Gauge("kubernetes.node.mem.allocatable", "byte", v, kv...)
			b.Gauge("kubernetes.node.mem.requests", "byte", r.mem, kv...)
			b.Gauge("kubernetes.node.mem.requests_pct", "percent", round1(100*r.mem/v), kv...)
		}
		st, msg := model.CheckOK, "prêt"
		switch c := condOf(n.Status.Conditions, "Ready"); {
		case c == nil || c.Status == "Unknown":
			st, msg = model.CheckCrit, "état inconnu : le kubelet ne répond plus"
		case c.Status != "True":
			st, msg = model.CheckCrit, "NotReady ("+firstNonEmpty(c.Reason, "sans raison")+")"
		default:
			ready++
		}
		var press []string
		for _, typ := range []string{"MemoryPressure", "DiskPressure", "PIDPressure", "NetworkUnavailable"} {
			if c := condOf(n.Status.Conditions, typ); c != nil && c.Status == "True" {
				press = append(press, typ)
			}
		}
		if len(press) > 0 {
			msg += " ; " + strings.Join(press, ", ")
			if st == model.CheckOK {
				st = model.CheckWarn
			}
		}
		if n.Spec.Unschedulable {
			msg += " ; fermé aux nouveaux pods (cordon)"
		}
		out = append(out, model.Check{TS: ts, Name: "kubernetes.node", Status: st,
			Message: fmt.Sprintf("%s %s (kubelet %s, %g pod(s))", n.Metadata.Name, msg, n.Status.NodeInfo.Kubelet, r.pods), Tags: tags(kv...)})
	}
	b.Gauge("kubernetes.nodes", "node", float64(len(nodes)), base...)
	b.Gauge("kubernetes.nodes.ready", "node", ready, base...)
	return out
}

// podSeries : pods par espace de noms et par phase, pods impossibles à placer, redémarrages.
func (k *Kubernetes) podSeries(ts int64, b *model.SeriesBuilder, tags model.TagFunc, base []string, pods []kubePod, complete bool) []model.Check {
	type nsCount struct{ running, pending, failed, restarts float64 }
	per := map[string]*nsCount{}
	unsched := 0.0
	cur := map[string]float64{}
	for _, p := range pods {
		ns := per[p.Metadata.Namespace]
		if ns == nil {
			ns = &nsCount{}
			per[p.Metadata.Namespace] = ns
		}
		switch p.Status.Phase {
		case "Running":
			ns.running++
		case "Pending":
			ns.pending++
			if c := condOf(p.Status.Conditions, "PodScheduled"); c != nil && c.Status == "False" && c.Reason == "Unschedulable" {
				unsched++
			}
		case "Failed":
			ns.failed++
		}
		n := 0.0
		for _, cs := range append(append([]kubeContainerStatus{}, p.Status.Inits...), p.Status.Containers...) {
			n += cs.Restarts
		}
		cur[p.Metadata.UID] = n
		if k.restarts != nil {
			if prev, ok := k.restarts[p.Metadata.UID]; ok {
				if d := n - prev; d > 0 {
					ns.restarts += d
				}
			} else if now := time.Now(); now.Sub(p.Metadata.Created) < 2*k.period()+time.Minute {
				ns.restarts += n // pod né depuis le passage précédent
			}
		}
	}
	names := make([]string, 0, len(per))
	for n := range per {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		c := per[name]
		kv := append(append([]string{}, base...), "kube_namespace", name)
		b.Gauge("kubernetes.pods.running", "pod", c.running, kv...)
		b.Gauge("kubernetes.pods.pending", "pod", c.pending, kv...)
		b.Gauge("kubernetes.pods.failed", "pod", c.failed, kv...)
		if k.restarts != nil {
			b.Add("kubernetes.containers.restarts", model.TypeCount, "restart", c.restarts, kv...)
		}
	}
	b.Gauge("kubernetes.pods.unschedulable", "pod", unsched, base...)
	if complete {
		k.restarts = cur
	}
	return nil
}

// relayEvents : événements Warning apparus depuis le passage précédent (le premier passage ne
// relaie pas l'historique de l'heure que garde la grappe).
func (k *Kubernetes) relayEvents(ctx context.Context, tok string, tags model.TagFunc, base []string) error {
	evs, err := kubeList[kubeEvent](ctx, k, tok, "/api/v1/events?fieldSelector="+url.QueryEscape("type=Warning"))
	if err != nil {
		return err
	}
	seen := make(map[string]bool, len(evs))
	var out []model.Event
	sort.Slice(evs, func(i, j int) bool { return evs[i].when().Before(evs[j].when()) })
	for _, e := range evs {
		count := e.Count
		if e.Series != nil {
			count = e.Series.Count
		}
		key := fmt.Sprintf("%s/%g/%d", e.Metadata.UID, count, e.when().Unix())
		seen[key] = true
		if k.events == nil || k.events[key] {
			continue
		}
		kind := strings.ToLower(e.Involved.Kind)
		ns := firstNonEmpty(e.Involved.Namespace, e.Metadata.Namespace)
		kv := append(append([]string{}, base...), "kube_namespace", ns, "kube_kind", kind, "kube_name", e.Involved.Name)
		title := fmt.Sprintf("%s %s/%s : %s", kind, ns, e.Involved.Name, e.Reason)
		if count > 1 {
			title += fmt.Sprintf(" (%g fois)", count)
		}
		ev := model.Event{TS: e.when().UnixMilli(), Source: "kubernetes", Type: "kubernetes_" + firstNonEmpty(e.Reason, "Warning"),
			Title: model.Truncate(title, 200), Text: model.Truncate(e.Message, 1000), AlertType: "warning", Priority: "normal",
			AggregationKey: "kubernetes:" + base[1] + ":" + e.Metadata.UID}
		if tags != nil {
			ev.Tags = tags(kv...)
		}
		out = append(out, ev)
	}
	k.events = seen
	if len(out) > kubeMaxEvents {
		out = out[len(out)-kubeMaxEvents:]
	}
	if len(out) > 0 && k.post != nil {
		k.post(routeEvents, map[string]any{"events": out})
	}
	return nil
}
