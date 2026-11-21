package sender

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// tls_pin : la clé du certificat autosigné de httptest est acceptée par son empreinte,
// refusée par une autre, et la chaîne non reconnue ne bloque plus.
func TestTLSPin(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	defer srv.Close()
	bon := PinOf(srv.Certificate().RawSubjectPublicKeyInfo)

	get := func(pin string, skip bool) error {
		tr, err := NewTransport("", skip, pin)
		if err != nil {
			return err
		}
		resp, err := (&http.Client{Transport: tr}).Get(srv.URL)
		if err == nil {
			resp.Body.Close()
		}
		return err
	}
	if err := get("", false); err == nil {
		t.Fatal("sans tls_pin, le certificat autosigné aurait dû être refusé")
	}
	if err := get(bon, false); err != nil {
		t.Fatalf("empreinte juste refusée : %v", err)
	}
	if err := get("sha256//AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="+";"+bon, false); err != nil {
		t.Fatalf("liste d'empreintes avec la bonne en second refusée : %v", err)
	}
	err := get("sha256//AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", true)
	if err == nil || !strings.Contains(err.Error(), "tls_pin") {
		t.Fatalf("mauvaise empreinte acceptée, même avec tls_skip_verify (err=%v)", err)
	}
	for _, mal := range []string{"sha1//abc", "sha256//pas-du-base64", "sha256//AAAA"} {
		if _, err := NewTransport("", false, mal); err == nil {
			t.Fatalf("tls_pin mal formé accepté : %q", mal)
		}
	}
}
