//go:build !darwin

package trust

import "fmt"

func installMacOSTrust(string) error {
	return fmt.Errorf("macOS Keychain unavailable on this platform")
}

func removeMacOSTrust(string) error { return nil }
