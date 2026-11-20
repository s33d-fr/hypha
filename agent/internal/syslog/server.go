package syslog

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/logs"
	"s33d.fr/hypha/agent/internal/model"
)

const (
	maxFrame    = 64 << 10         // trame TCP ou datagramme : au-delà, coupée (TCP) ou tronquée (UDP)
	idleTimeout = 10 * time.Minute // connexion TCP muette fermée
	maxHosts    = 500              // émetteurs distincts suivis dans les mesures
	silentAfter = 24 * time.Hour   // un émetteur vu dans les 24 h garde une mesure à 0 s'il se tait
)

// Server : écoute UDP et TCP, décodage, envoi dans la file des journaux, mesures.
type Server struct {
	cfg     config.SyslogConfig
	submit  func(logs.Entry) bool
	log     *slog.Logger
	allowed []*net.IPNet
	parsers []parserRule

	mu        sync.Mutex
	counts    map[string]float64
	seen      map[string]time.Time
	dropped   map[string]float64
	listening []string
	errs      []string
	conns     int
	tokens    float64
	last      time.Time
}

type parserRule struct {
	net    *net.IPNet
	glob   string
	parser string
}

// New prépare la réception ; submit confie une ligne à la file des journaux sans attendre.
func New(cfg config.SyslogConfig, submit func(logs.Entry) bool, log *slog.Logger) *Server {
	s := &Server{cfg: cfg, submit: submit, log: log, counts: map[string]float64{}, seen: map[string]time.Time{},
		dropped: map[string]float64{}, tokens: float64(cfg.RateLimit)}
	for _, a := range cfg.AllowedSources {
		if n := toNet(a); n != nil {
			s.allowed = append(s.allowed, n)
		}
	}
	for _, p := range cfg.Parsers {
		r := parserRule{parser: p.Parser}
		if n := toNet(p.Match); n != nil {
			r.net = n
		} else {
			r.glob = strings.ToLower(p.Match)
		}
		s.parsers = append(s.parsers, r)
	}
	return s
}

func toNet(a string) *net.IPNet {
	if _, n, err := net.ParseCIDR(a); err == nil {
		return n
	}
	if ip := net.ParseIP(a); ip != nil {
		bits := 128
		if ip.To4() != nil {
			ip, bits = ip.To4(), 32
		}
		return &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}
	}
	return nil
}

// Run écoute jusqu'à l'arrêt ; une écoute impossible (port pris, droits) est notée et rendue
// par le contrôle syslog.can_receive, l'autre protocole continue.
func (s *Server) Run(ctx context.Context) {
	var wg sync.WaitGroup
	if s.cfg.UDP != "" {
		pc, err := net.ListenPacket("udp", s.cfg.UDP)
		if err != nil {
			s.fail("UDP " + s.cfg.UDP + " : " + err.Error())
		} else {
			s.listen("UDP " + pc.LocalAddr().String())
			wg.Add(1)
			go func() { defer wg.Done(); s.serveUDP(ctx, pc) }()
		}
	}
	if s.cfg.TCP != "" {
		ln, err := net.Listen("tcp", s.cfg.TCP)
		if err != nil {
			s.fail("TCP " + s.cfg.TCP + " : " + err.Error())
		} else {
			s.listen("TCP " + ln.Addr().String())
			wg.Add(1)
			go func() { defer wg.Done(); s.serveTCP(ctx, ln) }()
		}
	}
	<-ctx.Done()
	wg.Wait()
}

func (s *Server) fail(msg string) {
	s.mu.Lock()
	s.errs = append(s.errs, msg)
	s.mu.Unlock()
	if s.log != nil {
		s.log.Warn("réception syslog impossible", "écoute", msg)
	}
}

func (s *Server) listen(what string) {
	s.mu.Lock()
	s.listening = append(s.listening, what)
	s.mu.Unlock()
}

// Addrs rend les adresses effectivement écoutées (tests, état).
func (s *Server) Addrs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.listening...)
}

// State : état du collecteur.
func (s *Server) State() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case len(s.errs) > 0 && len(s.listening) == 0:
		return "erreur : " + strings.Join(s.errs, " ; ")
	case len(s.errs) > 0:
		return "partiel : " + strings.Join(s.errs, " ; ")
	}
	return "ok"
}

func (s *Server) serveUDP(ctx context.Context, pc net.PacketConn) {
	go func() { <-ctx.Done(); pc.Close() }()
	buf := make([]byte, maxFrame)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		ip := ""
		if ua, ok := addr.(*net.UDPAddr); ok {
			ip = ua.IP.String()
		}
		s.handle(string(buf[:n]), ip, time.Now())
	}
}

func (s *Server) serveTCP(ctx context.Context, ln net.Listener) {
	var wg sync.WaitGroup
	go func() { <-ctx.Done(); ln.Close() }()
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				wg.Wait()
				return
			}
			time.Sleep(100 * time.Millisecond)
			continue
		}
		s.mu.Lock()
		full := s.conns >= s.cfg.MaxConnections
		if !full {
			s.conns++
		}
		s.mu.Unlock()
		if full {
			s.drop("connexions")
			c.Close()
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				s.mu.Lock()
				s.conns--
				s.mu.Unlock()
			}()
			s.serveConn(ctx, c)
		}()
	}
}

func (s *Server) serveConn(ctx context.Context, c net.Conn) {
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	ip := ""
	if ta, ok := c.RemoteAddr().(*net.TCPAddr); ok {
		ip = ta.IP.String()
	}
	if !s.admitted(ip) {
		s.drop("source refusée")
		return
	}
	r := bufio.NewReaderSize(c, maxFrame)
	for {
		_ = c.SetReadDeadline(time.Now().Add(idleTimeout))
		frame, err := readFrame(r)
		if frame != "" {
			s.handle(frame, ip, time.Now())
		}
		if err != nil {
			if errors.Is(err, errTooLong) {
				s.drop("trame trop longue")
				return
			}
			return
		}
	}
}

var errTooLong = errors.New("trame trop longue")

// readFrame lit une trame : « LONGUEUR SP MESSAGE » (octet-counting, RFC 6587 § 3.4.1) si elle
// commence par un chiffre, sinon jusqu'à la fin de ligne.
func readFrame(r *bufio.Reader) (string, error) {
	b, err := r.Peek(1)
	if err != nil {
		return "", err
	}
	if b[0] >= '1' && b[0] <= '9' {
		// Chiffres puis espace : octet-counting. Peek octet par octet : l'émetteur n'envoie
		// peut-être rien de plus que cette trame.
		for n := 2; n <= 8; n++ {
			head, err := r.Peek(n)
			if err != nil {
				return "", err
			}
			c := head[n-1]
			if c == ' ' {
				size, _ := strconv.Atoi(string(head[:n-1]))
				if size > maxFrame {
					return "", errTooLong
				}
				if _, err := r.Discard(n); err != nil {
					return "", err
				}
				buf := make([]byte, size)
				if _, err := io.ReadFull(r, buf); err != nil {
					return "", err
				}
				return string(buf), nil
			}
			if c < '0' || c > '9' {
				break
			}
		}
	}
	var line []byte
	for {
		chunk, err := r.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > maxFrame {
			return "", errTooLong
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			return strings.TrimRight(string(line), "\r\n\x00"), err
		}
		return strings.TrimRight(string(line), "\r\n\x00"), nil
	}
}

func (s *Server) admitted(ip string) bool {
	if len(s.allowed) == 0 {
		return true
	}
	p := net.ParseIP(ip)
	for _, n := range s.allowed {
		if p != nil && n.Contains(p) {
			return true
		}
	}
	return false
}

func (s *Server) drop(reason string) {
	s.mu.Lock()
	s.dropped[reason]++
	s.mu.Unlock()
}

// allow : seau de jetons (rate_limit lignes par seconde, rafale du même ordre).
func (s *Server) allow(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	limit := float64(s.cfg.RateLimit)
	if !s.last.IsZero() {
		s.tokens = min(limit, s.tokens+now.Sub(s.last).Seconds()*limit)
	}
	s.last = now
	if s.tokens < 1 {
		return false
	}
	s.tokens--
	return true
}

func (s *Server) parserFor(ip, host string) string {
	p := net.ParseIP(ip)
	h := strings.ToLower(host)
	for _, r := range s.parsers {
		if r.net != nil && p != nil && r.net.Contains(p) {
			return r.parser
		}
		if r.glob != "" && h != "" {
			if ok, _ := path.Match(r.glob, h); ok {
				return r.parser
			}
		}
	}
	return ""
}

// handle décode une ligne et la confie à la file des journaux.
func (s *Server) handle(raw, ip string, now time.Time) {
	if strings.TrimSpace(raw) == "" {
		return
	}
	if !s.admitted(ip) {
		s.drop("source refusée")
		return
	}
	if !s.allow(now) {
		s.drop("débit")
		return
	}
	l := Build(Parse(raw), raw, ip, now, s.cfg.Service)
	host := l.Tags["syslog_host"]
	for k, v := range s.cfg.Tags {
		l.Tags[k] = v
	}
	if p := s.parserFor(ip, host); p != "" {
		l.Attributes["syslog"].(map[string]any)["parser"] = p
	}
	if !s.submit(logs.Entry{Log: l}) {
		s.drop("file pleine")
		return
	}
	s.mu.Lock()
	if _, known := s.seen[host]; !known && len(s.seen) >= maxHosts {
		host = "autres"
	}
	s.counts[host]++
	s.seen[host] = now
	s.mu.Unlock()
}

// Build : ligne de journal HYPHA d'un message décodé. Horodatage = réception (l'horloge d'un
// équipement peut être fausse, et RFC 3164 n'a ni année ni fuseau) ; celui du message est
// gardé dans l'attribut syslog.timestamp.
func Build(m Message, raw, ip string, now time.Time, defService string) model.Log {
	host := m.Hostname
	if host == "" {
		host = ip
	}
	text := m.Text
	if strings.TrimSpace(text) == "" {
		text = strings.TrimRight(raw, "\r\n\x00")
	}
	attrs := map[string]any{
		"facility": FacilityName(m.Facility), "severity": m.Severity,
	}
	for k, v := range map[string]string{"hostname": m.Hostname, "appname": m.AppName, "procid": m.ProcID,
		"msgid": m.MsgID, "timestamp": m.Timestamp} {
		if v != "" {
			attrs[k] = v
		}
	}
	if m.Version > 0 {
		attrs["version"] = m.Version
	}
	if len(m.SD) > 0 {
		attrs["structured_data"] = m.SD
	}
	service := m.AppName
	if service == "" {
		service = defService
	}
	l := model.Log{
		TS: model.Ms(now), Message: text, Status: logs.SyslogStatus(m.Severity), Service: model.Truncate(service, 100),
		Source: "syslog", Attributes: map[string]any{"syslog": attrs}, Tags: map[string]string{"syslog_host": model.Truncate(host, model.MaxTagValue)},
	}
	if ip != "" {
		l.Attributes["network"] = map[string]any{"client": map[string]any{"ip": ip}}
	}
	return l
}

// Flush : mesures et contrôle depuis le dernier relevé (appelé à chaque intervalle de l'agent).
func (s *Server) Flush(now time.Time, tags model.TagFunc) ([]model.Series, []model.Check) {
	s.mu.Lock()
	counts, dropped := s.counts, s.dropped
	s.counts, s.dropped = map[string]float64{}, map[string]float64{}
	for h, t := range s.seen {
		if now.Sub(t) > silentAfter {
			delete(s.seen, h)
			continue
		}
		if _, ok := counts[h]; !ok {
			counts[h] = 0 // émetteur connu qui se tait : 0, pour que « muet » se voie
		}
	}
	conns := s.conns
	listening, errs := append([]string{}, s.listening...), append([]string{}, s.errs...)
	s.mu.Unlock()

	b := model.NewSeriesBuilder(model.Ms(now), tags)
	hosts := make([]string, 0, len(counts))
	for h := range counts {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	for _, h := range hosts {
		b.Add("syslog.messages", model.TypeCount, "", counts[h], "syslog_host", h)
	}
	for _, r := range model.SortedKeys(dropped) {
		b.Add("syslog.dropped", model.TypeCount, "", dropped[r], "reason", r)
	}
	if s.cfg.TCP != "" {
		b.Gauge("syslog.connections", "connection", float64(conns))
	}
	st, msg := model.CheckOK, "écoute "+strings.Join(listening, ", ")
	switch {
	case len(errs) > 0 && len(listening) == 0:
		st, msg = model.CheckCrit, "aucune écoute : "+strings.Join(errs, " ; ")
	case len(errs) > 0:
		st, msg = model.CheckWarn, msg+" ; en échec : "+strings.Join(errs, " ; ")
	}
	return b.Series(), []model.Check{{TS: model.Ms(now), Name: "syslog.can_receive", Status: st, Message: msg, Tags: tags()}}
}

// String : résumé de la conf (journal de démarrage).
func (s *Server) String() string {
	return fmt.Sprintf("syslog udp=%q tcp=%q", s.cfg.UDP, s.cfg.TCP)
}
