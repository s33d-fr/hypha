package integrations

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── Proxmox VE (§ 44) : la grappe par pvesh (sur un nœud) ou par l'API (jeton PVEAuditor) ─────
//
// Une lecture de /cluster/resources donne nœuds, stockages et invités (VM et conteneurs) ;
// /cluster/status le quorum ; /cluster/tasks les tâches récentes. Contrôles : quorum, nœud hors
// ligne, stockage indisponible, invité HA hors de l'état demandé, dernière sauvegarde (vzdump)
// de chaque nœud en échec, levé dès que la suivante réussit. Les tâches en échec partent en
// événements. Sur un nœud, seul le premier nœud en ligne (ordre des noms) lit la grappe : les
// autres agents de la grappe se taisent, sans doublon.

type Proxmox struct {
	cfg    config.ProxmoxConfig
	det    detector
	get    func(ctx context.Context, path string, out any) error
	local  bool // pvesh sur ce nœud
	post   func(route string, fields map[string]any)
	prev   map[string]float64
	prevAt time.Time
	since  int64 // fin de la dernière tâche en échec relayée (secondes)

	version   string // relue toutes les heures (un appel de pvesh de moins par passage)
	versionAt time.Time
}

func newProxmox(cfg config.ProxmoxConfig, d Deps) *Proxmox {
	p := &Proxmox{cfg: cfg, post: d.Post}
	mode := cfg.Enabled
	switch {
	case cfg.URL != "":
		if mode == "auto" {
			mode = "true"
		}
		tc := &tls.Config{InsecureSkipVerify: cfg.Insecure} //nolint:gosec // choisi dans la conf
		var initErr error
		if cfg.CAFile != "" && !cfg.Insecure {
			pool := x509.NewCertPool()
			if pem, err := os.ReadFile(cfg.CAFile); err != nil || !pool.AppendCertsFromPEM(pem) {
				initErr = fmt.Errorf("autorité de certification illisible : %s", cfg.CAFile)
			}
			tc.RootCAs = pool
		}
		client := &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{TLSClientConfig: tc}}
		base := strings.TrimRight(cfg.URL, "/") + "/api2/json"
		p.get = func(ctx context.Context, path string, out any) error {
			if initErr != nil {
				return initErr
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
			if err != nil {
				return err
			}
			req.Header.Set("Authorization", "PVEAPIToken="+cfg.TokenID+"="+cfg.TokenSecret)
			req.Header.Set("User-Agent", "hypha-agent")
			resp, err := client.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			switch {
			case resp.StatusCode == http.StatusUnauthorized:
				return fmt.Errorf("jeton refusé (HTTP 401)")
			case resp.StatusCode == http.StatusForbidden:
				return fmt.Errorf("%s : accès refusé (HTTP 403), rôle PVEAuditor attendu", path)
			case resp.StatusCode != http.StatusOK:
				return fmt.Errorf("%s : HTTP %d", path, resp.StatusCode)
			}
			var env struct {
				Data json.RawMessage `json:"data"`
			}
			if err := json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&env); err != nil {
				return err
			}
			return json.Unmarshal(env.Data, out)
		}
	default:
		p.local = true
		p.get = func(ctx context.Context, path string, out any) error {
			tool, ok := findTool("pvesh")
			if !ok {
				return errNoPvesh
			}
			args := []string{"get", path, "--output-format", "json"}
			// pvesh (Perl) occupe ~150 Mo : au-delà du plafond de l'agent, il s'enlise. Sous systemd,
			// il tourne dans sa propre unité, bornée elle aussi (mémoire, CPU, durée, lecture seule).
			if sr, ok := findTool("systemd-run"); ok && fileExists("/run/systemd/system") {
				args = append([]string{"--quiet", "--wait", "--pipe", "--collect", "-p", "MemoryMax=384M", "-p", "CPUQuota=50%",
					"-p", "Nice=10", "-p", "IOSchedulingClass=idle", "-p", "NoNewPrivileges=yes", "-p", "ProtectSystem=full",
					"-p", "ProtectHome=read-only", "-p", "PrivateTmp=yes", "-p", "RuntimeMaxSec=40", tool}, args...)
				tool = sr
			}
			b, err := runTool(ctx, 45*time.Second, tool, args...)
			if err != nil {
				return err
			}
			return json.Unmarshal(b, out)
		}
	}
	p.det = detector{mode: mode}
	return p
}

var errNoPvesh = fmt.Errorf("pvesh introuvable")

func (p *Proxmox) name() string { return "proxmox" }

// period : sur un nœud, deux minutes au moins (trois appels de pvesh à ~2 s de CPU chacun).
func (p *Proxmox) period() time.Duration {
	d := p.cfg.Interval.D()
	if d <= 0 {
		d = time.Minute
	}
	if p.local && d < 2*time.Minute {
		d = 2 * time.Minute
	}
	return d
}
func (p *Proxmox) close() {}

type pveItem map[string]any

func (it pveItem) s(k string) string {
	switch v := it[k].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

func (it pveItem) f(k string) (float64, bool) {
	switch v := it[k].(type) {
	case float64:
		return v, true
	case string:
		f, err := strconv.ParseFloat(v, 64)
		return f, err == nil
	case bool:
		if v {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}

func pct(used, total float64) float64 {
	if total <= 0 {
		return 0
	}
	return round1(100 * used / total)
}

func (p *Proxmox) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := p.det.skip(now); skip {
		return nil, st
	}
	ts := model.Ms(now)
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	var status []pveItem
	if err := p.get(ctx, "/cluster/status", &status); err != nil {
		if err == errNoPvesh && p.cfg.Enabled != "true" {
			return nil, "absent"
		}
		if silent, st := p.det.failed(err.Error()); silent {
			return nil, st
		}
		return []model.Check{canConnect("proxmox", ts, false, model.Truncate(err.Error(), 300), tags())}, "erreur : " + err.Error()
	}
	p.det.ok()
	cluster, quorate, hasCluster := "", 0.0, false
	var online []string
	localNode := ""
	for _, it := range status {
		switch it.s("type") {
		case "cluster":
			hasCluster = true
			cluster = it.s("name")
			quorate, _ = it.f("quorate")
		case "node":
			if v, _ := it.f("online"); v == 1 {
				online = append(online, it.s("name"))
			}
			if v, _ := it.f("local"); v == 1 {
				localNode = it.s("name")
			}
		}
	}
	sort.Strings(online)
	if p.local && len(online) > 0 && localNode != "" && localNode != online[0] {
		return nil, "ok : grappe lue par " + online[0]
	}
	if cluster == "" {
		cluster = firstNonEmpty(localNode, "proxmox")
	}
	base := []string{"proxmox_cluster", cluster}
	var checks []model.Check
	if hasCluster {
		st, msg := model.CheckOK, fmt.Sprintf("grappe %s : quorum atteint (%d nœud(s) en ligne)", cluster, len(online))
		if quorate != 1 {
			st, msg = model.CheckCrit, fmt.Sprintf("grappe %s : quorum perdu (%d nœud(s) en ligne), configuration en lecture seule", cluster, len(online))
		}
		checks = append(checks, model.Check{TS: ts, Name: "proxmox.cluster.quorum", Status: st, Message: msg, Tags: tags(base...)})
	}
	var res []pveItem
	var problems []string
	if err := p.get(ctx, "/cluster/resources", &res); err != nil {
		problems = append(problems, err.Error())
	}
	offline := map[string]bool{}
	elapsed := now.Sub(p.prevAt).Seconds()
	cur := map[string]float64{}
	nodes, guests, running := 0, 0, 0
	for _, it := range res {
		switch it.s("type") {
		case "node":
			nodes++
			node := it.s("node")
			kv := append(append([]string{}, base...), "proxmox_node", node)
			st, msg := model.CheckOK, node+" en ligne"
			if it.s("status") != "online" {
				st, msg = model.CheckCrit, node+" hors ligne"
				offline[node] = true
			} else {
				if c, ok := it.f("cpu"); ok {
					b.Gauge("proxmox.node.cpu.usage", "percent", round1(100*c), kv...)
				}
				if m, ok := it.f("mem"); ok {
					mx, _ := it.f("maxmem")
					b.Gauge("proxmox.node.mem.usage", "percent", pct(m, mx), kv...)
				}
				if d, ok := it.f("disk"); ok {
					mx, _ := it.f("maxdisk")
					b.Gauge("proxmox.node.rootfs.usage", "percent", pct(d, mx), kv...)
				}
				if u, ok := it.f("uptime"); ok {
					b.Gauge("proxmox.node.uptime", "second", u, kv...)
				}
			}
			checks = append(checks, model.Check{TS: ts, Name: "proxmox.node", Status: st, Message: msg, Tags: tags(kv...)})
		}
	}
	for _, it := range res {
		node := it.s("node")
		switch it.s("type") {
		case "storage":
			if offline[node] {
				continue // nœud hors ligne : déjà dit, ses stockages sont « unknown »
			}
			sto := it.s("storage")
			kv := append(append([]string{}, base...), "proxmox_node", node, "proxmox_storage", sto)
			st, msg := model.CheckOK, fmt.Sprintf("stockage %s de %s disponible", sto, node)
			if s := it.s("status"); s != "available" {
				st, msg = model.CheckCrit, fmt.Sprintf("stockage %s de %s indisponible (%s)", sto, node, firstNonEmpty(s, "sans état"))
			} else if d, ok := it.f("disk"); ok {
				mx, _ := it.f("maxdisk")
				if mx > 0 {
					u := pct(d, mx)
					b.Gauge("proxmox.storage.usage", "percent", u, kv...)
					b.Gauge("proxmox.storage.available", "byte", mx-d, kv...)
					msg += fmt.Sprintf(", occupé à %g %%", u)
				}
			}
			checks = append(checks, model.Check{TS: ts, Name: "proxmox.storage", Status: st, Message: msg, Tags: tags(kv...)})
		case "qemu", "lxc":
			if t, _ := it.f("template"); t == 1 {
				continue
			}
			guests++
			typ, vmid, name := it.s("type"), it.s("vmid"), it.s("name")
			kv := append(append([]string{}, base...), "proxmox_node", node, "proxmox_type", typ, "proxmox_vmid", vmid, "proxmox_guest", name)
			state := it.s("status")
			if state == "running" {
				running++
				if c, ok := it.f("cpu"); ok {
					b.Gauge("proxmox.guest.cpu.usage", "percent", round1(100*c), kv...)
				}
				if m, ok := it.f("mem"); ok {
					mx, _ := it.f("maxmem")
					b.Gauge("proxmox.guest.mem.usage", "percent", pct(m, mx), kv...)
				}
			}
			key := typ + "/" + vmid
			for _, x := range []struct{ field, metric string }{
				{"diskread", "proxmox.guest.disk.read"}, {"diskwrite", "proxmox.guest.disk.write"},
				{"netin", "proxmox.guest.net.rx"}, {"netout", "proxmox.guest.net.tx"},
			} {
				v, ok := it.f(x.field)
				if !ok {
					continue
				}
				cur[key+"/"+x.field] = v
				if pv, ok := p.prev[key+"/"+x.field]; ok && elapsed > 0 {
					if d, ok := deltaF(v, pv); ok {
						b.Add(x.metric, model.TypeCount, "byte", d, kv...)
					}
				}
			}
			if ha := it.s("hastate"); ha != "" {
				st, msg := model.CheckOK, fmt.Sprintf("%s %s (%s) : HA %s, %s", typ, vmid, name, ha, state)
				switch {
				case ha == "error" || ha == "fence" || ha == "recovery":
					st = model.CheckCrit
				case ha == "started" && state != "running":
					st = model.CheckCrit
				case ha == "stopped" && state == "running", ha == "migrate", ha == "relocate", ha == "freeze":
					st = model.CheckWarn
				}
				checks = append(checks, model.Check{TS: ts, Name: "proxmox.guest.ha", Status: st, Message: msg, Tags: tags(kv...)})
			}
		}
	}
	p.prev, p.prevAt = cur, now
	b.Gauge("proxmox.guests.total", "count", float64(guests), base...)
	b.Gauge("proxmox.guests.running", "count", float64(running), base...)
	b.Gauge("proxmox.nodes.online", "node", float64(len(online)), base...)

	if err := p.tasks(ctx, ts, tags, base, &checks); err != nil {
		problems = append(problems, "tâches : "+err.Error())
	}
	if p.version == "" || now.Sub(p.versionAt) >= time.Hour {
		var ver struct {
			Version string `json:"version"`
		}
		if p.get(ctx, "/version", &ver) == nil && ver.Version != "" {
			p.version, p.versionAt = ver.Version, now
		}
	}
	conn := canConnect("proxmox", ts, true, fmt.Sprintf("Proxmox VE %s : %d nœud(s), %d invité(s) dont %d en marche",
		firstNonEmpty(p.version, "?"), nodes, guests, running), tags(base...))
	if len(problems) > 0 {
		conn.Status, conn.Message = model.CheckWarn, conn.Message+" ; lecture partielle : "+model.Truncate(strings.Join(problems, " ; "), 300)
		return append(checks, conn), "partiel : " + model.Truncate(problems[0], 200)
	}
	return append(checks, conn), "ok"
}

// tasks : dernière sauvegarde de chaque nœud (contrôle), tâches en échec (événements).
func (p *Proxmox) tasks(ctx context.Context, ts int64, tags model.TagFunc, base []string, checks *[]model.Check) error {
	var list []pveItem
	if err := p.get(ctx, "/cluster/tasks", &list); err != nil {
		return err
	}
	sort.Slice(list, func(i, j int) bool { a, _ := list[i].f("endtime"); c, _ := list[j].f("endtime"); return a < c })
	last := map[string]pveItem{}
	var events []model.Event
	maxEnd := p.since
	for _, t := range list {
		end, ok := t.f("endtime")
		status := t.s("status")
		if !ok || end == 0 || status == "" { // tâche en cours
			continue
		}
		if t.s("type") == "vzdump" {
			last[t.s("node")] = t
		}
		if int64(end) > maxEnd {
			maxEnd = int64(end)
		}
		if status == "OK" || p.since == 0 || int64(end) <= p.since {
			continue
		}
		kv := append(append([]string{}, base...), "proxmox_node", t.s("node"), "proxmox_task", t.s("type"))
		alert := "error"
		if strings.HasPrefix(status, "WARNINGS") {
			alert = "warning"
		}
		title := fmt.Sprintf("tâche %s %s sur %s : %s", t.s("type"), t.s("id"), t.s("node"), status)
		ev := model.Event{TS: int64(end) * 1000, Source: "proxmox", Type: "proxmox_task_" + t.s("type"), Title: model.Truncate(title, 200),
			Text: model.Truncate(fmt.Sprintf("%s, lancée par %s", t.s("upid"), t.s("user")), 1000), AlertType: alert, Priority: "normal",
			AggregationKey: "proxmox:" + base[1] + ":" + t.s("upid")}
		if tags != nil {
			ev.Tags = tags(kv...)
		}
		events = append(events, ev)
	}
	if p.since == 0 && maxEnd == 0 {
		maxEnd = time.Now().Unix()
	}
	p.since = maxEnd
	if len(events) > 0 && p.post != nil {
		p.post(routeEvents, map[string]any{"events": events})
	}
	nodes := make([]string, 0, len(last))
	for n := range last {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)
	for _, n := range nodes {
		t := last[n]
		end, _ := t.f("endtime")
		when := time.Unix(int64(end), 0).Format("02/01 15:04")
		st, msg := model.CheckOK, fmt.Sprintf("dernière sauvegarde de %s réussie le %s", n, when)
		switch s := t.s("status"); {
		case strings.HasPrefix(s, "WARNINGS"):
			st, msg = model.CheckWarn, fmt.Sprintf("dernière sauvegarde de %s du %s avec avertissements (%s)", n, when, s)
		case s != "OK":
			st, msg = model.CheckCrit, fmt.Sprintf("dernière sauvegarde de %s du %s en échec : %s", n, when, model.Truncate(s, 200))
		}
		*checks = append(*checks, model.Check{TS: ts, Name: "proxmox.backup", Status: st, Message: msg,
			Tags: tags(append(append([]string{}, base...), "proxmox_node", n)...)})
	}
	return nil
}
