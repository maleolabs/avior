package runtime

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maleolabs/avior/internal/state"
)

// TestMonitorListsMultipleProjectsContextual: dua runtime → monitor
// menampilkan name, status, pid, url/ports, root masing-masing.
func TestMonitorListsMultipleProjectsContextual(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := filepath.Join(t.TempDir(), "shop")
	b := filepath.Join(t.TempDir(), "blog")
	for _, root := range []string{a, b} {
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	saveRuntime(t, a, state.RuntimeState{
		Version:        state.StateVersion,
		ProjectToken:   "token-shop",
		ProjectRoot:    a,
		ProjectName:    "shop",
		Domain:         "shop.localhost",
		Ports:          state.PortSet{App: 8012, Caddy: 8090, CaddyAdmin: 2027},
		CaddyAdminPort: 2027,
		MainPID:        state.Process{PID: os.Getpid(), Command: os.Args[0], StartedAt: now},
	})
	saveRuntime(t, b, state.RuntimeState{
		Version:        state.StateVersion,
		ProjectToken:   "token-blog",
		ProjectRoot:    b,
		ProjectName:    "blog",
		Domain:         "blog.localhost",
		Ports:          state.PortSet{App: 8013, Caddy: 8091, CaddyAdmin: 2028},
		CaddyAdminPort: 2028,
		MainPID:        state.Process{PID: os.Getpid(), Command: os.Args[0], StartedAt: now},
	})
	statuses, err := Monitor()
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 2 {
		t.Fatalf("statuses = %d, want 2", len(statuses))
	}
	byName := map[string]ProjectStatus{}
	for _, st := range statuses {
		byName[st.ProjectName] = st
	}
	shop, ok := byName["shop"]
	if !ok {
		t.Fatalf("monitor tidak memuat project shop: %+v", statuses)
	}
	if !shop.Running || shop.Token != "token-shop" {
		t.Fatalf("shop status salah: %+v", shop)
	}
	if shop.URL != "https://shop.localhost:8090" {
		t.Fatalf("shop URL = %q, want https://shop.localhost:8090", shop.URL)
	}
	blog, ok := byName["blog"]
	if !ok {
		t.Fatalf("monitor tidak memuat project blog: %+v", statuses)
	}
	if blog.URL != "https://blog.localhost:8091" {
		t.Fatalf("blog URL = %q, want https://blog.localhost:8091", blog.URL)
	}
}

// TestMonitorToleratesCorruptStateFile: satu state file rusak tidak
// menggagalkan monitor entry lain.
func TestMonitorToleratesCorruptStateFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := filepath.Join(t.TempDir(), "ok")
	b := filepath.Join(t.TempDir(), "corrupt")
	for _, root := range []string{a, b} {
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	saveRuntime(t, a, state.RuntimeState{
		ProjectRoot: a,
		ProjectName: "ok",
		MainPID:     state.Process{PID: os.Getpid(), Command: os.Args[0], StartedAt: time.Now()},
	})
	corruptPath, _, err := state.Paths(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(corruptPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(corruptPath, []byte("{corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	statuses, err := Monitor()
	if err != nil {
		t.Fatalf("Monitor error = %v, want nil", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("statuses = %d, want 1 (entry corrupt di-skip)", len(statuses))
	}
	if statuses[0].ProjectName != "ok" {
		t.Fatalf("entry tersisa salah: %+v", statuses[0])
	}
}

func saveRuntime(t *testing.T, root string, st state.RuntimeState) {
	t.Helper()
	path, _, err := state.Paths(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SaveState(path, st); err != nil {
		t.Fatal(err)
	}
}
