// Orchestrator serve: Plan → Launch → Supervise (arc:avior-architecture).
//
// internal/runtime TIDAK lagi god package: proses → internal/processgroup
// (registry identity-checked, privilege, signal, supervisor single-wait),
// trust CA → internal/trust, Caddy → internal/proxy, /etc/hosts →
// internal/hostname, state per-proyek → internal/state. Package ini hanya
// koordinasi; kebijakan serve (serveopts) dan keputusan queue-flag tetap di
// sini karena milik orchestrator serve.
//
// ponytail: alur virtual-domain (proxy + trust + hostname + readiness
// Caddy/HTTPS) belum di-wire — panggilan itu milik cmd/serve saat cmd
// terbentuk (p2 wiring); di sini inti supervisi supaya dependency runtime
// tetap linear (runtime → processgroup → state → brand).
package runtime

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/maleolabs/avior/internal/processgroup"
)

// Service adalah satu child yang akan di-launch. Args murni args-list —
// tanpa shell interpolation (construction invariant).
type Service struct {
	Name string
	Args []string
}

// Grace/force default teardown saat satu child exit atau runtime dihentikan.
// ponytail: nilai tetap; jadikan flag --grace saat ada permintaan nyata.
const (
	terminateGrace = 5 * time.Second
	terminateForce = 2 * time.Second
)

// Plan fase 1: intent (queue-flag semantics, fix H1) + evidence hasil
// discovery → daftar service yang akan di-launch.
func PlanServices(o Options, evidence Plan) []Service {
	services := []Service{{Name: "php", Args: []string{"artisan", "serve"}}}
	if ShouldStartHorizon(o, evidence) {
		services = append(services, Service{Name: "php", Args: []string{"artisan", "horizon"}})
	}
	return services
}

// Run menjalankan siklus Plan-Launch-Supervise: launch semua service hasil
// Plan di dir proyek, lalu mengawasi. Child pertama yang exit mengakhiri
// runtime — sisanya di-teardown (semantik legacy: satu child gagal, teardown
// semua). ctx.Done (Ctrl+C via signal.NotifyContext pemanggil) memicu teardown
// graceful lalu nil.
func Run(ctx context.Context, dir string, services []Service) error {
	supervised := make([]*processgroup.Supervised, 0, len(services))
	for _, s := range services {
		cmd := exec.Command(s.Name, s.Args...) // args-list saja, tanpa shell
		cmd.Dir = dir
		sup, err := processgroup.StartCmd(cmd)
		if err != nil {
			stopAll(supervised)
			return fmt.Errorf("runtime: start %s: %w", s.Name, err)
		}
		supervised = append(supervised, sup)
	}
	// Supervise: tunggu exit child pertama ATAU pembatalan context.
	for i, sup := range supervised {
		select {
		case <-ctx.Done():
			stopAll(supervised)
			return nil
		case <-sup.Reaped():
			err := sup.Wait()
			stopAll(supervised)
			if err != nil {
				return fmt.Errorf("runtime: %s berhenti: %w", services[i].Name, err)
			}
			return fmt.Errorf("runtime: %s berhenti", services[i].Name)
		}
	}
	return nil
}

// stopAll men-teardown seluruh group secara graceful (Terminate idempotent —
// aman untuk child yang sudah reaped).
func stopAll(group []*processgroup.Supervised) {
	for _, sup := range group {
		_ = sup.Terminate(terminateGrace, terminateForce)
	}
}
