package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/maleolabs/avior/internal/brand"
	"github.com/maleolabs/avior/internal/processgroup"
	"github.com/maleolabs/avior/internal/state"
)

// ProjectStatus is a project-scoped runtime snapshot for monitor and attach.
type ProjectStatus struct {
	ProjectRoot string
	Running     bool
	State       state.RuntimeState
	Services    []state.Process
}

// Monitor returns all runtimes under user state directory, one status per project.
func Monitor() ([]ProjectStatus, error) {
	dir := brand.StateDir()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var statuses []ProjectStatus
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !isRuntimeStateFile(name) {
			continue
		}
		path := filepath.Join(dir, name)
		st, exists, err := state.LoadState(path)
		if err != nil {
			return nil, fmt.Errorf("runtime: monitor %s: %w", name, err)
		}
		if !exists || st.ProjectRoot == "" {
			continue
		}
		_, registry, err := state.Paths(st.ProjectRoot)
		if err != nil {
			return nil, err
		}
		services, _, err := state.LoadProcesses(registry)
		if err != nil {
			return nil, fmt.Errorf("runtime: monitor %s processes: %w", st.ProjectRoot, err)
		}
		running := st.MainPID.PID > 0 && processgroup.ProcessAlive(st.MainPID)
		statuses = append(statuses, ProjectStatus{ProjectRoot: st.ProjectRoot, Running: running, State: st, Services: services})
	}
	return statuses, nil
}

// Attach snapshots only selected project's state. Project must be its root path.
func Attach(project string) (ProjectStatus, error) {
	statePath, registry, err := state.Paths(project)
	if err != nil {
		return ProjectStatus{}, err
	}
	st, exists, err := state.LoadState(statePath)
	if err != nil {
		return ProjectStatus{}, err
	}
	if !exists {
		return ProjectStatus{ProjectRoot: project}, nil
	}
	services, _, err := state.LoadProcesses(registry)
	if err != nil {
		return ProjectStatus{}, err
	}
	return ProjectStatus{ProjectRoot: st.ProjectRoot, Running: st.MainPID.PID > 0 && processgroup.ProcessAlive(st.MainPID), State: st, Services: services}, nil
}

// Stop stops only runtime registered for selected project root.
func Stop(project string) error {
	return stop(project, time.Sleep)
}

func stop(project string, wait func(time.Duration)) error {
	return stopWithHooks(project, wait, processgroup.StopManagedRuntime, processgroup.ProcessAlive, 7*time.Second)
}

func stopWithHooks(project string, wait func(time.Duration), stopManaged func(string, int) bool, alive func(state.Process) bool, timeout time.Duration) error {
	statePath, registry, err := state.Paths(project)
	if err != nil {
		return err
	}
	st, exists, err := state.LoadState(statePath)
	if err != nil || !exists {
		return err
	}
	root, err := filepath.Abs(project)
	if err != nil {
		return err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(root); resolveErr == nil {
		root = resolved
	}
	if filepath.Clean(st.ProjectRoot) != filepath.Clean(root) {
		return fmt.Errorf("runtime: project state mismatch")
	}
	if st.MainPID.PID <= 0 {
		return nil
	}
	if !stopManaged(registry, st.MainPID.PID) {
		if alive(st.MainPID) {
			return fmt.Errorf("runtime: cannot safely stop project %s", project)
		}
		return state.RemoveStateIfOwner(statePath, st.MainPID)
	}
	if !waitForExitTimeout(st.MainPID, wait, alive, timeout) {
		return fmt.Errorf("runtime: project %s still running after stop timeout", project)
	}
	return state.RemoveStateIfOwner(statePath, st.MainPID)
}

func waitForExit(pid state.Process, wait func(time.Duration), alive func(state.Process) bool) bool {
	return waitForExitTimeout(pid, wait, alive, 7*time.Second)
}

func waitForExitTimeout(pid state.Process, wait func(time.Duration), alive func(state.Process) bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) && alive(pid) {
		wait(25 * time.Millisecond)
	}
	return !alive(pid)
}

func isRuntimeStateFile(name string) bool {
	return len(name) == len("runtime-state-")+16+len(".json") && name[:len("runtime-state-")] == "runtime-state-" && filepath.Ext(name) == ".json"
}
