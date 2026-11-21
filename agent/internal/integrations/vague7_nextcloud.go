package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── Nextcloud : status.php et API serverinfo ────────────────────────────────────────────────
//
// status.php sans compte (installé, mode maintenance, mise à niveau de la base en attente, version) ;
// avec le jeton de l'application serverinfo (en-tête NC-Token) ou un compte administrateur : API
// /ocs/v2.php/apps/serverinfo/api/v1/info (comptes et comptes actifs, fichiers, partages, place
// libre, taille de la base, OPcache et APCu de PHP). Paramètres par défaut de serverinfo (skipApps,
// skipUpdate) : la sonde ne fait jamais interroger le magasin d'applications par Nextcloud.

type Nextcloud struct {
	probe
	cfg  config.NextcloudConfig
	http *httpProbe
}

func newNextcloud(cfg config.NextcloudConfig) *Nextcloud {
	return &Nextcloud{cfg: cfg, http: newHTTPProbe(cfg.URL, cfg.Username, cfg.Password, cfg.Insecure),
		probe: newProbe("nextcloud", "nextcloud.can_connect", intent(cfg.Enabled, cfg.URL != ""), "instance", cfg.URL)}
}

func (n *Nextcloud) period() time.Duration { return 60 * time.Second }

type ncServerInfo struct {
	OCS struct {
		Meta struct {
			Status     string `json:"status"`
			StatusCode int    `json:"statuscode"`
			Message    string `json:"message"`
		} `json:"meta"`
		Data struct {
			Nextcloud struct {
				System  map[string]any `json:"system"`
				Storage map[string]any `json:"storage"`
				Shares  map[string]any `json:"shares"`
			} `json:"nextcloud"`
			Server struct {
				Database struct {
					Type string `json:"type"`
					Size any    `json:"size"`
				} `json:"database"`
				PHP struct {
					MemoryLimit any `json:"memory_limit"`
					OPcache     struct {
						Enabled     any            `json:"opcache_enabled"`
						CacheFull   any            `json:"cache_full"`
						MemoryUsage map[string]any `json:"memory_usage"`
						Statistics  map[string]any `json:"opcache_statistics"`
					} `json:"opcache"`
					APCu struct {
						Cache map[string]any `json:"cache"`
						SMA   map[string]any `json:"sma"`
					} `json:"apcu"`
				} `json:"php"`
			} `json:"server"`
			ActiveUsers map[string]any `json:"activeUsers"`
		} `json:"data"`
	} `json:"ocs"`
}

// ncActive : périodes de activeUsers et leur étiquette.
var ncActive = [][2]string{{"last5minutes", "5m"}, {"last1hour", "1h"}, {"last24hours", "24h"}, {"last7days", "7d"}, {"last1month", "30d"}}

// ncShares : compteurs de partages par type.
var ncShares = [][2]string{{"num_shares_user", "user"}, {"num_shares_groups", "group"}, {"num_shares_link", "link"},
	{"num_shares_mail", "mail"}, {"num_shares_room", "room"}, {"num_fed_shares_sent", "federated_sent"},
	{"num_fed_shares_received", "federated_received"}}

func (n *Nextcloud) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := n.det.skip(now); skip {
		return nil, st
	}
	if n.cfg.URL == "" {
		return nil, "absent"
	}
	ts := model.Ms(now)
	var status struct {
		Installed      *bool  `json:"installed"`
		Maintenance    bool   `json:"maintenance"`
		NeedsDBUpgrade bool   `json:"needsDbUpgrade"`
		VersionString  string `json:"versionstring"`
		ProductName    string `json:"productname"`
	}
	err := n.http.getJSON(ctx, "/status.php", &status)
	if err == nil && status.Installed == nil {
		err = fmt.Errorf("/status.php : réponse sans « installed », est-ce bien Nextcloud ?")
	}
	if err != nil {
		return n.fail(ts, tags, err)
	}
	n.det.ok()
	kv := n.kvs()
	b.Gauge("nextcloud.maintenance", "", boolNum(status.Maintenance), kv...)
	b.Gauge("nextcloud.needs_db_upgrade", "", boolNum(status.NeedsDBUpgrade), kv...)
	if status.VersionString != "" {
		b.Gauge("nextcloud.info", "", 1, append(append([]string{}, kv...), "version", status.VersionString)...)
	}
	product := status.ProductName
	if product == "" {
		product = "Nextcloud"
	}
	st, msg := model.CheckOK, product+" "+status.VersionString+" en service"
	switch {
	case !*status.Installed:
		st, msg = model.CheckCrit, product+" n'est pas installé (assistant d'installation en attente)"
	case status.Maintenance:
		st, msg = model.CheckCrit, product+" en mode maintenance (occ maintenance:mode --off pour le lever)"
	case status.NeedsDBUpgrade:
		st, msg = model.CheckCrit, product+" attend la mise à niveau de sa base (occ upgrade)"
	}
	checks := []model.Check{{TS: ts, Name: "nextcloud.status", Status: st, Message: msg, Tags: tags(kv...)}}
	var errs []string
	token, terr := secretOf(n.cfg.Token, n.cfg.TokenFile)
	switch {
	case terr != nil:
		errs = append(errs, terr.Error())
	case token == "" && n.cfg.Username == "":
		errs = append(errs, "ni token ni compte : comptes, fichiers et stockage non lus")
	case status.Maintenance || !*status.Installed:
		// serverinfo ne répond pas en maintenance (503) : rien à lire de plus.
	default:
		if err := n.serverInfo(ctx, token, b, kv); err != nil {
			errs = append(errs, err.Error())
		}
	}
	checks = append(checks, n.okCheck(ts, tags, product+" joignable"))
	if len(errs) > 0 {
		return checks, "partiel : " + strings.Join(errs, " ; ")
	}
	return checks, "ok"
}

func (n *Nextcloud) serverInfo(ctx context.Context, token string, b *model.SeriesBuilder, kv []string) error {
	const path = "/ocs/v2.php/apps/serverinfo/api/v1/info?format=json"
	hdr := map[string]string{"OCS-APIRequest": "true", "Accept": "application/json"}
	if token != "" {
		hdr["NC-Token"] = token
	}
	data, code, err := n.http.do(ctx, http.MethodGet, path, nil, hdr)
	if err != nil {
		return err
	}
	if code == http.StatusUnauthorized {
		return fmt.Errorf("serverinfo : jeton ou compte refusé (HTTP 401)")
	}
	if err := httpStatusErr("serverinfo", code, data); err != nil {
		return err
	}
	var si ncServerInfo
	if err := json.Unmarshal(data, &si); err != nil {
		return fmt.Errorf("serverinfo : réponse illisible (%v)", err)
	}
	if si.OCS.Meta.Status != "ok" {
		return fmt.Errorf("serverinfo : %s (%d)", si.OCS.Meta.Message, si.OCS.Meta.StatusCode)
	}
	d := si.OCS.Data
	with := func(extra ...string) []string { return append(append([]string{}, kv...), extra...) }
	gauge := func(name, unit string, v any, extra ...string) {
		if f, ok := num(v); ok {
			b.Gauge(name, unit, f, with(extra...)...)
		}
	}
	gauge("nextcloud.storage.free", "byte", d.Nextcloud.System["freespace"])
	gauge("nextcloud.users", "", d.Nextcloud.Storage["num_users"])
	gauge("nextcloud.users.disabled", "", d.Nextcloud.Storage["num_disabled_users"])
	gauge("nextcloud.files", "file", d.Nextcloud.Storage["num_files"])
	gauge("nextcloud.storages", "", d.Nextcloud.Storage["num_storages"])
	gauge("nextcloud.shares", "", d.Nextcloud.Shares["num_shares"])
	gauge("nextcloud.shares.link_no_password", "", d.Nextcloud.Shares["num_shares_link_no_password"])
	for _, s := range ncShares {
		gauge("nextcloud.shares.by_type", "", d.Nextcloud.Shares[s[0]], "type", s[1])
	}
	for _, a := range ncActive {
		gauge("nextcloud.users.active", "", d.ActiveUsers[a[0]], "period", a[1])
	}
	if db := d.Server.Database; db.Type != "" {
		gauge("nextcloud.database.size", "byte", db.Size, "db_type", db.Type)
	}
	php := d.Server.PHP
	gauge("nextcloud.php.memory_limit", "byte", php.MemoryLimit)
	n.begin()
	if on, ok := num(php.OPcache.Enabled); ok && on == 1 {
		gauge("nextcloud.php.opcache.full", "", php.OPcache.CacheFull)
		gauge("nextcloud.php.opcache.memory.used", "byte", php.OPcache.MemoryUsage["used_memory"])
		gauge("nextcloud.php.opcache.memory.free", "byte", php.OPcache.MemoryUsage["free_memory"])
		gauge("nextcloud.php.opcache.memory.wasted", "byte", php.OPcache.MemoryUsage["wasted_memory"])
		if v, ok := num(php.OPcache.Statistics["opcache_hit_rate"]); ok {
			b.Gauge("nextcloud.php.opcache.hit_rate", "percent", round1(v), kv...)
		}
		if v, ok := num(php.OPcache.Statistics["oom_restarts"]); ok {
			n.count(b, "nextcloud.php.opcache.oom_restarts", "", "oom", v, kv...)
		}
	}
	if h, ok1 := num(php.APCu.Cache["num_hits"]); ok1 {
		if m, ok2 := num(php.APCu.Cache["num_misses"]); ok2 {
			n.cur["apcu_hits"], n.cur["apcu_misses"] = h, m
			n.ratio(b, "nextcloud.php.apcu.hit_rate", "apcu_hits", "apcu_misses", kv...)
		}
	}
	n.end()
	gauge("nextcloud.php.apcu.memory.available", "byte", php.APCu.SMA["avail_mem"])
	return nil
}
