// SINGLE-WAIT OWNERSHIP INVARIANT (fix review H5: double-Wait race).
//
// TEPAT SATU goroutine per Supervised yang memanggil cmd.Wait() — goroutine
// reaper yang di-spawn di Start(). Pihak lain (startup monitor, Terminate,
// Wait(), serve loop) WAJIB mengonsumsi channel reaping via Reaped()/Wait()
// — JANGAN pernah memanggil cmd.Wait() sendiri. Wait kedua pada child yang
// sama menghasilkan "waitid: no child processes" / error tak terduga karena
// reap itu side-effect one-shot: siapa pun yang menang memungut exit status,
// pihak kedua tidak mendapat apa-apa.
//
// catatan: split ke internal/processgroup (registry identity-checked +
// privilege + process-group kill) terjadi di work item p1-foundation; di
// sini supervisor tinggal inti Wait-ownership dalam package runtime.
package runtime

import (
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/maleolabs/avior/internal/state"
)

// waitResult adalah hasil reap satu proses.
type waitResult struct {
	err      error
	exitedAt time.Time
}

// Supervised membungkus satu exec.Cmd dengan kepemilikan Wait yang tunggal.
type Supervised struct {
	cmd    *exec.Cmd
	reapCh chan waitResult

	identity state.Process

	terminateOnce sync.Once // eskalasi SIGTERM->SIGKILL hanya dijalankan sekali

	mu     sync.Mutex
	result *waitResult // memoized oleh konsumen pertama reapCh
}

// Start menjalankan command (args-list saja, tanpa shell — konvensi repo)
// dan men-spawn TEPAT SATU goroutine reaper pemilik cmd.Wait().
func Start(name string, args ...string) (*Supervised, error) {
	cmd := exec.Command(name, args...) // ponytail: no extra config (env/dir) yet — add when serve needs it
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("runtime: start %s: %w", name, err)
	}
	s := &Supervised{
		cmd:    cmd,
		reapCh: make(chan waitResult, 1),
		identity: state.Process{
			PID:       cmd.Process.Pid,
			Command:   cmd.Path,
			StartedAt: time.Now(),
		},
	}
	go func() {
		err := cmd.Wait() // satu-satunya pemanggil Wait di seluruh supervisor
		r := waitResult{err: err, exitedAt: time.Now()}
		// Memoize SEBELUM kirim: konsumen yang mengambil nilai via channel
		// maupun yang memanggil Wait() setelahnya membaca hasil yang sama —
		// tidak ada jalur konsumsi yang bisa saling mengunci/mengosongkan.
		s.remember(r)
		s.reapCh <- r
	}()
	return s, nil
}

// Identity mengembalikan identitas proses tercatat (konvensi registry:
// verifikasi identitas sebelum sinyal; kill by-PID-only ada di processgroup).
func (s *Supervised) Identity() state.Process { return s.identity }

// PID adalah PID child (untuk logging/registry; JANGAN dipakai untuk sinyal
// tanpa verifikasi identitas).
func (s *Supervised) PID() int { return s.identity.PID }

// Reaped mengembalikan channel reaping untuk konsumen non-Wait (startup
// monitor, serve loop select). Channel buffered(1) dan hasilnya juga
// dimemoized, jadi konsumen bergantian tetap aman.
func (s *Supervised) Reaped() <-chan waitResult { return s.reapCh }

// Wait menunggu exit child via channel reaping (BUKAN cmd.Wait). Hasilnya
// dimemoized: pemanggilan ulang setelah Terminate tetap mengembalikan nilai.
func (s *Supervised) Wait() error {
	return s.consume().err
}

// ExitedAt mengembalikan waktu exit (zero time bila belum reaped).
func (s *Supervised) ExitedAt() time.Time {
	return s.consume().exitedAt
}

// consume memungut hasil reap tepat satu kali: konsumen pertama channel
// memutkan memo, konsumen berikutnya membaca memo.
func (s *Supervised) consume() waitResult {
	if r, ok := s.recall(); ok {
		return r
	}
	r := <-s.reapCh
	s.remember(r)
	return r
}

func (s *Supervised) remember(r waitResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.result == nil {
		s.result = &r
	}
}

func (s *Supervised) recall() (waitResult, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.result == nil {
		return waitResult{}, false
	}
	return *s.result, true
}

// Terminate menghentikan child: sinyal graceful, tunggu reaped hingga grace,
// eskalasi Kill, tunggu reaped hingga force lagi. Idempotent — Terminate
// ganda aman (eskalasi dijalankan sekali via terminateOnce; pemanggil kedua
// tinggal menunggu hasil reaping yang sama). Reap TETAP via channel yang
// sama; tidak ada Wait kedua.
func (s *Supervised) Terminate(grace, force time.Duration) error {
	s.terminateOnce.Do(func() {
		// Sinyal graceful; error diabaikan secara sadar — child mungkin
		// sudah exit sendiri, dan eskalasi tetap menutup kasus itu.
		_ = s.signalGraceful()
		time.AfterFunc(grace, func() {
			_ = s.cmd.Process.Kill() // ponytail: no process-group kill yet — lands with internal/processgroup at p1
		})
	})
	deadline := time.NewTimer(grace + force)
	defer deadline.Stop()
	for {
		if r, ok := s.recall(); ok {
			return r.err
		}
		select {
		case r := <-s.reapCh:
			s.remember(r)
			return r.err
		case <-deadline.C:
			if r, ok := s.recall(); ok {
				return r.err
			}
			return fmt.Errorf("runtime: proses pid %d tidak reaped dalam grace+force (%s+%s)", s.identity.PID, grace, force)
		}
	}
}

// signalGraceful mengirim SIGTERM. Windows tidak mendukung SIGTERM —
// fail-closed ke Kill (graceful Windows via job object menyusul di p1).
func (s *Supervised) signalGraceful() error {
	if err := s.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		return s.cmd.Process.Kill()
	}
	return nil
}
