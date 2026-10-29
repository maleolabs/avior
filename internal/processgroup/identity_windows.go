//go:build windows

package processgroup

import "os"

// Tanpa query start-time/executable, Windows tidak bisa menetapkan
// kepemilikan secara aman setelah restart. Mengembalikan identitas kosong
// mematikan aksi registry destruktif daripada berisiko kill akibat reuse PID.
func processIdentity(pid int) (string, string) { return "", "" }

func processCommandLine(pid int) string { return "" }

func processGone(pid int) bool {
	p, err := os.FindProcess(pid)
	return err != nil || p == nil
}

func terminatePID(pid int) {
	if p, err := os.FindProcess(pid); err == nil && p != nil {
		_ = p.Kill()
	}
}
