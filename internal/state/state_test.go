package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// isolateHome mengarahkan brand.StateDir() ke temp dir per-test agar test
// tidak menyentuh state user sungguhan. Windows membaca %USERPROFILE%.
func isolateHome(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}

func TestPathsKeyedPerProject(t *testing.T) {
	isolateHome(t)
	tests := []struct {
		name   string
		root   string
		absent bool // root berbeda harus menghasilkan file berbeda
	}{
		{"checkout A", "/home/dev/app", false},
		{"checkout B", "/home/dev/app-b", false},
	}
	stateA, procA, err := Paths(tests[0].root)
	if err != nil {
		t.Fatalf("Paths error: %v", err)
	}
	stateB, procB, err := Paths(tests[1].root)
	if err != nil {
		t.Fatalf("Paths error: %v", err)
	}
	if stateA == stateB || procA == procB {
		t.Fatalf("dua root menghasilkan file sama: %s vs %s", stateA, stateB)
	}
	// Kedua root harus berada di dir state yang sama (brand.StateDir()).
	if filepath.Dir(stateA) != filepath.Dir(stateB) || filepath.Dir(procA) != filepath.Dir(procB) {
		t.Fatalf("dir state beda antar root: %s vs %s", filepath.Dir(stateA), filepath.Dir(stateB))
	}
}

func TestPathsDeterministicAndAbsolute(t *testing.T) {
	isolateHome(t)
	s1, p1, err := Paths("/home/dev/app")
	if err != nil {
		t.Fatalf("Paths error: %v", err)
	}
	s2, p2, err := Paths("/home/dev/app/../app/.")
	if err != nil {
		t.Fatalf("Paths error: %v", err)
	}
	if s1 != s2 || p1 != p2 {
		t.Fatalf("root sama via path berbeda menghasilkan file berbeda: %s vs %s", s1, s2)
	}
	if !filepath.IsAbs(s1) || !filepath.IsAbs(p1) {
		t.Fatalf("path state harus absolut: %s", s1)
	}
}

func TestPathsCanonicalizeSymlinkAlias(t *testing.T) {
	if os.Getenv("GOOS") == "windows" {
		t.Skip("symlink privileges vary on Windows")
	}
	isolateHome(t)
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.Mkdir(real, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	a, b, err := Paths(real)
	if err != nil {
		t.Fatal(err)
	}
	c, d, err := Paths(alias)
	if err != nil {
		t.Fatal(err)
	}
	if a != c || b != d {
		t.Fatalf("symlink alias paths differ: %s %s / %s %s", a, b, c, d)
	}
}

func TestStateRoundTrip(t *testing.T) {
	isolateHome(t)
	statePath, _, err := Paths("/home/dev/app")
	if err != nil {
		t.Fatalf("Paths error: %v", err)
	}
	// Load saat file belum ada: exists=false, error nil.
	if _, exists, err := LoadState(statePath); err != nil || exists {
		t.Fatalf("load awal: exists=%v err=%v", exists, err)
	}
	started := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	want := RuntimeState{
		ProjectRoot: "/home/dev/app",
		Port:        8891,
		MainPID:     Process{PID: 4242, Command: "/usr/bin/php", StartedAt: started},
	}
	if err := SaveState(statePath, want); err != nil {
		t.Fatalf("SaveState error: %v", err)
	}
	got, exists, err := LoadState(statePath)
	if err != nil || !exists {
		t.Fatalf("LoadState: exists=%v err=%v", exists, err)
	}
	if got.ProjectRoot != want.ProjectRoot || got.Port != want.Port ||
		got.MainPID.PID != want.MainPID.PID || got.MainPID.Command != want.MainPID.Command ||
		!got.MainPID.StartedAt.Equal(started) {
		t.Fatalf("round-trip beda: got %+v want %+v", got, want)
	}
	if got.UpdatedAt.IsZero() {
		t.Fatal("UpdatedAt tidak diisi SaveState")
	}
}

func TestRemoveStateIfOwnerSerializesWithSave(t *testing.T) {
	isolateHome(t)
	path, _, err := Paths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	old := Process{PID: 1, Command: "old"}
	next := Process{PID: 2, Command: "next"}
	if err := SaveState(path, RuntimeState{MainPID: old}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- RemoveStateIfOwner(path, old) }()
	if err := SaveState(path, RuntimeState{MainPID: next}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	got, exists, err := LoadState(path)
	if err != nil || !exists || got.MainPID != next {
		t.Fatalf("new owner state lost: state=%+v exists=%v err=%v", got, exists, err)
	}
}

func TestRemoveStateIfOwnerCannotRemoveNewOwnerAcrossProcesses(t *testing.T) {
	isolateHome(t)
	path, _, err := Paths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	old := Process{PID: 1, Command: "old"}
	next := Process{PID: 2, Command: "next"}
	if err := SaveState(path, RuntimeState{MainPID: old}); err != nil {
		t.Fatal(err)
	}
	unlock, err := lockState(path)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- RemoveStateIfOwner(path, old) }()
	// The held OS lock guarantees remover cannot inspect or delete old state yet.
	data, err := json.Marshal(RuntimeState{MainPID: next})
	if err != nil {
		unlock()
		t.Fatal(err)
	}
	if err := saveAtomic(path, data, 0o600); err != nil {
		unlock()
		t.Fatal(err)
	}
	// Confirm remover remains blocked while this newer owner is persisted.
	select {
	case err := <-done:
		unlock()
		t.Fatalf("remover completed while lock owned: %v", err)
	default:
	}
	unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	got, exists, err := LoadState(path)
	if err != nil || !exists || got.MainPID != next {
		t.Fatalf("new owner state lost: state=%+v exists=%v err=%v", got, exists, err)
	}
}

func TestLoadStateRejectsRelativePath(t *testing.T) {
	if _, _, err := LoadState("runtime-state.json"); err == nil {
		t.Fatal("relative state path accepted")
	}
}

func TestProcessesRoundTrip(t *testing.T) {
	isolateHome(t)
	_, procPath, err := Paths("/home/dev/app")
	if err != nil {
		t.Fatalf("Paths error: %v", err)
	}
	if _, exists, err := LoadProcesses(procPath); err != nil || exists {
		t.Fatalf("load awal: exists=%v err=%v", exists, err)
	}
	want := []Process{
		{PID: 100, Command: "/usr/bin/php", StartedAt: time.Now().UTC()},
		{PID: 101, Command: "/usr/local/bin/caddy", StartedAt: time.Now().UTC()},
	}
	if err := SaveProcesses(procPath, want); err != nil {
		t.Fatalf("SaveProcesses error: %v", err)
	}
	got, exists, err := LoadProcesses(procPath)
	if err != nil || !exists {
		t.Fatalf("LoadProcesses: exists=%v err=%v", exists, err)
	}
	if len(got) != len(want) {
		t.Fatalf("jumlah proses: got %d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("proses %d beda: got %+v want %+v", i, got[i], want[i])
		}
	}
}

// TestSaveAtomicNoTempLeft memastikan penulisan atomik tidak meninggalkan
// file temp dan hasil akhir valid.
func TestSaveAtomicNoTempLeft(t *testing.T) {
	isolateHome(t)
	statePath, _, err := Paths("/home/dev/app")
	if err != nil {
		t.Fatalf("Paths error: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := SaveState(statePath, RuntimeState{ProjectRoot: "x", Port: i}); err != nil {
			t.Fatalf("SaveState iterasi %d: %v", i, err)
		}
	}
	entries, err := os.ReadDir(filepath.Dir(statePath))
	if err != nil {
		t.Fatalf("ReadDir error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("state plus lock file expected (no temp), got %d files", len(entries))
	}
	info, err := entries[0].Info()
	if err != nil {
		t.Fatalf("Info error: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("perm state = %v, want 0600", info.Mode().Perm())
	}
	// Perm processes file diverifikasi terpisah di bawah (0644).
	_, procPath, err := Paths("/home/dev/app")
	if err != nil {
		t.Fatalf("Paths error: %v", err)
	}
	if err := SaveProcesses(procPath, nil); err != nil {
		t.Fatalf("SaveProcesses error: %v", err)
	}
	procInfo, err := os.Stat(procPath)
	if err != nil {
		t.Fatalf("Stat error: %v", err)
	}
	if procInfo.Mode().Perm() != 0o644 {
		t.Fatalf("perm processes = %v, want 0644", procInfo.Mode().Perm())
	}
}

// TestLoadCorruptJSONError: file state rusak → LoadState error dan
// exists=false (jangan pernah diam-diam pakai state korup).
func TestLoadCorruptJSONError(t *testing.T) {
	isolateHome(t)
	statePath, procPath, err := Paths("/home/dev/app")
	if err != nil {
		t.Fatalf("Paths error: %v", err)
	}
	if err := SaveState(statePath, RuntimeState{ProjectRoot: "x"}); err != nil {
		t.Fatalf("SaveState error: %v", err)
	}
	if err := os.WriteFile(statePath, []byte(`{"port": `), 0o600); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}
	st, exists, err := LoadState(statePath)
	if err == nil {
		t.Fatal("LoadState harus error untuk JSON korup")
	}
	if exists {
		t.Fatal("exists harus false untuk JSON korup")
	}
	if st != (RuntimeState{}) {
		t.Fatalf("state harus zero value: %+v", st)
	}
	if err := SaveProcesses(procPath, nil); err != nil {
		t.Fatalf("SaveProcesses error: %v", err)
	}
	if err := os.WriteFile(procPath, []byte("not json"), 0o644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}
	if _, exists, err := LoadProcesses(procPath); err == nil || exists {
		t.Fatalf("LoadProcesses: err=%v exists=%v", err, exists)
	}
}

// TestParallelProjectsNoCrossContamination (AC3): dua root proyek ditulis
// dan dibaca berselang-seling; state masing-masing tetap utuh.
func TestParallelProjectsNoCrossContamination(t *testing.T) {
	isolateHome(t)
	const iterations = 20
	stateA, procA, err := Paths("/home/dev/app")
	if err != nil {
		t.Fatalf("Paths error: %v", err)
	}
	stateB, procB, err := Paths("/home/dev/app-parallel")
	if err != nil {
		t.Fatalf("Paths error: %v", err)
	}

	write := func(statePath, procPath, root string, port, pid int) {
		t.Helper()
		if err := SaveState(statePath, RuntimeState{
			ProjectRoot: root,
			Port:        port,
			MainPID:     Process{PID: pid, Command: "/usr/bin/php", StartedAt: time.Now().UTC()},
		}); err != nil {
			t.Fatalf("SaveState %s: %v", root, err)
		}
		if err := SaveProcesses(procPath, []Process{
			{PID: pid + 1, Command: "/usr/bin/php", StartedAt: time.Now().UTC()},
		}); err != nil {
			t.Fatalf("SaveProcesses %s: %v", root, err)
		}
	}

	// Interleave deterministic: tulis A, tulis B, baca A, baca B, ulang.
	for i := 0; i < iterations; i++ {
		write(stateA, procA, "/home/dev/app", 8800+i, 1000+i)
		write(stateB, procB, "/home/dev/app-parallel", 9900+i, 9000+i)

		got, exists, err := LoadState(stateA)
		if err != nil || !exists {
			t.Fatalf("iter %d LoadState A: exists=%v err=%v", i, exists, err)
		}
		if got.Port != 8800+i || got.MainPID.PID != 1000+i {
			t.Fatalf("iter %d state A terkontaminasi: %+v", i, got)
		}
		procsA, exists, err := LoadProcesses(procA)
		if err != nil || !exists || len(procsA) != 1 || procsA[0].PID != 1001+i {
			t.Fatalf("iter %d processes A terkontaminasi: %v %v %+v", i, exists, err, procsA)
		}

		got, exists, err = LoadState(stateB)
		if err != nil || !exists {
			t.Fatalf("iter %d LoadState B: exists=%v err=%v", i, exists, err)
		}
		if got.Port != 9900+i || got.MainPID.PID != 9000+i {
			t.Fatalf("iter %d state B terkontaminasi: %+v", i, got)
		}
		procsB, exists, err := LoadProcesses(procB)
		if err != nil || !exists || len(procsB) != 1 || procsB[0].PID != 9001+i {
			t.Fatalf("iter %d processes B terkontaminasi: %v %v %+v", i, exists, err, procsB)
		}
	}
}

// TestConcurrentSavesSameProject: penulis paralel pada proyek yang sama
// tidak merusak file (rename atomik; pembaca selalu melihat versi utuh).
func TestConcurrentSavesSameProject(t *testing.T) {
	isolateHome(t)
	statePath, _, err := Paths("/home/dev/app")
	if err != nil {
		t.Fatalf("Paths error: %v", err)
	}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(port int) {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				if err := SaveState(statePath, RuntimeState{ProjectRoot: "x", Port: port}); err != nil {
					t.Errorf("SaveState port %d: %v", port, err)
					return
				}
				if _, _, err := LoadState(statePath); err != nil {
					t.Errorf("LoadState: %v", err)
					return
				}
			}
		}(9000 + w)
	}
	wg.Wait()
	if _, exists, err := LoadState(statePath); err != nil || !exists {
		t.Fatalf("hasil akhir: exists=%v err=%v", exists, err)
	}
}
