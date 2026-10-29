// Orchestrator serve: Plan → Launch → Supervise (arc:avior-architecture).
//
// internal/runtime TIDAK god package: proses → internal/processgroup,
// trust CA → internal/trust, Caddy → internal/proxy, /etc/hosts →
// internal/hostname, state per-proyek → internal/state. Package ini hanya
// koordinasi; kebijakan serve (serveopts) dan keputusan queue-flag tetap di
// sini. Alur virtual-domain penuh (proxy + trust + hostname + readiness)
// ada di serve.go, dipanggil cmd/serve.
package runtime

import (
	"context"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sync"
	"time"

	"github.com/maleolabs/avior/internal/processgroup"
	"github.com/maleolabs/avior/internal/state"
)

// Service adalah satu child yang akan di-launch. Args murni args-list —
// tanpa shell interpolation (construction invariant). Output nil = child
// mewarisi stdout/stderr parent; serve mem-passing buffer bounded agar
// log child tidak membanjiri output CLI.
type Service struct {
	Name   string
	Args   []string
	Output io.Writer
}

// Grace/force default teardown saat satu child exit atau runtime dihentikan.
// ponytail: nilai tetap; jadikan flag --grace saat ada permintaan nyata.
const (
	terminateGrace = 5 * time.Second
	terminateForce = 2 * time.Second
)

// StartOptions adalah input Start. Port (bila != 0) dicatat di runtime
// state proyek sehingga monitor/attach bisa menampilkannya.
type StartOptions struct {
	Dir      string
	Port     int
	Services []Service
}

// Exit melaporkan child pertama yang berhenti (semantik legacy: satu child
// gagal, runtime berakhir).
type Exit struct {
	Service string
	Err     error
}

// Supervision adalah handle atas sekumpulan child yang sedang diawasi.
// Konkurensi: Start mengembalikan setelah SEMUA child ter-launch; Wait dan
// Stop aman dipanggil dari goroutine mana pun; keduanya idempoten.
type Supervision struct {
	ctx    context.Context
	cancel context.CancelFunc

	services  []Service
	sup       []*processgroup.Supervised
	unreg     []func() error
	statePath string
	stateOwn  state.Process

	raw   chan childExit // semua event exit child
	first chan Exit      // fan-out child PERTAMA (cap 1, tidak pernah block)

	once   sync.Once
	done   chan struct{}
	result error
}

type childExit struct {
	index int
	sup   *processgroup.Supervised
}

// Start me-launch semua service di dir proyek dan mulai mengawasi.
// Kegagalan launch salah satu child men-teardown yang sudah jalan.
func Start(ctx context.Context, o StartOptions) (*Supervision, error) {
	statePath, registryPath, err := state.Paths(o.Dir)
	if err != nil {
		return nil, err
	}
	if err := processgroup.CleanupStaleProcesses(registryPath); err != nil {
		log.Printf("runtime: cleanup stale processes: %v", err)
	}
	ctx, cancel := context.WithCancel(ctx)
	s := &Supervision{
		ctx:       ctx,
		cancel:    cancel,
		services:  o.Services,
		raw:       make(chan childExit, len(o.Services)),
		first:     make(chan Exit, 1),
		done:      make(chan struct{}),
		statePath: statePath,
	}
	supervised := make([]*processgroup.Supervised, 0, len(o.Services))
	teardown := func() {
		for _, remove := range s.unreg {
			if err := remove(); err != nil {
				log.Printf("runtime: unregister process: %v", err)
			}
		}
		stopAll(supervised)
		if s.stateOwn.PID != 0 {
			if err := state.RemoveStateIfOwner(s.statePath, s.stateOwn); err != nil {
				log.Printf("runtime: remove state: %v", err)
			}
		}
	}
	for i, svc := range o.Services {
		cmd := exec.Command(svc.Name, svc.Args...) // args-list saja, tanpa shell
		cmd.Dir = o.Dir
		if svc.Output != nil {
			cmd.Stdout, cmd.Stderr = svc.Output, svc.Output
		}
		sup, err := processgroup.StartCmd(cmd)
		if err != nil {
			teardown()
			cancel()
			return nil, fmt.Errorf("runtime: start %s: %w", svc.Name, err)
		}
		supervised = append(supervised, sup)
		remove, err := processgroup.RegisterProcess(registryPath, cmd.String(), sup.PID())
		if err != nil {
			teardown()
			cancel()
			return nil, fmt.Errorf("runtime: register %s: %w", svc.Name, err)
		}
		s.unreg = append(s.unreg, remove)
		if i == 0 {
			s.stateOwn = sup.Identity()
			if err := state.SaveState(statePath, state.RuntimeState{ProjectRoot: o.Dir, Port: o.Port, MainPID: s.stateOwn}); err != nil {
				teardown()
				cancel()
				return nil, fmt.Errorf("runtime: save state: %w", err)
			}
		}
		go func(i int, sup *processgroup.Supervised) {
			<-sup.Reaped()
			s.raw <- childExit{index: i, sup: sup}
		}(i, sup)
	}
	s.sup = supervised
	go s.supervise(teardown)
	return s, nil
}

// supervise mengawasi: child PERTAMA yang exit mengakhiri runtime (teardown
// semua), ctx.Done (Ctrl+C) memicu teardown graceful dengan hasil nil.
func (s *Supervision) supervise(teardown func()) {
	defer close(s.done)
	defer teardown()
	select {
	case <-s.ctx.Done():
		s.result = nil
	case e := <-s.raw:
		err := e.sup.Wait()
		stopAll(s.sup)
		first := Exit{Service: s.services[e.index].Name, Err: err}
		// Fan-out non-blocking: cap 1 menjamin pengirim pertama selalu muat;
		// exit child berikutnya di-drop (hanya yang pertama bermakna).
		select {
		case s.first <- first:
		default:
		}
		if err != nil {
			s.result = fmt.Errorf("runtime: %s berhenti: %w", first.Service, err)
		} else {
			s.result = fmt.Errorf("runtime: %s berhenti", first.Service)
		}
	}
}

// Exited mengembalikan channel yang menerima child pertama yang berhenti
// (untuk race readiness vs child exit). Nilai tetap terbuffer bila tidak
// ada pembaca; membaca lebih dari satu kali tidak menghasilkan apa-apa.
func (s *Supervision) Exited() <-chan Exit { return s.first }

// Wait menunggu runtime berakhir: child pertama exit (error) atau ctx.Done
// (nil). Idempoten.
func (s *Supervision) Wait() error {
	<-s.done
	return s.result
}

// Stop men-teardown seluruh child secara graceful. Idempoten; Wait tetap
// harus dipanggil untuk menunggu teardown selesai.
func (s *Supervision) Stop() { s.cancel() }

// Run menjalankan siklus Plan-Launch-Supervise sampai berakhir. Dipertahankan
// untuk pemanggil sederhana (e2e); serve memakai Start + Wait.
func Run(ctx context.Context, dir string, services []Service) error {
	s, err := Start(ctx, StartOptions{Dir: dir, Services: services})
	if err != nil {
		return err
	}
	return s.Wait()
}

// stopAll men-teardown seluruh group: sinyal graceful ke SEMUA child dulu
// (BeginTerminate), baru tunggu satu-satu — meminimalkan worst-case
// grace×N sebelum SIGTERM terakhir terkirim. Terminate idempotent — aman
// untuk child yang sudah reaped.
func stopAll(group []*processgroup.Supervised) {
	for _, sup := range group {
		sup.BeginTerminate(terminateGrace, terminateForce)
	}
	for _, sup := range group {
		_ = sup.Terminate(terminateGrace, terminateForce)
	}
}
