package runtime

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/maleolabs/avior/internal/brand"
	"github.com/maleolabs/avior/internal/processgroup"
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
	if err := state.SaveState(mustStatePath(t, rootA), state.RuntimeState{ProjectRoot: rootA, Port: 9000, MainPID: attached.State.MainPID}); err != nil {
		t.Fatal(err)
	}
	if attached.State.Port != 8000 {
		t.Fatal("attach result changed after state update; want immutable snapshot")
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

func TestStopStopsManagedRuntimeProcess(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	_, registry, err := state.Paths(root)
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	unregister, err := processgroup.RegisterProcess(registry, "sleep 60", cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	defer unregister()
	if !processgroup.StopManagedProcess(registry, cmd.Process.Pid) {
		t.Fatal("managed process stop refused")
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("managed process still running")
	}
}

func TestStopRetainsStateWhenProcessRemainsAlive(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := filepath.Join(t.TempDir(), "project")
	statePath, _, err := state.Paths(root)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	st := state.RuntimeState{ProjectRoot: root, MainPID: state.Process{PID: cmd.Process.Pid, Command: "sleep", StartedAt: time.Now()}}
	if err := state.SaveState(statePath, st); err != nil {
		t.Fatal(err)
	}

	err = stop(root, func(time.Duration) {})
	if err == nil {
		t.Fatal("stop succeeded while process remained alive")
	}
	got, exists, loadErr := state.LoadState(statePath)
	if loadErr != nil || !exists || got.MainPID.PID != st.MainPID.PID {
		t.Fatalf("state lost after failed stop: exists=%v state=%+v err=%v", exists, got, loadErr)
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
