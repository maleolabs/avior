package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/maleolabs/avior/internal/mode"
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

func contains(s, sub string) bool { return strings.Contains(s, sub) }
