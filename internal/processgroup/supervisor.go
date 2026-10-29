// SINGLE-WAIT OWNERSHIP INVARIANT (fix review H5: double-Wait race).
//
// TEPAT SATU goroutine per Supervised yang memanggil cmd.Wait() — goroutine
// reaper yang di-spawn di StartCmd(). Pihak lain (startup monitor, Terminate,
// Wait(), serve loop) WAJIB menunggu via Wait()/Terminate()/Reaped() —
// JANGAN pernah memanggil cmd.Wait() sendiri. Wait kedua pada child yang
// sama menghasilkan "waitid: no child processes" / error tak terduga karena
// reap itu side-effect one-shot: siapa pun yang menang memungut exit status,
// pihak kedua tidak mendapat apa-apa.
//
// BROADCAST, BUKAN CHANNEL SATU-KONSUMEN (fix review HIGH lanjutan):
// konsumen kedua yang block di `<-reapCh` SEBELUM memo terisi akan hang
// selamanya — channel mengirim nilai tepat sekali untuk SATU penerima, dan
// si penerima itu yang memutkan memo; konsumen kedua tidak pernah dibangun-
// kan. Karena itu hasil reaping disebarkan via channel `done` yang di-close:
// close membangunkan SEMUA waiter sekaligus, dan setiap waiter membaca memo
// (terjamin terisi: remember() selalu mendahului close(done)).
package processgroup

import (
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/maleolabs/avior/internal/state"
)

// waitResult adalah hasil reap satu proses. ponytail: tetap unexported
// sampai monitor per-proyek butuh detail hasil lintas package; export
// WaitResult saat monitor dibangun di atas state + processgroup.
type waitResult struct {
	err      error
	exitedAt time.Time
}

// Supervised membungkus satu exec.Cmd dengan kepemilikan Wait yang tunggal.
type Supervised struct {
	cmd *exec.Cmd
	// done di-close oleh reaper tepat setelah memo terisi: broadcast exit
	// ke semua konsumen (Wait, Terminate, monitor) tanpa risiko hang.
	done chan struct{}

	identity state.Process

	terminateOnce sync.Once   // sinyal graceful + eskalasi hanya dijalankan sekali
	escalate      *time.Timer // timer Kill; dibuat dalam terminateOnce

	mu     sync.Mutex
	result *waitResult // dimutkan oleh reaper sebelum close(done)
}

// Start menjalankan command (args-list saja, tanpa shell — konvensi repo)
// dan men-spawn TEPAT SATU goroutine reaper pemilik cmd.Wait().
func Start(name string, args ...string) (*Supervised, error) {
	return StartCmd(exec.Command(name, args...))
}

// StartCmd menjalankan cmd yang sudah dikonfigurasi pemanggil (Dir, Env,
// Stdout) dengan kontrak kepemilikan Wait yang sama dengan Start.
func StartCmd(cmd *exec.Cmd) (*Supervised, error) {
	ConfigureProcessGroup(cmd) // pid == pgid: sinyal menjangkau subtree child
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("processgroup: start %s: %w", cmd.Path, err)
	}
	s := &Supervised{
		cmd:  cmd,
		done: make(chan struct{}),
		identity: state.Process{
			PID:       cmd.Process.Pid,
			Command:   cmd.Path,
			StartedAt: time.Now(),
		},
	}
	go func() {
		err := cmd.Wait() // satu-satunya pemanggil Wait di seluruh supervisor
		r := waitResult{err: err, exitedAt: time.Now()}
		// Urutan WAJIB: memoize dulu, lalu close. Waiter yang dibangunkan
		// oleh close(done) dijamin menemukan memo terisi — tidak ada jalur
		// konsumsi yang bisa saling mengunci/mengosongkan.
		s.remember(r)
		close(s.done)
	}()
	return s, nil
}

// Identity mengembalikan identitas proses tercatat (konvensi registry:
// verifikasi identitas sebelum sinyal; jangan kill by-PID-only).
func (s *Supervised) Identity() state.Process { return s.identity }

// PID adalah PID child (untuk logging/registry; JANGAN dipakai untuk sinyal
// tanpa verifikasi identitas).
func (s *Supervised) PID() int { return s.identity.PID }

// Reaped mengembalikan channel yang di-close saat child reaped (broadcast:
// membangunkan semua observer, bukan sekali untuk satu penerima). Detail
// hasil (error, waktu exit) diambil lewat Wait()/ExitedAt() yang memakai
// memo — JANGAN mencoba memungut hasil dari channel ini.
func (s *Supervised) Reaped() <-chan struct{} { return s.done }

// Wait menunggu exit child via broadcast done (BUKAN cmd.Wait). Hasilnya
// dimemoized: pemanggilan ulang (juga setelah Terminate) tetap mengembalikan
// nilai yang sama, dan banyak Wait konkuren aman.
func (s *Supervised) Wait() error {
	return s.consume().err
}

// ExitedAt mengembalikan waktu exit (zero time bila belum reaped).
func (s *Supervised) ExitedAt() time.Time {
	return s.consume().exitedAt
}

// consume memungut hasil reap: memo dulu; bila kosong, tunggu broadcast
// done lalu baca ulang memo (terjamin terisi oleh urutan remember->close
// di reaper). Tidak ada pembacaan channel hasil secara langsung di sini —
// itu pola yang menyebabkan hang konsumen kedua (review HIGH).
func (s *Supervised) consume() waitResult {
	if r, ok := s.recall(); ok {
		return r
	}
	<-s.done
	r, ok := s.recall()
	if !ok {
		// Tidak mungkin: close(done) hanya terjadi setelah remember().
		panic("processgroup: done tertutup tanpa memo — pelanggaran invariant reaper")
	}
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

// Terminate menghentikan child: sinyal graceful ke process group, tunggu
// reaped hingga grace, eskalasi Kill ke process group, tunggu hingga force
// lagi. Idempotent — Terminate ganda aman (sinyal + eskalasi dijalankan
// sekali via terminateOnce; pemanggil kedua menunggu broadcast done yang
// sama), dan untuk pemanggil konkuren parameter grace/force dari pemanggil
// PERTAMA yang menang. Reap TETAP milik satu goroutine reaper; Terminate
// hanya mengonsumsi broadcast.
func (s *Supervised) Terminate(grace, force time.Duration) error {
	s.BeginTerminate(grace, force)
	deadline := time.NewTimer(grace + force)
	defer deadline.Stop()
	for {
		if r, ok := s.recall(); ok {
			s.stopEscalation()
			return r.err
		}
		select {
		case <-s.done:
			s.stopEscalation()
			if r, ok := s.recall(); ok {
				return r.err
			}
			// Tidak mungkin (remember selalu mendahului close) — fail
			// loud alih-alih mengembalikan nil palsu.
			panic("processgroup: done tertutup tanpa memo — pelanggaran invariant reaper")
		case <-deadline.C:
			if r, ok := s.recall(); ok {
				s.stopEscalation()
				return r.err
			}
			// Benar-benar belum exit (bukan konsumen terlambat — konsumen
			// terlambat tetap menemukan memo via recall di atas).
			return fmt.Errorf(
				"processgroup: proses pid %d belum exit dalam grace+force (%s+%s); reaper tetap berjalan, hasil tersedia via Wait()",
				s.identity.PID, grace, force)
		}
	}
}

// BeginTerminate menjadwalkan sinyal graceful + timer eskalasi Kill tepat
// sekali (terminateOnce) TANPA menunggu exit. Dipakai stopAll agar SIGTERM
// terkirim ke semua child sebelum ada pemanggil yang menunggu. Untuk
// pemanggil konkuren, parameter grace/force pemanggil pertama yang menang.
func (s *Supervised) BeginTerminate(grace, force time.Duration) {
	s.terminateOnce.Do(func() {
		// Sinyal graceful; error diabaikan secara sadar — child mungkin
		// sudah exit sendiri, dan eskalasi tetap menutup kasus itu.
		_ = s.signalGraceful()
		s.escalate = time.AfterFunc(grace, func() {
			_ = KillGroup(s.cmd.Process.Pid)
		})
	})
}

// stopEscalation membatalkan timer Kill bila child sudah reaped sebelum
// grace habis — Kill pada proses yang sudah exit memang no-op aman
// (ErrProcessDone), tapi timer mati mencegah tembakan sisa yang tak perlu.
func (s *Supervised) stopEscalation() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.escalate != nil {
		s.escalate.Stop()
	}
}

// signalGraceful mengirim SIGTERM ke process group child (pid == pgid karena
// Setpgid di StartCmd) agar subtree ikut menerima sinyal — semantik legacy
// terminateProcess. Windows tidak mendukung sinyal — fail-closed ke Kill
// (graceful Windows via job object menyusul).
func (s *Supervised) signalGraceful() error {
	if err := SignalGroup(s.cmd.Process.Pid, syscall.SIGTERM); err == nil {
		return nil
	}
	if err := s.cmd.Process.Signal(syscall.SIGTERM); err == nil {
		return nil
	}
	return s.cmd.Process.Kill()
}
