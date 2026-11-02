// Package gateway mengelola satu instance Caddy bersama (gateway) yang
// melayani SEMUA proyek aktif pada port standar (:80/:443) dengan routing
// berbasis Host header — sehingga URL user-facing bersih (tanpa port):
// https://shop.localhost/.
//
// Gateway hidup sebagai file config tunggal di StateDir/gateway/Caddyfile
// yang di-regenerate atomik setiap kali proyek start/stop; Caddy dijalankan
// ulang (atau reload via admin API) setelah config berubah.
//
// Mode ini opt-in (--clean-url): tanpa privilege untuk bind :80/:443,
// gateway tidak digunakan dan setiap proyek tetap memakai port tinggi.
package gateway

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/maleolabs/avior/internal/brand"
	"github.com/maleolabs/avior/internal/proxy"
)

// Site adalah satu blok site dalam gateway config.
type Site struct {
	Domain   string // e.g. shop.localhost
	Upstream string // e.g. 127.0.0.1:8012
	HTTPS    bool
}

// Manager membangun dan menyimpan config gateway. Stateless kecuali
// lokasi config; aman dipakai konkuren (mutex hanya melindungi tulis).
type Manager struct {
	configPath string
	certDir    string
	binary     string // path binary Caddy terverifikasi
	mu         sync.Mutex
}

// NewManager membuat manager dengan binary Caddy terverifikasi. Storage
// cert gateway selalu shared (StateDir/caddy — satu CA untuk semua domain,
// paritas Phase 6).
func NewManager(binary string) *Manager {
	return &Manager{
		configPath: filepath.Join(brand.StateDir(), "gateway", "Caddyfile"),
		certDir:    filepath.Join(brand.StateDir(), "caddy"),
		binary:     binary,
	}
}

// ConfigPath mengembalikan lokasi Caddyfile gateway.
func (m *Manager) ConfigPath() string { return m.configPath }

// BuildConfig merender Caddyfile gateway dari kumpulan site. Site dengan
// domain sama tidak boleh muncul dua kali (caller harus memastikan
// uniqueness — lihat internal/runtime domain allocator). Urutan site
// deterministik (sort domain) sehingga config identik untuk himpunan site
// yang sama — penting untuk diff/reload stability.
func (m *Manager) BuildConfig(sites []Site, adminPort int) string {
	// Urutan deterministik: config identik untuk himpunan site yang sama.
	sorted := make([]Site, len(sites))
	copy(sorted, sites)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Domain < sorted[j].Domain })

	var b strings.Builder
	b.WriteString("{\n")
	b.WriteString("\tadmin 127.0.0.1:" + strconv.Itoa(adminPort) + "\n")
	b.WriteString("\tauto_https disable_redirects\n")
	b.WriteString("\tstorage file_system " + strconv.Quote(mustAbs(m.certDir)) + "\n")
	b.WriteString("}\n\n")
	for _, s := range sorted {
		address := s.Domain
		if s.HTTPS {
			address = "https://" + s.Domain
		} else {
			// Prefix http:// menonaktifkan automatic HTTPS untuk site ini
			// (mencegah bind :80 redirect; paritas proxy.CaddyConfig).
			address = "http://" + s.Domain
		}
		b.WriteString(address + " {\n")
		b.WriteString("\tbind 127.0.0.1\n")
		if s.HTTPS {
			b.WriteString("\ttls internal\n")
		}
		b.WriteString("\treverse_proxy " + s.Upstream + " {\n")
		b.WriteString("\t\theader_up X-Forwarded-Proto https\n")
		b.WriteString("\t}\n")
		b.WriteString("}\n\n")
	}
	return b.String()
}

// Render menyimpan config gateway (collection sites) ke disk secara atomik
// dan memvalidasinya dengan binary Caddy. Mengembalikan error validasi
// tanpa menimpa config lama bila invalid.
func (m *Manager) Render(sites []Site, adminPort int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.renderLocked(sites, adminPort)
}

// ActiveSites membaca gateway config yang tersimpan dan mengembalikan daftar
// domain yang dilayaninya (untuk monitor & deteksi site terdaftar).
func (m *Manager) ActiveSites() ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, err := os.ReadFile(m.configPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("gateway: baca config: %w", err)
	}
	var sites []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasSuffix(line, " {") {
			continue
		}
		addr := strings.TrimSuffix(line, " {")
		switch {
		case strings.HasPrefix(addr, "https://"):
			sites = append(sites, strings.TrimPrefix(addr, "https://"))
		case strings.HasPrefix(addr, "http://"):
			sites = append(sites, strings.TrimPrefix(addr, "http://"))
		default:
			// global options block "{" — lewati.
		}
	}
	sort.Strings(sites)
	return sites, nil
}

// RegisterSite menambah/memperbarui satu site pada config gateway yang
// ada; site lain tetap utuh. Dipanggil saat serve proyek start.
func (m *Manager) RegisterSite(site Site, adminPort int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, err := os.ReadFile(m.configPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("gateway: baca config: %w", err)
	}
	// Hanya mem-parse config yang di-generate sendiri (bukan
	// parser Caddyfile umum).
	sites, err := m.parseSites(string(data))
	if err != nil {
		return fmt.Errorf("gateway: parse config: %w", err)
	}
	found := false
	for i, s := range sites {
		if s.Domain == site.Domain {
			sites[i] = site
			found = true
			break
		}
	}
	if !found {
		sites = append(sites, site)
	}
	return m.renderLocked(sites, adminPort)
}

// UnregisterSite menghapus satu site dari config gateway. Mengembalikan
// jumlah site tersisa (0 = gateway boleh dihentikan).
func (m *Manager) UnregisterSite(domain string, adminPort int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, err := os.ReadFile(m.configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("gateway: baca config: %w", err)
	}
	sites, err := m.parseSites(string(data))
	if err != nil {
		return 0, fmt.Errorf("gateway: parse config: %w", err)
	}
	kept := make([]Site, 0, len(sites))
	for _, s := range sites {
		if s.Domain != domain {
			kept = append(kept, s)
		}
	}
	if len(kept) == 0 {
		if err := m.removeLocked(); err != nil {
			return 0, err
		}
		return 0, nil
	}
	if err := m.renderLocked(kept, adminPort); err != nil {
		return 0, err
	}
	return len(kept), nil
}

func (m *Manager) renderLocked(sites []Site, adminPort int) error {
	config := m.BuildConfig(sites, adminPort)
	if err := os.MkdirAll(filepath.Dir(m.configPath), 0o700); err != nil {
		return fmt.Errorf("gateway: mkdir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(m.configPath), filepath.Base(m.configPath)+".tmp-*")
	if err != nil {
		return fmt.Errorf("gateway: temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.WriteString(config); err != nil {
		tmp.Close()
		return fmt.Errorf("gateway: tulis: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("gateway: close: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("gateway: chmod: %w", err)
	}
	// Validasi hanya bila binary ada di disk: render config sebelum binary
	// terpasang (mis. first-run sebelum unduhan) harus tetap berhasil.
	// Validasi adalah best-effort gate; parse Caddyfile sendiri sudah
	// menolak config yang strukturnya rusak.
	if _, statErr := os.Stat(m.binary); statErr == nil {
		if err := proxy.ValidateConfig(m.binary, tmpName); err != nil {
			return fmt.Errorf("gateway: config invalid: %w", err)
		}
	}
	return os.Rename(tmpName, m.configPath)
}

func (m *Manager) removeLocked() error {
	if err := os.Remove(m.configPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("gateway: hapus config: %w", err)
	}
	return nil
}

// parseSites mem-parsing config hasil BuildConfig (bukan parser umum).
func (m *Manager) parseSites(data string) ([]Site, error) {
	var sites []Site
	lines := strings.Split(data, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		var domain string
		var https bool
		switch {
		case strings.HasPrefix(line, "https://") && strings.HasSuffix(line, " {"):
			domain = strings.TrimSuffix(strings.TrimPrefix(line, "https://"), " {")
			https = true
		case strings.HasPrefix(line, "http://") && strings.HasSuffix(line, " {"):
			domain = strings.TrimSuffix(strings.TrimPrefix(line, "http://"), " {")
		default:
			continue
		}
		// Ambil upstream dari baris reverse_proxy di dalam blok.
		var upstream string
		for j := i + 1; j < len(lines); j++ {
			inner := strings.TrimSpace(lines[j])
			if strings.HasPrefix(inner, "reverse_proxy ") {
				upstream = strings.TrimPrefix(inner, "reverse_proxy ")
				if idx := strings.Index(upstream, " {"); idx >= 0 {
					upstream = upstream[:idx]
				}
				break
			}
			if inner == "}" {
				break
			}
		}
		if upstream == "" {
			return nil, fmt.Errorf("site %s tanpa upstream", domain)
		}
		sites = append(sites, Site{Domain: domain, Upstream: upstream, HTTPS: https})
	}
	return sites, nil
}

// netListen membuka listener TCP loopback sementara (probe privilege).
func netListen(port int) (net.Listener, error) {
	return net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
}

func mustAbs(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}
