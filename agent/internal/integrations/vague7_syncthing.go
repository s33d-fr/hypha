package integrations

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// ── Syncthing : API REST ────────────────────────────────────────────────────────────────────
//
// /rest/noauth/health sans clé ; avec la clé de l'interface (Actions → Configuration → Clé d'API,
// en-tête X-API-Key) : version, état du processus, appareils (connectés, en pause, vus pour la
// dernière fois), octets échangés, dossiers (état, erreur, fichiers et octets à recevoir, échecs de
// réception) et erreurs du système. /rest/db/status est coûteux pour Syncthing : relevé chaque minute,
// une requête par dossier.

type Syncthing struct {
	probe
	cfg  config.AppAPIConfig
	http *httpProbe
}

func newSyncthing(cfg config.AppAPIConfig) *Syncthing {
	explicit := cfg.HasKey() || (cfg.URL != "" && cfg.URL != config.DefaultSyncthingURL)
	return &Syncthing{cfg: cfg, http: newHTTPProbe(cfg.URL, "", "", cfg.Insecure),
		probe: newProbe("syncthing", "syncthing.can_connect", intent(cfg.Enabled, explicit), "instance", cfg.URL)}
}

func (s *Syncthing) period() time.Duration { return 60 * time.Second }

// syncthingMaxFolders : garde-fou (une requête coûteuse par dossier).
const syncthingMaxFolders = 100

type stFolderStatus struct {
	State                   string  `json:"state"`
	Error                   string  `json:"error"`
	NeedFiles               float64 `json:"needFiles"`
	NeedBytes               float64 `json:"needBytes"`
	NeedDeletes             float64 `json:"needDeletes"`
	GlobalBytes             float64 `json:"globalBytes"`
	GlobalFiles             float64 `json:"globalFiles"`
	LocalBytes              float64 `json:"localBytes"`
	LocalFiles              float64 `json:"localFiles"`
	InSyncBytes             float64 `json:"inSyncBytes"`
	PullErrors              float64 `json:"pullErrors"`
	WatchError              string  `json:"watchError"`
	ReceiveOnlyChangedFiles float64 `json:"receiveOnlyChangedFiles"`
}

func (s *Syncthing) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := s.det.skip(now); skip {
		return nil, st
	}
	if s.cfg.URL == "" {
		return nil, "absent"
	}
	ts := model.Ms(now)
	var health struct {
		Status string `json:"status"`
	}
	err := s.http.getJSON(ctx, "/rest/noauth/health", &health)
	if err == nil && health.Status != "OK" {
		err = fmt.Errorf("/rest/noauth/health : état %q, est-ce bien Syncthing ?", health.Status)
	}
	if err != nil {
		return s.fail(ts, tags, err)
	}
	s.det.ok()
	kv := s.kvs()
	key, kerr := secretOf(s.cfg.APIKey, s.cfg.APIKeyFile)
	if kerr != nil || key == "" {
		reason := "api_key absente : dossiers et appareils non lus"
		if kerr != nil {
			reason = kerr.Error()
		}
		return []model.Check{s.okCheck(ts, tags, "Syncthing joignable")}, "partiel : " + reason
	}
	s.http.headers = map[string]string{"X-API-Key": key}
	var checks []model.Check
	var errs []string

	var ver struct {
		Version string `json:"version"`
	}
	if err := s.http.getJSON(ctx, "/rest/system/version", &ver); err != nil {
		// La clé est vérifiée ici : sans elle, rien d'autre ne se lit.
		checks = append(checks, s.okCheck(ts, tags, "Syncthing joignable"))
		return checks, "partiel : " + err.Error()
	}
	b.Gauge("syncthing.info", "", 1, append(append([]string{}, kv...), "version", ver.Version)...)

	var sys struct {
		MyID       string  `json:"myID"`
		Uptime     float64 `json:"uptime"`
		Goroutines float64 `json:"goroutines"`
		Alloc      float64 `json:"alloc"`
		Sys        float64 `json:"sys"`
		Listeners  map[string]struct {
			Error *string `json:"error"`
		} `json:"connectionServiceStatus"`
		DiscoveryErrors map[string]string `json:"discoveryErrors"`
	}
	if err := s.http.getJSON(ctx, "/rest/system/status", &sys); err == nil {
		b.Gauge("syncthing.uptime", "second", sys.Uptime, kv...)
		b.Gauge("syncthing.goroutines", "", sys.Goroutines, kv...)
		b.Gauge("syncthing.memory.alloc", "byte", sys.Alloc, kv...)
		b.Gauge("syncthing.memory.sys", "byte", sys.Sys, kv...)
		bad := 0.0
		for _, l := range sys.Listeners {
			if l.Error != nil && *l.Error != "" {
				bad++
			}
		}
		b.Gauge("syncthing.listeners.errors", "", bad, kv...)
		b.Gauge("syncthing.discovery.errors", "", float64(len(sys.DiscoveryErrors)), kv...)
	} else {
		errs = append(errs, err.Error())
	}

	s.begin()
	errs = append(errs, s.devices(ctx, now, ts, b, tags, kv, sys.MyID, &checks)...)
	errs = append(errs, s.folders(ctx, ts, b, tags, kv, &checks)...)
	s.end()

	var sysErrs struct {
		Errors []struct {
			When    string `json:"when"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := s.http.getJSON(ctx, "/rest/system/error", &sysErrs); err == nil {
		b.Gauge("syncthing.system.errors", "", float64(len(sysErrs.Errors)), kv...)
	} else {
		errs = append(errs, err.Error())
	}
	checks = append(checks, s.okCheck(ts, tags, "Syncthing "+ver.Version+" joignable"))
	if len(errs) > 0 {
		return checks, "partiel : " + strings.Join(errs, " ; ")
	}
	return checks, "ok"
}

// shortID : les sept premiers caractères d'un identifiant d'appareil, comme l'interface de Syncthing.
func shortID(id string) string {
	if len(id) > 7 {
		return id[:7]
	}
	return id
}

func (s *Syncthing) devices(ctx context.Context, now time.Time, ts int64, b *model.SeriesBuilder, tags model.TagFunc, kv []string, myID string, checks *[]model.Check) []string {
	var devs []struct {
		DeviceID string `json:"deviceID"`
		Name     string `json:"name"`
		Paused   bool   `json:"paused"`
	}
	if err := s.http.getJSON(ctx, "/rest/config/devices", &devs); err != nil {
		return []string{err.Error()}
	}
	var errs []string
	var conns struct {
		Connections map[string]struct {
			Connected     bool    `json:"connected"`
			Paused        bool    `json:"paused"`
			InBytesTotal  float64 `json:"inBytesTotal"`
			OutBytesTotal float64 `json:"outBytesTotal"`
			ClientVersion string  `json:"clientVersion"`
		} `json:"connections"`
		Total struct {
			InBytesTotal  float64 `json:"inBytesTotal"`
			OutBytesTotal float64 `json:"outBytesTotal"`
		} `json:"total"`
	}
	if err := s.http.getJSON(ctx, "/rest/system/connections", &conns); err != nil {
		errs = append(errs, err.Error())
	} else {
		s.count(b, "syncthing.transfer.in", "byte", "in", conns.Total.InBytesTotal, kv...)
		s.count(b, "syncthing.transfer.out", "byte", "out", conns.Total.OutBytesTotal, kv...)
	}
	var stats map[string]struct {
		LastSeen string `json:"lastSeen"`
	}
	if err := s.http.getJSON(ctx, "/rest/stats/device", &stats); err != nil {
		errs = append(errs, err.Error())
	}
	byState := map[string]float64{"connected": 0, "disconnected": 0, "paused": 0}
	for _, d := range devs {
		if d.DeviceID == myID {
			continue
		}
		name := d.Name
		if name == "" {
			name = shortID(d.DeviceID)
		}
		dkv := append(append([]string{}, kv...), "device", name, "device_id", shortID(d.DeviceID))
		c, seen := conns.Connections[d.DeviceID]
		paused := d.Paused || (seen && c.Paused)
		connected := seen && c.Connected
		b.Gauge("syncthing.device.connected", "", boolNum(connected), dkv...)
		if seen {
			s.count(b, "syncthing.device.transfer.in", "byte", "in:"+d.DeviceID, c.InBytesTotal, dkv...)
			s.count(b, "syncthing.device.transfer.out", "byte", "out:"+d.DeviceID, c.OutBytesTotal, dkv...)
		}
		last := ""
		if st, ok := stats[d.DeviceID]; ok && st.LastSeen != "" {
			if t, err := time.Parse(time.RFC3339Nano, st.LastSeen); err == nil && t.Year() > 1970 {
				age := now.Sub(t).Seconds()
				if connected || age < 0 {
					age = 0
				}
				b.Gauge("syncthing.device.last_seen_age", "second", round1(age), dkv...)
				last = t.UTC().Format("02/01/2006 15:04 UTC")
			}
		}
		var st int
		var msg string
		switch {
		case paused:
			byState["paused"]++
			st, msg = model.CheckOK, "appareil "+name+" en pause"
		case connected:
			byState["connected"]++
			st, msg = model.CheckOK, "appareil "+name+" connecté"
			if c.ClientVersion != "" {
				msg += " (" + c.ClientVersion + ")"
			}
		default:
			byState["disconnected"]++
			st, msg = model.CheckWarn, "appareil "+name+" déconnecté"
			if last != "" {
				msg += ", vu pour la dernière fois le " + last
			} else {
				msg += ", jamais vu"
			}
		}
		*checks = append(*checks, model.Check{TS: ts, Name: "syncthing.device", Status: st, Message: msg, Tags: tags(dkv...)})
	}
	for _, k := range []string{"connected", "disconnected", "paused"} {
		b.Gauge("syncthing.devices", "", byState[k], append(append([]string{}, kv...), "state", k)...)
	}
	return errs
}

func (s *Syncthing) folders(ctx context.Context, ts int64, b *model.SeriesBuilder, tags model.TagFunc, kv []string, checks *[]model.Check) []string {
	var folders []struct {
		ID     string `json:"id"`
		Label  string `json:"label"`
		Type   string `json:"type"`
		Paused bool   `json:"paused"`
	}
	if err := s.http.getJSON(ctx, "/rest/config/folders", &folders); err != nil {
		return []string{err.Error()}
	}
	sort.Slice(folders, func(i, j int) bool { return folders[i].ID < folders[j].ID })
	var errs []string
	if len(folders) > syncthingMaxFolders {
		errs = append(errs, fmt.Sprintf("%d dossiers, %d premiers lus", len(folders), syncthingMaxFolders))
		folders = folders[:syncthingMaxFolders]
	}
	byState := map[string]float64{}
	for _, f := range folders {
		label := f.Label
		if label == "" {
			label = f.ID
		}
		fkv := append(append([]string{}, kv...), "folder", f.ID, "folder_label", label, "folder_type", f.Type)
		if f.Paused {
			byState["paused"]++
			*checks = append(*checks, model.Check{TS: ts, Name: "syncthing.folder", Status: model.CheckOK,
				Message: "dossier " + label + " en pause", Tags: tags(fkv...)})
			continue
		}
		var st stFolderStatus
		if err := s.http.getJSON(ctx, "/rest/db/status?folder="+url.QueryEscape(f.ID), &st); err != nil {
			errs = append(errs, err.Error())
			continue
		}
		state := st.State
		if state == "" {
			state = "unknown"
		}
		byState[snake(state)]++
		b.Gauge("syncthing.folder.need_files", "file", st.NeedFiles, fkv...)
		b.Gauge("syncthing.folder.need_bytes", "byte", st.NeedBytes, fkv...)
		b.Gauge("syncthing.folder.global_bytes", "byte", st.GlobalBytes, fkv...)
		b.Gauge("syncthing.folder.global_files", "file", st.GlobalFiles, fkv...)
		b.Gauge("syncthing.folder.local_bytes", "byte", st.LocalBytes, fkv...)
		b.Gauge("syncthing.folder.local_files", "file", st.LocalFiles, fkv...)
		b.Gauge("syncthing.folder.pull_errors", "file", st.PullErrors, fkv...)
		b.Gauge("syncthing.folder.error", "", boolNum(st.State == "error" || st.Error != ""), fkv...)
		completion := 100.0
		if st.GlobalBytes > 0 {
			completion = round1(100 * st.InSyncBytes / st.GlobalBytes)
		}
		b.Gauge("syncthing.folder.completion", "percent", completion, fkv...)
		cst, msg := model.CheckOK, "dossier "+label+" : "+state
		switch {
		case st.State == "error" || st.Error != "":
			cst, msg = model.CheckCrit, "dossier "+label+" en erreur : "+model.Truncate(st.Error, 300)
		case st.PullErrors > 0:
			cst, msg = model.CheckWarn, fmt.Sprintf("dossier %s : %d fichier(s) impossibles à recevoir", label, int(st.PullErrors))
		case st.WatchError != "":
			cst, msg = model.CheckWarn, "dossier "+label+" : surveillance des changements en échec ("+model.Truncate(st.WatchError, 200)+")"
		case st.NeedFiles > 0:
			msg = fmt.Sprintf("dossier %s : %s, %d fichier(s) à recevoir", label, state, int(st.NeedFiles))
		}
		*checks = append(*checks, model.Check{TS: ts, Name: "syncthing.folder", Status: cst, Message: msg, Tags: tags(fkv...)})
	}
	states := make([]string, 0, len(byState))
	for k := range byState {
		states = append(states, k)
	}
	sort.Strings(states)
	for _, k := range states {
		b.Gauge("syncthing.folders", "", byState[k], append(append([]string{}, kv...), "state", k)...)
	}
	return errs
}
