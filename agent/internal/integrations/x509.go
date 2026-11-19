package integrations

import (
	"bufio"
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/hostfs"
	"s33d.fr/hypha/agent/internal/model"
)

// Seuils du contrôle tls.cert.expiry (§ 31).
const (
	certWarnDays = 21
	certCritDays = 7
	certRescan   = 5 * time.Minute
)

// X509 surveille l'échéance des certificats posés sur l'hôte (relus toutes les 5 minutes, pour
// qu'un certificat renouvelé efface son alerte sans attendre ; lecture de fichiers locaux) : les
// chemins de la conf, ceux que déclarent les serveurs web et de messagerie (§ 33) et, sous
// Windows, le magasin de certificats de l'ordinateur.
type X509 struct {
	cfg   config.X509Config
	det   detector
	cache cache[[]certInfo]
	root  func(string) string // chemin de l'hôte (HOST_ROOT dans un conteneur) ; remplacé dans les tests
}

type certInfo struct {
	path, subject, issuer, source string
	sans                          []string
	notAfter                      time.Time
	err                           error
}

func newX509(cfg config.X509Config) *X509 {
	return &X509{cfg: cfg, det: detector{mode: cfg.Enabled}, root: hostfs.Root}
}

func (x *X509) name() string          { return "x509" }
func (x *X509) close()                {}
func (x *X509) period() time.Duration { return 60 * time.Second }

// readCert lit le premier certificat d'un fichier PEM (le certificat du serveur dans un
// fullchain.pem) ; un fichier DER est aussi accepté.
func readCert(data []byte) (*x509.Certificate, error) {
	rest := data
	for len(rest) > 0 {
		var blk *pem.Block
		blk, rest = pem.Decode(rest)
		if blk == nil {
			break
		}
		if blk.Type == "CERTIFICATE" {
			return x509.ParseCertificate(blk.Bytes)
		}
	}
	if c, err := x509.ParseCertificate(data); err == nil {
		return c, nil
	}
	return nil, fmt.Errorf("aucun certificat PEM")
}

func certSubject(c *x509.Certificate) string {
	if c.Subject.CommonName != "" {
		return c.Subject.CommonName
	}
	if len(c.DNSNames) > 0 {
		return c.DNSNames[0]
	}
	return c.Subject.String()
}

// certIssuer : « R11 (Let's Encrypt) ».
func certIssuer(c *x509.Certificate) string {
	cn, org := c.Issuer.CommonName, ""
	if len(c.Issuer.Organization) > 0 {
		org = c.Issuer.Organization[0]
	}
	switch {
	case cn != "" && org != "" && cn != org:
		return cn + " (" + org + ")"
	case cn != "":
		return cn
	case org != "":
		return org
	}
	return c.Issuer.String()
}

func certFromX509(c *x509.Certificate, path, source string) certInfo {
	return certInfo{path: path, source: source, subject: certSubject(c), issuer: certIssuer(c),
		sans: c.DNSNames, notAfter: c.NotAfter}
}

// certStatus : OK, WARN sous 21 jours, CRIT sous 7 jours (ou expiré).
func certStatus(days float64) int {
	switch {
	case days < certCritDays:
		return model.CheckCrit
	case days < certWarnDays:
		return model.CheckWarn
	}
	return model.CheckOK
}

// ── découverte : les certificats que les serveurs déclarent ─────────────────────────────

type certSource struct {
	name  string
	files []string       // motifs glob des fichiers de configuration
	re    *regexp.Regexp // groupe 1 : un chemin (ou une liste, pour Postfix)
}

var certSources = []certSource{
	{"apache", []string{"/etc/apache2/sites-enabled/*", "/etc/apache2/conf-enabled/*", "/etc/apache2/mods-enabled/ssl.conf",
		"/etc/httpd/conf.d/*.conf", "/etc/httpd/conf/httpd.conf"},
		regexp.MustCompile(`(?i)^\s*SSLCertificateFile\s+"?([^"\s]+)"?`)},
	{"nginx", []string{"/etc/nginx/nginx.conf", "/etc/nginx/sites-enabled/*", "/etc/nginx/conf.d/*.conf"},
		regexp.MustCompile(`^\s*ssl_certificate\s+"?([^";\s]+)"?\s*;`)},
	{"postfix", []string{"/etc/postfix/main.cf"},
		regexp.MustCompile(`^\s*smtpd_tls_(?:cert_file|chain_files)\s*=\s*(.+)$`)},
	{"dovecot", []string{"/etc/dovecot/dovecot.conf", "/etc/dovecot/conf.d/10-ssl.conf"},
		regexp.MustCompile(`^\s*ssl_(?:server_)?cert\s*=\s*<\s*(\S+)`)},
	{"haproxy", []string{"/etc/haproxy/haproxy.cfg"},
		regexp.MustCompile(`^\s*bind\s.*\bcrt\s+(\S+)`)},
}

// discover rend chemin complet (racine de l'hôte comprise) → source des certificats déclarés
// par les configurations de l'hôte.
func discover(root func(string) string) map[string]string {
	out := map[string]string{}
	for _, src := range certSources {
		for _, pat := range src.files {
			files, _ := filepath.Glob(root(pat))
			for _, f := range files {
				data, err := os.ReadFile(f)
				if err != nil || len(data) > 4<<20 {
					continue
				}
				sc := bufio.NewScanner(bytes.NewReader(data))
				sc.Buffer(make([]byte, 64<<10), 1<<20)
				for sc.Scan() {
					line := sc.Text()
					if strings.HasPrefix(strings.TrimSpace(line), "#") {
						continue
					}
					m := src.re.FindStringSubmatch(line)
					if m == nil {
						continue
					}
					for _, p := range strings.FieldsFunc(m[1], func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
						p = strings.Trim(p, `"'`)
						if !filepath.IsAbs(p) || strings.ContainsAny(p, "$") {
							continue
						}
						full := root(p)
						if st, err := os.Stat(full); err == nil && st.IsDir() {
							// HAProxy : crt <dossier> charge tous ses .pem.
							more, _ := filepath.Glob(filepath.Join(full, "*.pem"))
							for _, q := range more {
								if _, seen := out[q]; !seen {
									out[q] = src.name
								}
							}
							continue
						}
						if _, seen := out[full]; !seen {
							out[full] = src.name
						}
					}
				}
			}
		}
	}
	return out
}

func (x *X509) scan() ([]certInfo, error) {
	paths := map[string]string{}
	for _, p := range x.cfg.Paths {
		m, _ := filepath.Glob(x.root(p))
		for _, f := range m {
			paths[f] = "file"
		}
	}
	if x.cfg.DiscoverOn() {
		for full, src := range discover(x.root) {
			if _, seen := paths[full]; !seen {
				paths[full] = src
			}
		}
	}
	var out []certInfo
	for f, src := range paths {
		ci := certInfo{path: f, source: src}
		data, err := os.ReadFile(f)
		if err == nil {
			var c *x509.Certificate
			if c, err = readCert(data); err == nil {
				ci = certFromX509(c, f, src)
			}
		}
		ci.err = err
		out = append(out, ci)
	}
	out = append(out, platformCerts(x.cfg.Stores)...)
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	if len(out) == 0 {
		return nil, errNoData
	}
	return out, nil
}

// storeStaleDays : un certificat de magasin expiré depuis plus longtemps est un reste (un service
// qui s'en servirait serait en panne depuis des semaines, ce que les tests synthétiques voient).
const storeStaleDays = 30

func certNames(c certInfo) []string {
	out := make([]string, 0, len(c.sans)+1)
	for _, n := range append([]string{c.subject}, c.sans...) {
		if n = strings.ToLower(strings.TrimSpace(n)); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// covers : le nom est porté par la liste, directement ou par un joker « *.domaine » d'un niveau.
func covers(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
		if strings.HasPrefix(n, "*.") {
			if _, rest, ok := strings.Cut(name, "."); ok && rest == n[2:] {
				return true
			}
		}
	}
	return false
}

// currentStoreCerts : dans un magasin, un certificat renouvelé laisse souvent l'ancien en place,
// parfois sous un autre nom principal. Sont écartés les certificats dont tous les noms sont
// couverts par un certificat qui expire plus tard, et ceux expirés depuis plus de 30 jours.
func currentStoreCerts(list []certInfo, now time.Time) []certInfo {
	var out []certInfo
	for i, a := range list {
		if now.Sub(a.notAfter) > storeStaleDays*24*time.Hour {
			continue
		}
		names, superseded := certNames(a), false
		for j, b := range list {
			if i == j || !b.notAfter.After(a.notAfter) || len(names) == 0 {
				continue
			}
			bn, all := certNames(b), true
			for _, n := range names {
				if !covers(bn, n) {
					all = false
					break
				}
			}
			if all {
				superseded = true
				break
			}
		}
		if !superseded {
			out = append(out, a)
		}
	}
	return out
}

// latestPerSubject : seul le plus récent de chaque nom compte.
func latestPerSubject(list []certInfo) []certInfo {
	best := map[string]int{}
	for i, c := range list {
		key := strings.ToLower(c.subject)
		if j, ok := best[key]; !ok || c.notAfter.After(list[j].notAfter) {
			best[key] = i
		}
	}
	out := make([]certInfo, 0, len(best))
	for _, i := range best {
		out = append(out, list[i])
	}
	return out
}

func (x *X509) collect(_ context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := x.det.skip(now); skip {
		return nil, st
	}
	certs, err := x.cache.get(now, certRescan, x.scan)
	if err != nil {
		_, st := x.det.failed("aucun certificat aux chemins de integrations.x509.paths ni dans les configurations")
		return nil, st
	}
	x.det.ok()
	ts := model.Ms(now)
	var checks []model.Check
	bad := 0
	for _, c := range certs {
		if c.err != nil {
			bad++
			checks = append(checks, model.Check{TS: ts, Name: "tls.cert.expiry", Status: model.CheckUnknown,
				Message: "certificat illisible : " + c.err.Error(), Tags: tags("cert_path", c.path, "cert_source", c.source)})
			continue
		}
		days := math.Floor(time.Until(c.notAfter).Hours()/24*10) / 10
		b.Gauge("tls.cert.days_left", "", days, "cert_path", c.path, "subject", c.subject)
		msg := fmt.Sprintf("%s : expire le %s (dans %g j)", c.subject, c.notAfter.UTC().Format("02/01/2006"), days)
		if days < 0 {
			msg = fmt.Sprintf("%s : expiré depuis le %s", c.subject, c.notAfter.UTC().Format("02/01/2006"))
		}
		kv := []string{"cert_path", c.path, "subject", c.subject, "issuer", c.issuer, "cert_source", c.source,
			"not_after", c.notAfter.UTC().Format(time.RFC3339)}
		if len(c.sans) > 0 {
			kv = append(kv, "sans", model.Truncate(strings.Join(c.sans, ", "), 400))
		}
		checks = append(checks, model.Check{TS: ts, Name: "tls.cert.expiry", Status: certStatus(days), Message: msg, Tags: tags(kv...)})
	}
	state := "ok"
	if bad > 0 {
		state = fmt.Sprintf("partiel : %d certificat(s) illisible(s)", bad)
	}
	checks = append(checks, canConnect("x509", ts, bad < len(certs), fmt.Sprintf("%d certificat(s) lus", len(certs)-bad), tags()))
	return checks, state
}
