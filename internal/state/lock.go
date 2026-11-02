package state

import (
	"os"
	"path/filepath"
)

// lockFile adalah lock global lintas proses di StateDir (flock unix /
// LockFileEx windows). Dipakai untuk operasi yang harus diserialisasi
// lintas project: index write, alokasi port (Phase 3), hosts write.
func lockFile(name string) (func(), error) {
	dir := brandDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, name)
	return lockIndexFile(path)
}
