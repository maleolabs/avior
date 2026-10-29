package proxy

import (
	"net"
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
