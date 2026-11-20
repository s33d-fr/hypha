package logs

import (
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

// Filtres d'un canal des journaux d'événements Windows (logs.windows_eventlog, vague 3).
//
// Les inclusions (niveaux, identifiants) partent aussi dans la requête XPath d'EvtQuery : Windows
// ne rend que les événements utiles, ce qui compte sur un journal Sécurité de contrôleur de
// domaine. Tout est ensuite revérifié ici (repli wevtutil, requête XPath refusée), et les
// exclusions et les sources ne se jugent qu'ici : un événement écarté avance le signet sans partir.

// Mots-clés d'audit du journal Sécurité (winmeta.xml).
const (
	auditSuccessKeyword = 0x20000000000000
	// auditFailureKeyword : evtxml.go
)

// maxXPathIDs : au-delà, les identifiants ne sont filtrés qu'ici (Windows refuse une requête
// XPath trop longue).
const maxXPathIDs = 20

type evtFilter struct {
	channel     string
	levels      map[string]bool
	ids, exIDs  []config.IDRange
	providers   []string // motifs glob en minuscules
	exProviders []string
	service     string
	tags        map[string]string
	xpath       bool // inclusions passées dans la requête XPath
}

func newEvtFilter(c config.EventlogChannel) *evtFilter {
	f := &evtFilter{channel: c.Channel, service: c.Service, tags: c.Tags, xpath: true}
	for _, l := range c.Levels {
		if n := config.NormalizeEventlogLevel(l); n != "" {
			if f.levels == nil {
				f.levels = map[string]bool{}
			}
			f.levels[n] = true
		}
	}
	f.ids, _ = config.ParseEventIDs(c.EventIDs)
	f.exIDs, _ = config.ParseEventIDs(c.ExcludeEventIDs)
	for _, p := range c.Providers {
		f.providers = append(f.providers, strings.ToLower(strings.TrimSpace(p)))
	}
	for _, p := range c.ExcludeProviders {
		f.exProviders = append(f.exProviders, strings.ToLower(strings.TrimSpace(p)))
	}
	return f
}

// query rend la requête XPath des événements de numéro > after.
func (f *evtFilter) query(after uint64) string {
	conds := []string{fmt.Sprintf("EventRecordID>%d", after)}
	if f.xpath {
		if lv := f.xpathLevels(); lv != "" {
			conds = append(conds, lv)
		}
		if len(f.ids) > 0 && len(f.ids) <= maxXPathIDs {
			var parts []string
			for _, r := range f.ids {
				if r.Lo == r.Hi {
					parts = append(parts, fmt.Sprintf("EventID=%d", r.Lo))
				} else {
					parts = append(parts, fmt.Sprintf("(EventID>=%d and EventID<=%d)", r.Lo, r.Hi))
				}
			}
			conds = append(conds, "("+strings.Join(parts, " or ")+")")
		}
	}
	return "*[System[" + strings.Join(conds, " and ") + "]]"
}

// xpathLevels : valeurs de Level qui peuvent porter les niveaux voulus (les audits sont au
// niveau 0, comme les événements « toujours journalisés »).
func (f *evtFilter) xpathLevels() string {
	if len(f.levels) == 0 {
		return ""
	}
	set := map[int]bool{}
	for l := range f.levels {
		switch l {
		case "critical":
			set[1] = true
		case "error":
			set[2] = true
		case "warning":
			set[3] = true
		case "information":
			set[4], set[0] = true, true
		case "verbose":
			set[5] = true
		case "audit_success", "audit_failure":
			set[0] = true
		}
	}
	var nums []int
	for n := range set {
		nums = append(nums, n)
	}
	sort.Ints(nums)
	parts := make([]string, len(nums))
	for i, n := range nums {
		parts[i] = "Level=" + strconv.Itoa(n)
	}
	return "(" + strings.Join(parts, " or ") + ")"
}

// evtLevelNames : niveaux d'un événement au sens de levels (un audit au niveau 0 n'est pas une
// information).
func evtLevelNames(level, keywords string) []string {
	k, _ := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(keywords), "0x"), 16, 64)
	var out []string
	switch level {
	case "1":
		out = append(out, "critical")
	case "2":
		out = append(out, "error")
	case "3":
		out = append(out, "warning")
	case "5":
		out = append(out, "verbose")
	case "4":
		out = append(out, "information")
	}
	if k&auditSuccessKeyword != 0 {
		out = append(out, "audit_success")
	}
	if k&auditFailureKeyword != 0 {
		out = append(out, "audit_failure")
	}
	if len(out) == 0 {
		out = append(out, "information")
	}
	return out
}

func inRanges(id uint32, rs []config.IDRange) bool {
	for _, r := range rs {
		if id >= r.Lo && id <= r.Hi {
			return true
		}
	}
	return false
}

func matchAny(name string, globs []string) bool {
	name = strings.ToLower(name)
	for _, g := range globs {
		if ok, _ := path.Match(g, name); ok {
			return true
		}
	}
	return false
}

// match dit si l'événement passe les filtres du canal.
func (f *evtFilter) match(ev *evtEvent) bool {
	sys := &ev.System
	if len(f.levels) > 0 {
		ok := false
		for _, l := range evtLevelNames(sys.Level, sys.Keywords) {
			ok = ok || f.levels[l]
		}
		if !ok {
			return false
		}
	}
	id64, _ := strconv.ParseUint(strings.TrimSpace(sys.EventID), 10, 32)
	id := uint32(id64)
	if len(f.ids) > 0 && !inRanges(id, f.ids) {
		return false
	}
	if inRanges(id, f.exIDs) {
		return false
	}
	if len(f.providers) > 0 && !matchAny(sys.Provider.Name, f.providers) {
		return false
	}
	return !matchAny(sys.Provider.Name, f.exProviders)
}

// apply pose le service et les étiquettes propres au canal.
func (f *evtFilter) apply(l *model.Log) {
	if f.service != "" {
		l.Service = f.service
	}
	if len(f.tags) > 0 {
		if l.Tags == nil {
			l.Tags = map[string]string{}
		}
		for k, v := range f.tags {
			l.Tags[k] = v
		}
	}
}

// addChannels : canaux ajoutés d'office pour les rôles de Windows Server, sauf s'ils sont déjà listés
// (l'entrée de l'utilisateur, avec ses filtres, l'emporte).
func addChannels(list []config.EventlogChannel, extra ...string) []config.EventlogChannel {
	out := append([]config.EventlogChannel{}, list...)
	for _, name := range extra {
		dup := false
		for _, c := range out {
			dup = dup || strings.EqualFold(c.Channel, name)
		}
		if !dup {
			out = append(out, config.EventlogChannel{Channel: name})
		}
	}
	return out
}
