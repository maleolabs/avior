package state

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRuntimeStatePathKeyedByRoot: dua root berbeda menghasilkan dua file
// state berbeda (isolasi multi-project).
func TestRuntimeStatePathKeyedByRoot(t *testing.T) {
	isolateHome(t)
	a, _, err := Paths("/home/dev/app-a")
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := Paths("/home/dev/app-b")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatalf("root berbeda menghasilkan state file sama: %s", a)
	}
	if filepath.Dir(a) != filepath.Dir(b) {
		t.Fatalf("state file tidak satu dir: %s vs %s", a, b)
	}
}

// TestWriteReadStateV2: round-trip state v2 membawa identitas + port set.
func TestWriteReadStateV2(t *testing.T) {
	isolateHome(t)
	path, _, err := Paths("/home/dev/app")
	if err != nil {
		t.Fatal(err)
	}
	want := RuntimeState{
		Version:        StateVersion,
		ProjectToken:   "abc123def456",
		ProjectRoot:    "/home/dev/app",
		ProjectName:    "app",
		Domain:         "app.localhost",
		Port:           8012,
		CaddyAdminPort: 2027,
		Ports:          PortSet{App: 8012, Vite: 5189, Caddy: 8090, CaddyAdmin: 2027},
		MainPID:        Process{PID: 4242, Command: "/usr/bin/php"},
	}
	if err := SaveState(path, want); err != nil {
		t.Fatal(err)
	}
	got, exists, err := LoadState(path)
	if err != nil || !exists {
		t.Fatalf("LoadState: exists=%v err=%v", exists, err)
	}
	if got.Version != StateVersion {
		t.Fatalf("Version = %d, want %d", got.Version, StateVersion)
	}
	if got.ProjectToken != want.ProjectToken || got.ProjectName != want.ProjectName || got.Domain != want.Domain {
		t.Fatalf("identitas hilang: %+v", got)
	}
	if got.Ports != want.Ports {
		t.Fatalf("Ports = %+v, want %+v", got.Ports, want.Ports)
	}
	if got.CaddyAdminPort != want.CaddyAdminPort {
		t.Fatalf("CaddyAdminPort = %d, want %d", got.CaddyAdminPort, want.CaddyAdminPort)
	}
}

// TestReadLegacyStateV1: state v1 (tanpa token/name/ports) tetap terbaca.
func TestReadLegacyStateV1(t *testing.T) {
	isolateHome(t)
	path, _, err := Paths("/home/dev/legacy")
	if err != nil {
		t.Fatal(err)
	}
	legacy := `{"project_root":"/home/dev/legacy","port":8000,"main_pid":{"pid":111,"command":"php","started_at":"2026-10-01T00:00:00Z"},"updated_at":"2026-10-01T00:00:00Z"}`
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(path, legacy); err != nil {
		t.Fatal(err)
	}
	got, exists, err := LoadState(path)
	if err != nil || !exists {
		t.Fatalf("LoadState v1: exists=%v err=%v", exists, err)
	}
	if got.ProjectRoot != "/home/dev/legacy" || got.Port != 8000 || got.MainPID.PID != 111 {
		t.Fatalf("v1 round-trip beda: %+v", got)
	}
	if got.ProjectToken != "" || got.ProjectName != "" || got.Domain != "" {
		t.Fatalf("field v2 harus kosong pada state v1: %+v", got)
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}
