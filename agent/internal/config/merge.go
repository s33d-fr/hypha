package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Fusion de la conf : le fichier principal, puis conf.d/*.yaml du même dossier dans l'ordre
// alphabétique. Fusion par clé, récursive pour les objets ; une liste ou une valeur simple
// remplace la précédente, sauf les listes « instances » des intégrations, qui s'additionnent
// (§ 50) ; une clé absente garde la valeur précédente. Le parc pose par
// exemple conf.d/10-parc.yaml sans toucher au fichier principal.

type fileDoc struct {
	path string
	root *yaml.Node // objet de premier niveau ; nil pour un fichier vide
}

// parseDoc lit un fichier : YAML valide, objet au premier niveau, et types justes (décodage
// strict dans une conf vierge, pour que l'erreur nomme ce fichier et sa ligne). Une clé
// inconnue devient un avertissement, comme pour le fichier principal.
func parseDoc(path string, data []byte, warnings *[]string) (fileDoc, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fileDoc{}, fmt.Errorf("%s : %w", path, err)
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		return fileDoc{path: path}, nil
	}
	root := doc.Content[0]
	if root.Kind == yaml.ScalarNode && root.Tag == "!!null" {
		return fileDoc{path: path}, nil
	}
	if root.Kind != yaml.MappingNode {
		return fileDoc{}, fmt.Errorf("%s : un objet YAML (clé: valeur) est attendu au premier niveau", path)
	}
	scratch := Default()
	if err := decode(data, scratch); err != nil {
		return fileDoc{}, fmt.Errorf("%s : %w", path, err)
	}
	for _, w := range scratch.Warnings {
		*warnings = append(*warnings, path+" : "+w)
	}
	return fileDoc{path: path, root: root}, nil
}

// readConfD lit les fichiers *.yaml de dir (absent = rien), triés par nom ; les fichiers
// cachés (« . » en tête, fichiers temporaires d'un éditeur ou d'Ansible) sont ignorés.
func readConfD(dir string, warnings *[]string) ([]fileDoc, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s : %w", dir, err)
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || strings.HasPrefix(n, ".") || filepath.Ext(n) != ".yaml" {
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)
	var out []fileDoc
	for _, n := range names {
		p := filepath.Join(dir, n)
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("%s : %w", p, err)
		}
		d, err := parseDoc(p, data, warnings)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

// applyDocs fusionne les documents et décode le résultat par-dessus les valeurs par défaut.
func (c *Config) applyDocs(docs []fileDoc) error {
	c.origins = map[string]string{}
	merged := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, d := range docs {
		c.Files = append(c.Files, d.path)
		if declaresNagios(d.root) {
			c.NagiosFiles = append(c.NagiosFiles, d.path)
		}
		if d.root != nil {
			mergeMapping(merged, d.root, "", d.path, c.origins)
		}
	}
	if len(merged.Content) == 0 {
		return nil
	}
	if err := merged.Decode(c); err != nil {
		return fmt.Errorf("conf fusionnée (%s) : %w", strings.Join(c.Files, ", "), err)
	}
	return nil
}

// mergeMapping verse src dans dst ; origins reçoit, pour chaque chemin de clé, le fichier qui
// l'a posé en dernier.
func mergeMapping(dst, src *yaml.Node, prefix, file string, origins map[string]string) {
	for i := 0; i+1 < len(src.Content); i += 2 {
		k, v := src.Content[i], src.Content[i+1]
		p := prefix + k.Value
		markOrigins(v, p, file, origins)
		j := findKey(dst, k.Value)
		if j < 0 {
			dst.Content = append(dst.Content, k, v)
			continue
		}
		cur := dst.Content[j+1]
		if cur.Kind == yaml.MappingNode && v.Kind == yaml.MappingNode {
			mergeMapping(cur, v, p+".", file, origins)
			continue
		}
		if (k.Value == "instances" || concatLists[p]) && cur.Kind == yaml.SequenceNode && v.Kind == yaml.SequenceNode {
			// § 50 : un fichier conf.d par compte ou par grappe, les instances s'additionnent ;
			// de même les cibles OpenMetrics et les plugins Nagios (0.5.0).
			joined := *cur
			joined.Content = append(append([]*yaml.Node{}, cur.Content...), v.Content...)
			dst.Content[j+1] = &joined
			continue
		}
		dst.Content[j+1] = v
	}
}

// concatLists : listes qui s'additionnent d'un fichier à l'autre au lieu de se remplacer.
var concatLists = map[string]bool{"integrations.openmetrics": true, "integrations.nagios": true}

// declaresNagios dit si un document pose integrations.nagios.
func declaresNagios(root *yaml.Node) bool {
	if root == nil {
		return false
	}
	i := findKey(root, "integrations")
	if i < 0 || root.Content[i+1].Kind != yaml.MappingNode {
		return false
	}
	return findKey(root.Content[i+1], "nagios") >= 0
}

func markOrigins(v *yaml.Node, p, file string, origins map[string]string) {
	origins[p] = file
	if v.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(v.Content); i += 2 {
			markOrigins(v.Content[i+1], p+"."+v.Content[i].Value, file, origins)
		}
	}
}

func findKey(m *yaml.Node, key string) int {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return i
		}
	}
	return -1
}

var keyPathRe = regexp.MustCompile(`^[a-z0-9_]+(\.[a-z0-9_]+)*`)

// originOf rend le fichier d'où vient la clé nommée en tête d'un message d'erreur
// (« snmp.devices[0] (nas) : … » → fichier qui a posé snmp.devices).
func (c *Config) originOf(msg string) string {
	k := keyPathRe.FindString(msg)
	for k != "" {
		if f, ok := c.origins[k]; ok {
			return f
		}
		i := strings.LastIndex(k, ".")
		if i < 0 {
			break
		}
		k = k[:i]
	}
	return ""
}

// Clés dont la valeur ne s'affiche jamais (check-config) ni n'entre dans l'empreinte en clair.
var secretKeys = map[string]bool{
	"api_key": true, "community": true, "auth_key": true, "priv_key": true,
	"password": true, "token": true, "secret": true,
	"application_secret": true, "consumer_key": true, "application_key": true, "token_secret": true, "secret_access_key": true, "session_token": true, "secret_key": true, "client_secret": true,
}

// RedactedYAML rend la conf fusionnée et normalisée, secrets masqués.
func (c *Config) RedactedYAML() (string, error) {
	var n yaml.Node
	if err := n.Encode(c); err != nil {
		return "", err
	}
	redact(&n)
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(&n); err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	enc.Close()
	return b.String(), nil
}

func redact(n *yaml.Node) {
	switch n.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, x := range n.Content {
			redact(x)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i].Value, n.Content[i+1]
			switch {
			case secretKeys[k] && v.Kind == yaml.ScalarNode && v.Value != "":
				v.Value, v.Tag, v.Style = "****", "!!str", 0
			case (k == "dsn" || k == "uri") && v.Kind == yaml.ScalarNode:
				v.Value = redactDSN(v.Value)
			case k == "headers" && v.Kind == yaml.MappingNode:
				// En-têtes d'une cible OpenMetrics : Authorization, X-Api-Key… jamais affichés.
				for j := 1; j < len(v.Content); j += 2 {
					if x := v.Content[j]; x.Kind == yaml.ScalarNode && x.Value != "" {
						x.Value, x.Tag, x.Style = "****", "!!str", 0
					}
				}
			default:
				redact(v)
			}
		}
	}
}

var dsnPasswordRe = regexp.MustCompile(`(?i)(password\s*=\s*)(\S+)`)

// mysqlDSNRe : DSN du pilote MySQL, « utilisateur:motdepasse@tcp(hôte)/ ».
var mysqlDSNRe = regexp.MustCompile(`^([^:@/()]+):([^@]*)@`)

// redactDSN masque le mot de passe d'un DSN PostgreSQL (URL ou « clé=valeur ») ou MySQL.
func redactDSN(s string) string {
	if m := mysqlDSNRe.FindStringSubmatch(s); m != nil && !strings.HasPrefix(m[2], "//") { // pas une URL « schéma://… »
		return m[1] + ":****@" + s[len(m[0]):]
	}
	if u, err := url.Parse(s); err == nil && u.User != nil {
		if _, has := u.User.Password(); has {
			u.User = url.UserPassword(u.User.Username(), "****")
			return strings.Replace(u.String(), "%2A%2A%2A%2A", "****", 1)
		}
		return s
	}
	return dsnPasswordRe.ReplaceAllString(s, "${1}****")
}

// Hash rend l'empreinte de la conf fusionnée (secrets masqués) : agents.config_hash,
// pour repérer d'un coup d'œil les agents dont la conf diffère.
func (c *Config) Hash() string {
	y, err := c.RedactedYAML()
	if err != nil {
		return ""
	}
	h := sha256.Sum256([]byte(y))
	return hex.EncodeToString(h[:8])
}
