//go:build !windows

package processgroup

import (
	"os"
	"os/exec"
	"syscall"
)

// TerminationSignals adalah sinyal permintaan stop runtime (Ctrl+C/SIGTERM)
// — dipakai signal.NotifyContext oleh orchestrator.
func TerminationSignals() []os.Signal { return []os.Signal{os.Interrupt, syscall.SIGTERM} }

// ConfigureProcessGroup menempatkan child di process group sendiri (pid ==
// pgid) agar sinyal terminate menjangkau seluruh subtree child.
func ConfigureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// SignalGroup mengirim sig ke process group child. Gagal (mis. child sudah
// exit, ESRCH) dikembalikan ke pemanggil untuk fallback.
func SignalGroup(pid int, sig syscall.Signal) error {
	if pid <= 0 {
		return errInvalidPID
	}
	return syscall.Kill(-pid, sig)
}

// KillGroup adalah eskalasi SIGKILL ke process group child.
func KillGroup(pid int) error {
	if pid <= 0 {
		return errInvalidPID
	}
	return syscall.Kill(-pid, syscall.SIGKILL)
}
