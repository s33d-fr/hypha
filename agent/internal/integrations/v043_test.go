package integrations

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// § 33 : les certificats que déclarent Apache, nginx, Postfix et Dovecot sont trouvés seuls.
func TestX509Discover(t *testing.T) {
	dir := t.TempDir()
	root := func(p string) string { return filepath.Join(dir, p) }
	conf := map[string]string{
		"/etc/apache2/sites-enabled/000-exemple.conf": "<VirtualHost *:443>\n  SSLCertificateFile \"/etc/apache2/conf/ssl/example.org.crt\"\n  # SSLCertificateFile /etc/ancien.crt\n</VirtualHost>\n",
		"/etc/nginx/sites-enabled/hypha":           "server {\n  ssl_certificate /etc/nginx/ssl/hypha.pem;\n  ssl_certificate_key /etc/nginx/ssl/hypha.key;\n}\n",
		"/etc/postfix/main.cf":                     "smtpd_tls_chain_files = /etc/ssl/mx/key.pem, /etc/ssl/mx/fullchain.pem\n",
		"/etc/dovecot/conf.d/10-ssl.conf":          "ssl_cert = </etc/ssl/mx/fullchain.pem\n",
	}
	for p, body := range conf {
		os.MkdirAll(filepath.Dir(root(p)), 0o700)
		os.WriteFile(root(p), []byte(body), 0o600)
	}
	soon := time.Now().Add(12 * 24 * time.Hour)
	writeCert(t, root("/etc/apache2/conf/ssl/example.org.crt"), soon)
	writeCert(t, root("/etc/nginx/ssl/hypha.pem"), time.Now().Add(80*24*time.Hour))
	writeCert(t, root("/etc/ssl/mx/fullchain.pem"), time.Now().Add(60*24*time.Hour))
	found := discover(root)
	if found[root("/etc/apache2/conf/ssl/example.org.crt")] != "apache" || found[root("/etc/nginx/ssl/hypha.pem")] != "nginx" ||
		found[root("/etc/ssl/mx/fullchain.pem")] != "postfix" || found[root("/etc/nginx/ssl/hypha.key")] != "" {
		t.Fatalf("découverte : %v", found)
	}
	if _, ok := found[root("/etc/ancien.crt")]; ok {
		t.Fatal("ligne commentée prise en compte")
	}
	x := newX509(config.X509Config{Enabled: "auto"})
	x.root = root
	b := model.NewSeriesBuilder(0, testTags)
	checks, st := x.collect(nil, time.Now(), b, testTags)
	if st != "partiel : 1 certificat(s) illisible(s)" { // la clé de Postfix, déclarée dans la même liste
		t.Fatalf("état %q", st)
	}
	var apache model.Check
	for _, c := range checks {
		if c.Name == "tls.cert.expiry" && c.Tags["cert_source"] == "apache" {
			apache = c
		}
	}
	if apache.Status != model.CheckWarn || apache.Tags["issuer"] != "hypha.s33d.fr" || apache.Tags["not_after"] == "" {
		t.Fatalf("certificat Apache : %+v", apache)
	}
}

func TestLatestPerSubject(t *testing.T) {
	now := time.Now()
	list := latestPerSubject([]certInfo{{subject: "mail.example.org", notAfter: now.Add(-24 * time.Hour), path: "ancien"},
		{subject: "Mail.example.org", notAfter: now.Add(80 * 24 * time.Hour), path: "neuf"}, {subject: "autre", notAfter: now}})
	if len(list) != 2 {
		t.Fatalf("%+v", list)
	}
	for _, c := range list {
		if c.path == "ancien" {
			t.Fatal("certificat remplacé gardé")
		}
	}
}

func TestRspamd(t *testing.T) {
	s, err := parseRspamdStat([]byte(`{"version":"3.8.1","uptime":153564,"scanned":200,"learned":2,"spam_count":18,"ham_count":182,
	 "connections":3,"actions":{"reject":18,"soft reject":0,"add header":48,"greylist":34,"no action":100}}`))
	if err != nil || s.Scanned != 200 || s.Actions["add header"] != 48 {
		t.Fatalf("%+v %v", s, err)
	}
	if _, err := parseRspamdStat([]byte(`{"autre":1}`)); err == nil {
		t.Fatal("page étrangère acceptée")
	}
	if v, ok := delta(5, 9); ok || v != 0 {
		t.Fatal("compteur revenu en arrière compté")
	}
}

// Relevé réel d'un magasin Windows (18/11/2026) : l'ancien certificat example.net et celui
// d'example.org (autre autorité) sont couverts par le certificat consolidé example.com, plus récent ; le reste expiré de longue date est écarté.
func TestCurrentStoreCerts(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	list := []certInfo{
		{path: "autre-ac", subject: "example.org", sans: []string{"example.org", "*.example.com", "*.example.org"}, notAfter: now.Add(-39 * day)},
		{path: "be-ancien", subject: "example.net", sans: []string{"example.net", "*.example.net", "*.example.org", "example.org"}, notAfter: now.Add(-10 * day)},
		{path: "be", subject: "example.net", sans: []string{"example.net", "*.example.net", "*.example.org", "*.autre.test"}, notAfter: now.Add(6 * day)},
		{path: "com", subject: "example.com", sans: []string{"example.com", "*.example.com", "example.net", "*.example.net", "example.org", "*.example.org", "*.autre.test"}, notAfter: now.Add(31 * day)},
		{path: "his", subject: "d818da72", notAfter: now.Add(-288 * day)},
		{path: "auth", subject: "Microsoft Exchange Server Auth Certificate", notAfter: now.Add(1800 * day)},
	}
	got := map[string]bool{}
	for _, c := range currentStoreCerts(list, now) {
		got[c.path] = true
	}
	if len(got) != 2 || !got["com"] || !got["auth"] {
		t.Fatalf("gardés : %v", got)
	}
}
