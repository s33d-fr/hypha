package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/hostfs"
	"s33d.fr/hypha/agent/internal/model"
)

// ── Ceph : « ceph status » et « ceph df » en JSON ───────────────────────────────────────────
//
// Sur un nœud de la grappe (mon, mgr ou client admin) : l'outil ceph et un trousseau lisible
// (/etc/ceph/ceph.client.admin.keyring, ou un client en lecture seule « mon 'allow r' » posé par
// command : « ceph --id hypha »). En mode auto, la sonde ne s'active que si /etc/ceph/ceph.conf
// existe. Un seul agent par grappe suffit : chaque mesure porte l'identifiant de la grappe (fsid).

const cephMaxPools = 200

type cephStatus struct {
	FSID   string `json:"fsid"`
	Health struct {
		Status string `json:"status"`
		Checks map[string]struct {
			Severity string `json:"severity"`
			Summary  struct {
				Message string `json:"message"`
			} `json:"summary"`
			Muted bool `json:"muted"`
		} `json:"checks"`
	} `json:"health"`
	MonMap struct {
		NumMons float64 `json:"num_mons"`
		Mons    []any   `json:"mons"`
	} `json:"monmap"`
	QuorumNames []string        `json:"quorum_names"`
	OSDMap      json.RawMessage `json:"osdmap"`
	PGMap       struct {
		PGsByState []struct {
			State string  `json:"state_name"`
			Count float64 `json:"count"`
		} `json:"pgs_by_state"`
		NumPGs        float64 `json:"num_pgs"`
		NumPools      float64 `json:"num_pools"`
		NumObjects    float64 `json:"num_objects"`
		BytesUsed     float64 `json:"bytes_used"`
		BytesAvail    float64 `json:"bytes_avail"`
		BytesTotal    float64 `json:"bytes_total"`
		ReadBytesSec  float64 `json:"read_bytes_sec"`
		WriteBytesSec float64 `json:"write_bytes_sec"`
		ReadOpSec     float64 `json:"read_op_per_sec"`
		WriteOpSec    float64 `json:"write_op_per_sec"`
	} `json:"pgmap"`
}

type cephOSDMap struct {
	NumOSDs   float64 `json:"num_osds"`
	NumUpOSDs float64 `json:"num_up_osds"`
	NumInOSDs float64 `json:"num_in_osds"`
	Remapped  float64 `json:"num_remapped_pgs"`
}

type cephDF struct {
	Stats struct {
		TotalBytes     float64 `json:"total_bytes"`
		TotalAvail     float64 `json:"total_avail_bytes"`
		TotalUsedRaw   float64 `json:"total_used_raw_bytes"`
		TotalUsedRatio float64 `json:"total_used_raw_ratio"`
	} `json:"stats"`
	Pools []struct {
		Name  string `json:"name"`
		Stats struct {
			Stored      float64 `json:"stored"`
			Objects     float64 `json:"objects"`
			BytesUsed   float64 `json:"bytes_used"`
			PercentUsed float64 `json:"percent_used"`
			MaxAvail    float64 `json:"max_avail"`
		} `json:"stats"`
	} `json:"pools"`
}

type Ceph struct {
	probe
	cfg config.CommandProbe
}

func newCeph(cfg config.CommandProbe) *Ceph {
	explicit := len(cfg.Command) != 1 || cfg.Command[0] != "ceph"
	return &Ceph{cfg: cfg, probe: newProbe("ceph", "ceph.can_connect", intent(cfg.Enabled, explicit))}
}

func (c *Ceph) period() time.Duration { return 60 * time.Second }

func (c *Ceph) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := c.cfg.Command
	path := cmd[0]
	if !filepath.IsAbs(path) {
		p, ok := findTool(path)
		if !ok {
			return nil, fmt.Errorf("%s introuvable", path)
		}
		path = p
	}
	full := append(append(append([]string{}, cmd[1:]...), args...), "--format", "json", "--connect-timeout", "10")
	return runTool(ctx, c.cfg.Timeout.D(), path, full...)
}

// parseOSDMap : « osdmap » à plat (Nautilus et plus) ou imbriqué (Luminous, Mimic).
func parseOSDMap(raw json.RawMessage) cephOSDMap {
	var m cephOSDMap
	var nested struct {
		OSDMap *cephOSDMap `json:"osdmap"`
	}
	if json.Unmarshal(raw, &nested) == nil && nested.OSDMap != nil {
		return *nested.OSDMap
	}
	_ = json.Unmarshal(raw, &m)
	return m
}

func (c *Ceph) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := c.det.skip(now); skip {
		return nil, st
	}
	ts := model.Ms(now)
	if c.det.mode == "auto" && !c.det.seen && !fileExists(hostfs.Etc("ceph", "ceph.conf")) {
		return nil, "absent"
	}
	out, err := c.run(ctx, "status")
	var s cephStatus
	if err == nil {
		if jerr := json.Unmarshal(out, &s); jerr != nil || s.Health.Status == "" {
			err = fmt.Errorf("ceph status illisible")
		}
	}
	if err != nil {
		return c.fail(ts, tags, err)
	}
	c.det.ok()
	kv := c.kvs("ceph_fsid", s.FSID)
	osd := parseOSDMap(s.OSDMap)
	mons := s.MonMap.NumMons
	if mons == 0 {
		mons = float64(len(s.MonMap.Mons))
	}
	b.Gauge("ceph.num_mons", "node", mons, kv...)
	b.Gauge("ceph.num_in_quorum", "node", float64(len(s.QuorumNames)), kv...)
	b.Gauge("ceph.num_osds", "node", osd.NumOSDs, kv...)
	b.Gauge("ceph.num_up_osds", "node", osd.NumUpOSDs, kv...)
	b.Gauge("ceph.num_in_osds", "node", osd.NumInOSDs, kv...)
	b.Gauge("ceph.num_remapped_pgs", "object", osd.Remapped, kv...)
	p := s.PGMap
	b.Gauge("ceph.num_pgs", "object", p.NumPGs, kv...)
	b.Gauge("ceph.num_pools", "object", p.NumPools, kv...)
	b.Gauge("ceph.num_objects", "object", p.NumObjects, kv...)
	b.Gauge("ceph.total_bytes", "byte", p.BytesTotal, kv...)
	b.Gauge("ceph.used_bytes", "byte", p.BytesUsed, kv...)
	b.Gauge("ceph.avail_bytes", "byte", p.BytesAvail, kv...)
	if p.BytesTotal > 0 {
		b.Gauge("ceph.aggregate_pct_used", "percent", round1(100*p.BytesUsed/p.BytesTotal), kv...)
	}
	b.Gauge("ceph.read_bytes_sec", "byte_per_second", p.ReadBytesSec, kv...)
	b.Gauge("ceph.write_bytes_sec", "byte_per_second", p.WriteBytesSec, kv...)
	b.Gauge("ceph.read_op_per_sec", "operation_per_second", p.ReadOpSec, kv...)
	b.Gauge("ceph.write_op_per_sec", "operation_per_second", p.WriteOpSec, kv...)
	clean := 0.0
	for _, st := range p.PGsByState {
		b.Gauge("ceph.pgs", "object", st.Count, append(append([]string{}, kv...), "pg_state", st.State)...)
		if st.State == "active+clean" {
			clean = st.Count
		}
	}
	if p.NumPGs > 0 {
		b.Gauge("ceph.pgs.active_clean_pct", "percent", round1(100*clean/p.NumPGs), kv...)
	}
	if out, err := c.run(ctx, "df"); err == nil {
		var df cephDF
		if json.Unmarshal(out, &df) == nil {
			for i, pool := range df.Pools {
				if i >= cephMaxPools {
					break
				}
				pkv := append(append([]string{}, kv...), "ceph_pool", pool.Name)
				b.Gauge("ceph.pool.stored", "byte", pool.Stats.Stored, pkv...)
				b.Gauge("ceph.pool.objects", "object", pool.Stats.Objects, pkv...)
				b.Gauge("ceph.pool.max_avail", "byte", pool.Stats.MaxAvail, pkv...)
				b.Gauge("ceph.pool.pct_used", "percent", round1(100*pool.Stats.PercentUsed), pkv...)
			}
		}
	}
	// Santé : HEALTH_OK, HEALTH_WARN, HEALTH_ERR, avec le résumé des contrôles non masqués.
	st := model.CheckOK
	switch s.Health.Status {
	case "HEALTH_WARN":
		st = model.CheckWarn
	case "HEALTH_ERR":
		st = model.CheckCrit
	case "HEALTH_OK":
	default:
		st = model.CheckUnknown
	}
	var names []string
	for k := range s.Health.Checks {
		names = append(names, k)
	}
	sort.Strings(names)
	var parts []string
	for _, k := range names {
		ch := s.Health.Checks[k]
		if ch.Muted {
			continue
		}
		parts = append(parts, k+" : "+ch.Summary.Message)
	}
	msg := s.Health.Status
	if len(parts) > 0 {
		msg += " — " + strings.Join(parts, " ; ")
	}
	checks := []model.Check{
		{TS: ts, Name: "ceph.overall_status", Status: st, Message: model.Truncate(msg, 500), Tags: tags(kv...)},
		{TS: ts, Name: "ceph.can_connect", Status: model.CheckOK,
			Message: fmt.Sprintf("grappe %s lue (%g OSD, %g en service)", s.FSID, osd.NumOSDs, osd.NumUpOSDs), Tags: tags(kv...)},
	}
	return checks, "ok"
}
