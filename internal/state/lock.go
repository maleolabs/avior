package state

import (
	"os"
	"path/filepath"

	"github.com/maleolabs/avior/internal/brand"
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

// LockTrust mengunci operasi trust (instalasi CA / NSS DB) lintas proses.
// Dipakai internal/trust agar beberapa serve bersamaan tidak race pada
// root CA atau database NSS browser.
// LockTrust mengunci operasi trust (instalasi CA / NSS DB) lintas proses.
func LockTrust() (func(), error) { return lockFile(brand.Name + "-trust.lock") }

// LockHosts mengunci tulis /etc/hosts lintas proses (multi-project).
func LockHosts() (func(), error) { return lockFile(brand.Name + "-hosts.lock") }
