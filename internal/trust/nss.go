package trust

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	runtimepkg "runtime"
	"strings"

	"github.com/maleolabs/avior/internal/brand"
)

type nssManifest struct {
	Helpers map[string]nssHelper `json:"helpers"`
}
type nssHelper struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// resourceDirEnv adalah env var override eksplisit lokasi resource (bukan
// keharusan). Nama diturunkan dari brand agar tidak ada literal nama tool.
func resourceDirEnv() string {
	return strings.ToUpper(brand.Name) + "_RESOURCE_DIR"
}

// bundledCertutil memilih helper certutil terverifikasi yang dikirim di
// samping binary release. Instalasi development tetap punya fallback certutil
// PATH untuk kompatibilitas.
func bundledCertutil(binary string, resourceRoots ...string) (string, error) {
	// Arsip release menyimpan CLI dan resource-nya di bawah
	// <release>/tools/<nama-tool>/. os.Executable biasanya menunjuk binary asli,
	// tapi launcher/symlink development bisa meninggalkan layout staging atau
	// hanya direktori executable. Coba semua layout secara eksplisit.
	base := filepath.Dir(binary)
	if resolved, err := filepath.EvalSymlinks(binary); err == nil {
		base = filepath.Dir(resolved)
	}
	// Env var adalah override eksplisit, bukan keharusan. Override di urutan
	// pertama agar packager bisa memisahkan binary imutabel dan resource.
	manifestPaths := make([]string, 0, 12+len(resourceRoots))
	if resourceDir := strings.TrimSpace(os.Getenv(resourceDirEnv())); resourceDir != "" {
		manifestPaths = append(manifestPaths, filepath.Join(resourceDir, "nss", "MANIFEST.json"))
	}
	// CLI terinstal global bisa dijalankan dari luar proyek. Runtime root
	// menjadi anchor stabil resource bundulan, mengikuti layout Caddy:
	// <root>/tools/<nama-tool>/nss.
	for _, root := range resourceRoots {
		if root = strings.TrimSpace(root); root != "" {
			manifestPaths = append(manifestPaths,
				filepath.Join(root, "tools", brand.Name, "nss", "MANIFEST.json"),
				filepath.Join(root, "nss", "MANIFEST.json"))
		}
	}
	manifestPaths = append(manifestPaths,
		filepath.Join(base, "nss", "MANIFEST.json"),
		filepath.Join(base, "..", "nss", "MANIFEST.json"),
		filepath.Join(base, "..", "tools", brand.Name, "nss", "MANIFEST.json"),
		filepath.Join(base, "tools", brand.Name, "nss", "MANIFEST.json"),
		filepath.Join(base, "dist", "nss", "MANIFEST.json"),
		filepath.Join("nss", "MANIFEST.json"),
		filepath.Join("tools", brand.Name, "nss", "MANIFEST.json"),
		filepath.Join("dist", "nss", "MANIFEST.json"))
	// Hindari probe duplikat saat executable sendiri ada di checkout.
	seen := make(map[string]struct{}, len(manifestPaths))
	unique := manifestPaths[:0]
	for _, p := range manifestPaths {
		p = filepath.Clean(p)
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		unique = append(unique, p)
	}
	manifestPaths = unique
	var manifestPath string
	var b []byte
	var err error
	for _, candidate := range manifestPaths {
		b, err = os.ReadFile(candidate)
		if err == nil {
			manifestPath = candidate
			break
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("baca NSS manifest %s: %w", candidate, err)
		}
	}
	if err == nil {
		var m nssManifest
		if err := json.Unmarshal(b, &m); err != nil {
			return "", fmt.Errorf("parse NSS manifest: %w", err)
		}
		key := runtimepkg.GOOS + "/" + runtimepkg.GOARCH
		h, ok := m.Helpers[key]
		if !ok {
			return "", fmt.Errorf("NSS helper tidak tersedia untuk %s", key)
		}
		clean := filepath.Clean(h.Path)
		if filepath.IsAbs(h.Path) || clean == "." || clean == ".." || len(clean) >= 3 && clean[:3] == ".."+string(filepath.Separator) {
			return "", fmt.Errorf("path NSS helper tidak valid")
		}
		nssDir := filepath.Dir(manifestPath)
		p := filepath.Join(nssDir, clean)
		data, err := os.ReadFile(p)
		if err != nil {
			return "", fmt.Errorf("baca NSS helper: %w", err)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != h.SHA256 {
			return "", fmt.Errorf("checksum NSS helper tidak cocok")
		}
		if err := os.Chmod(p, 0755); err != nil {
			return "", err
		}
		return p, nil
	}
	if p, pathErr := exec.LookPath("certutil"); pathErr == nil {
		return p, nil
	}
	return "", fmt.Errorf("NSS certutil helper tidak ditemukan; manifest dicari di %s; pasang release lengkap (termasuk tools/%s/nss) atau sediakan certutil di PATH", strings.Join(manifestPaths, ", "), brand.Name)
}

func maybeCLIPath() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	return p
}
