package integrations

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/hostfs"
	"s33d.fr/hypha/agent/internal/model"
)

// ── OpenVPN : fichiers de statut (status-version 1, 2 ou 3) ──────────────────────────────

type OpenVPN struct {
	cfg    config.OpenVPNConfig
	det    detector
	prev   map[string]vpnClient // fichier|client|depuis → octets
	prevAt time.Time
}

func newOpenVPN(cfg config.OpenVPNConfig) *OpenVPN {
	return &OpenVPN{cfg: cfg, det: detector{mode: cfg.Enabled}, prev: map[string]vpnClient{}}
}

func (o *OpenVPN) name() string          { return "openvpn" }
func (o *OpenVPN) close()                {}
func (o *OpenVPN) period() time.Duration { return 60 * time.Second }

type vpnClient struct {
	name, since string
	in, out     int64 // octets reçus du client, envoyés au client
}

// parseOpenVPNClientStats lit le statut d'un tunnel en mode client (« OpenVPN STATISTICS ») :
// octets lus et écrits sur le lien TCP/UDP.
func parseOpenVPNClientStats(text string) (vpnClient, bool) {
	if !strings.HasPrefix(strings.TrimSpace(text), "OpenVPN STATISTICS") {
		return vpnClient{}, false
	}
	var c vpnClient
	n := 0
	for _, l := range strings.Split(text, "\n") {
		k, v, ok := strings.Cut(strings.TrimRight(l, "\r"), ",")
		if !ok {
			continue
		}
		x, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			continue
		}
		switch k {
		case "TCP/UDP read bytes":
			c.in, n = x, n+1
		case "TCP/UDP write bytes":
			c.out, n = x, n+1
		}
	}
	return c, n == 2
}

// parseOpenVPNStatus lit un fichier de statut de serveur : CLIENT_LIST (versions 2 et 3,
// virgules ou tabulations) ou section « OpenVPN CLIENT LIST » (version 1).
func parseOpenVPNStatus(text string) ([]vpnClient, error) {
	var out []vpnClient
	v1 := false
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	known := false
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		sep := ","
		if strings.Contains(line, "\t") {
			sep = "\t"
		}
		f := strings.Split(line, sep)
		switch {
		case line == "OpenVPN CLIENT LIST":
			v1, known = true, true
		case line == "ROUTING TABLE" || line == "GLOBAL STATS":
			v1 = false
		case f[0] == "TITLE" || f[0] == "HEADER" || f[0] == "TIME":
			known = true
		case f[0] == "CLIENT_LIST" && len(f) >= 8:
			// CLIENT_LIST,nom,adresse réelle,adresse virtuelle,[adresse v6,]reçus,envoyés,depuis,…
			in, e1 := strconv.ParseInt(f[5], 10, 64)
			out2, e2 := strconv.ParseInt(f[6], 10, 64)
			since := f[7]
			if e1 != nil || e2 != nil { // statut ancien, sans colonne IPv6
				in, e1 = strconv.ParseInt(f[4], 10, 64)
				out2, e2 = strconv.ParseInt(f[5], 10, 64)
				since = f[6]
			}
			if e1 == nil && e2 == nil {
				out = append(out, vpnClient{name: f[1], since: since, in: in, out: out2})
			}
		case v1 && len(f) == 5 && f[0] != "Common Name" && f[0] != "Updated":
			in, e1 := strconv.ParseInt(f[2], 10, 64)
			out2, e2 := strconv.ParseInt(f[3], 10, 64)
			if e1 == nil && e2 == nil {
				out = append(out, vpnClient{name: f[0], since: f[4], in: in, out: out2})
			}
		}
	}
	if !known {
		return nil, fmt.Errorf("fichier de statut OpenVPN non reconnu")
	}
	return out, nil
}

func (o *OpenVPN) files() []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range o.cfg.StatusFiles {
		m, _ := filepath.Glob(hostfs.Root(p))
		for _, f := range m {
			if !seen[f] {
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	sort.Strings(out)
	return out
}

var instanceRe = regexp.MustCompile(`(?i)(^status-|[-_.]?status|\.log$)`)

func vpnInstance(path string) string {
	n := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	n = instanceRe.ReplaceAllString(n, "")
	if n == "" {
		n = "openvpn"
	}
	return n
}

func (o *OpenVPN) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := o.det.skip(now); skip {
		return nil, st
	}
	files := o.files()
	if len(files) == 0 {
		_, st := o.det.failed("aucun fichier de statut")
		return nil, st
	}
	ts := model.Ms(now)
	dt := now.Sub(o.prevAt).Seconds()
	cur := map[string]vpnClient{}
	var checks []model.Check
	state := "ok"
	for _, f := range files {
		inst := vpnInstance(f)
		data, err := os.ReadFile(f)
		var clients []vpnClient
		clientMode := false
		if err == nil {
			if c, ok := parseOpenVPNClientStats(string(data)); ok {
				// Tunnel client : un seul « client », le tunnel lui-même.
				c.name, clientMode = inst, true
				clients = []vpnClient{c}
			} else {
				clients, err = parseOpenVPNStatus(string(data))
			}
		}
		if err != nil {
			checks = append(checks, canConnect("openvpn", ts, false, err.Error(), tags("instance", inst)))
			state = "partiel : " + err.Error()
			continue
		}
		fi, _ := os.Stat(f)
		if fi != nil && now.Sub(fi.ModTime()) > 10*time.Minute {
			msg := fmt.Sprintf("fichier de statut figé depuis %s : OpenVPN arrêté ?", now.Sub(fi.ModTime()).Round(time.Minute))
			checks = append(checks, canConnect("openvpn", ts, false, msg, tags("instance", inst)))
			continue
		}
		if !clientMode {
			b.Gauge("openvpn.clients.connected", "count", float64(len(clients)), "instance", inst)
		}
		for _, c := range clients {
			k := f + "|" + c.name + "|" + c.since
			cur[k] = c
			if p, ok := o.prev[k]; ok && !o.prevAt.IsZero() {
				if v, ok := rateOf(c.in, p.in, dt); ok {
					b.Gauge("openvpn.client.bytes_in", "byte_per_second", v, "instance", inst, "client", c.name)
				}
				if v, ok := rateOf(c.out, p.out, dt); ok {
					b.Gauge("openvpn.client.bytes_out", "byte_per_second", v, "instance", inst, "client", c.name)
				}
			}
		}
		msg := fmt.Sprintf("%d client(s) connecté(s)", len(clients))
		if clientMode {
			msg = "tunnel client, statut à jour"
		}
		checks = append(checks, canConnect("openvpn", ts, true, msg, tags("instance", inst)))
	}
	o.det.ok()
	o.prev, o.prevAt = cur, now
	return checks, state
}

// ── fail2ban : fail2ban-client status (relu toutes les 5 minutes : l'outil est en Python) ─

type Fail2ban struct {
	cfg   config.IntegrationToggle
	det   detector
	cache cache[map[string]f2bJail]
}

type f2bJail struct{ failed, banned int64 }

func newFail2ban(cfg config.IntegrationToggle) *Fail2ban {
	return &Fail2ban{cfg: cfg, det: detector{mode: cfg.Enabled}}
}

func (f *Fail2ban) name() string          { return "fail2ban" }
func (f *Fail2ban) close()                {}
func (f *Fail2ban) period() time.Duration { return 60 * time.Second }

// parseF2BJails lit « Jail list: sshd, recidive ».
func parseF2BJails(text string) []string {
	for _, l := range strings.Split(text, "\n") {
		if i := strings.Index(l, "Jail list:"); i >= 0 {
			var out []string
			for _, j := range strings.Split(l[i+len("Jail list:"):], ",") {
				if j = strings.TrimSpace(j); j != "" {
					out = append(out, j)
				}
			}
			return out
		}
	}
	return nil
}

// parseF2BJail lit « Currently failed: 3 » et « Currently banned: 1 ».
func parseF2BJail(text string) (f2bJail, bool) {
	var j f2bJail
	n := 0
	for _, l := range strings.Split(text, "\n") {
		for key, dst := range map[string]*int64{"Currently failed:": &j.failed, "Currently banned:": &j.banned} {
			if i := strings.Index(l, key); i >= 0 {
				if v, err := strconv.ParseInt(strings.TrimSpace(l[i+len(key):]), 10, 64); err == nil {
					*dst = v
					n++
				}
			}
		}
	}
	return j, n == 2
}

func (f *Fail2ban) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := f.det.skip(now); skip {
		return nil, st
	}
	tool, ok := findTool("fail2ban-client")
	if !ok {
		_, st := f.det.failed("fail2ban-client introuvable")
		return nil, st
	}
	ts := model.Ms(now)
	jails, err := f.cache.get(now, 5*time.Minute, func() (map[string]f2bJail, error) {
		out, err := runTool(ctx, 20*time.Second, tool, "status")
		if err != nil {
			return nil, err
		}
		res := map[string]f2bJail{}
		for _, j := range parseF2BJails(string(out)) {
			o, err := runTool(ctx, 20*time.Second, tool, "status", j)
			if err != nil {
				continue
			}
			if st, ok := parseF2BJail(string(o)); ok {
				res[j] = st
			}
		}
		return res, nil
	})
	if err != nil {
		if silent, st := f.det.failed(err.Error()); silent {
			return nil, st
		} else {
			return []model.Check{canConnect("fail2ban", ts, false, err.Error(), tags())}, st
		}
	}
	f.det.ok()
	for _, j := range model.SortedKeys(jails) {
		b.Gauge("fail2ban.banned", "count", float64(jails[j].banned), "jail", j)
		b.Gauge("fail2ban.failed", "count", float64(jails[j].failed), "jail", j)
	}
	return []model.Check{canConnect("fail2ban", ts, true, fmt.Sprintf("%d prison(s)", len(jails)), tags())}, "ok"
}
