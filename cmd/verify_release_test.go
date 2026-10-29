package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maleolabs/avior/internal/brand"
)

// writeReleaseDir membangun direktori artefak rilis sintetis: file-file
// artefak + SHA256SUMS yang benar. File `.data` dipakai sebagai pengganti
// arsip — verify-release hanya peduli byte + manifest.
func writeReleaseDir(t *testing.T, extra string) string {
	t.Helper()
	dir := t.TempDir()
	sums := ""
	for _, name := range []string{brand.Name + "-v1-linux-amd64.tar.gz", brand.Name + "-v1-windows-amd64.zip"} {
		content := []byte("payload-" + name)
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(content)
		sums += hex.EncodeToString(sum[:]) + "  " + name + "\n"
	}
	sums += extra
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(sums), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Jalur sukses: seluruh entri cocok, tanpa file ekstra.
func TestVerifyReleaseOK(t *testing.T) {
	dir := writeReleaseDir(t, "")
	out, err := runCLIStrip(t, "verify-release", dir)
	if err != nil {
		t.Fatalf("verify-release gagal pada direktori konsisten: %v\n%s", err, out)
	}
	if !strings.Contains(out, "SHA256SUMS ok") {
		t.Fatalf("output tidak menyatakan sukses: %s", out)
	}
}

// Entri manifest hilang dari disk → gagal (fail-closed).
func TestVerifyReleaseMissingEntry(t *testing.T) {
	dir := writeReleaseDir(t, "")
	if err := os.Remove(filepath.Join(dir, brand.Name+"-v1-linux-amd64.tar.gz")); err != nil {
		t.Fatal(err)
	}
	out, err := runCLIStrip(t, "verify-release", dir)
	if err == nil {
		t.Fatalf("entri hilang harus gagal:\n%s", out)
	}
	if !strings.Contains(out, "entri hilang: "+brand.Name+"-v1-linux-amd64.tar.gz") {
		t.Fatalf("masalah entri hilang tidak dilaporkan:\n%s", out)
	}
}

// Digest tidak cocok (artefak dimodifikasi) → gagal.
func TestVerifyReleaseDigestMismatch(t *testing.T) {
	dir := writeReleaseDir(t, "")
	if err := os.WriteFile(filepath.Join(dir, brand.Name+"-v1-windows-amd64.zip"), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runCLIStrip(t, "verify-release", dir)
	if err == nil {
		t.Fatalf("digest mismatch harus gagal:\n%s", out)
	}
	if !strings.Contains(out, "digest tidak cocok: "+brand.Name+"-v1-windows-amd64.zip") {
		t.Fatalf("mismatch tidak dilaporkan:\n%s", out)
	}
}

// File di direktori yang tidak terdaftar di manifest → gagal.
func TestVerifyReleaseExtraFile(t *testing.T) {
	dir := writeReleaseDir(t, "")
	if err := os.WriteFile(filepath.Join(dir, "extra.bin"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runCLIStrip(t, "verify-release", dir)
	if err == nil {
		t.Fatalf("file ekstra harus gagal:\n%s", out)
	}
	if !strings.Contains(out, "file ekstra") || !strings.Contains(out, "extra.bin") {
		t.Fatalf("file ekstra tidak dilaporkan:\n%s", out)
	}
}

// Manifest hilang / rusak / berbahaya = error manifest, bukan sekadar issue.
func TestVerifyReleaseManifestErrors(t *testing.T) {
	t.Run("manifest hilang", func(t *testing.T) {
		_, err := runCLIStrip(t, "verify-release", t.TempDir())
		if err == nil || !strings.Contains(err.Error(), "tidak dapat diverifikasi") {
			t.Fatalf("manifest hilang harus error: %v", err)
		}
	})
	t.Run("path traversal", func(t *testing.T) {
		dir := writeReleaseDir(t, strings.Repeat("a", 64)+"  ../escape\n")
		_, err := runCLIStrip(t, "verify-release", dir)
		if err == nil || !strings.Contains(err.Error(), "path tidak aman") {
			t.Fatalf("traversal harus ditolak: %v", err)
		}
	})
	t.Run("baris rusak", func(t *testing.T) {
		dir := writeReleaseDir(t, "zzz\n")
		_, err := runCLIStrip(t, "verify-release", dir)
		if err == nil || !strings.Contains(err.Error(), "baris 3 rusak") {
			t.Fatalf("baris rusak harus error: %v", err)
		}
	})
}

// Argumen bukan direktori → error bersih.
func TestVerifyReleaseRejectsNonDirectory(t *testing.T) {
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runCLIStrip(t, "verify-release", file); err == nil {
		t.Fatal("argumen file harus ditolak")
	}
}
