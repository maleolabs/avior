// Package state — project index (Phase 2): daftar proyek + alokasi port
// yang diketahui, ditulis atomik di bawah lock global. Index adalah cache
// konteks (nama/domain/ports) untuk monitor & selector; kebenaran liveness
// tetap di runtime-state-<token>.json.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/maleolabs/avior/internal/brand"
)

// IndexVersion adalah versi skema ProjectIndex saat ini.
const IndexVersion = 1

// ProjectIndex adalah daftar seluruh proyek yang pernah di-serve.
type ProjectIndex struct {
	Version   int            `json:"version"`
	UpdatedAt time.Time      `json:"updated_at"`
	Projects  []ProjectEntry `json:"projects"`
}

// ProjectEntry adalah satu baris index: identitas + alokasi terakhir.
type ProjectEntry struct {
	Token    string    `json:"token"`
	Root     string    `json:"root"`
	Name     string    `json:"name"`
	Domain   string    `json:"domain"`
	Ports    PortSet   `json:"ports"`
	LastPID  int       `json:"last_pid"`
	LastSeen time.Time `json:"last_seen"`
}

var indexLockName = brand.Name + ".lock"

func lockIndex() (func(), error) { return lockFile(indexLockName) }

// IndexPath mengembalikan lokasi projects.json di StateDir.
func IndexPath() string {
	return filepath.Join(brandDir(), "projects.json")
}

// brandDir mengembalikan StateDir (dibungkus agar testable via HOME override).
// Dipakai index.go dan lock.go.
func brandDir() string {
	return brand.StateDir()
}

// LoadIndex membaca projects.json. exists=false (error nil) saat belum ada.
func LoadIndex() (*ProjectIndex, bool, error) {
	path := IndexPath()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("index: baca: %w", err)
	}
	var idx ProjectIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, false, fmt.Errorf("index: decode: %w", err)
	}
	return &idx, true, nil
}

// UpsertProject menambah/memperbarui entry index secara atomik di bawah
// lock global. Entry di-identifikasi oleh token.
func UpsertProject(entry ProjectEntry) error {
	unlock, err := lockIndex()
	if err != nil {
		return fmt.Errorf("index: lock: %w", err)
	}
	defer unlock()
	idx, _, err := LoadIndex()
	if err != nil {
		return err
	}
	if idx == nil {
		idx = &ProjectIndex{Version: IndexVersion}
	}
	if entry.Token == "" {
		return fmt.Errorf("index: entry tanpa token")
	}
	if entry.LastSeen.IsZero() {
		entry.LastSeen = time.Now().UTC()
	}
	found := false
	for i, p := range idx.Projects {
		if p.Token == entry.Token {
			if entry.Root != "" {
				idx.Projects[i].Root = entry.Root
			}
			if entry.Name != "" {
				idx.Projects[i].Name = entry.Name
			}
			if entry.Domain != "" {
				idx.Projects[i].Domain = entry.Domain
			}
			if entry.Ports != (PortSet{}) {
				idx.Projects[i].Ports = entry.Ports
			}
			if entry.LastPID != 0 {
				idx.Projects[i].LastPID = entry.LastPID
			}
			idx.Projects[i].LastSeen = entry.LastSeen
			found = true
			break
		}
	}
	if !found {
		idx.Projects = append(idx.Projects, entry)
	}
	idx.Version = IndexVersion
	idx.UpdatedAt = time.Now().UTC()
	return saveIndex(idx)
}

// RemoveProject menghapus entry token dari index (idempoten).
func RemoveProject(token string) error {
	unlock, err := lockIndex()
	if err != nil {
		return fmt.Errorf("index: lock: %w", err)
	}
	defer unlock()
	idx, exists, err := LoadIndex()
	if err != nil || !exists {
		return err
	}
	kept := idx.Projects[:0]
	found := false
	for _, p := range idx.Projects {
		if p.Token == token {
			found = true
			continue
		}
		kept = append(kept, p)
	}
	if !found {
		return nil
	}
	idx.Projects = kept
	idx.UpdatedAt = time.Now().UTC()
	return saveIndex(idx)
}

func saveIndex(idx *ProjectIndex) error {
	path := IndexPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("index: mkdir: %w", err)
	}
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("index: encode: %w", err)
	}
	return saveAtomic(path, data, 0o600)
}

// FindProject mencari entry index by token/root/name (case-insensitive
// untuk name). Root dicocokkan terlebih dahulu kanonikal.
func FindProject(selector string) (ProjectEntry, bool, error) {
	idx, exists, err := LoadIndex()
	if err != nil || !exists {
		return ProjectEntry{}, false, err
	}
	if canon, cerr := CanonicalRoot(selector); cerr == nil {
		for _, p := range idx.Projects {
			if p.Root == canon || p.Root == selector {
				return p, true, nil
			}
		}
	}
	for _, p := range idx.Projects {
		if p.Token == selector {
			return p, true, nil
		}
	}
	lower := selector
	for _, p := range idx.Projects {
		if p.Name == lower {
			return p, true, nil
		}
	}
	return ProjectEntry{}, false, nil
}

// UsedPorts mengembalikan kumpulan port yang sudah dialokasikan ke proyek
// lain (untuk deteksi collision multi-project). Entry sendiri (selfToken)
// dikecualikan.
func UsedPorts(selfToken string) map[int]bool {
	used := map[int]bool{}
	idx, exists, err := LoadIndex()
	if err != nil || !exists {
		return used
	}
	for _, p := range idx.Projects {
		if p.Token == selfToken {
			continue
		}
		for _, port := range []int{p.Ports.App, p.Ports.Vite, p.Ports.Caddy, p.Ports.CaddyAdmin} {
			if port != 0 {
				used[port] = true
			}
		}
	}
	return used
}
