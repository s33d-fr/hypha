package synthetics

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// Types admis (mêmes que le serveur).
var dnsTypes = map[string]dnsmessage.Type{
	"A": dnsmessage.TypeA, "AAAA": dnsmessage.TypeAAAA, "CNAME": dnsmessage.TypeCNAME, "MX": dnsmessage.TypeMX,
	"TXT": dnsmessage.TypeTXT, "NS": dnsmessage.TypeNS, "SOA": dnsmessage.TypeSOA, "CAA": dnsmessage.Type(257),
	"SRV": dnsmessage.TypeSRV, "PTR": dnsmessage.TypePTR,
}

// probeDNS : requête du type demandé au serveur nameserver (sinon le premier serveur du
// système), réponses écrites comme dnspython (pour un verdict identique au serveur).
func probeDNS(ctx context.Context, target string, cfg map[string]any) Result {
	timeout := dialTimeout(cfg, 10)
	rtype := strings.ToUpper(cfgString(cfg, "record_type", "A"))
	qt, ok := dnsTypes[rtype]
	if !ok {
		return Result{Error: "erreur DNS : type " + rtype + " non pris en charge"}
	}
	ns := cfgString(cfg, "nameserver", "")
	if ns == "" {
		list := systemNameservers()
		if len(list) == 0 {
			return Result{Error: "erreur DNS : aucun serveur DNS système"}
		}
		ns = list[0]
	}
	name := strings.TrimSuffix(strings.TrimSpace(target), ".") + "."
	t0 := time.Now()
	records, ttl, rcode, err := dnsQuery(ctx, ns, name, qt, timeout)
	latency := time.Since(t0)
	switch {
	case err != nil && errors.Is(err, errDNSTimeout):
		return Result{Error: fmt.Sprintf("délai dépassé (%g s)", timeout.Seconds())}
	case err != nil:
		return Result{Error: "erreur DNS : " + err.Error()}
	case rcode == dnsmessage.RCodeNameError:
		return Result{LatencyMS: ptrMS(latency), Error: "NXDOMAIN : nom inexistant"}
	case rcode != dnsmessage.RCodeSuccess:
		return Result{LatencyMS: ptrMS(latency), Error: "erreur DNS : " + strings.TrimPrefix(rcode.String(), "RCode")}
	case len(records) == 0:
		return Result{LatencyMS: ptrMS(latency), Error: "aucun enregistrement " + rtype}
	}
	if len(records) > 50 {
		records = records[:50]
	}
	details := map[string]any{"dns_ms": round2(float64(latency.Microseconds()) / 1000), "records": records, "ttl": ttl, "nameserver": ns}
	if expect := cfgString(cfg, "expect", ""); expect != "" {
		found := false
		for _, r := range records {
			if strings.Contains(r, expect) {
				found = true
				break
			}
		}
		if !found {
			return Result{LatencyMS: ptrMS(latency), Error: fmt.Sprintf("« %s » absent des réponses", expect), Details: details}
		}
	}
	return Result{OK: true, LatencyMS: ptrMS(latency), Details: details}
}

var errDNSTimeout = errors.New("délai dépassé")

// dnsQuery envoie la question en UDP (puis en TCP si la réponse est tronquée).
func dnsQuery(ctx context.Context, ns, name string, qt dnsmessage.Type, timeout time.Duration) ([]string, uint32, dnsmessage.RCode, error) {
	n, err := dnsmessage.NewName(name)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("nom invalide : %v", err)
	}
	id := uint16(rand.IntN(1 << 16))
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: id, RecursionDesired: true})
	b.EnableCompression()
	if err := b.StartQuestions(); err != nil {
		return nil, 0, 0, err
	}
	if err := b.Question(dnsmessage.Question{Name: n, Type: qt, Class: dnsmessage.ClassINET}); err != nil {
		return nil, 0, 0, err
	}
	query, err := b.Finish()
	if err != nil {
		return nil, 0, 0, err
	}
	server := ns
	if _, _, err := net.SplitHostPort(ns); err != nil {
		server = net.JoinHostPort(ns, "53")
	}
	resp, err := exchange(ctx, "udp", server, query, timeout)
	if err != nil {
		return nil, 0, 0, err
	}
	var p dnsmessage.Parser
	h, err := p.Start(resp)
	if err == nil && h.Truncated {
		if r2, err2 := exchange(ctx, "tcp", server, query, timeout); err2 == nil {
			resp = r2
			h, err = p.Start(resp)
		}
	}
	if err != nil {
		return nil, 0, 0, fmt.Errorf("réponse illisible : %v", err)
	}
	if h.ID != id {
		return nil, 0, 0, errors.New("réponse d'une autre requête")
	}
	if err := p.SkipAllQuestions(); err != nil {
		return nil, 0, 0, err
	}
	var out []string
	var ttl uint32
	for {
		rr, err := p.Answer()
		if errors.Is(err, dnsmessage.ErrSectionDone) {
			break
		}
		if err != nil {
			return nil, 0, 0, fmt.Errorf("réponse illisible : %v", err)
		}
		if rr.Header.Type != qt {
			continue // chaîne de CNAME : seules les réponses du type demandé comptent
		}
		if len(out) == 0 {
			ttl = rr.Header.TTL
		}
		out = append(out, recordText(rr.Body))
	}
	return out, ttl, h.RCode, nil
}

func exchange(ctx context.Context, network, server string, query []byte, timeout time.Duration) ([]byte, error) {
	d := net.Dialer{Timeout: timeout}
	c, err := d.DialContext(ctx, network, server)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	dl := time.Now().Add(timeout)
	if cd, ok := ctx.Deadline(); ok && cd.Before(dl) {
		dl = cd
	}
	c.SetDeadline(dl)
	if network == "tcp" {
		msg := make([]byte, 2+len(query))
		binary.BigEndian.PutUint16(msg, uint16(len(query)))
		copy(msg[2:], query)
		if _, err := c.Write(msg); err != nil {
			return nil, err
		}
		var l [2]byte
		if _, err := readFull(c, l[:]); err != nil {
			return nil, timeoutErr(err)
		}
		buf := make([]byte, binary.BigEndian.Uint16(l[:]))
		if _, err := readFull(c, buf); err != nil {
			return nil, timeoutErr(err)
		}
		return buf, nil
	}
	if _, err := c.Write(query); err != nil {
		return nil, err
	}
	buf := make([]byte, 4096)
	n, err := c.Read(buf)
	if err != nil {
		return nil, timeoutErr(err)
	}
	return buf[:n], nil
}

func readFull(c net.Conn, b []byte) (int, error) {
	n := 0
	for n < len(b) {
		m, err := c.Read(b[n:])
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

func timeoutErr(err error) error {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return errDNSTimeout
	}
	return err
}

// recordText écrit un enregistrement comme dnspython (to_text).
func recordText(b dnsmessage.ResourceBody) string {
	switch r := b.(type) {
	case *dnsmessage.AResource:
		return net.IP(r.A[:]).String()
	case *dnsmessage.AAAAResource:
		return net.IP(r.AAAA[:]).String()
	case *dnsmessage.CNAMEResource:
		return r.CNAME.String()
	case *dnsmessage.NSResource:
		return r.NS.String()
	case *dnsmessage.PTRResource:
		return r.PTR.String()
	case *dnsmessage.MXResource:
		return fmt.Sprintf("%d %s", r.Pref, r.MX.String())
	case *dnsmessage.TXTResource:
		parts := make([]string, len(r.TXT))
		for i, t := range r.TXT {
			parts[i] = strconv.Quote(t)
		}
		return strings.Join(parts, " ")
	case *dnsmessage.SOAResource:
		return fmt.Sprintf("%s %s %d %d %d %d %d", r.NS.String(), r.MBox.String(), r.Serial, r.Refresh, r.Retry, r.Expire, r.MinTTL)
	case *dnsmessage.SRVResource:
		return fmt.Sprintf("%d %d %d %s", r.Priority, r.Weight, r.Port, r.Target.String())
	case *dnsmessage.UnknownResource:
		if r.Type == dnsmessage.Type(257) {
			return caaText(r.Data)
		}
		return fmt.Sprintf("\\# %d %x", len(r.Data), r.Data)
	}
	return fmt.Sprint(b)
}

// caaText : « drapeaux étiquette "valeur" » (RFC 8659).
func caaText(d []byte) string {
	if len(d) < 2 || int(d[1])+2 > len(d) {
		return fmt.Sprintf("\\# %d %x", len(d), d)
	}
	tag := string(d[2 : 2+int(d[1])])
	return fmt.Sprintf("%d %s %s", d[0], tag, strconv.Quote(string(d[2+int(d[1]):])))
}
