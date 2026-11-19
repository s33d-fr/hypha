package logs

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/model"
)

// Statuts syslog admis par le contrat, par gravité décroissante.
var syslogNames = []string{"emergency", "alert", "critical", "error", "warning", "notice", "info", "debug"}

// SyslogStatus rend le statut d'une priorité syslog (0 à 7).
func SyslogStatus(prio int) string {
	if prio < 0 || prio >= len(syslogNames) {
		return "info"
	}
	return syslogNames[prio]
}

// NormalizeLevel ramène un niveau écrit par une application (texte ou nombre) à un statut
// du contrat ; "" si le niveau n'est pas reconnu.
func NormalizeLevel(v any) string {
	switch t := v.(type) {
	case json.Number:
		if n, err := t.Int64(); err == nil {
			return numericLevel(n)
		}
	case float64:
		return numericLevel(int64(t))
	case string:
		s := strings.ToLower(strings.TrimSpace(t))
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return numericLevel(n)
		}
		switch s {
		case "emerg", "emergency", "panic":
			return "emergency"
		case "alert":
			return "alert"
		case "crit", "critical", "fatal", "dpanic":
			return "critical"
		case "err", "error", "eror", "severe":
			return "error"
		case "warn", "warning":
			return "warning"
		case "notice":
			return "notice"
		case "info", "information", "informational":
			return "info"
		case "debug", "trace", "verbose", "fine", "finer", "finest":
			return "debug"
		}
	}
	return ""
}

// numericLevel : 0 à 7 = syslog ; 10 à 60 = pino/bunyan (10 trace … 60 fatal).
func numericLevel(n int64) string {
	switch {
	case n >= 0 && n <= 7:
		return SyslogStatus(int(n))
	case n >= 60:
		return "critical"
	case n >= 50:
		return "error"
	case n >= 40:
		return "warning"
	case n >= 30:
		return "info"
	case n >= 10:
		return "debug"
	}
	return ""
}

// ApplyLine range une ligne de texte dans l'entrée. Une ligne JSON est décodée (contrat
// § 3.3) : message/msg devient le message, level/severity le statut, le reste va dans les
// attributs. Une ligne de texte garde son texte ; son niveau est deviné s'il est écrit
// de façon reconnaissable (ERROR, [warn], level=error…).
func ApplyLine(l *model.Log, line string, now time.Time) {
	t := strings.TrimSpace(line)
	if len(t) > 1 && t[0] == '{' && t[len(t)-1] == '}' {
		dec := json.NewDecoder(strings.NewReader(t))
		dec.UseNumber()
		var obj map[string]any
		if err := dec.Decode(&obj); err == nil {
			applyJSON(l, obj, t, now)
			return
		}
	}
	l.Message = line
	if l.Status == "" {
		l.Status = GuessLevel(line)
	}
}

func applyJSON(l *model.Log, obj map[string]any, raw string, now time.Time) {
	take := func(keys ...string) (any, bool) {
		for _, k := range keys {
			if v, ok := obj[k]; ok {
				delete(obj, k)
				return v, true
			}
		}
		return nil, false
	}
	if v, ok := take("message", "msg"); ok {
		if s, isStr := v.(string); isStr {
			l.Message = s
		} else {
			b, _ := json.Marshal(v)
			l.Message = string(b)
		}
	} else {
		l.Message = raw
	}
	if v, ok := take("level", "severity", "lvl", "loglevel", "log.level"); ok {
		if st := NormalizeLevel(v); st != "" {
			l.Status = st
		}
	}
	if v, ok := take("trace_id", "traceId", "dd.trace_id"); ok {
		if s := scalarString(v); s != "" {
			l.TraceID = &s
		}
	}
	if v, ok := take("span_id", "spanId", "dd.span_id"); ok {
		if s := scalarString(v); s != "" {
			l.SpanID = &s
		}
	}
	// L'horodatage écrit par l'application est plus juste que l'heure de lecture (rattrapage
	// après un arrêt de l'agent) ; il n'est retenu que s'il tombe dans la fenêtre du serveur.
	for _, k := range []string{"timestamp", "time", "@timestamp", "ts"} {
		if v, ok := obj[k]; ok {
			if ms, ok := parseTimestamp(v); ok && ms > now.Add(-14*24*time.Hour).UnixMilli() && ms < now.Add(10*time.Minute).UnixMilli() {
				l.TS = ms
				delete(obj, k)
			}
			break
		}
	}
	if l.Attributes == nil {
		l.Attributes = map[string]any{}
	}
	for k, v := range obj {
		if _, exists := l.Attributes[k]; !exists {
			l.Attributes[k] = v
		}
	}
}

func scalarString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	}
	return ""
}

// parseTimestamp accepte RFC 3339 et les nombres d'époque Unix (secondes, ms, µs, ns).
func parseTimestamp(v any) (int64, bool) {
	switch t := v.(type) {
	case string:
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02T15:04:05.999999999"} {
			if ts, err := time.Parse(layout, t); err == nil {
				return ts.UnixMilli(), true
			}
		}
	case json.Number:
		f, err := t.Float64()
		if err != nil {
			return 0, false
		}
		switch {
		case f > 1e17:
			return int64(f / 1e6), true
		case f > 1e14:
			return int64(f / 1e3), true
		case f > 1e11:
			return int64(f), true
		case f > 1e8:
			return int64(f * 1000), true
		}
	}
	return 0, false
}

// Jetons de niveau reconnus dans une ligne de texte : en capitales (ERROR, WARN — Java,
// Python), ou en minuscules entre crochets ([error] — nginx, Apache).
var levelTokens = map[string]string{
	"EMERG": "emergency", "EMERGENCY": "emergency", "PANIC": "emergency",
	"ALERT": "alert",
	"CRIT":  "critical", "CRITICAL": "critical", "FATAL": "critical",
	"ERROR": "error", "ERR": "error",
	"WARN": "warning", "WARNING": "warning",
	"NOTICE": "notice",
	"INFO":   "info",
	"DEBUG":  "debug", "TRACE": "debug",
}

// GuessLevel devine le niveau d'une ligne de texte dans ses 200 premiers octets ;
// "info" par défaut. Seuls les jetons non ambigus sont retenus : « error » en minuscules
// au milieu d'une phrase ne fait pas une erreur.
func GuessLevel(line string) string {
	head := line
	if len(head) > 200 {
		head = head[:200]
	}
	for i := 0; i < len(head); {
		// Début d'un mot
		for i < len(head) && !isLetter(head[i]) {
			i++
		}
		j := i
		for j < len(head) && isLetter(head[j]) {
			j++
		}
		if j > i {
			w := head[i:j]
			if st, ok := levelTokens[w]; ok {
				return st
			}
			bracketed := i > 0 && j < len(head) && (head[i-1] == '[' && head[j] == ']' || head[i-1] == '<' && head[j] == '>')
			keyed := i >= 6 && strings.HasSuffix(head[:i], "level=")
			if bracketed || keyed {
				if st, ok := levelTokens[strings.ToUpper(w)]; ok {
					return st
				}
			}
		}
		i = j
	}
	return "info"
}

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
