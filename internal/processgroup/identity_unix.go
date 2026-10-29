//go:build !windows

package processgroup

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

func processIdentity(pid int) (string, string) {
	if pid <= 0 || processGone(pid) {
		return "", ""
	}
	p := strconv.Itoa(pid)
	exe := strings.TrimSpace(string(mustOutput("ps", "-p", p, "-o", "comm=")))
	cmd := processCommandLine(pid)
	if exe == "" || cmd == "" {
		return "", ""
	}
	return exe, cmd
}

func processCommandLine(pid int) string {
	return strings.TrimSpace(string(mustOutput("ps", "-p", strconv.Itoa(pid), "-o", "args=")))
}

func processGone(pid int) bool {
	if pid <= 0 {
		return true
	}
	// os.FindProcess bukan cek eksistensi di Unix (bisa mengembalikan handle
	// untuk PID positif apa pun), dan Signal(nil) menyamakan proses tak
	// terjangkau dengan proses yang hilang — monitor di bawah uid berbeda
	// pernah menandai layanan yang hidup sebagai "gone" karena EPERM.
	// kill(pid, 0) punya semantik yang benar: ESRCH berarti absen; EPERM
	// berarti hadir tapi tidak bisa disinyal.
	err := syscall.Kill(pid, 0)
	return err == syscall.ESRCH
}

func terminatePID(pid int) {
	if pid > 0 {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
}

func mustOutput(name string, args ...string) []byte {
	b, _ := exec.Command(name, args...).Output()
	return b
}
