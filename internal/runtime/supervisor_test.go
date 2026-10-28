package runtime

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"syscall"
	"testing"
	"time"
)

// waitReady menunggu stub menandai dirinya siap (trap terpasang).
func waitReady(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("stub tidak siap dalam 2s: %s tidak muncul", path)
}

// recallExited mengembalikan sinyal penyebab kematian child (untuk assert
// SIGTERM vs SIGKILL), atau 0 bila bukan ExitError.
func (s *Supervised) recallExited() syscall.Signal {
	r, ok := s.recall()
	if !ok {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(r.err, &ee) {
		if ws, okSys := ee.Sys().(syscall.WaitStatus); okSys && ws.Signaled() {
			return ws.Signal()
		}
	}
	return 0
}

// lookSleep mencari binary `sleep` untuk test integrasi proses nyata.
// SKIP WINDOWS SECARA FAIL-CLOSED: stub sleep + SIGTERM/SIGKILL tidak
// portabel (dan graceful Windows butuh job object yang menyusul di p1),
// jadi jangan dipaksakan jalan — skip, bukan pass palsu.
func lookSleep(t *testing.T) string {
	t.Helper()
	if goruntime.GOOS == "windows" {
		t.Skip("windows: stub proses + sinyal Unix tidak portabel (fail-closed); coverage menyusul dengan job object di p1")
	}
	p, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("binary `sleep` tidak ditemukan di PATH; test integrasi butuh proses nyata")
	}
	return p
}

func mustStart(t *testing.T, args ...string) *Supervised {
	t.Helper()
	s, err := Start(args[0], args[1:]...)
	if err != nil {
		t.Fatalf("Start(%v): %v", args, err)
	}
	t.Cleanup(func() { _ = s.cmd.Process.Kill() })
	return s
}

// 1. Terminate dengan child sleep nyata: graceful terminate, reaped TEPAT
// sekali (konsumen pertama memo, Wait() berikutnya membaca memo yang sama).
func TestTerminateSleepReapedOnce(t *testing.T) {
	sleep := lookSleep(t)
	s := mustStart(t, sleep, "30")

	err := s.Terminate(2*time.Second, 2*time.Second)
	if err == nil {
		t.Fatal("Terminate sleep via SIGTERM harus mengembalikan error signal, dapat nil")
	}
	if s.recallExited() != syscall.SIGTERM {
		t.Fatalf("child harus mati karena SIGTERM, dapat %v", s.recallExited())
	}

	// Wait() setelah Terminate tidak boleh block/hang — konsumsi memo.
	done := make(chan error, 1)
	go func() { done <- s.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Wait() setelah terminate harus error signal")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait() block setelah Terminate — memo reaping bocor")
	}
	// Reap tepat-sekali dijamin struktural: TEPAT SATU goroutine pemilik
	// cmd.Wait() (lihat invariant di header supervisor.go); semua konsumen
	// lain (Terminate/Wait/monitor) hanya membaca channel/memo yang sama.
}

// 2. Kill-after-timeout: grace sangat pendek, eskalasi SIGKILL menutup child.
// SIGTERM tidak boleh dikirim sebelum stub sempat memasang trap (race di
// jalur failed-startup H5) — jadi stub menulis file "ready" setelah trap,
// dan test menunggu file itu sebelum Terminate.
func TestTerminateEscalatesToKill(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("binary `sh` tidak ditemukan; test eskalasi butuh trap POSIX")
	}
	ready := filepath.Join(t.TempDir(), "ready")
	// trap "" TERM: child mengabaikan SIGTERM — jalur eskalasi SIGKILL.
	s := mustStart(t, sh, "-c", `trap "" TERM; : > `+ready+`; sleep 30`)
	waitReady(t, ready)

	start := time.Now()
	if err := s.Terminate(50*time.Millisecond, 2*time.Second); err == nil {
		t.Fatal("eskalasi Kill harus mengembalikan error signal")
	}
	if el := time.Since(start); el > time.Second {
		t.Fatalf("eskalasi terlalu lambat: %v", el)
	}
	if s.recallExited() != syscall.SIGKILL {
		t.Fatalf("child harus mati karena SIGKILL setelah grace habis, dapat %v", s.recallExited())
	}
}

// 3. Double-Terminate idempotent (juga konkuren, untuk -race).
func TestTerminateIdempotent(t *testing.T) {
	sleep := lookSleep(t)
	s := mustStart(t, sleep, "30")

	const n = 4
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() { errs <- s.Terminate(500*time.Millisecond, 2*time.Second) }()
	}
	for i := 0; i < n; i++ {
		select {
		case err := <-errs:
			if err == nil {
				t.Fatal("Terminate harus mengembalikan error signal child")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("Terminate ganda deadlock — idempotensi rusak")
		}
	}
}

// 4. Failed-startup path (H5): child exit instan; monitor (konsumen channel)
// melihat exit tanpa pernah memanggil Wait sendiri. Jalankan di bawah -race.
func TestFailedStartupReapedViaChannel(t *testing.T) {
	sleep := lookSleep(t)
	// `sleep` tanpa argumen: Start sukses (proses nyata), lalu exit instan
	// dengan usage error — replikasi jalur failed-startup H5.
	s := mustStart(t, sleep)

	select {
	case r := <-s.Reaped():
		if r.err == nil {
			t.Fatal("exit instan harus menghasilkan error (usage), dapat nil")
		}
		if r.exitedAt.IsZero() {
			t.Fatal("waitResult tanpa exitedAt")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("monitor tidak melihat exit instan via channel reaping (H5 regresi)")
	}

	// Wait() setelahnya membaca memo, bukan Wait kedua.
	if err := s.Wait(); err == nil {
		t.Fatal("Wait() harus membaca memo error yang sama")
	}
}

// 5. Start gagal untuk binary tak ada: error kembali, tanpa goroutine reaper.
func TestStartNonexistentFails(t *testing.T) {
	if _, err := Start("avior-definitely-not-a-binary-xyz"); err == nil {
		t.Fatal("Start binary tak ada harus error")
	}
}
