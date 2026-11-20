// Package nagios lance les plugins Nagios, Monitoring Plugins et Centreon déclarés dans la conf
// locale de l'hôte (integrations.nagios) : code de sortie 0/1/2/3 → contrôle nagios.check
// OK/WARN/CRIT/UNKNOWN, données de performance → métriques nagios.perfdata.
//
// Sécurité : les commandes ne viennent que des fichiers de conf de l'hôte (le serveur ne pousse
// jamais de commande) ; sous Linux, ces fichiers et l'exécutable doivent appartenir à root et
// n'être modifiables ni par le groupe ni par les autres ; lancement direct, sans shell ;
// environnement réduit ; sortie bornée ; groupe de processus tué au délai dépassé.
package nagios

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

// uomRe : unité de mesure admise après la valeur (s, ms, %, B, KB, c, b/s…).
var uomRe = regexp.MustCompile(`^[A-Za-z%/]{0,8}$`)

// Perf : une donnée de performance « 'label'=valeur[UOM];warn;crit;min;max ».
type Perf struct {
	Label      string
	Value      float64
	UOM        string
	Warn, Crit *float64
	Min, Max   *float64
}

// SplitOutput sépare le texte et les données de performance (format Nagios 3+ : après la
// première « | » de la première ligne, puis tout ce qui suit la première « | » des lignes
// suivantes).
func SplitOutput(out string) (text, long, perf string) {
	out = strings.TrimRight(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
	first, rest, _ := strings.Cut(out, "\n")
	text, p1, _ := strings.Cut(first, "|")
	var perfParts []string
	if p := strings.TrimSpace(p1); p != "" {
		perfParts = append(perfParts, p)
	}
	if rest != "" {
		l, p2, found := strings.Cut(rest, "|")
		long = strings.TrimSpace(l)
		if found {
			perfParts = append(perfParts, strings.TrimSpace(strings.ReplaceAll(p2, "\n", " ")))
		}
	}
	return strings.TrimSpace(text), long, strings.Join(perfParts, " ")
}

// ParsePerfdata lit les données de performance ; une donnée illisible ou « U » est sautée.
func ParsePerfdata(s string) []Perf {
	var out []Perf
	for _, tok := range perfTokens(s) {
		label, rest, ok := cutLabel(tok)
		if !ok || label == "" {
			continue
		}
		fields := strings.Split(rest, ";")
		val, uom, ok := splitValue(fields[0])
		if !ok {
			continue
		}
		p := Perf{Label: label, Value: val, UOM: uom}
		if len(fields) > 1 {
			p.Warn = threshold(fields[1])
		}
		if len(fields) > 2 {
			p.Crit = threshold(fields[2])
		}
		if len(fields) > 3 {
			p.Min = plain(fields[3])
		}
		if len(fields) > 4 {
			p.Max = plain(fields[4])
		}
		out = append(out, p)
	}
	return out
}

// perfTokens découpe aux espaces, sauf entre guillemets simples (” = guillemet échappé).
func perfTokens(s string) []string {
	var out []string
	var cur strings.Builder
	inQuote := false
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case r == '\'' && inQuote && i+1 < len(rs) && rs[i+1] == '\'':
			cur.WriteRune('\'')
			cur.WriteRune('\'')
			i++
		case r == '\'':
			inQuote = !inQuote
			cur.WriteRune(r)
		case (r == ' ' || r == '\t' || r == '\n') && !inQuote:
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// cutLabel : « 'mon label'=12ms » → (mon label, 12ms).
func cutLabel(tok string) (string, string, bool) {
	if strings.HasPrefix(tok, "'") {
		// Fin du libellé : le guillemet suivi de « = » qui n'est pas un guillemet doublé.
		for i := 1; i < len(tok); i++ {
			if tok[i] != '\'' {
				continue
			}
			if i+1 < len(tok) && tok[i+1] == '\'' {
				i++
				continue
			}
			if i+1 < len(tok) && tok[i+1] == '=' {
				return strings.ReplaceAll(tok[1:i], "''", "'"), tok[i+2:], true
			}
			return "", "", false
		}
		return "", "", false
	}
	l, r, ok := strings.Cut(tok, "=")
	return l, r, ok
}

// splitValue : « 12.5ms » → 12.5, ms ; « U » ou vide → refusé.
func splitValue(s string) (float64, string, bool) {
	s = strings.TrimSpace(s)
	i := 0
	for i < len(s) && (s[i] == '-' || s[i] == '+' || s[i] == '.' || s[i] == ',' || (s[i] >= '0' && s[i] <= '9') ||
		((s[i] == 'e' || s[i] == 'E') && i > 0 && i+1 < len(s) && (s[i+1] == '-' || s[i+1] == '+' || (s[i+1] >= '0' && s[i+1] <= '9')))) {
		i++
	}
	num := strings.Replace(s[:i], ",", ".", 1) // certains plugins écrivent la virgule décimale
	v, err := strconv.ParseFloat(num, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || !uomRe.MatchString(s[i:]) {
		return 0, "", false
	}
	return v, s[i:], true
}

func plain(s string) *float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	v, err := strconv.ParseFloat(strings.Replace(s, ",", ".", 1), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	return &v
}

// threshold : borne lisible d'une plage Nagios ([@][début:][fin]) ; la fin si elle existe,
// sinon le début (« 10: » = alerte sous 10).
func threshold(s string) *float64 {
	s = strings.TrimPrefix(strings.TrimSpace(s), "@")
	if s == "" {
		return nil
	}
	start, end, isRange := strings.Cut(s, ":")
	if !isRange {
		return plain(s)
	}
	if v := plain(end); v != nil {
		return v
	}
	if start == "~" {
		return nil
	}
	return plain(start)
}

// Normalize ramène l'unité Nagios à une unité HYPHA et la valeur à cette unité.
func Normalize(v float64, uom string) (float64, string) {
	switch strings.ToLower(uom) {
	case "s":
		return v, "second"
	case "ms":
		return v, "millisecond"
	case "us":
		return v / 1000, "millisecond"
	case "%":
		return v, "percent"
	case "b":
		if uom == "b" {
			return v, "" // bits : rare, laissé sans unité
		}
		return v, "byte"
	case "kb":
		return v * 1024, "byte"
	case "mb":
		return v * 1024 * 1024, "byte"
	case "gb":
		return v * 1024 * 1024 * 1024, "byte"
	case "tb":
		return v * 1024 * 1024 * 1024 * 1024, "byte"
	case "b/s":
		if uom == "b/s" {
			return v, "bit_per_second"
		}
		return v, "byte_per_second"
	case "c":
		return v, "count"
	}
	return v, ""
}
