//go:build windows

package processgroup

import (
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

var (
	kernel32Identity      = syscall.NewLazyDLL("kernel32.dll")
	openProcess           = kernel32Identity.NewProc("OpenProcess")
	queryFullProcessImage = kernel32Identity.NewProc("QueryFullProcessImageNameW")
	getProcessTimes       = kernel32Identity.NewProc("GetProcessTimes")
)

const (
	processQueryLimitedInfo = 0x1000
	processTerminate        = 0x0001
)

// processIdentity mengembalikan (executable, identity) untuk pid di Windows.
// executable = full path image (QueryFullProcessImageNameW); identity = raw
// CreationTime FILETIME (hex) — unik per proses hidup, membuat reuse-PID
// setelah exit terdeteksi, padan start-time di sisi Unix (ps -o lstart).
//
// Hanya PROCESS_QUERY_LIMITED_INFORMATION — cukup untuk query image/times,
// tidak butuh PROCESS_VM_READ (terutama child console seperti cmd.exe yang
// membungkus timeout). Bila pid tidak bisa dibuka (tidak ada, atau
// ERROR_ACCESS_DENIED untuk proses sesi lain) kedua nilai dikembalikan
// kosong — pemanggil wajib memperlakukan identitas kosong sebagai "tidak
// dikenal" dan mematikan aksi destruktif (lihat recordMatchesProcess).
func processIdentity(pid int) (string, string) {
	if pid <= 0 {
		return "", ""
	}
	h, _, _ := openProcess.Call(processQueryLimitedInfo, 0, uintptr(pid))
	if h == 0 {
		return "", ""
	}
	defer syscall.CloseHandle(syscall.Handle(h))

	var buf [syscall.MAX_LONG_PATH]uint16
	size := uint32(len(buf))
	ret, _, _ := queryFullProcessImage.Call(h, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if ret == 0 {
		return "", ""
	}
	exe := strings.TrimSpace(syscall.UTF16ToString(buf[:size]))

	var create, exit, kernel, user syscall.Filetime
	ret, _, callErr := getProcessTimes.Call(h,
		uintptr(unsafe.Pointer(&create)),
		uintptr(unsafe.Pointer(&exit)),
		uintptr(unsafe.Pointer(&kernel)),
		uintptr(unsafe.Pointer(&user)))
	if ret == 0 {
		_ = callErr
		return exe, ""
	}
	// HighDateTime<<32 | LowDateTime: nilai mentah cukup sebagai identitas
	// (100ns sejak 1601); tidak dikonversi ke time.Time.
	raw := uint64(create.HighDateTime)<<32 | uint64(create.LowDateTime)
	return exe, strconv.FormatUint(raw, 16)
}

func processCommandLine(pid int) string { return "" }

func processGone(pid int) bool {
	h, _, _ := openProcess.Call(processQueryLimitedInfo, 0, uintptr(pid))
	if h == 0 {
		return true
	}
	_ = syscall.CloseHandle(syscall.Handle(h))
	return false
}

func terminatePID(pid int) {
	h, _, _ := openProcess.Call(processQueryLimitedInfo|processTerminate, 0, uintptr(pid))
	if h == 0 {
		return
	}
	_ = syscall.TerminateProcess(syscall.Handle(h), 1)
	_ = syscall.CloseHandle(syscall.Handle(h))
}
