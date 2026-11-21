// Package sender envoie les corps JSON au serveur (contrat § 3) : gzip, en-tête
// X-Hypha-Api-Key, objet "agent" ajouté à chaque corps, reprise sur 429/5xx/réseau avec
// recul exponentiel (1 s → 5 min) et tampon disque, abandon journalisé sur 400/401/403/413.
//
// Les collecteurs ne sont jamais bloqués par le réseau : Post compresse puis confie le
// corps à une file ; un ouvrier l'envoie, ou le range sur disque si le serveur ne répond
// pas. Un second ouvrier vide le tampon disque quand le serveur revient.
package sender

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"s33d.fr/hypha/agent/internal/agentlog"
	"s33d.fr/hypha/agent/internal/model"
)

// Routes d'ingestion (contrat § 3).
const (
	RouteHost        = "/api/v1/intake/host"
	RouteMetrics     = "/api/v1/intake/metrics"
	RouteLogs        = "/api/v1/intake/logs"
	RouteChecks      = "/api/v1/intake/checks"
	RouteEvents      = "/api/v1/intake/events"
	RouteProcesses   = "/api/v1/intake/processes"
	RouteConnections = "/api/v1/intake/connections"
	RouteContainers  = "/api/v1/intake/containers"

	// 0.4.0 (CONTRAT-04 §§ 15, 27, 28, 29, 30).
	RouteInventory  = "/api/v1/intake/inventory"
	RouteSynthetics = "/api/v1/intake/synthetics"
	RouteDBM        = "/api/v1/intake/dbm"
	RouteControl    = "/api/v1/agent/control"
)

// RouteActionResult rend la route du compte rendu d'une action (§ 28).
func RouteActionResult(id int64) string {
	return fmt.Sprintf("/api/v1/agent/actions/%d/result", id)
}

const (
	minBackoff     = time.Second
	maxBackoff     = 5 * time.Minute
	requestTimeout = 30 * time.Second
	queueSize      = 64
)

// Options règle l'expéditeur.
type Options struct {
	URL            string
	APIKey         string
	Agent          model.AgentInfo
	Proxy          string
	TLSSkipVerify  bool
	TLSPin         string // tls_pin : empreintes sha256// de la clé publique du serveur
	BufferDir      string
	BufferMaxBytes int64
	DryRun         bool
	Out            io.Writer // sortie du --dry-run
	Logger         *slog.Logger
}

// Sender est l'expéditeur unique de l'agent.
type Sender struct {
	o       Options
	client  *http.Client
	buf     *Buffer
	limiter *agentlog.Limiter

	queue    chan *payload
	closeMu  sync.RWMutex
	closed   bool
	workerWG sync.WaitGroup

	mu        sync.Mutex
	backoff   time.Duration
	nextTry   time.Time
	outMu     sync.Mutex
	sentBytes atomic.Int64
	dropped   atomic.Int64
	lastOK    atomic.Int64
	lastErr   atomic.Value // string
}

type payload struct {
	route     string
	gz        []byte
	onDurable func()
}

// New crée l'expéditeur ; en --dry-run, ni réseau ni tampon disque.
func New(o Options) (*Sender, error) {
	s := &Sender{o: o, limiter: agentlog.NewLimiter(time.Minute), queue: make(chan *payload, queueSize)}
	s.lastErr.Store("")
	if o.DryRun {
		return s, nil
	}
	tr, err := NewTransport(o.Proxy, o.TLSSkipVerify, o.TLSPin)
	if err != nil {
		return nil, err
	}
	s.client = &http.Client{
		Transport: tr,
		Timeout:   requestTimeout,
		// Une redirection transformerait le POST en GET et perdrait le corps : on la refuse
		// et on la signale (l'url de la conf est à corriger).
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	if s.buf, err = OpenBuffer(o.BufferDir, o.BufferMaxBytes); err != nil {
		return nil, fmt.Errorf("tampon disque %s : %w", o.BufferDir, err)
	}
	return s, nil
}

// NewTransport crée le transport HTTP commun (envoi, relais OTLP, mise à jour) : mandataire
// de la conf, sinon celui de l'environnement (HTTPS_PROXY), et vérification TLS réglable.
// pin (tls_pin) : la clé publique du certificat présenté par le serveur doit avoir l'une de
// ces empreintes ; la chaîne de certification n'est alors plus exigée (certificat autosigné
// ou autorité interne d'un serveur auto-hébergé), mais la clé l'est, sur chaque connexion.
func NewTransport(proxy string, skipVerify bool, pin string) (*http.Transport, error) {
	proxyFn := http.ProxyFromEnvironment
	if proxy != "" {
		u, err := url.Parse(proxy)
		if err != nil {
			return nil, fmt.Errorf("proxy %q : %w", proxy, err)
		}
		proxyFn = http.ProxyURL(u)
	}
	tc := &tls.Config{InsecureSkipVerify: skipVerify, MinVersion: tls.VersionTLS12} //nolint:gosec // choix explicite de la conf
	if strings.TrimSpace(pin) != "" {
		pins, err := ParsePins(pin)
		if err != nil {
			return nil, err
		}
		tc.InsecureSkipVerify = true //nolint:gosec // remplacé par la comparaison d'empreinte ci-dessous
		tc.VerifyConnection = func(cs tls.ConnectionState) error { return checkPin(cs, pins) }
	}
	return &http.Transport{
		Proxy:                 proxyFn,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSClientConfig:       tc,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: requestTimeout,
		MaxIdleConns:          4,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       90 * time.Second,
		ForceAttemptHTTP2:     true,
	}, nil
}

// ParsePins lit tls_pin : « sha256//<base64> », plusieurs séparées par « ; » (même écriture
// que --pinnedpubkey de curl, pour qu'une seule valeur serve à l'installation et à l'agent).
func ParsePins(v string) ([][]byte, error) {
	var out [][]byte
	for _, p := range strings.Split(v, ";") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		b64, ok := strings.CutPrefix(p, "sha256//")
		if !ok {
			return nil, fmt.Errorf("tls_pin : %q ne commence pas par sha256//", p)
		}
		sum, err := base64.StdEncoding.DecodeString(b64)
		if err != nil || len(sum) != sha256.Size {
			return nil, fmt.Errorf("tls_pin : %q n'est pas une empreinte SHA-256 en base64", p)
		}
		out = append(out, sum)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("tls_pin vide")
	}
	return out, nil
}

// PinOf rend l'empreinte tls_pin d'un certificat DER (clé publique du sujet).
func PinOf(spkiDER []byte) string {
	sum := sha256.Sum256(spkiDER)
	return "sha256//" + base64.StdEncoding.EncodeToString(sum[:])
}

func checkPin(cs tls.ConnectionState, pins [][]byte) error {
	if len(cs.PeerCertificates) == 0 {
		return fmt.Errorf("tls_pin : le serveur ne présente aucun certificat")
	}
	sum := sha256.Sum256(cs.PeerCertificates[0].RawSubjectPublicKeyInfo)
	for _, p := range pins {
		if subtle.ConstantTimeCompare(sum[:], p) == 1 {
			return nil
		}
	}
	return fmt.Errorf("tls_pin : la clé du serveur (%s) n'est pas celle attendue", PinOf(cs.PeerCertificates[0].RawSubjectPublicKeyInfo))
}

// Start lance l'ouvrier d'envoi et, si drain, celui qui vide le tampon disque.
func (s *Sender) Start(ctx context.Context, drain bool) {
	if s.o.DryRun {
		return
	}
	s.workerWG.Add(1)
	go func() {
		defer s.workerWG.Done()
		s.worker(ctx)
	}()
	if drain {
		go s.drainer(ctx)
	}
}

// Close attend que la file soit vidée (envoyée ou rangée sur disque).
func (s *Sender) Close() {
	s.closeMu.Lock()
	if s.closed {
		s.closeMu.Unlock()
		return
	}
	s.closed = true
	close(s.queue)
	s.closeMu.Unlock()
	s.workerWG.Wait()
}

// Post envoie fields (plus l'objet agent) sur route. onDurable, s'il est donné, est appelé
// quand le corps est en sécurité : accepté par le serveur, rangé sur disque, ou refusé
// pour de bon (le renvoyer n'y changerait rien). Les curseurs de journaux avancent là.
// Un objet agent déjà posé dans fields est gardé : un équipement SNMP envoie sous sa propre
// identité (§ 14).
func (s *Sender) Post(route string, fields map[string]any, onDurable func()) {
	if _, ok := fields["agent"]; !ok {
		fields["agent"] = s.o.Agent
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		s.o.Logger.Error("encodage JSON impossible", "route", route, "err", err)
		return
	}
	if s.o.DryRun {
		s.print(route, raw)
		if onDurable != nil {
			onDurable()
		}
		return
	}
	gz, err := compress(raw)
	if err != nil {
		s.o.Logger.Error("compression impossible", "route", route, "err", err)
		return
	}
	p := &payload{route: route, gz: gz, onDurable: onDurable}

	s.closeMu.RLock()
	defer s.closeMu.RUnlock()
	if s.closed {
		s.toBuffer(p)
		return
	}
	select {
	case s.queue <- p:
	default:
		// File pleine : le serveur est lent ; le disque prend le relais.
		s.toBuffer(p)
	}
}

func compress(raw []byte) ([]byte, error) {
	var b bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&b, gzip.DefaultCompression)
	if _, err := zw.Write(raw); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func (s *Sender) print(route string, raw []byte) {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		pretty.Write(raw)
	}
	s.outMu.Lock()
	defer s.outMu.Unlock()
	fmt.Fprintf(s.o.Out, "### POST %s (%d octets JSON)\n%s\n\n", route, len(raw), pretty.Bytes())
}

// worker envoie les corps de la file un par un.
func (s *Sender) worker(ctx context.Context) {
	for p := range s.queue {
		if ctx.Err() != nil || s.inBackoff() {
			s.toBuffer(p)
			continue
		}
		switch s.attempt(ctx, p.route, p.gz) {
		case outcomeOK, outcomeDrop:
			if p.onDurable != nil {
				p.onDurable()
			}
		case outcomeRetry:
			s.toBuffer(p)
		}
	}
}

// drainer vide le tampon disque, du plus ancien au plus récent, quand le recul le permet.
func (s *Sender) drainer(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		for ctx.Err() == nil && !s.inBackoff() {
			name, route, gz, ok := s.buf.Oldest()
			if !ok {
				break
			}
			res := s.attempt(ctx, route, gz)
			if res == outcomeRetry {
				break
			}
			s.buf.Remove(name)
		}
	}
}

func (s *Sender) toBuffer(p *payload) {
	evicted, err := s.buf.Put(p.route, p.gz)
	if evicted > 0 {
		s.dropped.Add(int64(evicted))
		if s.limiter.Allow("evict") {
			s.o.Logger.Warn("tampon disque plein : corps les plus anciens jetés", "jetés", evicted, "max_mb", s.o.BufferMaxBytes>>20)
		}
	}
	if err != nil {
		if evicted == 0 {
			s.dropped.Add(1)
		}
		if s.limiter.Allow("buffer") {
			s.o.Logger.Error("écriture dans le tampon disque impossible : corps perdu", "err", err)
		}
		return
	}
	if p.onDurable != nil {
		p.onDurable()
	}
}

type outcome int

const (
	outcomeOK outcome = iota
	outcomeRetry
	outcomeDrop
)

// attempt fait un POST et classe le résultat selon le contrat.
func (s *Sender) attempt(ctx context.Context, route string, gz []byte) outcome {
	status, detail, err := s.post(ctx, route, gz)
	switch {
	case err == nil && status/100 == 2:
		s.sentBytes.Add(int64(len(gz)))
		s.lastOK.Store(time.Now().UnixMilli())
		s.resetBackoff()
		return outcomeOK
	case err != nil || status == http.StatusTooManyRequests || status >= 500:
		msg := detail
		if err != nil {
			msg = err.Error()
		}
		s.lastErr.Store(fmt.Sprintf("%s : %s", route, msg))
		d := s.growBackoff()
		if s.limiter.Allow("retry") {
			s.o.Logger.Warn("envoi en échec, nouvel essai plus tard", "route", route, "statut", status, "err", msg, "attente", d.Round(time.Second))
		}
		return outcomeRetry
	default:
		// 400, 401, 403, 413 (et tout autre refus) : renvoyer ne servirait à rien.
		s.dropped.Add(1)
		s.lastErr.Store(fmt.Sprintf("%s : HTTP %d %s", route, status, detail))
		if s.limiter.Allow(fmt.Sprintf("drop-%d", status)) {
			s.o.Logger.Error("corps refusé par le serveur, abandonné", "route", route, "statut", status, "détail", detail, "conseil", hint(status))
		}
		return outcomeDrop
	}
}

func hint(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return "clé absente ou inconnue : vérifier api_key"
	case http.StatusForbidden:
		return "clé révoquée ou de type app : créer une clé d'ingestion"
	case http.StatusRequestEntityTooLarge:
		return "corps trop gros"
	case http.StatusBadRequest:
		return "corps invalide : signaler ce message (version de l'agent et du serveur)"
	}
	if status/100 == 3 {
		return "redirection : corriger url dans la conf (https, chemin)"
	}
	return ""
}

func (s *Sender) post(ctx context.Context, route string, gz []byte) (int, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.o.URL+route, bytes.NewReader(gz))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("X-Hypha-Api-Key", s.o.APIKey)
	req.Header.Set("User-Agent", "hypha-agent/"+s.o.Agent.Version)
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var r struct {
		Error    string `json:"error"`
		Detail   string `json:"detail"`
		Accepted int    `json:"accepted"`
		Dropped  int    `json:"dropped"`
	}
	_ = json.Unmarshal(body, &r)
	if resp.StatusCode/100 == 2 {
		if r.Dropped > 0 && s.limiter.Allow("server-dropped") {
			if route == "/api/v1/intake/logs" {
				// Le compte des journaux inclut les lignes retirées par les filtres d'exclusion de
				// l'espace (contrat § 11) : un écart y est le cas normal, pas une anomalie.
				s.o.Logger.Info("le serveur a écarté des lignes de journal (filtres d'exclusion compris)", "route", route, "acceptés", r.Accepted, "écartés", r.Dropped)
			} else {
				s.o.Logger.Warn("le serveur a écarté des éléments (horodatage hors fenêtre ou élément invalide)", "route", route, "acceptés", r.Accepted, "écartés", r.Dropped)
			}
		}
		return resp.StatusCode, "", nil
	}
	detail := r.Detail
	if r.Error != "" {
		detail = r.Error + " : " + r.Detail
	}
	if detail == "" {
		detail = http.StatusText(resp.StatusCode)
	}
	return resp.StatusCode, detail, nil
}

// Agent rend l'objet agent des corps.
func (s *Sender) Agent() model.AgentInfo { return s.o.Agent }

// DryRun dit si l'expéditeur n'envoie rien.
func (s *Sender) DryRun() bool { return s.o.DryRun }

// Do fait une requête directe, hors file et hors tampon disque (canal de contrôle, compte
// rendu d'action) : en-têtes de clé et d'identité, corps JSON compressé s'il est donné
// (objet agent ajouté), réponse lue jusqu'à 4 Mio. Les redirections sont refusées comme
// pour les envois.
func (s *Sender) Do(ctx context.Context, method, route string, body map[string]any) (int, []byte, error) {
	if s.o.DryRun || s.client == nil {
		return 0, nil, fmt.Errorf("envoi coupé (--dry-run)")
	}
	var rd io.Reader
	if body != nil {
		if _, ok := body["agent"]; !ok {
			body["agent"] = s.o.Agent
		}
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		gz, err := compress(raw)
		if err != nil {
			return 0, nil, err
		}
		rd = bytes.NewReader(gz)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.o.URL+route, rd)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Content-Encoding", "gzip")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Hypha-Api-Key", s.o.APIKey)
	req.Header.Set("X-Hypha-Agent-Id", s.o.Agent.ID)
	req.Header.Set("X-Hypha-Agent-Version", s.o.Agent.Version)
	req.Header.Set("User-Agent", "hypha-agent/"+s.o.Agent.Version)
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, data, nil
}

func (s *Sender) inBackoff() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Now().Before(s.nextTry)
}

func (s *Sender) resetBackoff() {
	s.mu.Lock()
	s.backoff, s.nextTry = 0, time.Time{}
	s.mu.Unlock()
}

// growBackoff double l'attente (1 s, 2 s, 4 s… 5 min) avec un peu d'aléa, pour que
// 50 agents ne reviennent pas tous à la même seconde après une panne du serveur.
func (s *Sender) growBackoff() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.backoff == 0 {
		s.backoff = minBackoff
	} else {
		s.backoff = min(2*s.backoff, maxBackoff)
	}
	d := s.backoff + time.Duration(rand.Int64N(int64(s.backoff)/5+1))
	s.nextTry = time.Now().Add(d)
	return d
}

// Stats décrit l'activité depuis le dernier appel (métriques hypha.agent.*).
type Stats struct {
	SentBytes     int64 // octets compressés acceptés par le serveur depuis le dernier appel
	Dropped       int64 // corps abandonnés depuis le dernier appel
	BufferedBytes int64 // octets en attente dans le tampon disque
	BufferedCount int
	LastOK        time.Time
	LastError     string
}

// TakeStats rend les compteurs et remet à zéro ceux qui portent sur l'intervalle.
func (s *Sender) TakeStats() Stats {
	st := Stats{
		SentBytes: s.sentBytes.Swap(0),
		Dropped:   s.dropped.Swap(0),
		LastError: s.lastErr.Load().(string),
	}
	if ms := s.lastOK.Load(); ms > 0 {
		st.LastOK = time.UnixMilli(ms)
	}
	if s.buf != nil {
		st.BufferedCount, st.BufferedBytes = s.buf.Stats()
	}
	return st
}
