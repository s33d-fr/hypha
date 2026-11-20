// Package openmetrics lit les cibles au format d'exposition Prometheus (texte 0.0.4) ou
// OpenMetrics 1.0 (texte) déclarées dans integrations.openmetrics, et les traduit en séries
// HYPHA : jauges telles quelles, compteurs en débits par seconde, histogrammes et résumés en
// .count/.sum (débits), .avg et centiles ; étiquettes gardées selon une liste blanche ; plafond
// de séries par instance ; contrôle openmetrics.can_connect.
package openmetrics

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Label : paire nom, valeur d'une étiquette Prometheus.
type Label struct{ Name, Value string }

// Sample : une ligne d'échantillon.
type Sample struct {
	Name   string // nom complet (avec _total, _bucket, _sum…)
	Labels []Label
	Value  float64
}

// Family : famille de métriques (déclarée par # TYPE, ou déduite du nom).
type Family struct {
	Name    string
	Type    string // counter, gauge, histogram, gaugehistogram, summary, info, stateset, unknown
	Unit    string
	Samples []Sample
}

// Label rend la valeur d'une étiquette.
func (s Sample) Label(name string) (string, bool) {
	for _, l := range s.Labels {
		if l.Name == name {
			return l.Value, true
		}
	}
	return "", false
}

// suffixes des échantillons d'une famille composée, du plus long au plus court.
var familySuffixes = []string{"_bucket", "_gcount", "_gsum", "_created", "_total", "_count", "_sum", "_info"}

// Parse lit une exposition texte ; une ligne illisible est comptée et sautée (bad), elle
// n'arrête pas la lecture. maxLine borne la longueur d'une ligne.
func Parse(r io.Reader) (fams []*Family, bad int, err error) {
	types := map[string]string{}
	units := map[string]string{}
	byName := map[string]*Family{}
	family := func(name string) *Family {
		if f, ok := byName[name]; ok {
			return f
		}
		t := types[name]
		if t == "" {
			t = "unknown"
		}
		f := &Family{Name: name, Type: t, Unit: units[name]}
		byName[name] = f
		fams = append(fams, f)
		return f
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" {
			continue
		}
		if line[0] == '#' {
			f := strings.Fields(line)
			if len(f) >= 3 {
				switch f[1] {
				case "TYPE":
					if len(f) >= 4 {
						t := strings.ToLower(f[3])
						if t == "untyped" {
							t = "unknown"
						}
						types[f[2]] = t
						if fam, ok := byName[f[2]]; ok && fam.Type == "unknown" {
							fam.Type = t
						}
					}
				case "UNIT":
					if len(f) >= 4 {
						units[f[2]] = f[3]
					}
				}
			}
			continue
		}
		s, ok := parseSample(line)
		if !ok {
			bad++
			continue
		}
		fam := family(familyOf(s.Name, types))
		fam.Samples = append(fam.Samples, s)
	}
	return fams, bad, sc.Err()
}

// familyOf : la famille déclarée qui porte cet échantillon (nom tel quel, ou sans suffixe).
func familyOf(name string, types map[string]string) string {
	if _, ok := types[name]; ok {
		return name
	}
	for _, suf := range familySuffixes {
		if base, ok := strings.CutSuffix(name, suf); ok {
			if _, declared := types[base]; declared {
				return base
			}
		}
	}
	return name
}

func isNameStart(c byte) bool {
	return c == '_' || c == ':' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isNameChar(c byte) bool { return isNameStart(c) || (c >= '0' && c <= '9') }

// parseSample : nom{étiquettes} valeur [horodatage] [# exemplaire].
func parseSample(line string) (Sample, bool) {
	var s Sample
	i := 0
	if i >= len(line) || !isNameStart(line[i]) {
		return s, false
	}
	for i < len(line) && isNameChar(line[i]) {
		i++
	}
	s.Name = line[:i]
	if i < len(line) && line[i] == '{' {
		labels, n, ok := parseLabels(line[i:])
		if !ok {
			return s, false
		}
		s.Labels = labels
		i += n
	}
	rest := strings.TrimLeft(line[i:], " \t")
	if rest == "" {
		return s, false
	}
	// Exemplaire OpenMetrics (« # {…} valeur ») et horodatage : seule la valeur compte.
	if j := strings.Index(rest, " #"); j >= 0 {
		rest = rest[:j]
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 || len(fields) > 2 {
		return s, false
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return s, false
	}
	s.Value = v
	return s, true
}

// parseLabels lit « {a="1",b="x\"y"} » ; rend les étiquettes et le nombre d'octets lus.
func parseLabels(s string) ([]Label, int, bool) {
	var out []Label
	i := 1 // après {
	for {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i >= len(s) {
			return nil, 0, false
		}
		if s[i] == '}' {
			return out, i + 1, true
		}
		start := i
		for i < len(s) && isNameChar(s[i]) {
			i++
		}
		name := s[start:i]
		if name == "" {
			return nil, 0, false
		}
		for i < len(s) && s[i] == ' ' {
			i++
		}
		if i+1 >= len(s) || s[i] != '=' || s[i+1] != '"' {
			return nil, 0, false
		}
		i += 2
		var val strings.Builder
		closed := false
		for i < len(s) {
			c := s[i]
			if c == '\\' && i+1 < len(s) {
				switch s[i+1] {
				case 'n':
					val.WriteByte('\n')
				case '\\':
					val.WriteByte('\\')
				case '"':
					val.WriteByte('"')
				default:
					val.WriteByte('\\')
					val.WriteByte(s[i+1])
				}
				i += 2
				continue
			}
			if c == '"' {
				closed = true
				i++
				break
			}
			val.WriteByte(c)
			i++
		}
		if !closed {
			return nil, 0, false
		}
		out = append(out, Label{Name: name, Value: val.String()})
		for i < len(s) && s[i] == ' ' {
			i++
		}
		if i < len(s) && s[i] == ',' {
			i++
		}
	}
}

// String : forme lisible d'un échantillon (tests, messages).
func (s Sample) String() string {
	var b strings.Builder
	b.WriteString(s.Name)
	if len(s.Labels) > 0 {
		b.WriteByte('{')
		for i, l := range s.Labels {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, "%s=%q", l.Name, l.Value)
		}
		b.WriteByte('}')
	}
	fmt.Fprintf(&b, " %g", s.Value)
	return b.String()
}
