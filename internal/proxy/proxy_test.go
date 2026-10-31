package proxy

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	stdruntime "runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/maleolabs/avior/internal/brand"
)

func TestCaddyArchivePlatformName(t *testing.T) {
	// Upstream menamai asset: darwin → "mac_", windows → ".zip".
	wantOS := stdruntime.GOOS
	if wantOS == "darwin" {
		wantOS = "mac"
	}
	wantExt := ".tar.gz"
	if stdruntime.GOOS == "windows" {
		wantExt = ".zip"
	}
	want := "https://github.com/caddyserver/caddy/releases/download/v" + CaddyVersion + "/caddy_" + CaddyVersion + "_" + wantOS + "_" + stdruntime.GOARCH + wantExt
	if got := fetchArchiveURL(); got != want {
		t.Fatalf("archive URL=%q want %q", got, want)
	}
}

func TestFetchCaddyExtractsOnlyBinaryAndRejectsMissing(t *testing.T) {
	// Isolasi manifest: FetchCaddy menulis manifest StateDir setelah pin
	// cocok — arahkan StateDir ke temp agar home asli tidak tersentuh.
	home := t.TempDir()
	t.Setenv("HOME", home)        // Unix: os.UserHomeDir
	t.Setenv("USERPROFILE", home) // Windows: os.UserHomeDir
	// Pin test: sha256("data") — dibekukan agar verifikasi Kontrak B tetap
	// dieksekusi di jalur sukses, bukan di-skip.
	pin := sha256.Sum256([]byte("data"))
	oldPin := pinOverride
	pinOverride = func(_, _ string) (string, bool) { return hex.EncodeToString(pin[:]), true }
	defer func() { pinOverride = oldPin }()

	archiveName := "caddy"
	if stdruntime.GOOS == "windows" {
		archiveName += ".exe"
	}
	for _, tc := range []struct {
		name    string
		include bool
		wantErr bool
	}{{"caddy", true, false}, {"other", false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if stdruntime.GOOS == "windows" {
					zw := zip.NewWriter(w)
					if tc.include {
						fw, _ := zw.Create(archiveName)
						_, _ = fw.Write([]byte("data"))
					}
					_ = zw.Close()
					return
				}
				gz := gzip.NewWriter(w)
				tw := tar.NewWriter(gz)
				if tc.include {
					_ = tw.WriteHeader(&tar.Header{Name: archiveName, Mode: 0755, Size: 4, Typeflag: tar.TypeReg})
					_, _ = tw.Write([]byte("data"))
				}
				_ = tw.Close()
				_ = gz.Close()
			}))
			defer server.Close()
			old := urlOverride
			urlOverride = func() string { return server.URL }
			defer func() { urlOverride = old }()
			dest := filepath.Join(t.TempDir(), "caddy")
			err := FetchCaddy(context.Background(), dest)
			if (err != nil) != tc.wantErr {
				t.Fatalf("FetchCaddy err=%v", err)
			}
			if tc.wantErr {
				// Fail-closed: binary yang tidak terverifikasi tidak boleh tersisa.
				if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
					t.Fatalf("destination harus bersih setelah gagal, stat=%v", statErr)
				}
				return
			}
			b, readErr := os.ReadFile(dest)
			if readErr != nil || string(b) != "data" {
				t.Fatalf("binary=%q err=%v", b, readErr)
			}
			// Manifest hanya ditulis setelah pin cocok, dan isinya checksum
			// binary yang baru terpasang (cache install-time, Kontrak B).
			data, err := os.ReadFile(ManifestPath())
			if err != nil {
				t.Fatalf("manifest tidak ditulis setelah install: %v", err)
			}
			var m Manifest
			if err := json.Unmarshal(data, &m); err != nil {
				t.Fatalf("manifest rusak: %v", err)
			}
			if m.SHA256 != hex.EncodeToString(pin[:]) || m.Version != CaddyVersion {
				t.Fatalf("manifest salah: %+v", m)
			}
		})
	}
}

// Tanpa pin untuk pasangan OS/arch, FetchCaddy wajib menolak (fail-closed)
// dan tidak menulis manifest — tidak ada fallback silent.
func TestFetchCaddyFailsClosedWithoutPin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	oldPin := pinOverride
	pinOverride = func(_, _ string) (string, bool) { return "", false }
	defer func() { pinOverride = oldPin }()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gz := gzip.NewWriter(w)
		tw := tar.NewWriter(gz)
		_ = tw.WriteHeader(&tar.Header{Name: "caddy", Mode: 0755, Size: 4, Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte("data"))
		_ = tw.Close()
		_ = gz.Close()
	}))
	defer server.Close()
	old := urlOverride
	urlOverride = func() string { return server.URL }
	defer func() { urlOverride = old }()
	dest := filepath.Join(t.TempDir(), "caddy")
	err := FetchCaddy(context.Background(), dest)
	if err == nil {
		t.Fatal("tanpa pin harus fail-closed")
	}
	if !strings.Contains(err.Error(), "fail-closed") {
		t.Fatalf("pesan harus actionable: %v", err)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatal("destination harus bersih")
	}
	if _, statErr := os.Stat(ManifestPath()); !os.IsNotExist(statErr) {
		t.Fatal("manifest tidak boleh ditulis tanpa pin")
	}
}

func TestFetchCaddyFirstRunManagedAndFailClosed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	binName := "caddy"
	if stdruntime.GOOS == "windows" {
		binName += ".exe"
	}
	// VerifyManaged invokes `version`; marker proves bad pinned bytes never execute.
	marker := filepath.Join(t.TempDir(), "executed")
	if stdruntime.GOOS == "windows" {
		t.Skip("shell execution marker unavailable on Windows")
	}
	payload := []byte("#!/bin/sh\ntouch " + marker + "\necho 'v2.10.0 h1:test'\n")
	archive := func(content []byte) []byte {
		var b bytes.Buffer
		gz := gzip.NewWriter(&b)
		tw := tar.NewWriter(gz)
		_ = tw.WriteHeader(&tar.Header{Name: binName, Mode: 0755, Size: int64(len(content)), Typeflag: tar.TypeReg})
		_, _ = tw.Write(content)
		_ = tw.Close()
		_ = gz.Close()
		return b.Bytes()
	}
	serve := func(status int, body []byte) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status); _, _ = w.Write(body) }))
	}
	goodHash := sha256.Sum256(payload)
	oldPin := pinOverride
	defer func() { pinOverride = oldPin }()
	oldURL := urlOverride
	defer func() { urlOverride = oldURL }()
	dest := filepath.Join(t.TempDir(), binName)
	t.Run("success fetch then managed verification", func(t *testing.T) {
		s := serve(http.StatusOK, archive(payload))
		defer s.Close()
		urlOverride = func() string { return s.URL }
		pinOverride = func(_, _ string) (string, bool) { return hex.EncodeToString(goodHash[:]), true }
		if err := FetchCaddy(context.Background(), dest); err != nil {
			t.Fatal(err)
		}
		if err := VerifyManaged(dest); err != nil {
			t.Fatalf("VerifyManaged: %v", err)
		}
		if _, err := os.Stat(marker); err != nil {
			t.Fatalf("managed binary not executed: %v", err)
		}
	})
	for _, tc := range []struct {
		name   string
		status int
		data   []byte
		pin    string
	}{{"download failure", http.StatusBadGateway, nil, hex.EncodeToString(goodHash[:])}, {"pin mismatch", http.StatusOK, archive(payload), strings.Repeat("0", 64)}} {
		t.Run(tc.name, func(t *testing.T) {
			_ = os.Remove(dest)
			_ = os.Remove(ManifestPath())
			_ = os.Remove(marker)
			s := serve(tc.status, tc.data)
			defer s.Close()
			urlOverride = func() string { return s.URL }
			pinOverride = func(_, _ string) (string, bool) { return tc.pin, true }
			if err := FetchCaddy(context.Background(), dest); err == nil {
				t.Fatal("expected FetchCaddy failure")
			}
			if _, err := os.Stat(dest); !os.IsNotExist(err) {
				t.Fatalf("binary remains usable: %v", err)
			}
			if _, err := os.Stat(ManifestPath()); !os.IsNotExist(err) {
				t.Fatalf("manifest remains: %v", err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("unverified binary executed: %v", err)
			}
		})
	}
}

// Checksum binary hasil download yang tidak cocok pin → ditolak, file
// dihapus, manifest tidak ditulis (Kontrak B, hash-before-record).
func TestFetchCaddyRejectsPinMismatch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	oldPin := pinOverride
	pinOverride = func(_, _ string) (string, bool) { return "deadbeef", true }
	defer func() { pinOverride = oldPin }()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gz := gzip.NewWriter(w)
		tw := tar.NewWriter(gz)
		_ = tw.WriteHeader(&tar.Header{Name: "caddy", Mode: 0755, Size: 4, Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte("data"))
		_ = tw.Close()
		_ = gz.Close()
	}))
	defer server.Close()
	old := urlOverride
	urlOverride = func() string { return server.URL }
	defer func() { urlOverride = old }()
	dest := filepath.Join(t.TempDir(), "caddy")
	if err := FetchCaddy(context.Background(), dest); err == nil {
		t.Fatal("pin mismatch harus ditolak")
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatal("destination harus bersih setelah mismatch")
	}
	if _, statErr := os.Stat(ManifestPath()); !os.IsNotExist(statErr) {
		t.Fatal("manifest tidak boleh ditulis saat pin mismatch")
	}
}

// Race first-run concurrent (path EEXIST, deterministik tanpa goroutine):
// destination sudah ada → proses kedua wajib gagal actionable "sudah
// terpasang" TANPA menghapus binary existing milik proses pertama; manifest
// juga tidak boleh ditulis dari file yang tidak di-create proses ini.
func TestFetchCaddyEEXISTKeepsExistingBinary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	oldPin := pinOverride
	pin := sha256.Sum256([]byte("data"))
	pinOverride = func(_, _ string) (string, bool) { return hex.EncodeToString(pin[:]), true }
	defer func() { pinOverride = oldPin }()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gz := gzip.NewWriter(w)
		tw := tar.NewWriter(gz)
		_ = tw.WriteHeader(&tar.Header{Name: "caddy", Mode: 0755, Size: 4, Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte("data"))
		_ = tw.Close()
		_ = gz.Close()
	}))
	defer server.Close()
	old := urlOverride
	urlOverride = func() string { return server.URL }
	defer func() { urlOverride = old }()
	dest := filepath.Join(t.TempDir(), "caddy")
	// Simulasi binary terverifikasi milik "winner" yang datang lebih dulu.
	winner := []byte("winner-verified-binary")
	if err := os.WriteFile(dest, winner, 0755); err != nil {
		t.Fatal(err)
	}
	err := FetchCaddy(context.Background(), dest)
	if err == nil {
		t.Fatal("EEXIST harus ditolak, bukan diam-diam menimpa")
	}
	if !strings.Contains(err.Error(), "sudah terpasang") {
		t.Fatalf("pesan harus actionable: %v", err)
	}
	got, readErr := os.ReadFile(dest)
	if readErr != nil || !bytes.Equal(got, winner) {
		t.Fatalf("binary winner terhapus/berubah: content=%q err=%v", got, readErr)
	}
	if _, statErr := os.Stat(ManifestPath()); !os.IsNotExist(statErr) {
		t.Fatal("manifest tidak boleh ditulis dari file yang tidak di-create proses ini")
	}
}

// Tabel pin build-time harus diisi dan berformat sha256 hex 64 char untuk
// seluruh target rilis; pin hilang = platform tersebut fail-closed saat
// install (Kontrak B).
func TestCaddyBinaryPinsCoverReleaseTargets(t *testing.T) {
	for _, target := range [][2]string{
		{"linux", "amd64"}, {"linux", "arm64"},
		{"darwin", "amd64"}, {"darwin", "arm64"},
		{"windows", "amd64"}, {"windows", "arm64"},
	} {
		pin, ok := CaddyBinaryPin(target[0], target[1])
		if !ok {
			t.Errorf("pin %s/%s hilang dari tabel build-time", target[0], target[1])
			continue
		}
		if len(pin) != 64 {
			t.Errorf("pin %s/%s bukan sha256 hex 64 char: %q", target[0], target[1], pin)
		}
	}
}

func TestFetchCaddyHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := FetchCaddy(ctx, filepath.Join(t.TempDir(), "caddy")); err == nil {
		t.Fatal("expected canceled download error")
	}
}

func TestSelectCaddyPortKeepsAvailableConfiguredPort(t *testing.T) {
	port := freePort(t)
	got, err := SelectCaddyPort(port, 8000)
	if err != nil || got != port {
		t.Fatalf("got %d, %v; want configured port %d", got, err, port)
	}
}

func TestSelectCaddyPortFallsBackWhenPortIsBusy(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	_, portText, _ := net.SplitHostPort(l.Addr().String())
	port, _ := strconv.Atoi(portText)
	got, err := SelectCaddyPort(port, 8000)
	if err != nil {
		t.Fatal(err)
	}
	if got == port || got == 8000 || !portAvailable(got) {
		t.Fatalf("invalid fallback port %d", got)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	_, p, _ := net.SplitHostPort(l.Addr().String())
	port, _ := strconv.Atoi(p)
	return port
}

func TestWaitTLSReadyUsesExplicitServerNameWithoutTrustingLocalCA(t *testing.T) {
	server := httptest.NewTLSServer(nil)
	t.Cleanup(server.Close)
	_, portText, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}

	if err := WaitTLSReady(brand.Name+".test", port, time.Second); err != nil {
		t.Fatalf("WaitTLSReady() = %v", err)
	}
}

func TestWaitReadyOrProcessReturnsEarlyChildExit(t *testing.T) {
	done := make(chan ProcessResult, 1)
	done <- ProcessResult{Index: 1, Err: os.ErrProcessDone}

	result, err := WaitReadyOrProcess("127.0.0.1", 1, time.Second, done)
	if err != nil {
		t.Fatalf("WaitReadyOrProcess() error = %v", err)
	}
	if result == nil || result.Index != 1 || result.Err != os.ErrProcessDone {
		t.Fatalf("WaitReadyOrProcess() = %#v, want early process result", result)
	}
}

func TestCaddyPathFindsCheckoutStagingBinary(t *testing.T) {
	root := t.TempDir()
	name := "caddy_" + stdruntime.GOOS + "_" + stdruntime.GOARCH
	if stdruntime.GOOS == "windows" {
		name += ".exe"
	}
	want := filepath.Join(root, "tools", brand.Name, "dist", "bin", name)
	if err := os.MkdirAll(filepath.Dir(want), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(want, []byte("test"), 0755); err != nil {
		t.Fatal(err)
	}
	got, err := CaddyPath(root)
	if err != nil || got != want {
		t.Fatalf("CaddyPath() = %q, %v; want %q", got, err, want)
	}
}

func TestCaddyConfigIsLocalhostOnly(t *testing.T) {
	config := CaddyConfig(brand.Name+".test", "127.0.0.1:8000", 8080, true, "/tmp/certs")
	if !strings.Contains(config, "bind 127.0.0.1") || !strings.Contains(config, "reverse_proxy 127.0.0.1:8000") {
		t.Fatalf("unsafe or incomplete config: %s", config)
	}
	if !strings.Contains(config, "tls") && !strings.Contains(config, "https://") {
		t.Fatal("expected HTTPS config")
	}
	if !strings.Contains(config, "auto_https disable_redirects") {
		t.Fatal("HTTPS config must retain certificate automation without an implicit :80 redirect listener")
	}
	if !strings.Contains(config, "admin 127.0.0.1:2019") {
		t.Fatal("Caddy admin API must bind to the address used by the readiness probe")
	}
	if !strings.Contains(config, "https://"+brand.Name+".test:8080") {
		t.Fatal("HTTPS config must use an explicit HTTPS site address")
	}
	if strings.Contains(config, "auto_https off") {
		t.Fatal("auto_https off disables certificate automation")
	}
	if !strings.Contains(config, "header_up X-Forwarded-Proto https") || strings.Contains(config, "reverse_proxy https://") {
		t.Fatal("HTTPS client scheme must not turn the Laravel HTTP upstream into HTTPS")
	}
}

func TestCaddyConfigQuotesStoragePath(t *testing.T) {
	config := CaddyConfig(brand.Name+".test", "127.0.0.1:8000", 8080, true, "/tmp/caddy storage/issuer")
	if !strings.Contains(config, `storage file_system "/tmp/caddy storage/issuer"`) {
		t.Fatalf("storage path must be a single Caddyfile token: %s", config)
	}
}

// Config HTTP (virtual domain tanpa HTTPS) wajib memakai site address
// http:// eksplisit: tanpa itu Caddy menyalakan automatic HTTPS untuk domain
// .localhost/.test dan mencoba bind :80 untuk redirect — gagal non-root
// (ditemukan e2e sto:p2-e2e-smoke).
func TestCaddyConfigHTTPDisablesAutoHTTPS(t *testing.T) {
	config := CaddyConfig(brand.Name+".test", "127.0.0.1:8000", 8080, false, "/tmp/certs")
	if !strings.Contains(config, "http://"+brand.Name+".test:8080") {
		t.Fatalf("HTTP config must use an explicit http:// site address: %s", config)
	}
	if strings.Contains(config, "tls ") || strings.Contains(config, "https://") {
		t.Fatalf("HTTP config must not enable TLS: %s", config)
	}
	if !strings.Contains(config, "bind 127.0.0.1") || !strings.Contains(config, "reverse_proxy 127.0.0.1:8000") {
		t.Fatalf("unsafe or incomplete config: %s", config)
	}
}

func TestLocalDomainValidation(t *testing.T) {
	for _, domain := range []string{"localhost", "app.localhost", brand.Name + ".test"} {
		if !ValidLocalDomain(domain) {
			t.Errorf("expected valid %s", domain)
		}
	}
	if ValidLocalDomain("example.com") {
		t.Fatal("public domain must be rejected")
	}
}
