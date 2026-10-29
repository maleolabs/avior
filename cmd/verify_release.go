package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/maleolabs/avior/internal/ui"
	"github.com/spf13/cobra"
)

// verifyReleaseCmd memverifikasi direktori artefak rilis terhadap
// SHA256SUMS co-located (Kontrak A — release-artifact): setiap entri
// manifest harus ada dan checksumnya cocok, dan setiap file di direktori
// (selain SHA256SUMS itu sendiri) harus terdaftar — entri hilang, digest
// mismatch, dan file ekstra semuanya gagal (fail-closed).
//
// Batas kejujuran: checksum unsigned hanya membuktikan INTEGRITAS (artefak
// utuh/konsisten dengan manifestnya), BUKAN autentisitas (bukti penerbit).
// Ia bukan pengganti tanda tangan digital.
var verifyReleaseCmd = &cobra.Command{
	Use:   "verify-release <direktori-rilis>",
	Short: "verifikasi arsip rilis terhadap SHA256SUMS (integritas, bukan autentisitas)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir := args[0]
		info, err := os.Stat(dir)
		if err != nil {
			return fmt.Errorf("direktori rilis tidak dapat diakses: %w", err)
		}
		if !info.IsDir() {
			return fmt.Errorf("bukan direktori: %s", dir)
		}
		issues, err := verifyReleaseDir(dir)
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		for _, issue := range issues {
			fmt.Fprintln(out, issue)
		}
		if len(issues) > 0 {
			return ui.NewReportedError(
				fmt.Errorf("verifikasi rilis gagal: %d masalah", len(issues)),
				fmt.Sprintf("Verifikasi SHA256SUMS gagal: %d masalah.\nPeriksa daftar di atas — integritas artefak tidak terpenuhi.\nCatatan: checksum unsigned menilai integritas, bukan autentisitas penerbit.", len(issues)))
		}
		fmt.Fprintln(out, "SHA256SUMS ok: seluruh entri cocok, tanpa file ekstra")
		return nil
	},
}

// sumsFile adalah nama manifest checksum yang ditulis job release CI
// (Kontrak A) di dalam direktori artefak.
const sumsFile = "SHA256SUMS"

// verifyReleaseDir murni: mengembalikan daftar masalah (kosong = lolos).
// Manifest rusak/path berbahaya mengembalikan error (bukan "masalah
// verifikasi") karena manifest tidak dapat dipercaya sebagai acuan.
func verifyReleaseDir(dir string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(dir, sumsFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%s tidak ada di %s — arsip tanpa manifest checksum tidak dapat diverifikasi", sumsFile, dir)
		}
		return nil, err
	}
	entries := make(map[string]string)
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || len(fields[0]) != 64 {
			return nil, fmt.Errorf("%s baris %d rusak: %q", sumsFile, i+1, line)
		}
		// Nama artefak relatif flat; tolak path absolut dan traversal.
		name := filepath.ToSlash(filepath.Clean(fields[1]))
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
			return nil, fmt.Errorf("%s baris %d memuat path tidak aman: %q", sumsFile, i+1, fields[1])
		}
		if _, dup := entries[name]; dup {
			return nil, fmt.Errorf("%s entri ganda: %s", sumsFile, name)
		}
		entries[name] = strings.ToLower(fields[0])
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("%s kosong", sumsFile)
	}
	var issues []string
	for name, want := range entries {
		got, err := fileSum(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			issues = append(issues, "entri hilang: "+name)
			continue
		}
		if got != want {
			issues = append(issues, fmt.Sprintf("digest tidak cocok: %s (got %s, want %s)", name, got, want))
		}
	}
	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		if name == sumsFile {
			return nil
		}
		if _, listed := entries[name]; !listed {
			issues = append(issues, "file ekstra (tidak terdaftar di "+sumsFile+"): "+name)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(issues) // deterministik: iterasi map tidak berurutan
	return issues, nil
}

// fileSum menghitung sha256 file secara streaming (hex lowercase, format
// sama dengan `sha256sum`).
func fileSum(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec -- path dari argumen user, read-only
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
