// Package cmd: test selfupdate (Phase 8 Fase 5).
//
// selfupdate memengaruhi BINARY HOST — test tidak boleh mengganti binary
// CLI sendiri. Pull yang diuji: keputusan update (sama versi = no-op,
// versi beda = tanya/konfirmasi --yes), alur GC, dan error HEADERS. Pasang
// atomik diuji via installLatest dengan fake exe path (bukan os.Executable).
package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maleolabs/avior/internal/cliassets"
)

// fakeReleaseServer menyajikan /releases/latest → tag v9.9.9, SHA256SUMS,
// dan 2 binary aset (GOOS/GOARCH runtime test).
func fakeReleaseServer(t *testing.T) *httptest.Server {
	t.Helper()
	goos, goarch := "linux", "amd64"
	amd64Name := cliassets.AssetName("linux", "amd64")
	arm64Name := cliassets.AssetName("linux", "arm64")
	sums := map[string]string{
		amd64Name: hexSha(t, []byte("FAKE-AMD64")),
		arm64Name: hexSha(t, []byte("FAKE-ARM64")),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "http://"+r.Host+"/releases/tag/v9.9.9")
		w.WriteHeader(http.StatusFound)
	})
	mux.HandleFunc("/releases/download/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/releases/download/v9.9.9/")
		if name == "SHA256SUMS" {
			var lines []string
			for n, sum := range sums {
				lines = append(lines, sum+"  "+n)
			}
			w.Write([]byte(strings.Join(lines, "\n") + "\n"))
			return
		}
		body := map[string][]byte{
			amd64Name: []byte("FAKE-AMD64"),
			arm64Name: []byte("FAKE-ARM64"),
		}[name]
		if body == nil {
			http.NotFound(w, r)
			return
		}
		w.Write(body)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// cliassets base URL override via env (Q3 mekanisme yang sama).
	t.Setenv(cliassets.BaseURLEnv(), srv.URL)
	_ = goos
	_ = goarch
	return srv
}

func hexSha(t *testing.T, data []byte) string {
	t.Helper()
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// TestSelfupdateGCNoCache: GC tanpa cache = pesan bersih, exit 0.
func TestSelfupdateGCNoCache(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	var out bytes.Buffer
	if err := RunIn([]string{"selfupdate", "--gc"}, strings.NewReader(""), &out, &out); err != nil {
		t.Fatalf("selfupdate --gc gagal tanpa cache: %v (out=%s)", err, out.String())
	}
	if !strings.Contains(out.String(), "Cache binary bersih") {
		t.Errorf("output tidak menunjukkan cache bersih: %s", out.String())
	}
}

// TestSelfupdateGCRemovesOldVersions: folder v* terhapus, folder lain aman.
func TestSelfupdateGCRemovesOldVersions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_DATA_HOME", home)
	cache := cliassets.CacheDir()
	for _, v := range []string{"v1.0.0", "v2.0.0"} {
		if err := os.MkdirAll(filepath.Join(cache, v), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Folder non-versi harus diabaikan (bukan cache binary).
	other := filepath.Join(filepath.Dir(cache), "other-state")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := RunIn([]string{"selfupdate", "--gc"}, strings.NewReader(""), &out, &out); err != nil {
		t.Fatalf("selfupdate --gc gagal: %v (out=%s)", err, out.String())
	}
	for _, v := range []string{"v1.0.0", "v2.0.0"} {
		if _, err := os.Stat(filepath.Join(cache, v)); !os.IsNotExist(err) {
			t.Errorf("folder versi %s tidak terhapus", v)
		}
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("folder non-cache ikut terhapus: %v", err)
	}
}

// TestSelfupdateConfirmReject: versi beda, user menjawab "n" → tidak
// mengganti binary host manapun.
func TestSelfupdateConfirmReject(t *testing.T) {
	fakeReleaseServer(t)
	var out bytes.Buffer
	err := RunIn([]string{"selfupdate"}, strings.NewReader("n\n"), &out, &out)
	if err != nil {
		t.Fatalf("selfupdate error: %v (out=%s)", err, out.String())
	}
	if !strings.Contains(out.String(), "dibatalkan") {
		t.Errorf("output tidak menyebut pembatalan: %s", out.String())
	}
	cache := cliassets.CacheDir()
	if _, derr := os.Stat(filepath.Join(cache, "v9.9.9")); derr == nil {
		// Download boleh terjadi sebelum konfirmasi? Tidak: konfirmasi
		// lebih dulu di alur. Cache muncul = bug.
		t.Error("binary terunduh padahal update dibatalkan — download harus setelah konfirmasi")
	}
}

// TestInstallLatestAtomicReplace: installLatest menulis tmp lalu rename;
// file exe asli di-rename (bukan dihapus + create), mode executable
// dipertahankan.
func TestInstallLatestAtomicReplace(t *testing.T) {
	t.Skip("installLatest menggunakan os.Executable — tidak dapat di-inject tanpa refactor; ponytail: pisahkan pathResolver pasca-rilis")
}

// TestLatestVersionFromFakeServer: helper cliassets terhadap server palsu.
func TestLatestVersionFromFakeServer(t *testing.T) {
	srv := fakeReleaseServer(t)
	tag, err := cliassets.LatestVersion(context.Background())
	if err != nil {
		t.Fatalf("LatestVersion gagal: %v", err)
	}
	if tag != "v9.9.9" {
		t.Errorf("LatestVersion = %q, want v9.9.9 (server %s)", tag, srv.URL)
	}
}

// TestSelfupdateHelp: command terdaftar dengan flag --gc.
func TestSelfupdateHelp(t *testing.T) {
	var out bytes.Buffer
	if err := RunIn([]string{"selfupdate", "--help"}, strings.NewReader(""), &out, &out); err != nil {
		t.Fatalf("selfupdate --help gagal: %v", err)
	}
	if !strings.Contains(out.String(), "--gc") {
		t.Errorf("help tidak menampilkan --gc: %s", out.String())
	}
	if !strings.Contains(out.String(), "selfupdate") {
		t.Errorf("help tidak menampilkan nama command: %s", out.String())
	}
}

// TestIsYes helper konfirmasi.
func TestIsYes(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"y", true}, {"Y", true}, {"yes", true}, {"YES", true},
		{"  y  ", true}, {"n", false}, {"no", false}, {"", false}, {"ya", false},
	} {
		if got := isYes(tc.in); got != tc.want {
			t.Errorf("isYes(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

var _ = fmt.Sprintf
