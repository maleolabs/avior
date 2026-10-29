package proxy

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/maleolabs/avior/internal/brand"
)

// CaddyPath hanya me-resolve binary Caddy nyata; tidak pernah membuat binary
// placeholder. Rilis terpaket meletakkan Caddy di samping CLI; instalasi
// developer boleh memakai PATH.
//
// ponytail: layout kandidat masih mengikuti predecessor (tools/<name>/bin);
// rapikan saat layer download/install Caddy terbentuk dan ManifestPath jadi
// sumber kebenaran lokasi binary terkelola.
func CaddyPath(root string) (string, error) {
	name := "caddy_" + runtime.GOOS + "_" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	// `dist/bin` adalah lokasi staging workflow release sebelum dipindah ke
	// arsip produksi. Mendukungnya membuat CLI hasil build lokal berperilaku
	// seperti CLI arsip.
	candidates := []string{
		filepath.Join(root, "tools", brand.Name, "bin", name),
		filepath.Join(root, "tools", brand.Name, "dist", "bin", name),
		filepath.Join(filepath.Dir(os.Args[0]), "bin", name),
		filepath.Join(filepath.Dir(os.Args[0]), "dist", "bin", name),
		filepath.Join("bin", name),
		filepath.Join("dist", "bin", name),
		filepath.Join("tools", brand.Name, "bin", name),
		filepath.Join("tools", brand.Name, "dist", "bin", name),
	}
	// Utamakan lokasi executable asli saat dipanggil lewat symlink/launcher;
	// ini menutup arsip yang cwd-nya tidak berhubungan dengan release.
	if executable, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(executable); err == nil {
			dir := filepath.Dir(resolved)
			candidates = append(candidates, filepath.Join(dir, "bin", name), filepath.Join(dir, "dist", "bin", name), filepath.Join(dir, name))
		}
	}
	seen := make(map[string]struct{}, len(candidates))
	for _, p := range candidates {
		p = filepath.Clean(p)
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, nil
		}
	}
	if p, err := exec.LookPath("caddy"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("Caddy %s belum tersedia; pasang binary terverifikasi atau PATH caddy", CaddyVersion)
}

var localDomainPattern = regexp.MustCompile(`^(localhost|[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.(?:localhost|test))$`)

// ValidLocalDomain menuntut domain localhost-only (.localhost atau .test).
func ValidLocalDomain(domain string) bool {
	domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
	return localDomainPattern.MatchString(domain)
}

// CaddyConfig menghasilkan Caddyfile untuk reverse proxy lokal.
func CaddyConfig(domain, upstream string, port int, https bool, storage string) string {
	scheme := "http"
	if https {
		scheme = "https"
	}
	global := ""
	if https {
		// Automatic certificate management tetap aktif, tapi redirect listener
		// implisit HTTP->HTTPS pada :80 ditekan. `auto_https off` juga
		// menonaktifkan certificate automation, yang membuat `tls internal`
		// gagal provision/pilih sertifikat secara andal.
		// Path adalah token Caddyfile, bukan shell argument. HOME tanpa quote
		// (mis. username ber-spasi) terpecah jadi beberapa token dan internal
		// issuer gagal saat handshake.
		global = fmt.Sprintf("{\n\tadmin 127.0.0.1:2019\n\tauto_https disable_redirects\n\tstorage file_system %s\n}\n\n", caddyfileString(storage))
	}
	tls := ""
	if https {
		tls = "\ttls internal\n"
	}
	// Laravel selalu disajikan artisan lewat HTTP biasa. Scheme client-facing
	// tidak boleh bocor ke koneksi upstream (kalau bocor, Caddy mengirim TLS
	// ke localhost:8000 dan menghasilkan "unsupported SSL request").
	// Site address HTTPS eksplisit penting: mengaktifkan automation HTTPS
	// Caddy sementara `disable_redirects` di atas menghindari bind :80.
	address := domain
	if https {
		address = "https://" + domain
	}
	return fmt.Sprintf("%s%s:%d {\n\tbind 127.0.0.1\n%s\treverse_proxy %s {\n\t\theader_up X-Forwarded-Proto %s\n\t}\n}\n", global, address, port, tls, upstream, scheme)
}

// caddyfileString mengembalikan string literal Caddyfile. Caddyfile memakai
// quoted string mirip Go, jadi strconv.Quote sengaja dipakai alih-alih shell
// escaping buatan (config diberikan langsung ke Caddy, tidak pernah lewat
// shell).
func caddyfileString(value string) string {
	return strconv.Quote(value)
}

// ValidateConfig menjalankan konversi adapter yang sama dengan `caddy run`
// sebelum child process mana pun dimulai. Ini mengubah race startup/shutdown
// yang tidak jelas menjadi error parser Caddy yang actionable.
func ValidateConfig(binary, config string) error {
	cmd := exec.Command(binary, "validate", "--config", config, "--adapter", "caddyfile")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
