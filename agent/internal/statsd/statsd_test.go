package statsd

import (
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestAggregation(t *testing.T) {
	s := New("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, l := range []string{
		"req:1|c|#route:/api,Code:200",
		"req:2|c|@0.5|#route:/api,Code:200",
		"lat:10|ms", "lat:20|ms", "lat:30|ms", "lat:40|ms",
		"queue:3|g", "queue:7|g",
		"users:a|s", "users:b|s", "users:a|s",
		"multi:1:2:3|d",
		"_sc|base|2|#zone:a|m:injoignable",
		"_e{5,4}:titre|text|t:warning|#k:v",
		"cassé", "x:|c", "y:abc|g",
	} {
		s.handle(l)
	}
	series, checks, events, dropped := s.Flush(time.Now(), func(...string) map[string]string { return map[string]string{"host": "h"} })
	got := map[string]float64{}
	for _, x := range series {
		got[x.Metric] = x.Points[0].Value
		if x.Tags["host"] != "h" {
			t.Fatalf("étiquette host absente : %v", x.Tags)
		}
		if x.Metric == "req" && (x.Tags["route"] != "/api" || x.Tags["code"] != "200" || x.Type != "count") {
			t.Fatalf("compteur : %+v", x)
		}
	}
	want := map[string]float64{
		"req": 5, "queue": 7, "users": 2,
		"lat.avg": 25, "lat.max": 40, "lat.p50": 20, "lat.p95": 40, "lat.count": 4,
		"multi.count": 3, "multi.max": 3,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, attendu %v", k, got[k], v)
		}
	}
	if len(checks) != 1 || checks[0].Status != 2 || checks[0].Message != "injoignable" || checks[0].Tags["zone"] != "a" {
		t.Errorf("contrôle : %+v", checks)
	}
	if len(events) != 1 || events[0].Title != "titre" || events[0].Text != "text" || events[0].AlertType != "warning" {
		t.Errorf("événement : %+v", events)
	}
	if dropped != 0 {
		t.Errorf("jetés : %d", dropped)
	}
	if s2, _, _, _ := s.Flush(time.Now(), func(...string) map[string]string { return map[string]string{} }); len(s2) != 0 {
		t.Errorf("Flush ne remet pas à zéro : %d séries", len(s2))
	}
}
