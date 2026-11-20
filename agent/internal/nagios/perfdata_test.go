package nagios

import (
	"testing"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

func f(v float64) *float64 { return &v }

func eq(a, b *float64) bool { return (a == nil && b == nil) || (a != nil && b != nil && *a == *b) }

func TestSplitOutput(t *testing.T) {
	text, long, perf := SplitOutput("DISK OK - free space: / 3326 MB (56%);| /=2643MB;5948;5958;0;5968\n/ 15272 MB (77%);\n/boot 68 MB (69%);| /boot=68MB;88;93;0;98\n/home=69357MB;253404;253409;0;253414\n")
	if text != "DISK OK - free space: / 3326 MB (56%);" || long != "/ 15272 MB (77%);\n/boot 68 MB (69%);" {
		t.Fatalf("texte %q, long %q", text, long)
	}
	if perf != "/=2643MB;5948;5958;0;5968 /boot=68MB;88;93;0;98 /home=69357MB;253404;253409;0;253414" {
		t.Fatalf("perfdata %q", perf)
	}
}

func TestParsePerfdata(t *testing.T) {
	got := ParsePerfdata(`load1=0.150;5.000;10.000;0; 'temps de réponse'=0.012s;;;0 'l''étiquette'=3 time=U rta=12,5ms;100;200 pl=0%;20;60;0;100 'casse'=x 'cpu'=10:20 used=@10:20;~:30`)
	want := []Perf{
		{Label: "load1", Value: 0.15, Warn: f(5), Crit: f(10), Min: f(0)},
		{Label: "temps de réponse", Value: 0.012, UOM: "s", Min: f(0)},
		{Label: "l'étiquette", Value: 3},
		{Label: "rta", Value: 12.5, UOM: "ms", Warn: f(100), Crit: f(200)},
		{Label: "pl", Value: 0, UOM: "%", Warn: f(20), Crit: f(60), Min: f(0), Max: f(100)},
		{Label: "used", Value: 0, Warn: nil, Crit: nil},
	}
	// « used=@10:20;~:30 » n'a pas de valeur : sauté ; « cpu=10:20 » non plus (plage en valeur).
	want = want[:5]
	if len(got) != len(want) {
		t.Fatalf("%d données, %d attendues : %+v", len(got), len(want), got)
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.Label != w.Label || g.Value != w.Value || g.UOM != w.UOM || !eq(g.Warn, w.Warn) || !eq(g.Crit, w.Crit) || !eq(g.Min, w.Min) || !eq(g.Max, w.Max) {
			t.Errorf("donnée %d : %+v, attendu %+v", i, g, w)
		}
	}
	if th := threshold("@10:20"); th == nil || *th != 20 {
		t.Fatal("plage @10:20")
	}
	if th := threshold("10:"); th == nil || *th != 10 {
		t.Fatal("plage 10:")
	}
	if th := threshold("~:"); th != nil {
		t.Fatal("plage ~:")
	}
}

func TestNormalizeUnits(t *testing.T) {
	for _, c := range []struct {
		v    float64
		uom  string
		want float64
		unit string
	}{{2, "MB", 2 << 20, "byte"}, {3, "KB", 3072, "byte"}, {5, "B", 5, "byte"}, {7, "b", 7, ""}, {1500, "us", 1.5, "millisecond"},
		{9, "%", 9, "percent"}, {4, "s", 4, "second"}, {8, "c", 8, "count"}, {1, "", 1, ""}, {10, "B/s", 10, "byte_per_second"}} {
		v, u := Normalize(c.v, c.uom)
		if v != c.want || u != c.unit {
			t.Errorf("%v%s → %v %q, attendu %v %q", c.v, c.uom, v, u, c.want, c.unit)
		}
	}
}

func TestMeasure(t *testing.T) {
	tags := func(kv ...string) map[string]string {
		m := map[string]string{"host": "h"}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	c := &Check{cfg: config.NagiosCheck{Name: "disque", Tags: map[string]string{"role": "web"}}, o: Options{Tags: tags}}
	series, check := c.Measure(Result{Status: 1, Output: "DISK WARNING - / 90%|/=900MB;800;950;0;1000\n", Duration: time.Second}, time.Unix(10, 0))
	if check.Name != "nagios.check" || check.Status != model.CheckWarn || check.Message != "DISK WARNING - / 90%" ||
		check.Tags["nagios_check"] != "disque" || check.Tags["role"] != "web" {
		t.Fatalf("contrôle : %+v", check)
	}
	got := map[string]float64{}
	for _, s := range series {
		got[s.Metric+"|"+s.Tags["perf_label"]+"|"+s.Unit] = s.Points[0].Value
	}
	for k, v := range map[string]float64{"nagios.duration||second": 1, "nagios.perfdata|/|byte": 900 << 20, "nagios.perfdata.warn|/|byte": 800 << 20,
		"nagios.perfdata.crit|/|byte": 950 << 20, "nagios.perfdata.min|/|byte": 0, "nagios.perfdata.max|/|byte": 1000 << 20} {
		if got[k] != v {
			t.Errorf("%s = %v, attendu %v (%v)", k, got[k], v, got)
		}
	}
	off := false
	c.cfg.Perfdata = &off
	series, _ = c.Measure(Result{Status: 0, Output: "OK|a=1"}, time.Unix(10, 0))
	if len(series) != 0 {
		t.Fatalf("perfdata: false ignoré : %+v", series)
	}
	_, check = c.Measure(Result{Status: 3, Err: "plugin x introuvable"}, time.Unix(10, 0))
	if check.Status != model.CheckUnknown || check.Message != "plugin x introuvable" {
		t.Fatalf("erreur de lancement : %+v", check)
	}
}
