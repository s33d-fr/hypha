package integrations

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/hostfs"
	"s33d.fr/hypha/agent/internal/model"
)

// ── OpenLDAP : base cn=Monitor de slapd ─────────────────────────────────────────────────────
//
// Client LDAP v3 réduit au nécessaire (bind simple ou SASL EXTERNAL sur ldapi://, recherche,
// unbind), écrit ici en BER pour ne pas tirer de bibliothèque. La base « monitor » doit être
// déclarée dans slapd (olcDatabase=monitor) avec un accès en lecture pour le compte de l'agent.

const ldapTimeout = 5 * time.Second

// ── BER : encodage TLV ──────────────────────────────────────────────────────────────────

func berLen(n int) []byte {
	if n < 128 {
		return []byte{byte(n)}
	}
	var b []byte
	for x := n; x > 0; x >>= 8 {
		b = append([]byte{byte(x)}, b...)
	}
	return append([]byte{0x80 | byte(len(b))}, b...)
}

func ber(tag byte, content ...[]byte) []byte {
	var body []byte
	for _, c := range content {
		body = append(body, c...)
	}
	return append(append([]byte{tag}, berLen(len(body))...), body...)
}

func berInt(tag byte, v int) []byte {
	var b []byte
	for {
		b = append([]byte{byte(v)}, b...)
		v >>= 8
		if v == 0 && b[0]&0x80 == 0 {
			break
		}
		if v == -1 && b[0]&0x80 != 0 {
			break
		}
	}
	return ber(tag, b)
}

func berStr(tag byte, s string) []byte { return ber(tag, []byte(s)) }

// berElem : un élément lu (étiquette et contenu).
type berElem struct {
	tag  byte
	data []byte
}

// berRead lit un élément complet sur le flux.
func berRead(r *bufio.Reader) (berElem, error) {
	tag, err := r.ReadByte()
	if err != nil {
		return berElem{}, err
	}
	l, err := r.ReadByte()
	if err != nil {
		return berElem{}, err
	}
	n := int(l)
	if l&0x80 != 0 {
		k := int(l & 0x7f)
		if k == 0 || k > 4 {
			return berElem{}, errors.New("longueur BER invalide")
		}
		n = 0
		for i := 0; i < k; i++ {
			c, err := r.ReadByte()
			if err != nil {
				return berElem{}, err
			}
			n = n<<8 | int(c)
		}
	}
	if n > 16<<20 {
		return berElem{}, errors.New("message LDAP trop long")
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return berElem{}, err
	}
	return berElem{tag: tag, data: buf}, nil
}

// berSplit découpe le contenu d'un élément construit en ses éléments.
func berSplit(data []byte) ([]berElem, error) {
	var out []berElem
	for len(data) > 0 {
		if len(data) < 2 {
			return nil, errors.New("BER tronqué")
		}
		tag, l := data[0], int(data[1])
		off := 2
		if l&0x80 != 0 {
			k := l & 0x7f
			if k == 0 || k > 4 || len(data) < 2+k {
				return nil, errors.New("longueur BER invalide")
			}
			l = 0
			for i := 0; i < k; i++ {
				l = l<<8 | int(data[2+i])
			}
			off += k
		}
		if len(data) < off+l {
			return nil, errors.New("BER tronqué")
		}
		out = append(out, berElem{tag: tag, data: data[off : off+l]})
		data = data[off+l:]
	}
	return out, nil
}

func berToInt(b []byte) int {
	v := 0
	for i, c := range b {
		if i == 0 && c&0x80 != 0 {
			v = -1
		}
		v = v<<8 | int(c)
	}
	return v
}

// ── Connexion LDAP ─────────────────────────────────────────────────────────────────────

type ldapConn struct {
	conn  net.Conn
	r     *bufio.Reader
	msgID int
}

// ldapEntry : DN et attributs (valeurs texte).
type ldapEntry struct {
	dn    string
	attrs map[string][]string
}

func ldapDial(ctx context.Context, raw string, insecure bool) (*ldapConn, error) {
	d := net.Dialer{Timeout: ldapTimeout}
	var c net.Conn
	var err error
	if rest, ok := strings.CutPrefix(raw, "ldapi://"); ok {
		// Chemin de la socket encodé dans l'hôte (%2F) : url.Parse le refuse, on le lit à part.
		path, _, _ := strings.Cut(rest, "/")
		if path == "" {
			path = "/run/slapd/ldapi"
		} else if p, uerr := url.PathUnescape(path); uerr == nil {
			path = p
		}
		c, err = d.DialContext(ctx, "unix", hostfs.Root(path))
	} else {
		u, perr := url.Parse(raw)
		if perr != nil {
			return nil, perr
		}
		switch u.Scheme {
		case "ldaps":
			host := u.Host
			if u.Port() == "" {
				host = net.JoinHostPort(u.Hostname(), "636")
			}
			var rc net.Conn
			if rc, err = d.DialContext(ctx, "tcp", host); err == nil {
				tc := tls.Client(rc, &tls.Config{ServerName: u.Hostname(), InsecureSkipVerify: insecure}) //nolint:gosec // choix de la conf
				if err = tc.HandshakeContext(ctx); err != nil {
					rc.Close()
				}
				c = tc
			}
		default:
			host := u.Host
			if u.Port() == "" {
				host = net.JoinHostPort(u.Hostname(), "389")
			}
			c, err = d.DialContext(ctx, "tcp", host)
		}
	}
	if err != nil {
		return nil, err
	}
	_ = c.SetDeadline(time.Now().Add(4 * ldapTimeout))
	return &ldapConn{conn: c, r: bufio.NewReader(c)}, nil
}

func (l *ldapConn) send(op []byte) (int, error) {
	l.msgID++
	_, err := l.conn.Write(ber(0x30, berInt(0x02, l.msgID), op))
	return l.msgID, err
}

// read lit le prochain message : identifiant, étiquette de l'opération et son contenu.
func (l *ldapConn) read() (int, berElem, error) {
	msg, err := berRead(l.r)
	if err != nil {
		return 0, berElem{}, err
	}
	parts, err := berSplit(msg.data)
	if err != nil || len(parts) < 2 {
		return 0, berElem{}, errors.New("message LDAP illisible")
	}
	return berToInt(parts[0].data), parts[1], nil
}

// result lit un LDAPResult (code, message de diagnostic).
func ldapResult(op berElem) (int, string) {
	parts, err := berSplit(op.data)
	if err != nil || len(parts) < 3 {
		return -1, "réponse illisible"
	}
	return berToInt(parts[0].data), string(parts[2].data)
}

// bind : simple (DN et mot de passe ; vide = anonyme) ou SASL EXTERNAL (identité de la socket).
func (l *ldapConn) bind(dn, password string, external bool) error {
	auth := berStr(0x80, password)
	if external {
		// Réponse initiale vide (identité de la socket) : sans elle, slapd répond 14
		// (saslBindInProgress) et attend un second échange.
		auth = ber(0xa3, berStr(0x04, "EXTERNAL"), berStr(0x04, ""))
	}
	if _, err := l.send(ber(0x60, berInt(0x02, 3), berStr(0x04, dn), auth)); err != nil {
		return err
	}
	_, op, err := l.read()
	if err != nil {
		return err
	}
	if op.tag != 0x61 {
		return fmt.Errorf("réponse au bind inattendue")
	}
	if code, diag := ldapResult(op); code != 0 {
		if code == 49 {
			return fmt.Errorf("bind refusé : identifiants invalides (code 49)")
		}
		return fmt.Errorf("bind refusé (code %d) %s", code, diag)
	}
	return nil
}

// search : filtre (objectClass=*), portée 0 (base) ou 1 (enfants).
func (l *ldapConn) search(base string, scope int, attrs ...string) ([]ldapEntry, error) {
	var al [][]byte
	for _, a := range attrs {
		al = append(al, berStr(0x04, a))
	}
	req := ber(0x63, berStr(0x04, base), berInt(0x0a, scope), berInt(0x0a, 0), berInt(0x02, 0), berInt(0x02, 0),
		ber(0x01, []byte{0}), berStr(0x87, "objectClass"), ber(0x30, al...))
	id, err := l.send(req)
	if err != nil {
		return nil, err
	}
	var out []ldapEntry
	for {
		mid, op, err := l.read()
		if err != nil {
			return nil, err
		}
		if mid != id {
			continue
		}
		switch op.tag {
		case 0x64: // SearchResultEntry
			parts, err := berSplit(op.data)
			if err != nil || len(parts) < 2 {
				return nil, errors.New("entrée LDAP illisible")
			}
			e := ldapEntry{dn: string(parts[0].data), attrs: map[string][]string{}}
			list, _ := berSplit(parts[1].data)
			for _, a := range list {
				kv, err := berSplit(a.data)
				if err != nil || len(kv) < 2 {
					continue
				}
				vals, _ := berSplit(kv[1].data)
				for _, v := range vals {
					e.attrs[strings.ToLower(string(kv[0].data))] = append(e.attrs[strings.ToLower(string(kv[0].data))], string(v.data))
				}
			}
			out = append(out, e)
		case 0x65: // SearchResultDone
			if code, diag := ldapResult(op); code != 0 {
				if code == 32 {
					return nil, fmt.Errorf("%s introuvable (base monitor absente, ou invisible pour ce compte)", base)
				}
				if code == 50 {
					return nil, fmt.Errorf("%s : droits insuffisants (code 50)", base)
				}
				return nil, fmt.Errorf("recherche %s : code %d %s", base, code, diag)
			}
			return out, nil
		}
	}
}

func (l *ldapConn) close() {
	_, _ = l.send([]byte{0x42, 0x00}) // UnbindRequest
	l.conn.Close()
}

// ── Sonde ──────────────────────────────────────────────────────────────────────────────

type OpenLDAP struct {
	probe
	cfg config.OpenLDAPConfig
}

func newOpenLDAP(cfg config.OpenLDAPConfig) *OpenLDAP {
	return &OpenLDAP{cfg: cfg, probe: newProbe("openldap", "openldap.can_connect", intent(cfg.Enabled, cfg.URL != ""), "instance", cfg.URL)}
}

func (o *OpenLDAP) period() time.Duration { return 30 * time.Second }

func firstNum(e ldapEntry, attr string) (float64, bool) {
	v := e.attrs[strings.ToLower(attr)]
	if len(v) == 0 {
		return 0, false
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v[0]), 64)
	return f, err == nil
}

// rdn : « cn=Bind,cn=Operations,cn=Monitor » → « bind ».
func rdn(dn string) string {
	first, _, _ := strings.Cut(dn, ",")
	_, v, _ := strings.Cut(first, "=")
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(v), " ", "_"))
}

func (o *OpenLDAP) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := o.det.skip(now); skip {
		return nil, st
	}
	if o.cfg.URL == "" {
		return nil, "absent"
	}
	ts := model.Ms(now)
	ctx, cancel := context.WithTimeout(ctx, 4*ldapTimeout)
	defer cancel()
	conn, err := ldapDial(ctx, o.cfg.URL, o.cfg.Insecure)
	if err != nil {
		return o.fail(ts, tags, err)
	}
	defer conn.close()
	external := strings.HasPrefix(o.cfg.URL, "ldapi:") && o.cfg.BindDN == ""
	if err := conn.bind(o.cfg.BindDN, o.cfg.Password, external); err != nil {
		return o.fail(ts, tags, err)
	}
	root, err := conn.search("cn=Monitor", 0, "monitoredInfo")
	if err == nil && len(root) == 0 {
		err = fmt.Errorf("cn=Monitor vide")
	}
	if err != nil {
		return o.fail(ts, tags, err)
	}
	o.det.ok()
	o.begin()
	defer o.end()
	kv := o.kvs()
	attrs := []string{"monitorCounter", "monitorOpInitiated", "monitorOpCompleted", "monitoredInfo"}
	var errs []string
	children := func(base string) []ldapEntry {
		list, err := conn.search(base, 1, attrs...)
		if err != nil {
			errs = append(errs, err.Error())
		}
		return list
	}
	for _, e := range children("cn=Connections,cn=Monitor") {
		switch rdn(e.dn) {
		case "total":
			if v, ok := firstNum(e, "monitorCounter"); ok {
				o.count(b, "openldap.connections.total", "connection", "conn_total", v, kv...)
			}
		case "current":
			if v, ok := firstNum(e, "monitorCounter"); ok {
				b.Gauge("openldap.connections.current", "connection", v, kv...)
			}
		}
	}
	for _, e := range children("cn=Operations,cn=Monitor") {
		op := rdn(e.dn)
		okv := o.kvs("operation", op)
		if v, ok := firstNum(e, "monitorOpInitiated"); ok {
			o.count(b, "openldap.operations.initiated", "operation", "init/"+op, v, okv...)
		}
		if v, ok := firstNum(e, "monitorOpCompleted"); ok {
			o.count(b, "openldap.operations.completed", "operation", "done/"+op, v, okv...)
		}
	}
	for _, e := range children("cn=Statistics,cn=Monitor") {
		if v, ok := firstNum(e, "monitorCounter"); ok {
			switch name := rdn(e.dn); name {
			case "bytes":
				o.count(b, "openldap.statistics.bytes", "byte", "stat/"+name, v, kv...)
			case "pdu":
				o.count(b, "openldap.statistics.pdu", "message", "stat/"+name, v, kv...)
			case "entries":
				o.count(b, "openldap.statistics.entries", "entry", "stat/"+name, v, kv...)
			case "referrals":
				o.count(b, "openldap.statistics.referrals", "referral", "stat/"+name, v, kv...)
			}
		}
	}
	for _, e := range children("cn=Threads,cn=Monitor") {
		name := rdn(e.dn)
		switch name {
		case "max", "max_pending", "open", "starting", "active", "pending", "backload":
			if v, ok := firstNum(e, "monitoredInfo"); ok {
				b.Gauge("openldap.threads", "thread", v, o.kvs("status", name)...)
			}
		}
	}
	for _, e := range children("cn=Waiters,cn=Monitor") {
		if v, ok := firstNum(e, "monitorCounter"); ok {
			switch rdn(e.dn) {
			case "read":
				b.Gauge("openldap.waiter.read", "connection", v, kv...)
			case "write":
				b.Gauge("openldap.waiter.write", "connection", v, kv...)
			}
		}
	}
	if up, err := conn.search("cn=Uptime,cn=Time,cn=Monitor", 0, "monitoredInfo"); err == nil && len(up) == 1 {
		if v, ok := firstNum(up[0], "monitoredInfo"); ok {
			b.Gauge("openldap.uptime", "second", v, kv...)
		}
	}
	version := "slapd"
	if v := root[0].attrs["monitoredinfo"]; len(v) > 0 {
		version = strings.TrimSpace(v[0])
		if i := strings.Index(version, " ("); i > 0 {
			version = version[:i]
		}
		version = strings.TrimPrefix(version, "OpenLDAP: ")
	}
	checks := []model.Check{o.okCheck(ts, tags, model.Truncate(version, 120)+" joignable")}
	if len(errs) > 0 {
		return checks, "partiel : " + strings.Join(errs, " ; ")
	}
	return checks, "ok"
}
