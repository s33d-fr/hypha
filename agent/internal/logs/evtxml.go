package logs

import (
	"encoding/xml"
	"strconv"
	"strings"
	"time"

	"s33d.fr/hypha/agent/internal/model"
)

// evtEvent reprend le XML d'un événement Windows (schéma « event » de Windows), tel que
// le rendent EvtRender, EvtFormatMessage (avec RenderingInfo) ou `wevtutil qe /f:RenderedXml`.
type evtEvent struct {
	System struct {
		Provider struct {
			Name string `xml:"Name,attr"`
		} `xml:"Provider"`
		EventID     string `xml:"EventID"`
		Level       string `xml:"Level"`
		Task        string `xml:"Task"`
		Opcode      string `xml:"Opcode"`
		Keywords    string `xml:"Keywords"`
		TimeCreated struct {
			SystemTime string `xml:"SystemTime,attr"`
		} `xml:"TimeCreated"`
		EventRecordID uint64 `xml:"EventRecordID"`
		Execution     struct {
			ProcessID string `xml:"ProcessID,attr"`
		} `xml:"Execution"`
		Channel  string `xml:"Channel"`
		Computer string `xml:"Computer"`
		Security struct {
			UserID string `xml:"UserID,attr"`
		} `xml:"Security"`
	} `xml:"System"`
	EventData struct {
		Data []struct {
			Name  string `xml:"Name,attr"`
			Value string `xml:",chardata"`
		} `xml:"Data"`
	} `xml:"EventData"`
	UserData struct {
		Inner string `xml:",innerxml"`
	} `xml:"UserData"`
	RenderingInfo struct {
		Message string `xml:"Message"`
	} `xml:"RenderingInfo"`
}

// Mot-clé « échec d'audit » du journal Sécurité : niveau 0 mais à traiter comme un avertissement.
const auditFailureKeyword = 0x10000000000000

// evtLevelStatus : 1 critique, 2 erreur, 3 avertissement, 4 information, 5 verbeux ;
// 0 (« toujours journaliser ») vaut information, sauf échec d'audit.
func evtLevelStatus(level, keywords string) string {
	switch level {
	case "1":
		return "critical"
	case "2":
		return "error"
	case "3":
		return "warning"
	case "5":
		return "debug"
	}
	if k, err := strconv.ParseUint(strings.TrimPrefix(keywords, "0x"), 16, 64); err == nil && k&auditFailureKeyword != 0 {
		return "warning"
	}
	return "info"
}

// parseEventXML convertit un événement en ligne de journal ; rend aussi son numéro
// d'enregistrement (le signet).
func parseEventXML(data string, now time.Time) (model.Log, uint64, error) {
	var ev evtEvent
	if err := xml.Unmarshal([]byte(data), &ev); err != nil {
		return model.Log{}, 0, err
	}
	return eventToLog(&ev, now), ev.System.EventRecordID, nil
}

func eventToLog(ev *evtEvent, now time.Time) model.Log {
	sys := &ev.System
	l := model.Log{
		TS:      now.UnixMilli(),
		Service: sys.Provider.Name,
		Source:  "eventlog",
		Status:  evtLevelStatus(sys.Level, sys.Keywords),
	}
	if t, err := time.Parse(time.RFC3339Nano, sys.TimeCreated.SystemTime); err == nil {
		l.TS = t.UnixMilli()
	}
	data := map[string]string{}
	var values []string
	for i, d := range ev.EventData.Data {
		v := strings.TrimSpace(d.Value)
		name := d.Name
		if name == "" {
			name = "param" + strconv.Itoa(i+1)
		}
		data[name] = v
		if v != "" {
			values = append(values, v)
		}
	}
	l.Message = strings.TrimSpace(ev.RenderingInfo.Message)
	if l.Message == "" {
		// Fournisseur sans texte de message installé : on montre au moins les valeurs.
		l.Message = strings.Join(values, " ; ")
		if l.Message == "" {
			l.Message = strings.TrimSpace(ev.UserData.Inner)
		}
		if l.Message == "" {
			l.Message = "Événement " + sys.EventID + " (" + sys.Provider.Name + ")"
		}
	}
	attrs := map[string]any{
		"channel":   sys.Channel,
		"event_id":  sys.EventID,
		"record_id": sys.EventRecordID,
		"provider":  sys.Provider.Name,
		"computer":  sys.Computer,
	}
	for k, v := range map[string]string{"task": sys.Task, "opcode": sys.Opcode, "keywords": sys.Keywords,
		"user_sid": sys.Security.UserID, "pid": sys.Execution.ProcessID} {
		if v != "" {
			attrs[k] = v
		}
	}
	if len(data) > 0 {
		attrs["data"] = data
	}
	l.Attributes = map[string]any{"eventlog": attrs}
	return l
}

// splitEvents découpe la sortie de `wevtutil qe … /e:Events` en événements.
func splitEvents(out string) []string {
	var evs []string
	for {
		i := strings.Index(out, "<Event ")
		if i < 0 {
			i = strings.Index(out, "<Event>")
		}
		if i < 0 {
			return evs
		}
		out = out[i:]
		j := strings.Index(out, "</Event>")
		if j < 0 {
			return evs
		}
		evs = append(evs, out[:j+len("</Event>")])
		out = out[j+len("</Event>"):]
	}
}

// providerName extrait <Provider Name="…"> sans décoder tout le XML.
func providerName(x string) string {
	i := strings.Index(x, "<Provider Name='")
	q := "'"
	if i < 0 {
		i = strings.Index(x, `<Provider Name="`)
		q = `"`
	}
	if i < 0 {
		return ""
	}
	rest := x[i+len(`<Provider Name="`):]
	j := strings.Index(rest, q)
	if j < 0 {
		return ""
	}
	return rest[:j]
}
