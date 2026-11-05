//go:build windows

package processgroup

// ownerCleanupWithoutIdentity: Windows tidak boleh membersihkan record
// owner hanya dari eksistensi PID — identitas kosong (akses ditolak /
// proses sesi lain) harus fail closed, bukan ditarik ke cleanup.
// Catatan: processIdentity natif (QueryFullProcessImageNameW +
// GetProcessTimes) sekarang menyediakan identitas; helper ini hanya
// menjaga fallback saat identitas tidak terbaca.
func ownerCleanupWithoutIdentity() bool { return false }

// processCommandLineOptional: command line proses lain tidak tersedia
// secara portabel di Windows; identitas (executable + creation time)
// sudah PID-scoped.
func processCommandLineOptional() bool { return true }
