package brand_test

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// File-file yang masih memuat literal identitas dan DIBERSIHKAN oleh item
// p2-rename-sweep. Nilai = jumlah BARIS literal yang dipin saat ini; test
// gagal jika jumlahnya melebihi pin. JANGAN menambah entri baru; saat sweep,
// turunkan count ke 0 lalu hapus entrinya (ADR: adr:brand-contract).
var sweepAllowlist = map[string]int{
	// Komentar historis "diport dari simpskba" — hanya komentar, bukan string runtime.
	"internal/detect/detect.go": 1,
	"internal/env/env.go":       3,
	"internal/setup/setup.go":   2,
	"internal/ui/report.go":     1,
	// Komentar roadmap di entrypoint; string runtime sudah pakai brand.Name.
	"main.go": 2,
	// Komentar path release <release>/tools/avior — sweep.
	"internal/trust/nss.go": 2,
	// Fixture test (domain/binary/env-var) yang menunggu sweep.
	"internal/env/env_test.go":                 2,
	"internal/hostname/hosts_test.go":          5,
	"internal/processgroup/supervisor_test.go": 1,
	"internal/proxy/proxy_test.go":             6,
	"internal/proxy/verify_test.go":            2,
	"internal/trust/nss_test.go":               7,
}

// stripSkipped membuang substring yang di-skip eksplisit (target sweep juga)
// sehingga literal lain yang berdekatan di baris yang sama tetap terdeteksi:
//   - import path modul ("github.com/maleolabs/avior/...") — ikut go.mod saat sweep.
//   - tag arsitektur "arc:avior-architecture" — pengenal pengetahuan EKA.
func stripSkipped(line string) string {
	s := strings.ReplaceAll(line, `"github.com/maleolabs/avior`, "")
	return strings.ReplaceAll(s, "arc:avior-architecture", "")
}

// isBrandLiteral melaporkan apakah baris (setelah strip) memuat literal brand.
func isBrandLiteral(line string) bool {
	low := strings.ToLower(stripSkipped(line))
	return strings.Contains(low, "simpskba") || strings.Contains(low, "avior")
}

// TestNoBrandLiteralOutsideBrand memindai semua file Go di luar internal/brand
// dan gagal jika ada literal "simpskba"/"avior" (case-insensitive) di luar
// allowlist sweep, atau jika jumlah literal di file allowlisted melebihi pin.
func TestNoBrandLiteralOutsideBrand(t *testing.T) {
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".eka", "EKA", "exchange":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel := filepath.ToSlash(path)
		for strings.HasPrefix(rel, "../") {
			rel = rel[3:]
		}
		if rel == "internal/brand" || strings.HasPrefix(rel, "internal/brand/") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var hits []string
		for i, line := range strings.Split(string(data), "\n") {
			if !isBrandLiteral(line) {
				continue
			}
			hits = append(hits, fmt.Sprintf("%s:%d: %s", rel, i+1, strings.TrimSpace(line)))
		}
		if len(hits) == 0 {
			return nil
		}
		if expected, ok := sweepAllowlist[rel]; ok {
			if len(hits) <= expected {
				return nil
			}
			t.Errorf("literal brand di %s: %d baris, melebihi pin %d (p2-rename-sweep): %v",
				rel, len(hits), expected, hits[expected:])
			return nil
		}
		for _, h := range hits {
			t.Errorf("literal brand di luar internal/brand: %s", h)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}
