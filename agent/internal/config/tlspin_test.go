package config

import (
	"strings"
	"testing"
)

// tls_pin : une ou plusieurs empreintes sha256// en base64 ; HYPHA_TLS_PIN l'emporte sur le fichier.
func TestTLSPin(t *testing.T) {
	bon := "sha256//" + strings.Repeat("A", 43) + "="
	cas := []struct {
		pin    string
		erreur bool
	}{
		{bon, false},
		{bon + ";" + bon, false},
		{" " + bon + " ; ", false},
		{"sha1//abc", true},
		{"sha256//trop-court=", true},
		{";", true},
	}
	for _, k := range cas {
		c := Default()
		c.APIKey = "hph_" + strings.Repeat("a", 40)
		c.URL = "https://hypha.exemple.test"
		c.TLSPin = k.pin
		errs := c.Validate(true)
		if (len(errs) > 0) != k.erreur {
			t.Errorf("tls_pin %q : erreurs %v, attendu erreur=%v", k.pin, errs, k.erreur)
		}
	}
	t.Setenv("HYPHA_TLS_PIN", bon)
	c := Default()
	c.applyEnv()
	if c.TLSPin != bon {
		t.Errorf("HYPHA_TLS_PIN non appliquée : %q", c.TLSPin)
	}
}
