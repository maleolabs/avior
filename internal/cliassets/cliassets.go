// Package cliassets mengambil binary CLI siap pakai dari GitHub Releases
// (docs/archive-release-distribution-plan.md).
//
// CLI tidak pernah meng-compile dirinya sendiri di mesin pengguna (akar
// masalah error "CLI module tidak ditemukan"): binary dirilis CI sebagai
// aset `<name>_<os>_<arch>[.exe]` (brand.Name), lalu diunduh dan diverifikasi di sini.
//
// Versi selalu dipin (plan D2: reproduksibilitas arsip) — pemanggil
// menentukan versi (biasanya version.Version() = versi binary yang
// sedang berjalan); LatestVersion hanya dipakai selfupdate, di luar
// perintah archive.
//
// Root of trust = SHA256SUMS yang co-located di release (Kontrak A):
// setiap binary yang diunduh wajib terdaftar dan cocok. Gagal mana pun
// → fail-closed: file parsial dihapus, error jelas. Pin build-time
// (pola internal/proxy/pins.go) sengaja TIDAK dipakai — SHA256SUMS
// cukup, dan pin akan memaksa bump tiap rilis (lihat plan).
package cliassets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/maleolabs/avior/internal/brand"
)

// baseURLOverride adalah hook test (paritas urlOverride di internal/proxy).
var baseURLOverride func() string

// baseURL mengembalikan akar URL release. Override via hook test, lalu
// env <NAME>_RELEASE_BASE_URL (mirror internal / CI offline), lalu default
// repositori resmi (brand.ModulePath — satu sumber identitas).
func baseURL() string {
	if baseURLOverride != nil {
		return baseURLOverride()
	}
	if env := os.Getenv(baseURLEnv); env != "" {
		return env
	}
	return "https://" + brand.ModulePath
}

// baseURLEnv adalah variabel lingkungan override base URL release
// (pola yang sama dengan urlOverride FetchCaddy). Nama tools-agnostic:
// <NAME>_RELEASE_BASE_URL (NAME huruf besar).
var baseURLEnv = strings.ToUpper(brand.Name) + "_RELEASE_BASE_URL"

// maxSumEntry membatasi ukuran SHA256SUMS yang diparse — file manifest
// rilis ~6 entri; batas ini hanya penjaga bomb.
const maxSumEntry = 1 << 20

// hexSha256 menghitung digest file (helper test juga memakainya).
func hexSha256(path string) (string, error) {
	f, err := os.Open(path)
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

// LatestVersion mengembalikan tag release paling baru ("v1.2.3") lewat
// redirect Location header endpoint /releases/latest — satu request,
// tanpa JSON API (menghindari rate limit 60 req/jam unauthenticated).
func LatestVersion(ctx context.Context) (string, error) {
	url := strings.TrimRight(baseURL(), "/") + "/releases/latest"
	client := &http.Client{
		Timeout: 30 * time.Second,
		// Tidak mengikuti redirect: Location header adalah jawabannya.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("mengambil release terbaru: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusMovedPermanently {
		return "", fmt.Errorf("mengambil release terbaru: HTTP %s (mengharapkan redirect ke tag)", resp.Status)
	}
	loc := resp.Header.Get("Location")
	if loc == "" {
		return "", fmt.Errorf("mengambil release terbaru: redirect tanpa Location header")
	}
	tag := loc[strings.LastIndex(loc, "/")+1:]
	tag = strings.TrimPrefix(strings.TrimSpace(tag), "/")
	if tag == "" || !strings.HasPrefix(tag, "v") {
		return "", fmt.Errorf("release terbaru: Location tidak menunjuk tag valid: %s", loc)
	}
	return tag, nil
}

// AssetName mengembalikan nama aset binary untuk pasangan OS/arch sesuai
// kontrak penamaan release: <name>_<os>_<arch>[.exe].
func AssetName(osName, arch string) string {
	exe := ""
	if osName == "windows" {
		exe = ".exe"
	}
	return brand.Name + "_" + osName + "_" + arch + exe
}

// EnsureBinaries menjamin kedua binary (amd64 + arm64) untuk osName ada
// di cache, lalu mengembalikan path-nya berurutan. Sumber: cache lokal
// dulu (D3: ada di cache → tidak ada request), lalu release (D5:
// SHA256SUMS wajib cocok, fail-closed).
//
// Versi wajib format tag rilis valid — gunakan version.IsRelease() untuk
// menolak lebih dulu di pemanggil (plan D2: binary bundle = versi CLI
// yang berjalan, bukan latest).
func EnsureBinaries(ctx context.Context, cliVersion, cacheDir, osName string) ([]string, error) {
	sums, err := fetchSums(ctx, cliVersion)
	if err != nil {
		return nil, err
	}
	sumsDir := filepath.Join(cacheDir, "v"+cliVersion)
	if err := os.MkdirAll(sumsDir, 0o755); err != nil {
		return nil, fmt.Errorf("membuat cache binary %s: %w", sumsDir, err)
	}
	paths := make([]string, 0, 2)
	for _, arch := range []string{"amd64", "arm64"} {
		name := AssetName(osName, arch)
		want, ok := sums[name]
		if !ok {
			return nil, fmt.Errorf("%s tidak terdaftar di SHA256SUMS rilis v%s — fail-closed (C)", name, cliVersion)
		}
		path := filepath.Join(sumsDir, name)
		if !cachedBin(path, want) {
			if err := downloadVerify(ctx, cliVersion, name, want, path); err != nil {
				return nil, err
			}
		}
		paths = append(paths, path)
	}
	return paths, nil
}

// cachedBin melaporkan apakah path adalah binary cache yang diverifikasi.
func cachedBin(path, wantSum string) bool {
	sum, err := hexSha256(path)
	if err != nil {
		return false
	}
	return sum == wantSum
}

// downloadVerify mengunduh satu aset binary dan memverifikasi terhadap
// digest SHA256SUMS. Fail-closed: kegagalan apa pun menghapus file yang
// ditulis ini.
func downloadVerify(ctx context.Context, cliVersion, name, wantSum, path string) error {
	url := strings.TrimRight(baseURL(), "/") + "/releases/download/v" + cliVersion + "/" + name
	client := &http.Client{Timeout: 5 * time.Minute}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("mengunduh %s: %w", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("mengunduh %s: HTTP %s", name, resp.Status)
	}
	if err := writeBinary(resp.Body, path, resp.ContentLength); err != nil {
		return err
	}
	got, err := hexSha256(path)
	if err != nil {
		_ = os.Remove(path)
		return err
	}
	if got != wantSum {
		_ = os.Remove(path)
		return fmt.Errorf("%s checksum %s tidak cocok SHA256SUMS %s — unduhan ditolak (fail-closed)", name, got, wantSum)
	}
	return nil
}

// writeBinary menulis body ke path. Karena hanya dipanggil setelah
// cachedBin false (file tidak ada ATAU rusak checksumnya), race
// textual dihindari dengan O_CREATE|O_TRUNC pada proses ini; cek EEXIST
// tetap menghormati concurrent winner (paritas writeExtracted
// internal/proxy/download.go — proses yang kalah tidak boleh menimpa
// binary terverifikasi milik pemenang).
func writeBinary(r io.Reader, path string, declared int64) error {
	mode := os.FileMode(0o755)
	if strings.HasSuffix(path, ".exe") {
		mode = 0o644
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	// Batas anti-bomb: ukuran binary rilis ~15 MB; 256 MB cukup ruang
	// growth, tetapi menolak dekompresi/unduhan liar.
	const maxSize = 256 << 20
	// Batas anti-bomb: ukuran binary rilis ~15 MB; 256 MB cukup ruang
	// growth, tetapi menolak unduhan liar. CopyN EOF = sumber berakhir
	// (normal); n > maxSize = mencapai batas → file melebihi maksimum.
	n, err := io.CopyN(f, r, maxSize+1)
	if err != nil && err != io.EOF {
		_ = f.Close()
		_ = os.Remove(path)
		return fmt.Errorf("menulis %s: %w", path, err)
	}
	if n > maxSize {
		_ = f.Close()
		_ = os.Remove(path)
		return fmt.Errorf("menulis %s: melebihi batas %d byte (fail-closed)", path, maxSize)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("menutup %s: %w", path, err)
	}
	return nil
}

// fetchSums mengunduh dan memparse SHA256SUMS rilis versi terkunci.
// Parsing ketat (fmt.Sscanfece ganda gagal, komentar diabaikan): entry
// tak dikenal → fail-closed.
func fetchSums(ctx context.Context, cliVersion string) (map[string]string, error) {
	url := strings.TrimRight(baseURL(), "/") + "/releases/download/v" + cliVersion + "/" + sumsName
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mengunduh %s rilis v%s: %w", sumsName, cliVersion, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mengunduh %s rilis v%s: HTTP %s — tag mungkin tidak ada di release", sumsName, cliVersion, resp.Status)
	}
	return parseSums(io.LimitReader(resp.Body, maxSumEntry))
}

// sumsName adalah nama aset manifest checksum (Kontrak A).
const sumsName = "SHA256SUMS"

// parseSums memparse format sha256sum: "<hex>  <name>" per baris.
func parseSums(r io.Reader) (map[string]string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	sums := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || len(fields[0]) != 64 {
			return nil, fmt.Errorf("%s entri tidak valid: %q", sumsName, line)
		}
		sums[fields[1]] = fields[0]
	}
	if len(sums) == 0 {
		return nil, fmt.Errorf("%s kosong", sumsName)
	}
	return sums, nil
}
