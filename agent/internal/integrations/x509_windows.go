//go:build windows

package integrations

import (
	"crypto/sha1"
	"crypto/x509"
	"encoding/hex"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Ouverture du magasin système de l'ordinateur, en lecture seule (wincrypt.h).
const (
	certStoreProvSystemW        = 10
	certStoreOpenExistingFlag   = 0x00004000
	certStoreReadonlyFlag       = 0x00008000
	certSystemStoreLocalMachine = 0x00020000
)

// platformCerts lit les magasins de l'ordinateur (« My » par défaut : certificats des services,
// Exchange, IIS, LDAPS, Bureau à distance). Chemin rendu : cert:\LocalMachine\<magasin>\<empreinte>.
func platformCerts(stores []string) []certInfo {
	var out []certInfo
	for _, name := range stores {
		p, err := windows.UTF16PtrFromString(name)
		if err != nil {
			continue
		}
		store, err := windows.CertOpenStore(certStoreProvSystemW, 0, 0,
			certSystemStoreLocalMachine|certStoreReadonlyFlag|certStoreOpenExistingFlag, uintptr(unsafe.Pointer(p)))
		if err != nil {
			continue
		}
		var list []certInfo
		var ctx *windows.CertContext
		for {
			ctx, err = windows.CertEnumCertificatesInStore(store, ctx)
			if err != nil || ctx == nil {
				break
			}
			der := append([]byte(nil), unsafe.Slice(ctx.EncodedCert, ctx.Length)...)
			c, perr := x509.ParseCertificate(der)
			if perr != nil {
				continue
			}
			sum := sha1.Sum(der)
			list = append(list, certFromX509(c, `cert:\LocalMachine\`+name+`\`+strings.ToUpper(hex.EncodeToString(sum[:])), "store"))
		}
		windows.CertCloseStore(store, 0)
		out = append(out, currentStoreCerts(list, time.Now())...)
	}
	return out
}
