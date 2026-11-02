package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/maleolabs/avior/internal/brand"
	"github.com/maleolabs/avior/internal/gateway"
	"github.com/maleolabs/avior/internal/processgroup"
	"github.com/maleolabs/avior/internal/state"
)

// ProjectStatus is a project-scoped runtime snapshot for monitor and attach.
type ProjectStatus struct {
	ProjectRoot string
	ProjectName string
	Token       string
	URL         string
	Running     bool
	State       state.RuntimeState
	Services    []state.Process
}

// statusURL menyusun URL user-facing: HTTPS bila caddy admin tercatat dan
// domain ada, HTTP jika tidak; port situs disertakan bila bukan 80/443.
// Phase 7: state CleanURL (terdaftar di gateway :80/:443) → URL tanpa port.
func statusURL(st state.RuntimeState) string {
	if st.Domain == "" {
		return ""
	}
	if st.CleanURL {
		scheme := "http"
		if st.CaddyAdminPort != 0 {
			scheme = "https"
		}
		return scheme + "://" + st.Domain
	}
	scheme := "http"
	port := st.Ports.Caddy
	if st.CaddyAdminPort != 0 {
		scheme = "https"
	}
	if port == 0 {
		port = st.Port
	}
	if port == 0 || port == 80 || (scheme == "https" && port == 443) {
		return scheme + "://" + st.Domain
	}
	return fmt.Sprintf("%s://%s:%d", scheme, st.Domain, port)
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
			// Toleran: skip entry rusak, jangan gagalkan seluruh monitor.
			continue
		}
		if !exists || st.ProjectRoot == "" {
			continue
		}
		projectStatePath, registry, err := state.Paths(st.ProjectRoot)
		if err != nil {
			continue
		}
		if filepath.Clean(projectStatePath) != filepath.Clean(path) {
			// State file tidak cocok dengan root-nya (legacy/corrupt): skip.
			continue
		}
		services, _, err := state.LoadProcesses(registry)
		if err != nil {
			services = nil
		}
		running := st.MainPID.PID > 0 && processgroup.ProcessAlive(st.MainPID)
		statuses = append(statuses, ProjectStatus{
			ProjectRoot: st.ProjectRoot,
			ProjectName: st.ProjectName,
			Token:       st.ProjectToken,
			URL:         statusURL(st),
			Running:     running,
			State:       st,
			Services:    services,
		})
	}
	return statuses, nil
}

// RuntimeRunning melaporkan apakah proyek (root) sudah memiliki runtime
// hidup — duplicate guard untuk `serve`.
func RuntimeRunning(project string) bool {
	statePath, _, err := state.Paths(project)
	if err != nil {
		return false
	}
	st, exists, err := state.LoadState(statePath)
	if err != nil || !exists {
		return false
	}
	return st.MainPID.PID > 0 && processgroup.ProcessAlive(st.MainPID)
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
	canonicalRoot, err := filepath.Abs(project)
	if err != nil {
		return ProjectStatus{}, err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(canonicalRoot); resolveErr == nil {
		canonicalRoot = resolved
	}
	if filepath.Clean(st.ProjectRoot) != filepath.Clean(canonicalRoot) {
		return ProjectStatus{}, fmt.Errorf("runtime: project state mismatch")
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
	// Stop terkelola: hentikan child milik owner runtime (proses CLI yang
	// menjalankan runtime ini). Owner catatan: state v2 (Owner) atau
	// fallback MainPID (state v1, child pertama).
	owner := st.Owner
	if owner.PID == 0 {
		owner = st.MainPID
	}
	// Owner == pemanggil (e2e/TUI in-process): hentikan child-nya saja,
	// jangan kirim sinyal ke diri sendiri. Owner berbeda proses: biarkan
	// runtime owner menyelesaikan teardown normalnya.
	stopped := false
	if owner.PID == os.Getpid() {
		stopped = processgroup.StopManagedChildren(registry, owner.PID)
	} else {
		stopped = stopManaged(registry, owner.PID)
	}
	if !stopped {
		if alive(st.MainPID) {
			return fmt.Errorf("runtime: cannot safely stop project %s", project)
		}
		return state.RemoveStateIfOwner(statePath, st.MainPID)
	}
	if !waitForExitTimeout(st.MainPID, wait, alive, timeout) {
		return fmt.Errorf("runtime: project %s still running after stop timeout", project)
	}
	if err := state.RemoveStateIfOwner(statePath, st.MainPID); err != nil {
		return err
	}
	// Phase 7: hapus site dari gateway bersama (stop gateway saat site
	// terakhir). MainPID.Command = path binary Caddy per-proyek yang
	// dipakai registrasi serve.
	if st.CleanURL && st.Domain != "" && st.MainPID.Command != "" {
		_, _ = gateway.UnregisterSite(st.MainPID.Command, st.Domain, st.GatewayAdmin)
	}
	if st.ProjectToken != "" {
		_ = state.RemoveProject(st.ProjectToken)
	}
	return nil
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
	if !strings.HasPrefix(name, "runtime-state-") || filepath.Ext(name) != ".json" {
		return false
	}
	token := strings.TrimPrefix(name, "runtime-state-")
	token = strings.TrimSuffix(token, ".json")
	return len(token) == 16 || len(token) == 24
}
