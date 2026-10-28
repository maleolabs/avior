// Package state adalah runtime state per-proyek (arc:avior-architecture,
// adr:per-project-runtime-state). Tidak ada state global lintas proyek:
// semua file state di-key per root proyek memakai token deterministik
// (hash 8 hex pertama dari path absolut proyek), sehingga dua checkout
// paralel tidak saling menimpa state, dan stop/attach/monitor hanya bisa
// menargetkan runtime milik proyek itu sendiri. Semua file berada di
// user-level dir (internal/brand.StateDir), tidak pernah di dalam proyek.
package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/maleolabs/avior/internal/brand"
)

// Process adalah identitas satu proses terkelola. Konvensi registry:
// JANGAN pernah kirim sinyal ke PID tanpa verifikasi identitas (Command +
// StartedAt) — PID bisa dipakai ulang OS untuk proses lain.
type Process struct {
	PID       int       `json:"pid"`
	Command   string    `json:"command"`    // path executable; diverifikasi sebelum kill
	StartedAt time.Time `json:"started_at"` // deteksi PID reuse
}

// RuntimeState adalah metadata runtime satu proyek. Nilai nol (MainPID.PID
// == 0) berarti tidak ada runtime berjalan.
type RuntimeState struct {
	ProjectRoot string    `json:"project_root"`
	Port        int       `json:"port,omitempty"`
	MainPID     Process   `json:"main_pid"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Paths mengembalikan lokasi kedua file state untuk projectRoot:
// runtime-state-<sha8>.json dan runtime-processes-<sha8>.json di dalam
// brand.StateDir(). Path relatif dinormalisasi ke absolut dulu, jadi root
// yang sama selalu menghasilkan token yang sama.
func Paths(projectRoot string) (statePath, processesPath string, err error) {
	abs, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", "", fmt.Errorf("state: abs path: %w", err)
	}
	sum := sha256.Sum256([]byte(abs))
	token := hex.EncodeToString(sum[:8]) // ponytail: 8 hex cukup unik antar checkout; naik ke 16 jika tabrakan nyata
	dir := brand.StateDir()
	return filepath.Join(dir, "runtime-state-"+token+".json"),
		filepath.Join(dir, "runtime-processes-"+token+".json"), nil
}

// SaveState menulis st ke statePath secara atomik (temp file + rename di
// dir yang sama). Perm 0600: file berisi PID dan path proses milik user;
// least privilege, tidak ada alasan dibaca user lain.
func SaveState(statePath string, st RuntimeState) error {
	st.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("state: encode: %w", err)
	}
	return saveAtomic(statePath, data, 0o600)
}

// LoadState membaca state dari statePath. exists=false (dengan error nil)
// saat file belum ada — kondisi normal "belum ada runtime".
func LoadState(statePath string) (st RuntimeState, exists bool, err error) {
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
// atomik pada filesystem yang sama), sync, chmod, lalu rename.
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
