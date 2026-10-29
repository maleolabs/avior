// Package processgroup adalah layer proses (arc:avior-architecture): registry
// proses identity-checked, privilege (sudo preflight), signal/process-group,
// dan supervisor single-wait-ownership. Semua aksi destruktif (kill) WAJIB
// melewati verifikasi identitas — PID bisa dipakai ulang OS.
//
// Registry disimpan per-proyek: pemanggil (orchestrator runtime) memberikan
// path dari state.Paths(projectRoot) — tidak ada registry global lintas
// proyek. Package ini tidak memilih path sendiri.
package processgroup

// Registry adalah mekanisme crash-recovery best-effort, BUKAN otoritas
// kepemilikan proses: setiap cleanup wajib melewati verifikasi identitas
// (executable + cmdline; Unix juga start time via kill(pid,0) semantics)
// sebelum mengirim sinyal apa pun.
import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/maleolabs/avior/internal/state"
)

type processRecord struct {
	PID             int       `json:"pid"`
	Command         string    `json:"command"`
	Executable      string    `json:"executable"`
	Identity        string    `json:"identity"`
	CreatedAt       time.Time `json:"created_at"`
	OwnerPID        int       `json:"owner_pid"`
	OwnerExecutable string    `json:"owner_executable"`
	OwnerIdentity   string    `json:"owner_identity"`
}

var registryMu sync.Mutex
var chmodRegistryTemp = func(file *os.File, mode os.FileMode) error { return file.Chmod(mode) }

// RegisterProcess mencatat child ke registry (path = file registry
// per-proyek, mis. dari state.Paths) dan mengembalikan fungsi unregister.
// Unregister hanya menghapus registration yang persis sama: matching
// hanya-by-PID tidak aman saat OS memakai ulang PID sebelum deferred cleanup
// berjalan.
func RegisterProcess(path, cmd string, pid int) (func() error, error) {
	if path == "" {
		return nil, os.ErrInvalid
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	unlock, err := lockRegistry(path)
	if err != nil {
		return nil, err
	}
	defer unlock()
	records, err := readProcessRegistry(path)
	if err != nil {
		return nil, err
	}
	exe, identity := processIdentity(pid)
	ownerExe, ownerIdentity := processIdentity(os.Getpid())
	record := processRecord{PID: pid, Command: cmd, Executable: exe, Identity: identity, CreatedAt: time.Now(), OwnerPID: os.Getpid(), OwnerExecutable: ownerExe, OwnerIdentity: ownerIdentity}
	records = append(records, record)
	if err := writeProcessRegistry(path, records); err != nil {
		return nil, err
	}
	return func() error { return unregisterProcess(path, record) }, nil
}

func unregisterProcess(path string, record processRecord) error {
	if path == "" {
		return os.ErrInvalid
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	unlock, err := lockRegistry(path)
	if err != nil {
		return err
	}
	defer unlock()
	records, err := readProcessRegistry(path)
	if err != nil {
		return err
	}
	kept := records[:0]
	for _, r := range records {
		if r.PID != record.PID || r.Executable != record.Executable || r.Identity != record.Identity || r.Command != record.Command || !r.CreatedAt.Equal(record.CreatedAt) {
			kept = append(kept, r)
		}
	}
	if len(kept) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	} else {
		if err := writeProcessRegistry(path, kept); err != nil {
			return err
		}
	}
	return nil
}

// StopManagedProcess hanya mengirim sinyal bila identitas proses yang
// tercatat masih cocok dengan proses hidup pada PID tersebut.
func StopManagedProcess(path string, pid int) bool {
	if path == "" {
		return false
	}
	registryMu.Lock()
	unlock, err := lockRegistry(path)
	if err != nil {
		registryMu.Unlock()
		return false
	}
	records, err := readProcessRegistry(path)
	unlock()
	registryMu.Unlock()
	if err != nil {
		return false
	}
	for _, r := range records {
		if r.PID == pid {
			if !recordMatchesProcess(r) {
				return false
			}
			terminatePID(pid)
			removeExactRecords(path, []processRecord{r})
			return true
		}
	}
	return false
}

// StopManagedRuntime menghentikan owner runtime dan semua child yang
// tercatat atas namanya. Owner dan child diverifikasi identitasnya sebelum
// disinyal — state lama (atau PID reuse) tidak akan menghentikan proses
// orang lain.
func StopManagedRuntime(path string, ownerPID int) bool {
	if path == "" {
		return false
	}
	registryMu.Lock()
	unlock, err := lockRegistry(path)
	if err != nil {
		registryMu.Unlock()
		return false
	}
	records, err := readProcessRegistry(path)
	unlock()
	registryMu.Unlock()
	if err != nil {
		return false
	}
	ownerExe, ownerIdentity := processIdentity(ownerPID)
	if ownerExe == "" || ownerIdentity == "" {
		return false
	}
	owned := recordsForOwner(records, ownerPID, ownerExe, ownerIdentity)
	matched := false
	for _, r := range owned {
		if !recordMatchesProcess(r) {
			continue
		}
		terminatePID(r.PID)
		matched = true
	}
	if matched {
		// Biarkan owner menjalankan context-cancel dan cleanup normalnya.
		terminatePID(ownerPID)
		removeExactRecords(path, owned)
	}
	return matched
}

// ProcessAlive reports whether recorded process identity still matches live PID.
func ProcessAlive(process state.Process) bool {
	if process.PID <= 0 || process.Command == "" || process.StartedAt.IsZero() {
		return false
	}
	exe, _ := processIdentity(process.PID)
	return exe != "" && executablesMatch(exe, process.Command)
}

// removeExactRecords avoids holding registry locks during process termination.
// Snapshot records are removed only if unchanged; concurrent registrations survive.
func removeExactRecords(path string, targets []processRecord) {
	registryMu.Lock()
	defer registryMu.Unlock()
	unlock, err := lockRegistry(path)
	if err != nil {
		return
	}
	defer unlock()
	records, err := readProcessRegistry(path)
	if err != nil {
		return
	}
	kept := records[:0]
	for _, r := range records {
		remove := false
		for _, target := range targets {
			if r.PID == target.PID && r.Executable == target.Executable && r.Identity == target.Identity && r.Command == target.Command && r.CreatedAt.Equal(target.CreatedAt) && r.OwnerPID == target.OwnerPID && r.OwnerExecutable == target.OwnerExecutable && r.OwnerIdentity == target.OwnerIdentity {
				remove = true
				break
			}
		}
		if !remove {
			kept = append(kept, r)
		}
	}
	if len(kept) == 0 {
		_ = os.Remove(path)
	} else {
		_ = writeProcessRegistry(path, kept)
	}
}

// CleanupStaleProcesses memulihkan child hanya bila owner-nya pasti sudah
// mati. Identitas owner dan child keduanya diverifikasi agar PID reuse tidak
// mengubah startup cleanup menjadi operasi destruktif.
func CleanupStaleProcesses(path string) error {
	if path == "" {
		return os.ErrInvalid
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	unlock, err := lockRegistry(path)
	if err != nil {
		return err
	}
	defer unlock()
	records, err := readProcessRegistry(path)
	if err != nil {
		return err
	}
	kept := records[:0]
	for _, r := range records {
		if !ownerDefinitelyGone(r) {
			kept = append(kept, r)
			continue
		}
		// Child hilang/tidak cocok aman dilupakan. Child yang cocok hanya
		// dihentikan SETELAH cek owner di atas berhasil.
		if recordMatchesProcess(r) {
			terminatePID(r.PID)
		}
	}
	if len(kept) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	} else {
		if err := writeProcessRegistry(path, kept); err != nil {
			return err
		}
	}
	return nil
}

func recordsForOwner(records []processRecord, ownerPID int, ownerExecutable, ownerIdentity string) []processRecord {
	owned := make([]processRecord, 0)
	for _, r := range records {
		if r.OwnerPID == ownerPID && r.OwnerExecutable == ownerExecutable && r.OwnerIdentity == ownerIdentity {
			owned = append(owned, r)
		}
	}
	return owned
}

func readProcessRegistry(path string) ([]processRecord, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var records []processRecord
	if len(b) != 0 {
		if err := json.Unmarshal(b, &records); err != nil {
			return nil, err
		}
	}
	return records, nil
}

func writeProcessRegistry(path string, records []processRecord) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".runtime-processes-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := chmodRegistryTemp(tmp, 0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err = tmp.Write(b); err == nil {
		err = tmp.Close()
	} else {
		_ = tmp.Close()
	}
	if err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func ownerDefinitelyGone(r processRecord) bool {
	if r.OwnerPID <= 0 || r.OwnerExecutable == "" || r.OwnerIdentity == "" {
		return false // fail closed, termasuk record legacy/Windows
	}
	exe, identity := processIdentity(r.OwnerPID)
	if exe == "" || identity == "" {
		if !identityUnavailableOwnerCleanupAllowed() {
			return false
		}
		return processGone(r.OwnerPID)
	}
	// Identitas berbeda berarti owner asli sudah mati dan PID dipakai ulang;
	// itu bukan runtime aktif.
	return !executablesMatch(exe, r.OwnerExecutable) || identity != r.OwnerIdentity
}

func processCommand(pid int) (string, error) { return strings.TrimSpace(processCommandLine(pid)), nil }

func recordMatchesProcess(r processRecord) bool {
	exe, identity := processIdentity(r.PID)
	if exe == "" || identity == "" || !executablesMatch(exe, r.Executable) || identity != r.Identity {
		return false
	}
	// Windows tidak bisa mengambil command line secara portabel dengan
	// implementasi saat ini. Identity tetap PID-scoped di sana; Unix
	// memverifikasi command line juga.
	command, err := processCommand(r.PID)
	return err == nil && command != "" && r.Command != "" && commandsMatch(command, r.Command)
}

// Field comm milik ps sengaja pendek dan hanya melaporkan nama proses,
// sedangkan registry menyimpan path/nama yang diberikan ke exec.Command.
// Keduanya representasi berbeda dari executable yang sama (/usr/bin/php vs
// php). Bandingkan basename, bukan format tampilan.
func executablesMatch(actual, recorded string) bool {
	actual = strings.TrimSpace(actual)
	recorded = strings.TrimSpace(recorded)
	if actual == "" || recorded == "" {
		return false
	}
	return filepath.Base(actual) == filepath.Base(recorded)
}

// ps melaporkan command memakai path executable yang di-resolve kernel,
// sedangkan registry menyimpan path/nama yang diberikan ke exec.Command.
// Bandingkan token executable per basename, argumen tetap exact; ini
// menghindari stale-state palsu sekaligus match-by-PID yang terlalu luas.
func commandsMatch(actual, recorded string) bool {
	a := strings.Fields(strings.TrimSpace(actual))
	r := strings.Fields(strings.TrimSpace(recorded))
	if len(a) == 0 || len(r) == 0 || filepath.Base(a[0]) != filepath.Base(r[0]) {
		return false
	}
	if len(a) != len(r) {
		return false
	}
	for i := 1; i < len(a); i++ {
		if a[i] != r[i] {
			return false
		}
	}
	return true
}
