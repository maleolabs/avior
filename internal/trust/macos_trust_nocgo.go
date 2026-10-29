//go:build darwin && !cgo

package trust

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func installMacOSTrust(path string) error {
	if _, err := macOSCertificate(path); err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve macOS home directory: %w", err)
	}
	keychain := filepath.Join(home, "Library", "Keychains", "login.keychain-db")
	if output, err := exec.Command("security", "add-trusted-cert", "-r", "trustRoot", "-k", keychain, path).CombinedOutput(); err != nil {
		return fmt.Errorf("add macOS login Keychain trust: %w: %s", err, output)
	}
	return nil
}

func removeMacOSTrust(string) error { return nil }
