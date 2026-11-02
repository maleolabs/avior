package gateway

// Process mengelola hidup-mati satu instance Caddy gateway: start, reload
// (via admin API — zero downtime, bekerja lintas proses CLI), dan stop.
// Caddy dipicu lewat `caddy run --config <Caddyfile> --adapter caddyfile`
// sebagai child processgroup.
//
// Child gateway dicatat di registry PROSES GLOBAL (bukan per-proyek) agar
// instance CLI lain bisa menghentikannya dengan verifikasi identitas
// (processgroup.StopManagedProcess) saat proyek terakhir berhenti.

import (
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/maleolabs/avior/internal/brand"
	"github.com/maleolabs/avior/internal/processgroup"
	"github.com/maleolabs/avior/internal/proxy"
	"github.com/maleolabs/avior/internal/state"
)

// RegistryPath adalah lokasi registry proses gateway (global, lintas
// proyek): StateDir/gateway-processes.json.
func RegistryPath() string {
	return filepath.Join(brand.StateDir(), "gateway-processes.json")
}

// Process adalah controller satu gateway Caddy hidup dalam proses ini.
type Process struct {
	binary    string
	adminPort int
	sup       *processgroup.Supervised
	mu        sync.Mutex
}

// NewProcess membuat controller gateway untuk binary + port admin yang
// sudah dialokasikan.
func NewProcess(binary string, adminPort int) *Process {
	return &Process{binary: binary, adminPort: adminPort}
}

// AdminPort mengembalikan port admin API gateway (probe kesiapan).
func (p *Process) AdminPort() int { return p.adminPort }

// Start menjalankan gateway dengan config di configPath. Bila gateway
// proses ini sudah hidup, Start tidak melakukan apa-apa (idempotent).
// Child terdaftar di registry global → StopGateway lintas proses aman.
func (p *Process) Start(configPath string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sup != nil && !p.exited() {
		return nil // gateway proses ini masih hidup: jangan start ganda.
	}
	cmd := exec.Command(p.binary, "run", "--config", configPath, "--adapter", "caddyfile")
	sup, err := processgroup.StartCmd(cmd)
	if err != nil {
		return fmt.Errorf("gateway: start caddy: %w", err)
	}
	if _, err := processgroup.RegisterProcess(RegistryPath(), cmd.String(), sup.PID()); err != nil {
		_ = sup.Terminate(stopGrace, stopForce)
		return fmt.Errorf("gateway: daftar proses: %w", err)
	}
	p.sup = sup
	return nil
}

// Reload menjalankan `caddy reload` satu kali terhadap admin API gateway.
// Berkerja lintas proses CLI (subprocess hanya memanggil admin API; tidak
// perlu handle child) dan zero-downtime: Caddy mengganti config tanpa
// menjatuhkan listener.
func (p *Process) Reload(configPath string) error {
	cmd := exec.Command(p.binary, "reload", "--config", configPath, "--adapter", "caddyfile",
		"--address", net.JoinHostPort("127.0.0.1", strconv.Itoa(p.adminPort)))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("gateway: reload: %w: %s", err, truncate(string(out)))
	}
	return nil
}

// Stop menghentikan gateway yang di-start proses ini. Idempotent. Untuk
// menghentikan gateway yang di-start proses CLI lain, pakai StopGateway
// (verifikasi identitas via registry global).
func (p *Process) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sup == nil {
		return nil
	}
	if !p.exited() {
		p.sup.Terminate(stopGrace, stopForce)
		_ = p.sup.Wait()
	}
	p.sup = nil
	return nil
}

// Running melaporkan gateway (proses ini) masih hidup.
func (p *Process) Running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sup != nil && !p.exited()
}

func (p *Process) exited() bool {
	if p.sup == nil {
		return true
	}
	select {
	case <-p.sup.Reaped():
		return true
	default:
		return false
	}
}

// WaitReady memblokir hingga admin API gateway menerima koneksi.
func (p *Process) WaitReady(timeout time.Duration) error {
	return proxy.WaitReady("127.0.0.1", p.adminPort, timeout)
}

// Identity mengembalikan identitas proses gateway proses ini (PID=0 bila
// tidak hidup) — disimpan ke gateway-state.json untuk diagnostik.
func (p *Process) Identity() state.Process {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sup == nil {
		return state.Process{}
	}
	return p.sup.Identity()
}

func truncate(s string) string {
	const max = 300
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}

const (
	stopGrace = 5 * time.Second
	stopForce = 2 * time.Second
)
