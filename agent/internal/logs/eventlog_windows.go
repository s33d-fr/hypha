//go:build windows

package logs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"s33d.fr/hypha/agent/internal/state"
)

const (
	eventlogStateName = "eventlog_bookmarks.json"
	eventlogPoll      = 5 * time.Second
	eventlogMaxCycle  = 2000 // événements lus au plus par canal et par cycle
)

var (
	modwevtapi                   = windows.NewLazySystemDLL("wevtapi.dll")
	procEvtQuery                 = modwevtapi.NewProc("EvtQuery")
	procEvtNext                  = modwevtapi.NewProc("EvtNext")
	procEvtRender                = modwevtapi.NewProc("EvtRender")
	procEvtClose                 = modwevtapi.NewProc("EvtClose")
	procEvtOpenPublisherMetadata = modwevtapi.NewProc("EvtOpenPublisherMetadata")
	procEvtFormatMessage         = modwevtapi.NewProc("EvtFormatMessage")
)

// Constantes de winevt.h.
const (
	evtQueryChannelPath      = 0x1
	evtQueryForwardDirection = 0x100
	evtQueryReverseDirection = 0x200
	evtRenderEventXML        = 1
	evtFormatMessageXML      = 9
	errNoMoreItems           = syscall.Errno(259)
	errInsufficientBuffer    = syscall.Errno(122)
)

// Eventlog lit les journaux d'événements Windows par l'API wevtapi.dll (EvtQuery, EvtNext,
// EvtRender), canal par canal, en reprenant après le dernier numéro d'enregistrement
// (EventRecordID) mis en sécurité. Si l'API manque, repli sur `wevtutil qe`.
type Eventlog struct {
	channels []string
	pipe     *Pipeline
	store    *state.Store
	saver    *state.Saver
	log      *slog.Logger
	setStat  func(string)

	mu        sync.Mutex
	durable   map[string]uint64 // canal → dernier EventRecordID en sécurité
	next      map[string]uint64 // canal → dernier EventRecordID lu
	publisher map[string]windows.Handle
	useAPI    bool
}

// NewEventlog prépare la lecture des canaux.
func NewEventlog(channels []string, pipe *Pipeline, store *state.Store, log *slog.Logger, setStat func(string)) *Eventlog {
	e := &Eventlog{channels: channels, pipe: pipe, store: store, log: log, setStat: setStat,
		durable: map[string]uint64{}, next: map[string]uint64{}, publisher: map[string]windows.Handle{},
		useAPI: procEvtQuery.Find() == nil && procEvtNext.Find() == nil && procEvtRender.Find() == nil}
	e.saver = state.NewSaver(store, eventlogStateName, func() any {
		e.mu.Lock()
		defer e.mu.Unlock()
		out := make(map[string]uint64, len(e.durable))
		for k, v := range e.durable {
			out[k] = v
		}
		return out
	}, func(err error) { log.Warn("signets des journaux d'événements non écrits", "err", err) })
	return e
}

// EventlogAvailable : vrai sous Windows.
func EventlogAvailable() bool { return true }

// Run interroge chaque canal toutes les 5 s jusqu'à l'arrêt.
func (e *Eventlog) Run(ctx context.Context) {
	if err := e.store.ReadJSON(eventlogStateName, &e.durable); err != nil && !errors.Is(err, os.ErrNotExist) {
		e.log.Warn("signets illisibles : reprise aux derniers événements", "err", err)
		e.durable = map[string]uint64{}
	}
	for ch, id := range e.durable {
		e.next[ch] = id
	}
	go e.saver.Run(ctx)
	if !e.useAPI {
		e.log.Warn("wevtapi.dll indisponible : lecture par wevtutil")
	}
	t := time.NewTicker(eventlogPoll)
	defer t.Stop()
	lastCheck := map[string]time.Time{}
	for {
		var errs []string
		for _, ch := range e.channels {
			if ctx.Err() != nil {
				return
			}
			e.mu.Lock()
			last, known := e.next[ch]
			e.mu.Unlock()
			if !known {
				// Premier démarrage pour ce canal : on part du dernier événement existant,
				// sans renvoyer l'historique.
				newest, err := e.newest(ch)
				if err != nil {
					errs = append(errs, ch+" : "+err.Error())
					continue
				}
				e.mu.Lock()
				e.next[ch], e.durable[ch] = newest, newest
				e.mu.Unlock()
				e.saver.MarkDirty()
				continue
			}
			n, err := e.readAfter(ctx, ch, last)
			if err != nil {
				errs = append(errs, ch+" : "+err.Error())
				continue
			}
			// Journal vidé (wevtutil cl, effacement) : les numéros repartent de 1. On le
			// détecte une fois par minute, quand rien de neuf n'arrive.
			if n == 0 && time.Since(lastCheck[ch]) > time.Minute {
				lastCheck[ch] = time.Now()
				if newest, err := e.newest(ch); err == nil && newest < last {
					e.log.Info("journal d'événements vidé, reprise au début", "canal", ch)
					e.mu.Lock()
					e.next[ch], e.durable[ch] = 0, 0
					e.mu.Unlock()
					e.saver.MarkDirty()
				}
			}
		}
		if len(errs) > 0 {
			e.setStat("erreur : " + strings.Join(errs, " ; "))
		} else {
			e.setStat("ok")
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Once envoie les 20 derniers événements de chaque canal (aperçu de --once).
func (e *Eventlog) Once(ctx context.Context) error {
	var errs []error
	for _, ch := range e.channels {
		newest, err := e.newest(ch)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s : %w", ch, err))
			continue
		}
		after := uint64(0)
		if newest > 20 {
			after = newest - 20
		}
		if _, err := e.readAfter(ctx, ch, after); err != nil {
			errs = append(errs, fmt.Errorf("%s : %w", ch, err))
		}
	}
	return errors.Join(errs...)
}

// readAfter envoie les événements de numéro > after ; rend leur nombre.
func (e *Eventlog) readAfter(ctx context.Context, ch string, after uint64) (int, error) {
	query := fmt.Sprintf("*[System[EventRecordID>%d]]", after)
	var xmls []string
	var err error
	if e.useAPI {
		xmls, err = e.queryAPI(ch, query, evtQueryForwardDirection, eventlogMaxCycle)
	} else {
		xmls, err = queryWevtutil(ctx, ch, query, false, eventlogMaxCycle)
	}
	if err != nil {
		return 0, err
	}
	now := time.Now()
	count := 0
	for _, x := range xmls {
		l, id, err := parseEventXML(x, now)
		if err != nil || id == 0 {
			continue
		}
		e.mu.Lock()
		if id > e.next[ch] {
			e.next[ch] = id
		}
		e.mu.Unlock()
		channel, rid := ch, id
		if !e.pipe.Submit(ctx, Entry{Log: l, Commit: func() {
			// Les lots partent dans l'ordre : le dernier confirmé est le plus récent, y compris
			// après un journal vidé où les numéros repartent de 1.
			e.mu.Lock()
			e.durable[channel] = rid
			e.mu.Unlock()
			e.saver.MarkDirty()
		}}) {
			break
		}
		count++
	}
	return count, nil
}

// newest rend le numéro du dernier événement du canal (0 si le canal est vide).
func (e *Eventlog) newest(ch string) (uint64, error) {
	var xmls []string
	var err error
	if e.useAPI {
		xmls, err = e.queryAPI(ch, "*", evtQueryReverseDirection, 1)
	} else {
		xmls, err = queryWevtutil(context.Background(), ch, "*", true, 1)
	}
	if err != nil || len(xmls) == 0 {
		return 0, err
	}
	_, id, err := parseEventXML(xmls[0], time.Now())
	return id, err
}

// queryAPI exécute une requête XPath sur un canal et rend le XML des événements, message
// compris quand le fournisseur a ses textes installés.
func (e *Eventlog) queryAPI(ch, query string, direction uint32, max int) ([]string, error) {
	chp, err := windows.UTF16PtrFromString(ch)
	if err != nil {
		return nil, err
	}
	qp, err := windows.UTF16PtrFromString(query)
	if err != nil {
		return nil, err
	}
	h, _, callErr := procEvtQuery.Call(0, uintptr(unsafe.Pointer(chp)), uintptr(unsafe.Pointer(qp)), uintptr(evtQueryChannelPath|direction))
	if h == 0 {
		return nil, fmt.Errorf("EvtQuery : %w", callErr)
	}
	defer procEvtClose.Call(h)

	var out []string
	events := make([]uintptr, 64)
	for len(out) < max {
		want := min(len(events), max-len(out))
		var returned uint32
		ok, _, callErr := procEvtNext.Call(h, uintptr(want), uintptr(unsafe.Pointer(&events[0])), 1000, 0, uintptr(unsafe.Pointer(&returned)))
		if ok == 0 {
			if errors.Is(callErr, errNoMoreItems) {
				break
			}
			return out, fmt.Errorf("EvtNext : %w", callErr)
		}
		for i := 0; i < int(returned); i++ {
			if x, err := e.render(events[i]); err == nil {
				out = append(out, x)
			}
			procEvtClose.Call(events[i])
		}
		if returned == 0 {
			break
		}
	}
	return out, nil
}

// render rend le XML complet (avec RenderingInfo et son message) par EvtFormatMessage ;
// à défaut (fournisseur sans métadonnées), le XML brut par EvtRender.
func (e *Eventlog) render(ev uintptr) (string, error) {
	raw, err := evtRender(ev)
	if err != nil {
		return "", err
	}
	if procEvtFormatMessage.Find() != nil {
		return raw, nil
	}
	provider := providerName(raw)
	if provider == "" {
		return raw, nil
	}
	pub := e.publisherHandle(provider)
	if pub == 0 {
		return raw, nil
	}
	if full, err := evtFormatXML(pub, ev); err == nil && full != "" {
		return full, nil
	}
	return raw, nil
}

func (e *Eventlog) publisherHandle(provider string) windows.Handle {
	e.mu.Lock()
	defer e.mu.Unlock()
	if h, ok := e.publisher[provider]; ok {
		return h
	}
	p, _ := windows.UTF16PtrFromString(provider)
	h, _, _ := procEvtOpenPublisherMetadata.Call(0, uintptr(unsafe.Pointer(p)), 0, 0, 0)
	e.publisher[provider] = windows.Handle(h) // 0 retenu aussi : on ne réessaie pas à chaque événement
	return windows.Handle(h)
}

func evtRender(ev uintptr) (string, error) {
	var used, props uint32
	// Premier appel pour connaître la taille (en octets) ; ERROR_INSUFFICIENT_BUFFER attendu.
	ok, _, callErr := procEvtRender.Call(0, ev, evtRenderEventXML, 0, 0, uintptr(unsafe.Pointer(&used)), uintptr(unsafe.Pointer(&props)))
	if ok == 0 && !errors.Is(callErr, errInsufficientBuffer) {
		return "", fmt.Errorf("EvtRender : %w", callErr)
	}
	if used == 0 {
		return "", errors.New("EvtRender : rendu vide")
	}
	buf := make([]uint16, used/2+1)
	ok, _, callErr = procEvtRender.Call(0, ev, evtRenderEventXML, uintptr(len(buf)*2), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&used)), uintptr(unsafe.Pointer(&props)))
	if ok == 0 {
		return "", fmt.Errorf("EvtRender : %w", callErr)
	}
	return windows.UTF16ToString(buf), nil
}

func evtFormatXML(pub windows.Handle, ev uintptr) (string, error) {
	var used uint32
	// Taille en caractères (et non en octets, contrairement à EvtRender).
	ok, _, callErr := procEvtFormatMessage.Call(uintptr(pub), ev, 0, 0, 0, evtFormatMessageXML, 0, 0, uintptr(unsafe.Pointer(&used)))
	if ok == 0 && !errors.Is(callErr, errInsufficientBuffer) {
		return "", callErr
	}
	if used == 0 {
		return "", errors.New("EvtFormatMessage : rendu vide")
	}
	buf := make([]uint16, used+1)
	ok, _, callErr = procEvtFormatMessage.Call(uintptr(pub), ev, 0, 0, 0, evtFormatMessageXML, uintptr(len(buf)), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&used)))
	if ok == 0 {
		return "", callErr
	}
	return windows.UTF16ToString(buf), nil
}

// queryWevtutil : repli par l'outil en ligne de commande, livré avec toutes les versions
// de Windows. /f:RenderedXml inclut le message, /e:Events enveloppe la sortie.
func queryWevtutil(ctx context.Context, ch, query string, reverse bool, max int) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	rd := "false"
	if reverse {
		rd = "true"
	}
	cmd := exec.CommandContext(ctx, "wevtutil", "qe", ch, "/q:"+query, "/f:RenderedXml", "/e:Events",
		fmt.Sprintf("/c:%d", max), "/rd:"+rd)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("wevtutil : %w", err)
	}
	// La sortie console peut ne pas être de l'UTF-8 : on remplace ce qui ne l'est pas
	// plutôt que de rejeter tout l'événement.
	return splitEvents(strings.ToValidUTF8(string(out), "?")), nil
}

// FlushState écrit les signets maintenant (arrêt : après la confirmation du dernier lot).
func (e *Eventlog) FlushState() { e.saver.Flush() }
