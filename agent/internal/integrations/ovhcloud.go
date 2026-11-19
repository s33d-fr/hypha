package integrations

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── OVHcloud (§ 34) : connecteur de l'API OVHcloud, en lecture seule ─────────────────────────
//
// Ce que l'agent d'une machine ne voit pas : l'état des serveurs dédiés vu par OVH, les adresses
// IP bloquées pour spam ou piratage, la mitigation anti-DDoS, les échéances des services et des
// domaines, les interventions et tâches d'OVH. Les appels partent d'une tâche de fond, à la
// cadence de la conf (15 min ; échéances et domaines toutes les 6 h), espacés de 200 ms : la
// collecte de l'agent relit le dernier relevé et n'attend jamais l'API. Seuls des GET partent.

const (
	ovhSlowEvery  = 6 * time.Hour
	ovhPace       = 200 * time.Millisecond
	ovhMaxDetails = 5 // détails d'interventions ou de tâches lus par passage et par serveur
	routeEvents   = "/api/v1/intake/events"
)

type ovhCheck struct {
	name   string
	status int
	msg    string
	kv     []string
}

type ovhGauge struct {
	name, unit string
	v          float64
	kv         []string
}

type ovhSnapshot struct {
	at        time.Time
	checks    []ovhCheck
	resources []*cloudResource // serveurs dédiés, publiés comme hôtes (§ 34)
	calls  int
	errs   []string
	fatal  string
	took   time.Duration
}

type ovhSlow struct {
	at     time.Time
	checks []ovhCheck
	gauges []ovhGauge
	routed map[string]string // bloc IP → service vers lequel il est routé
}

type OVHCloud struct {
	cfg    config.OVHCloudConfig
	post   func(route string, fields map[string]any)
	pub    *resourcePublisher
	client *http.Client
	pace   time.Duration
	clock  func() time.Time

	once sync.Once
	stop context.CancelFunc

	mu    sync.Mutex
	tags  model.TagFunc
	snap  ovhSnapshot
	slow  ovhSlow
	seen  map[string]map[int64]bool // « serveur/intervention » ou « serveur/task » → identifiants vus
	delta time.Duration             // heure OVH − heure locale (signature)
}

func newOVHCloud(cfg config.OVHCloudConfig, d Deps) *OVHCloud {
	return &OVHCloud{cfg: cfg, post: d.Post, pub: newResourcePublisher(d), client: &http.Client{Timeout: 20 * time.Second}, pace: ovhPace,
		clock: time.Now, seen: map[string]map[int64]bool{}}
}

func (o *OVHCloud) name() string          { return "ovhcloud" }
func (o *OVHCloud) period() time.Duration { return 60 * time.Second }
func (o *OVHCloud) close() {
	if o.stop != nil {
		o.stop()
	}
}

func (o *OVHCloud) collect(_ context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if o.cfg.Enabled == "false" {
		return nil, "disabled"
	}
	if !o.cfg.Configured() {
		if o.cfg.Enabled == "true" {
			return nil, "erreur : identifiants OVH incomplets"
		}
		return nil, "absent"
	}
	o.mu.Lock()
	o.tags = tags
	s, sl := o.snap, o.slow
	o.mu.Unlock()
	o.once.Do(o.start)
	ts := model.Ms(now)
	host := o.cfg.Endpoint
	if u, err := url.Parse(o.cfg.Endpoint); err == nil {
		host = u.Host
	}
	if s.at.IsZero() {
		return nil, "partiel : premier relevé en cours"
	}
	if age := now.Sub(s.at); s.fatal != "" || age > 3*o.cfg.Interval.D() {
		msg := s.fatal
		if msg == "" {
			msg = fmt.Sprintf("aucun relevé réussi depuis %s", age.Round(time.Minute))
		}
		return []model.Check{canConnect("ovhcloud", ts, false, msg, tags("ovh_endpoint", host))}, "erreur : " + msg
	}
	// Échéances et jours restants d'un serveur dédié : sur sa ressource ; le reste (IP, domaines)
	// sur l'agent qui interroge OVH.
	res := make([]*cloudResource, 0, len(s.resources))
	byService := map[string]*cloudResource{}
	for _, r := range s.resources {
		rc := *r
		rc.checks = append([]ovhCheck{}, r.checks...)
		rc.gauges = append([]ovhGauge{}, r.gauges...)
		res = append(res, &rc)
		byService[rc.device.Service] = &rc
	}
	out := make([]model.Check, 0, len(s.checks)+len(sl.checks)+1)
	for _, c := range s.checks {
		out = append(out, model.Check{TS: ts, Name: c.name, Status: c.status, Message: c.msg, Tags: tags(c.kv...)})
	}
	for _, c := range sl.checks {
		if r := byService[kvGet(c.kv, "ovh_service")]; r != nil && kvGet(c.kv, "ovh_type") == "dedicated_server" {
			r.checks = append(r.checks, c)
			continue
		}
		out = append(out, model.Check{TS: ts, Name: c.name, Status: c.status, Message: c.msg, Tags: tags(c.kv...)})
	}
	for _, g := range sl.gauges {
		if r := byService[kvGet(g.kv, "ovh_service")]; r != nil && kvGet(g.kv, "ovh_type") == "dedicated_server" {
			r.gauges = append(r.gauges, g)
			continue
		}
		b.Gauge(g.name, g.unit, g.v, g.kv...)
	}
	o.pub.publish(now, res, "ovhcloud", "ok")
	msg := fmt.Sprintf("%d appels à l'API OVH, relevé de %s en %s", s.calls, s.at.Format("15:04"), s.took.Round(time.Second))
	state := "ok"
	conn := canConnect("ovhcloud", ts, true, msg, tags("ovh_endpoint", host))
	if len(s.errs) > 0 {
		state = fmt.Sprintf("partiel : %d appel(s) en échec", len(s.errs))
		conn.Status = model.CheckWarn
		conn.Message = msg + " ; en échec : " + model.Truncate(strings.Join(s.errs, " ; "), 600)
	}
	return append(out, conn), state
}

func (o *OVHCloud) start() {
	ctx, cancel := context.WithCancel(context.Background())
	o.stop = cancel
	go func() {
		for {
			o.poll(ctx)
			select {
			case <-ctx.Done():
				return
			case <-time.After(o.cfg.Interval.D()):
			}
		}
	}()
}

// ── appels signés ──────────────────────────────────────────────────────────────────────────

type ovhPass struct {
	o     *OVHCloud
	ctx   context.Context
	calls int
	errs  []string
}

type ovhHTTPError struct {
	path   string
	status int
	msg    string
}

func (e *ovhHTTPError) Error() string {
	if e.msg != "" {
		return fmt.Sprintf("GET %s : HTTP %d (%s)", e.path, e.status, e.msg)
	}
	return fmt.Sprintf("GET %s : HTTP %d", e.path, e.status)
}

// ovhSignature : « $1$ » + SHA-1 hexadécimal de secret+clé consumer+méthode+URL+corps+horodatage.
func ovhSignature(secret, consumer, method, fullURL, body string, ts int64) string {
	h := sha1.Sum([]byte(strings.Join([]string{secret, consumer, method, fullURL, body, strconv.FormatInt(ts, 10)}, "+")))
	return "$1$" + hex.EncodeToString(h[:])
}

func (p *ovhPass) get(path string, out any) error {
	o := p.o
	if p.calls > 0 && o.pace > 0 {
		select {
		case <-p.ctx.Done():
			return p.ctx.Err()
		case <-time.After(o.pace):
		}
	}
	p.calls++
	full := strings.TrimRight(o.cfg.Endpoint, "/") + path
	req, err := http.NewRequestWithContext(p.ctx, http.MethodGet, full, nil)
	if err != nil {
		return err
	}
	o.mu.Lock()
	ts := o.clock().Add(o.delta).Unix()
	o.mu.Unlock()
	req.Header.Set("User-Agent", "hypha-agent")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Ovh-Application", o.cfg.ApplicationKey)
	req.Header.Set("X-Ovh-Consumer", o.cfg.ConsumerKey)
	req.Header.Set("X-Ovh-Timestamp", strconv.FormatInt(ts, 10))
	req.Header.Set("X-Ovh-Signature", ovhSignature(o.cfg.ApplicationSecret, o.cfg.ConsumerKey, http.MethodGet, full, "", ts))
	resp, err := o.client.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s : %w", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		var m struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(body, &m)
		return &ovhHTTPError{path: path, status: resp.StatusCode, msg: model.Truncate(m.Message, 160)}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("GET %s : réponse illisible : %w", path, err)
	}
	return nil
}

// try : un appel dont l'échec est noté sans arrêter le relevé.
func (p *ovhPass) try(path string, out any) bool {
	if err := p.get(path, out); err != nil {
		p.errs = append(p.errs, err.Error())
		return false
	}
	return true
}

// syncTime : décalage entre l'heure d'OVH (GET /auth/time, sans signature) et l'heure locale.
func (o *OVHCloud) syncTime(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(o.cfg.Endpoint, "/")+"/auth/time", nil)
	if err != nil {
		return err
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	t, err := strconv.ParseInt(strings.TrimSpace(string(body)), 10, 64)
	if resp.StatusCode != http.StatusOK || err != nil {
		return fmt.Errorf("GET /auth/time : HTTP %d", resp.StatusCode)
	}
	o.mu.Lock()
	o.delta = time.Unix(t, 0).Sub(o.clock())
	o.mu.Unlock()
	return nil
}

// ── relevé ─────────────────────────────────────────────────────────────────────────────────

type ovhServer struct {
	Name            string `json:"name"`
	State           string `json:"state"`
	PowerState      string `json:"powerState"`
	Datacenter      string `json:"datacenter"`
	IP              string `json:"ip"`
	Reverse         string `json:"reverse"`
	CommercialRange string `json:"commercialRange"`
	Monitoring      bool   `json:"monitoring"`
	IAM             struct {
		DisplayName string `json:"displayName"`
	} `json:"iam"`
}

type ovhServiceInfos struct {
	Expiration string `json:"expiration"`
	Status     string `json:"status"`
	Renew      struct {
		Automatic          bool `json:"automatic"`
		DeleteAtExpiration bool `json:"deleteAtExpiration"`
		ManualPayment      bool `json:"manualPayment"`
	} `json:"renew"`
}

type ovhIPState struct {
	State     string `json:"state"`
	Time      int64  `json:"time"`
	Auto      bool   `json:"auto"`
	Permanent bool   `json:"permanent"`
}

func (o *OVHCloud) poll(parent context.Context) {
	start := o.clock()
	ctx, cancel := context.WithTimeout(parent, 10*time.Minute)
	defer cancel()
	p := &ovhPass{o: o, ctx: ctx}
	var resources []*cloudResource
	finish := func(fatal string, checks []ovhCheck) {
		o.mu.Lock()
		o.snap = ovhSnapshot{at: o.clock(), checks: checks, resources: resources, calls: p.calls, errs: p.errs, fatal: fatal,
			took: o.clock().Sub(start)}
		o.mu.Unlock()
	}
	if err := o.syncTime(ctx); err != nil {
		finish("heure de l'API OVH illisible : "+err.Error(), nil)
		return
	}
	var servers []string
	if err := p.get("/dedicated/server", &servers); err != nil {
		var he *ovhHTTPError
		if asHTTP(err, &he) && (he.status == 401 || he.status == 403) {
			finish("identifiants refusés par OVH : "+err.Error(), nil)
			return
		}
		p.errs = append(p.errs, err.Error())
	}
	sort.Strings(servers)
	var checks []ovhCheck
	names := map[string]string{}
	for _, n := range servers {
		var s ovhServer
		if !p.try("/dedicated/server/"+url.PathEscape(n), &s) {
			continue
		}
		names[n] = firstNonEmpty(s.IAM.DisplayName, strings.TrimSuffix(s.Reverse, "."), n)
		r := &cloudResource{key: "ovh:dedicated_server:" + n, name: n, platform: "OVHcloud " + s.CommercialRange,
			tags: map[string]string{"provider": "ovh"}, checks: []ovhCheck{serverCheck(n, names[n], s)},
			device: model.Device{Provider: "ovh", Type: "dedicated_server", Service: n, DisplayName: names[n], IP: s.IP,
				Region: s.Datacenter, PowerState: s.PowerState, Vendor: "OVHcloud", Model: s.CommercialRange}}
		if s.IP != "" {
			r.ips = []string{s.IP}
		}
		resources = append(resources, r)
		o.newItems(p, n, names[n], "intervention")
		o.newItems(p, n, names[n], "task")
	}
	var blocks []string
	p.try("/ip", &blocks)
	sort.Strings(blocks)
	o.mu.Lock()
	routed := o.slow.routed
	o.mu.Unlock()
	for _, blk := range blocks {
		if strings.Contains(blk, ":") {
			continue // IPv6 : OVH ne bloque ni pour spam ni pour piratage sur ces blocs
		}
		checks = append(checks, ipChecks(p, blk, routed[blk])...)
	}
	o.mu.Lock()
	slowDue := o.clock().Sub(o.slow.at) >= ovhSlowEvery
	o.mu.Unlock()
	if slowDue {
		o.pollSlow(p, servers, names, blocks)
	}
	finish("", checks)
}

func kvGet(kv []string, key string) string {
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i] == key {
			return kv[i+1]
		}
	}
	return ""
}

func asHTTP(err error, target **ovhHTTPError) bool {
	he, ok := err.(*ovhHTTPError)
	if ok {
		*target = he
	}
	return ok
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func serverCheck(service, display string, s ovhServer) ovhCheck {
	st := model.CheckOK
	switch {
	case s.State == "error" || s.State == "hacked" || s.State == "hackedBlocked" || s.PowerState == "poweroff":
		st = model.CheckCrit
	case s.State != "ok":
		st = model.CheckWarn
	}
	msg := fmt.Sprintf("%s : état « %s », alimentation « %s », %s (%s)", display, s.State, s.PowerState, s.Datacenter, s.CommercialRange)
	if !s.Monitoring {
		msg += " ; surveillance OVH désactivée"
	}
	return ovhCheck{name: "ovh.dedicated_server.state", status: st, msg: msg,
		kv: []string{"ovh_service", service, "ovh_name", display, "ovh_type", "dedicated_server", "ip", s.IP, "datacenter", s.Datacenter}}
}

// newItems : interventions ou tâches d'OVH apparues depuis le passage précédent, en événements.
// Au premier passage, l'historique est seulement noté : il n'est pas rejoué.
func (o *OVHCloud) newItems(p *ovhPass, service, display, kind string) {
	var ids []int64
	base := "/dedicated/server/" + url.PathEscape(service) + "/" + kind
	if !p.try(base, &ids) {
		return
	}
	key := service + "/" + kind
	o.mu.Lock()
	seen, known := o.seen[key]
	if !known {
		seen = map[int64]bool{}
		for _, id := range ids {
			seen[id] = true
		}
		o.seen[key] = seen
		o.mu.Unlock()
		return
	}
	var fresh []int64
	for _, id := range ids {
		if !seen[id] {
			fresh = append(fresh, id)
		}
	}
	tags := o.tags
	o.mu.Unlock()
	sort.Slice(fresh, func(i, j int) bool { return fresh[i] < fresh[j] })
	if len(fresh) > ovhMaxDetails {
		fresh = fresh[len(fresh)-ovhMaxDetails:]
	}
	var events []model.Event
	for _, id := range fresh {
		var d map[string]any
		if !p.try(base+"/"+strconv.FormatInt(id, 10), &d) {
			continue
		}
		o.mu.Lock()
		seen[id] = true
		o.mu.Unlock()
		events = append(events, ovhItemEvent(o.clock(), service, display, kind, id, d, tags))
	}
	if len(events) > 0 && o.post != nil {
		o.post(routeEvents, map[string]any{"events": events})
	}
}

func ovhItemEvent(now time.Time, service, display, kind string, id int64, d map[string]any, tags model.TagFunc) model.Event {
	str := func(k string) string {
		if v, ok := d[k]; ok && v != nil {
			return fmt.Sprint(v)
		}
		return ""
	}
	kv := []string{"ovh_service", service, "ovh_name", display, "ovh_type", "dedicated_server"}
	var t map[string]string
	if tags != nil {
		t = tags(kv...)
	} else {
		t = map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			t[kv[i]] = kv[i+1]
		}
	}
	ev := model.Event{TS: model.Ms(now), Source: "ovhcloud", Tags: t, AggregationKey: fmt.Sprintf("ovh:%s:%s:%d", service, kind, id)}
	if kind == "intervention" {
		ev.Type, ev.AlertType, ev.Priority = "ovh_intervention", "warning", "normal"
		ev.Title = fmt.Sprintf("OVH : intervention sur %s", display)
		ev.Text = strings.TrimSpace(fmt.Sprintf("%s %s", str("type"), str("date")))
	} else {
		ev.Type, ev.AlertType, ev.Priority = "ovh_task", "info", "low"
		ev.Title = fmt.Sprintf("OVH : tâche « %s » sur %s", str("function"), display)
		ev.Text = strings.TrimSpace(fmt.Sprintf("état %s %s %s", str("status"), str("startDate"), str("comment")))
	}
	return ev
}

// ipChecks : blocage pour spam, blocage pour piratage et mitigation anti-DDoS d'un bloc IPv4.
func ipChecks(p *ovhPass, blk, service string) []ovhCheck {
	e := url.PathEscape(blk)
	kv := []string{"ovh_ip_block", blk, "ovh_type", "ip"}
	if service != "" {
		kv = append(kv, "ovh_service", service)
	}
	var out []ovhCheck
	for _, c := range []struct{ check, path, bad, pending, label string }{
		{"ovh.ip.spam", "/ip/" + e + "/spam", "blockedForSpam", "unblocking", "bloquée pour spam"},
		{"ovh.ip.antihack", "/ip/" + e + "/antihack", "blocked", "unblocking", "bloquée pour piratage"},
	} {
		var ips []string
		if !p.try(c.path, &ips) {
			continue
		}
		var bad, pending []string
		for _, ip := range ips {
			var s ovhIPState
			if !p.try(c.path+"/"+url.PathEscape(ip), &s) {
				continue
			}
			since := ""
			if s.Time > 0 {
				since = " depuis le " + time.Unix(s.Time, 0).UTC().Format("02/01/2006 15:04")
			}
			switch s.State {
			case c.bad:
				bad = append(bad, ip+since)
			case c.pending:
				pending = append(pending, ip)
			}
		}
		switch {
		case len(bad) > 0:
			out = append(out, ovhCheck{c.check, model.CheckCrit, blk + " : " + strings.Join(bad, ", ") + " " + c.label, kv})
		case len(pending) > 0:
			out = append(out, ovhCheck{c.check, model.CheckWarn, blk + " : déblocage en cours pour " + strings.Join(pending, ", "), kv})
		default:
			out = append(out, ovhCheck{c.check, model.CheckOK, blk + " : aucune adresse " + c.label, kv})
		}
	}
	var ips []string
	if p.try("/ip/"+e+"/mitigation", &ips) {
		var auto, permanent []string
		for _, ip := range ips {
			var s ovhIPState
			if !p.try("/ip/"+e+"/mitigation/"+url.PathEscape(ip), &s) {
				continue
			}
			if s.Auto {
				auto = append(auto, ip)
			} else if s.Permanent {
				permanent = append(permanent, ip)
			}
		}
		switch {
		case len(auto) > 0:
			out = append(out, ovhCheck{"ovh.ip.mitigation", model.CheckWarn, blk + " : mitigation automatique en cours (attaque détectée) pour " + strings.Join(auto, ", "), kv})
		case len(permanent) > 0:
			out = append(out, ovhCheck{"ovh.ip.mitigation", model.CheckOK, blk + " : mitigation permanente active pour " + strings.Join(permanent, ", "), kv})
		default:
			out = append(out, ovhCheck{"ovh.ip.mitigation", model.CheckOK, blk + " : aucune mitigation en cours", kv})
		}
	}
	return out
}

// pollSlow : échéances des serveurs et des domaines, routage des blocs IP (toutes les 6 h).
func (o *OVHCloud) pollSlow(p *ovhPass, servers []string, names map[string]string, blocks []string) {
	now := o.clock()
	sl := ovhSlow{at: now, routed: map[string]string{}}
	add := func(service, display, kind string, si ovhServiceInfos) {
		c, g := renewalCheck(service, display, kind, si, now)
		sl.checks = append(sl.checks, c)
		if g != nil {
			sl.gauges = append(sl.gauges, *g)
		}
	}
	for _, n := range servers {
		var si ovhServiceInfos
		if p.try("/dedicated/server/"+url.PathEscape(n)+"/serviceInfos", &si) {
			add(n, firstNonEmpty(names[n], n), "dedicated_server", si)
		}
	}
	var domains []string
	if p.try("/domain", &domains) {
		sort.Strings(domains)
		for _, d := range domains {
			var si ovhServiceInfos
			if p.try("/domain/"+url.PathEscape(d)+"/serviceInfos", &si) {
				add(d, d, "domain", si)
			}
		}
	}
	for _, blk := range blocks {
		if strings.Contains(blk, ":") {
			continue
		}
		var ip struct {
			RoutedTo struct {
				ServiceName string `json:"serviceName"`
			} `json:"routedTo"`
		}
		if p.try("/ip/"+url.PathEscape(blk), &ip) && ip.RoutedTo.ServiceName != "" {
			sl.routed[blk] = ip.RoutedTo.ServiceName
		}
	}
	o.mu.Lock()
	o.slow = sl
	o.mu.Unlock()
}

// renewalCheck : échéance d'un service OVH. Renouvellement automatique : OK ; sinon alerte à
// 30 jours, critique à 7 ; service expiré ou impayé : critique.
func renewalCheck(service, display, kind string, si ovhServiceInfos, now time.Time) (ovhCheck, *ovhGauge) {
	kv := []string{"ovh_service", service, "ovh_name", display, "ovh_type", kind}
	exp, err := time.Parse("2006-01-02", si.Expiration)
	if err != nil {
		return ovhCheck{"ovh.service.renewal", model.CheckUnknown, display + " : échéance illisible (" + si.Expiration + ")", kv}, nil
	}
	days := math.Floor(exp.Sub(now).Hours() / 24)
	g := &ovhGauge{name: "ovh.service.days_left", unit: "day", v: days, kv: kv}
	date := exp.Format("02/01/2006")
	auto := si.Renew.Automatic && !si.Renew.ManualPayment && !si.Renew.DeleteAtExpiration
	switch {
	case si.Status == "expired" || si.Status == "unPaid" || si.Status == "pendingDebt":
		return ovhCheck{"ovh.service.renewal", model.CheckCrit, fmt.Sprintf("%s : statut « %s », échéance le %s", display, si.Status, date), kv}, g
	case auto:
		return ovhCheck{"ovh.service.renewal", model.CheckOK, fmt.Sprintf("%s : renouvellement automatique, échéance le %s (dans %g j)", display, date, days), kv}, g
	}
	what := "renouvellement manuel"
	if si.Renew.DeleteAtExpiration {
		what = "résilié à l'échéance"
	}
	st := model.CheckOK
	if days <= 7 {
		st = model.CheckCrit
	} else if days <= 30 {
		st = model.CheckWarn
	}
	return ovhCheck{"ovh.service.renewal", st, fmt.Sprintf("%s : %s, échéance le %s (dans %g j)", display, what, date, days), kv}, g
}
