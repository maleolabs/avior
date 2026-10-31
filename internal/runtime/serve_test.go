package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maleolabs/avior/internal/mode"
	"github.com/maleolabs/avior/internal/proxy"
)

func serveOpts(mutate func(*ServeOptions)) ServeOptions {
	o := ServeOptions{
		Root:          "/tmp/serve-test-root",
		Mode:          mode.ModeProduction,
		Host:          "127.0.0.1",
		Port:          8000,
		VitePort:      5173,
		CaddyPort:     8080,
		Domain:        "app.localhost",
		VirtualDomain: true, // paritas default flag CLI production
		HTTPS:         true,
		TrustLocalCA:  true,
		Out:           &sink{},
	}
	if mutate != nil {
		mutate(&o)
	}
	return o
}

type sink struct{ b []byte }

func (s *sink) Write(p []byte) (int, error) { s.b = append(s.b, p...); return len(p), nil }

// Consent .test (review M-7): .test tanpa --hosts ditolak fail-closed.
func TestServeRejectsTestDomainWithoutHostsConsent(t *testing.T) {
	for _, domain := range []string{"app.test", "APP.TEST.", "app.test."} {
		o := serveOpts(func(o *ServeOptions) { o.Domain = domain })
		err := Serve(context.Background(), o)
		if err == nil || !contains(err.Error(), "--hosts") {
			t.Errorf("domain %q tanpa consent harus ditolak, dapat: %v", domain, err)
		}
	}
}

func TestServeValidationFailClosedBeforeSpawn(t *testing.T) {
	cases := []struct {
		name string
		o    ServeOptions
		want string
	}{
		{"port range", serveOpts(func(o *ServeOptions) { o.Port = 0 }), "port harus antara"},
		{"domain invalid", serveOpts(func(o *ServeOptions) { o.Domain = "evil.example.com" }), "localhost-only"},
		{"https tanpa trust", serveOpts(func(o *ServeOptions) { o.HTTPS = true; o.TrustLocalCA = false }), "trust-local-ca"},
		{"caddy port bentrok", serveOpts(func(o *ServeOptions) { o.CaddyPort = 8000 }), "berbeda"},
		{"mode invalid", serveOpts(func(o *ServeOptions) { o.Mode = mode.Mode("staging") }), "mode harus"},
		{"out wajib", func() ServeOptions { o := serveOpts(nil); o.Out = nil; return o }(), "Out wajib"},
	}
	for _, c := range cases {
		err := Serve(context.Background(), c.o)
		if err == nil || !contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want mengandung %q", c.name, err, c.want)
		}
	}
}

// HTTPS + trust di development (explicit) tetap harus lolos validasi consent —
// jangan sampai normalisasi membalik consent eksplisit.
func TestServeExplicitFlagsInDevelopmentNotNormalizedAway(t *testing.T) {
	o := serveOpts(func(o *ServeOptions) {
		o.Mode = mode.ModeDevelopment
		o.VirtualDomain, o.HTTPS, o.TrustLocalCA = true, true, true
		o.Changed = map[string]bool{"virtual-domain": true, "https": true, "trust-local-ca": true}
	})
	// Kegagalan berikutnya diharapkan dari resolve Caddy (tidak terpasang di
	// test env), BUKAN dari penurunan flag oleh normalisasi.
	err := Serve(context.Background(), o)
	if err != nil && contains(err.Error(), "HTTPS lokal membutuhkan") {
		t.Fatalf("flag eksplisit jangan dinormalisasi hilang: %v", err)
	}
}

// Serve dengan virtual domain dinonaktifkan: tanpa Caddy terpasang, runtime
// tetap harus mencoba launch php — di test env tanpa php/Laravel, kegagalan
// datang dari start php (bukan dari Caddy path).
func TestServeWithoutVirtualDomainSkipsCaddy(t *testing.T) {
	o := serveOpts(func(o *ServeOptions) {
		o.Mode = mode.ModeDevelopment
		o.VirtualDomain = false
	})
	start := time.Now()
	err := Serve(context.Background(), o)
	if err == nil {
		t.Fatal("serve tanpa php artisan harus gagal di test env")
	}
	if !contains(err.Error(), "php") {
		t.Fatalf("kegagalan harus dari child php, dapat: %v", err)
	}
	// Startup gagal cepat — bukan menunggu readiness timeout penuh.
	if elapsed := time.Since(start); elapsed > readinessTimeout {
		t.Fatalf("startup gagal terlalu lambat: %v", elapsed)
	}
}

func TestPrepareCaddyRequiresConsentBeforeDownload(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	oldFetch := fetchManagedCaddy
	defer func() { fetchManagedCaddy = oldFetch }()
	fetched := false
	fetchManagedCaddy = func(context.Context, string) error { fetched = true; return nil }
	o := serveOpts(func(o *ServeOptions) { o.NonInteractive = true })
	_, _, _, _, err := prepareCaddy(context.Background(), o, "app.localhost", false)
	if err == nil || !contains(err.Error(), "consent") {
		t.Fatalf("prepareCaddy error=%v, want consent gate", err)
	}
	if fetched {
		t.Fatal("download attempted without consent")
	}
}

func TestPrepareCaddyDeclinedConsentSkipsDownload(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	oldFetch := fetchManagedCaddy
	defer func() { fetchManagedCaddy = oldFetch }()
	fetched := false
	fetchManagedCaddy = func(context.Context, string) error { fetched = true; return nil }
	asked := 0
	o := serveOpts(func(o *ServeOptions) {
		o.ConfirmCaddyDownload = func() (bool, error) { asked++; return false, nil }
	})
	_, _, _, _, err := prepareCaddy(context.Background(), o, "app.localhost", false)
	if err == nil || !contains(err.Error(), "dibatalkan") {
		t.Fatalf("prepareCaddy error=%v, want declined download", err)
	}
	if asked != 1 || fetched {
		t.Fatalf("consent asked=%d fetched=%v, want asked=1 fetched=false", asked, fetched)
	}
}

func TestPrepareCaddyFirstRunUsesVerifiedManagedPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	oldFetch, oldVerify, oldValidate := fetchManagedCaddy, verifyManagedCaddy, validateCaddyConfig
	defer func() { fetchManagedCaddy, verifyManagedCaddy, validateCaddyConfig = oldFetch, oldVerify, oldValidate }()
	destination := ""
	verified := false
	fetchManagedCaddy = func(_ context.Context, path string) error {
		destination = path
		return os.MkdirAll(filepath.Dir(path), 0700)
	}
	verifyManagedCaddy = func(path string) error {
		if path != destination {
			t.Fatalf("verify path=%q, downloaded=%q", path, destination)
		}
		verified = true
		return nil
	}
	validateCaddyConfig = func(path, _ string) error {
		if path != destination {
			t.Fatalf("validate path=%q, downloaded=%q", path, destination)
		}
		return nil
	}
	o := serveOpts(func(o *ServeOptions) { o.AcceptCaddyDownload = true })
	svc, _, config, _, err := prepareCaddy(context.Background(), o, "app.localhost", false)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(config)
	if !verified || destination == "" || svc.Name != destination {
		t.Fatalf("managed first-run not verified: verified=%v destination=%q service=%q", verified, destination, svc.Name)
	}
}

func TestPrepareCaddyPromptAcceptDownloadsToManagedPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	oldFetch, oldVerify, oldValidate := fetchManagedCaddy, verifyManagedCaddy, validateCaddyConfig
	defer func() { fetchManagedCaddy, verifyManagedCaddy, validateCaddyConfig = oldFetch, oldVerify, oldValidate }()
	destination := ""
	fetchManagedCaddy = func(_ context.Context, path string) error {
		destination = path
		return os.MkdirAll(filepath.Dir(path), 0700)
	}
	verifyManagedCaddy = func(string) error { return nil }
	validateCaddyConfig = func(string, string) error { return nil }
	asked := 0
	o := serveOpts(func(o *ServeOptions) {
		o.ConfirmCaddyDownload = func() (bool, error) { asked++; return true, nil }
	})
	_, _, config, _, err := prepareCaddy(context.Background(), o, "app.localhost", false)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(config)
	if asked != 1 {
		t.Fatalf("consent asked=%d, want 1", asked)
	}
	if destination != proxy.ManagedCaddyPath() {
		t.Fatalf("download destination=%q, want managed path %q", destination, proxy.ManagedCaddyPath())
	}
}

func TestPrepareCaddyRejectsMissingPinBeforeDownload(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	oldFetch, oldPin := fetchManagedCaddy, caddyBinaryPin
	defer func() { fetchManagedCaddy, caddyBinaryPin = oldFetch, oldPin }()
	fetched := false
	fetchManagedCaddy = func(context.Context, string) error { fetched = true; return nil }
	caddyBinaryPin = func(string, string) (string, bool) { return "", false }
	o := serveOpts(func(o *ServeOptions) { o.AcceptCaddyDownload = true })
	_, _, _, _, err := prepareCaddy(context.Background(), o, "app.localhost", false)
	if err == nil || !contains(err.Error(), "tambahkan pin di internal/proxy/pins.go") {
		t.Fatalf("missing pin error=%v", err)
	}
	if fetched {
		t.Fatal("download attempted without build-time pin")
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

// Label TUI pakai vocab produk (review m4-serve-tui); subcommand artisan tak
// dikenal tetap label teknis.
func TestServiceLabelsUseProductNames(t *testing.T) {
	services := []Service{
		{Name: "php", Args: []string{"artisan", "serve"}},
		{Name: "php", Args: []string{"artisan", "schedule:work"}},
		{Name: "php", Args: []string{"artisan", "horizon"}},
		{Name: "php", Args: []string{"artisan", "pail"}},
		{Name: "npm", Args: []string{"run", "dev"}},
		{Name: "caddy", Args: []string{"run", "--config", "Caddyfile"}},
		{Name: "php", Args: []string{"artisan", "custom:cmd"}},
	}
	want := []string{"Laravel", "Scheduler", "Horizon", "Pail", "Vite", "Caddy", "custom:cmd"}
	got := serviceLabels(services)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("label[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
