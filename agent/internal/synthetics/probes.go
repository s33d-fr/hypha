package synthetics

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	userAgent = "HYPHA-Synthetics/0.4 (agent ; +https://hypha.s33d.fr)"
	maxBody   = 1 << 20
)

// splitHostPort lit « hôte:port », « [v6]:port », ou une URL ; port par défaut sinon.
func splitHostPort(target string, def int) (string, int, error) {
	t := strings.TrimSpace(target)
	if i := strings.Index(t, "://"); i >= 0 {
		t = t[i+3:]
	}
	if i := strings.Index(t, "/"); i >= 0 {
		t = t[:i]
	}
	host, port := t, ""
	switch {
	case strings.HasPrefix(t, "["):
		h, rest, _ := strings.Cut(t[1:], "]")
		host, port = h, strings.TrimPrefix(rest, ":")
	case strings.Count(t, ":") == 1:
		host, port, _ = strings.Cut(t, ":")
	}
	p := def
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil {
			return "", 0, fmt.Errorf("port invalide : %s", port)
		}
		p = n
	}
	if p != 0 && (p < 1 || p > 65535) {
		return "", 0, fmt.Errorf("port hors bornes : %d", p)
	}
	return host, p, nil
}

// httpOutcome : une requête HTTP menée jusqu'à la réponse finale.
type httpOutcome struct {
	status   int
	header   http.Header
	body     []byte
	latency  time.Duration
	details  map[string]any
	tlsState *tls.ConnectionState
}

// doHTTP envoie la requête, suit les redirections à la main (10 au plus, 303 et POST
// redirigé en 301/302 deviennent GET, comme le serveur) et mesure la dernière étape.
func doHTTP(ctx context.Context, method, rawURL string, headers map[string]string, body string, follow bool) (*httpOutcome, error) {
	tr := &http.Transport{
		Proxy:             nil, // un emplacement privé vise le réseau de l'hôte, sans mandataire
		TLSClientConfig:   &tls.Config{MinVersion: tls.VersionTLS12},
		DisableKeepAlives: true,
		ForceAttemptHTTP2: true,
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	details := map[string]any{"redirects": 0}
	start := time.Now()
	cur := rawURL
	redirects := 0
	for {
		u, err := url.Parse(cur)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("URL refusée : %s", cur)
		}
		var rd io.Reader
		if body != "" {
			rd = strings.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, cur, rd)
		if err != nil {
			return nil, fmt.Errorf("URL refusée : %s", cur)
		}
		req.Header.Set("User-Agent", userAgent)
		for k, v := range headers {
			req.Header.Set(k, v)
			if strings.EqualFold(k, "host") {
				req.Host = v
			}
		}
		var dnsS, dnsE, conS, conE, tlsS, tlsE, wrote, first time.Time
		trace := &httptrace.ClientTrace{
			DNSStart:             func(httptrace.DNSStartInfo) { dnsS = time.Now() },
			DNSDone:              func(httptrace.DNSDoneInfo) { dnsE = time.Now() },
			ConnectStart:         func(string, string) { conS = time.Now() },
			ConnectDone:          func(string, string, error) { conE = time.Now() },
			TLSHandshakeStart:    func() { tlsS = time.Now() },
			TLSHandshakeDone:     func(tls.ConnectionState, error) { tlsE = time.Now() },
			WroteHeaders:         func() { wrote = time.Now() },
			GotFirstResponseByte: func() { first = time.Now() },
		}
		req = req.WithContext(httptrace.WithClientTrace(ctx, trace))
		resp, err := client.Do(req)
		if err != nil {
			return &httpOutcome{latency: time.Since(start), details: details}, classifyNetErr(ctx, err)
		}
		var buf bytes.Buffer
		n, rerr := io.Copy(&buf, io.LimitReader(resp.Body, maxBody))
		extra, _ := io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		done := time.Now()
		if rerr != nil {
			return &httpOutcome{latency: time.Since(start), details: details}, classifyNetErr(ctx, rerr)
		}
		loc := resp.Header.Get("Location")
		if follow && isRedirect(resp.StatusCode) && loc != "" && redirects < 10 {
			next, err := u.Parse(loc)
			if err != nil {
				return nil, fmt.Errorf("redirection illisible : %s", loc)
			}
			cur = next.String()
			redirects++
			details["redirects"] = redirects
			if resp.StatusCode == http.StatusSeeOther || (method == http.MethodPost && (resp.StatusCode == 301 || resp.StatusCode == 302)) {
				method, body = http.MethodGet, ""
			}
			continue
		}
		span := func(a, b time.Time) any {
			if a.IsZero() || b.IsZero() {
				return nil
			}
			return round2(float64(b.Sub(a).Microseconds()) / 1000)
		}
		details["dns_ms"] = span(dnsS, dnsE)
		details["connect_ms"] = span(conS, conE)
		details["tls_ms"] = span(tlsS, tlsE)
		details["ttfb_ms"] = span(wrote, first)
		details["download_ms"] = span(first, done)
		details["body_bytes"] = n + extra
		details["final_url"] = cur
		details["http_version"] = resp.Proto
		return &httpOutcome{status: resp.StatusCode, header: resp.Header, body: buf.Bytes(),
			latency: done.Sub(start), details: details, tlsState: resp.TLS}, nil
	}
}

func isRedirect(code int) bool {
	switch code {
	case 301, 302, 303, 307, 308:
		return true
	}
	return false
}

// classifyNetErr traduit une erreur réseau dans les termes des sondes du serveur.
func classifyNetErr(ctx context.Context, err error) error {
	var dnsErr *net.DNSError
	var certErr *tls.CertificateVerificationError
	var uaErr x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	var invErr x509.CertificateInvalidError
	switch {
	case errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil:
		return errors.New("délai dépassé")
	case errors.As(err, &dnsErr):
		return fmt.Errorf("résolution DNS impossible : %v", dnsErr)
	case errors.As(err, &certErr), errors.As(err, &uaErr), errors.As(err, &hostErr), errors.As(err, &invErr):
		return fmt.Errorf("certificat refusé : %v", err)
	}
	return fmt.Errorf("connexion impossible : %v", err)
}

func statusOK(code int, expect any, follow bool) bool {
	switch e := expect.(type) {
	case nil:
		if follow {
			return code >= 200 && code < 300
		}
		return code >= 200 && code < 400
	case float64:
		return code == int(e)
	case []any:
		for _, x := range e {
			if f, ok := x.(float64); ok && int(f) == code {
				return true
			}
		}
		return false
	}
	return false
}

func certDays(cs *tls.ConnectionState) (float64, int64, bool) {
	if cs == nil || len(cs.PeerCertificates) == 0 {
		return 0, 0, false
	}
	na := cs.PeerCertificates[0].NotAfter
	return float64(int64(time.Until(na).Hours()/24*10)) / 10, na.UnixMilli(), true
}

func tlsVersionName(v uint16) string {
	switch v {
	case tls.VersionTLS10:
		return "TLSv1"
	case tls.VersionTLS11:
		return "TLSv1.1"
	case tls.VersionTLS12:
		return "TLSv1.2"
	case tls.VersionTLS13:
		return "TLSv1.3"
	}
	return fmt.Sprintf("0x%04x", v)
}

// probeHTTP : statut attendu (expect_status, nul = 2xx), texte attendu, échéance du
// certificat (ssl_days_warn), et config.assertions (§ 29).
func probeHTTP(ctx context.Context, target string, cfg map[string]any) Result {
	method := strings.ToUpper(cfgString(cfg, "method", "GET"))
	follow := cfgBool(cfg, "follow_redirects", true)
	headers := map[string]string{}
	if h, ok := cfg["headers"].(map[string]any); ok {
		for k, v := range h {
			if s, ok := v.(string); ok {
				headers[k] = s
			}
		}
	}
	timeout := time.Duration(cfgFloat(cfg, "timeout_s", 10) * float64(time.Second))
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := doHTTP(rctx, method, strings.TrimSpace(target), headers, cfgString(cfg, "body", ""), follow)
	if err != nil {
		r := Result{Error: err.Error()}
		if out != nil {
			r.Details = out.details
			if err.Error() == "délai dépassé" {
				r.Error = fmt.Sprintf("délai dépassé (%g s)", timeout.Seconds())
				r.LatencyMS = ptrMS(out.latency)
			}
		}
		return r
	}
	var errs []string
	expect, has := cfg["expect_status"]
	if !has {
		expect = float64(200)
	}
	if !statusOK(out.status, expect, follow) {
		msg := fmt.Sprintf("statut %d", out.status)
		if expect != nil {
			msg += fmt.Sprintf(" (attendu %v)", fmtExpect(expect))
		}
		errs = append(errs, msg)
	}
	if text := cfgString(cfg, "expect_text", ""); text != "" && !bytes.Contains(out.body, []byte(text)) {
		errs = append(errs, "texte attendu absent de la réponse")
	}
	if days, na, ok := certDays(out.tlsState); ok {
		out.details["ssl_days_left"] = days
		out.details["ssl_not_after"] = na
		out.details["tls_version"] = tlsVersionName(out.tlsState.Version)
		if warn, ok := cfg["ssl_days_warn"].(float64); ok && days < warn {
			errs = append(errs, fmt.Sprintf("certificat expirant dans %g jours", days))
		}
	}
	if as := parseAssertions(cfg["assertions"]); len(as) > 0 {
		res, failed := evalAssertions(as, out)
		out.details["assertions"] = res
		errs = append(errs, failed...)
	}
	r := Result{OK: len(errs) == 0, StatusCode: ptrInt(out.status), LatencyMS: ptrMS(out.latency), Details: out.details}
	r.Error = strings.Join(errs, "; ")
	return r
}

func fmtExpect(e any) string {
	switch v := e.(type) {
	case float64:
		return strconv.Itoa(int(v))
	case []any:
		var parts []string
		for _, x := range v {
			parts = append(parts, fmtExpect(x))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	return fmt.Sprint(e)
}

func dialTimeout(cfg map[string]any, def float64) time.Duration {
	return time.Duration(cfgFloat(cfg, "timeout_s", def) * float64(time.Second))
}

// resolveFirst résout l'hôte et rend la première adresse et la durée de résolution.
func resolveFirst(ctx context.Context, host string) (string, float64, error) {
	t0 := time.Now()
	if ip := net.ParseIP(host); ip != nil {
		return ip.String(), 0, nil
	}
	addrs, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil || len(addrs) == 0 {
		if err == nil {
			err = errors.New("aucune adresse")
		}
		return "", msSince(t0), fmt.Errorf("résolution DNS impossible : %v", err)
	}
	return addrs[0], msSince(t0), nil
}

// probeTCP : ouverture de connexion.
func probeTCP(ctx context.Context, target string, cfg map[string]any) Result {
	timeout := dialTimeout(cfg, 10)
	details := map[string]any{}
	start := time.Now()
	host, port, err := splitHostPort(target, 0)
	if err != nil || port == 0 {
		return Result{Error: "connexion impossible : target doit être hôte:port"}
	}
	addr, dnsMS, err := resolveFirst(ctx, host)
	details["dns_ms"] = dnsMS
	if err != nil {
		return Result{Error: err.Error(), Details: details}
	}
	t0 := time.Now()
	d := net.Dialer{Timeout: timeout}
	c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(addr, strconv.Itoa(port)))
	if err != nil {
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			return Result{Error: fmt.Sprintf("délai dépassé (%g s)", timeout.Seconds()), Details: details}
		}
		return Result{Error: "connexion impossible : " + err.Error(), Details: details}
	}
	details["connect_ms"] = msSince(t0)
	details["address"] = addr
	c.Close()
	return Result{OK: true, LatencyMS: ptrMS(time.Since(start)), Details: details}
}

// probeSSL : poignée de main TLS vérifiée, échéance (ssl_days_warn, 21 j par défaut) ; un
// certificat refusé donne quand même son échéance, lue sans vérification.
func probeSSL(ctx context.Context, target string, cfg map[string]any) Result {
	if proto := cfgString(cfg, "starttls", ""); proto == "smtp" || proto == "imap" || proto == "pop3" {
		// Port de messagerie en clair au départ (25, 587, 143, 110) : STARTTLS, puis le certificat.
		c := make(map[string]any, len(cfg)+1)
		for k, v := range cfg {
			c[k] = v
		}
		c["tls"] = "starttls"
		return probeMail(ctx, proto, target, c)
	}
	timeout := dialTimeout(cfg, 10)
	warn := cfgFloat(cfg, "ssl_days_warn", 21)
	details := map[string]any{}
	start := time.Now()
	host, port, err := splitHostPort(target, 443)
	if err != nil {
		return Result{Error: "poignée de main TLS impossible : " + err.Error()}
	}
	addr, dnsMS, err := resolveFirst(ctx, host)
	details["dns_ms"] = dnsMS
	if err != nil {
		return Result{Error: err.Error(), Details: details}
	}
	dst := net.JoinHostPort(addr, strconv.Itoa(port))
	t0 := time.Now()
	d := net.Dialer{Timeout: timeout}
	raw, err := d.DialContext(ctx, "tcp", dst)
	if err != nil {
		return Result{Error: "poignée de main TLS impossible : " + err.Error(), Details: details}
	}
	details["connect_ms"] = msSince(t0)
	raw.SetDeadline(time.Now().Add(timeout))
	t1 := time.Now()
	conn := tls.Client(raw, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS10})
	err = conn.HandshakeContext(ctx)
	if err != nil {
		conn.Close()
		if isCertError(err) {
			if days, na, ok := unverifiedDays(ctx, dst, host, timeout); ok {
				details["ssl_days_left"], details["ssl_not_after"] = days, na
			}
			return Result{Error: "certificat refusé : " + err.Error(), Details: details}
		}
		return Result{Error: "poignée de main TLS impossible : " + err.Error(), Details: details}
	}
	details["tls_ms"] = msSince(t1)
	cs := conn.ConnectionState()
	conn.Close()
	days, na, _ := certDays(&cs)
	details["ssl_days_left"], details["ssl_not_after"] = days, na
	details["tls_version"] = tlsVersionName(cs.Version)
	details["cipher"] = tls.CipherSuiteName(cs.CipherSuite)
	if len(cs.PeerCertificates) > 0 {
		details["issuer"] = cs.PeerCertificates[0].Issuer.String()
		details["subject"] = cs.PeerCertificates[0].Subject.String()
	}
	latency := time.Since(start)
	r := Result{OK: true, LatencyMS: ptrMS(latency), Details: details}
	if days < warn {
		r.OK, r.Error = false, fmt.Sprintf("certificat expirant dans %g jours", days)
	}
	if as := parseAssertions(cfg["assertions"]); len(as) > 0 {
		res, failed := evalAssertions(as, &httpOutcome{latency: latency, details: details})
		details["assertions"] = res
		if len(failed) > 0 {
			r.OK = false
			r.Error = strings.Trim(r.Error+"; "+strings.Join(failed, "; "), "; ")
		}
	}
	return r
}

func isCertError(err error) bool {
	var certErr *tls.CertificateVerificationError
	var uaErr x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	var invErr x509.CertificateInvalidError
	return errors.As(err, &certErr) || errors.As(err, &uaErr) || errors.As(err, &hostErr) || errors.As(err, &invErr)
}

func unverifiedDays(ctx context.Context, dst, host string, timeout time.Duration) (float64, int64, bool) {
	d := net.Dialer{Timeout: timeout}
	raw, err := d.DialContext(ctx, "tcp", dst)
	if err != nil {
		return 0, 0, false
	}
	raw.SetDeadline(time.Now().Add(timeout))
	c := tls.Client(raw, &tls.Config{ServerName: host, InsecureSkipVerify: true}) // lecture de l'échéance seulement
	defer c.Close()
	if err := c.HandshakeContext(ctx); err != nil {
		return 0, 0, false
	}
	cs := c.ConnectionState()
	return certDays(&cs)
}
