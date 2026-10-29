package processgroup

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/maleolabs/avior/internal/state"
)

func TestWriteProcessRegistryChmodFailureCleansTempAndPreservesRegistry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.json")
	original := []byte(`[{"pid":7,"command":"existing"}]`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("chmod failed")
	originalChmod := chmodRegistryTemp
	chmodRegistryTemp = func(*os.File, os.FileMode) error { return wantErr }
	t.Cleanup(func() { chmodRegistryTemp = originalChmod })

	err := writeProcessRegistry(path, []processRecord{{PID: 8, Command: "new"}})
	if !errors.Is(err, wantErr) {
		t.Fatalf("writeProcessRegistry() error = %v, want %v", err, wantErr)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("registry changed: got %q, want %q", got, original)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(path) {
		t.Fatalf("temporary file leaked: entries = %v", entries)
	}
}

func TestUnregisterProcessRemovesOnlyExactRegistration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime-processes.json")
	now := time.Now().UTC()
	record := processRecord{PID: 123, Command: "server", Executable: "/bin/server", Identity: "start:1", CreatedAt: now}
	other := record
	other.Command = "different-instance"
	if err := writeProcessRegistry(path, []processRecord{record, other}); err != nil {
		t.Fatal(err)
	}
	if err := unregisterProcess(path, record); err != nil {
		t.Fatal(err)
	}
	kept, err := readProcessRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 1 || kept[0].Command != other.Command {
		t.Fatalf("unregister matching removed wrong records: %#v", kept)
	}
}

func TestProjectRegistriesRemainIsolated(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	_, pathA, err := state.Paths(filepath.Join(t.TempDir(), "project-a"))
	if err != nil {
		t.Fatal(err)
	}
	_, pathB, err := state.Paths(filepath.Join(t.TempDir(), "project-b"))
	if err != nil {
		t.Fatal(err)
	}
	unregisterA, err := RegisterProcess(pathA, "server-a", os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	_, err = RegisterProcess(pathB, "server-b", os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if err := unregisterA(); err != nil {
		t.Fatal(err)
	}
	if records, err := readProcessRegistry(pathA); err != nil || len(records) != 0 {
		t.Fatalf("project A registry after unregister: records=%+v err=%v", records, err)
	}
	if records, err := readProcessRegistry(pathB); err != nil || len(records) != 1 || records[0].Command != "server-b" {
		t.Fatalf("project B registry changed: records=%+v err=%v", records, err)
	}
}

func TestRegistryCleanupReturnsFilesystemErrors(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(parent, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "registry.json")
	record := processRecord{PID: 1, Command: "x", Executable: "x", Identity: "x", CreatedAt: time.Now()}
	if err := unregisterProcess(path, record); err == nil {
		t.Fatal("unregisterProcess() must report lock creation failure")
	}
	if err := CleanupStaleProcesses(path); err == nil {
		t.Fatal("CleanupStaleProcesses() must report lock creation failure")
	}
}

func TestConcurrentRegistryMutationsPreserveRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime-processes.json")
	const count = 16
	var wg sync.WaitGroup
	errCh := make(chan error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := processRecord{PID: 10000 + i, Command: "command", Executable: "exe", Identity: "identity", CreatedAt: time.Now().Add(time.Duration(i))}
			registryMu.Lock()
			defer registryMu.Unlock()
			unlock, err := lockRegistry(path)
			if err != nil {
				errCh <- err
				return
			}
			defer unlock()
			records, err := readProcessRegistry(path)
			if err != nil {
				errCh <- err
				return
			}
			records = append(records, r)
			errCh <- writeProcessRegistry(path, records)
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	records, err := readProcessRegistry(path)
	if err != nil || len(records) != count {
		t.Fatalf("records=%d err=%v, want %d", len(records), err, count)
	}
}

func TestRegistryAPIsSerializeAcrossProcesses(t *testing.T) {
	if path := os.Getenv("REGISTRY_CONTENTION_FILE"); path != "" {
		for i := 0; i < 12; i++ {
			pid := os.Getpid()
			unregister, err := RegisterProcess(path, "child-"+strconv.Itoa(i), pid)
			if err != nil {
				os.Exit(2)
			}
			if err := unregister(); err != nil {
				os.Exit(3)
			}
		}
		os.Exit(0)
	}
	path := filepath.Join(t.TempDir(), "registry.json")
	const workers = 5
	cmds := make([]*exec.Cmd, workers)
	for i := range cmds {
		cmds[i] = exec.Command(os.Args[0], "-test.run=^TestRegistryAPIsSerializeAcrossProcesses$")
		cmds[i].Env = append(os.Environ(), "REGISTRY_CONTENTION_FILE="+path)
		if err := cmds[i].Start(); err != nil {
			t.Fatal(err)
		}
	}
	for _, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	records, err := readProcessRegistry(path)
	if err != nil || len(records) != 0 {
		t.Fatalf("records remaining=%d err=%v, want all subprocess registrations unregistered", len(records), err)
	}
}

func TestStopManagedProcessAllowsConcurrentUnregister(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skipf("sleep unavailable: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	unregister, err := RegisterProcess(path, "sleep 30", cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		StopManagedProcess(path, cmd.Process.Pid)
		close(done)
	}()
	cleanupDone := make(chan error, 1)
	go func() { cleanupDone <- unregister() }()

	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		t.Fatal("stop deadlocked on registry lock")
	}
	select {
	case err := <-cleanupDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-timer.C:
		t.Fatal("unregister deadlocked on registry lock")
	}
}

func TestStopManagedRuntimeAllowsConcurrentUnregister(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skipf("sleep unavailable: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	unregister, err := RegisterProcess(path, "sleep 30", cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { StopManagedRuntime(path, -1); close(done) }()
	cleanupDone := make(chan error, 1)
	go func() { cleanupDone <- unregister() }()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		t.Fatal("runtime stop deadlocked on registry lock")
	}
	select {
	case err := <-cleanupDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-timer.C:
		t.Fatal("unregister deadlocked on registry lock")
	}
}

func TestRecordsForOwnerIncludesAllRuntimeChildren(t *testing.T) {
	records := []processRecord{
		{PID: 10, OwnerPID: 7, OwnerExecutable: "runtime", OwnerIdentity: "start:7"},
		{PID: 11, OwnerPID: 7, OwnerExecutable: "runtime", OwnerIdentity: "start:7"},
		{PID: 12, OwnerPID: 8, OwnerExecutable: "runtime", OwnerIdentity: "start:8"},
		{PID: 13, OwnerPID: 7, OwnerExecutable: "other", OwnerIdentity: "start:7"},
	}
	owned := recordsForOwner(records, 7, "runtime", "start:7")
	if len(owned) != 2 || owned[0].PID != 10 || owned[1].PID != 11 {
		t.Fatalf("recordsForOwner() = %#v, want both children of owner 7", owned)
	}
}

func TestOwnerAliveIsNeverConsideredStale(t *testing.T) {
	exe, identity := processIdentity(os.Getpid())
	if exe == "" || identity == "" {
		t.Skip("process identity unavailable on this platform")
	}
	r := processRecord{OwnerPID: os.Getpid(), OwnerExecutable: exe, OwnerIdentity: identity}
	if ownerDefinitelyGone(r) {
		t.Fatal("active runtime owner was considered gone")
	}
}

func TestOwnerPIDReuseIsConsideredGone(t *testing.T) {
	exe, identity := processIdentity(os.Getpid())
	if exe == "" || identity == "" {
		t.Skip("process identity unavailable on this platform")
	}
	r := processRecord{OwnerPID: os.Getpid(), OwnerExecutable: exe, OwnerIdentity: "identity-from-a-different-runtime"}
	if !ownerDefinitelyGone(r) {
		t.Fatal("owner PID reuse was not detected")
	}
}

func TestOwnerIdentityUnavailableFailsClosed(t *testing.T) {
	r := processRecord{OwnerPID: os.Getpid()}
	if ownerDefinitelyGone(r) {
		t.Fatal("missing owner identity must fail closed")
	}
}

func TestCommandsMatchAllowsExecutablePathDifferences(t *testing.T) {
	if !commandsMatch("/usr/bin/php artisan serve --host=127.0.0.1", "php artisan serve --host=127.0.0.1") {
		t.Fatal("equivalent executable names should match")
	}
}

func TestCommandsMatchRejectsDifferentArguments(t *testing.T) {
	if commandsMatch("/usr/bin/php artisan serve --port=8000", "php artisan serve --port=8001") {
		t.Fatal("different arguments must not match")
	}
}

func TestExecutablesMatchIgnoresPathPresentation(t *testing.T) {
	for _, tc := range []struct {
		actual, recorded string
	}{
		{"php", "/usr/bin/php"},
		{"/usr/local/bin/caddy", "caddy"},
	} {
		if !executablesMatch(tc.actual, tc.recorded) {
			t.Fatalf("executable forms should match: %q vs %q", tc.actual, tc.recorded)
		}
	}
}

func TestExecutablesMatchRejectsDifferentPrograms(t *testing.T) {
	if executablesMatch("/usr/bin/php", "/usr/bin/caddy") {
		t.Fatal("different executable basenames must not match")
	}
}
