package synthetics

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Step : une étape d'un test multistep (§ 29).
type Step struct {
	Name            string            `json:"name"`
	Method          string            `json:"method"`
	URL             string            `json:"url"`
	Headers         map[string]string `json:"headers"`
	Body            string            `json:"body"`
	TimeoutS        float64           `json:"timeout_s"`
	Assertions      []Assertion       `json:"assertions"`
	Extract         []Extract         `json:"extract"`
	FollowRedirects *bool             `json:"follow_redirects"`
}

// Assertion : type status|header|body_contains|json_path|latency_ms, op eq|ne|lt|gt|contains|matches.
type Assertion struct {
	Type  string `json:"type"`
	Path  string `json:"path,omitempty"`
	Op    string `json:"op"`
	Value any    `json:"value"`
}

// Extract : variable tirée d'une réponse (json_path, header ou regex) pour les étapes suivantes.
type Extract struct {
	Name string `json:"name"`
	From string `json:"from"`
	Path string `json:"path"`
}

func stepTimeout(s Step) time.Duration {
	if s.TimeoutS <= 0 {
		return 10 * time.Second
	}
	return time.Duration(s.TimeoutS * float64(time.Second))
}

var varRe = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_.\-]+)\s*\}\}`)

// render remplace {{variable}} ; une variable inconnue reste telle quelle.
func render(s string, vars map[string]string) string {
	return varRe.ReplaceAllStringFunc(s, func(m string) string {
		name := varRe.FindStringSubmatch(m)[1]
		if v, ok := vars[name]; ok {
			return v
		}
		return m
	})
}

// probeMultistep enchaîne les étapes ; la première en échec arrête le test (les suivantes
// sont marquées non jouées). ok si toutes les assertions de toutes les étapes passent.
func probeMultistep(ctx context.Context, steps []Step, cfg map[string]any) Result {
	vars := map[string]string{}
	if v, ok := cfg["variables"].(map[string]any); ok {
		for k, x := range v {
			vars[k] = scalarString(x)
		}
	}
	if len(steps) == 0 {
		return Result{Error: "test multistep sans étape"}
	}
	start := time.Now()
	var out []map[string]any
	var last *int
	failed := ""
	for i, s := range steps {
		name := s.Name
		if name == "" {
			name = fmt.Sprintf("étape %d", i+1)
		}
		if failed != "" {
			out = append(out, map[string]any{"name": name, "ok": false, "skipped": true})
			continue
		}
		method := strings.ToUpper(s.Method)
		if method == "" {
			method = http.MethodGet
		}
		headers := map[string]string{}
		for k, v := range s.Headers {
			headers[k] = render(v, vars)
		}
		follow := s.FollowRedirects == nil || *s.FollowRedirects
		sctx, cancel := context.WithTimeout(ctx, stepTimeout(s))
		t0 := time.Now()
		res, err := doHTTP(sctx, method, render(s.URL, vars), headers, render(s.Body, vars), follow)
		cancel()
		sd := map[string]any{"name": name, "duration_ms": msSince(t0)}
		if err != nil {
			sd["ok"], sd["error"] = false, err.Error()
			out = append(out, sd)
			failed = fmt.Sprintf("%s : %v", name, err)
			continue
		}
		sd["status_code"] = res.status
		last = ptrInt(res.status)
		as := s.Assertions
		if len(as) == 0 {
			as = []Assertion{{Type: "status", Op: "lt", Value: float64(400)}}
		}
		ar, bad := evalAssertions(as, res)
		sd["assertions"] = ar
		for _, x := range s.Extract {
			v, ok := extract(x, res)
			if ok && x.Name != "" {
				vars[x.Name] = v
			} else if x.Name != "" {
				bad = append(bad, fmt.Sprintf("extraction %s impossible", x.Name))
			}
		}
		sd["ok"] = len(bad) == 0
		out = append(out, sd)
		if len(bad) > 0 {
			failed = name + " : " + strings.Join(bad, "; ")
		}
	}
	r := Result{OK: failed == "", StatusCode: last, LatencyMS: ptrMS(time.Since(start)),
		Details: map[string]any{"steps": out}, Error: failed}
	return r
}

func parseAssertions(v any) []Assertion {
	raw, err := json.Marshal(v)
	if err != nil || v == nil {
		return nil
	}
	var out []Assertion
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	return out
}

// evalAssertions rend le détail de chaque assertion et les messages des échecs.
func evalAssertions(as []Assertion, r *httpOutcome) ([]map[string]any, []string) {
	var out []map[string]any
	var failed []string
	for _, a := range as {
		actual, found := actualOf(a, r)
		op := a.Op
		if op == "" {
			op = map[string]string{"body_contains": "contains", "latency_ms": "lt"}[a.Type]
			if op == "" {
				op = "eq"
			}
		}
		ok, why := false, ""
		switch {
		case !found:
			why = "valeur introuvable"
		case a.Type == "body_contains":
			s := scalarString(a.Value)
			switch op {
			case "ne":
				ok = !strings.Contains(actual.(string), s)
			case "matches":
				ok, why = matches(actual.(string), s)
			default:
				ok = strings.Contains(actual.(string), s)
			}
		default:
			ok, why = compare(actual, op, a.Value)
		}
		d := map[string]any{"type": a.Type, "op": op, "value": a.Value, "ok": ok}
		if a.Path != "" {
			d["path"] = a.Path
		}
		if found && a.Type != "body_contains" {
			d["actual"] = actual
		}
		out = append(out, d)
		if !ok {
			msg := fmt.Sprintf("assertion %s %s %v", label(a), op, scalarString(a.Value))
			if found && a.Type != "body_contains" {
				msg += fmt.Sprintf(" (obtenu %s)", scalarString(actual))
			}
			if why != "" {
				msg += " : " + why
			}
			failed = append(failed, msg)
		}
	}
	return out, failed
}

func label(a Assertion) string {
	if a.Path != "" {
		return a.Type + " " + a.Path
	}
	return a.Type
}

func actualOf(a Assertion, r *httpOutcome) (any, bool) {
	switch a.Type {
	case "status":
		if r.status == 0 {
			return nil, false
		}
		return float64(r.status), true
	case "header":
		if r.header == nil {
			return nil, false
		}
		v := r.header.Values(a.Path)
		if len(v) == 0 {
			return nil, false
		}
		return strings.Join(v, ", "), true
	case "body_contains":
		return string(r.body), r.status != 0
	case "json_path":
		var doc any
		if json.Unmarshal(r.body, &doc) != nil {
			return nil, false
		}
		return jsonPath(doc, a.Path)
	case "latency_ms":
		return round2(float64(r.latency.Microseconds()) / 1000), true
	}
	return nil, false
}

func extract(x Extract, r *httpOutcome) (string, bool) {
	switch x.From {
	case "json_path":
		var doc any
		if json.Unmarshal(r.body, &doc) != nil {
			return "", false
		}
		v, ok := jsonPath(doc, x.Path)
		if !ok {
			return "", false
		}
		return scalarString(v), true
	case "header":
		if r.header == nil || r.header.Get(x.Path) == "" {
			return "", false
		}
		return r.header.Get(x.Path), true
	case "regex":
		re, err := regexp.Compile(x.Path)
		if err != nil {
			return "", false
		}
		m := re.FindSubmatch(r.body)
		switch {
		case m == nil:
			return "", false
		case len(m) > 1:
			return string(m[1]), true
		default:
			return string(m[0]), true
		}
	}
	return "", false
}

// jsonPath suit un chemin simple : « $.a.b[0].c », « a.b.0.c » ou « $['a'] ».
func jsonPath(doc any, p string) (any, bool) {
	p = strings.TrimSpace(p)
	p = strings.TrimPrefix(p, "$")
	p = strings.NewReplacer("['", ".", "']", "", "[\"", ".", "\"]", "", "[", ".", "]", "").Replace(p)
	cur := doc
	for _, part := range strings.Split(p, ".") {
		if part == "" {
			continue
		}
		switch v := cur.(type) {
		case map[string]any:
			x, ok := v[part]
			if !ok {
				return nil, false
			}
			cur = x
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil {
				if part == "length" {
					cur = float64(len(v))
					continue
				}
				return nil, false
			}
			if i < 0 {
				i += len(v)
			}
			if i < 0 || i >= len(v) {
				return nil, false
			}
			cur = v[i]
		default:
			return nil, false
		}
	}
	return cur, true
}

func toNumber(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case bool:
		return 0, false
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	}
	return 0, false
}

func scalarString(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return x
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1e15 {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func matches(s, pattern string) (bool, string) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return false, "expression régulière invalide"
	}
	return re.MatchString(s), ""
}

// compare applique op ; numérique si les deux côtés sont des nombres, texte sinon.
func compare(actual any, op string, expected any) (bool, string) {
	a, an := toNumber(actual)
	e, en := toNumber(expected)
	num := an && en
	switch op {
	case "eq":
		if num {
			return a == e, ""
		}
		return scalarString(actual) == scalarString(expected), ""
	case "ne":
		if num {
			return a != e, ""
		}
		return scalarString(actual) != scalarString(expected), ""
	case "lt", "gt":
		if !num {
			return false, "comparaison numérique impossible"
		}
		if op == "lt" {
			return a < e, ""
		}
		return a > e, ""
	case "contains":
		return strings.Contains(scalarString(actual), scalarString(expected)), ""
	case "matches":
		return matches(scalarString(actual), scalarString(expected))
	}
	return false, "opérateur " + op + " inconnu"
}
