package integrations

import (
	"context"
	"os"
	"testing"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// Essai contre la vraie API OVH (GET seulement), lancé à la main : OVH_REEL=1 et les identifiants
// OVH_ENDPOINT, OVH_APP_KEY, OVH_APP_SECRET, OVH_CONSUMER_KEY dans l'environnement.
func TestOVHCloudReel(t *testing.T) {
	if os.Getenv("OVH_REEL") != "1" {
		t.Skip("OVH_REEL=1 pour interroger la vraie API OVH")
	}
	cfg := config.OVHCloudConfig{Enabled: "true", Endpoint: os.Getenv("OVH_ENDPOINT"),
		ApplicationKey: os.Getenv("OVH_APP_KEY"), ApplicationSecret: os.Getenv("OVH_APP_SECRET"),
		ConsumerKey: os.Getenv("OVH_CONSUMER_KEY"), Interval: config.Duration(15 * time.Minute)}
	o := newOVHCloud(cfg, Deps{Post: func(string, map[string]any) {}})
	o.tags = testTags
	o.once.Do(func() {})
	o.poll(context.Background())
	b := model.NewSeriesBuilder(0, testTags)
	checks, state := o.collect(context.Background(), time.Now(), b, testTags)
	t.Logf("état : %s ; %d appels ; décalage d'horloge %s", state, o.snap.calls, o.delta)
	for _, e := range o.snap.errs {
		t.Logf("ÉCHEC %s", e)
	}
	for _, c := range checks {
		t.Logf("[%d] %s %s/%s : %s", c.Status, c.Name, c.Tags["ovh_type"], c.Tags["ovh_service"]+c.Tags["ovh_ip_block"], c.Message)
	}
	for _, s := range b.Series() {
		t.Logf("série %s %s = %v", s.Metric, s.Tags["ovh_service"], s.Points[0].Value)
	}
}
