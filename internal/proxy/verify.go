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
// saat binary selesai di-download/terpasang (install time), bukan saat
// verifikasi. Format minimal JSON tanpa signature — manifest dianggap
// tepercaya karena ditulis oleh tool itu sendiri di user dir milik user.
// ponytail: belum ada signature verification (attacker dengan akses tulis
// ke StateDir bisa menimpa manifest); upgrade path: tandatangani manifest
// atau bandingkan checksum terhadap nilai tertanam saat build.
type Manifest struct {
	SHA256  string `json:"sha256"`
	Version string `json:"version"`
}

// ManifestPath mengembalikan lokasi manifest checksum binary di
// brand.StateDir().
func ManifestPath() string {
	return filepath.Join(brand.StateDir(), "caddy-manifest.json")
}

// VerifyManaged adalah gate ketat: versi binary HARUS match pin dan
// checksum sha256 binary HARUS match manifest yang tercatat saat install.
// Pemanggil sudo (`caddy trust`) dan pemakaian binary terkelola lainnya
// WAJIB lewat sini. Manifest hilang/rusak/checksum beda = refuse (fail
// closed).
func VerifyManaged(binPath string) error {
	if err := checkVersion(binPath); err != nil {
		return err
	}
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
	return nil
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
// timeout 5s) dan menuntut output memuat versi yang dipin. Yang dipin
// adalah major.minor ("v2.10."); patch bebas mengikuti rilis pin agar
// patch keamanan upstream tidak memaksa bump konstanta. ponytail: kalau
// mau pin patch exact, ganti prefix match di versionMatches dengan
// perbandingan token penuh "v"+CaddyVersion.
func checkVersion(binPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, binPath, "version").Output()
	if err != nil {
		return fmt.Errorf("proxy: jalankan %q version: %w", binPath, err)
	}
	prefix := "v" + CaddyVersion[:strings.LastIndex(CaddyVersion, ".")+1]
	for _, field := range strings.Fields(string(out)) {
		if strings.HasPrefix(field, prefix) {
			return nil
		}
	}
	return fmt.Errorf("proxy: versi %s tidak match pin %s (output: %q)", binPath, CaddyVersion, strings.TrimSpace(string(out)))
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
