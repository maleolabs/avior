package archive

// Test acquireCLIBinaries (Fase 3): binary CLI bersumber dari release —
// download/ cache ditangani cliassets, archive hanya menyalin hasil
// terverifikasi ke staging dan melaporkan path manifest arsitektur tetap.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maleolabs/avior/internal/brand"
	"github.com/maleolabs/avior/internal/cliassets"
)

// goModContent adalah fixture go.mod minimal yang ditulis test.
// Sengaja dipisah agar raw string di avoidance escape bug heredoc.
const goModContent = "module example.com/x\n\ngo 1.24\n"

// stubEnsure mengembalikan file binary palsu di tmp — mensimulasikan
// cliassets.EnsureBinaries tanpa network di test archive.
func stubEnsure(t *testing.T) func(context.Context, string, string, string) ([]string, error) {
	t.Helper()
	return func(_ context.Context, _ string, _ string, osName string) ([]string, error) {
		dir := t.TempDir()
		var paths []string
		for _, arch := range []string{"amd64", "arm64"} {
			p := filepath.Join(dir, cliassets.AssetName(osName, arch))
			if err := os.WriteFile(p, []byte("STUB-"+osName+"-"+arch), 0o755); err != nil {
				t.Fatal(err)
			}
			paths = append(paths, p)
		}
		return paths, nil
	}
}

// TestAcquireCLIBinariesDryRun: dry tidak menyentuh ensure/ jaringan.
func TestAcquireCLIBinariesDryRun(t *testing.T) {
	called := false
	ensure := func(context.Context, string, string, string) ([]string, error) {
		called = true
		return nil, nil
	}
	got, err := acquireCLIBinaries(context.Background(), t.TempDir(), "1.2.3", "linux", true, nil, ensure)
	if err != nil {
		t.Fatalf("dry-run gagal: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("dry-run mengembalikan %d path, want 0", len(got))
	}
	if called {
		t.Error("dry-run memanggil ensure — seharusnya tidak menyentuh sumber binary")
	}
}

// TestAcquireCLIBinariesBundlesTwo: 2 binary (amd64+arm64) disalin ke
// staging dengan nama kontrak release, path manifest tetap
// tools/<brand>/bin/<name>_<os>_<arch>.
func TestAcquireCLIBinariesBundlesTwo(t *testing.T) {
	root := t.TempDir()
	var events []string
	progress := func(e ProgressEvent) { events = append(events, e.Status+":"+e.Label) }

	got, err := acquireCLIBinaries(context.Background(), root, "1.2.3", "darwin", false, progress, stubEnsure(t))
	if err != nil {
		t.Fatalf("acquire gagal: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("mengembalikan %d path, want 2", len(got))
	}
	for _, rel := range got {
		if !strings.HasPrefix(filepath.ToSlash(rel), "tools/"+brand.Name+"/bin/") {
			t.Errorf("path manifest bukan layout tools/<brand>/bin: %s", rel)
		}
	}
	// Staging fisik: binary ada dan executable.
	for _, name := range []string{cliassets.AssetName("darwin", "amd64"), cliassets.AssetName("darwin", "arm64")} {
		p := filepath.Join(root, "tools", brand.Name, "bin", name)
		info, err := os.Stat(p)
		if err != nil {
			t.Errorf("staging binary hilang %s: %v", name, err)
			continue
		}
		if info.Mode().Perm()&0o100 == 0 {
			t.Errorf("staging binary tidak executable: %s (mode %v)", name, info.Mode())
		}
	}
	// Progress penuh: 2 completed.
	completed := 0
	for _, e := range events {
		if strings.HasPrefix(e, "completed:") {
			completed++
		}
	}
	if completed != 2 {
		t.Errorf("progress completed = %d, want 2 (%v)", completed, events)
	}
}

// TestAcquireCLIBinariesWindowsExe: windows mempertahankan suffix .exe.
func TestAcquireCLIBinariesWindowsExe(t *testing.T) {
	got, err := acquireCLIBinaries(context.Background(), t.TempDir(), "1.2.3", "windows", false, nil, stubEnsure(t))
	if err != nil {
		t.Fatalf("acquire windows gagal: %v", err)
	}
	for _, rel := range got {
		if !strings.HasSuffix(filepath.ToSlash(rel), ".exe") {
			t.Errorf("path windows tanpa .exe: %s", rel)
		}
	}
}

// TestAcquireCLIBinariesEnsureFails: ensure gagal → acquire gagal
// (fail-closed, tidak ada staging binary tersisa).
func TestAcquireCLIBinariesEnsureFails(t *testing.T) {
	root := t.TempDir()
	ensure := func(context.Context, string, string, string) ([]string, error) {
		return nil, errEnsure
	}
	if _, err := acquireCLIBinaries(context.Background(), root, "1.2.3", "linux", false, nil, ensure); err == nil {
		t.Fatal("acquire harus gagal saat ensure gagal")
	}
	bin := filepath.Join(root, "tools", brand.Name, "bin")
	if entries, _ := os.ReadDir(bin); len(entries) != 0 {
		t.Errorf("fail-closed meninggalkan %d file staging", len(entries))
	}
}

var errEnsure = &acquireErr{}

type acquireErr struct{}

func (e *acquireErr) Error() string { return "ensure stub gagal" }

// TestAcquireCLIBinariesDefaultEnsure: ensure kosong → default
// cliassets.EnsureBinaries (hanya memastikan tidak nil-panic; menjalankan
// ensure asli diuji di internal/cliassets sendiri).
func TestAcquireCLIBinariesDefaultEnsure(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("acquire dengan ensure nil panic: %v", r)
		}
	}()
	_, _ = acquireCLIBinaries(context.Background(), t.TempDir(), "9.9.9", "linux", true, nil, nil)
}
