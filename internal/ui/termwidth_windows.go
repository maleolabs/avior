//go:build windows

package ui

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32            = syscall.NewLazyDLL("kernel32.dll")
	getConsoleScreenBuf = kernel32.NewProc("GetConsoleScreenBufferInfo")
)

type coord struct{ x, y int16 }
type smallRect struct{ left, top, right, bottom int16 }
type consoleScreenBufferInfo struct {
	dwSize              coord
	dwCursorPosition    coord
	wAttributes         uint16
	srWindow            smallRect
	dwMaximumWindowSize coord
}

// TermWidth mengembalikan lebar terminal (kolom) untuk stdout bila console.
// Fallback 80 bila bukan console atau panggilan gagal.
func TermWidth() int {
	h := syscall.Handle(os.Stdout.Fd())
	var info consoleScreenBufferInfo
	r, _, _ := getConsoleScreenBuf.Call(uintptr(h), uintptr(unsafe.Pointer(&info)))
	if r == 0 {
		return 80
	}
	w := int(info.srWindow.right - info.srWindow.left + 1)
	if w <= 0 {
		w = int(info.dwSize.x)
	}
	if w <= 0 {
		return 80
	}
	return w
}
