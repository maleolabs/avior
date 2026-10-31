// Alur serve virtual-domain penuh (item m2-cmd-serve): validasi + consent,
// planning service dari evidence discovery, Caddy config, trust CA, hosts,
// readiness probe, dan output ringkas. Logika domain tinggal di
// internal/runtime — cmd/serve hanya wiring flag (arc:avior-architecture).
package runtime

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"time"

	"github.com/maleolabs/avior/internal/brand"
	"github.com/maleolabs/avior/internal/discovery"
	"github.com/maleolabs/avior/internal/hostname"
	"github.com/maleolabs/avior/internal/mode"
	"github.com/maleolabs/avior/internal/proxy"
	"github.com/maleolabs/avior/internal/trust"
)

var (
	fetchManagedCaddy   = proxy.FetchCaddy
	verifyManagedCaddy  = proxy.VerifyManaged
	validateCaddyConfig = proxy.ValidateConfig
	caddyBinaryPin      = proxy.CaddyBinaryPin
)

// ServeOptions adalah seluruh intent serve dari CLI. VirtualDomain/HTTPS/
// TrustLocalCA berisi nilai flag mentah (default production); normalisasi
// mode dilakukan Serve lewat NormalizeServeOptions dengan Changed.
type ServeOptions struct {
	Root string
	Mode mode.Mode

	Host                 string
	Port                 int
	VitePort             int
	CaddyPort            int
	Domain               string
	VirtualDomain        bool
	HTTPS                bool
	TrustLocalCA         bool
	CertStorage          string
	Hosts                bool // consent hosts eksplisit (cmd sudah menuntut --yes)
	Queue                *bool
	Scheduler            *bool
	Changed              map[string]bool
	NonInteractive       bool
	AcceptCaddyDownload  bool
	ConfirmCaddyDownload func() (bool, error)
	OutputMode           OutputMode

	Out io.Writer
}

// OutputMode mengatur verbositas output runtime.
type OutputMode string

const (
	OutputConcise OutputMode = "concise"
	OutputDetail  OutputMode = "detail"
)

// Timeout readiness (paritas codebase pendahulu).
const (
	readinessTimeout = 10 * time.Second
	tlsTimeout       = 10 * time.Second
	adminPort        = 2019
)

// Serve menjalankan runtime terkoordinasi: validasi → plan → launch →
// readiness → trust → tunggu (Ctrl+C teardown bersih). Satu child berhenti
// mengakhiri runtime (semantik legacy).
func Serve(ctx context.Context, o ServeOptions) error {
	if o.Out == nil {
		return fmt.Errorf("serve: Out wajib diisi")
	}
	if o.Mode != mode.ModeDevelopment && o.Mode != mode.ModeProduction {
		return fmt.Errorf("mode harus development atau production")
	}
	if o.Port < 1 || o.Port > 65535 || o.VitePort < 1 || o.VitePort > 65535 {
		return fmt.Errorf("port harus antara 1 dan 65535")
	}
	virtual, https, trustCA := NormalizeServeOptions(o.Mode, o.Changed, o.NonInteractive, o.VirtualDomain, o.HTTPS, o.TrustLocalCA)
	domain := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(o.Domain), "."))
	if virtual {
		if domain == "" {
			domain = defaultDomain()
		}
		if !proxy.ValidLocalDomain(domain) {
			return fmt.Errorf("domain harus localhost-only (.localhost atau .test)")
		}
		// Consent .test (review M-7): domain .test tidak resolves otomatis
		// seperti .localhost — butuh entri hosts, dan mengubah hosts butuh
		// consent eksplisit. Tanpa --hosts, gagal dini (fail-closed).
		if strings.HasSuffix(domain, ".test") && !o.Hosts {
			return fmt.Errorf("domain .test membutuhkan --hosts (dengan --yes) untuk entri hosts")
		}
		if https && !trustCA {
			return fmt.Errorf("HTTPS lokal membutuhkan consent eksplisit: gunakan --trust-local-ca")
		}
		if o.CaddyPort < 1 || o.CaddyPort > 65535 {
			return fmt.Errorf("caddy port harus antara 1 dan 65535")
		}
		if o.CaddyPort == o.Port {
			return fmt.Errorf("caddy port harus berbeda dari port Laravel")
		}
	}

	plan := discovery.Discover(o.Root)
	services, err := planServices(o, plan)
	if err != nil {
		return err
	}

	caddyConfigPath := ""
	configKept := false
	cleanupHost := func() {}
	defer func() {
		if caddyConfigPath != "" && !configKept {
			_ = os.Remove(caddyConfigPath)
		}
		cleanupHost()
	}()
	var trustManager *trust.TrustManager
	if virtual {
		svc, manager, configPath, caddyPort, err := prepareCaddy(ctx, o, domain, https)
		if err != nil {
			return err
		}
		o.CaddyPort = caddyPort
		services = append([]Service{svc}, services...)
		trustManager = manager
		caddyConfigPath = configPath
		if o.Hosts {
			cleanup, err := hostname.EnsureHost(domain, true)
			if err != nil {
				return err
			}
			cleanupHost = cleanup
		}
	}

	// Buffer bounded per child: log child tidak membanjiri output CLI;
	// tail dicetak saat child gagal (diagnostik actionable).
	buffers := make([]*boundedBuffer, len(services))
	for i := range services {
		if services[i].Output == nil {
			buffers[i] = &boundedBuffer{}
			services[i].Output = buffers[i]
		}
	}
	sup, err := Start(ctx, StartOptions{Dir: o.Root, Port: o.Port, Services: services})
	if err != nil {
		return err
	}
	printServeIntro(o, domain, virtual, https)

	// Fase trust otoritatif: admin API Caddy siap → gate hash-before-exec
	// (review M-1) → InstallCaddy (`caddy trust` bisa memanggil sudo).
	if virtual && https && trustCA {
		if err := proxy.WaitReady("127.0.0.1", adminPort, readinessTimeout); err != nil {
			sup.Stop()
			_ = sup.Wait()
			configKept = true
			return fmt.Errorf("admin Caddy belum siap (Caddyfile disimpan di %s): %w", caddyConfigPath, err)
		}
		if err := proxy.VerifyManaged(services[0].Name); err != nil {
			sup.Stop()
			_ = sup.Wait()
			configKept = true
			return fmt.Errorf("binary Caddy tidak terverifikasi untuk trust (hash-before-exec): %w", err)
		}
		results, err := trustManager.InstallCaddy(caddyConfigPath)
		if err != nil {
			sup.Stop()
			_ = sup.Wait()
			configKept = true
			return fmt.Errorf("install CA Caddy gagal (Caddyfile disimpan di %s): %w", caddyConfigPath, err)
		}
		printTrustFailures(o.Out, results)
	}

	// Readiness Laravel dengan race child-exit: child mati duluan = error
	// actionable, bukan timeout menyesatkan.
	if err := waitReadinessOrExit(o, sup); err != nil {
		sup.Stop()
		_ = sup.Wait()
		printExitTails(o.Out, o.OutputMode, services, buffers)
		return err
	}
	if virtual {
		if https {
			if err := proxy.WaitTLSReady(domain, o.CaddyPort, tlsTimeout); err != nil {
				sup.Stop()
				_ = sup.Wait()
				configKept = true
				return fmt.Errorf("proxy Caddy HTTPS belum siap (Caddyfile disimpan di %s): %w", caddyConfigPath, err)
			}
		} else if err := proxy.WaitReady("127.0.0.1", o.CaddyPort, readinessTimeout); err != nil {
			sup.Stop()
			_ = sup.Wait()
			configKept = true
			return fmt.Errorf("proxy Caddy belum siap (Caddyfile disimpan di %s): %w", caddyConfigPath, err)
		}
		// Target browser/OS opsional SETELAH TLS Caddy siap — tidak boleh
		// menunda startup proxy (kontrak internal/trust).
		if https && trustCA {
			results, _ := trustManager.InstallOptionalTargets()
			printTrustFailures(o.Out, results)
		}
		if !o.NonInteractive {
			if https {
				fmt.Fprintf(o.Out, "HTTPS siap\nURL: https://%s:%d\nTrust siap\n", domain, o.CaddyPort)
			} else {
				fmt.Fprintf(o.Out, "URL: http://%s:%d\n", domain, o.CaddyPort)
			}
		}
	}
	fmt.Fprintf(o.Out, "Runtime siap: %s\n", strings.Join(serviceNames(services), ", "))
	if err := sup.Wait(); err != nil {
		printExitTails(o.Out, o.OutputMode, services, buffers)
		if configKept {
			return fmt.Errorf("%w (Caddyfile disimpan di %s)", err, caddyConfigPath)
		}
		return err
	}
	return nil
}

func defaultDomain() string { return DefaultDomain() }

// DefaultDomain adalah virtual domain default (dipakai cmd sebagai nilai
// flag dan Serve bila domain kosong).
func DefaultDomain() string { return brand.Name + ".localhost" }

// PlanServices fase 1: intent (queue-flag semantics, fix H1) + evidence
// hasil discovery → daftar service yang akan di-launch. Dipertahankan sebagai
// kontrak ter-tes; planServices (ServeOptions) mengomposisi di atasnya.
func PlanServices(o Options, evidence Plan) []Service {
	services := []Service{{Name: "php", Args: []string{"artisan", "serve"}}}
	if ShouldStartHorizon(o, evidence) {
		services = append(services, Service{Name: "php", Args: []string{"artisan", "horizon"}})
	}
	return services
}

// planServices menyusun daftar child dari evidence + flag otoritatif
// (fix p0: --queue/--scheduler menang saat di-set; unset = evidence).
func planServices(o ServeOptions, plan discovery.ServicePlan) ([]Service, error) {
	intent := Options{Queue: o.Queue, Scheduler: o.Scheduler}
	p := Plan{Horizon: plan.Horizon, Scheduler: plan.Scheduler, Vite: plan.Vite}
	services := PlanServices(intent, p)
	services[0] = Service{Name: "php", Args: []string{"artisan", "serve", "--host", o.Host, "--port", strconv.Itoa(o.Port)}}
	if ShouldStartScheduler(intent, p) {
		services = append(services, Service{Name: "php", Args: []string{"artisan", "schedule:work"}})
	}
	if o.Mode == mode.ModeDevelopment {
		// Pail dev-only, evidence-gated: child yang gagal mengakhiri runtime,
		// jadi jangan launch bila paketnya tidak ada.
		if planHasPail(o.Root) {
			services = append(services, Service{Name: "php", Args: []string{"artisan", "pail"}})
		}
		if plan.Vite {
			services = append(services, Service{Name: "npm", Args: []string{"run", "dev", "--", "--host", o.Host, "--port", strconv.Itoa(o.VitePort)}})
		}
	}
	return services, nil
}

func planHasPail(root string) bool {
	if _, err := os.Stat(filepath.Join(root, "vendor", "laravel", "pail")); err == nil {
		return true
	}
	b, err := os.ReadFile(filepath.Join(root, "composer.json"))
	return err == nil && strings.Contains(string(b), "laravel/pail")
}

// prepareCaddy me-resolve binary, menulis + memvalidasi config, memilih
// port, dan menyiapkan storage cert. Binary PATH-fallback hanya boleh untuk
// child `caddy run` non-privileged; jalur privileged (`caddy trust`)
// divalidasi terpisah lewat proxy.VerifyManaged (hash-before-exec, M-1).
func prepareCaddy(ctx context.Context, o ServeOptions, domain string, https bool) (Service, *trust.TrustManager, string, int, error) {
	caddy := proxy.ManagedCaddyPath()
	if _, err := os.Stat(caddy); err == nil {
		if err := verifyManagedCaddy(caddy); err != nil {
			return Service{}, nil, "", 0, fmt.Errorf("Caddy terkelola gagal diverifikasi: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return Service{}, nil, "", 0, err
	} else {
		if o.Mode == mode.ModeDevelopment {
			if pathBinary, pathErr := proxy.CaddyPath(o.Root); pathErr == nil {
				if err := proxy.VerifyPATH(pathBinary); err != nil {
					return Service{}, nil, "", 0, err
				}
				caddy = pathBinary
			} else {
				return Service{}, nil, "", 0, pathErr
			}
		} else {
			if _, ok := caddyBinaryPin(goruntime.GOOS, goruntime.GOARCH); !ok {
				return Service{}, nil, "", 0, fmt.Errorf("pin checksum Caddy untuk %s/%s tidak tersedia; tambahkan pin di internal/proxy/pins.go", goruntime.GOOS, goruntime.GOARCH)
			}
			if !o.AcceptCaddyDownload {
				if o.NonInteractive || o.ConfirmCaddyDownload == nil {
					return Service{}, nil, "", 0, fmt.Errorf("Caddy belum terpasang; unduhan membutuhkan consent. Jalankan ulang dengan --yes atau gunakan terminal interaktif")
				}
				accepted, err := o.ConfirmCaddyDownload()
				if err != nil {
					return Service{}, nil, "", 0, fmt.Errorf("konfirmasi unduhan Caddy: %w", err)
				}
				if !accepted {
					return Service{}, nil, "", 0, fmt.Errorf("unduhan Caddy dibatalkan")
				}
			}
			if err := fetchManagedCaddy(ctx, caddy); err != nil {
				return Service{}, nil, "", 0, err
			}
			if err := verifyManagedCaddy(caddy); err != nil {
				return Service{}, nil, "", 0, fmt.Errorf("Caddy hasil unduhan gagal diverifikasi: %w", err)
			}
		}
	}
	port, err := proxy.SelectCaddyPort(o.CaddyPort, o.Port)
	if err != nil {
		return Service{}, nil, "", 0, err
	}
	if port != o.CaddyPort {
		fmt.Fprintf(o.Out, "Port Caddy %d tidak tersedia; memakai port %d.\n", o.CaddyPort, port)
	}
	storage := o.CertStorage
	if storage == "" {
		storage = filepath.Join(brand.StateDir(), "caddy")
	}
	if https {
		if strings.ContainsAny(storage, "\r\n") {
			return Service{}, nil, "", 0, fmt.Errorf("cert-storage tidak boleh mengandung newline")
		}
		if !filepath.IsAbs(storage) {
			return Service{}, nil, "", 0, fmt.Errorf("cert-storage harus berupa path absolut")
		}
		if err := os.MkdirAll(storage, 0700); err != nil {
			return Service{}, nil, "", 0, err
		}
		if err := os.Chmod(storage, 0700); err != nil {
			return Service{}, nil, "", 0, fmt.Errorf("amankan cert-storage: %w", err)
		}
	}
	config := proxy.CaddyConfig(domain, "127.0.0.1:"+strconv.Itoa(o.Port), port, https, storage)
	f, err := os.CreateTemp("", brand.Name+"-Caddyfile-")
	if err != nil {
		return Service{}, nil, "", 0, err
	}
	if _, err := f.WriteString(config); err != nil {
		f.Close()
		return Service{}, nil, "", 0, err
	}
	f.Close()
	if err := validateCaddyConfig(caddy, f.Name()); err != nil {
		return Service{}, nil, "", 0, fmt.Errorf("konfigurasi Caddy invalid (Caddyfile disimpan di %s): %w", f.Name(), err)
	}
	manager := trust.NewTrustManager(caddy, storage, o.Root)
	svc := Service{Name: caddy, Args: []string{"run", "--config", f.Name(), "--adapter", "caddyfile"}}
	return svc, manager, f.Name(), port, nil
}

func printTrustFailures(out io.Writer, results []trust.TrustTargetResult) {
	for _, r := range results {
		if r.Status == trust.TrustTargetFailed {
			fmt.Fprintf(out, "trust %s gagal: %s\n", r.Target, r.Error)
		}
	}
}

// waitReadinessOrExit menunggu port Laravel siap; child yang mati duluan
// menang race dan dilaporkan (paritas waitReadyOrProcess legacy).
func waitReadinessOrExit(o ServeOptions, sup *Supervision) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.After(readinessTimeout)
	for {
		select {
		case e := <-sup.Exited():
			if e.Err != nil {
				return fmt.Errorf("runtime berhenti (%s): %w", e.Service, e.Err)
			}
			return fmt.Errorf("runtime berhenti (%s)", e.Service)
		case <-deadline:
			return fmt.Errorf("server Laravel belum siap pada %s:%d", o.Host, o.Port)
		case <-ticker.C:
			if proxy.WaitReady(o.Host, o.Port, 200*time.Millisecond) == nil {
				return nil
			}
		}
	}
}

// boundedBuffer menahan log child dengan batas; tail dipakai untuk
// diagnostik kegagalan startup. Konkurensi Write dari exec.Cmd
// (stdout+stderr digabung) butuh mutex — channel semafor cukup.
type boundedBuffer struct {
	sem chan struct{}
	buf []byte
}

const bufferLimit = 64 * 1024

func (b *boundedBuffer) lock() {
	if b.sem == nil {
		b.sem = make(chan struct{}, 1)
	}
	b.sem <- struct{}{}
}

func (b *boundedBuffer) unlock() { <-b.sem }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.lock()
	defer b.unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > bufferLimit {
		b.buf = b.buf[len(b.buf)-bufferLimit:]
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	b.lock()
	defer b.unlock()
	return string(b.buf)
}

// printExitTails mencetak tail log child — tanpa ini, user hanya melihat
// "berhenti" tanpa penyebab.
func printExitTails(out io.Writer, mode OutputMode, services []Service, buffers []*boundedBuffer) {
	limit := 2000
	if mode == OutputDetail {
		limit = 4096
	}
	for i, buf := range buffers {
		if buf == nil {
			continue
		}
		if tail := diagnosticTail(buf.String(), limit); tail != "" {
			fmt.Fprintf(out, "log %s: %s\n", services[i].Name, tail)
		}
	}
}

func diagnosticTail(value string, limit int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) > limit {
		value = value[len(value)-limit:]
	}
	return strings.ReplaceAll(strings.ReplaceAll(value, "\r", ""), "\n", " | ")
}

func serviceNames(services []Service) []string {
	names := make([]string, len(services))
	for i, s := range services {
		names[i] = s.Name
	}
	return names
}

func printServeIntro(o ServeOptions, domain string, virtual, https bool) {
	if virtual {
		scheme := "http"
		if https {
			scheme = "https"
		}
		fmt.Fprintf(o.Out, "\n╭─ %s SERVE · %s\n│  %s://%s:%d → 127.0.0.1:%d\n╰─\n", strings.ToUpper(brand.Name), o.Mode, scheme, domain, o.CaddyPort, o.Port)
		return
	}
	fmt.Fprintf(o.Out, "\n╭─ %s SERVE · %s\n│  http://%s:%d\n╰─\n", strings.ToUpper(brand.Name), o.Mode, o.Host, o.Port)
}
