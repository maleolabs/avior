// Package hostname adalah layer /etc/hosts (arc:avior-architecture): satu
// entri idempoten ber-marker yang dikelola tool, cleanup exact-line saja.
package hostname

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/maleolabs/avior/internal/brand"
)

// Hook ini menjaga batas filesystem/privilege tetap testable tanpa
// memperlemah jalur produksi.
var (
	hostsPath          = hostsFile
	privilegedWriter   = writePrivileged
	privilegePreflight = preflightSudo
)

func hostsFile() string {
	if runtime.GOOS == "windows" {
		root := os.Getenv("SystemRoot")
		if root == "" {
			root = `C:\Windows`
		}
		return root + `\System32\drivers\etc\hosts`
	}
	return "/etc/hosts"
}

// EnsureHost menambah satu entri idempoten ber-marker. Menolak mengubah hosts
// tanpa consent eksplisit; cleanup hanya menghapus baris managed yang persis
// sama ini.
func EnsureHost(domain string, consent bool) (func(), error) {
	domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
	if domain == "" || strings.ContainsAny(domain, "\r\n \t") {
		return func() {}, fmt.Errorf("domain hosts tidak valid")
	}
	if !consent {
		return func() {}, fmt.Errorf("menambah %s ke hosts membutuhkan --yes atau konfirmasi eksplisit", domain)
	}
	p := hostsPath()
	b, err := os.ReadFile(p)
	if err != nil {
		return func() {}, fmt.Errorf("baca hosts: %w", err)
	}
	line := managedHostLine(domain)
	text := string(b)
	inserted := false
	if !strings.Contains(text, line) {
		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		// Lakukan sebelum percobaan tulis. Beberapa filesystem Unix melaporkan
		// kegagalan tulis hosts sebagai varian EPERM/EACCES yang os.IsPermission
		// tidak klasifikasikan andal. Lebih penting: ini menjaga prompt sudo
		// tetap di foreground sebelum proses server menghasilkan output.
		if err = privilegePreflight(p); err != nil {
			return func() {}, err
		}
		if err = privilegedWriter(p, []byte(text+line+"\n")); err != nil {
			return func() {}, fmt.Errorf("tulis hosts (butuh hak administrator): %w", err)
		}
		inserted = true
	}
	return func() {
		if !inserted {
			return
		}
		current, e := os.ReadFile(p)
		if e != nil {
			return
		}
		_ = privilegedWriter(p, []byte(removeManagedHost(string(current), line)))
	}, nil
}

func managedHostLine(domain string) string {
	return "127.0.0.1 " + domain + " " + brand.HostsMarker + " " + domain
}

func removeManagedHost(text, line string) string {
	lines := strings.Split(text, "\n")
	out := lines[:0]
	for _, l := range lines {
		if strings.TrimSpace(l) != line {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// writePrivileged melakukan elevasi sebagai operasi foreground yang sadar.
// Dipanggil sebelum child process berjalan, sehingga prompt sudo tidak pernah
// terkubur output Laravel/Caddy. Di Unix sudo menerima command lewat file
// sementara dan tetap memakai terminal user untuk prompt-nya.
func writePrivileged(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0644); err == nil {
		return nil
	} else if !os.IsPermission(err) {
		return fmt.Errorf("buka hosts %s: %w", path, err)
	}
	if runtime.GOOS == "windows" {
		return fmt.Errorf("akses Administrator diperlukan untuk %s; buka terminal sebagai Administrator lalu jalankan ulang (Windows tidak mendukung sudo otomatis)", path)
	}
	// Pemanggil normalnya tidak privileged, jadi membuat file staging di
	// samping /etc/hosts akan gagal sebelum sudo sempat berjalan. Buat di temp
	// user yang writable; sudo melakukan pemindahan privileged di bawah. File
	// sementara selesai sebelum sudo memindahkannya. Jika direktorinya beda
	// filesystem, mv bisa menyalin alih-alih rename, tapi itu tetap lebih baik
	// daripada gagal sebelum elevasi dan tetap dilindungi siklus staging
	// 0600/0644 plus permission tujuan dari sudo.
	tmp, err := os.CreateTemp(os.TempDir(), "."+brand.Name+"-hosts-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Chmod(tmpName, 0644); err != nil {
		return err
	}
	if _, err := exec.LookPath("sudo"); err != nil {
		return fmt.Errorf("akses administrator diperlukan untuk %s, tetapi sudo tidak tersedia; jalankan ulang sebagai root atau instal sudo: %w", path, err)
	}
	// File sementara selesai sebelum sudo memindahkannya ke tempatnya.
	cmd := exec.Command("sudo", "--", "mv", tmpName, path)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err = cmd.Run(); err != nil {
		return fmt.Errorf("sudo gagal menulis hosts: %w", err)
	}
	return nil
}

func preflightSudo(path string) error {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		return nil
	}
	if _, err := exec.LookPath("sudo"); err != nil {
		return fmt.Errorf("akses administrator diperlukan untuk %s, tetapi sudo tidak tersedia; jalankan ulang sebagai root atau instal sudo: %w", path, err)
	}
	cmd := exec.Command("sudo", "-v")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("otorisasi sudo gagal untuk menulis %s: %w", path, err)
	}
	return nil
}
