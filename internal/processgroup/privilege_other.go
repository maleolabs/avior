//go:build !linux

package processgroup

// Platform selain Linux menangani privilege via jalurnya sendiri
// (UAC di Windows, keychain di macOS); tidak ada preflight sudo.
func AuthorizeSudo() error { return nil }
