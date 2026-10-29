package runtime

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maleolabs/avior/internal/brand"
	"github.com/maleolabs/avior/internal/state"
)

func TestMonitorAndAttachStayProjectScoped(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	rootA, rootB := filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")
	for i, root := range []string{rootA, rootB} {
		statePath, _, err := state.Paths(root)
		if err != nil {
			t.Fatal(err)
		}
		pid := 2147483647
		if i == 1 {
			pid = os.Getpid()
		}
		if err := state.SaveState(statePath, state.RuntimeState{ProjectRoot: root, Port: 8000, MainPID: state.Process{PID: pid, Command: "php", StartedAt: time.Now()}}); err != nil {
			t.Fatal(err)
		}
	}
	statuses, err := Monitor()
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 2 {
		t.Fatalf("statuses=%d, want 2", len(statuses))
	}
	attached, err := Attach(rootA)
	if err != nil {
		t.Fatal(err)
	}
	if attached.ProjectRoot != rootA || attached.State.Port != 8000 {
		t.Fatalf("attach = %+v", attached)
	}
	if err := Stop(rootA); err != nil {
		t.Fatal(err)
	}
	if _, exists, err := state.LoadState(mustStatePath(t, rootA)); err != nil || exists {
		t.Fatalf("A state remains: exists=%v err=%v", exists, err)
	}
	if _, exists, err := state.LoadState(mustStatePath(t, rootB)); err != nil || !exists {
		t.Fatalf("B state lost: exists=%v err=%v", exists, err)
	}
	if _, err := os.Stat(brand.StateDir()); err != nil {
		t.Fatal(err)
	}
}

func mustStatePath(t *testing.T, root string) string {
	t.Helper()
	path, _, err := state.Paths(root)
	if err != nil {
		t.Fatal(err)
	}
	return path
}
