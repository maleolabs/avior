// Package version adalah sumber tunggal versi CLI. Versi menentukan tag
// release yang dipakai saat mengunduh resource distribusi (binary CLI yang
// dibundle ke arsip produksi — lihat docs/archive-release-distribution-plan.md),
// jadi tidak boleh ada sumber kedua di luar paket ini.
//
// Urutan resolusi (yang paling spesifik menang):
//
//  1. ldflags -X <brand.ModulePath>/internal/version.version — build
//     rilis CI: tag menjadi nilai eksplisit, paling otoritatif.
//  2. File VERSION di root modul — build lokal dari checkout (go build /
//     go test: binary/cwd berada di dalam tree modul).
//  3. debug.ReadBuildInfo vcs.revision — diagnostik build dari checkout
//     tanpa ldflags; label pendek, BUKAN tag rilis yang bisa diunduh.
//  4. "0.0.0-dev" — fallback terakhir.
//
// Konsekuensi fail-closed: versi yang bukan tag rilis membuat pengunduhan
// resource release gagal dengan error actionable (tidak pernah diam-diam
// mengambil tag lain — plan D2: reproduksibilitas arsip).
package version

import (
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
)

// version diisi lewat ldflags -X pada build rilis CI.
var version string

// devVersion adalah fallback ketika tidak ada sumber versi sama sekali.
const devVersion = "0.0.0-dev"

// maxWalkUp membatasi pencarian file VERSION ke atas — mencegah scan
// seluruh filesystem pada layout direktori yang dalam.
const maxWalkUp = 5

// Version mengembalikan versi CLI (tanpa prefix "v").
func Version() string {
	if v := strings.TrimSpace(version); v != "" {
		return strings.TrimPrefix(v, "v")
	}
	if v, ok := versionFile(); ok {
		return v
	}
	if v, ok := buildInfo(); ok {
		return v
	}
	return devVersion
}

// Tag mengembalikan tag release GitHub untuk versi ini ("v" + Version).
func Tag() string {
	return "v" + Version()
}

// IsRelease melaporkan apakah Version berformat tag rilis yang valid
// (X.Y.Z atau X.Y.Z-prerelease). Versi dev (0.0.0-dev / sha pendek) tidak
// punya release binary — pemanggil wajib menolak pengunduhan, bukan
// mencoba-coba.
func IsRelease() bool {
	return isReleaseVersion(Version())
}

// isReleaseVersion mengevaluasi string versi terhadap format tag rilis.
// Dipisah agar test dapat menguji kasus tanpa memanipulasi var paket.
// Suffix "-dev" ditolak eksplisit: devVersion ("0.0.0-dev") dan label
// build ("1.2.3-dev") boleh formatnya mirip tag, tapi tidak punya release.
func isReleaseVersion(v string) bool {
	base, suffix, _ := strings.Cut(v, "-")
	if suffix == "dev" {
		return false
	}
	nums := strings.Split(base, ".")
	if len(nums) != 3 {
		return false
	}
	for _, n := range nums {
		if _, err := strconv.Atoi(n); err != nil {
			return false
		}
	}
	return true
}

// versionFile mencari file VERSION di root modul: dari direktori binary
// (build lokal menaruh binary di dalam tree modul) dan dari cwd, masing
// satu naik ke atas paling banyak maxWalkUp level.
func versionFile() (string, bool) {
	var bases []string
	if exe, err := os.Executable(); err == nil {
		if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
			exe = resolved
		}
		bases = append(bases, filepath.Dir(exe))
	}
	if cwd, err := os.Getwd(); err == nil {
		bases = append(bases, cwd)
	}
	for _, base := range bases {
		dir := base
		for i := 0; i < maxWalkUp; i++ {
			data, err := os.ReadFile(filepath.Join(dir, "VERSION"))
			if err == nil {
				if v := strings.TrimSpace(string(data)); v != "" {
					return strings.TrimPrefix(v, "v"), true
				}
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return "", false
}

// buildInfo mengekstrak commit pendek dari info build VCS (build dari
// checkout tanpa ldflags dan tanpa file VERSION). Hanya label — lihat
// doc paket: sha commit bukan tag rilis.
func buildInfo() (string, bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", false
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" && len(setting.Value) >= 7 {
			return "dev-" + setting.Value[:7], true
		}
	}
	return "", false
}
