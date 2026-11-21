package integrations

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// Sondes de la vague 7 contre les réponses relevées sur les vrais logiciels, en conteneur
// (testdata/vague7/<logiciel>/*.json) : Syncthing 2.1.7 (deux appareils reliés, un dossier partagé,
// un dossier seul ; err_* : dossier sans marqueur .stfolder), Jellyfin 12.4.0 (une lecture en cours,
// une bibliothèque de films), Nextcloud 35.0.3 (serverinfo 7.0.0, SQLite) et Immich 3.4.2.

func fixture(t *testing.T, soft, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "vague7", soft, name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// fakeAPI sert des fichiers par chemin ; auth dit si la requête porte la bonne clé.
func fakeAPI(t *testing.T, routes map[string][]byte, open map[string]bool, auth func(*http.Request) bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Path
		if q := r.URL.Query().Get("folder"); q != "" {
			key += "?folder=" + q
		}
		body, ok := routes[key]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if !open[r.URL.Path] && !auth(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// serie : la série de nom metric dont les étiquettes contiennent kv.
func serie(series map[string]model.Series, metric string, kv ...string) (model.Series, bool) {
	for _, s := range series {
		if s.Metric != metric {
			continue
		}
		ok := true
		for i := 0; i+1 < len(kv); i += 2 {
			if s.Tags[kv[i]] != kv[i+1] {
				ok = false
			}
		}
		if ok {
			return s, true
		}
	}
	return model.Series{}, false
}

func wantValue(t *testing.T, series map[string]model.Series, want float64, metric string, kv ...string) {
	t.Helper()
	s, ok := serie(series, metric, kv...)
	if !ok {
		t.Errorf("%s %v absente", metric, kv)
		return
	}
	if got := s.Points[len(s.Points)-1].Value; got != want {
		t.Errorf("%s %v = %v, attendu %v", metric, kv, got, want)
	}
}

func checkWith(checks []model.Check, name, tag, val string) (model.Check, bool) {
	for _, c := range checks {
		if c.Name == name && (tag == "" || c.Tags[tag] == val) {
			return c, true
		}
	}
	return model.Check{}, false
}

// ── Syncthing ──────────────────────────────────────────────────────────────────────────────

func syncthingRoutes(t *testing.T, errFolder bool) map[string][]byte {
	r := map[string][]byte{}
	for p, f := range map[string]string{
		"/rest/noauth/health": "health.json", "/rest/system/version": "version.json", "/rest/system/status": "system_status.json",
		"/rest/system/connections": "connections.json", "/rest/config/devices": "config_devices.json",
		"/rest/config/folders": "config_folders.json", "/rest/stats/device": "stats_device.json",
		"/rest/db/status?folder=essai": "db_status_essai.json", "/rest/db/status?folder=solo": "db_status_solo.json",
		"/rest/system/error": "system_error.json",
	} {
		r[p] = fixture(t, "syncthing", f)
	}
	if errFolder {
		r["/rest/db/status?folder=essai"] = fixture(t, "syncthing", "err_db_status_essai.json")
		r["/rest/system/error"] = fixture(t, "syncthing", "err_system_error.json")
	}
	return r
}

func TestSyncthingOnRealResponses(t *testing.T) {
	srv := fakeAPI(t, syncthingRoutes(t, false), map[string]bool{"/rest/noauth/health": true},
		func(r *http.Request) bool { return r.Header.Get("X-API-Key") == "cle-essai" })
	s := newSyncthing(config.AppAPIConfig{Enabled: "auto", URL: srv.URL, APIKey: "cle-essai"})
	checks, state, series := run2(t, s)
	if state != "ok" {
		t.Fatalf("état %q", state)
	}
	if c, ok := checkBy(checks, "syncthing.can_connect"); !ok || c.Status != model.CheckOK || !strings.Contains(c.Message, "v2.1.7") {
		t.Errorf("contrôle de joignabilité : %+v", c)
	}
	dev, ok := checkWith(checks, "syncthing.device", "device", "st-b")
	if !ok || dev.Status != model.CheckOK || dev.Tags["device_id"] != "FA7KCNA" {
		t.Errorf("appareil st-b : %+v", dev)
	}
	if _, mine := checkWith(checks, "syncthing.device", "device", "st-a"); mine {
		t.Errorf("l'appareil local ne doit pas être contrôlé")
	}
	for _, f := range []string{"essai", "solo"} {
		if c, ok := checkWith(checks, "syncthing.folder", "folder", f); !ok || c.Status != model.CheckOK {
			t.Errorf("dossier %s : %+v", f, c)
		}
	}
	wantValue(t, series, 1, "syncthing.device.connected", "device", "st-b")
	wantValue(t, series, 0, "syncthing.device.last_seen_age", "device", "st-b")
	wantValue(t, series, 1, "syncthing.devices", "state", "connected")
	wantValue(t, series, 0, "syncthing.devices", "state", "disconnected")
	wantValue(t, series, 100, "syncthing.folder.completion", "folder", "essai", "folder_label", "Essai")
	wantValue(t, series, 5, "syncthing.folder.global_files", "folder", "essai")
	wantValue(t, series, 1e6, "syncthing.folder.global_bytes", "folder", "essai")
	wantValue(t, series, 0, "syncthing.folder.error", "folder", "essai")
	wantValue(t, series, 2, "syncthing.folders", "state", "idle")
	wantValue(t, series, 1, "syncthing.info", "version", "v2.1.7")
	for _, m := range []string{"syncthing.uptime", "syncthing.memory.alloc", "syncthing.goroutines", "syncthing.system.errors",
		"syncthing.listeners.errors", "syncthing.discovery.errors"} {
		if _, ok := serie(series, m); !ok {
			t.Errorf("%s absente", m)
		}
	}
	// Second relevé identique : écarts nuls des compteurs d'octets.
	wantValue(t, series, 0, "syncthing.transfer.in")
}

func TestSyncthingFolderErrorAndDisconnectedDevice(t *testing.T) {
	routes := syncthingRoutes(t, true)
	var conns map[string]any
	json.Unmarshal(routes["/rest/system/connections"], &conns)
	for _, v := range conns["connections"].(map[string]any) {
		v.(map[string]any)["connected"] = false
	}
	routes["/rest/system/connections"], _ = json.Marshal(conns)
	srv := fakeAPI(t, routes, map[string]bool{"/rest/noauth/health": true}, func(r *http.Request) bool { return r.Header.Get("X-API-Key") == "k" })
	checks, _, series := run2(t, newSyncthing(config.AppAPIConfig{Enabled: "true", URL: srv.URL, APIKey: "k"}))
	c, _ := checkWith(checks, "syncthing.folder", "folder", "essai")
	if c.Status != model.CheckCrit || !strings.Contains(c.Message, "folder marker missing") {
		t.Errorf("dossier sans marqueur : %+v", c)
	}
	d, _ := checkWith(checks, "syncthing.device", "device", "st-b")
	if d.Status != model.CheckWarn || !strings.Contains(d.Message, "vu pour la dernière fois le 21/11/2026") {
		t.Errorf("appareil déconnecté : %+v", d)
	}
	wantValue(t, series, 1, "syncthing.folder.error", "folder", "essai")
	wantValue(t, series, 1, "syncthing.devices", "state", "disconnected")
	wantValue(t, series, 4, "syncthing.system.errors")
	if s, ok := serie(series, "syncthing.device.last_seen_age", "device", "st-b"); !ok || s.Points[0].Value <= 0 {
		t.Errorf("âge de la dernière connexion : %+v", s)
	}
}

func TestSyncthingWithoutKeyAndAbsent(t *testing.T) {
	srv := fakeAPI(t, syncthingRoutes(t, false), map[string]bool{"/rest/noauth/health": true}, func(*http.Request) bool { return false })
	checks, state, _ := run2(t, newSyncthing(config.AppAPIConfig{Enabled: "auto", URL: srv.URL}))
	if !strings.HasPrefix(state, "partiel : api_key absente") || len(checks) != 1 || checks[0].Name != "syncthing.can_connect" {
		t.Errorf("sans clé : %q %+v", state, checks)
	}
	// Mauvaise clé : la version n'est pas lue, l'état le dit.
	_, state, _ = run2(t, newSyncthing(config.AppAPIConfig{Enabled: "auto", URL: srv.URL, APIKey: "fausse"}))
	if !strings.Contains(state, "identifiants refusés") {
		t.Errorf("mauvaise clé : %q", state)
	}
	// Rien à l'adresse par défaut en mode auto : muet.
	s := newSyncthing(config.AppAPIConfig{Enabled: "auto", URL: config.DefaultSyncthingURL})
	s.http = newHTTPProbe("http://127.0.0.1:1", "", "", false)
	b := model.NewSeriesBuilder(0, testTags)
	if checks, st := s.collect(context.Background(), time.Now(), b, testTags); len(checks) != 0 || st != "absent" {
		t.Errorf("absent : %q %+v", st, checks)
	}
}

// ── Jellyfin ───────────────────────────────────────────────────────────────────────────────

func jellyfinRoutes(t *testing.T) map[string][]byte {
	r := map[string][]byte{"/health": []byte("Healthy")}
	for p, f := range map[string]string{
		"/System/Info/Public": "system_info_public.json", "/System/Info": "system_info.json", "/Sessions": "sessions.json",
		"/Items/Counts": "items_counts.json", "/ScheduledTasks": "scheduled_tasks.json",
	} {
		r[p] = fixture(t, "jellyfin", f)
	}
	return r
}

func jellyfinAuth(r *http.Request) bool {
	return r.Header.Get("Authorization") == `MediaBrowser Token="cle"`
}

func TestJellyfinOnRealResponses(t *testing.T) {
	srv := fakeAPI(t, jellyfinRoutes(t), map[string]bool{"/health": true, "/System/Info/Public": true}, jellyfinAuth)
	checks, state, series := run2(t, newJellyfin(config.AppAPIConfig{Enabled: "auto", URL: srv.URL, APIKey: "cle"}))
	if state != "ok" {
		t.Fatalf("état %q", state)
	}
	for _, n := range []string{"jellyfin.can_connect", "jellyfin.health", "jellyfin.scheduled_tasks"} {
		if c, ok := checkBy(checks, n); !ok || c.Status != model.CheckOK {
			t.Errorf("%s : %+v", n, c)
		}
	}
	wantValue(t, series, 1, "jellyfin.healthy")
	wantValue(t, series, 1, "jellyfin.info", "version", "12.4.0")
	wantValue(t, series, 1, "jellyfin.sessions.active")
	wantValue(t, series, 1, "jellyfin.sessions.playing")
	wantValue(t, series, 0, "jellyfin.sessions.paused")
	wantValue(t, series, 0, "jellyfin.sessions.transcoding")
	wantValue(t, series, 1, "jellyfin.streams", "play_method", "directplay")
	wantValue(t, series, 0, "jellyfin.streams", "play_method", "transcode")
	wantValue(t, series, 1, "jellyfin.library.items", "type", "movie")
	wantValue(t, series, 10, "jellyfin.library.items_total")
	wantValue(t, series, 0, "jellyfin.pending_restart")
	wantValue(t, series, 0, "jellyfin.update_available")
	wantValue(t, series, 0, "jellyfin.scheduled_tasks.failed")
}

func TestJellyfinDegradedAndFailedTask(t *testing.T) {
	routes := jellyfinRoutes(t)
	routes["/health"] = []byte("Degraded")
	var tasks []map[string]any
	json.Unmarshal(routes["/ScheduledTasks"], &tasks)
	tasks[0]["LastExecutionResult"] = map[string]any{"Status": "Failed", "ErrorMessage": "disque plein"}
	routes["/ScheduledTasks"], _ = json.Marshal(tasks)
	srv := fakeAPI(t, routes, map[string]bool{"/health": true, "/System/Info/Public": true}, jellyfinAuth)
	checks, _, series := run2(t, newJellyfin(config.AppAPIConfig{Enabled: "auto", URL: srv.URL, APIKey: "cle"}))
	if c, _ := checkBy(checks, "jellyfin.health"); c.Status != model.CheckWarn {
		t.Errorf("Degraded : %+v", c)
	}
	if c, _ := checkBy(checks, "jellyfin.scheduled_tasks"); c.Status != model.CheckWarn || !strings.Contains(c.Message, "Audio Normalization (disque plein)") {
		t.Errorf("tâche en échec : %+v", c)
	}
	wantValue(t, series, 1, "jellyfin.scheduled_tasks.failed")
	// Sans clé : santé et version seulement, état partiel ; une autre appli sur le port : muet.
	_, state, _ := run2(t, newJellyfin(config.AppAPIConfig{Enabled: "auto", URL: srv.URL}))
	if !strings.HasPrefix(state, "partiel : api_key absente") {
		t.Errorf("sans clé : %q", state)
	}
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("<html>")) }))
	defer other.Close()
	b := model.NewSeriesBuilder(0, testTags)
	jf := newJellyfin(config.AppAPIConfig{Enabled: "auto", URL: config.DefaultJellyfinURL})
	jf.http = newHTTPProbe(other.URL, "", "", false)
	if c, st := jf.collect(context.Background(), time.Now(), b, testTags); len(c) != 0 || st != "absent" {
		t.Errorf("autre logiciel : %q %+v", st, c)
	}
}

// ── Nextcloud ──────────────────────────────────────────────────────────────────────────────

func TestNextcloudOnRealResponses(t *testing.T) {
	routes := map[string][]byte{"/status.php": fixture(t, "nextcloud", "status.json"),
		"/ocs/v2.php/apps/serverinfo/api/v1/info": fixture(t, "nextcloud", "serverinfo.json")}
	srv := fakeAPI(t, routes, map[string]bool{"/status.php": true}, func(r *http.Request) bool {
		return r.Header.Get("NC-Token") == "jeton" && r.Header.Get("OCS-APIRequest") == "true"
	})
	checks, state, series := run2(t, newNextcloud(config.NextcloudConfig{Enabled: "auto", URL: srv.URL, Token: "jeton"}))
	if state != "ok" {
		t.Fatalf("état %q", state)
	}
	if c, _ := checkBy(checks, "nextcloud.status"); c.Status != model.CheckOK || !strings.Contains(c.Message, "35.0.3") {
		t.Errorf("état de Nextcloud : %+v", c)
	}
	wantValue(t, series, 0, "nextcloud.maintenance")
	wantValue(t, series, 1, "nextcloud.info", "version", "35.0.3")
	wantValue(t, series, 1, "nextcloud.users")
	wantValue(t, series, 84, "nextcloud.files")
	wantValue(t, series, 3, "nextcloud.storages")
	wantValue(t, series, 2, "nextcloud.shares")
	wantValue(t, series, 1, "nextcloud.shares.by_type", "type", "link")
	wantValue(t, series, 1, "nextcloud.shares.by_type", "type", "user")
	wantValue(t, series, 1, "nextcloud.shares.link_no_password")
	wantValue(t, series, 1, "nextcloud.users.active", "period", "5m")
	wantValue(t, series, 70594981888, "nextcloud.storage.free")
	wantValue(t, series, 2314240, "nextcloud.database.size", "db_type", "sqlite3")
	wantValue(t, series, 536870912, "nextcloud.php.memory_limit")
	wantValue(t, series, 0, "nextcloud.php.opcache.full")
	for _, m := range []string{"nextcloud.php.opcache.hit_rate", "nextcloud.php.opcache.memory.used", "nextcloud.php.apcu.memory.available"} {
		if _, ok := serie(series, m); !ok {
			t.Errorf("%s absente", m)
		}
	}
	// Mauvais jeton : l'état le dit, status.php reste lu.
	_, state, _ = run2(t, newNextcloud(config.NextcloudConfig{Enabled: "auto", URL: srv.URL, Token: "faux"}))
	if !strings.Contains(state, "jeton ou compte refusé") {
		t.Errorf("mauvais jeton : %q", state)
	}
}

func TestNextcloudMaintenance(t *testing.T) {
	var st map[string]any
	json.Unmarshal(fixture(t, "nextcloud", "status.json"), &st)
	st["maintenance"] = true
	body, _ := json.Marshal(st)
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status.php" {
			w.Write(body)
			return
		}
		called = true
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	checks, state, series := run2(t, newNextcloud(config.NextcloudConfig{Enabled: "auto", URL: srv.URL, Token: "jeton"}))
	if c, _ := checkBy(checks, "nextcloud.status"); c.Status != model.CheckCrit || !strings.Contains(c.Message, "mode maintenance") {
		t.Errorf("maintenance : %+v", c)
	}
	if c, _ := checkBy(checks, "nextcloud.can_connect"); c.Status != model.CheckOK {
		t.Errorf("joignable pendant la maintenance : %+v", c)
	}
	if called || state != "ok" {
		t.Errorf("serverinfo ne doit pas être lu en maintenance (%v, %q)", called, state)
	}
	wantValue(t, series, 1, "nextcloud.maintenance")
}

// ── Immich ─────────────────────────────────────────────────────────────────────────────────

func immichRoutes(t *testing.T) map[string][]byte {
	r := map[string][]byte{}
	for p, f := range map[string]string{
		"/api/server/ping": "server_ping.json", "/api/server/version": "server_version.json",
		"/api/server/storage": "server_storage.json", "/api/server/statistics": "server_statistics.json", "/api/jobs": "jobs.json",
	} {
		r[p] = fixture(t, "immich", f)
	}
	return r
}

func immichAuth(r *http.Request) bool { return r.Header.Get("x-api-key") == "cle" }

func TestImmichOnRealResponses(t *testing.T) {
	srv := fakeAPI(t, immichRoutes(t), map[string]bool{"/api/server/ping": true, "/api/server/version": true}, immichAuth)
	checks, state, series := run2(t, newImmich(config.AppAPIConfig{Enabled: "auto", URL: srv.URL, APIKey: "cle"}))
	if state != "ok" {
		t.Fatalf("état %q", state)
	}
	if c, _ := checkBy(checks, "immich.queues"); c.Status != model.CheckOK {
		t.Errorf("files : %+v", c)
	}
	wantValue(t, series, 1, "immich.info", "version", "3.4.2")
	wantValue(t, series, 37.6, "immich.storage.usage")
	wantValue(t, series, 102915489792, "immich.storage.size")
	wantValue(t, series, 3, "immich.assets", "type", "photo")
	wantValue(t, series, 0, "immich.assets", "type", "video")
	wantValue(t, series, 1, "immich.users")
	wantValue(t, series, 3, "immich.user.assets", "user", "Essai")
	wantValue(t, series, 0, "immich.jobs", "queue", "thumbnail_generation", "state", "failed")
	wantValue(t, series, 0, "immich.queue.paused", "queue", "metadata_extraction")
	if _, ok := serie(series, "immich.user.quota"); ok {
		t.Errorf("aucun quota posé dans l'essai")
	}
}

func TestImmichPausedQueueAndFailures(t *testing.T) {
	routes := immichRoutes(t)
	var jobs map[string]map[string]map[string]any
	json.Unmarshal(routes["/api/jobs"], &jobs)
	jobs["thumbnailGeneration"]["queueStatus"]["isPaused"] = true
	jobs["metadataExtraction"]["jobCounts"]["failed"] = 4
	routes["/api/jobs"], _ = json.Marshal(jobs)
	srv := fakeAPI(t, routes, map[string]bool{"/api/server/ping": true, "/api/server/version": true}, immichAuth)
	checks, _, series := run2(t, newImmich(config.AppAPIConfig{Enabled: "auto", URL: srv.URL, APIKey: "cle"}))
	c, _ := checkBy(checks, "immich.queues")
	if c.Status != model.CheckWarn || !strings.Contains(c.Message, "thumbnail_generation") || !strings.Contains(c.Message, "metadata_extraction (4)") {
		t.Errorf("files : %+v", c)
	}
	wantValue(t, series, 1, "immich.queue.paused", "queue", "thumbnail_generation")
	wantValue(t, series, 4, "immich.jobs", "queue", "metadata_extraction", "state", "failed")
	// Sans url : absent ; sans clé : partiel.
	b := model.NewSeriesBuilder(0, testTags)
	if _, st := newImmich(config.AppAPIConfig{Enabled: "auto"}).collect(context.Background(), time.Now(), b, testTags); st != "absent" {
		t.Errorf("sans url : %q", st)
	}
	_, st, _ := run2(t, newImmich(config.AppAPIConfig{Enabled: "auto", URL: srv.URL}))
	if !strings.HasPrefix(st, "partiel : api_key absente") {
		t.Errorf("sans clé : %q", st)
	}
}

// ── Clé dans un fichier, relue à chaque relevé ─────────────────────────────────────────────

func TestSecretOfFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cle")
	os.WriteFile(p, []byte("  cle\n"), 0o600)
	if k, err := secretOf("", p); err != nil || k != "cle" {
		t.Errorf("fichier : %q %v", k, err)
	}
	if k, _ := secretOf("en-clair", ""); k != "en-clair" {
		t.Errorf("en clair : %q", k)
	}
	os.WriteFile(p, nil, 0o600)
	if _, err := secretOf("", p); err == nil {
		t.Errorf("fichier vide accepté")
	}
	srv := fakeAPI(t, immichRoutes(t), map[string]bool{"/api/server/ping": true, "/api/server/version": true}, immichAuth)
	os.WriteFile(p, []byte("cle"), 0o600)
	if _, st, _ := run2(t, newImmich(config.AppAPIConfig{Enabled: "auto", URL: srv.URL, APIKeyFile: p})); st != "ok" {
		t.Errorf("clé par fichier : %q", st)
	}
}
