//go:build windows

package integrations

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"s33d.fr/hypha/agent/internal/config"
	"s33d.fr/hypha/agent/internal/model"
)

func platformIntegrations(cfg config.IntegrationsConfig) []integration {
	return append([]integration{newIIS(cfg.IIS), newMSSQL(cfg.MSSQL), newExchange(cfg.Exchange), newHyperv(cfg.HyperV)},
		windowsProbes(cfg)...) // vague 3 : winprobes_windows.go
}

// ── compteurs de performance (PDH), noms anglais quelle que soit la langue de Windows ────

var (
	pdh                             = windows.NewLazySystemDLL("pdh.dll")
	procPdhOpenQuery                = pdh.NewProc("PdhOpenQueryW")
	procPdhAddEnglishCounter        = pdh.NewProc("PdhAddEnglishCounterW")
	procPdhCollectQueryData         = pdh.NewProc("PdhCollectQueryData")
	procPdhGetFormattedCounterValue = pdh.NewProc("PdhGetFormattedCounterValue")
	procPdhCloseQuery               = pdh.NewProc("PdhCloseQuery")
)

const (
	pdhFmtDouble   = 0x00000200
	pdhFmtNoCap100 = 0x00008000
)

// pdhValue reprend PDH_FMT_COUNTERVALUE (statut, puis union de 8 octets alignée).
type pdhValue struct {
	CStatus uint32
	_       uint32
	Double  float64
}

type pdhQuery struct {
	h        uintptr
	counters map[string]uintptr
	primed   bool
}

// openPDH ouvre une requête sur des compteurs ; un compteur absent est une erreur (logiciel
// non installé ou instance inconnue).
func openPDH(paths []string) (*pdhQuery, error) {
	if err := procPdhOpenQuery.Find(); err != nil {
		return nil, err
	}
	q := &pdhQuery{counters: map[string]uintptr{}}
	if r, _, _ := procPdhOpenQuery.Call(0, 0, uintptr(unsafe.Pointer(&q.h))); r != 0 {
		return nil, fmt.Errorf("PdhOpenQuery : 0x%x", r)
	}
	for _, p := range paths {
		ptr, _ := windows.UTF16PtrFromString(p)
		var c uintptr
		if r, _, _ := procPdhAddEnglishCounter.Call(q.h, uintptr(unsafe.Pointer(ptr)), 0, uintptr(unsafe.Pointer(&c))); r != 0 {
			q.close()
			return nil, fmt.Errorf("compteur %s absent (0x%x)", p, r)
		}
		q.counters[p] = c
	}
	return q, nil
}

// read relève les compteurs ; un compteur de débit (« /sec ») demande deux relevés : le
// premier appel ne rend que les valeurs instantanées.
func (q *pdhQuery) read() (map[string]float64, error) {
	if r, _, _ := procPdhCollectQueryData.Call(q.h); r != 0 {
		return nil, fmt.Errorf("PdhCollectQueryData : 0x%x", r)
	}
	out := map[string]float64{}
	for p, c := range q.counters {
		var v pdhValue
		if r, _, _ := procPdhGetFormattedCounterValue.Call(c, pdhFmtDouble|pdhFmtNoCap100, 0, uintptr(unsafe.Pointer(&v))); r == 0 && v.CStatus <= 1 {
			out[p] = v.Double
		}
	}
	first := !q.primed
	q.primed = true
	if first && len(out) == 0 {
		return out, errNoData
	}
	return out, nil
}

func (q *pdhQuery) close() {
	if q != nil && q.h != 0 {
		procPdhCloseQuery.Call(q.h)
		q.h = 0
	}
}

// ── IIS ──────────────────────────────────────────────────────────────────────────────────

const (
	iisRequests    = `\Web Service(_Total)\Total Method Requests/sec`
	iisConnections = `\Web Service(_Total)\Current Connections`
)

type IIS struct {
	cfg config.IntegrationToggle
	det detector
	q   *pdhQuery
}

func newIIS(cfg config.IntegrationToggle) *IIS { return &IIS{cfg: cfg, det: detector{mode: cfg.Enabled}} }

func (i *IIS) name() string          { return "iis" }
func (i *IIS) close()                { i.q.close(); i.q = nil }
func (i *IIS) period() time.Duration { return 15 * time.Second }

func (i *IIS) collect(_ context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := i.det.skip(now); skip {
		return nil, st
	}
	ts := model.Ms(now)
	var err error
	if i.q == nil {
		i.q, err = openPDH([]string{iisRequests, iisConnections})
	}
	var v map[string]float64
	if err == nil {
		v, err = i.q.read()
	}
	if err != nil && !errors.Is(err, errNoData) {
		i.close()
		if silent, st := i.det.failed(err.Error()); silent {
			return nil, st
		} else {
			return []model.Check{canConnect("iis", ts, false, err.Error(), tags())}, st
		}
	}
	i.det.ok()
	if x, ok := v[iisRequests]; ok {
		b.Gauge("iis.requests_per_s", "operation_per_second", x)
	}
	if x, ok := v[iisConnections]; ok {
		b.Gauge("iis.current_connections", "count", x)
	}
	return []model.Check{canConnect("iis", ts, true, "compteurs Web Service lus", tags())}, "ok"
}

// ── SQL Server ───────────────────────────────────────────────────────────────────────────

type MSSQL struct {
	cfg    config.IntegrationToggle
	det    detector
	q      map[string]*pdhQuery // instance → requête
	instAt time.Time
	insts  []string
}

func newMSSQL(cfg config.IntegrationToggle) *MSSQL {
	return &MSSQL{cfg: cfg, det: detector{mode: cfg.Enabled}, q: map[string]*pdhQuery{}}
}

func (m *MSSQL) name() string          { return "mssql" }
func (m *MSSQL) period() time.Duration { return 15 * time.Second }
func (m *MSSQL) close() {
	for k, q := range m.q {
		q.close()
		delete(m.q, k)
	}
}

// sqlInstances lit les instances installées (registre « Instance Names\SQL »).
func sqlInstances() []string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Microsoft SQL Server\Instance Names\SQL`, registry.QUERY_VALUE)
	if err != nil {
		return nil
	}
	defer k.Close()
	names, _ := k.ReadValueNames(-1)
	sort.Strings(names)
	return names
}

// mssqlObject : objet de compteurs d'une instance (SQLServer pour l'instance par défaut).
func mssqlObject(inst string) string {
	if strings.EqualFold(inst, "MSSQLSERVER") {
		return "SQLServer"
	}
	return "MSSQL$" + inst
}

func mssqlPaths(inst string) [3]string {
	o := mssqlObject(inst)
	return [3]string{`\` + o + `:Buffer Manager\Buffer cache hit ratio`, `\` + o + `:SQL Statistics\Batch Requests/sec`, `\` + o + `:General Statistics\User Connections`}
}

func (m *MSSQL) collect(_ context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := m.det.skip(now); skip {
		return nil, st
	}
	if m.insts == nil || now.Sub(m.instAt) > 10*time.Minute {
		m.insts, m.instAt = sqlInstances(), now
	}
	if len(m.insts) == 0 {
		_, st := m.det.failed("aucune instance SQL Server")
		return nil, st
	}
	m.det.ok()
	ts := model.Ms(now)
	var checks []model.Check
	state := "ok"
	for _, inst := range m.insts {
		p := mssqlPaths(inst)
		q := m.q[inst]
		var err error
		if q == nil {
			if q, err = openPDH(p[:]); err == nil {
				m.q[inst] = q
			}
		}
		var v map[string]float64
		if err == nil {
			v, err = q.read()
		}
		if err != nil && !errors.Is(err, errNoData) {
			q.close()
			delete(m.q, inst)
			state = "partiel : " + inst + " : " + err.Error()
			checks = append(checks, canConnect("mssql", ts, false, "compteurs de l'instance illisibles (service arrêté ?) : "+err.Error(), tags("instance", inst)))
			continue
		}
		if x, ok := v[p[0]]; ok {
			b.Gauge("mssql.buffer_cache_hit_ratio", "percent", x, "instance", inst)
		}
		if x, ok := v[p[1]]; ok {
			b.Gauge("mssql.batch_requests_per_s", "operation_per_second", x, "instance", inst)
		}
		if x, ok := v[p[2]]; ok {
			b.Gauge("mssql.user_connections", "count", x, "instance", inst)
		}
		checks = append(checks, canConnect("mssql", ts, true, "compteurs de performance lus", tags("instance", inst)))
	}
	return checks, state
}

// ── compteurs à instances (« (*) ») et ouverture tolérante ────────────────────────────────

var procPdhGetFormattedCounterArray = pdh.NewProc("PdhGetFormattedCounterArrayW")

const pdhMoreData = 0x800007D2

// pdhItem reprend PDH_FMT_COUNTERVALUE_ITEM_W : nom de l'instance, puis la valeur.
type pdhItem struct {
	Name  *uint16
	Value pdhValue
}

// openPDHAny : comme openPDH, mais un compteur absent est ignoré (versions, rôles installés) ;
// erreur si aucun n'existe.
func openPDHAny(paths []string) (*pdhQuery, error) {
	if err := procPdhOpenQuery.Find(); err != nil {
		return nil, err
	}
	q := &pdhQuery{counters: map[string]uintptr{}}
	if r, _, _ := procPdhOpenQuery.Call(0, 0, uintptr(unsafe.Pointer(&q.h))); r != 0 {
		return nil, fmt.Errorf("PdhOpenQuery : 0x%x", r)
	}
	for _, p := range paths {
		ptr, _ := windows.UTF16PtrFromString(p)
		var c uintptr
		if r, _, _ := procPdhAddEnglishCounter.Call(q.h, uintptr(unsafe.Pointer(ptr)), 0, uintptr(unsafe.Pointer(&c))); r == 0 {
			q.counters[p] = c
		}
	}
	if len(q.counters) == 0 {
		q.close()
		return nil, errors.New("aucun des compteurs n'existe")
	}
	return q, nil
}

// instances rend instance → valeur d'un compteur « (*) », après read() (qui relève la requête).
func (q *pdhQuery) instances(path string) map[string]float64 {
	c, ok := q.counters[path]
	if !ok {
		return nil
	}
	var size, count uint32
	const fmtFlags = pdhFmtDouble | pdhFmtNoCap100
	r, _, _ := procPdhGetFormattedCounterArray.Call(c, fmtFlags, uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&count)), 0)
	if uint32(r) != pdhMoreData || size == 0 {
		return nil
	}
	buf := make([]byte, size)
	if r, _, _ = procPdhGetFormattedCounterArray.Call(c, fmtFlags, uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&count)),
		uintptr(unsafe.Pointer(&buf[0]))); r != 0 || count == 0 {
		return nil
	}
	items := unsafe.Slice((*pdhItem)(unsafe.Pointer(&buf[0])), count)
	out := make(map[string]float64, count)
	for _, it := range items {
		if it.Value.CStatus <= 1 && it.Name != nil {
			out[windows.UTF16PtrToString(it.Name)] = it.Value.Double
		}
	}
	return out
}

// ── Exchange (§ 32) : messages en transit, bases montées, connexions IMAP ─────────────────

const (
	exDepot   = `\MSExchangeTransport MessageDepot(*)\`
	exMounted = `\MSExchange Active Manager(*)\Database Mounted`
	exImap    = `\MSExchangeImap4(_total)\Current Connections`
)

// États des messages du dépôt de transport (MessageDepot, ce que rend Get-Queue côté messages).
var exDepotStates = []struct{ counter, metric string }{
	{"Ready Messages", "ready"}, {"Deferred Messages", "deferred"}, {"Suspended Messages", "suspended"},
	{"Poison Messages", "poison"}, {"Retry Messages", "retry"}, {"Processing Messages", "processing"},
	{"Expiring Messages", "expiring"},
}

type Exchange struct {
	cfg config.IntegrationToggle
	det detector
	q   *pdhQuery
}

func newExchange(cfg config.IntegrationToggle) *Exchange {
	return &Exchange{cfg: cfg, det: detector{mode: cfg.Enabled}}
}

func (e *Exchange) name() string          { return "exchange" }
func (e *Exchange) close()                { e.q.close(); e.q = nil }
func (e *Exchange) period() time.Duration { return 30 * time.Second }

func sortedInstances(m map[string]float64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		if !strings.EqualFold(k, "_total") {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func (e *Exchange) collect(_ context.Context, now time.Time, b *model.SeriesBuilder, tags model.TagFunc) ([]model.Check, string) {
	if skip, st := e.det.skip(now); skip {
		return nil, st
	}
	ts := model.Ms(now)
	var err error
	if e.q == nil {
		paths := []string{exMounted, exImap}
		for _, s := range exDepotStates {
			paths = append(paths, exDepot+s.counter)
		}
		if e.q, err = openPDHAny(paths); err != nil {
			e.q = nil
			_, st := e.det.failed("compteurs Exchange absents : " + err.Error())
			return nil, st
		}
	}
	scalars, err := e.q.read()
	if err != nil && !errors.Is(err, errNoData) {
		e.close()
		if silent, st := e.det.failed(err.Error()); silent {
			return nil, st
		} else {
			return []model.Check{canConnect("exchange", ts, false, "compteurs Exchange illisibles : "+err.Error(), tags())}, st
		}
	}
	e.det.ok()
	var checks []model.Check
	mounted := e.q.instances(exMounted)
	for _, db := range sortedInstances(mounted) {
		v := mounted[db]
		b.Gauge("exchange.database.mounted", "count", v, "database", db)
		st, msg := model.CheckOK, "base montée"
		if v < 1 {
			st, msg = model.CheckCrit, "base démontée : les boîtes qu'elle porte ne reçoivent plus rien"
		}
		checks = append(checks, model.Check{TS: ts, Name: "exchange.database.mounted", Status: st, Message: msg, Tags: tags("database", db)})
	}
	totals := map[string]float64{}
	for _, s := range exDepotStates {
		vals := e.q.instances(exDepot + s.counter)
		for _, inst := range sortedInstances(vals) {
			b.Gauge("exchange.queue."+s.metric, "count", vals[inst], "depot", inst)
			totals[s.metric] += vals[inst]
		}
	}
	for _, c := range []struct{ metric, what string }{{"poison", "en quarantaine (poison)"}, {"suspended", "suspendu(s)"}} {
		n := totals[c.metric]
		st := model.CheckOK
		if n > 0 {
			st = model.CheckWarn
		}
		checks = append(checks, model.Check{TS: ts, Name: "exchange.queue." + c.metric, Status: st,
			Message: fmt.Sprintf("%g message(s) %s", n, c.what), Tags: tags()})
	}
	if v, ok := scalars[exImap]; ok {
		b.Gauge("exchange.imap.connections", "count", v)
	}
	checks = append(checks, canConnect("exchange", ts, true, "compteurs Exchange lus", tags()))
	return checks, "ok"
}
