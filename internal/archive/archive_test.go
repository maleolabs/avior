package archive

// Test lokasi modul CLI (kasus error "CLI module tidak ditemukan" setelah
// pemisahan tool dari source proyek): tool standalone harus menemukan
// modulnya sendiri lewat executable yang berjalan.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// goModContent adalah fixture go.mod minimal yang ditulis test.
// Sengaja dipisah agar raw string di avoidance escape bug heredoc.
const goModContent = "module example.com/x\n\ngo 1.24\n"

// TestCLIModuleLocatorExplicitDir: locator menerima path modul valid
// (validasi go.mod) walaupun root lain.
func TestCLIModuleLocatorExplicitDir(t *testing.T) {
	mod := t.TempDir()
	if err := os.WriteFile(filepath.Join(mod, "go.mod"), []byte(goModContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := DefaultCLIModuleLocator(mod); err != nil {
		t.Fatalf("locator gagal pada dir modul valid: %v", err)
	}
}

// TestCLIModuleLocatorExplicitEnv: ARC_CLIMODULE eksplisit menang apapun
// kondisi root.
func TestCLIModuleLocatorExplicitEnv(t *testing.T) {
	root := t.TempDir()
	mod := t.TempDir()
	if err := os.WriteFile(filepath.Join(mod, "go.mod"), []byte(goModContent), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(cliModuleEnv, mod)

	got, err := DefaultCLIModuleLocator(root)
	if err != nil {
		t.Fatalf("locator dengan env eksplisit gagal: %v", err)
	}
	if got != mod {
		t.Fatalf("locator = %s, want %s (eksplisit)", got, mod)
	}
}

// TestCLIModuleLocatorEmbedsLayout: layout monorepo pendahulu
// (tools/<brand>/go.mod di dalam root) tetap didukung.
func TestCLIModuleLocatorEmbedsLayout(t *testing.T) {
	root := t.TempDir()
	embedded := toolLayout(root)
	if err := os.MkdirAll(embedded, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(embedded, "go.mod"), []byte(goModContent), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(cliModuleEnv, "")

	got, err := DefaultCLIModuleLocator(root)
	if err != nil {
		t.Fatalf("locator layout embed gagal: %v", err)
	}
	if got != embedded {
		t.Fatalf("locator = %s, want %s (layout embed)", got, embedded)
	}
}

// TestCLIModuleLocatorActionableError: semua kandidat absen → error
// menyebut masing-masing path (pesan actionable, bukan generik).
//
// Catatan: test binary go ditempatkan di cache build di LUAR tree modul,
// sehingga kandidat exe-dir memang tidak valid di sini — itu sekaligus
// membuktikan fail-closed pada lingkungan yang berbeda dari CI rilis.
func TestCLIModuleLocatorActionableError(t *testing.T) {
	root := t.TempDir()
	_, err := DefaultCLIModuleLocator(root)
	if err == nil {
		t.Skip("kandidat lain (exe dir) kebetulan valid — skip tes error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "kandidat diperiksa") {
		t.Fatalf("error tidak mendaftar kandidat: %v", err)
	}
	if !strings.Contains(msg, toolLayout("")) {
		t.Fatalf("error tidak menyebut kandidat layout embed: %v", err)
	}
	if !strings.Contains(msg, root) {
		t.Fatalf("error tidak menyebut kandidat root: %v", err)
	}
}

// TestBuildBinariesUsesLocator: buildBinaries memakai locator yang
// di-inject; buildBinaries HARUS gagal bila locator gagal (fail-closed,
// tanpa fallback diam-diam ke root).
func TestBuildBinariesUsesLocator(t *testing.T) {
	called := false
	locator := func(root string) (string, error) {
		called = true
		return "", &moduleError{root: root}
	}
	if _, err := buildBinaries(t.TempDir(), false, nil, locator); err == nil {
		t.Fatal("buildBinaries harus gagal saat locator gagal")
	}
	if !called {
		t.Fatal("buildBinaries tidak memanggil locator yang di-inject")
	}
}

type moduleError struct{ root string }

func (e *moduleError) Error() string { return "locator gagal untuk " + e.root }
