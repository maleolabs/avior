//go:build windows

package processgroup

// Lookup identitas Windows sengaja tidak tersedia; jangan pernah menyimpulkan
// kepemilikan hanya dari eksistensi PID.
func identityUnavailableOwnerCleanupAllowed() bool { return false }
