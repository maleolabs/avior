package cliassets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maleolabs/avior/internal/brand"
)

// newFakeRelease adalah server release palsu: menyajikan SHA256SUMS dan
// binary per-arch. Mengembalikan base URL + data manifest.
// newFakeRelease adalah server release palsu: menyajikan SHA256SUMS dan
// binary per-arch. binHits (boleh nil) menghitung unduhan binary yang
// dilayani — dipakai membuktikan D3 (cache dulu, bukan network).
func newFakeRelease(t *testing.T, sums map[string]string, bins map[string][]byte, binHits *int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/releases/download/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/releases/download/v1.2.3/")
		if r.URL.Path == "/releases/download/v1.2.3/"+sumsName {
			var lines []string
			for n, sum := range sums {
				lines = append(lines, sum+"  "+n)
			}
			w.Write([]byte(strings.Join(lines, "\n") + "\n"))
			return
		}
		body, ok := bins[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if binHits != nil {
			*binHits++
		}
		w.Write(body)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// overrideBase memakai fake server (paritas urlOverride FetchCaddy).
func overrideBase(t *testing.T, url string) {
	t.Helper()
	orig := baseURLOverride
	baseURLOverride = func() string { return url }
	t.Cleanup(func() { baseURLOverride = orig })
}

func fakeBin(t *testing.T, content string) []byte {
	t.Helper()
	return []byte(content)
}

func sumHex(t *testing.T, data []byte) string {
	t.Helper()
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// TestEnsureBinariesCacheFirst: pemanggilan kedua tidak menyentuh server
// (hit counter tidak bertambah).
func TestEnsureBinariesCacheFirst(t *testing.T) {
	hits := 0
	amd64Name := AssetName("linux", "amd64")
	arm64Name := AssetName("linux", "arm64")
	bins := map[string][]byte{
		amd64Name: fakeBin(t, "AMD64-BODY"),
		arm64Name: fakeBin(t, "ARM64-BODY"),
	}
	sums := map[string]string{
		amd64Name: sumHex(t, bins[amd64Name]),
		arm64Name: sumHex(t, bins[arm64Name]),
	}
	srv := newFakeRelease(t, sums, bins, &hits)
	overrideBase(t, srv.URL)

	ctx := context.Background()
	cache := t.TempDir()
	p1, err := EnsureBinaries(ctx, "1.2.3", cache, "linux")
	if err != nil {
		t.Fatalf("EnsureBinaries pertama gagal: %v", err)
	}
	if len(p1) != 2 {
		t.Fatalf("EnsureBinaries mengembalikan %d path, want 2", len(p1))
	}
	hitsAfterFirst := hits
	p2, err := EnsureBinaries(ctx, "1.2.3", cache, "linux")
	if err != nil {
		t.Fatalf("EnsureBinaries kedua gagal: %v", err)
	}
	if hits != hitsAfterFirst {
		t.Errorf("pemanggilan kedua menambah request server (%d → %d); cache seharusnya tidak menyentuh network", hitsAfterFirst, hits)
	}
	if strings.Join(p1, ",") != strings.Join(p2, ",") {
		t.Errorf("path cache tidak stabil: %v vs %v", p1, p2)
	}
}

// TestEnsureBinariesWindowsExe: windows dapat suffix .exe di nama aset.
func TestEnsureBinariesWindowsExe(t *testing.T) {
	bins := map[string][]byte{
		AssetName("windows", "amd64"): fakeBin(t, "WIN-AMD64"),
		AssetName("windows", "arm64"): fakeBin(t, "WIN-ARM64"),
	}
	sums := map[string]string{
		AssetName("windows", "amd64"): sumHex(t, bins[AssetName("windows", "amd64")]),
		AssetName("windows", "arm64"): sumHex(t, bins[AssetName("windows", "arm64")]),
	}
	srv := newFakeRelease(t, sums, bins, nil)
	overrideBase(t, srv.URL)

	got, err := EnsureBinaries(context.Background(), "1.2.3", t.TempDir(), "windows")
	if err != nil {
		t.Fatalf("EnsureBinaries windows gagal: %v", err)
	}
	for _, p := range got {
		if !strings.HasSuffix(p, ".exe") {
			t.Errorf("path windows tanpa .exe: %s", p)
		}
	}
}

// TestEnsureBinariesMissingAsset: binary tidak terdaftar di SHA256SUMS →
// fail-closed, tidak ada file tersisa.
func TestEnsureBinariesMissingAsset(t *testing.T) {
	amd64Name := AssetName("linux", "amd64")
	bins := map[string][]byte{amd64Name: fakeBin(t, "BODY")}
	sums := map[string]string{amd64Name: sumHex(t, bins[amd64Name])}
	srv := newFakeRelease(t, sums, bins, nil)
	overrideBase(t, srv.URL)

	cache := t.TempDir()
	_, err := EnsureBinaries(context.Background(), "1.2.3", cache, "linux")
	if err == nil {
		t.Fatal("EnsureBinaries shaingga sukses padahal arm64 tidak terdaftar")
	}
	if !strings.Contains(err.Error(), sumsName) {
		t.Errorf("error tidak menyebut %s: %v", sumsName, err)
	}
	entries, _ := os.ReadDir(filepath.Join(cache, "v1.2.3"))
	if len(entries) > 1 {
		t.Errorf("fail-closed meninggalkan %d file di cache, want ≤1", len(entries))
	}
}

// TestEnsureBinariesChecksumMismatch: digest salah → tolak + hapus.
func TestEnsureBinariesChecksumMismatch(t *testing.T) {
	bins := map[string][]byte{
		AssetName("linux", "amd64"): fakeBin(t, "CORRUPT-BODY"),
		AssetName("linux", "arm64"): fakeBin(t, "ARM64-BODY"),
	}
	// sums sengaja menukar digest → mismatch pada amd64.
	sums := map[string]string{
		AssetName("linux", "amd64"): sumHex(t, bins[AssetName("linux", "arm64")]),
		AssetName("linux", "arm64"): sumHex(t, bins[AssetName("linux", "arm64")]),
	}
	srv := newFakeRelease(t, sums, bins, nil)
	overrideBase(t, srv.URL)

	cache := t.TempDir()
	_, err := EnsureBinaries(context.Background(), "1.2.3", cache, "linux")
	if err == nil {
		t.Fatal("checksum mismatch harus gagal")
	}
	if !strings.Contains(err.Error(), "fail-closed") {
		t.Errorf("error tidak menyebut fail-closed: %v", err)
	}
	stalePath := filepath.Join(cache, "v1.2.3", AssetName("linux", "amd64"))
	if _, statErr := os.Stat(stalePath); statErr == nil {
		t.Error("binary korup tertinggal di cache — seharusnya dihapus")
	}
}

// TestEnsureBinariesServerDown: server release 500 → gagal (tidak ada
// fallback ke sumber lain).
func TestEnsureBinariesServerDown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	overrideBase(t, srv.URL)

	_, err := EnsureBinaries(context.Background(), "1.2.3", t.TempDir(), "linux")
	if err == nil {
		t.Fatal("server 500 harus gagal")
	}
	if !strings.Contains(err.Error(), "HTTP 500") && !strings.Contains(err.Error(), sumsName) {
		t.Errorf(" error tidak menyebut status: %v", err)
	}
}

// TestEnsureBinariesCorruptedCache: cache ada tetapi rusak (checksum
// tidak cocok) → download ulang, bukan pakai yang rusak.
func TestEnsureBinariesCorruptedCache(t *testing.T) {
	bins := map[string][]byte{
		AssetName("linux", "amd64"): fakeBin(t, "FRESH-BODY"),
		AssetName("linux", "arm64"): fakeBin(t, "ARM64-BODY"),
	}
	sums := map[string]string{
		AssetName("linux", "amd64"): sumHex(t, bins[AssetName("linux", "amd64")]),
		AssetName("linux", "arm64"): sumHex(t, bins[AssetName("linux", "arm64")]),
	}
	srv := newFakeRelease(t, sums, bins, nil)
	overrideBase(t, srv.URL)

	cache := t.TempDir()
	dir := filepath.Join(cache, "v1.2.3")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, AssetName("linux", "amd64")), []byte("STALE-CORRUPT"), 0o644); err != nil {
		t.Fatal(err)
	}
	paths, err := EnsureBinaries(context.Background(), "1.2.3", cache, "linux")
	if err != nil {
		t.Fatalf("cache rusak seharusnya direfresh: %v", err)
	}
	got, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "FRESH-BODY" {
		t.Errorf("binary cache rusak tidak diganti: %q", got)
	}
}

// TestParseSums: format sha256sum ketat — entri ganda, komentar, baris
// rusak, dan manifest kosong ditangani.
func TestParseSums(t *testing.T) {
	assetA := AssetName("linux", "amd64")
	assetB := AssetName("linux", "arm64")
	good := "abc123" + strings.Repeat("0", 58) + "  " + assetA + "\n# comment\n"
	cases := []struct {
		name    string
		input   string
		wantErr bool
		wantLen int
	}{
		{"valid", good + "def456" + strings.Repeat("0", 58) + "  " + assetB + "\n", false, 2},
		{"komentar-dan-kosong", "# only comment\n\n", true, 0},
		{"entri-rusak", "SHORT  " + assetA + "\n", true, 0},
		{"field-hilang", good + "extra  field  here\n", true, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sums, err := parseSums(strings.NewReader(tc.input))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseSums(%q) sukses, want error", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSums gagal: %v", err)
			}
			if len(sums) != tc.wantLen {
				t.Errorf("len(sums) = %d, want %d", len(sums), tc.wantLen)
			}
		})
	}
}

// TestLatestVersion: /releases/latest → 302, tag diambil dari Location.
func TestLatestVersion(t *testing.T) {
	var loc string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", loc+"/releases/tag/v9.8.7")
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	loc = srv.URL
	overrideBase(t, srv.URL)

	tag, err := LatestVersion(context.Background())
	if err != nil {
		t.Fatalf("LatestVersion gagal: %v", err)
	}
	if tag != "v9.8.7" {
		t.Errorf("LatestVersion = %q, want v9.8.7", tag)
	}
}

// TestLatestVersionNoRedirect: 200 tanpa redirect → error jelas.
func TestLatestVersionNoRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	overrideBase(t, srv.URL)

	if _, err := LatestVersion(context.Background()); err == nil {
		t.Error("LatestVersion sukses pada 200, want error (mengharapkan redirect)")
	}
}

// TestBaseURLOverrideEnv: env <NAME>_RELEASE_BASE_URL dipakai saat hook kosong.
func TestBaseURLOverrideEnv(t *testing.T) {
	t.Setenv(baseURLEnv, "https://mirror.internal/cli")
	orig := baseURLOverride
	baseURLOverride = nil
	t.Cleanup(func() { baseURLOverride = orig })

	if got := baseURL(); got != "https://mirror.internal/cli" {
		t.Errorf("baseURL() = %q, want mirror", got)
	}
}

// TestBaseURLDefault: tanpa override dan env → repo resmi brand.
func TestBaseURLDefault(t *testing.T) {
	t.Setenv(baseURLEnv, "")
	orig := baseURLOverride
	baseURLOverride = nil
	t.Cleanup(func() { baseURLOverride = orig })

	want := "https://" + brand.ModulePath
	if got := baseURL(); got != want {
		t.Errorf("baseURL() = %q, want %q", got, want)
	}
}

// TestAssetName: kontrak penamaan aset (paritas layout manifest arsip).
func TestAssetName(t *testing.T) {
	if got := AssetName("linux", "amd64"); got != brand.Name+"_linux_amd64" {
		t.Errorf("AssetName(linux) = %q", got)
	}
	if got := AssetName("windows", "arm64"); got != brand.Name+"_windows_arm64.exe" {
		t.Errorf("AssetName(windows) = %q, want .exe suffix", got)
	}
}
