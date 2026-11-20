package integrations

import (
	"context"
	"fmt"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── Couchbase Server : API REST du cluster (8091) ───────────────────────────────────────────
//
// Compte au rôle « Read-Only Admin » (ro_admin) ou « External Stats Reader ». /pools/default
// (stockage, nœuds, santé), /pools/default/buckets (opérations, éléments, mémoire et disque par
// bucket). Un nœud hors du cluster ou en mauvaise santé passe couchbase.node.health en CRIT.

const couchbaseMaxBuckets = 100

type cbStorage struct {
	RAM struct {
		Total      float64 `json:"total"`
		QuotaTotal float64 `json:"quotaTotal"`
		QuotaUsed  float64 `json:"quotaUsed"`
		Used       float64 `json:"used"`
		UsedByData float64 `json:"usedByData"`
	} `json:"ram"`
	HDD struct {
		Total      float64 `json:"total"`
		Used       float64 `json:"used"`
		UsedByData float64 `json:"usedByData"`
		Free       float64 `json:"free"`
	} `json:"hdd"`
}

type cbNode struct {
	Hostname          string             `json:"hostname"`
	ClusterMembership string             `json:"clusterMembership"`
	Status            string             `json:"status"`
	Version           string             `json:"version"`
	ThisNode          bool               `json:"thisNode"`
	Services          []string           `json:"services"`
	SystemStats       map[string]float64 `json:"systemStats"`
	InterestingStats  map[string]float64 `json:"interestingStats"`
}

type cbPool struct {
	ClusterName     string    `json:"clusterName"`
	StorageTotals   cbStorage `json:"storageTotals"`
	Nodes           []cbNode  `json:"nodes"`
	RebalanceStatus string    `json:"rebalanceStatus"`
	Balanced        *bool     `json:"balanced"`
}

type cbBucket struct {
	Name       string `json:"name"`
	BucketType string `json:"bucketType"`
	BasicStats struct {
		QuotaPercentUsed float64 `json:"quotaPercentUsed"`
		OpsPerSec        float64 `json:"opsPerSec"`
		DiskFetches      float64 `json:"diskFetches"`
		ItemCount        float64 `json:"itemCount"`
		DiskUsed         float64 `json:"diskUsed"`
		DataUsed         float64 `json:"dataUsed"`
		MemUsed          float64 `json:"memUsed"`
	} `json:"basicStats"`
	Quota struct {
		RAM float64 `json:"ram"`
	} `json:"quota"`
}

type Couchbase struct {
	probe
	cfg  config.ServiceProbe
	http *httpProbe
}

func newCouchbase(cfg config.ServiceProbe) *Couchbase {
	return &Couchbase{cfg: cfg, http: newHTTPProbe(cfg.URL, cfg.Username, cfg.Password, cfg.Insecure),
		probe: newProbe("couchbase", "couchbase.can_connect", intent(cfg.Enabled, cfg.Username != ""), "instance", cfg.URL)}
}

func (c *Couchbase) period() time.Duration { return 30 * time.Second }

func (c *Couchbase) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := c.det.skip(now); skip {
		return nil, st
	}
	if c.cfg.Username == "" && c.det.mode == "auto" {
		return nil, "absent" // l'API exige un compte
	}
	ts := model.Ms(now)
	var pool cbPool
	if err := c.http.getJSON(ctx, "/pools/default", &pool); err != nil {
		return c.fail(ts, tags, err)
	}
	c.det.ok()
	kv := c.kvs()
	if pool.ClusterName != "" {
		kv = c.kvs("couchbase_cluster", pool.ClusterName)
	}
	s := pool.StorageTotals
	b.Gauge("couchbase.ram.used", "byte", s.RAM.Used, kv...)
	b.Gauge("couchbase.ram.total", "byte", s.RAM.Total, kv...)
	b.Gauge("couchbase.ram.quota_total", "byte", s.RAM.QuotaTotal, kv...)
	b.Gauge("couchbase.ram.quota_used", "byte", s.RAM.QuotaUsed, kv...)
	b.Gauge("couchbase.ram.used_by_data", "byte", s.RAM.UsedByData, kv...)
	if s.RAM.QuotaTotal > 0 {
		b.Gauge("couchbase.ram.quota_usage", "percent", round1(100*s.RAM.QuotaUsed/s.RAM.QuotaTotal), kv...)
	}
	b.Gauge("couchbase.hdd.used", "byte", s.HDD.Used, kv...)
	b.Gauge("couchbase.hdd.total", "byte", s.HDD.Total, kv...)
	b.Gauge("couchbase.hdd.free", "byte", s.HDD.Free, kv...)
	b.Gauge("couchbase.hdd.used_by_data", "byte", s.HDD.UsedByData, kv...)
	if s.HDD.Total > 0 {
		b.Gauge("couchbase.hdd.usage", "percent", round1(100*s.HDD.Used/s.HDD.Total), kv...)
	}
	var checks []model.Check
	version := ""
	active := 0.0
	for _, n := range pool.Nodes {
		nkv := append(append([]string{}, kv...), "node", n.Hostname)
		if n.ThisNode || version == "" {
			version = strings.SplitN(n.Version, "-", 2)[0]
		}
		if n.ClusterMembership == "active" {
			active++
		}
		for k, metric := range map[string]string{"cpu_utilization_rate": "couchbase.by_node.cpu_utilization",
			"mem_free": "couchbase.by_node.mem_free", "mem_total": "couchbase.by_node.mem_total"} {
			if v, ok := n.SystemStats[k]; ok {
				b.Gauge(metric, map[bool]string{true: "percent", false: "byte"}[k == "cpu_utilization_rate"], round1(v), nkv...)
			}
		}
		for k, metric := range map[string]string{"ops": "couchbase.by_node.ops", "curr_items": "couchbase.by_node.curr_items",
			"mem_used": "couchbase.by_node.mem_used", "couch_docs_actual_disk_size": "couchbase.by_node.docs_disk_size",
			"cmd_get": "couchbase.by_node.cmd_get", "get_hits": "couchbase.by_node.get_hits"} {
			if v, ok := n.InterestingStats[k]; ok {
				b.Gauge(metric, "", v, nkv...)
			}
		}
		st, msg := model.CheckOK, fmt.Sprintf("nœud %s : %s, %s", n.Hostname, n.Status, n.ClusterMembership)
		switch {
		case n.ClusterMembership != "active" && n.ClusterMembership != "inactiveAdded":
			st = model.CheckCrit
		case n.Status == "warmup":
			st = model.CheckWarn
		case n.Status != "healthy":
			st = model.CheckCrit
		}
		checks = append(checks, model.Check{TS: ts, Name: "couchbase.node.health", Status: st, Message: msg, Tags: tags(nkv...)})
	}
	b.Gauge("couchbase.nodes.active", "node", active, kv...)
	b.Gauge("couchbase.nodes.total", "node", float64(len(pool.Nodes)), kv...)
	if pool.Balanced != nil {
		v := 0.0
		if *pool.Balanced {
			v = 1
		}
		b.Gauge("couchbase.cluster.balanced", "", v, kv...)
	}
	var buckets []cbBucket
	if err := c.http.getJSON(ctx, "/pools/default/buckets", &buckets); err == nil {
		for i, bk := range buckets {
			if i >= couchbaseMaxBuckets {
				break
			}
			bkv := append(append([]string{}, kv...), "bucket", bk.Name)
			s := bk.BasicStats
			b.Gauge("couchbase.by_bucket.ops", "operation_per_second", round1(s.OpsPerSec), bkv...)
			b.Gauge("couchbase.by_bucket.item_count", "item", s.ItemCount, bkv...)
			b.Gauge("couchbase.by_bucket.mem_used", "byte", s.MemUsed, bkv...)
			b.Gauge("couchbase.by_bucket.disk_used", "byte", s.DiskUsed, bkv...)
			b.Gauge("couchbase.by_bucket.data_used", "byte", s.DataUsed, bkv...)
			b.Gauge("couchbase.by_bucket.disk_fetches", "operation_per_second", s.DiskFetches, bkv...)
			b.Gauge("couchbase.by_bucket.quota_percent_used", "percent", round1(s.QuotaPercentUsed), bkv...)
			b.Gauge("couchbase.by_bucket.ram_quota", "byte", bk.Quota.RAM, bkv...)
		}
	}
	msg := fmt.Sprintf("Couchbase %s joignable (%d nœud(s), %g actif(s))", version, len(pool.Nodes), active)
	checks = append(checks, model.Check{TS: ts, Name: "couchbase.can_connect", Status: model.CheckOK, Message: msg, Tags: tags(kv...)})
	return checks, "ok"
}
