//go:build windows

package trust

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// Windows trust sengaja diimplementasikan via Crypt32: tanpa certutil,
// PowerShell, elevasi, atau executable lain yang di-install user.
var (
	crypt32       = syscall.NewLazyDLL("crypt32.dll")
	certOpenStore = crypt32.NewProc("CertOpenStore")
	certEnum      = crypt32.NewProc("CertEnumCertificatesInStore")
	certAdd       = crypt32.NewProc("CertAddEncodedCertificateToStore")
	certDelete    = crypt32.NewProc("CertDeleteCertificateFromStore")
	certClose     = crypt32.NewProc("CertCloseStore")
)

const (
	certStoreProvSystem    = 10
	collectionCurrentUser  = 0x00010000
	certAddReplaceExisting = 3
)

type certContext struct {
	encoding   uint32
	encoded    *byte
	encodedLen uint32
	certInfo   uintptr
	store      uintptr
}

func windowsStore() (uintptr, error) {
	name, _ := syscall.UTF16PtrFromString("ROOT")
	r, _, e := certOpenStore.Call(certStoreProvSystem, 0, 0, collectionCurrentUser, uintptr(unsafe.Pointer(name)))
	if r == 0 {
		return 0, fmt.Errorf("open CurrentUser\\Root: %w", e)
	}
	return r, nil
}

func readWindowsCert(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if p, _ := pem.Decode(b); p != nil {
		b = p.Bytes
	}
	if _, err := x509.ParseCertificate(b); err != nil {
		return nil, fmt.Errorf("parse root certificate: %w", err)
	}
	return b, nil
}

func installWindowsTrust(path string) error {
	b, err := readWindowsCert(path)
	if err != nil {
		return err
	}
	h, err := windowsStore()
	if err != nil {
		return err
	}
	defer certClose.Call(h, 0)
	r, _, e := certAdd.Call(h, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), certAddReplaceExisting, 0)
	if r == 0 {
		return fmt.Errorf("add certificate to CurrentUser\\Root: %w", e)
	}
	return nil
}

func removeWindowsTrust(path string) error {
	b, err := readWindowsCert(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	h, err := windowsStore()
	if err != nil {
		return err
	}
	defer certClose.Call(h, 0)
	// Enumerasi dan bandingkan DER — menghindari penghapusan sertifikat lain
	// yang kebetulan punya friendly name sama.
	var ctx uintptr
	for {
		r, _, _ := certEnum.Call(h, ctx)
		if r == 0 {
			break
		}
		ctx = r
		c := (*certContext)(unsafe.Pointer(r))
		der := unsafe.Slice(c.encoded, c.encodedLen)
		if string(der) == string(b) {
			certDelete.Call(r)
			break
		}
	}
	return nil
}
