package proxy

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// FetchCaddy adalah alur install Caddy terkelola: unduh arsip rilis resmi
// terpin, ekstrak binary ke destination, verifikasi sha256 terhadap pin
// build-time (Kontrak B), lalu — HANYA setelah checksum cocok — tulis
// manifest StateDir. Fail-closed total: kegagalan apa pun tidak menyisakan
// binary di destination, dan manifest tidak pernah ditulis dari binary yang
// tidak terverifikasi. Tanpa shell; batas ukuran entry mencegah
// decompression bomb.
func FetchCaddy(ctx context.Context, destination string) error {
	url := fetchArchiveURL()
	if urlOverride != nil {
		url = urlOverride()
	}
	client := &http.Client{Timeout: 2 * time.Minute}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download Caddy: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download Caddy: HTTP %s", resp.Status)
	}
	want := "caddy"
	if runtime.GOOS == "windows" {
		want += ".exe"
	}
	// Rilis Windows upstream didistribusikan sebagai zip; platform lain
	// tar.gz. Nama asset darwin adalah "mac_" (lihat fetchArchiveURL).
	var extractErr error
	if runtime.GOOS == "windows" {
		extractErr = extractZip(resp.Body, want, destination)
	} else {
		extractErr = extractTarGz(resp.Body, want, destination)
	}
	if extractErr != nil {
		// EEXIST berarti proses ini TIDAK menciptakan destination —
		// first-run concurrent yang kalah tidak boleh menghapus binary
		// terverifikasi milik yang menang. Hapus hanya file yang kita tulis.
		if !errors.Is(extractErr, fs.ErrExist) {
			_ = os.Remove(destination)
		}
		return extractErr
	}
	return verifyAndRecordInstall(destination)
}

// extractTarGz menulis entry binary pertama yang cocok ke destination
// (O_EXCL) dan membersihkan file parsial saat copy gagal.
func extractTarGz(r io.Reader, want, destination string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("read Caddy archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("Caddy archive does not contain %s", want)
		}
		if err != nil {
			return err
		}
		if filepath.Base(h.Name) != want || h.Typeflag != tar.TypeReg {
			continue
		}
		if h.Size <= 0 || h.Size > 100<<20 {
			return fmt.Errorf("Caddy binary size invalid: %d", h.Size)
		}
		return writeExtracted(tr, destination, h.Size)
	}
}

// extractZip menulis entry binary pertama yang cocok dari arsip zip rilis
// Windows. zip.Reader butuh Size+ReaderAt, jadi body dibaca ke memori
// (arsip rilis Caddy ~20 MB, sekali saat install).
func extractZip(r io.Reader, want, destination string) error {
	body, err := io.ReadAll(io.LimitReader(r, 200<<20))
	if err != nil {
		return fmt.Errorf("read Caddy archive: %w", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return fmt.Errorf("read Caddy archive: %w", err)
	}
	for _, f := range zr.File {
		if filepath.Base(f.Name) != want || f.FileInfo().IsDir() {
			continue
		}
		if f.UncompressedSize64 <= 0 || f.UncompressedSize64 > 100<<20 {
			return fmt.Errorf("Caddy binary size invalid: %d", f.UncompressedSize64)
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		return writeExtracted(rc, destination, int64(f.UncompressedSize64))
	}
	return fmt.Errorf("Caddy archive does not contain %s", want)
}

// writeExtracted menyalin persis size byte ke destination dengan mode
// eksekusi yang benar; file parsial dihapus.
func writeExtracted(r io.Reader, destination string, size int64) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}
	mode := os.FileMode(0755)
	if runtime.GOOS == "windows" {
		mode = 0644
	}
	f, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			// First-run concurrent: proses lain sudah memasang binary —
			// kembalikan error actionable tanpa menyentuh file existing.
			return fmt.Errorf("install %s dibatalkan: Caddy sudah terpasang oleh proses lain: %w", destination, err)
		}
		return err
	}
	_, copyErr := io.CopyN(f, r, size)
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(destination)
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	}
	return nil
}

// verifyAndRecordInstall adalah gate Kontrak B: tanpa pin untuk pasangan
// OS/arch → tolak (fail-closed, tanpa fallback silent); checksum tidak
// cocok → tolak dan hapus hasil unduhan; cocok → tulis manifest StateDir.
func verifyAndRecordInstall(destination string) error {
	pin, ok := caddyPin(runtime.GOOS, runtime.GOARCH)
	if !ok {
		_ = os.Remove(destination)
		return fmt.Errorf("pin checksum Caddy untuk %s/%s tidak tersedia di build ini — unduhan ditolak (fail-closed); tambahkan pin di internal/proxy/pins.go", runtime.GOOS, runtime.GOARCH)
	}
	sum, err := fileSHA256(destination)
	if err != nil {
		_ = os.Remove(destination)
		return err
	}
	if sum != pin {
		_ = os.Remove(destination)
		return fmt.Errorf("Caddy %s checksum %s tidak cocok pin build-time %s — unduhan ditolak (H3)", destination, sum, pin)
	}
	if err := saveManifest(Manifest{SHA256: sum, Version: CaddyVersion}); err != nil {
		_ = os.Remove(destination)
		return err
	}
	return nil
}

// writeManifest menulis manifest StateDir SETELAH pin terverifikasi.
// Manifest adalah cache install-time (bisa dihapus/dibuat ulang dengan
// install ulang), bukan root of trust — root of trust adalah pin build-time.
func saveManifest(m Manifest) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	path := ManifestPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

var urlOverride func() string

// fetchArchiveURL mengikuti penamaan asset rilis upstream: platform darwin
// dipublikasikan sebagai "mac_", platform windows sebagai .zip.
func fetchArchiveURL() string {
	osName := runtime.GOOS
	ext := ".tar.gz"
	switch osName {
	case "darwin":
		osName = "mac"
	case "windows":
		ext = ".zip"
	}
	name := "caddy_" + CaddyVersion + "_" + osName + "_" + runtime.GOARCH + ext
	return "https://github.com/caddyserver/caddy/releases/download/v" + CaddyVersion + "/" + name
}
