// Package state adalah runtime state per-proyek (arc:avior-architecture,
// adr:per-project-runtime-state). Tidak ada state global lintas proyek:
// semua file state di-key per root proyek memakai token deterministik
// (hash 8 byte pertama / 16 hex dari path absolut proyek), sehingga dua
// checkout paralel tidak saling menimpa state, dan stop/attach/monitor
// hanya bisa menargetkan runtime milik proyek itu sendiri. Semua file
// berada di user-level dir (internal/brand.StateDir), tidak pernah di
// dalam proyek.
package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/maleolabs/avior/internal/brand"
)

// Process adalah identitas satu proses terkelola. Konvensi registry:
// JANGAN pernah kirim sinyal ke PID tanpa verifikasi identitas (Command +
// StartedAt) — PID bisa dipakai ulang OS untuk proses lain.
type Process struct {
	PID       int       `json:"pid"`
	Command   string    `json:"command"`    // path executable; diverifikasi sebelum kill
	StartedAt time.Time `json:"started_at"` // WAJIB diambil spawner saat spawn (sebelum SaveState), bukan saat save
}

// PortSet menyimpan alokasi port per proyek (Phase 3 allocator).
type PortSet struct {
	App        int `json:"app,omitempty"`
	Vite       int `json:"vite,omitempty"`
	Caddy      int `json:"caddy,omitempty"`
	CaddyAdmin int `json:"caddy_admin,omitempty"`
}

// ServiceState menyimpan status satu service child (running/exited/failed).
type ServiceState struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// StateVersion adalah versi skema RuntimeState saat ini.
const StateVersion = 2

// RuntimeState adalah metadata runtime satu proyek. Nilai nol (MainPID.PID
// == 0) berarti tidak ada runtime berjalan. Field dengan omitempty menjaga
// kompatibilitas baca dengan state v1 (tanpa token/root/name/ports).
type RuntimeState struct {
	Version        int            `json:"version,omitempty"`
	ProjectToken   string         `json:"project_token,omitempty"`
	ProjectRoot    string         `json:"project_root"`
	ProjectName    string         `json:"project_name,omitempty"`
	Domain         string         `json:"domain,omitempty"`
	Ports          PortSet        `json:"ports,omitempty"`
	Port           int            `json:"port,omitempty"` // legacy compat (Laravel port)
	CaddyAdminPort int            `json:"caddy_admin_port,omitempty"`
	MainPID        Process        `json:"main_pid"`
	StartedAt      time.Time      `json:"started_at,omitempty"`
	UpdatedAt      time.Time      `json:"updated_at"`
	Heartbeat      time.Time      `json:"heartbeat,omitempty"`
	Services       []ServiceState `json:"services,omitempty"`
}

var stateMu sync.Mutex

// Paths mengembalikan lokasi kedua file state untuk projectRoot:
// runtime-state-<sha8>.json dan runtime-processes-<sha8>.json di dalam
// brand.StateDir(). Token dihitung dari path LITERAL setelah
// filepath.Abs and symlink-resolved when possible, so aliases share runtime.
func Paths(projectRoot string) (statePath, processesPath string, err error) {
	abs, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", "", fmt.Errorf("state: abs path: %w", err)
	}
	abs = filepath.Clean(abs)
	if resolved, resolveErr := filepath.EvalSymlinks(abs); resolveErr == nil {
		abs = resolved
	} else if !os.IsNotExist(resolveErr) {
		// Resolve partial existing path prefixes; retain absolute input if unavailable.
		if resolved, partialErr := resolveExistingPrefix(abs); partialErr == nil {
			abs = resolved
		}
	}
	sum := sha256.Sum256([]byte(abs))
	token := hex.EncodeToString(sum[:12]) // 96-bit (24 hex): collision-safe antar project
	dir := brand.StateDir()
	return filepath.Join(dir, "runtime-state-"+token+".json"),
		filepath.Join(dir, "runtime-processes-"+token+".json"), nil
}

func resolveExistingPrefix(path string) (string, error) {
	missing := []string{}
	for {
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "", err
		}
		missing = append(missing, filepath.Base(path))
		path = parent
	}
}

// SaveState menulis st ke statePath secara atomik (temp file + rename di
// dir yang sama). Perm 0600: file berisi PID dan path proses milik user;
// least privilege, tidak ada alasan dibaca user lain.
func SaveState(statePath string, st RuntimeState) error {
	stateMu.Lock()
	defer stateMu.Unlock()
	unlock, err := lockState(statePath)
	if err != nil {
		return fmt.Errorf("state: lock: %w", err)
	}
	defer unlock()
	if st.Version == 0 {
		st.Version = StateVersion
	}
	now := time.Now().UTC()
	if st.UpdatedAt.IsZero() {
		st.UpdatedAt = now
	} else {
		st.UpdatedAt = now
	}
	if st.Heartbeat.IsZero() {
		st.Heartbeat = now
	}
	if st.StartedAt.IsZero() && st.MainPID.StartedAt.IsZero() == false {
		st.StartedAt = st.MainPID.StartedAt
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("state: encode: %w", err)
	}
	return saveAtomic(statePath, data, 0o600)
}

// LoadState reads state from a clean absolute statePath. It is an unlocked
// snapshot: concurrent writes may replace the file before or after the read.
// exists=false (with nil error) means the file does not exist.
func LoadState(statePath string) (st RuntimeState, exists bool, err error) {
	if !filepath.IsAbs(statePath) || filepath.Clean(statePath) != statePath || filepath.Dir(statePath) == "." {
		return RuntimeState{}, false, fmt.Errorf("state: path must be clean absolute path: %q", statePath)
	}
	data, err := os.ReadFile(statePath)
	if os.IsNotExist(err) {
		return RuntimeState{}, false, nil
	}
	if err != nil {
		return RuntimeState{}, false, fmt.Errorf("state: baca: %w", err)
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return RuntimeState{}, false, fmt.Errorf("state: decode %s: %w", statePath, err)
	}
	return st, true, nil
}

// RemoveStateIfOwner removes state only while it still names this runtime.
func RemoveStateIfOwner(statePath string, owner Process) error {
	stateMu.Lock()
	defer stateMu.Unlock()
	unlock, err := lockState(statePath)
	if err != nil {
		return fmt.Errorf("state: lock: %w", err)
	}
	defer unlock()
	st, exists, err := LoadState(statePath)
	if err != nil || !exists {
		return err
	}
	if st.MainPID != owner {
		return nil
	}
	return os.Remove(statePath)
}

// SaveProcesses menulis registry proses anak (php serve, worker, dll) ke
// processesPath secara atomik. Perm 0644: registry ini murni data diagnostik
// (PID+path) tanpa informasi sensitif, aman dibaca untuk troubleshooting.
func SaveProcesses(processesPath string, procs []Process) error {
	data, err := json.MarshalIndent(procs, "", "  ")
	if err != nil {
		return fmt.Errorf("state: encode: %w", err)
	}
	return saveAtomic(processesPath, data, 0o644)
}

// LoadProcesses membaca registry proses dari processesPath. exists=false
// (dengan error nil) saat file belum ada.
func LoadProcesses(processesPath string) (procs []Process, exists bool, err error) {
	data, err := os.ReadFile(processesPath)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("state: baca: %w", err)
	}
	if err := json.Unmarshal(data, &procs); err != nil {
		return nil, false, fmt.Errorf("state: decode %s: %w", processesPath, err)
	}
	return procs, true, nil
}

// saveAtomic menulis data ke path: temp file di dir yang sama (agar rename
// atomik pada filesystem yang sama), sync, chmod, lalu rename. Di Windows,
// rename menimpa file yang sedang terbuka gagal dengan error (sharing
// violation) — fail closed, file lama tetap utuh.
func saveAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("state: mkdir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("state: temp: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("state: tulis: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("state: sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("state: close: %w", err)
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("state: chmod: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("state: rename: %w", err)
	}
	return nil
}
