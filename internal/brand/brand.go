package brand

import (
	"os"
	"path/filepath"
)

// Brand contract (ADR: adr:brand-contract) — satu-satunya sumber identitas
// tool. Dilarang menulis literal nama tool di luar paket ini; dijaga oleh
// test grep pada internal/brand/contract_test.go.

const (
	// Name adalah nama CLI (binary, help, output user-facing).
	Name = "avior"
	// ModulePath adalah module Go; ditinjau ulang saat rename sweep (p2-rename-sweep).
	ModulePath   = "github.com/maleolabs/avior"
	ManifestFile = Name + ".json"
)

// HostsMarker adalah komentar penanda pada /etc/hosts (Windows hosts) yang
// ditulis tool dan dibersihkan exact-line saat proses berhenti.
const HostsMarker = "# avior-managed"

// CANickname adalah nama CA lokal di trust store OS/browser.
const CANickname = "Avior Local CA"

// StateDir mengembalikan direktori state user-level (~/.local/share/avior
// atau ekuivalen per-OS). Satu sumber; jangan duplikat di tempat lain.
func StateDir() string {
	base, err := os.UserHomeDir()
	if err != nil {
		return ".avior" // ponytail: fallback minim; upgrade ke os.UserConfigDir saat p1-foundation
	}
	return filepath.Join(base, ".local", "share", Name)
}

// ArchivePrefix mengembalikan prefix nama arsip distribusi: avior-<version>-<build>.
func ArchivePrefix() string { return Name + "-" }
