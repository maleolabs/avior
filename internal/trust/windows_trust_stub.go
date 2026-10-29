//go:build !windows

package trust

import "fmt"

func installWindowsTrust(string) error {
	return fmt.Errorf("Windows certificate store unavailable on this platform")
}

func removeWindowsTrust(string) error { return nil }
