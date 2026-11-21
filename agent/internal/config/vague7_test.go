package config

import (
	"strings"
	"testing"
)

func TestV7Defaults(t *testing.T) {
	c, errs := loadV5(t, "")
	if errs != "" {
		t.Fatalf("erreurs : %s", errs)
	}
	in := c.Integrations.IntegrationsV7
	if in.Nextcloud.Enabled != "auto" || in.Nextcloud.URL != "" || in.Immich.URL != "" ||
		in.Jellyfin.URL != DefaultJellyfinURL || in.Syncthing.URL != DefaultSyncthingURL {
		t.Fatalf("valeurs par défaut : %+v", in)
	}
}

func TestV7InstancesValidationAndRedaction(t *testing.T) {
	c, errs := loadV5(t, `integrations:
  nextcloud: {url: "https://cloud.exemple", token: "jeton-nc", name: cloud}
  jellyfin: {api_key: "cle-jf", api_key_file: /etc/hypha/jf.key}
  syncthing:
    api_key_file: relatif/cle
    instances:
      - {name: nas, url: "http://192.0.2.20:8384", api_key: "cle-st"}
      - {name: nas, url: "http://192.0.2.21:8384"}
  immich:
    instances:
      - {name: photos}
`)
	x := c.Integrations.V7Extra
	if len(x.Syncthing) != 2 || x.Syncthing[0].Enabled != "true" || x.Syncthing[0].APIKey != "cle-st" || len(x.Immich) != 1 {
		t.Fatalf("instances : %+v", x)
	}
	for _, want := range []string{
		"integrations.jellyfin : api_key et api_key_file s'excluent",
		"integrations.syncthing.api_key_file : chemin absolu attendu",
		"integrations.syncthing.instances[1] : name \"nas\" en double",
		"integrations.immich.instances[0] (photos) : url manquante",
	} {
		if !strings.Contains(errs, want) {
			t.Errorf("erreur attendue %q dans :\n%s", want, errs)
		}
	}
	y, err := c.RedactedYAML()
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"jeton-nc", "cle-jf", "cle-st"} {
		if strings.Contains(y, secret) {
			t.Errorf("secret %q affiché par check-config", secret)
		}
	}
	// Identifiants en clair vers une autre machine : remarque.
	c, _ = loadV5(t, "integrations:\n  nextcloud: {url: \"http://192.0.2.5\", username: admin, password: x}\n")
	if !strings.Contains(strings.Join(c.Warnings, "\n"), "identifiants envoyés en clair") {
		t.Errorf("remarque attendue : %v", c.Warnings)
	}
	_, errs = loadV5(t, "integrations:\n  nextcloud: {url: \"http://127.0.0.1\", username: admin}\n")
	if !strings.Contains(errs, "username et password vont ensemble") {
		t.Errorf("compte incomplet accepté : %s", errs)
	}
}
