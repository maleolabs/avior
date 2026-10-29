//go:build windows

package processgroup

import (
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

var (
	kernel32Lock = syscall.NewLazyDLL("kernel32.dll")
	lockFileEx   = kernel32Lock.NewProc("LockFileEx")
	unlockFileEx = kernel32Lock.NewProc("UnlockFileEx")
)

const lockfileExclusiveLock = 0x00000002

func lockRegistry(path string) (func(), error) {
	// Keep lockfile after unlock; deletion can split waiters across handles.
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	var overlapped syscall.Overlapped
	r, _, callErr := lockFileEx.Call(f.Fd(), lockfileExclusiveLock, 0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
	if r == 0 {
		_ = f.Close()
		return nil, callErr
	}
	return func() {
		_, _, _ = unlockFileEx.Call(f.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
		_ = f.Close()
	}, nil
}
