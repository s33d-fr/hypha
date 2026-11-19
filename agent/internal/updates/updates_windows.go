//go:build windows

package updates

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync/atomic"

	ole "github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"golang.org/x/sys/windows/registry"

	"s33d.fr/hypha/agent/internal/model"
)

// collect interroge l'agent Windows Update (Microsoft.Update.Session) hors ligne : la
// recherche porte sur le dernier relevé fait par Windows Update ou WSUS, sans contacter
// de serveur. Sans réponse (service coupé, droits), Known reste faux et pending est absent.
func collect(ctx context.Context) Result {
	r := Result{Source: "windows_update"}
	if !searching.CompareAndSwap(false, true) {
		r.Err = errors.New("recherche Windows Update précédente toujours en cours")
		return r
	}
	done := make(chan Result, 1)
	go func() {
		defer searching.Store(false)
		done <- search()
	}()
	select {
	case res := <-done:
		res.Source = r.Source
		return res
	case <-ctx.Done():
		r.Err = errors.New("Windows Update ne répond pas")
		return r
	}
}

// searching : une seule recherche COM à la fois, même si la précédente ne rend jamais la main.
var searching atomic.Bool

func search() (r Result) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer func() {
		if p := recover(); p != nil {
			r = Result{Err: fmt.Errorf("Windows Update : %v", p)}
		}
	}()
	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
		var oe *ole.OleError
		if !errors.As(err, &oe) || oe.Code() != 1 { // S_FALSE : déjà initialisé
			return Result{Err: err}
		}
	}
	defer ole.CoUninitialize()
	unk, err := oleutil.CreateObject("Microsoft.Update.Session")
	if err != nil {
		return Result{Err: err}
	}
	defer unk.Release()
	session, err := unk.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return Result{Err: err}
	}
	defer session.Release()
	sv, err := oleutil.CallMethod(session, "CreateUpdateSearcher")
	if err != nil {
		return Result{Err: err}
	}
	searcher := sv.ToIDispatch()
	defer searcher.Release()
	if _, err := oleutil.PutProperty(searcher, "Online", false); err != nil {
		return Result{Err: err}
	}
	res, err := oleutil.CallMethod(searcher, "Search", "IsInstalled=0 and IsHidden=0 and Type='Software'")
	if err != nil {
		return Result{Err: err}
	}
	result := res.ToIDispatch()
	defer result.Release()
	uv, err := oleutil.GetProperty(result, "Updates")
	if err != nil {
		return Result{Err: err}
	}
	coll := uv.ToIDispatch()
	defer coll.Release()
	cv, err := oleutil.GetProperty(coll, "Count")
	if err != nil {
		return Result{Err: err}
	}
	n := int(cv.Val)
	for i := 0; i < n; i++ {
		iv, err := oleutil.GetProperty(coll, "Item", i)
		if err != nil {
			continue
		}
		item := iv.ToIDispatch()
		u := model.PendingUpdate{}
		if t, err := oleutil.GetProperty(item, "Title"); err == nil {
			u.Name = t.ToString()
		}
		if s, err := oleutil.GetProperty(item, "MsrcSeverity"); err == nil && s.ToString() != "" {
			u.Security = true
		}
		if !u.Security {
			u.Security = inSecurityCategory(item)
		}
		item.Release()
		r.List = append(r.List, u)
	}
	r.Known = true
	r.Pending, r.Security = len(r.List), countSecurity(r.List)
	return r
}

func inSecurityCategory(item *ole.IDispatch) bool {
	cv, err := oleutil.GetProperty(item, "Categories")
	if err != nil {
		return false
	}
	cats := cv.ToIDispatch()
	defer cats.Release()
	nv, err := oleutil.GetProperty(cats, "Count")
	if err != nil {
		return false
	}
	for i := 0; i < int(nv.Val); i++ {
		c, err := oleutil.GetProperty(cats, "Item", i)
		if err != nil {
			continue
		}
		cd := c.ToIDispatch()
		name, _ := oleutil.GetProperty(cd, "Name")
		cd.Release()
		if name != nil && strings.Contains(strings.ToLower(name.ToString()), "security") {
			return true
		}
	}
	return false
}

// quickReboot : clés de registre posées quand un redémarrage est attendu.
func quickReboot() (bool, bool) {
	for _, p := range []string{
		`SOFTWARE\Microsoft\Windows\CurrentVersion\WindowsUpdate\Auto Update\RebootRequired`,
		`SOFTWARE\Microsoft\Windows\CurrentVersion\Component Based Servicing\RebootPending`,
	} {
		if k, err := registry.OpenKey(registry.LOCAL_MACHINE, p, registry.QUERY_VALUE); err == nil {
			k.Close()
			return true, true
		}
	}
	return false, true
}
