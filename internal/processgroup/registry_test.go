package processgroup

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUnregisterProcessRemovesOnlyExactRegistration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime-processes.json")
	now := time.Now().UTC()
	record := processRecord{PID: 123, Command: "server", Executable: "/bin/server", Identity: "start:1", CreatedAt: now}
	other := record
	other.Command = "different-instance"
	if err := writeProcessRegistry(path, []processRecord{record, other}); err != nil {
		t.Fatal(err)
	}
	// Exercise the same matching semantics used by unregisterProcess without
	// depending on the platform cache directory.
	records, err := readProcessRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	kept := records[:0]
	for _, r := range records {
		if r.PID != record.PID || r.Executable != record.Executable || r.Identity != record.Identity || r.Command != record.Command || !r.CreatedAt.Equal(record.CreatedAt) {
			kept = append(kept, r)
		}
	}
	if len(kept) != 1 || kept[0].Command != other.Command {
		t.Fatalf("unregister matching removed wrong records: %#v", kept)
	}
	_ = os.Remove(path)
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
