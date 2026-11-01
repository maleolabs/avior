//go:build !windows

package ui

import (
	"os"
	"syscall"
	"unsafe"
)

type winsize struct {
	row, col, xpixel, ypixel uint16
}

// TermWidth mengembalikan lebar terminal (kolom) untuk stdout bila TTY.
// Fallback 80 bila bukan TTY atau ioctl gagal.
func TermWidth() int {
	f, err := os.Stdout.Stat()
	if err != nil || f.Mode()&os.ModeCharDevice == 0 {
		return 80
	}
	var ws winsize
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, os.Stdout.Fd(), uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws)))
	if errno != 0 || ws.col == 0 {
		return 80
	}
	return int(ws.col)
}
