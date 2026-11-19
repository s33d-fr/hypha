package checks

import (
	"context"
	"testing"
	"time"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

func TestProcessMatch(t *testing.T) {
	one, two := 1, 2
	list := []config.ProcessCheck{
		{Name: "nginx", Match: "exact", Min: &one},
		{Name: "gunicorn", Match: "contains", Min: &two},
		{Name: `^php-fpm[0-9.]*$`, Match: "regex", Min: &one, Max: &one},
		{Name: "systemd-networkd-wait", Match: "exact", Min: &one},
		{Name: "absent", Match: "exact", Min: &one},
	}
	w := newProcWatch(list)
	w.list = []procEntry{
		{name: "nginx"}, {name: "nginx"}, {name: "python3", cmdline: "/opt/app/venv/bin/python3 /opt/app/venv/bin/gunicorn app:wsgi"},
		{name: "php-fpm8.3"}, {name: "systemd-network", argv0: "systemd-networkd-wait"},
	}
	w.listAt = time.Now()
	tags := func(kv ...string) map[string]string {
		m := map[string]string{"host": "h"}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	series, checks, err := w.run(context.Background(), time.Now(), tags)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"nginx": model.CheckOK, "gunicorn": model.CheckCrit, `^php-fpm[0-9.]*$`: model.CheckOK,
		"systemd-networkd-wait": model.CheckOK, "absent": model.CheckCrit}
	for _, c := range checks {
		if c.Name != "process.up" || want[c.Tags["process"]] != c.Status {
			t.Errorf("%s : statut %d (%s)", c.Tags["process"], c.Status, c.Message)
		}
	}
	count := map[string]float64{}
	for _, s := range series {
		count[s.Tags["process"]] = s.Points[0].Value
	}
	if count["nginx"] != 2 || count["gunicorn"] != 1 || count["absent"] != 0 {
		t.Fatalf("process.count : %v", count)
	}
}
