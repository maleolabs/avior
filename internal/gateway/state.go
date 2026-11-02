package gateway

// Coordinator menyatukan config gateway (Manager) dan proses gateway hidup
// (Process) menjadi satu pintu untuk serve/stop. Semua mutasi config
// gateway diserialisasi lewat state.LockGateway() (lock global lintas
// proses) sehingga beberapa `serve`/`stop` paralel tidak race pada
// Caddyfile gateway atau start/stop proses gateway.
//
// Invariant: gateway Caddy bind :80/:443 (127.0.0.1). Hanya SATU gateway
// per mesin per StateDir; semua proyek aktif share gateway ini. Setiap
// proyek tetap menjalankan runtime pada port tinggi; gateway hanya dipakai
// saat user memilih --clean-url (mode Hybrid — default tetap high-port).
//
// Status gateway dipersisten di StateDir/gateway/gateway-state.json; child
// gateway dicatat di registry global (StateDir/gateway-processes.json)
// agar instance CLI lain dapat menghentikannya dengan verifikasi identitas.

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/maleolabs/avior/internal/brand"
	"github.com/maleolabs/avior/internal/processgroup"
	"github.com/maleolabs/avior/internal/proxy"
	"github.com/maleolabs/avior/internal/state"
)

// GatewayState adalah metadata gateway yang dipersisten.
type GatewayState struct {
	AdminPort int       `json:"admin_port"`
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
}

// statePath lokasi gateway-state.json.
func statePath() string {
	return filepath.Join(brand.StateDir(), "gateway", "gateway-state.json")
}

// LoadState membaca status gateway tersimpan (exists=false bila belum ada).
func LoadState() (GatewayState, bool, error) {
	data, err := os.ReadFile(statePath())
	if os.IsNotExist(err) {
		return GatewayState{}, false, nil
	}
	if err != nil {
		return GatewayState{}, false, fmt.Errorf("gateway: baca state: %w", err)
	}
	var st GatewayState
	if err := json.Unmarshal(data, &st); err != nil {
		return GatewayState{}, false, fmt.Errorf("gateway: decode state: %w", err)
	}
	return st, true, nil
}

func saveState(st GatewayState) error {
	dir := filepath.Dir(statePath())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("gateway: mkdir: %w", err)
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(statePath())+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, statePath())
}

func removeState() error {
	if err := os.Remove(statePath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("gateway: hapus state: %w", err)
	}
	return nil
}

// SelectAdminPort memilih port admin gateway: eksplisit bila non-zero,
// lalu port admin gateway yang sudah dialokasikan (state gateway), lalu
// default 2019 bila bebas, atau fallback unik (menghindari port proyek
// lain yang sedang dipakai). Gateway hanya satu, jadi alokasi sekali.
func SelectAdminPort(explicit int, avoid map[int]bool) (int, error) {
	if explicit > 0 {
		return explicit, nil
	}
	if st, exists, err := LoadState(); err == nil && exists && st.AdminPort > 0 {
		return st.AdminPort, nil
	}
	port, err := proxy.SelectCaddyAdminPortAvoiding(proxy.DefaultAdminPort, avoid)
	if err != nil {
		return 0, fmt.Errorf("gateway: alokasi port admin: %w", err)
	}
	return port, nil
}

// gatewayAlive melaporkan apakah PID di state gateway masih merupakan
// proses Caddy hidup (verifikasi comm + probe admin port).
func gatewayAlive(st GatewayState, binary string) bool {
	if st.PID <= 0 {
		return false
	}
	alive := processgroup.ProcessAlive(state.Process{PID: st.PID, Command: binary, StartedAt: st.StartedAt})
	if !alive {
		return false
	}
	// Setara double-check: admin port yang tercatat harus benar-benar
	// menerima koneksi (membuktikan gateway, bukan proses lain di PID itu).
	l, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(st.AdminPort)))
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}

// EnsureRunning memulai gateway bila belum hidup, lalu menyimpan state.
// Bila gateway-state mencatat PID yang masih cocok dengan proses Caddy
// hidup pada port admin yang sama, gateway dianggap sudah berjalan
// (mis. di-start oleh instance CLI lain) dan tidak di-start ganda.
//
// Pemanggil WAJIB memegang lock global (lihat RegisterSite / StopGateway).
func EnsureRunning(binary string, adminPort int, configPath string) error {
	st, exists, err := LoadState()
	if err != nil {
		return err
	}
	if exists && st.AdminPort == adminPort && gatewayAlive(st, binary) {
		return nil // gateway sudah hidup (dimulai proses CLI lain).
	}
	p := NewProcess(binary, adminPort)
	if err := p.Start(configPath); err != nil {
		return err
	}
	if err := p.WaitReady(gatewayReadyTimeout); err != nil {
		_ = p.Stop()
		return fmt.Errorf("gateway: admin API tidak siap: %w", err)
	}
	id := p.Identity()
	if id.PID == 0 {
		_ = p.Stop()
		return fmt.Errorf("gateway: identitas proses tidak tercatat")
	}
	return saveState(GatewayState{AdminPort: adminPort, PID: id.PID, StartedAt: id.StartedAt})
}

// StopGateway menghentikan gateway lewat registry global (verifikasi
// identitas; bisa menghentikan gateway yang di-start proses CLI lain) dan
// menghapus state. Idempotent.
func StopGateway(binary string) error {
	st, exists, err := LoadState()
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if gatewayAlive(st, binary) {
		if !processgroup.StopManagedProcess(RegistryPath(), st.PID) {
			return fmt.Errorf("gateway: tidak dapat menghentikan proses gateway (identitas tidak cocok)")
		}
	}
	return removeState()
}

// RegisterSite menambah/memperbarui satu site di config gateway, memastikan
// gateway hidup, dan reload config bila gateway sudah berjalan proses lain.
// Aman dipanggil konkuren lintas proses (lock global di dalam).
func RegisterSite(binary string, site Site, adminPort int) error {
	unlock, err := state.LockGateway()
	if err != nil {
		return fmt.Errorf("gateway: lock: %w", err)
	}
	defer unlock()
	mgr := NewManager(binary)
	// Gateway sudah hidup dengan port admin berbeda (start instance CLI
	// lain): adopsi port yang berjalan agar tidak start ganda bentrok :80.
	if st, exists, lerr := LoadState(); lerr == nil && exists && gatewayAlive(st, binary) && st.AdminPort != adminPort {
		adminPort = st.AdminPort
	}
	if err := mgr.RegisterSite(site, adminPort); err != nil {
		return err
	}
	return EnsureRunning(binary, adminPort, mgr.ConfigPath())
}

// UnregisterSite menghapus satu site dari config gateway. Bila tidak ada
// lagi site tersisa, gateway dihentikan dan state dibersihkan. Mengembalikan
// jumlah site tersisa (0 = gateway berhenti).
func UnregisterSite(binary, domain string, adminPort int) (int, error) {
	unlock, err := state.LockGateway()
	if err != nil {
		return 0, fmt.Errorf("gateway: lock: %w", err)
	}
	defer unlock()
	mgr := NewManager(binary)
	remaining, err := mgr.UnregisterSite(domain, adminPort)
	if err != nil {
		return 0, err
	}
	if remaining == 0 {
		return 0, StopGateway(binary)
	}
	// Gateway hidup: update config aktifnya (reload lewat admin API).
	if st, exists, lerr := LoadState(); lerr == nil && exists {
		p := NewProcess(binary, st.AdminPort)
		if err := p.Reload(mgr.ConfigPath()); err != nil {
			return remaining, err
		}
	}
	return remaining, nil
}

// CanBindStandardPorts reports whether this process can bind :80/:443.
// Fail-closed: when unsure, false (caller keeps high-port default).
func CanBindStandardPorts() bool {
	for _, port := range []int{443, 80} {
		l, err := netListen(port)
		if err != nil {
			return false
		}
		_ = l.Close()
	}
	return true
}

const gatewayReadyTimeout = 20 * time.Second
