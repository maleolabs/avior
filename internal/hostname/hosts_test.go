package hostname

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maleolabs/avior/internal/brand"
)

func TestEnsureHostUsesInjectedWriterAndCleanup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts")
	if err := os.WriteFile(path, []byte("127.0.0.1 other.test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	oldPath, oldWriter, oldPreflight := hostsPath, privilegedWriter, privilegePreflight
	t.Cleanup(func() { hostsPath, privilegedWriter, privilegePreflight = oldPath, oldWriter, oldPreflight })
	hostsPath = func() string { return path }
	privilegePreflight = func(string) error { return nil }
	privilegedWriter = func(p string, data []byte) error { return os.WriteFile(p, data, 0644) }

	cleanup, err := EnsureHost(strings.ToUpper(brand.Name)+".test.", true)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	want := "127.0.0.1 other.test\n127.0.0.1 " + brand.Name + ".test " + brand.HostsMarker + " " + brand.Name + ".test\n"
	if string(got) != want {
		t.Fatalf("unexpected hosts content: %q", got)
	}
	cleanup()
	got, _ = os.ReadFile(path)
	if string(got) != "127.0.0.1 other.test\n" {
		t.Fatalf("cleanup changed unrelated content: %q", got)
	}
}

func TestEnsureHostPropagatesWriterError(t *testing.T) {
	oldPath, oldWriter, oldPreflight := hostsPath, privilegedWriter, privilegePreflight
	t.Cleanup(func() { hostsPath, privilegedWriter, privilegePreflight = oldPath, oldWriter, oldPreflight })
	path := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(path, nil, 0644); err != nil {
		t.Fatal(err)
	}
	hostsPath = func() string { return path }
	privilegePreflight = func(string) error { return nil }
	privilegedWriter = func(string, []byte) error { return os.ErrPermission }
	if _, err := EnsureHost("app.test", true); err == nil {
		t.Fatal("expected permission error")
	}
}

func TestEnsureHostPreflightsBeforeWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(path, nil, 0644); err != nil {
		t.Fatal(err)
	}
	oldPath, oldWriter, oldPreflight := hostsPath, privilegedWriter, privilegePreflight
	t.Cleanup(func() { hostsPath, privilegedWriter, privilegePreflight = oldPath, oldWriter, oldPreflight })
	hostsPath = func() string { return path }
	var events []string
	privilegePreflight = func(string) error { events = append(events, "preflight"); return nil }
	privilegedWriter = func(string, []byte) error { events = append(events, "write"); return nil }

	_, err := EnsureHost("app.test", true)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(events, ","), "preflight,write"; got != want {
		t.Fatalf("events %q, want %q", got, want)
	}
}

func TestManagedHostRemovalOnlyRemovesExactOwnedLine(t *testing.T) {
	line := managedHostLine(brand.Name + ".test")
	input := "127.0.0.1 other.test\n" + line + "\n127.0.0.1 " + brand.Name + ".test # user entry\n"
	want := "127.0.0.1 other.test\n127.0.0.1 " + brand.Name + ".test # user entry\n"
	if got := removeManagedHost(input, line); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
