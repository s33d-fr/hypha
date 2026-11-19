package synthetics

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// Sondes de messagerie (contrat § 32), comme celles du serveur : smtp et imap (bannière,
// capacités, STARTTLS ou TLS implicite, certificat vérifié), et le test ssl d'un port de
// messagerie en clair au départ (config.starttls : smtp, imap, pop3).

const ehloName = "hypha-synthetics.s33d.fr"

var mailDefaultPorts = map[string]int{"smtp": 25, "imap": 993, "pop3": 110}

type mailSession struct {
	conn net.Conn
	rd   *bufio.Reader
	t    time.Duration
}


type refusal struct{ msg string }

func (r refusal) Error() string { return r.msg }

func (s *mailSession) line() (string, error) {
	s.conn.SetReadDeadline(time.Now().Add(s.t))
	l, err := s.rd.ReadString('\n')
	if err != nil && l == "" {
		return "", fmt.Errorf("connexion fermée par le serveur : %w", err)
	}
	return strings.TrimRight(l, "\r\n"), nil
}

func (s *mailSession) send(cmd string) error {
	s.conn.SetWriteDeadline(time.Now().Add(s.t))
	_, err := s.conn.Write([]byte(cmd + "\r\n"))
	return err
}

func (s *mailSession) smtpReply() (int, []string, error) {
	var lines []string
	for i := 0; i < 200; i++ {
		l, err := s.line()
		if err != nil {
			return 0, nil, err
		}
		lines = append(lines, l)
		if len(l) < 4 || l[3] != '-' {
			code, err := strconv.Atoi(l[:min(3, len(l))])
			if err != nil {
				return 0, nil, fmt.Errorf("réponse SMTP illisible : %.80s", l)
			}
			out := make([]string, len(lines))
			for j, x := range lines {
				if len(x) > 4 {
					out[j] = x[4:]
				}
			}
			return code, out, nil
		}
	}
	return 0, nil, errors.New("réponse SMTP trop longue")
}

func (s *mailSession) imapCmd(tag, cmd string) (string, []string, error) {
	if err := s.send(tag + " " + cmd); err != nil {
		return "", nil, err
	}
	var untagged []string
	for i := 0; i < 500; i++ {
		l, err := s.line()
		if err != nil {
			return "", nil, err
		}
		if strings.HasPrefix(l, tag+" ") {
			return l[len(tag)+1:], untagged, nil
		}
		untagged = append(untagged, l)
	}
	return "", nil, errors.New("réponse IMAP trop longue")
}

func (s *mailSession) greeting(proto string) (string, error) {
	if proto == "smtp" {
		code, lines, err := s.smtpReply()
		if err != nil {
			return "", err
		}
		banner := ""
		if len(lines) > 0 {
			banner = truncate(lines[0], 200)
		}
		if code != 220 {
			return banner, refusal{fmt.Sprintf("bannière SMTP %d : %s", code, banner)}
		}
		return banner, nil
	}
	l, err := s.line()
	if err != nil {
		return "", err
	}
	ok := strings.HasPrefix(l, "* OK")
	if proto == "pop3" {
		ok = strings.HasPrefix(l, "+OK")
	}
	if !ok {
		return l, refusal{fmt.Sprintf("accueil %s inattendu : %.80s", strings.ToUpper(proto), l)}
	}
	return truncate(l, 200), nil
}

func (s *mailSession) capabilities(proto, ehlo, tag string) ([]string, error) {
	switch proto {
	case "smtp":
		if err := s.send("EHLO " + ehlo); err != nil {
			return nil, err
		}
		code, lines, err := s.smtpReply()
		if err != nil {
			return nil, err
		}
		if code != 250 {
			return nil, refusal{fmt.Sprintf("EHLO refusé (%d)", code)}
		}
		var caps, auth []string
		for _, l := range lines[1:] {
			f := strings.Fields(l)
			if len(f) == 0 {
				continue
			}
			caps = append(caps, strings.ToUpper(f[0]))
			if strings.EqualFold(f[0], "AUTH") {
				for _, m := range f[1:] {
					auth = append(auth, "AUTH="+strings.ToUpper(m))
				}
			}
		}
		return append(caps, auth...), nil
	case "imap":
		status, untagged, err := s.imapCmd(tag, "CAPABILITY")
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(strings.ToUpper(status), "OK") {
			return nil, refusal{fmt.Sprintf("CAPABILITY refusé : %.80s", status)}
		}
		for _, l := range untagged {
			if strings.HasPrefix(strings.ToUpper(l), "* CAPABILITY") {
				f := strings.Fields(l)[2:]
				for i := range f {
					f[i] = strings.ToUpper(f[i])
				}
				return f, nil
			}
		}
	}
	return nil, nil
}

// prelude : conversation en clair jusqu'au feu vert pour la poignée de main TLS.
func (s *mailSession) prelude(proto, ehlo string, details map[string]any) error {
	banner, err := s.greeting(proto)
	details["banner"] = banner
	if err != nil {
		return err
	}
	if proto == "pop3" {
		if err := s.send("STLS"); err != nil {
			return err
		}
		l, err := s.line()
		if err != nil {
			return err
		}
		if !strings.HasPrefix(l, "+OK") {
			return refusal{fmt.Sprintf("STLS refusé : %.80s", l)}
		}
		return nil
	}
	caps, err := s.capabilities(proto, ehlo, "h1")
	if err != nil {
		return err
	}
	details["capabilities"] = caps
	has := false
	for _, c := range caps {
		has = has || c == "STARTTLS"
	}
	if !has {
		return refusal{"STARTTLS non proposé par le serveur"}
	}
	if proto == "smtp" {
		if err := s.send("STARTTLS"); err != nil {
			return err
		}
		code, lines, err := s.smtpReply()
		if err != nil {
			return err
		}
		if code != 220 {
			last := ""
			if len(lines) > 0 {
				last = lines[len(lines)-1]
			}
			return refusal{fmt.Sprintf("STARTTLS refusé (%d %.80s)", code, last)}
		}
		return nil
	}
	status, _, err := s.imapCmd("h2", "STARTTLS")
	if err != nil {
		return err
	}
	if !strings.HasPrefix(strings.ToUpper(status), "OK") {
		return refusal{fmt.Sprintf("STARTTLS refusé : %.80s", status)}
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func mailTLSMode(cfg map[string]any, port int) string {
	m := cfgString(cfg, "tls", "auto")
	if m == "auto" {
		if port == 465 || port == 993 || port == 995 {
			return "implicit"
		}
		return "starttls"
	}
	return m
}

// mailUnverified : échéance d'un certificat refusé, lue sans vérification (STARTTLS rejoué).
func mailUnverified(ctx context.Context, dst, name, proto, mode, ehlo string, timeout time.Duration) (float64, int64, bool) {
	d := net.Dialer{Timeout: timeout}
	raw, err := d.DialContext(ctx, "tcp", dst)
	if err != nil {
		return 0, 0, false
	}
	defer raw.Close()
	if mode == "starttls" {
		s := &mailSession{conn: raw, rd: bufio.NewReader(raw), t: timeout}
		if s.prelude(proto, ehlo, map[string]any{}) != nil {
			return 0, 0, false
		}
	}
	raw.SetDeadline(time.Now().Add(timeout))
	c := tls.Client(raw, &tls.Config{ServerName: name, InsecureSkipVerify: true}) // lecture de l'échéance seulement
	if c.HandshakeContext(ctx) != nil {
		return 0, 0, false
	}
	cs := c.ConnectionState()
	return certDays(&cs)
}

func probeMail(ctx context.Context, proto, target string, cfg map[string]any) Result {
	timeout := dialTimeout(cfg, 10)
	warn := cfgFloat(cfg, "ssl_days_warn", 21)
	ehlo := cfgString(cfg, "ehlo_name", ehloName)
	details := map[string]any{"protocol": proto}
	start := time.Now()
	host, port, err := splitHostPort(target, mailDefaultPorts[proto])
	if err != nil {
		return Result{Error: "échange " + strings.ToUpper(proto) + " impossible : " + err.Error()}
	}
	mode := mailTLSMode(cfg, port)
	name := cfgString(cfg, "tls_server_name", host)
	details["tls_mode"] = mode
	addr, dnsMS, err := resolveFirst(ctx, host)
	details["dns_ms"] = dnsMS
	if err != nil {
		return Result{Error: err.Error(), Details: details}
	}
	details["address"] = addr
	dst := net.JoinHostPort(addr, strconv.Itoa(port))
	t0 := time.Now()
	d := net.Dialer{Timeout: timeout}
	raw, err := d.DialContext(ctx, "tcp", dst)
	if err != nil {
		return Result{Error: "connexion impossible : " + err.Error(), Details: details}
	}
	details["connect_ms"] = msSince(t0)
	conn := net.Conn(raw)
	defer func() { conn.Close() }()
	s := &mailSession{conn: raw, rd: bufio.NewReader(raw), t: timeout}
	handshake := func() error {
		t1 := time.Now()
		raw.SetDeadline(time.Now().Add(timeout))
		tc := tls.Client(raw, &tls.Config{ServerName: name, MinVersion: tls.VersionTLS10})
		if err := tc.HandshakeContext(ctx); err != nil {
			return err
		}
		details["tls_ms"] = msSince(t1)
		cs := tc.ConnectionState()
		days, na, _ := certDays(&cs)
		details["ssl_days_left"], details["ssl_not_after"] = days, na
		details["tls_version"] = tlsVersionName(cs.Version)
		details["cipher"] = tls.CipherSuiteName(cs.CipherSuite)
		if len(cs.PeerCertificates) > 0 {
			details["issuer"] = cs.PeerCertificates[0].Issuer.String()
			details["subject"] = cs.PeerCertificates[0].Subject.String()
		}
		conn = tc
		s = &mailSession{conn: tc, rd: bufio.NewReader(tc), t: timeout}
		return nil
	}
	fail := func(err error) Result {
		var r refusal
		switch {
		case errors.As(err, &r):
			return Result{Error: r.msg, Details: details}
		case isCertError(err):
			if days, na, ok := mailUnverified(ctx, dst, name, proto, mode, ehlo, timeout); ok {
				details["ssl_days_left"], details["ssl_not_after"] = days, na
			}
			return Result{Error: "certificat refusé : " + err.Error(), Details: details}
		}
		return Result{Error: "échange " + strings.ToUpper(proto) + " impossible : " + err.Error(), Details: details}
	}
	switch mode {
	case "implicit":
		if err := handshake(); err != nil {
			return fail(err)
		}
		banner, err := s.greeting(proto)
		details["banner"] = banner
		if err != nil {
			return fail(err)
		}
		caps, err := s.capabilities(proto, ehlo, "h1")
		if err != nil {
			return fail(err)
		}
		details["capabilities"] = caps
	case "starttls":
		t1 := time.Now()
		if err := s.prelude(proto, ehlo, details); err != nil {
			return fail(err)
		}
		details["banner_ms"] = msSince(t1)
		if err := handshake(); err != nil {
			return fail(err)
		}
		caps, err := s.capabilities(proto, ehlo, "h3")
		if err != nil {
			return fail(err)
		}
		details["capabilities_tls"] = caps
	default:
		banner, err := s.greeting(proto)
		details["banner"] = banner
		if err != nil {
			return fail(err)
		}
		caps, err := s.capabilities(proto, ehlo, "h1")
		if err != nil {
			return fail(err)
		}
		details["capabilities"] = caps
	}
	if proto == "imap" {
		s.send("z LOGOUT")
	} else {
		s.send("QUIT")
	}
	latency := time.Since(start)
	r := Result{OK: true, LatencyMS: ptrMS(latency), Details: details}
	var errs []string
	if days, ok := details["ssl_days_left"].(float64); ok && days < warn {
		errs = append(errs, fmt.Sprintf("certificat expirant dans %g jours", days))
	}
	if want := cfgString(cfg, "expect_banner", ""); want != "" {
		if b, _ := details["banner"].(string); !strings.Contains(strings.ToLower(b), strings.ToLower(want)) {
			errs = append(errs, fmt.Sprintf("« %s » absent de la bannière", want))
		}
	}
	if as := parseAssertions(cfg["assertions"]); len(as) > 0 {
		res, failed := evalAssertions(as, &httpOutcome{latency: latency, details: details})
		details["assertions"] = res
		errs = append(errs, failed...)
	}
	if len(errs) > 0 {
		r.OK, r.Error = false, strings.Join(errs, "; ")
	}
	return r
}
