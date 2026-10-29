package trust

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/maleolabs/avior/internal/brand"
)

func TestBundledCertutilVerifiesManifest(t *testing.T) {
	dir := t.TempDir()
	nss := filepath.Join(dir, "nss")
	_ = os.Mkdir(nss, 0700)
	p := filepath.Join(nss, "certutil")
	data := []byte("helper")
	if err := os.WriteFile(p, data, 0700); err != nil {
		t.Fatal(err)
	}
	s := sha256.Sum256(data)
	m := nssManifest{Helpers: map[string]nssHelper{runtime.GOOS + "/" + runtime.GOARCH: {Path: "certutil", SHA256: hex.EncodeToString(s[:])}}}
	b, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(nss, "MANIFEST.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := bundledCertutil(filepath.Join(dir, brand.Name))
	if err != nil || got != p {
		t.Fatalf("got %q, %v; want %q", got, err, p)
	}
	data[0]++
	if err := os.WriteFile(p, data, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := bundledCertutil(filepath.Join(dir, brand.Name)); err == nil {
		t.Fatal("expected checksum failure")
	}
}

func TestBundledCertutilUsesExplicitResourceDirectory(t *testing.T) {
	dir := t.TempDir()
	nss := filepath.Join(dir, "resources", "nss")
	if err := os.MkdirAll(nss, 0700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(nss, "certutil")
	data := []byte("helper")
	if err := os.WriteFile(p, data, 0700); err != nil {
		t.Fatal(err)
	}
	s := sha256.Sum256(data)
	m := nssManifest{Helpers: map[string]nssHelper{runtime.GOOS + "/" + runtime.GOARCH: {Path: "certutil", SHA256: hex.EncodeToString(s[:])}}}
	b, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(nss, "MANIFEST.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(resourceDirEnv(), filepath.Join(dir, "resources"))
	got, err := bundledCertutil(filepath.Join(dir, "bin", brand.Name))
	if err != nil || got != p {
		t.Fatalf("got %q, %v; want %q", got, err, p)
	}
}

func TestBundledCertutilUsesRuntimeRoot(t *testing.T) {
	dir := t.TempDir()
	nss := filepath.Join(dir, "tools", brand.Name, "nss")
	if err := os.MkdirAll(nss, 0700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(nss, "certutil")
	data := []byte("helper from runtime root")
	if err := os.WriteFile(p, data, 0700); err != nil {
		t.Fatal(err)
	}
	s := sha256.Sum256(data)
	m := nssManifest{Helpers: map[string]nssHelper{runtime.GOOS + "/" + runtime.GOARCH: {Path: "certutil", SHA256: hex.EncodeToString(s[:])}}}
	b, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(nss, "MANIFEST.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(resourceDirEnv(), "")

	got, err := bundledCertutil(filepath.Join(t.TempDir(), "bin", brand.Name), dir)
	if err != nil || got != p {
		t.Fatalf("got %q, %v; want %q", got, err, p)
	}
}
