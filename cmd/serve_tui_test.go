package cmd

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestServeTUIRejectsNonTTY(t *testing.T) {
	err := RunIn([]string{"serve", "development", "--tui"}, strings.NewReader(""), &strings.Builder{}, &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "stdin dan stdout TTY") {
		t.Fatalf("expected actionable TTY error, got %v", err)
	}
}

func TestServeTUIStateUpdates(t *testing.T) {
	m := serveTUIModel{services: []string{"Laravel"}, states: map[string]string{"Laravel": "starting"}}
	updated, _ := m.Update(serveStateMsg{name: "Laravel", state: "running"})
	model := updated.(serveTUIModel)
	if model.states["Laravel"] != "running" || !strings.Contains(model.View(), "Laravel: RUNNING") {
		t.Fatalf("state update absent from TUI: %#v", model.states)
	}
}

func TestServeTUIViewElapsedDoesNotMutateModel(t *testing.T) {
	m := serveTUIModel{status: "RUNNING", started: time.Now().Add(-3 * time.Second)}
	view := m.View()
	if !strings.Contains(view, "Elapsed: 3s") {
		t.Fatalf("elapsed tidak dihitung dari started: %q", view)
	}
	if m.started.IsZero() {
		t.Fatal("View memutasi model (started jadi zero)")
	}
}

// TestServeTUICtrlCStopsRuntime menggerakkan program secara deterministik
// in-process: input pipe mengirim byte Ctrl+C, serve palsu menunggu
// cancellation, lalu runServeTUI harus kembali dengan state STOPPED.
func TestServeTUICtrlCStopsRuntime(t *testing.T) {
	inR, inW := io.Pipe()
	defer inR.Close()
	defer inW.Close()
	out := &syncBuffer{}

	started := make(chan struct{})
	cancelled := make(chan struct{})
	serve := func(ctx context.Context, ready func([]string), state func(string, string)) error {
		ready([]string{"Laravel"})
		state("Laravel", "running")
		close(started)
		<-ctx.Done()
		close(cancelled)
		state("Laravel", "exited")
		return nil
	}

	done := make(chan error, 1)
	go func() { done <- runServeTUI(context.Background(), serve, inR, out) }()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("serve tidak pernah mulai")
	}
	if _, err := inW.Write([]byte{3}); err != nil {
		t.Fatalf("kirim Ctrl+C: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runServeTUI error setelah Ctrl+C: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runServeTUI tidak kembali setelah Ctrl+C")
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("runtime belum menerima cancellation saat runServeTUI kembali")
	}
	view := out.String()
	if !strings.Contains(view, "Runtime: STOPPED") || !strings.Contains(view, "Laravel: EXITED") {
		t.Fatalf("state akhir TUI tidak jujur: %q", view)
	}
}

func TestServeTUISurfacesShutdownError(t *testing.T) {
	inR, inW := io.Pipe()
	defer inR.Close()
	defer inW.Close()
	out := &syncBuffer{}

	started := make(chan struct{})
	shutdownErr := errors.New("shutdown gagal")
	serve := func(ctx context.Context, _ func([]string), _ func(string, string)) error {
		close(started)
		<-ctx.Done()
		return shutdownErr
	}

	done := make(chan error, 1)
	go func() { done <- runServeTUI(context.Background(), serve, inR, out) }()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("serve tidak pernah mulai")
	}
	if _, err := inW.Write([]byte{3}); err != nil {
		t.Fatalf("kirim Ctrl+C: %v", err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, shutdownErr) {
			t.Fatalf("shutdown error tidak diteruskan, got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runServeTUI tidak kembali setelah Ctrl+C")
	}
	if !strings.Contains(out.String(), "shutdown gagal") {
		t.Fatalf("error shutdown tidak tampil di UI: %q", out.String())
	}
}

// TestServeTUICtrlCStopsRuntimePTY adalah integrasi terminal sungguhan:
// bubbletea membaca byte Ctrl+C dari PTY, bukan dari pipe. Test binary yang
// sama dipakai ulang sebagai helper (tanpa compile `go run`, tanpa fixture
// Laravel) sehingga hasilnya deterministik.
func TestServeTUICtrlCStopsRuntimePTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PTY check requires POSIX")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 tidak tersedia untuk membuat PTY")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "-c", `import os, pty, select, subprocess, sys, time
master, slave = pty.openpty()
env = dict(os.environ, TUI_PTY_HELPER="1")
p = subprocess.Popen([sys.argv[2], "-test.run=^TestServeTUIPTYHelper$"], stdin=slave, stdout=slave, stderr=slave, cwd=sys.argv[1], env=env, start_new_session=True)
os.close(slave)
buf = b""
deadline = time.time() + 20
while b"Ctrl+C stop runtime" not in buf and time.time() < deadline:
    ready, _, _ = select.select([master], [], [], .1)
    if ready:
        try: buf += os.read(master, 65536)
        except OSError: break
if b"Ctrl+C stop runtime" not in buf:
    p.kill(); sys.exit("TUI did not start: " + repr(buf))
os.write(master, b"\x03")
try: sys.exit(p.wait(timeout=10))
except subprocess.TimeoutExpired:
    p.kill(); sys.exit("Ctrl+C did not stop runtime and return")`, root, exe)
	cmd.Dir = root
	output := &strings.Builder{}
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("PTY Ctrl+C test: %v\n%s", err, output.String())
	}
}

// TestServeTUIPTYHelper hanya aktif saat dipanggil ulang oleh test PTY:
// menjalankan runServeTUI dengan serve palsu di terminal sungguhan.
func TestServeTUIPTYHelper(t *testing.T) {
	if os.Getenv("TUI_PTY_HELPER") != "1" {
		t.Skip("helper process untuk TestServeTUICtrlCStopsRuntimePTY")
	}
	serve := func(ctx context.Context, ready func([]string), state func(string, string)) error {
		ready([]string{"Laravel"})
		state("Laravel", "running")
		<-ctx.Done()
		state("Laravel", "exited")
		return nil
	}
	if err := runServeTUI(context.Background(), serve, os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

// syncBuffer melindungi output renderer bubbletea (goroutine terpisah) saat
// test membacanya.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
