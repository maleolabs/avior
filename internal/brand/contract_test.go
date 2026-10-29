package brand_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// File-file yang masih memuat literal identitas dan DIBERSIHKAN oleh item
// p2-rename-sweep. JANGAN menambah entri baru; literal baru di file lain
// membuat test ini gagal (ADR: adr:brand-contract).
var sweepAllowlist = map[string]string{
	// Komentar historis "diport dari simpskba" — hanya komentar, bukan string runtime.
	"internal/detect/detect.go": "komentar historis simpskba",
	"internal/env/env.go":       "komentar historis simpskba",
	"internal/setup/setup.go":   "komentar historis simpskba",
	"internal/ui/report.go":     "komentar historis simpskba",
	// Komentar roadmap di entrypoint; string runtime sudah pakai brand.Name.
	"main.go": "komentar roadmap",
	// Komentar path release <release>/tools/avior — sweep.
	"internal/trust/nss.go": "komentar path release",
	// Fixture test (domain/binary/env-var) yang menunggu sweep.
	"internal/env/env_test.go":                "fixture APP_NAME",
	"internal/hostname/hosts_test.go":         "domain fixture avior.test",
	"internal/processgroup/supervisor_test.go": "nama binary fixture",
	"internal/proxy/proxy_test.go":            "domain fixture avior.test",
	"internal/proxy/verify_test.go":           "isi file fixture",
	"internal/trust/nss_test.go":              "env AVIOR_RESOURCE_DIR + path fixture",
}

// TestNoBrandLiteralOutsideBrand memindai semua file Go di luar internal/brand
// dan gagal jika ada literal "simpskba"/"avior" (case-insensitive) di luar
// allowlist sweep. Baris yang di-skip secara eksplisit (juga target sweep):
//   - import path modul ("github.com/maleolabs/avior/...") — ikut go.mod saat sweep.
//   - tag arsitektur "arc:avior-architecture" — pengenal pengetahuan EKA.
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
		for i, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, `"github.com/maleolabs/avior`) ||
				strings.Contains(line, "arc:avior-architecture") {
				continue
			}
			low := strings.ToLower(line)
			if !strings.Contains(low, "simpskba") && !strings.Contains(low, "avior") {
				continue
			}
			if reason, ok := sweepAllowlist[rel]; ok {
				t.Logf("allowlist (p2-rename-sweep) %s:%d: %s", rel, i+1, reason)
				continue
			}
			t.Errorf("literal brand di luar internal/brand: %s:%d: %s",
				rel, i+1, strings.TrimSpace(line))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}
