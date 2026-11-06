package version

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// saveVersion merestore var ldflags setelah test menggantinya — nilai
// bocor antar-test akan membuat hasil tidak deterministik.
func saveVersion(t *testing.T) {
	t.Helper()
	orig := version
	t.Cleanup(func() { version = orig })
}

// TestVersionFromLdflags: var ldflags menang, prefix "v" dibuang.
func TestVersionFromLdflags(t *testing.T) {
	saveVersion(t)
	for _, tc := range []struct{ in, want string }{
		{"1.2.3", "1.2.3"},
		{"v2.0.0", "2.0.0"},
		{"  1.1.1  ", "1.1.1"}, // TrimSpace
	} {
		version = tc.in
		if got := Version(); got != tc.want {
			t.Errorf("Version() dengan ldflags %q = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestVersionFromFile: tanpa ldflags, file VERSION di sisi binary
// (disimulasikan lewat versionFileFrom) dipakai.
func TestVersionFromFile(t *testing.T) {
	saveVersion(t)
	version = ""
	dir := t.TempDir()
	want := "3.7.1"
	if err := os.WriteFile(filepath.Join(dir, "VERSION"), []byte(want+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got, ok := versionFileFrom(dir); !ok || got != want {
		t.Fatalf("versionFileFrom(%q) = %q, %v, want %q, true", dir, got, ok, want)
	}
}

// TestVersionFileWalksUp: file VERSION di atas direktori binary ditemukan
// dengan berjalan ke atas.
func TestVersionFileWalksUp(t *testing.T) {
	saveVersion(t)
	version = ""
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "VERSION"), []byte("9.9.9\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got, ok := versionFileFrom(nested); !ok || got != "9.9.9" {
		t.Fatalf("versionFileFrom(%q) = %q, %v, want 9.9.9, true", nested, got, ok)
	}
}

// TestVersionFileIgnoresProjectCWD: file VERSION di cwd proyek TIDAK
// boleh menjadi versi CLI — kebocoran ini membuat archive production
// mencari rilis GitHub versi aplikasi user (HTTP 404). Sumber
// versionFile hanya direktori executable; test binary go berada di
// cache build di luar tree modul sehingga tidak menemukan VERSION
// manapun, walau cwd proyek memilikinya.
func TestVersionFileIgnoresProjectCWD(t *testing.T) {
	saveVersion(t)
	version = ""
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(project)

	if got, ok := versionFile(); ok {
		t.Fatalf("versionFile() menemukan VERSION dari cwd proyek: %q — exeDir satu-satunya sumber yangboleh", got)
	}
	// versionFileFrom sendiri tetap mencari dari dir yang diberikan —
	// itu kontraknya; pemanggil versionFile() yang membatasi ke exe dir.
	if got, ok := versionFileFrom(project); !ok || got != "1.0.0" {
		t.Fatalf("versionFileFrom(project) = %q, %v, want 1.0.0, true (kontrak helper)", got, ok)
	}
}

// TestBuildInfoLabel: vcs.revision (bila ada) menghasilkan label dev-
// yang JELAS bukan tag rilis — tidak bisa lolos gate IsRelease.
func TestBuildInfoLabel(t *testing.T) {
	got, ok := buildInfo()
	if !ok {
		t.Skip("build info tidak memuat vcs.revision (wajar pada sebagian environment)")
	}
	if !strings.HasPrefix(got, "dev-") {
		t.Fatalf("label buildInfo = %q, harus berawalan dev-", got)
	}
	if isReleaseVersion(got) {
		t.Fatalf("label dev %q lolos cek rilis", got)
	}
}

// TestIsRelease: hanya X.Y.Z(-prerelease) yang valid; versi dev ditolak.
func TestIsRelease(t *testing.T) {
	for _, tc := range []struct {
		v    string
		want bool
	}{
		{"1.0.0", true},
		{"0.1.0", true},
		{"1.2.3-beta.1", true},
		{"1.2.3-dev", false}, // suffix dev = build lokal, no release
		{"12.34.56", true},
		{"0.0.0-dev", false},
		{"dev-abc1234", false},
		{"1.2", false},
		{"1.2.x", false},
		{"v1.2.3", false}, // Version() sudah trim prefix v
		{"", false},
	} {
		if got := isReleaseVersion(tc.v); got != tc.want {
			t.Errorf("isReleaseVersion(%q) = %v, want %v", tc.v, got, tc.want)
		}
	}
}

// TestTag: Tag = "v" + Version.
func TestTag(t *testing.T) {
	saveVersion(t)
	version = "4.5.6"
	if got := Tag(); got != "v4.5.6" {
		t.Fatalf("Tag() = %q, want v4.5.6", got)
	}
}
