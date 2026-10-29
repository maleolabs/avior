//go:build linux

package processgroup

import (
	"fmt"
	"os"
	"os/exec"
)

// AuthorizeSudo memperoleh kredensial sudo SEBELUM layanan long-running
// dimulai. Operasi yang butuh privilege (trust CA, tulis /etc/hosts) dapat
// memanggil sudo; melakukan preflight di sini menjaga prompt tetap menempel
// di terminal user, bukan tercampur output server.
func AuthorizeSudo() error {
	if os.Geteuid() == 0 {
		return nil
	}
	sudo, err := exec.LookPath("sudo")
	if err != nil {
		return fmt.Errorf("privilege administrator membutuhkan sudo, tetapi sudo tidak ditemukan; pasang sudo atau jalankan sebagai root")
	}
	cmd := exec.Command(sudo, "-v")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("otorisasi sudo gagal: %w", err)
	}
	return nil
}
