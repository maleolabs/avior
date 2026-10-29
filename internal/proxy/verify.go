// Package proxy adalah layer Caddy (arc:avior-architecture): path resolver,
// Caddyfile generator, port selection, dan verifikasi binary. File ini
// hanya memuat verifikasi; resolver/generator menyusul di package yang sama.
//
// Verifikasi adalah construction invariant (review finding H3): binary
// Caddy yang akan dipakai — khususnya sebelum `caddy trust` yang berjalan
// di bawah sudo — WAJIB lolos VerifyManaged. Predecessor menjalankan
// `caddy trust` pada binary apa pun yang di-resolve PATH tanpa verifikasi;
// binary tak terverifikasi/malicious bisa dieksekusi sebagai root.
package proxy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/maleolabs/avior/internal/brand"
)

// CaddyVersion adalah versi binary Caddy yang dipin. Dipakai bersama oleh
// verifikasi (checkVersion) dan nanti oleh layer download (checksum
// manifest HARUS ditulis dari pin yang sama).
const CaddyVersion = "2.10.0"

// Manifest adalah isi caddy-manifest.json di brand.StateDir(), ditulis
// saat install (proxy.FetchCaddy) HANYA setelah hasil download cocok dengan
// pin checksum build-time (Kontrak B, internal/proxy/pins.go) — manifest
// adalah cache install-time, bukan root of trust. Format minimal JSON tanpa
// signature: ditulis oleh tool itu sendiri di user dir milik user.
// ponytail: manifest masih bisa ditimpa attacker dengan akses tulis ke
// StateDir; mitigasi jalur install = pin build-time. Upgrade path bila
// dibutuhkan: tandatangani manifest.
type Manifest struct {
	SHA256  string `json:"sha256"`
	Version string `json:"version"` // tidak divalidasi di readManifest; checksum adalah binding
}

// ManifestPath mengembalikan lokasi manifest checksum binary di
// brand.StateDir().
func ManifestPath() string {
	return filepath.Join(brand.StateDir(), "caddy-manifest.json")
}

// VerifyManaged adalah gate ketat: checksum sha256 binary HARUS match
// manifest yang tercatat saat install, dan versi binary HARUS match pin.
// Pemanggil sudo (`caddy trust`) dan pemakaian binary terkelola lainnya
// WAJIB lewat sini. Manifest hilang/rusak/checksum beda = refuse (fail
// closed).
//
// Urutan hash-before-exec: checksum diverifikasi SEBELUM binary dieksekusi
// (checkVersion) — binary tak terverifikasi/tampered tidak pernah dijalankan
// selama verifikasi itu sendiri.
//
// Kontrak TOCTOU untuk pemanggil sudo: VerifyManaged HARUS dipanggil
// SEGERA sebelum exec sudo (`caddy trust`), tanpa operasi tulis file di
// antara keduanya — binary/manifest bisa ditimpa di jendela gate-to-exec.
func VerifyManaged(binPath string) error {
	m, err := readManifest()
	if err != nil {
		return err
	}
	sum, err := fileSHA256(binPath)
	if err != nil {
		return err
	}
	if sum != m.SHA256 {
		return fmt.Errorf("proxy: checksum %s tidak cocok dengan manifest (H3): got %s want %s", binPath, sum, m.SHA256)
	}
	return checkVersion(binPath)
}

// VerifyPATH adalah fallback dev-only untuk binary yang di-resolve dari
// PATH (bukan milik tool): hanya cek versi. TIDAK BOLEH dipakai sebagai
// gate `caddy trust` — nama sengaja dibuat eksplisit agar tidak tertukar
// dengan VerifyManaged. Pemanggil CLI wajib menampilkan warning dev saat
// jatuh ke path ini (warning ditangani di layer command, bukan di sini).
func VerifyPATH(binPath string) error {
	return checkVersion(binPath)
}

// checkVersion menjalankan `<binPath> version` (args-list, tanpa shell,
// timeout 5s) dan menuntut output memuat versi yang dipin via
// versionMatches.
func checkVersion(binPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, binPath, "version").Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return fmt.Errorf("proxy: jalankan %q version: %w (stderr: %s)", binPath, err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return fmt.Errorf("proxy: jalankan %q version: %w", binPath, err)
	}
	if versionMatches(string(out)) {
		return nil
	}
	return fmt.Errorf("proxy: versi %s tidak match pin %s (output: %q)", binPath, CaddyVersion, strings.TrimSpace(string(out)))
}

// versionMatches menuntut output memuat token versi dengan prefix pin.
// Yang dipin adalah major.minor ("v2.10."); patch bebas mengikuti rilis
// pin agar patch keamanan upstream tidak memaksa bump konstanta.
// ponytail: kalau mau pin patch exact, ganti prefix match dengan
// perbandingan token penuh "v"+CaddyVersion.
func versionMatches(output string) bool {
	prefix := "v" + CaddyVersion[:strings.LastIndex(CaddyVersion, ".")+1]
	for _, field := range strings.Fields(output) {
		if strings.HasPrefix(field, prefix) {
			return true
		}
	}
	return false
}

// readManifest membaca dan memvalidasi manifest checksum. Fail closed:
// hilang, rusak, atau tanpa sha256 = error.
func readManifest() (Manifest, error) {
	path := ManifestPath()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Manifest{}, fmt.Errorf("proxy: manifest %s tidak ada — binary bukan hasil download terkelola; jalankan install ulang", path)
	}
	if err != nil {
		return Manifest{}, fmt.Errorf("proxy: baca manifest %s: %w", path, err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("proxy: decode manifest %s: %w", path, err)
	}
	if m.SHA256 == "" {
		return Manifest{}, fmt.Errorf("proxy: manifest %s tanpa sha256", path)
	}
	return m, nil
}

// fileSHA256 menghitung checksum sha256 file (hex lowercase, sama format
// dengan `sha256sum`).
func fileSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("proxy: baca %s: %w", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
