// Package syslog reçoit les journaux d'équipements et de serveurs par syslog (UDP et TCP,
// RFC 3164 et RFC 5424, trames TCP en octet-counting ou terminées par une fin de ligne) et les
// verse dans la file des journaux de l'agent. Coupé par défaut ; l'agent ne touche à aucun
// pare-feu.
package syslog

import (
	"strconv"
	"strings"
	"time"
)

// Message : une ligne syslog décodée.
type Message struct {
	Facility  int
	Severity  int
	HasPRI    bool
	Version   int // 1 pour RFC 5424, 0 sinon
	Timestamp string
	Hostname  string
	AppName   string
	ProcID    string
	MsgID     string
	SD        map[string]map[string]string
	Text      string
}

var facilityNames = []string{"kern", "user", "mail", "daemon", "auth", "syslog", "lpr", "news", "uucp", "cron",
	"authpriv", "ftp", "ntp", "security", "console", "solaris-cron", "local0", "local1", "local2", "local3", "local4",
	"local5", "local6", "local7"}

// FacilityName : nom de la facilité (0 à 23).
func FacilityName(f int) string {
	if f >= 0 && f < len(facilityNames) {
		return facilityNames[f]
	}
	return strconv.Itoa(f)
}

var months = map[string]bool{"Jan": true, "Feb": true, "Mar": true, "Apr": true, "May": true, "Jun": true, "Jul": true,
	"Aug": true, "Sep": true, "Oct": true, "Nov": true, "Dec": true}

// Parse décode une ligne ; tolérant : une partie illisible reste dans le texte.
func Parse(raw string) Message {
	m := Message{Facility: 1, Severity: 5} // sans PRI : user.notice (RFC 3164 § 4.3.3)
	s := strings.TrimRight(raw, "\r\n\x00")
	if len(s) > 2 && s[0] == '<' {
		if end := strings.IndexByte(s[:min(len(s), 6)], '>'); end > 1 {
			if pri, err := strconv.Atoi(s[1:end]); err == nil && pri >= 0 && pri <= 191 {
				m.Facility, m.Severity, m.HasPRI = pri/8, pri%8, true
				s = s[end+1:]
			}
		}
	}
	if len(s) > 2 && s[0] == '1' && s[1] == ' ' {
		if parse5424(&m, s[2:]) {
			return m
		}
	}
	parse3164(&m, s)
	return m
}

func nilValue(s string) string {
	if s == "-" {
		return ""
	}
	return s
}

// parse5424 : TIMESTAMP SP HOSTNAME SP APP-NAME SP PROCID SP MSGID SP STRUCTURED-DATA [SP MSG].
func parse5424(m *Message, s string) bool {
	f := strings.SplitN(s, " ", 6)
	if len(f) < 6 {
		return false
	}
	m.Version = 1
	m.Timestamp, m.Hostname, m.AppName, m.ProcID, m.MsgID = nilValue(f[0]), nilValue(f[1]), nilValue(f[2]), nilValue(f[3]), nilValue(f[4])
	rest := f[5]
	if strings.HasPrefix(rest, "-") {
		rest = strings.TrimPrefix(rest, "-")
	} else if strings.HasPrefix(rest, "[") {
		sd, n := parseSD(rest)
		m.SD = sd
		rest = rest[n:]
	}
	rest = strings.TrimPrefix(rest, " ")
	// rsyslog garde souvent l'espace de t\u00EAte du message d'origine : retir\u00E9.
	m.Text = strings.TrimLeft(strings.TrimPrefix(rest, "\uFEFF"), " ")
	return true
}

// parseSD lit [id clé="valeur" …][…] ; rend les éléments et le nombre d'octets lus.
func parseSD(s string) (map[string]map[string]string, int) {
	out := map[string]map[string]string{}
	i := 0
	for i < len(s) && s[i] == '[' {
		j := i + 1
		for j < len(s) && s[j] != ' ' && s[j] != ']' {
			j++
		}
		id := s[i+1 : j]
		params := map[string]string{}
		for j < len(s) && s[j] == ' ' {
			j++
			k := j
			for k < len(s) && s[k] != '=' && s[k] != ']' {
				k++
			}
			if k+1 >= len(s) || s[k] != '=' || s[k+1] != '"' {
				return out, len(s) // élément cassé : le reste n'est pas lisible
			}
			name := s[j:k]
			k += 2
			var v strings.Builder
			for k < len(s) && s[k] != '"' {
				if s[k] == '\\' && k+1 < len(s) && (s[k+1] == '"' || s[k+1] == '\\' || s[k+1] == ']') {
					k++
				}
				v.WriteByte(s[k])
				k++
			}
			if k >= len(s) {
				return out, len(s)
			}
			params[name] = v.String()
			j = k + 1
		}
		if j >= len(s) || s[j] != ']' {
			return out, len(s)
		}
		if id != "" && len(out) < 20 {
			out[id] = params
		}
		i = j + 1
	}
	return out, i
}

// parse3164 : « Mmm dd hh:mm:ss HOSTNAME TAG[PID]: MSG », ou horodatage ISO 8601 (rsyslog) ;
// beaucoup d'équipements s'en écartent : ce qui n'est pas reconnu reste dans le texte.
func parse3164(m *Message, s string) {
	rest := s
	switch {
	case len(s) >= 15 && months[s[:3]] && s[3] == ' ' && s[9] == ':' && s[12] == ':':
		m.Timestamp, rest = s[:15], strings.TrimPrefix(s[15:], " ")
		// Fraction de seconde éventuelle (« Nov 20 03:00:00.123 »).
		if len(rest) > 0 && rest[0] == '.' {
			k := 1
			for k < len(rest) && rest[k] >= '0' && rest[k] <= '9' {
				k++
			}
			m.Timestamp += rest[:k]
			rest = strings.TrimPrefix(rest[k:], " ")
		}
	case len(s) >= 19 && s[4] == '-' && s[7] == '-' && s[10] == 'T':
		end := strings.IndexByte(s, ' ')
		if end < 0 {
			m.Text = s
			return
		}
		if _, err := time.Parse(time.RFC3339Nano, s[:end]); err != nil {
			m.Text = s
			return
		}
		m.Timestamp, rest = s[:end], s[end+1:]
	default:
		m.Text = s
		return
	}
	// Nom d'hôte : premier mot, s'il n'est pas déjà l'étiquette (« sshd[12]: », « %ASA-6-302013: »).
	word, after, _ := strings.Cut(rest, " ")
	if word != "" && !strings.HasSuffix(word, ":") && !strings.ContainsAny(word, "[]") && !strings.HasPrefix(word, "%") {
		m.Hostname, rest = word, after
	}
	// Étiquette : jusqu'à « : » ou « [ », 48 caractères au plus, lettres, chiffres, . _ - /.
	k := 0
	for k < len(rest) && k < 48 {
		c := rest[k]
		if !(c == '.' || c == '_' || c == '-' || c == '/' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
			break
		}
		k++
	}
	if k > 0 && k < len(rest) && (rest[k] == ':' || rest[k] == '[') {
		m.AppName = rest[:k]
		r := rest[k:]
		if r[0] == '[' {
			if e := strings.IndexByte(r, ']'); e > 0 {
				m.ProcID = r[1:e]
				r = r[e+1:]
			}
		}
		if strings.HasPrefix(r, ":") {
			m.Text = strings.TrimLeft(r[1:], " ")
			return
		}
	}
	m.Text = rest
}
