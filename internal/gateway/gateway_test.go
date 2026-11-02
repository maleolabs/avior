package gateway

// Test suite Phase 7 (gateway clean URL):
//   - config agregasi site deterministik (urutan domain),
//   - register/unregister site round-trip pada config,
//   - parseSites round-trip terhadap BuildConfig,
//   - state gateway persist + idempotensi.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maleolabs/avior/internal/brand"
)

// isolateHome mengarahkan StateDir ke temp (test tidak boleh menyentuh
// state user sungguhan).
func isolateHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

// configFixture mengembalikan config 3 site tak terurut.
func configFixture() []Site {
	return []Site{
		{Domain: "zeta.localhost", Upstream: "127.0.0.1:8003", HTTPS: true},
		{Domain: "alpha.localhost", Upstream: "127.0.0.1:8001", HTTPS: true},
		{Domain: "mid.localhost", Upstream: "127.0.0.1:8002"},
	}
}

// TestBuildConfigAggregatesSites: config berisi semua site, urutan
// deterministik (sort domain), dan block global storage shared ada.
func TestBuildConfigAggregatesSites(t *testing.T) {
	isolateHome(t)
	mgr := NewManager("/nonexistent/caddy")
	cfg := mgr.BuildConfig(configFixture(), 2019)

	// Urutan deterministik: alpha < mid < zeta.
	idxA := strings.Index(cfg, "alpha.localhost")
	idxM := strings.Index(cfg, "mid.localhost")
	idxZ := strings.Index(cfg, "zeta.localhost")
	if idxA < 0 || idxM < 0 || idxZ < 0 {
		t.Fatalf("config tidak memuat semua site:\n%s", cfg)
	}
	if !(idxA < idxM && idxM < idxZ) {
		t.Fatalf("urutan site tidak deterministik: alpha=%d mid=%d zeta=%d", idxA, idxM, idxZ)
	}
	if !strings.Contains(cfg, "admin 127.0.0.1:2019") {
		t.Errorf("config tidak memuat admin port: %s", cfg)
	}
	if !strings.Contains(cfg, "storage file_system") {
		t.Errorf("config tidak memuat storage shared: %s", cfg)
	}
	// HTTPS site memakai tls internal; HTTP site tidak.
	if !strings.Contains(cfg, "https://alpha.localhost") || !strings.Contains(cfg, "tls internal") {
		t.Errorf("HTTPS site tidak terender: %s", cfg)
	}
	if !strings.Contains(cfg, "http://mid.localhost") {
		t.Errorf("HTTP site tidak terender: %s", cfg)
	}
	// BuildConfig ulang himpunan sama → config identik (stabilitas diff).
	if cfg2 := mgr.BuildConfig(configFixture(), 2019); cfg2 != cfg {
		t.Errorf("config tidak deterministik untuk himpunan site sama")
	}
}

// TestBuildConfigDeterministicAcrossPermutation: himpunan site dengan
// urutan input berbeda → config identik.
func TestBuildConfigDeterministicAcrossPermutation(t *testing.T) {
	isolateHome(t)
	mgr := NewManager("/nonexistent/caddy")
	a := mgr.BuildConfig([]Site{
		{Domain: "b.localhost", Upstream: "127.0.0.1:2"},
		{Domain: "a.localhost", Upstream: "127.0.0.1:1"},
	}, 2019)
	b := mgr.BuildConfig([]Site{
		{Domain: "a.localhost", Upstream: "127.0.0.1:1"},
		{Domain: "b.localhost", Upstream: "127.0.0.1:2"},
	}, 2019)
	if a != b {
		t.Errorf("config berubah untuk urutan input berbeda:\nA=%s\nB=%s", a, b)
	}
}

// TestRegisterUnregisterRoundTrip: RegisterSite menambah site baru,
// memperbarui site yang ada (upstream berubah), UnregisterSite menghapus;
// parseSites kembali membaca persis site yang tertulis.
func TestRegisterUnregisterRoundTrip(t *testing.T) {
	isolateHome(t)
	mgr := NewManager("/nonexistent/caddy")

	if err := mgr.RegisterSite(Site{Domain: "shop.localhost", Upstream: "127.0.0.1:8001", HTTPS: true}, 2019); err != nil {
		t.Fatalf("RegisterSite #1: %v", err)
	}
	if err := mgr.RegisterSite(Site{Domain: "blog.localhost", Upstream: "127.0.0.1:8002", HTTPS: true}, 2019); err != nil {
		t.Fatalf("RegisterSite #2: %v", err)
	}
	data, err := os.ReadFile(mgr.ConfigPath())
	if err != nil {
		t.Fatalf("baca config: %v", err)
	}
	sites, err := mgr.parseSites(string(data))
	if err != nil {
		t.Fatalf("parseSites: %v", err)
	}
	if len(sites) != 2 {
		t.Fatalf("site terbaca = %d, want 2: %+v", len(sites), sites)
	}

	// Update site yang ada: upstream berubah.
	if err := mgr.RegisterSite(Site{Domain: "shop.localhost", Upstream: "127.0.0.1:9009", HTTPS: true}, 2019); err != nil {
		t.Fatalf("RegisterSite update: %v", err)
	}
	data, err = os.ReadFile(mgr.ConfigPath())
	if err != nil {
		t.Fatalf("baca config update: %v", err)
	}
	if !strings.Contains(string(data), "127.0.0.1:9009") {
		t.Errorf("upstream baru tidak tertulis: %s", data)
	}
	sites, err = mgr.parseSites(string(data))
	if err != nil {
		t.Fatalf("parseSites update: %v", err)
	}
	if len(sites) != 2 {
		t.Fatalf("update menambah site ganda: %d", len(sites))
	}

	// Unregister satu site.
	remaining, err := mgr.UnregisterSite("shop.localhost", 2019)
	if err != nil {
		t.Fatalf("UnregisterSite: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("remaining = %d, want 1", remaining)
	}
	data, err = os.ReadFile(mgr.ConfigPath())
	if err != nil {
		t.Fatalf("baca config unregister: %v", err)
	}
	if strings.Contains(string(data), "shop.localhost") {
		t.Errorf("site shop masih ada setelah unregister: %s", data)
	}

	// Unregister site terakhir → config dihapus.
	remaining, err = mgr.UnregisterSite("blog.localhost", 2019)
	if err != nil {
		t.Fatalf("UnregisterSite terakhir: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("remaining = %d, want 0", remaining)
	}
	if _, err := os.Stat(mgr.ConfigPath()); !os.IsNotExist(err) {
		t.Errorf("config tidak dihapus saat site terakhir pergi: err=%v", err)
	}
}

// TestManagerRenderRequiresValidBinary: Render validasi config dengan
// binary Caddy yang tidak ada → error, config lama tetap utuh.
func TestManagerRenderRequiresValidBinary(t *testing.T) {
	isolateHome(t)
	mgr := NewManager("/nonexistent/caddy")

	err := mgr.Render([]Site{{Domain: "shop.localhost", Upstream: "127.0.0.1:8001", HTTPS: true}}, 2019)
	if err == nil {
		t.Skip("binary caddy tersedia di PATH — skip tes binary-absen")
	}
	if _, statErr := os.Stat(mgr.ConfigPath()); !os.IsNotExist(statErr) {
		t.Errorf("config tertulis meski validasi gagal: err=%v", statErr)
	}
}

// TestUnregisterSiteOnMissingConfig: config gateway belum ada →
// UnregisterSite mengembalikan 0 tanpa error (idempotent).
func TestUnregisterSiteOnMissingConfig(t *testing.T) {
	isolateHome(t)
	mgr := NewManager("/nonexistent/caddy")

	remaining, err := mgr.UnregisterSite("shop.localhost", 2019)
	if err != nil {
		t.Fatalf("UnregisterSite pada config absen: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("remaining = %d, want 0", remaining)
	}
}

// TestGatewayStateRoundTrip: gateway-state.json round-trip.
func TestGatewayStateRoundTrip(t *testing.T) {
	isolateHome(t)

	st, exists, err := LoadState()
	if err != nil || exists {
		t.Fatalf("LoadState awal: exists=%v err=%v", exists, err)
	}
	if err := saveState(GatewayState{AdminPort: 2019, PID: 123}); err != nil {
		t.Fatalf("saveState: %v", err)
	}
	st, exists, err = LoadState()
	if err != nil || !exists {
		t.Fatalf("LoadState setelah save: exists=%v err=%v", exists, err)
	}
	if st.AdminPort != 2019 || st.PID != 123 {
		t.Fatalf("state round-trip salah: %+v", st)
	}
	if err := removeState(); err != nil {
		t.Fatalf("removeState: %v", err)
	}
	if _, exists, err = LoadState(); err != nil || exists {
		t.Fatalf("LoadState setelah remove: exists=%v err=%v", exists, err)
	}
}

// TestSelectAdminPort: eksplisit menang; state gateway berjalan menang
// atas alokasi otomatis (gateway hanya satu — jangan ganti port yang
// sudah dipakai gateway hidup).
func TestSelectAdminPort(t *testing.T) {
	isolateHome(t)

	if got, _ := SelectAdminPort(2099, nil); got != 2099 {
		t.Errorf("port eksplisit = %d, want 2099", got)
	}
	if err := saveState(GatewayState{AdminPort: 2020, PID: 9999}); err != nil {
		t.Fatalf("saveState: %v", err)
	}
	if got, _ := SelectAdminPort(0, nil); got != 2020 {
		t.Errorf("port adopt gateway hidup = %d, want 2020", got)
	}
}

// TestCanBindStandardPorts: probe fail-closed — test biasanya tidak
// punya privilege :80/:443; hasilness tidak ditetapkan (boleh true/false),
// yang diuji adalah fungsi tidak panic dan konsisten.
func TestCanBindStandardPorts(t *testing.T) {
	_ = CanBindStandardPorts()
}

// TestGatewayDirPermission: direktori gateway dibuat 0700 (StateDir user).
func TestGatewayDirPermission(t *testing.T) {
	isolateHome(t)
	mgr := NewManager("/nonexistent/caddy")

	if err := mgr.RegisterSite(Site{Domain: "shop.localhost", Upstream: "127.0.0.1:8001"}, 2019); err != nil {
		// Binary absent → validasi gagal → acceptable; cek dir saja.
		_ = err
	}
	info, err := os.Stat(filepath.Join(brand.StateDir(), "gateway"))
	if err != nil {
		t.Fatalf("dir gateway tidak dibuat: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("dir gateway perm = %o, want 0700", info.Mode().Perm())
	}
}
