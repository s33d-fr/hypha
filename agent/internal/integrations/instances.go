package integrations

import (
	"context"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// Instances d'une intégration (§ 50) : chaque entrée « instances » de la conf devient une
// intégration à part, nommée « type:nom » (état, cadence et capacités distincts), dont les
// mesures, contrôles, événements et ressources portent l'étiquette instance_name.

type instance struct {
	id, tag string
	inner   integration
}

func (w *instance) name() string { return w.id }
func (w *instance) close()       { w.inner.close() }

// period : la cadence de l'intégration enveloppée ; 0 = à chaque intervalle de l'agent.
func (w *instance) period() time.Duration {
	if p, ok := w.inner.(periodic); ok {
		return p.period()
	}
	return 0
}

func (w *instance) collect(ctx context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	t := func(kv ...string) map[string]string {
		return tags(append(append(make([]string, 0, len(kv)+2), kv...), config.InstanceTag, w.tag)...)
	}
	return w.inner.collect(ctx, now, b.With(t), t)
}

// named : l'intégration telle quelle sans nom ; nommée, enveloppée et étiquetée.
func named(kind, name string, d Deps, mk func(Deps) integration) integration {
	if name == "" {
		return mk(d)
	}
	tags := make(map[string]string, len(d.Tags)+1)
	for k, v := range d.Tags {
		tags[k] = v
	}
	tags[config.InstanceTag] = name
	d.Tags = tags
	return &instance{id: kind + ":" + name, tag: name, inner: mk(d)}
}

// instanceIntegrations : une intégration par entrée « instances ».
func instanceIntegrations(cfg config.IntegrationsConfig, d Deps) []integration {
	x := cfg.Extra
	var out []integration
	add := func(kind, name string, mk func(Deps) integration) { out = append(out, named(kind, name, d, mk)) }
	for _, c := range x.OVHCloud {
		add("ovhcloud", c.Name, func(d Deps) integration { return newOVHCloud(c, d) })
	}
	for _, c := range x.MySQL {
		add("mysql", c.Name, func(Deps) integration { return newMySQL(c) })
	}
	for _, c := range x.Redis {
		add("redis", c.Name, func(Deps) integration { return newRedis(c) })
	}
	for _, c := range x.Memcached {
		add("memcached", c.Name, func(Deps) integration { return newMemcached(c) })
	}
	for _, c := range x.RabbitMQ {
		add("rabbitmq", c.Name, func(Deps) integration { return newRabbitMQ(c) })
	}
	for _, c := range x.MongoDB {
		add("mongodb", c.Name, func(Deps) integration { return newMongoDB(c) })
	}
	for _, c := range x.OpenSearch {
		add("opensearch", c.Name, func(Deps) integration { return newOpenSearch(c) })
	}
	for _, c := range x.Kubernetes {
		add("kubernetes", c.Name, func(d Deps) integration { return newKubernetes(c, d) })
	}
	for _, c := range x.Libvirt {
		add("libvirt", c.Name, func(Deps) integration { return newLibvirt(c) })
	}
	for _, c := range x.Proxmox {
		add("proxmox", c.Name, func(d Deps) integration { return newProxmox(c, d) })
	}
	for _, c := range x.AWS {
		add("aws", c.Name, func(d Deps) integration { return newAWS(c, d) })
	}
	for _, c := range x.Scaleway {
		add("scaleway", c.Name, func(d Deps) integration { return newScaleway(c, d) })
	}
	for _, c := range x.Azure {
		add("azure", c.Name, func(d Deps) integration { return newAzure(c, d) })
	}
	for _, c := range x.GCP {
		add("gcp", c.Name, func(d Deps) integration { return newGCP(c, d) })
	}
	return out
}
