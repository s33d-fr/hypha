package integrations

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── ZooKeeper : commandes de quatre lettres (mntr, ruok) ───────────────────────────────────
//
// Depuis ZooKeeper 3.5, ces commandes doivent figurer dans 4lw.commands.whitelist (mntr, ruok,
// srvr ; ZOO_4LW_COMMANDS_WHITELIST dans l'image Docker officielle).

const zkTimeout = 5 * time.Second

var zkGauges = map[string][2]string{
	"zk_avg_latency":                {"zookeeper.latency.avg", "millisecond"},
	"zk_max_latency":                {"zookeeper.latency.max", "millisecond"},
	"zk_min_latency":                {"zookeeper.latency.min", "millisecond"},
	"zk_num_alive_connections":      {"zookeeper.connections", "connection"},
	"zk_outstanding_requests":       {"zookeeper.outstanding_requests", "request"},
	"zk_znode_count":                {"zookeeper.znode_count", "node"},
	"zk_watch_count":                {"zookeeper.watch_count", "watch"},
	"zk_ephemerals_count":           {"zookeeper.ephemerals_count", "node"},
	"zk_approximate_data_size":      {"zookeeper.approximate_data_size", "byte"},
	"zk_open_file_descriptor_count": {"zookeeper.open_file_descriptor_count", "file"},
	"zk_max_file_descriptor_count":  {"zookeeper.max_file_descriptor_count", "file"},
	"zk_followers":                  {"zookeeper.followers", "node"},
	"zk_synced_followers":           {"zookeeper.synced_followers", "node"},
	"zk_pending_syncs":              {"zookeeper.pending_syncs", "operation"},
	"zk_last_proposal_size":         {"zookeeper.last_proposal_size", "byte"},
	"zk_uptime":                     {"zookeeper.uptime", "millisecond"},
	"zk_quorum_size":                {"zookeeper.quorum_size", "node"},
}

var zkCounters = map[string][2]string{
	"zk_packets_received":       {"zookeeper.packets.received", "packet"},
	"zk_packets_sent":           {"zookeeper.packets.sent", "packet"},
	"zk_stale_requests_dropped": {"zookeeper.stale_requests_dropped", "request"},
	"zk_auth_failed_count":      {"zookeeper.auth_failed", "request"},
	"zk_connection_rejected":    {"zookeeper.connection_rejected", "connection"},
}

type ZooKeeper struct {
	probe
	cfg config.AddressProbe
}

func newZooKeeper(cfg config.AddressProbe) *ZooKeeper {
	return &ZooKeeper{cfg: cfg, probe: newProbe("zookeeper", "zookeeper.can_connect",
		intent(cfg.Enabled, cfg.Address != "127.0.0.1:2181"), "instance", cfg.Address)}
}

func (z *ZooKeeper) period() time.Duration { return 30 * time.Second }

// fourLetter envoie une commande et lit la réponse jusqu'à la fermeture par le serveur.
func fourLetter(ctx context.Context, address, cmd string) (string, error) {
	d := net.Dialer{Timeout: zkTimeout}
	conn, err := d.DialContext(ctx, "tcp", address)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(zkTimeout))
	if _, err := conn.Write([]byte(cmd)); err != nil {
		return "", err
	}
	data, err := io.ReadAll(io.LimitReader(conn, 1<<20))
	if err != nil && len(data) == 0 {
		return "", err
	}
	s := string(data)
	if strings.Contains(s, "is not executed because it is not in the whitelist") {
		return "", fmt.Errorf("%s refusée : l'ajouter à 4lw.commands.whitelist", cmd)
	}
	return s, nil
}

func parseMntr(s string) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		f := strings.SplitN(sc.Text(), "\t", 2)
		if len(f) == 2 {
			out[strings.TrimSpace(f[0])] = strings.TrimSpace(f[1])
		}
	}
	return out
}

func (z *ZooKeeper) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := z.det.skip(now); skip {
		return nil, st
	}
	ts := model.Ms(now)
	out, err := fourLetter(ctx, z.cfg.Address, "mntr")
	m := parseMntr(out)
	if err == nil && m["zk_server_state"] == "" {
		err = fmt.Errorf("réponse à mntr inattendue")
	}
	if err != nil {
		return z.fail(ts, tags, err)
	}
	z.det.ok()
	z.begin()
	defer z.end()
	kv := z.kvs()
	for k, g := range zkGauges {
		if v, err := strconv.ParseFloat(m[k], 64); err == nil {
			b.Gauge(g[0], g[1], v, kv...)
		}
	}
	for k, c := range zkCounters {
		if v, err := strconv.ParseFloat(m[k], 64); err == nil {
			z.count(b, c[0], c[1], k, v, kv...)
		}
	}
	state := m["zk_server_state"]
	for _, s := range []string{"leader", "follower", "observer", "standalone"} {
		v := 0.0
		if state == s {
			v = 1
		}
		b.Gauge("zookeeper.server_state", "", v, z.kvs("mode", s)...)
	}
	var checks []model.Check
	if r, err := fourLetter(ctx, z.cfg.Address, "ruok"); err == nil {
		st := model.CheckOK
		if strings.TrimSpace(r) != "imok" {
			st = model.CheckCrit
		}
		checks = append(checks, model.Check{TS: ts, Name: "zookeeper.ruok", Status: st, Message: "ruok : " + model.Truncate(strings.TrimSpace(r), 80), Tags: tags(kv...)})
	}
	version := strings.SplitN(m["zk_version"], "-", 2)[0]
	checks = append(checks, z.okCheck(ts, tags, fmt.Sprintf("ZooKeeper %s joignable (%s)", version, state)))
	return checks, "ok"
}
