package integrations

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── Immich : API du serveur ─────────────────────────────────────────────────────────────────
//
// /api/server/ping et /api/server/version sans clé ; avec une clé d'API (en-tête x-api-key) portant
// les permissions server.storage, server.statistics et job.read : disque de la photothèque, photos et
// vidéos (et la place prise par compte), files de tâches (actives, en attente, en échec, en pause).
// Les mesures Prometheus d'Immich (IMMICH_TELEMETRY_INCLUDE) ne donnent ni le disque ni l'état des
// files : elles restent lisibles par le lecteur OpenMetrics générique.

type Immich struct {
	probe
	cfg  config.AppAPIConfig
	http *httpProbe
}

func newImmich(cfg config.AppAPIConfig) *Immich {
	return &Immich{cfg: cfg, http: newHTTPProbe(cfg.URL, "", "", cfg.Insecure),
		probe: newProbe("immich", "immich.can_connect", intent(cfg.Enabled, cfg.URL != ""), "instance", cfg.URL)}
}

func (im *Immich) period() time.Duration { return 60 * time.Second }

// immichJobStates : compteurs d'une file (BullMQ) et leur état HYPHA.
var immichJobStates = [][2]string{{"active", "active"}, {"waiting", "waiting"}, {"delayed", "delayed"}, {"failed", "failed"}, {"paused", "paused"}}

func (im *Immich) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := im.det.skip(now); skip {
		return nil, st
	}
	if im.cfg.URL == "" {
		return nil, "absent"
	}
	ts := model.Ms(now)
	im.http.headers = nil
	var ping struct {
		Res string `json:"res"`
	}
	err := im.http.getJSON(ctx, "/api/server/ping", &ping)
	if err == nil && ping.Res != "pong" {
		err = fmt.Errorf("/api/server/ping : réponse %q, est-ce bien Immich ?", ping.Res)
	}
	if err != nil {
		return im.fail(ts, tags, err)
	}
	im.det.ok()
	kv := im.kvs()
	var errs []string
	var ver struct {
		Major, Minor, Patch int
	}
	if err := im.http.getJSON(ctx, "/api/server/version", &ver); err == nil {
		b.Gauge("immich.info", "", 1, append(append([]string{}, kv...), "version", fmt.Sprintf("%d.%d.%d", ver.Major, ver.Minor, ver.Patch))...)
	} else {
		errs = append(errs, err.Error())
	}
	var checks []model.Check
	key, kerr := secretOf(im.cfg.APIKey, im.cfg.APIKeyFile)
	switch {
	case kerr != nil:
		errs = append(errs, kerr.Error())
	case key == "":
		errs = append(errs, "api_key absente : disque, photothèque et files non lus")
	default:
		im.http.headers = map[string]string{"x-api-key": key}
		errs = append(errs, im.withKey(ctx, ts, b, tags, kv, &checks)...)
	}
	checks = append(checks, im.okCheck(ts, tags, "Immich joignable"))
	if len(errs) > 0 {
		return checks, "partiel : " + strings.Join(errs, " ; ")
	}
	return checks, "ok"
}

func (im *Immich) withKey(ctx context.Context, ts int64, b *model.SeriesBuilder, tags model.TagFunc, kv []string, checks *[]model.Check) []string {
	var errs []string
	var disk struct {
		Size      float64 `json:"diskSizeRaw"`
		Use       float64 `json:"diskUseRaw"`
		Available float64 `json:"diskAvailableRaw"`
		Percent   float64 `json:"diskUsagePercentage"`
	}
	if err := im.http.getJSON(ctx, "/api/server/storage", &disk); err == nil {
		b.Gauge("immich.storage.size", "byte", disk.Size, kv...)
		b.Gauge("immich.storage.used", "byte", disk.Use, kv...)
		b.Gauge("immich.storage.available", "byte", disk.Available, kv...)
		b.Gauge("immich.storage.usage", "percent", round1(disk.Percent), kv...)
	} else {
		errs = append(errs, err.Error())
	}

	var stats struct {
		Photos      float64 `json:"photos"`
		Videos      float64 `json:"videos"`
		Usage       float64 `json:"usage"`
		UsagePhotos float64 `json:"usagePhotos"`
		UsageVideos float64 `json:"usageVideos"`
		ByUser      []struct {
			UserName string   `json:"userName"`
			UserID   string   `json:"userId"`
			Photos   float64  `json:"photos"`
			Videos   float64  `json:"videos"`
			Usage    float64  `json:"usage"`
			Quota    *float64 `json:"quotaSizeInBytes"`
		} `json:"usageByUser"`
	}
	if err := im.http.getJSON(ctx, "/api/server/statistics", &stats); err == nil {
		b.Gauge("immich.assets", "item", stats.Photos, append(append([]string{}, kv...), "type", "photo")...)
		b.Gauge("immich.assets", "item", stats.Videos, append(append([]string{}, kv...), "type", "video")...)
		b.Gauge("immich.usage", "byte", stats.Usage, kv...)
		b.Gauge("immich.usage.by_type", "byte", stats.UsagePhotos, append(append([]string{}, kv...), "type", "photo")...)
		b.Gauge("immich.usage.by_type", "byte", stats.UsageVideos, append(append([]string{}, kv...), "type", "video")...)
		b.Gauge("immich.users", "", float64(len(stats.ByUser)), kv...)
		for _, u := range stats.ByUser {
			name := u.UserName
			if name == "" {
				name = shortID(u.UserID)
			}
			ukv := append(append([]string{}, kv...), "user", name)
			b.Gauge("immich.user.usage", "byte", u.Usage, ukv...)
			b.Gauge("immich.user.assets", "item", u.Photos+u.Videos, ukv...)
			if u.Quota != nil && *u.Quota > 0 {
				b.Gauge("immich.user.quota", "byte", *u.Quota, ukv...)
				b.Gauge("immich.user.quota_usage", "percent", round1(100*u.Usage / *u.Quota), ukv...)
			}
		}
	} else {
		errs = append(errs, err.Error())
	}

	var jobs map[string]struct {
		Status struct {
			IsPaused bool `json:"isPaused"`
			IsActive bool `json:"isActive"`
		} `json:"queueStatus"`
		Counts map[string]float64 `json:"jobCounts"`
	}
	if err := im.http.getJSON(ctx, "/api/jobs", &jobs); err != nil {
		return append(errs, err.Error())
	}
	names := make([]string, 0, len(jobs))
	for q := range jobs {
		names = append(names, q)
	}
	sort.Strings(names)
	var paused, failed []string
	for _, q := range names {
		j := jobs[q]
		qkv := append(append([]string{}, kv...), "queue", snakeCamel(q))
		for _, s := range immichJobStates {
			b.Gauge("immich.jobs", "job", j.Counts[s[0]], append(append([]string{}, qkv...), "state", s[1])...)
		}
		b.Gauge("immich.queue.paused", "", boolNum(j.Status.IsPaused), qkv...)
		if j.Status.IsPaused {
			paused = append(paused, snakeCamel(q))
		}
		if j.Counts["failed"] > 0 {
			failed = append(failed, fmt.Sprintf("%s (%d)", snakeCamel(q), int(j.Counts["failed"])))
		}
	}
	st, msg := model.CheckOK, fmt.Sprintf("%d files de tâches, aucune en pause ni en échec", len(names))
	switch {
	case len(paused) > 0:
		st, msg = model.CheckWarn, "files en pause : "+strings.Join(paused, ", ")
		if len(failed) > 0 {
			msg += " ; tâches en échec : " + strings.Join(failed, ", ")
		}
	case len(failed) > 0:
		st, msg = model.CheckWarn, "tâches en échec : "+strings.Join(failed, ", ")
	}
	*checks = append(*checks, model.Check{TS: ts, Name: "immich.queues", Status: st, Message: model.Truncate(msg, 500), Tags: tags(kv...)})
	return errs
}

// snakeCamel : « thumbnailGeneration » → « thumbnail_generation » (noms des files d'Immich).
func snakeCamel(s string) string {
	var out strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				out.WriteByte('_')
			}
			out.WriteRune(r + ('a' - 'A'))
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}
