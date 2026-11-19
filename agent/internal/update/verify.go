// Package update fait la mise à jour signée de l'agent (docs/CONTRAT-04.md § 27).
//
// Chaîne de confiance : la clé publique ed25519 de HYPHA est embarquée dans le binaire ;
// SHA256SUMS.sig est la signature Ed25519 pure (openssl pkeyutl -sign -rawin) du fichier
// SHA256SUMS publié sous /install/ ; SHA256SUMS donne la somme du binaire de chaque
// plateforme. Rien n'est écrit à la place du binaire avant que la signature, la somme et la
// version du binaire téléchargé aient été vérifiées.
package update

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

//go:embed release_pubkey.txt
var releasePubKey string

// ErrNoPublicKey : binaire construit sans clé publique ; toute mise à jour est refusée.
var ErrNoPublicKey = errors.New("clé publique de publication absente du binaire : mise à jour refusée")

// ParsePublicKey lit une clé publique ed25519 écrite en base64 (32 octets).
func ParsePublicKey(text string) (ed25519.PublicKey, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, ErrNoPublicKey
	}
	raw, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		return nil, fmt.Errorf("clé publique illisible (base64 attendu) : %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("clé publique de %d octets (%d attendus)", len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}

// EmbeddedPublicKey rend la clé publique embarquée au build.
func EmbeddedPublicKey() (ed25519.PublicKey, error) { return ParsePublicKey(releasePubKey) }

// VerifySums vérifie que sig (base64 d'une signature Ed25519 de 64 octets) signe exactement
// sums avec pub. Signature absente, illisible ou fausse : erreur.
func VerifySums(pub ed25519.PublicKey, sums, sig []byte) error {
	if len(pub) != ed25519.PublicKeySize {
		return ErrNoPublicKey
	}
	s := strings.TrimSpace(string(sig))
	if s == "" {
		return errors.New("signature SHA256SUMS.sig absente")
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return fmt.Errorf("signature illisible (base64 attendu) : %w", err)
	}
	if len(raw) != ed25519.SignatureSize {
		return fmt.Errorf("signature de %d octets (%d attendus)", len(raw), ed25519.SignatureSize)
	}
	if !ed25519.Verify(pub, sums, raw) {
		return errors.New("signature de SHA256SUMS fausse : fichiers non publiés par HYPHA ou altérés")
	}
	return nil
}

// ParseSums lit SHA256SUMS (format de sha256sum : « somme  nom » ou « somme *nom »).
func ParseSums(data []byte) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		sum, name, ok := strings.Cut(line, " ")
		if !ok {
			return nil, fmt.Errorf("ligne de SHA256SUMS illisible : %q", line)
		}
		name = strings.TrimPrefix(strings.TrimSpace(name), "*")
		if _, err := hex.DecodeString(sum); err != nil || len(sum) != 64 {
			return nil, fmt.Errorf("somme illisible pour %q", name)
		}
		out[name] = strings.ToLower(sum)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("SHA256SUMS vide")
	}
	return out, nil
}

// AssetName rend le nom du binaire publié pour une plateforme (contrat § 5).
func AssetName(goos, goarch string) (string, error) {
	switch goos + "/" + goarch {
	case "linux/amd64", "linux/arm64":
		return "hypha-agent-" + goos + "-" + goarch, nil
	case "windows/amd64":
		return "hypha-agent-windows-amd64.exe", nil
	}
	return "", fmt.Errorf("aucun binaire publié pour %s/%s", goos, goarch)
}

// ParseVersionOutput lit la sortie de « hypha-agent version » : « hypha-agent 0.4.0 (…) ».
func ParseVersionOutput(out string) string {
	f := strings.Fields(out)
	if len(f) >= 2 && f[0] == "hypha-agent" {
		return f[1]
	}
	return ""
}
