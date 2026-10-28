// SINGLE-WAIT OWNERSHIP INVARIANT (fix review H5: double-Wait race).
//
// TEPAT SATU goroutine per Supervised yang memanggil cmd.Wait() — goroutine
// reaper yang di-spawn di Start(). Pihak lain (startup monitor, Terminate,
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

// waitResult adalah hasil reap satu proses. ponytail: tetap unexported
// karena startup monitor tinggal in-package; export WaitResult saat monitor
// pindah ke internal/processgroup di p1.
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
	cmd := exec.Command(name, args...) // ponytail: no extra config (env/dir) yet — add when serve needs it
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("runtime: start %s: %w", name, err)
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
// verifikasi identitas sebelum sinyal; kill by-PID-only ada di processgroup).
func (s *Supervised) Identity() state.Process { return s.identity }

// PID adalah PID child (untuk logging/registry; JANGAN dipakai untuk sinyal
// tanpa verifikasi identitas).
func (s *Supervised) PID() int { return s.identity.PID }

// Reaped mengembalikan channel yang di-close saat child reaped (broadcast:
// membangunkan semua observer, bukan sekali untuk satu penerima). Detail
// hasil (error, waktu exit) diambil lewat Wait()/ExitedAt() yang memakai
// memo — JANGAN mencoba memungut hasil dari channel ini. Konsumen in-package
// (startup monitor, serve loop) juga memakai pasangan ini;
// ponytail: Reaped mengembalikan <chan struct{}> (bukan waitResult) agar API
// jujur terhadap package luar — upgrade path: export WaitResult saat monitor
// pindah ke internal/processgroup di p1.
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
		panic("runtime: done tertutup tanpa memo — pelanggaran invariant reaper")
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

// Terminate menghentikan child: sinyal graceful, tunggu reaped hingga grace,
// eskalasi Kill, tunggu hingga force lagi. Idempotent — Terminate ganda aman
// (sinyal + eskalasi dijalankan sekali via terminateOnce; pemanggil kedua
// menunggu broadcast done yang sama). Reap TETAP milik satu goroutine
// reaper; Terminate hanya mengonsumsi broadcast.
func (s *Supervised) Terminate(grace, force time.Duration) error {
	s.terminateOnce.Do(func() {
		// Sinyal graceful; error diabaikan secara sadar — child mungkin
		// sudah exit sendiri, dan eskalasi tetap menutup kasus itu.
		_ = s.signalGraceful()
		s.escalate = time.AfterFunc(grace, func() {
			_ = s.cmd.Process.Kill() // ponytail: no process-group kill yet — lands with internal/processgroup at p1
		})
	})
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
			panic("runtime: done tertutup tanpa memo — pelanggaran invariant reaper")
		case <-deadline.C:
			if r, ok := s.recall(); ok {
				s.stopEscalation()
				return r.err
			}
			// Benar-benar belum exit (bukan konsumen terlambat — konsumen
			// terlambat tetap menemukan memo via recall di atas).
			return fmt.Errorf(
				"runtime: proses pid %d belum exit dalam grace+force (%s+%s); reaper tetap berjalan, hasil tersedia via Wait()",
				s.identity.PID, grace, force)
		}
	}
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

// signalGraceful mengirim SIGTERM. Windows tidak mendukung SIGTERM —
// fail-closed ke Kill (graceful Windows via job object menyusul di p1).
func (s *Supervised) signalGraceful() error {
	if err := s.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		return s.cmd.Process.Kill()
	}
	return nil
}
