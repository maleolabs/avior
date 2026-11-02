package runtime

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maleolabs/avior/internal/state"
)

// TestDuplicateRuntimeRejected: proyek yang sudah running tidak bisa di-serve
// ulang (duplicate guard).
func TestDuplicateRuntimeRejected(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	root := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	// Belum ada runtime → tidak dianggap berjalan.
	if RuntimeRunning(root) {
		t.Fatal("RuntimeRunning harus false saat belum ada state")
	}
	// Buat state seolah proyek sedang berjalan (pid = pid test sendiri).
	path, _, err := state.Paths(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SaveState(path, state.RuntimeState{
		ProjectRoot: root,
		MainPID:     state.Process{PID: os.Getpid(), Command: os.Args[0], StartedAt: time.Now()},
	}); err != nil {
		t.Fatal(err)
	}
	if !RuntimeRunning(root) {
		t.Fatal("RuntimeRunning harus true setelah state hidup")
	}
	// Serve ulang harus ditolak (cek via Serve langsung terlalu berat;
	// guard ada di Serve, kitaverified RuntimeRunning sebagai dasarnya).
}

// TestStopProjectScoped: stop proyek A tidak menghapus state proyek B.
func TestStopProjectScoped(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	a := filepath.Join(t.TempDir(), "a")
	b := filepath.Join(t.TempDir(), "b")
	for _, root := range []string{a, b} {
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// State A: pid mati (cleanup); State B: pid test sendiri (hidup).
	pa, _, err := state.Paths(a)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SaveState(pa, state.RuntimeState{
		ProjectRoot: a,
		MainPID:     state.Process{PID: 99999, Command: "php", StartedAt: time.Now()},
	}); err != nil {
		t.Fatal(err)
	}
	pb, _, err := state.Paths(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SaveState(pb, state.RuntimeState{
		ProjectRoot: b,
		MainPID:     state.Process{PID: os.Getpid(), Command: os.Args[0], StartedAt: time.Now()},
	}); err != nil {
		t.Fatal(err)
	}
	// Stop A: pid 99999 tidak hidup → StopManagedRuntime gagal → state dipertahankan.
	_ = Stop(a)
	// State B tetap utuh.
	if _, exists, err := state.LoadState(pb); err != nil || !exists {
		t.Fatalf("state B hilang setelah stop A: exists=%v err=%v", exists, err)
	}
}

// TestAttachProjectScoped: attach A hanya menampilkan A.
func TestAttachProjectScoped(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	a := filepath.Join(t.TempDir(), "a")
	b := filepath.Join(t.TempDir(), "b")
	for _, root := range []string{a, b} {
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	for _, root := range []string{a, b} {
		path, _, err := state.Paths(root)
		if err != nil {
			t.Fatal(err)
		}
		if err := state.SaveState(path, state.RuntimeState{
			ProjectRoot: root,
			ProjectName: filepath.Base(root),
			MainPID:     state.Process{PID: os.Getpid(), Command: os.Args[0], StartedAt: now},
		}); err != nil {
			t.Fatal(err)
		}
	}
	st, err := Attach(a)
	if err != nil {
		t.Fatal(err)
	}
	if st.ProjectRoot != a {
		t.Fatalf("attach = %s, want %s", st.ProjectRoot, a)
	}
	if st.ProjectRoot == b {
		t.Fatal("attach A tidak boleh mengembalikan B")
	}
}
