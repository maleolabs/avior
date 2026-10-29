//go:build windows

package processgroup

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// TerminationSignals pada Windows hanya Interrupt: sinyal Unix tidak ada.
func TerminationSignals() []os.Signal { return []os.Signal{os.Interrupt} }

// Windows tidak punya padanan process-group yang portabel di os/exec.
// Kill child langsung adalah fallback yang didukung; proses cucu diharapkan
// dimiliki command runner masing-masing dan dihentikan oleh parentnya.
func ConfigureProcessGroup(cmd *exec.Cmd) {}

func SignalGroup(int, syscall.Signal) error {
	return errors.New("processgroup: windows tidak mendukung sinyal process group")
}

func KillGroup(pid int) error {
	if pid <= 0 {
		return errInvalidPID
	}
	p, err := os.FindProcess(pid)
	if err != nil || p == nil {
		return err
	}
	return p.Kill()
}
