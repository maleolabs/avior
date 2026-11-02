package proxy

import (
	"strings"
	"testing"
)

// TestCaddyConfigUsesAdminPort: config mengandung admin port eksplisit.
func TestCaddyConfigUsesAdminPort(t *testing.T) {
	config := CaddyConfigAdmin("app.test", "127.0.0.1:8000", 8080, true, "/tmp/certs", 2030)
	if !strings.Contains(config, "admin 127.0.0.1:2030") {
		t.Fatalf("config tidak memuat admin port 2030:\n%s", config)
	}
	// default path tetap 2019 untuk compat
	def := CaddyConfig("app.test", "127.0.0.1:8000", 8080, true, "/tmp/certs")
	if !strings.Contains(def, "admin 127.0.0.1:2019") {
		t.Fatalf("default config tidak memuat admin port 2019:\n%s", def)
	}
}

// TestSelectCaddyAdminPortAvoiding: port yang sudah dipakai proyek lain
// dihindari.
func TestSelectCaddyAdminPortAvoiding(t *testing.T) {
	used := map[int]bool{2019: true}
	port, err := SelectCaddyAdminPortAvoiding(2019, used)
	if err != nil {
		t.Fatal(err)
	}
	if port == 2019 {
		t.Fatal("admin port 2019 harus dihindari")
	}
	if port < 2020 || port > 2119 {
		t.Fatalf("admin port fallback di luar range: %d", port)
	}
	// configured bebas → dipakai langsung.
	got, err := SelectCaddyAdminPortAvoiding(2100, used)
	if err != nil || got != 2100 {
		t.Fatalf("admin port bebas = %d, want 2100 (err=%v)", got, err)
	}
}

// TestSelectCaddyPortAvoiding: port proyek lain dihindari.
func TestSelectCaddyPortAvoiding(t *testing.T) {
	used := map[int]bool{8080: true}
	port, err := SelectCaddyPortAvoiding(8080, 8000, used)
	if err != nil {
		t.Fatal(err)
	}
	if port == 8080 {
		t.Fatal("caddy port 8080 harus dihindari")
	}
	if port == 8000 {
		t.Fatal("caddy port tidak boleh sama dengan port Laravel")
	}
}
