//go:build !windows

package processgroup

// ownerCleanupWithoutIdentity: Unix boleh membersihkan record owner yang
// PID-nya hilang meski identitas tidak terbaca (kill -0 sudah membedakan
// ESRCH/EPERM); proses hidup tidak pernah dianggap hilang.
func ownerCleanupWithoutIdentity() bool { return true }

// processCommandLineOptional: Unix menyediakan command line proses lain
// (ps), jadi record wajib memverifikasinya.
func processCommandLineOptional() bool { return false }
