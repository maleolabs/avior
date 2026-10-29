// Package trust adalah layer trust CA lokal per-OS (arc:avior-architecture):
// Caddy trust adalah instalasi otoritatif; target browser/OS (NSS, keychain,
// Windows root store) bersifat best-effort dan tidak boleh mengubah keberhasilan
// startup menjadi gagal.
package trust

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/maleolabs/avior/internal/brand"
)

// ChromiumBinary mendeteksi browser keluarga Chromium, termasuk instalasi
// Brave native yang tidak terekspos lewat PATH.
func ChromiumBinary(lookPath func(string) (string, error), stat func(string) error) (string, bool) {
	for _, name := range []string{"brave-browser", "brave", "google-chrome", "google-chrome-stable", "chromium", "chromium-browser"} {
		if path, err := lookPath(name); err == nil {
			return path, true
		}
	}
	for _, path := range []string{"/opt/brave.com/brave/brave", "/usr/bin/brave-browser", "/usr/bin/brave", "/usr/bin/google-chrome", "/usr/bin/google-chrome-stable", "/usr/bin/chromium", "/usr/bin/chromium-browser"} {
		if stat(path) == nil {
			return path, true
		}
	}
	return "", false
}

func detectChromiumBinary() (string, bool) {
	return ChromiumBinary(exec.LookPath, func(path string) error { _, err := os.Stat(path); return err })
}

// TrustManager mengelola trust CA lokal: binary Caddy yang otoritatif,
// storage CA, dan metadata hasil instalasi per target.
type TrustManager struct{ binary, storage, metadata, resourceRoot string }

// TrustTarget mengidentifikasi satu trust store yang dikelola TrustManager.
// Nama target dipisah dari teks status agar hasil instalasi bisa dikonsumsi
// pemanggil tanpa parse output CLI.
type TrustTarget string

const (
	TrustTargetCaddy         TrustTarget = "caddy"
	TrustTargetFirefox       TrustTarget = "firefox"
	TrustTargetChromiumNSS   TrustTarget = "chromium-nss"
	TrustTargetWindowsRoot   TrustTarget = "windows-current-user-root"
	TrustTargetMacOSKeychain TrustTarget = "macos-user-keychain"
)

type TrustTargetStatus string

const (
	TrustTargetInstalled TrustTargetStatus = "installed"
	TrustTargetSkipped   TrustTargetStatus = "skipped"
	TrustTargetFailed    TrustTargetStatus = "failed"
	TrustTargetRemoved   TrustTargetStatus = "removed"
)

// TrustTargetResult adalah hasil untuk satu target. Error sengaja string agar
// hasil tetap cocok untuk JSON/metadata.
type TrustTargetResult struct {
	Target TrustTarget       `json:"target"`
	Status TrustTargetStatus `json:"status"`
	Error  string            `json:"error,omitempty"`
}

type trustMetadata struct {
	Fingerprint string   `json:"fingerprint"`
	Root        string   `json:"root"`
	Targets     []string `json:"targets"`
}

type TrustStatus struct {
	Fingerprint string              `json:"fingerprint,omitempty"`
	Root        string              `json:"root,omitempty"`
	Targets     []TrustTargetResult `json:"targets,omitempty"`
}

func NewTrustManager(binary, storage string, resourceRoots ...string) *TrustManager {
	var resourceRoot string
	if len(resourceRoots) > 0 {
		resourceRoot = resourceRoots[0]
	}
	return &TrustManager{binary: binary, storage: storage, resourceRoot: resourceRoot, metadata: filepath.Join(storage, brand.Name+"-trust.json")}
}

func (m *TrustManager) root() string {
	return filepath.Join(m.storage, "pki", "authorities", "local", "root.crt")
}

func (m *TrustManager) Install(config string) error {
	_, err := m.InstallTargets(config)
	return err
}

// InstallCaddy hanya melakukan instalasi CA Caddy yang otoritatif. Sengaja
// aman dipanggil sebelum child process berjalan: helper browser/NSS opsional
// tidak terlibat, jadi tidak bisa memblok atau race startup TLS Caddy.
func (m *TrustManager) InstallCaddy(config string) ([]TrustTargetResult, error) {
	results := []TrustTargetResult{{Target: TrustTargetCaddy}}
	c := exec.Command(m.binary, "trust", "--config", config, "--adapter", "caddyfile")
	var diagnostics strings.Builder
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, &diagnostics, &diagnostics
	if err := c.Run(); err != nil {
		results[0].Status = TrustTargetFailed
		results[0].Error = err.Error()
		message := strings.TrimSpace(diagnostics.String())
		if message != "" {
			return results, fmt.Errorf("instal CA internal Caddy gagal; browser akan menampilkan SSL error: %w: %s", err, message)
		}
		return results, fmt.Errorf("instal CA internal Caddy gagal; browser akan menampilkan SSL error: %w", err)
	}
	// Caddy trust otoritatif di sini. Ia memiliki integrasi trust
	// platform-specific (termasuk prompt privilege) dan bisa melaporkan sukses
	// walau backend p11-kit/certutil opsional tidak tersedia. Jangan jalankan
	// installer Linux kedua setelahnya: itu mengubah sukses trust Caddy
	// menjadi kegagalan startup palsu.
	results[0].Status = TrustTargetInstalled
	return results, nil
}

// InstallOptionalTargets memasang target browser/platform setelah Caddy
// melayani TLS, lalu menyimpan fingerprint dan metadata target lengkap.
func (m *TrustManager) InstallOptionalTargets() ([]TrustTargetResult, error) {
	b, err := os.ReadFile(m.root())
	if err != nil {
		return nil, fmt.Errorf("baca root CA Caddy: %w", err)
	}
	s := sha256.Sum256(b)
	results := []TrustTargetResult{}
	if runtime.GOOS == "linux" {
		results = append(results, m.installOptionalTarget(TrustTargetFirefox, func() error { _, err := installFirefoxTrust(m.root(), m.resourceRoot); return err })...)
		results = append(results, m.installOptionalTarget(TrustTargetChromiumNSS, func() error { _, err := installChromiumNSS(m.root(), s, m.resourceRoot); return err })...)
	} else if runtime.GOOS == "windows" {
		results = append(results, m.installOptionalTarget(TrustTargetWindowsRoot, func() error { return installWindowsTrust(m.root()) })...)
	} else if runtime.GOOS == "darwin" {
		results = append(results, m.installOptionalTarget(TrustTargetMacOSKeychain, func() error { return installMacOSTrust(m.root()) })...)
	}
	var targets []string
	for _, result := range results {
		if result.Status == TrustTargetInstalled {
			targets = append(targets, string(result.Target))
		}
	}
	data, err := json.MarshalIndent(trustMetadata{Fingerprint: hex.EncodeToString(s[:]), Root: m.root(), Targets: targets}, "", "  ")
	if err != nil {
		return results, fmt.Errorf("buat metadata trust: %w", err)
	}
	if err := os.WriteFile(m.metadata, append(data, '\n'), 0600); err != nil {
		return results, fmt.Errorf("simpan metadata trust: %w", err)
	}
	return results, nil
}

// InstallTargets memasang CA Caddy lalu target browser trust best-effort.
// Caddy tetap otoritatif: kegagalan Caddy menggagalkan operasi, kegagalan
// target browser opsional hanya dilaporkan di hasil.
func (m *TrustManager) InstallTargets(config string) ([]TrustTargetResult, error) {
	results, err := m.InstallCaddy(config)
	if err != nil {
		return results, err
	}
	optional, err := m.InstallOptionalTargets()
	results = append(results, optional...)
	if err != nil {
		return results, err
	}
	return results, nil
}

func (m *TrustManager) installOptionalTarget(target TrustTarget, install func() error) []TrustTargetResult {
	if err := install(); err != nil {
		return []TrustTargetResult{{Target: target, Status: TrustTargetFailed, Error: err.Error()}}
	}
	return []TrustTargetResult{{Target: target, Status: TrustTargetInstalled}}
}

type firefoxProfile struct{ Path string }

// firefoxProfiles membaca subset profiles.ini yang dibutuhkan di sini. Firefox
// menerima path absolut maupun path relatif terhadap direktori profil.
func firefoxProfiles(data, base string) []firefoxProfile {
	var profiles []firefoxProfile
	section := ""
	var path string
	var relative = true
	flush := func() {
		if !strings.HasPrefix(section, "Profile") || path == "" {
			return
		}
		if relative {
			path = filepath.Join(base, path)
		}
		profiles = append(profiles, firefoxProfile{Path: filepath.Clean(path)})
	}
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			flush()
			section, path, relative = line[1:len(line)-1], "", true
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "Path":
			path = strings.TrimSpace(value)
		case "IsRelative":
			relative = strings.TrimSpace(value) != "0"
		}
	}
	flush()
	return profiles
}

func firefoxProfilePaths(home string, readFile func(string) ([]byte, error)) []string {
	roots := []string{
		filepath.Join(home, ".mozilla", "firefox"),
		filepath.Join(home, ".var", "app", "org.mozilla.firefox", ".mozilla", "firefox"),
		filepath.Join(home, "snap", "firefox", "common", ".mozilla", "firefox"),
	}
	var result []string
	for _, root := range roots {
		data, err := readFile(filepath.Join(root, "profiles.ini"))
		if err != nil {
			continue
		}
		for _, profile := range firefoxProfiles(string(data), root) {
			result = append(result, profile.Path)
		}
	}
	return result
}

func firefoxCertutilArgs(profile, root string) []string {
	return []string{"-A", "-n", brand.CANickname, "-t", "C,,", "-i", root, "-d", "sql:" + profile}
}

func chromiumNSSPaths(home string, stat func(string) (os.FileInfo, error)) []string {
	candidates := []string{filepath.Join(home, ".pki", "nssdb"), filepath.Join(home, ".local", "share", "pki", "nssdb")}
	var existing []string
	for _, path := range candidates {
		if _, err := stat(filepath.Join(path, "cert9.db")); err == nil {
			existing = append(existing, path)
		}
	}
	if len(existing) > 0 {
		return existing
	}
	return []string{candidates[0]}
}

func chromiumNSSCertutilArgs(db, root string) []string {
	return []string{"-A", "-n", brand.CANickname, "-t", "C,,", "-i", root, "-d", "sql:" + db}
}

// installChromiumNSS sengaja menarget DB NSS Chromium per-user bersama; ia
// tidak pernah menulis /etc atau memanggil update-ca-certificates.
func installChromiumNSS(root string, fingerprint [32]byte, resourceRoot string) (bool, error) {
	// Brave (seperti Chromium di Linux) memakai DB NSS user bersama, bukan
	// database Cookies/Preferences profil browser. Target ini tetap
	// browser-neutral agar bekerja untuk Brave native dan Chromium.
	_ = fingerprint
	if _, ok := detectChromiumBinary(); !ok {
		return false, fmt.Errorf("browser Chromium/Brave tidak ditemukan; NSS database tidak dipasang")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false, err
	}
	certutil, err := bundledCertutil(maybeCLIPath(), resourceRoot)
	if err != nil {
		return false, err
	}
	dbs := chromiumNSSPaths(home, os.Stat)
	for _, db := range dbs {
		if err := os.MkdirAll(db, 0700); err != nil {
			return false, fmt.Errorf("buat Chromium NSS database: %w", err)
		}
		// DB NSS Chromium bersama sering tidak ada di mesin baru. Buat dengan
		// password kosong; certutil menyimpan database, bukan secret login.
		if _, err := os.Stat(filepath.Join(db, "cert9.db")); os.IsNotExist(err) {
			cmd := exec.Command(certutil, "-N", "-d", "sql:"+db, "--empty-password")
			if output, err := cmd.CombinedOutput(); err != nil {
				return false, fmt.Errorf("inisialisasi Chromium NSS database: %w: %s", err, strings.TrimSpace(string(output)))
			}
		} else if err != nil {
			return false, err
		}
		// certutil -A idempotent untuk nickname sama di NSS saat ini; hapus
		// entri lama dulu agar CA Caddy yang diperbarui menggantinya bersih.
		_ = exec.Command(certutil, "-D", "-n", brand.CANickname, "-d", "sql:"+db).Run()
		cmd := exec.Command(certutil, chromiumNSSCertutilArgs(db, root)...)
		if output, err := cmd.CombinedOutput(); err != nil {
			return false, fmt.Errorf("pasang sertifikat Chromium NSS: %w: %s", err, strings.TrimSpace(string(output)))
		}
	}
	return true, nil
}

func installFirefoxTrust(root string, resourceRoot string) (int, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return 0, err
	}
	certutil, err := bundledCertutil(maybeCLIPath(), resourceRoot)
	if err != nil {
		return 0, err
	}
	profiles := firefoxProfilePaths(home, os.ReadFile)
	imported := 0
	var lastError string
	for _, profile := range profiles {
		if _, err := os.Stat(profile); err != nil {
			continue
		}
		if output, err := exec.Command(certutil, firefoxCertutilArgs(profile, root)...).CombinedOutput(); err == nil {
			imported++
		} else {
			// Pertahankan diagnosa helper; dia hilang di balik error agregat
			// Firefox best-effort jika tidak disimpan.
			lastError = fmt.Sprintf("%v: %s", err, strings.TrimSpace(string(output)))
		}
	}
	if imported == 0 {
		return 0, fmt.Errorf("tidak ada profil Firefox yang berhasil diimpor: %s", lastError)
	}
	return imported, nil
}

func (m *TrustManager) Status() (string, bool) {
	b, e := os.ReadFile(m.root())
	if e != nil {
		return "", false
	}
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:]), true
}

// StatusDetailed hanya membaca metadata yang ditulis manager ini. Metadata
// hilang/rusak dilaporkan sebagai belum terpasang — tidak pernah trusted.
func (m *TrustManager) StatusDetailed() TrustStatus {
	var meta trustMetadata
	b, err := os.ReadFile(m.metadata)
	if err != nil || json.Unmarshal(b, &meta) != nil {
		return TrustStatus{}
	}
	status := TrustStatus{Fingerprint: meta.Fingerprint, Root: meta.Root}
	for _, target := range meta.Targets {
		status.Targets = append(status.Targets, TrustTargetResult{Target: TrustTarget(target), Status: TrustTargetInstalled})
	}
	return status
}

// removeTempConfig menulis config Caddyfile minimal yang hanya memuat
// storage block, agar `caddy untrust` menemukan CA di storage tool ini.
// File temp dihapus caller.
func (m *TrustManager) removeTempConfig() (string, error) {
	f, err := os.CreateTemp("", brand.Name+"-untrust-")
	if err != nil {
		return "", err
	}
	// caddyfileString ada di internal/proxy; di sini cukup strconv.Quote
	// (semantik sama: string literal Caddyfile, tanpa shell).
	config := fmt.Sprintf("{\n\tstorage file_system %s\n}\n", strconv.Quote(m.storage))
	if _, err := f.WriteString(config); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return f.Name(), nil
}

// untrustCaddy membalik instalasi CA Caddy yang otoritatif (review H7):
// tanpa ini, `caddy untrust` tidak pernah dijalankan dan CA tetap trusted di
// system store setelah remove. Best-effort: kegagalan dilaporkan sebagai
// hasil target, bukan panic.
func (m *TrustManager) untrustCaddy() error {
	config, err := m.removeTempConfig()
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(config) }()
	c := exec.Command(m.binary, "untrust", "--config", config, "--adapter", "caddyfile")
	var diagnostics strings.Builder
	c.Stdout, c.Stderr = &diagnostics, &diagnostics
	if err := c.Run(); err != nil {
		message := strings.TrimSpace(diagnostics.String())
		if message != "" {
			return fmt.Errorf("%w: %s", err, message)
		}
		return err
	}
	return nil
}

// RemoveTargets membalik seluruh instalasi trust per target (review H7):
// untrust CA Caddy di system store, semua DB NSS Chromium yang ada (bukan
// satu path), semua profil Firefox, store platform-specific, lalu metadata.
// Signature per-target: pemanggil bisa melapor mana target yang gagal
// tanpa kehilangan keberhasilan target lain.
func (m *TrustManager) RemoveTargets() []TrustTargetResult {
	var results []TrustTargetResult
	report := func(target TrustTarget, err error) {
		if err != nil {
			results = append(results, TrustTargetResult{Target: target, Status: TrustTargetFailed, Error: err.Error()})
			return
		}
		results = append(results, TrustTargetResult{Target: target, Status: TrustTargetRemoved})
	}
	// CA Caddy adalah target otoritatif: kegagalan untrust tetap dilaporkan
	// tetapi tidak menghentikan pembersihan target lain.
	report(TrustTargetCaddy, m.untrustCaddy())
	if runtime.GOOS == "windows" {
		report(TrustTargetWindowsRoot, removeWindowsTrust(m.root()))
	}
	if runtime.GOOS == "darwin" {
		report(TrustTargetMacOSKeychain, removeMacOSTrust(m.root()))
	}
	if runtime.GOOS == "linux" {
		if home, err := os.UserHomeDir(); err == nil {
			if certutil, e := bundledCertutil(maybeCLIPath(), m.resourceRoot); e == nil {
				// Semua kandidat DB NSS Chromium yang ada — bukan satu path
				// (review H7: .pki/nssdb dan .local/share/pki/nssdb). Delete
				// per nickname best-effort; satu laporan untuk target.
				for _, db := range chromiumNSSPaths(home, os.Stat) {
					_ = exec.Command(certutil, "-D", "-n", brand.CANickname, "-d", "sql:"+db).Run() //nolint:gosec -- certutil dari resource terverifikasi
				}
				report(TrustTargetChromiumNSS, nil)
			}
			for _, profile := range firefoxProfilePaths(home, os.ReadFile) {
				if certutil, e := bundledCertutil(maybeCLIPath(), m.resourceRoot); e == nil {
					_ = exec.Command(certutil, "-D", "-n", brand.CANickname, "-d", "sql:"+profile).Run() //nolint:gosec -- idem
					report(TrustTargetFirefox, nil)
				}
			}
		}
	}
	if err := os.Remove(m.metadata); err != nil && !os.IsNotExist(err) {
		report(TrustTargetCaddy, fmt.Errorf("hapus metadata trust: %w", err))
	}
	return results
}
