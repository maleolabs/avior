package brand

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestNoLiteralOutsideBrand dijalankan via eksternal; test ini mengunci
// nilai kontrak brand itu sendiri.
func TestContractValues(t *testing.T) {
	if Name != "avior" {
		t.Fatalf("Name = %q, want avior", Name)
	}
	if !strings.Contains(StateDir(), Name) {
		t.Fatalf("StateDir() = %q, harus mengandung nama tool", StateDir())
	}
	if got := ArchivePrefix(); got != "avior-" {
		t.Fatalf("ArchivePrefix() = %q", got)
	}
	_ = filepath.Join // keep import stable
}

func TestHostsMarkerFormat(t *testing.T) {
	if !strings.HasPrefix(HostsMarker, "# ") {
		t.Fatalf("HostsMarker harus komentar: %q", HostsMarker)
	}
}
