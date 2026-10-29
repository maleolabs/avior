package brand

import (
	"os"
	"path/filepath"
	"testing"
)

// TestContractValues mengunci nilai kontrak brand (ADR: adr:brand-contract).
func TestContractValues(t *testing.T) {
	if Name != "avior" {
		t.Fatalf("Name = %q, want avior", Name)
	}
	if HostsMarker != "# avior-managed" {
		t.Fatalf("HostsMarker = %q, want %q", HostsMarker, "# avior-managed")
	}
	if CANickname != "Avior Local CA" {
		t.Fatalf("CANickname = %q, want %q", CANickname, "Avior Local CA")
	}
	if got := ArchivePrefix(); got != "avior-" {
		t.Fatalf("ArchivePrefix() = %q, want avior-", got)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("os.UserHomeDir: %v", err)
	}
	if want := filepath.Join(home, ".local", "share", "avior"); StateDir() != want {
		t.Fatalf("StateDir() = %q, want %q", StateDir(), want)
	}
}

func TestHostsMarkerFormat(t *testing.T) {
	if len(HostsMarker) < 3 || HostsMarker[0] != '#' || HostsMarker[1] != ' ' {
		t.Fatalf("HostsMarker harus komentar: %q", HostsMarker)
	}
}
