//go:build !windows

package integrations

// platformCerts : pas de magasin de certificats système hors Windows.
func platformCerts([]string) []certInfo { return nil }
