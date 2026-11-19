package config

import "testing"

func TestRedactDSNFormats(t *testing.T) {
	for in, want := range map[string]string{
		"hypha:s3cret@tcp(127.0.0.1:3306)/":     "hypha:****@tcp(127.0.0.1:3306)/",
		"hypha:@unix(/run/mysqld/mysqld.sock)/": "hypha:****@unix(/run/mysqld/mysqld.sock)/",
		"postgres://hypha:s3cret@db:5432/x":     "postgres://hypha:****@db:5432/x",
		"host=db user=hypha password=s3cret":    "host=db user=hypha password=****",
	} {
		if got := redactDSN(in); got != want {
			t.Errorf("%q : %q, attendu %q", in, got, want)
		}
	}
}
