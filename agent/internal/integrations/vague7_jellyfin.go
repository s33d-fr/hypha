package integrations

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── Jellyfin : API du serveur ──────────────────────────────────────────────────────────────
//
// /health (Healthy, Degraded, Unhealthy) et /System/Info/Public sans clé ; avec une clé d'API
// (Tableau de bord → Clés d'API), en-tête « Authorization: MediaBrowser Token="…" » (Jellyfin 12
// refuse X-Emby-Token et ?api_key=, vu sur 12.4.0) : /System/Info (redémarrage en attente, mise à
// jour), /Sessions (lectures en cours, pauses, transcodages), /Items/Counts (bibliothèques) et
// /ScheduledTasks (tâches planifiées en échec).

type Jellyfin struct {
	probe
	cfg  config.AppAPIConfig
	http *httpProbe
}

func newJellyfin(cfg config.AppAPIConfig) *Jellyfin {
	explicit := cfg.HasKey() || (cfg.URL != "" && cfg.URL != config.DefaultJellyfinURL)
	return &Jellyfin{cfg: cfg, http: newHTTPProbe(cfg.URL, "", "", cfg.Insecure),
		probe: newProbe("jellyfin", "jellyfin.can_connect", intent(cfg.Enabled, explicit), "instance", cfg.URL)}
}

func (j *Jellyfin) period() time.Duration { return 60 * time.Second }

// jellyfinCounts : champs de /Items/Counts et le type HYPHA de chacun.
var jellyfinCounts = [][2]string{
	{"MovieCount", "movie"}, {"SeriesCount", "series"}, {"EpisodeCount", "episode"}, {"ArtistCount", "artist"},
	{"ProgramCount", "program"}, {"TrailerCount", "trailer"}, {"SongCount", "song"}, {"AlbumCount", "album"},
	{"MusicVideoCount", "music_video"}, {"BoxSetCount", "box_set"}, {"BookCount", "book"},
}

type jellyfinSession struct {
	Client         string `json:"Client"`
	DeviceName     string `json:"DeviceName"`
	UserName       string `json:"UserName"`
	NowPlayingItem *struct {
		Name      string `json:"Name"`
		MediaType string `json:"MediaType"`
	} `json:"NowPlayingItem"`
	PlayState struct {
		IsPaused   bool   `json:"IsPaused"`
		PlayMethod string `json:"PlayMethod"`
	} `json:"PlayState"`
	TranscodingInfo *struct {
		IsVideoDirect bool `json:"IsVideoDirect"`
		IsAudioDirect bool `json:"IsAudioDirect"`
	} `json:"TranscodingInfo"`
}

func (j *Jellyfin) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := j.det.skip(now); skip {
		return nil, st
	}
	if j.cfg.URL == "" {
		return nil, "absent"
	}
	ts := model.Ms(now)
	body, code, err := j.http.do(ctx, http.MethodGet, "/health", nil, nil)
	health := strings.TrimSpace(string(body))
	if err == nil && health != "Healthy" && health != "Degraded" && health != "Unhealthy" {
		err = fmt.Errorf("/health : réponse inattendue (HTTP %d), est-ce bien Jellyfin ?", code)
	}
	if err != nil {
		return j.fail(ts, tags, err)
	}
	j.det.ok()
	kv := j.kvs()
	var errs []string
	checks := []model.Check{}
	st, msg := model.CheckOK, "Jellyfin en bonne santé"
	switch health {
	case "Degraded":
		st, msg = model.CheckWarn, "Jellyfin dégradé (/health : Degraded)"
	case "Unhealthy":
		st, msg = model.CheckCrit, "Jellyfin en mauvaise santé (/health : Unhealthy)"
	}
	b.Gauge("jellyfin.healthy", "", boolNum(health == "Healthy"), kv...)
	checks = append(checks, model.Check{TS: ts, Name: "jellyfin.health", Status: st, Message: msg, Tags: tags(kv...)})

	var pub struct {
		Version                string `json:"Version"`
		StartupWizardCompleted bool   `json:"StartupWizardCompleted"`
	}
	if err := j.http.getJSON(ctx, "/System/Info/Public", &pub); err == nil && pub.Version != "" {
		b.Gauge("jellyfin.info", "", 1, append(append([]string{}, kv...), "version", pub.Version)...)
	} else if err != nil {
		errs = append(errs, err.Error())
	}

	key, kerr := secretOf(j.cfg.APIKey, j.cfg.APIKeyFile)
	switch {
	case kerr != nil:
		errs = append(errs, kerr.Error())
	case key == "":
		errs = append(errs, "api_key absente : sessions, bibliothèques et tâches non lues")
	default:
		j.http.headers = map[string]string{"Authorization": `MediaBrowser Token="` + key + `"`}
		errs = append(errs, j.withKey(ctx, ts, b, tags, kv, &checks)...)
	}
	checks = append(checks, j.okCheck(ts, tags, "Jellyfin joignable"))
	if len(errs) > 0 {
		return checks, "partiel : " + strings.Join(errs, " ; ")
	}
	return checks, "ok"
}

func (j *Jellyfin) withKey(ctx context.Context, ts int64, b *model.SeriesBuilder, tags model.TagFunc, kv []string, checks *[]model.Check) []string {
	var errs []string
	var info struct {
		HasPendingRestart  bool `json:"HasPendingRestart"`
		HasUpdateAvailable bool `json:"HasUpdateAvailable"`
		IsShuttingDown     bool `json:"IsShuttingDown"`
	}
	if err := j.http.getJSON(ctx, "/System/Info", &info); err == nil {
		b.Gauge("jellyfin.pending_restart", "", boolNum(info.HasPendingRestart), kv...)
		b.Gauge("jellyfin.update_available", "", boolNum(info.HasUpdateAvailable), kv...)
	} else {
		errs = append(errs, err.Error())
	}

	// Sessions actives dans les 16 dernières minutes, comme le tableau de bord de Jellyfin.
	var sessions []jellyfinSession
	if err := j.http.getJSON(ctx, "/Sessions?activeWithinSeconds=960", &sessions); err == nil {
		var playing, paused, transcoding float64
		methods := map[string]float64{"DirectPlay": 0, "DirectStream": 0, "Transcode": 0}
		for _, s := range sessions {
			if s.NowPlayingItem == nil {
				continue
			}
			playing++
			if s.PlayState.IsPaused {
				paused++
			}
			m := s.PlayState.PlayMethod
			if s.TranscodingInfo != nil && !(s.TranscodingInfo.IsVideoDirect && s.TranscodingInfo.IsAudioDirect) {
				m = "Transcode"
			}
			if m == "Transcode" {
				transcoding++
			}
			if m != "" {
				methods[m]++
			}
		}
		b.Gauge("jellyfin.sessions.active", "session", float64(len(sessions)), kv...)
		b.Gauge("jellyfin.sessions.playing", "session", playing, kv...)
		b.Gauge("jellyfin.sessions.paused", "session", paused, kv...)
		b.Gauge("jellyfin.sessions.transcoding", "session", transcoding, kv...)
		names := make([]string, 0, len(methods))
		for m := range methods {
			names = append(names, m)
		}
		sort.Strings(names)
		for _, m := range names {
			b.Gauge("jellyfin.streams", "session", methods[m], append(append([]string{}, kv...), "play_method", snake(m))...)
		}
	} else {
		errs = append(errs, err.Error())
	}

	var counts map[string]any
	if err := j.http.getJSON(ctx, "/Items/Counts", &counts); err == nil {
		for _, c := range jellyfinCounts {
			if v, ok := num(counts[c[0]]); ok {
				b.Gauge("jellyfin.library.items", "item", v, append(append([]string{}, kv...), "type", c[1])...)
			}
		}
		if v, ok := num(counts["ItemCount"]); ok {
			b.Gauge("jellyfin.library.items_total", "item", v, kv...)
		}
	} else {
		errs = append(errs, err.Error())
	}

	var tasks []struct {
		Name                string `json:"Name"`
		State               string `json:"State"`
		LastExecutionResult *struct {
			Status       string `json:"Status"`
			ErrorMessage string `json:"ErrorMessage"`
		} `json:"LastExecutionResult"`
	}
	if err := j.http.getJSON(ctx, "/ScheduledTasks?isHidden=false", &tasks); err == nil {
		var failed []string
		running := 0.0
		for _, t := range tasks {
			if t.State == "Running" {
				running++
			}
			if r := t.LastExecutionResult; r != nil && (r.Status == "Failed" || r.Status == "Aborted") {
				m := t.Name
				if r.ErrorMessage != "" {
					m += " (" + model.Truncate(r.ErrorMessage, 100) + ")"
				}
				failed = append(failed, m)
			}
		}
		b.Gauge("jellyfin.scheduled_tasks.failed", "task", float64(len(failed)), kv...)
		b.Gauge("jellyfin.scheduled_tasks.running", "task", running, kv...)
		st, msg := model.CheckOK, fmt.Sprintf("%d tâches planifiées, aucune en échec", len(tasks))
		if len(failed) > 0 {
			sort.Strings(failed)
			st, msg = model.CheckWarn, "dernière exécution en échec : "+strings.Join(failed, " ; ")
		}
		*checks = append(*checks, model.Check{TS: ts, Name: "jellyfin.scheduled_tasks", Status: st, Message: model.Truncate(msg, 500), Tags: tags(kv...)})
	} else {
		errs = append(errs, err.Error())
	}
	return errs
}
