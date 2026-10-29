//go:build !windows

package processgroup

func identityUnavailableOwnerCleanupAllowed() bool { return true }
