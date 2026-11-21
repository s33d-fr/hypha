package integrations

import "s33d.fr/hypha/agent/internal/config"

// vague6Integrations : intégrations de la vague 6 ; entrées principales puis instances (§ 50) de
// Jenkins, GitHub et Odoo, puis WSUS (Windows seulement, vague6_windows.go).
func vague6Integrations(cfg config.IntegrationsConfig, d Deps) []integration {
	x := cfg.V6Extra
	out := []integration{
		named("jenkins", cfg.Jenkins.Name, d, func(Deps) integration { return newJenkins(cfg.Jenkins) }),
		named("github", cfg.GitHub.Name, d, func(d Deps) integration { return newGitHub(cfg.GitHub, d) }),
		named("odoo", cfg.Odoo.Name, d, func(Deps) integration { return newOdoo(cfg.Odoo) }),
	}
	for _, c := range x.Jenkins {
		out = append(out, named("jenkins", c.Name, d, func(Deps) integration { return newJenkins(c) }))
	}
	for _, c := range x.GitHub {
		out = append(out, named("github", c.Name, d, func(d Deps) integration { return newGitHub(c, d) }))
	}
	for _, c := range x.Odoo {
		out = append(out, named("odoo", c.Name, d, func(Deps) integration { return newOdoo(c) }))
	}
	return append(out, wsusIntegrations(cfg)...)
}
