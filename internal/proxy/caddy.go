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
// Kandidat: layout proyek/arsip (tools/<name>/bin), lokasi executable asli,
// lalu binary terkelola hasil first-run di StateDir (ManagedCaddyPath), dan
// terakhir PATH. Manifest StateDir adalah cache install, bukan root of trust —
// verifikasi hash tetap lewat VerifyManaged/pin build-time.
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
	// Binary hasil first-run download tinggal di StateDir. Ditaruh setelah
	// kandidat proyek/arsip agar layout rilis tetap menang, tapi sebelum PATH
	// supaya `trust` memakai binary terkelola (bukan caddy PATH acak).
	candidates = append(candidates, ManagedCaddyPath())
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

// ManagedCaddyPath is stable user-level install location used by first-run.
func ManagedCaddyPath() string {
	name := "caddy_" + runtime.GOOS + "_" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(brand.StateDir(), "bin", name)
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
	} else {
		// Domain .localhost/.test memenuhi kualifikasi automatic HTTPS Caddy;
		// tanpa scheme eksplisit Caddy menyalakan redirect listener HTTP->HTTPS
		// pada :80 yang butuh privilege root (ditemukan e2e sto:p2-e2e-smoke).
		// Prefix http:// menonaktifkan automation untuk site ini saja.
		address = "http://" + domain
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
