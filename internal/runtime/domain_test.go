package runtime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/maleolabs/avior/internal/state"
)

// TestSlugDomain: nama proyek → domain localhost-only yang aman.
func TestSlugDomain(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"feather", "feather.localhost"},
		{"My App!", "my-app.localhost"},
		{"blog_v2", "blog-v2.localhost"},
		{"  ", "app.localhost"},
		{"shop.example", "shop-example.localhost"},
		{"---", "app.localhost"},
	}
	for _, tt := range tests {
		if got := SlugDomain(tt.name); got != tt.want {
			t.Errorf("SlugDomain(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// TestUniqueDomainCollision: dua proyek nama sama → domain berbeda.
func TestUniqueDomainCollision(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(state.IndexPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	first, err := UniqueDomain("feather", "token-a")
	if err != nil {
		t.Fatal(err)
	}
	if first != "feather.localhost" {
		t.Fatalf("domain pertama = %q, want feather.localhost", first)
	}
	// Daftarkan proyek pertama ke index (seolah sedang berjalan).
	if err := state.UpsertProject(state.ProjectEntry{
		Token:  "token-a",
		Root:   "/home/a/feather",
		Name:   "feather",
		Domain: first,
	}); err != nil {
		t.Fatal(err)
	}
	second, err := UniqueDomain("feather", "token-b")
	if err != nil {
		t.Fatalf("collision tidak ditangani: %v", err)
	}
	if second == first {
		t.Fatalf("domain kedua = %q, harus berbeda dari %q", second, first)
	}
	if second != "feather-toke.localhost" {
		t.Fatalf("domain kedua = %q, want feather-toke.localhost", second)
	}
}

// TestUniqueDomainSelfExcluded: proyek sendiri boleh mempertahankan domainnya.
func TestUniqueDomainSelfExcluded(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	if err := state.UpsertProject(state.ProjectEntry{
		Token:  "token-a",
		Root:   "/home/a/feather",
		Name:   "feather",
		Domain: "feather.localhost",
	}); err != nil {
		t.Fatal(err)
	}
	got, err := UniqueDomain("feather", "token-a")
	if err != nil {
		t.Fatal(err)
	}
	if got != "feather.localhost" {
		t.Fatalf("domain sendiri = %q, want feather.localhost", got)
	}
}

// TestUniqueDomainExhausted: suffix collision juga dipakai → error jelas.
func TestUniqueDomainExhausted(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	if err := state.UpsertProject(state.ProjectEntry{Token: "token-a", Root: "/a", Name: "feather", Domain: "feather.localhost"}); err != nil {
		t.Fatal(err)
	}
	if err := state.UpsertProject(state.ProjectEntry{Token: "token-b", Root: "/b", Name: "feather", Domain: "feather-toke.localhost"}); err != nil {
		t.Fatal(err)
	}
	// token-c: feather.localhost (a) + feather-toke.localhost (b) dipakai,
	// suffix token-c juga collide dengan "feather-toke" (b) → error.
	_, err := UniqueDomain("feather", "token-c")
	if err == nil {
		t.Fatal("domain habis harus error")
	}
}
