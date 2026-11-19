package integrations

import (
	"testing"

	"s33d.fr/hypha/agent/internal/model"
)

func TestNginxStubStatus(t *testing.T) {
	page := "Active connections: 291 \nserver accepts handled requests\n 16630948 16630948 31070465 \nReading: 6 Writing: 179 Waiting: 106 \n"
	s, err := parseStubStatus(page)
	if err != nil {
		t.Fatal(err)
	}
	if s.active != 291 || s.requests != 31070465 || s.reading != 6 || s.writing != 179 || s.waiting != 106 {
		t.Fatalf("lecture : %+v", s)
	}
	b := model.NewSeriesBuilder(1000, testTags)
	nginxSeries(b, s, 31070465-150, true, 15)
	got := index(b.Series())
	expectValue(t, got, "nginx.connections.active{}", 291)
	expectValue(t, got, "nginx.connections.waiting{}", 106)
	expectValue(t, got, "nginx.requests{}", 10) // 150 requêtes en 15 s

	b = model.NewSeriesBuilder(1000, testTags)
	nginxSeries(b, s, 0, false, 0)
	expectAbsent(t, index(b.Series()), "nginx.requests{}") // premier relevé : pas de débit

	if _, err := parseStubStatus("<html>404 Not Found</html>"); err == nil {
		t.Error("une page qui n'est pas stub_status doit être refusée")
	}
}
