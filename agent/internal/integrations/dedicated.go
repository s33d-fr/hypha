package integrations

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── Sondes dédiées (vague 3 des intégrations) ──────────────────────────────────────────────
//
// Chaque logiciel est lu par son interface native : page de statut, socket d'administration,
// commande de statistiques ou API REST. Les sondes suivent les règles des intégrations de
// produits : mode auto (active après une première lecture réussie), contrôle <nom>.can_connect
// une fois le logiciel vu, compteurs cumulés rendus en écarts par intervalle, instances (§ 50).

// dedicatedIntegrations : entrées principales puis instances de chaque sonde.
func dedicatedIntegrations(cfg config.IntegrationsConfig, d Deps) []integration {
	in, x := cfg.DedicatedIntegrations, cfg.DedicatedExtra
	var out []integration
	add := func(kind, name string, mk func() integration) {
		out = append(out, named(kind, name, d, func(Deps) integration { return mk() }))
	}
	add("phpfpm", in.PHPFPM.Name, func() integration { return newPHPFPM(in.PHPFPM) })
	for _, c := range x.PHPFPM {
		add("phpfpm", c.Name, func() integration { return newPHPFPM(c) })
	}
	add("varnish", in.Varnish.Name, func() integration { return newVarnish(in.Varnish) })
	for _, c := range x.Varnish {
		add("varnish", c.Name, func() integration { return newVarnish(c) })
	}
	add("squid", in.Squid.Name, func() integration { return newSquid(in.Squid) })
	for _, c := range x.Squid {
		add("squid", c.Name, func() integration { return newSquid(c) })
	}
	add("lighttpd", in.Lighttpd.Name, func() integration { return newLighttpd(in.Lighttpd) })
	for _, c := range x.Lighttpd {
		add("lighttpd", c.Name, func() integration { return newLighttpd(c) })
	}
	add("supervisord", in.Supervisord.Name, func() integration { return newSupervisord(in.Supervisord) })
	for _, c := range x.Supervisord {
		add("supervisord", c.Name, func() integration { return newSupervisord(c) })
	}
	add("pgbouncer", in.PgBouncer.Name, func() integration { return newPgBouncer(in.PgBouncer) })
	for _, c := range x.PgBouncer {
		add("pgbouncer", c.Name, func() integration { return newPgBouncer(c) })
	}
	add("proxysql", in.ProxySQL.Name, func() integration { return newProxySQL(in.ProxySQL) })
	for _, c := range x.ProxySQL {
		add("proxysql", c.Name, func() integration { return newProxySQL(c) })
	}
	add("clickhouse", in.ClickHouse.Name, func() integration { return newClickHouse(in.ClickHouse) })
	for _, c := range x.ClickHouse {
		add("clickhouse", c.Name, func() integration { return newClickHouse(c) })
	}
	add("couchdb", in.CouchDB.Name, func() integration { return newCouchDB(in.CouchDB) })
	for _, c := range x.CouchDB {
		add("couchdb", c.Name, func() integration { return newCouchDB(c) })
	}
	add("couchbase", in.Couchbase.Name, func() integration { return newCouchbase(in.Couchbase) })
	for _, c := range x.Couchbase {
		add("couchbase", c.Name, func() integration { return newCouchbase(c) })
	}
	add("zookeeper", in.ZooKeeper.Name, func() integration { return newZooKeeper(in.ZooKeeper) })
	for _, c := range x.ZooKeeper {
		add("zookeeper", c.Name, func() integration { return newZooKeeper(c) })
	}
	add("ceph", in.Ceph.Name, func() integration { return newCeph(in.Ceph) })
	for _, c := range x.Ceph {
		add("ceph", c.Name, func() integration { return newCeph(c) })
	}
	add("openldap", in.OpenLDAP.Name, func() integration { return newOpenLDAP(in.OpenLDAP) })
	for _, c := range x.OpenLDAP {
		add("openldap", c.Name, func() integration { return newOpenLDAP(c) })
	}
	add("powerdns", in.PowerDNS.Name, func() integration { return newPowerDNS(in.PowerDNS, false) })
	for _, c := range x.PowerDNS {
		add("powerdns", c.Name, func() integration { return newPowerDNS(c, false) })
	}
	add("powerdns_recursor", in.PowerDNSRecursor.Name, func() integration { return newPowerDNS(in.PowerDNSRecursor, true) })
	for _, c := range x.PowerDNSRecursor {
		add("powerdns_recursor", c.Name, func() integration { return newPowerDNS(c, true) })
	}
	add("keycloak", in.Keycloak.Name, func() integration { return newKeycloak(in.Keycloak) })
	for _, c := range x.Keycloak {
		add("keycloak", c.Name, func() integration { return newKeycloak(c) })
	}
	add("nexus", in.Nexus.Name, func() integration { return newNexus(in.Nexus) })
	for _, c := range x.Nexus {
		add("nexus", c.Name, func() integration { return newNexus(c) })
	}
	add("harbor", in.Harbor.Name, func() integration { return newHarbor(in.Harbor) })
	for _, c := range x.Harbor {
		add("harbor", c.Name, func() integration { return newHarbor(c) })
	}
	add("fluentd", in.Fluentd.Name, func() integration { return newFluentd(in.Fluentd) })
	for _, c := range x.Fluentd {
		add("fluentd", c.Name, func() integration { return newFluentd(c) })
	}
	add("wazuh", in.Wazuh.Name, func() integration { return newWazuh(in.Wazuh) })
	for _, c := range x.Wazuh {
		add("wazuh", c.Name, func() integration { return newWazuh(c) })
	}
	add("suricata", in.Suricata.Name, func() integration { return newSuricata(in.Suricata) })
	for _, c := range x.Suricata {
		add("suricata", c.Name, func() integration { return newSuricata(c) })
	}
	out = append(out, newGunicorn(in.Gunicorn), newBtrfs(in.Btrfs), newNFS(in.NFS), newSSHProbe(in.SSH))
	return out
}

// intent : en mode auto, une conf posée (adresse non standard, identifiants) dit l'intention :
// la sonde passe à true et ses échecs se signalent dès le premier passage.
func intent(mode config.Tristate, explicit bool) config.Tristate {
	if mode == "auto" && explicit {
		return "true"
	}
	return mode
}

// probe : ce que partagent les sondes (mode, nom, dernier état, écarts de compteurs).
type probe struct {
	kind  string // nom de l'intégration (état, cadence)
	can   string // nom du contrôle de joignabilité (« php_fpm.can_connect »), écrit en entier
	det   detector
	prev  map[string]float64
	cur   map[string]float64
	where []string // étiquettes de la cible (instance, url…)
}

func newProbe(kind, can string, mode config.Tristate, where ...string) probe {
	return probe{kind: kind, can: can, det: detector{mode: mode}, where: where}
}

func (p *probe) name() string { return p.kind }
func (p *probe) close()       {}

// fail : lecture en échec ; muet tant qu'un logiciel en mode auto n'a jamais été vu, sinon
// contrôle de joignabilité (p.can) en CRIT.
func (p *probe) fail(ts int64, tags model.TagFunc, err error) ([]model.Check, string) {
	if silent, st := p.det.failed(err.Error()); silent {
		return nil, st
	}
	return []model.Check{{TS: ts, Name: p.can, Status: model.CheckCrit, Message: err.Error(),
		Tags: tags(p.where...)}}, "erreur : " + err.Error()
}

// okCheck : contrôle de joignabilité en OK.
func (p *probe) okCheck(ts int64, tags model.TagFunc, msg string) model.Check {
	return model.Check{TS: ts, Name: p.can, Status: model.CheckOK, Message: msg, Tags: tags(p.where...)}
}

// begin ouvre un relevé de compteurs ; end le clôt (les écarts du relevé suivant partent de là).
func (p *probe) begin() { p.cur = map[string]float64{} }
func (p *probe) end()   { p.prev, p.cur = p.cur, nil }

// count publie l'écart d'un compteur cumulé depuis le relevé précédent (rien au premier relevé
// ni après une remise à zéro). key distingue les compteurs qui partagent un nom de métrique.
func (p *probe) count(b *model.SeriesBuilder, metric, unit, key string, v float64, kv ...string) {
	p.cur[key] = v
	if p.prev == nil {
		return
	}
	old, ok := p.prev[key]
	if !ok {
		return
	}
	if d, ok := deltaF(v, old); ok {
		b.Add(metric, model.TypeCount, unit, d, kv...)
	}
}

// diff rend l'écart d'un compteur déjà relevé par count dans ce relevé (taux dérivés).
func (p *probe) diff(key string) (float64, bool) {
	if p.prev == nil {
		return 0, false
	}
	old, ok1 := p.prev[key]
	v, ok2 := p.cur[key]
	if !ok1 || !ok2 {
		return 0, false
	}
	return deltaF(v, old)
}

// ratio publie 100·a/(a+b) des écarts de deux compteurs (taux de succès d'un cache).
func (p *probe) ratio(b *model.SeriesBuilder, metric, hitKey, missKey string, kv ...string) {
	h, ok1 := p.diff(hitKey)
	m, ok2 := p.diff(missKey)
	if ok1 && ok2 && h+m > 0 {
		b.Gauge(metric, "percent", round1(100*h/(h+m)), kv...)
	}
}

// kvs : étiquettes de la cible suivies d'autres paires.
func (p *probe) kvs(kv ...string) []string {
	return append(append(make([]string, 0, len(p.where)+len(kv)), p.where...), kv...)
}

// ── Client HTTP des sondes ─────────────────────────────────────────────────────────────────

type httpProbe struct {
	base     string
	user     string
	pass     string
	headers  map[string]string
	client   *http.Client
	maxBytes int64
}

func newHTTPProbe(base, user, pass string, insecure bool) *httpProbe {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil // une sonde locale ne passe jamais par le mandataire de l'environnement
	if insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // choix explicite de la conf
	}
	tr.MaxIdleConns, tr.IdleConnTimeout = 2, 90*time.Second
	return &httpProbe{base: strings.TrimRight(base, "/"), user: user, pass: pass,
		client: &http.Client{Timeout: 10 * time.Second, Transport: tr}, maxBytes: 32 << 20}
}

// unixHTTP : client HTTP vers une socket Unix (Supervisor).
func unixHTTP(path string) *http.Client {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", path)
		},
		MaxIdleConns: 1, IdleConnTimeout: 90 * time.Second,
	}
	return &http.Client{Timeout: 10 * time.Second, Transport: tr}
}

// do envoie une requête ; rend le corps (borné), le code HTTP et une erreur réseau.
func (h *httpProbe) do(ctx context.Context, method, path string, body io.Reader, hdr map[string]string) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, h.base+path, body)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "hypha-agent")
	if h.user != "" || h.pass != "" {
		req.SetBasicAuth(h.user, h.pass)
	}
	for k, v := range h.headers {
		req.Header.Set(k, v)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, 0, cleanNetErr(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, h.maxBytes))
	return data, resp.StatusCode, err
}

// getJSON lit une ressource JSON ; un code autre que 200 est une erreur qui le nomme.
func (h *httpProbe) getJSON(ctx context.Context, path string, out any) error {
	data, code, err := h.do(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return err
	}
	if err := httpStatusErr(path, code, data); err != nil {
		return err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%s : réponse illisible (%v)", path, err)
	}
	return nil
}

func httpStatusErr(path string, code int, body []byte) error {
	switch {
	case code == http.StatusOK:
		return nil
	case code == http.StatusUnauthorized:
		return fmt.Errorf("%s : identifiants refusés (HTTP 401)", path)
	case code == http.StatusForbidden:
		return fmt.Errorf("%s : droits insuffisants (HTTP 403)", path)
	}
	msg := strings.TrimSpace(string(body))
	if len(msg) > 120 || strings.ContainsAny(msg, "<{") {
		msg = ""
	}
	if msg != "" {
		return fmt.Errorf("%s : HTTP %d (%s)", path, code, msg)
	}
	return fmt.Errorf("%s : HTTP %d", path, code)
}

// cleanNetErr retire l'URL de l'erreur du client HTTP (« Get "http://…": dial tcp … »).
func cleanNetErr(err error) error {
	msg := err.Error()
	if i := strings.Index(msg, `": `); i > 0 && strings.Contains(msg[:i], "://") {
		msg = msg[i+3:]
	}
	return fmt.Errorf("%s", msg)
}

// num lit un nombre JSON rendu en texte ou en nombre.
func num(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case string:
		var f float64
		_, err := fmt.Sscan(strings.TrimSpace(x), &f)
		return f, err == nil
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}

// snake : « udp-queries » → « udp_queries » (noms de métriques).
func snake(s string) string {
	return strings.NewReplacer("-", "_", " ", "_", "/", "_").Replace(strings.ToLower(s))
}
