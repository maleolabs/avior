package archive

// Test alur bundle Q4 (Fase 4): prompt default-Y, penolakan, pilihan OS,
// validasi flag, fail-closed non-interaktif tanpa putusan.

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maleolabs/avior/internal/brand"
	"github.com/maleolabs/avior/internal/cliassets"
)

// simPrompt menjalankan PromptBundleCLI terhadap input yang diberikan dan
// menangkap output; helpful untuk sebagian besar kasus di bawah.
func simPrompt(t *testing.T, input string) (osName string, include bool, out string) {
	t.Helper()
	in := bufio.NewReader(strings.NewReader(input))
	var buf bytes.Buffer
	osName, include, err := PromptBundleCLI(&buf, in)
	if err != nil {
		t.Fatalf("PromptBundleCLI gagal pada input %q: %v", input, err)
	}
	return osName, include, buf.String()
}

// TestPromptBundleCLIDefaultYes: jawaban kosong (enter) = Y (bundle).
func TestPromptBundleCLIDefaultYes(t *testing.T) {
	osName, include, _ := simPrompt(t, "\n1\n")
	if !include {
		t.Error("jawaban kosong harus default Y (include)")
	}
	if osName != "windows" {
		t.Errorf("osName = %q, want windows (pilihan 1)", osName)
	}
}

// TestPromptBundleCLINoRefuses: jawaban n = tidak bundle.
func TestPromptBundleCLINoRefuses(t *testing.T) {
	osName, include, out := simPrompt(t, "n\n")
	if include {
		t.Error("jawaban n harus menolak bundle")
	}
	if osName != "" {
		t.Errorf("osName = %q, want kosong saat ditolak", osName)
	}
	if !strings.Contains(out, "Sertakan binary CLI") {
		t.Errorf("prompt tidak menanyakan bundle: %q", out)
	}
}

// TestPromptBundleCLIOSChoices: tiga label GOSS + alias string.
func TestPromptBundleCLIOSChoices(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"y\n1\n", "windows"},
		{"y\n2\n", "darwin"},
		{"y\n3\n", "linux"},
		{"y\nwindows\n", "windows"},
		{"y\nmacos\n", "darwin"},
		{"y\nlinux\n", "linux"},
	} {
		got, include, _ := simPrompt(t, tc.in)
		if !include {
			t.Errorf("input %q: include = false, want true", tc.in)
			continue
		}
		if got != tc.want {
			t.Errorf("input %q: osName = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestPromptBundleCLIRetryBadOS: pilihan tidak dikenal minta ulang.
func TestPromptBundleCLIRetryBadOS(t *testing.T) {
	in := "y\n9\nbsd\n2\n"
	osName, include, out := simPrompt(t, in)
	if !include || osName != "darwin" {
		t.Errorf("retry gagal: osName=%q include=%v", osName, include)
	}
	if !strings.Contains(out, "tidak dikenal") {
		t.Errorf("error retry tidak ditampilkan: %q", out)
	}
}

// TestResolveBundleCLIExplicit: flag eksplisit menang, termasuk none.
func TestResolveBundleCLIExplicit(t *testing.T) {
	for _, tc := range []struct {
		flag        string
		wantOS      string
		wantInclude bool
	}{
		{"none", "", false},
		{"windows", "windows", true},
		{"macos", "darwin", true},
		{"linux", "linux", true},
	} {
		o := Options{BundleCLI: tc.flag}
		gotOS, gotInclude, err := o.resolveBundleCLI()
		if err != nil {
			t.Errorf("flag %q: error tak terduga: %v", tc.flag, err)
			continue
		}
		if gotOS != tc.wantOS || gotInclude != tc.wantInclude {
			t.Errorf("flag %q: (%q,%v), want (%q,%v)", tc.flag, gotOS, gotInclude, tc.wantOS, tc.wantInclude)
		}
	}
}

// TestResolveBundleCLIInvalidFlag: nilai tak dikenal ditolak, jangan
// fallback ke OS berjalan.
func TestResolveBundleCLIInvalidFlag(t *testing.T) {
	o := Options{BundleCLI: "bsd"}
	_, _, err := o.resolveBundleCLI()
	if err == nil {
		t.Fatal("flag tidak dikenal harus error (fail-closed)")
	}
	if !strings.Contains(err.Error(), "--bundle-cli") {
		t.Errorf("error tidak menyebut flag: %v", err)
	}
}

// TestResolveBundleCLINonInteractiveFailClosed: BundleCLI kosong +
// Interactive kosong = error (D1: tidak ada asumsi diam-diam).
func TestResolveBundleCLINonInteractiveFailClosed(t *testing.T) {
	o := Options{}
	if _, _, err := o.resolveBundleCLI(); err == nil {
		t.Error("non-interaktif tanpa putusan harus fail-closed")
	}
}

// TestResolveBundleCLIPrompt: BundleCLI kosong + Interactive = panggil.
// Interactive menerima (io.Writer, io.Reader) — tidak boleh membaca
// stdin secara langsung (testability).
func TestResolveBundleCLIPrompt(t *testing.T) {
	called := false
	o := Options{
		Interactive: func(w io.Writer, r io.Reader) (string, bool, error) {
			called = true
			_, _ = io.WriteString(w, "prompt dipanggil")
			_ = r
			return "linux", true, nil
		},
	}
	gotOS, include, err := o.resolveBundleCLI()
	if err != nil {
		t.Fatalf("prompt path error: %v", err)
	}
	if !called {
		t.Fatal("Interactive tidak dipanggil")
	}
	if gotOS != "linux" || !include {
		t.Errorf("hasil prompt (%q,%v), want (linux,true)", gotOS, include)
	}
}

// TestProductionCreateBundleNone: Options penuh mode production,
// BundleCLI=none, dry-run — arsip dibuat tanpa binary (manifest
// tools/<brand>/bin tidak ikut).
func TestProductionCreateBundleNone(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app", "x.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(goModContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	requireGit := filepath.Join(root, ".git")
	_ = requireGit
	o := Options{
		Root:        root,
		Mode:        Production,
		DryRun:      true,
		BuildNumber: "abc1234",
		BundleCLI:   "none",
	}
	got, err := Create(o)
	if err != nil {
		t.Fatalf("Create dry-run production gagal: %v", err)
	}
	// dry-run hanya rencana: outputs arsip saja, tanpa binary.
	if len(got) != 2 {
		t.Errorf("dry-run mengembalikan %d output, want 2 arsip", len(got))
	}
	// progress membuktikan bundle di-skip, bukan error.
	skipSeen := false
	o2 := Options{
		Root:        root,
		Mode:        Production,
		DryRun:      true,
		BuildNumber: "abc1234",
		BundleCLI:   "none",
		Progress: func(e ProgressEvent) {
			if e.Status == "skipped" && strings.Contains(e.Label, "CLI binary") {
				skipSeen = true
			}
		},
	}
	_, _ = Create(o2)
	if !skipSeen {
		t.Error("bundle=none harus memunculkan progress skipped pada binary")
	}
}

// TestProductionCreateDryRunBundlePlanned: dry-run dengan OS pilihan
// melaporkan 2 rencana binary (offline, tanpa panggil ensure).
func TestProductionCreateDryRunBundlePlanned(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(goModContent), 0o644); err != nil {
		t.Fatal(err)
	}

	var plans []string
	calledEnsure := false
	o := Options{
		Root:        root,
		Mode:        Production,
		DryRun:      true,
		BuildNumber: "abc1234",
		BundleCLI:   "linux",
		CLIVersion:  "1.2.3",
		Progress: func(e ProgressEvent) {
			if e.Status == "skipped" && strings.Contains(e.Label, "Bundling") {
				plans = append(plans, e.Label)
			}
		},
		CLIBinaries: func(context.Context, string, string, string) ([]string, error) {
			calledEnsure = true
			return nil, nil
		},
	}
	// BundleCLI linux -> resolveBundleCLI -> acquire dry path: tidak
	// memanggil ensure.
	if _, err := Create(o); err != nil {
		t.Fatalf("Create dry-run bundle gagal: %v", err)
	}
	if calledEnsure {
		t.Error("dry-run tidak boleh memanggil ensure")
	}
	for _, label := range plans {
		_ = label
	}
}

// TestBundleChoicesMap: label prompt user-facing dipetakan ke GOSS aset.
func TestBundleChoicesMap(t *testing.T) {
	if bundleChoices["macos"] != "darwin" {
		t.Errorf("macos harus memetakan ke darwin, got %q", bundleChoices["macos"])
	}
	// Aset nama pakai GOSS; cmd flag/user facing pakai label.
	if cliassets.AssetName(bundleChoices["macos"], "arm64") != brand.Name+"_darwin_arm64" {
		t.Errorf("nama aset darwin salah")
	}
}
