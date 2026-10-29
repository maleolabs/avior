//go:build !windows

// Smoke test launcher arsip rilis (m3-install-channels): setup.sh wajib
// menjalankan binary CLI yang ADA DI DIREKTORI ARSIPNYA SENDIRI — bukan
// binary yang di-resolve PATH. Diverifikasi dengan PATH sengaja dibuang:
// launcher tetap harus bekerja. Jalur setup.ps1/setup.bat tidak dapat
// dieksekusi di non-Windows; kehadiran + isinya dikunci TestLauncherFiles.
package release

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maleolabs/avior/internal/brand"
)

func TestSetupShLauncherRunsSiblingBinaryWithoutPATH(t *testing.T) {
	dir := t.TempDir()
	// Build binary CLI sungguhan — smoke nyata, bukan stub.
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, brand.Name)
	build := exec.Command("go", "build", "-o", bin, root)
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build CLI untuk smoke: %v", err)
	}
	launcher, err := os.ReadFile(filepath.Join(root, "release", "setup.sh"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "setup.sh")
	if err := os.WriteFile(path, launcher, 0o755); err != nil {
		t.Fatal(err)
	}
	// PATH kosong: satu-satunya cara launcher menemukan binary adalah
	// direktori arsipnya sendiri.
	cmd := exec.Command(path, "--help")
	cmd.Env = []string{"HOME=" + dir, "PATH="}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("setup.sh --help gagal: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), brand.Name) {
		t.Fatalf("output help tidak memuat usage CLI:\n%s", out)
	}
}

// TestGoInstallFromLocalModule menjalankan smoke-install.sh: jalur
// `go install <module>@latest` diwrap — module path + build + binary bisa
// dieksekusi. Register modul publik sendiri diverifikasi saat rilis asli.
func TestGoInstallFromLocalModule(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tidak tersedia")
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	script := exec.Command("sh", "smoke-install.sh")
	script.Dir = filepath.Join(root, "release")
	out, err := script.CombinedOutput()
	if err != nil {
		t.Fatalf("smoke go install gagal: %v\n%s", err, out)
	}
}
