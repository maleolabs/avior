package brand_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stripSkipped membuang substring yang diizinkan secara eksplisit sehingga
// literal lain yang berdekatan di baris yang sama tetap terdeteksi:
//   - import path modul ("github.com/maleolabs/avior/...") — identitas module
//     Go; satu-satunya literal yang diizinkan di luar brand (perubahan module
//     path = go.mod + seluruh import, keputusan terpisah dari ADR brand).
//   - tag arsitektur "arc:avior-architecture" — pengenal pengetahuan EKA,
//     bukan identitas runtime.
func stripSkipped(line string) string {
	s := strings.ReplaceAll(line, `"github.com/maleolabs/avior`, "")
	s = strings.ReplaceAll(s, "arc:avior-architecture", "")
	return strings.ReplaceAll(s, "internal/brand.ManifestFile", "")
}

// isBrandLiteral melaporkan apakah baris (setelah strip) memuat literal brand.
func isBrandLiteral(line string) bool {
	low := strings.ToLower(stripSkipped(line))
	return strings.Contains(low, "simpskba") || strings.Contains(low, "sim-pskba") || strings.Contains(low, "avior")
}

// TestNoBrandLiteralOutsideBrand memindai semua file Go di luar internal/brand
// dan gagal jika ada literal "simpskba"/"avior" (case-insensitive) di luar
// dua pengecualian stripSkipped. Allowlist kosong sejak p2-rename-sweep:
// seluruh referensi identitas (termasuk fixture test) mengonsumsi
// internal/brand (ADR: adr:brand-contract).
func TestNoBrandLiteralOutsideBrand(t *testing.T) {
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := filepath.ToSlash(path)
		for strings.HasPrefix(rel, "../") {
			rel = rel[3:]
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".eka", "EKA", "exchange":
				return filepath.SkipDir
			}
			if rel == "internal/brand" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasPrefix(rel, "internal/brand/") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(data), "\n") {
			if !isBrandLiteral(line) {
				continue
			}
			t.Errorf("literal brand di luar internal/brand: %s:%d: %s",
				rel, i+1, strings.TrimSpace(stripSkipped(line)))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}
