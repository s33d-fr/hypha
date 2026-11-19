package integrations

import (
	"context"
	"testing"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

type fakeIntegration struct{ every time.Duration }

func (f *fakeIntegration) name() string            { return "faux" }
func (f *fakeIntegration) close()                  {}
func (f *fakeIntegration) period() time.Duration   { return f.every }
func (f *fakeIntegration) collect(_ context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	b.Gauge("faux.valeur", "count", 1, "instance", "192.0.2.5:6379")
	return []model.Check{{TS: now.UnixMilli(), Name: "faux.can_connect", Tags: tags("instance", "192.0.2.5:6379")}}, "ok"
}

func TestInstanceWrapper(t *testing.T) {
	base := func(kv ...string) map[string]string {
		m := map[string]string{"host": "app-01"}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	var seen Deps
	it := named("redis", "cache", Deps{Tags: map[string]string{"env": "prod"}}, func(d Deps) integration {
		seen = d
		return &fakeIntegration{every: 2 * time.Minute}
	})
	if it.name() != "redis:cache" {
		t.Fatalf("nom : %q", it.name())
	}
	if p, ok := it.(periodic); !ok || p.period() != 2*time.Minute {
		t.Fatal("la cadence de l'intégration enveloppée est perdue")
	}
	if seen.Tags[config.InstanceTag] != "cache" || seen.Tags["env"] != "prod" {
		t.Fatalf("étiquettes des ressources : %v", seen.Tags)
	}
	b := model.NewSeriesBuilder(0, base)
	checks, st := it.collect(context.Background(), time.Unix(0, 0), b, base)
	if st != "ok" || len(checks) != 1 || checks[0].Tags[config.InstanceTag] != "cache" || checks[0].Tags["instance"] != "192.0.2.5:6379" {
		t.Fatalf("contrôle : %v %+v", st, checks)
	}
	s := b.Series()
	if len(s) != 1 || s[0].Tags[config.InstanceTag] != "cache" || s[0].Tags["host"] != "app-01" {
		t.Fatalf("séries versées dans le constructeur principal : %+v", s)
	}
	if plain := named("redis", "", Deps{}, func(Deps) integration { return &fakeIntegration{} }); plain.name() != "faux" {
		t.Fatalf("entrée sans nom enveloppée : %q", plain.name())
	}
}
