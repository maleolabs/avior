package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/maleolabs/avior/internal/brand"
)

// trustFixture menyiapkan stub `caddy` di PATH (CaddyPath fallback LookPath)
// sehingga trustManager() bisa dibangun tanpa binary terkelola.
func trustFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "caddy"), []byte("#!/bin/sh\necho v2.10.0 stub\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir()) // state dir + manifest terisolasi
	return root
}

func TestTrustStatusWithoutInstall(t *testing.T) {
	root := trustFixture(t)
	out, err := runCLIStrip(t, "--root", root, "trust", "status")
	if err != nil {
		t.Fatalf("trust status: %v\n%s", err, out)
	}
	if !strings.Contains(out, "trust belum terpasang") {
		t.Errorf("status tidak melaporkan belum terpasang:\n%s", out)
	}
}

func TestTrustStatusJSON(t *testing.T) {
	root := trustFixture(t)
	out, err := runCLI(t, "--root", root, "trust", "status", "--json")
	if err != nil {
		t.Fatalf("trust status --json: %v\n%s", err, out)
	}
	var status struct {
		Fingerprint string `json:"fingerprint"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(out)), &status) != nil {
		t.Fatalf("status --json bukan JSON: %q", out)
	}
	if status.Fingerprint != "" {
		t.Errorf("fingerprint harus kosong sebelum install: %q", out)
	}
}

// Gate M-1: install/remove menolak fail-closed bila binary bukan hasil
// download terkelola (manifest tidak ada) — walau caddy ada di PATH.
func TestTrustInstallAndRemoveRefuseUnmanagedBinary(t *testing.T) {
	root := trustFixture(t)
	for _, args := range [][]string{
		{"--root", root, "trust", "install"},
		{"--root", root, "trust", "remove"},
	} {
		out, err := runCLIStrip(t, args...)
		if err == nil {
			t.Errorf("trust %v harus ditolak tanpa binary terkelola:\n%s", args, out)
			continue
		}
		if !strings.Contains(out+err.Error(), "tidak terverifikasi") && !strings.Contains(out+err.Error(), "manifest") {
			t.Errorf("penolakan tidak menyebut verifikasi: %v / %s", err, out)
		}
		if strings.Contains(out, "installed") {
			t.Errorf("trust terpasang padahal gate harus menolak:\n%s", out)
		}
	}
}

// TrustConfig harus memuat storage block milik tool — `caddy trust` tanpa
// storage block meng-trust CA storage default Caddy, bukan milik tool ini.
func TestTrustConfigPointsAtToolStorage(t *testing.T) {
	storage := filepath.Join(t.TempDir(), "caddy")
	path, err := trustConfig(storage)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	config := string(b)
	if !strings.Contains(config, "storage file_system") || !strings.Contains(config, strconv.Quote(storage)) {
		t.Errorf("config tidak menunjuk storage tool:\n%s", config)
	}
	if !strings.Contains(config, "localhost {\n\ttls internal\n}") {
		t.Errorf("config tidak memuat site tls internal:\n%s", config)
	}
}

// Binary terkelola + manifest cocok: gate lolos, `caddy trust` (stub) sukses,
// target opsional gagal terlapor tanpa mengubah keberhasilan Caddy.
func TestTrustInstallWithManagedBinaryRunsGate(t *testing.T) {
	root := trustFixture(t)
	stub := filepath.Join(root, "tools", brand.Name, "bin", "caddy_linux_amd64")
	if err := os.MkdirAll(filepath.Dir(stub), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho v2.10.0 stub managed\n"), 0755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(mustRead(t, stub)))
	if err := os.MkdirAll(brand.StateDir(), 0700); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(brand.StateDir(), "caddy-manifest.json")
	if err := os.WriteFile(manifest, []byte(`{"sha256":"`+hex.EncodeToString(sum[:])+`","version":"2.10.0"}`), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := runCLIStrip(t, "--root", root, "trust", "install")
	// Caddy target sukses; target opsional (NSS/Firefox) gagal karena stub
	// tidak mem-provision root CA — error tidak nil, tapi laporan target
	// harus tercetak.
	if !strings.Contains(out, "caddy: installed") {
		t.Fatalf("caddy target tidak terpasang: %v\n%s", err, out)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestTrustParentRejectsArgs(t *testing.T) {
	root := trustFixture(t)
	if _, err := runCLI(t, "--root", root, "trust", "bogus"); err == nil {
		t.Fatal("trust subcommand asing harus ditolak")
	}
}
