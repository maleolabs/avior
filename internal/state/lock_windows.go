//go:build windows

package state

import (
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

var (
	stateKernel32     = syscall.NewLazyDLL("kernel32.dll")
	stateLockFileEx   = stateKernel32.NewProc("LockFileEx")
	stateUnlockFileEx = stateKernel32.NewProc("UnlockFileEx")
)

func lockState(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	var overlapped syscall.Overlapped
	r, _, callErr := stateLockFileEx.Call(f.Fd(), 2, 0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
	if r == 0 {
		_ = f.Close()
		return nil, callErr
	}
	return func() {
		_, _, _ = stateUnlockFileEx.Call(f.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
		_ = f.Close()
	}, nil
}
