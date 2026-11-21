package integrations

import (
	"fmt"
	"os"
	"strings"

	"s33d.fr/hypha/agent/internal/config"
)

// ── Vague 7 : applications auto-hébergées ──────────────────────────────────────────────────
//
// Nextcloud (status.php et serverinfo), Jellyfin, Syncthing et Immich lus par leur API REST, sur le
// modèle des sondes dédiées de la vague 3 (mode auto, contrôle <nom>.can_connect une fois le logiciel
// vu, instances § 50). Gitea/Forgejo, Synapse, Home Assistant et Mastodon passent par le lecteur
// OpenMetrics (profils de omprofiles/vague7.go).

func vague7Integrations(cfg config.IntegrationsConfig, d Deps) []integration {
	in, x := cfg.IntegrationsV7, cfg.V7Extra
	var out []integration
	add := func(kind, name string, mk func() integration) {
		out = append(out, named(kind, name, d, func(Deps) integration { return mk() }))
	}
	add("nextcloud", in.Nextcloud.Name, func() integration { return newNextcloud(in.Nextcloud) })
	for _, c := range x.Nextcloud {
		add("nextcloud", c.Name, func() integration { return newNextcloud(c) })
	}
	add("jellyfin", in.Jellyfin.Name, func() integration { return newJellyfin(in.Jellyfin) })
	for _, c := range x.Jellyfin {
		add("jellyfin", c.Name, func() integration { return newJellyfin(c) })
	}
	add("syncthing", in.Syncthing.Name, func() integration { return newSyncthing(in.Syncthing) })
	for _, c := range x.Syncthing {
		add("syncthing", c.Name, func() integration { return newSyncthing(c) })
	}
	add("immich", in.Immich.Name, func() integration { return newImmich(in.Immich) })
	for _, c := range x.Immich {
		add("immich", c.Name, func() integration { return newImmich(c) })
	}
	return out
}

// secretOf rend la clé posée en clair, ou celle du fichier (relu à chaque relevé : une clé changée
// est prise sans relancer l'agent). Rien n'est rendu ni affiché d'autre que la cause d'un échec.
func secretOf(inline, file string) (string, error) {
	if file == "" {
		return inline, nil
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("fichier de clé illisible : %v", err)
	}
	k := strings.TrimSpace(string(data))
	if k == "" {
		return "", fmt.Errorf("fichier de clé %s vide", file)
	}
	return k, nil
}

// boolNum : 1 pour vrai, 0 pour faux.
func boolNum(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
