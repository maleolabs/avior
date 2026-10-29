//go:build e2e

// Harness smoke e2e tool (sto:p2-e2e-smoke). cmd/ belum ada, jadi siklus
// doctor-setup-serve-stop dieksekusi lewat entry point internal yang sudah
// ter-wire (setup.Run, runtime.Run, proxy, hostname, state, processgroup)
// dengan proses NYATA: php, composer, npm, node, dan binary Caddy asli.
// Jalankan: go test -tags e2e ./e2e -v
// Siklus hosts privileged opt-in via E2E_ALLOW_HOSTS=1; default hanya path
// refusal (sudo tidak terotorisasi) agar /etc/hosts tidak pernah tersentuh.
// Tanpa jaringan/Caddy, test Caddy skip otomatis — sisanya tetap jalan.
package e2e

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/maleolabs/avior/internal/brand"
	"github.com/maleolabs/avior/internal/env"
	"github.com/maleolabs/avior/internal/executor"
	"github.com/maleolabs/avior/internal/hostname"
	"github.com/maleolabs/avior/internal/mode"
	"github.com/maleolabs/avior/internal/processgroup"
	"github.com/maleolabs/avior/internal/proxy"
	appruntime "github.com/maleolabs/avior/internal/runtime"
	"github.com/maleolabs/avior/internal/setup"
	"github.com/maleolabs/avior/internal/state"
)

// caddyBin menunjuk binary Caddy asli hasil unduhan TestMain; kosong berarti
// unduhan gagal (offline) dan test virtual-domain di-skip, bukan di-fake.
var caddyBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "e2e-caddy-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e: mkdir temp:", err)
		os.Exit(1)
	}
	caddyBin, err = downloadCaddy(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e: caddy tidak tersedia (%v); test virtual-domain akan skip\n", err)
		caddyBin = ""
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// downloadCaddy mengunduh rilis resmi sesuai pin proxy.CaddyVersion dan
// memverifikasi versi binary lewat gate repo (proxy.VerifyPATH).
func downloadCaddy(dir string) (string, error) {
	v := proxy.CaddyVersion
	url := fmt.Sprintf("https://github.com/caddyserver/caddy/releases/download/v%s/caddy_%s_%s_%s.tar.gz", v, v, runtime.GOOS, runtime.GOARCH)
	// Timeout eksplisit: http.Get memakai DefaultClient tanpa deadline —
	// unduhan menggantung bisa menahan seluruh run e2e.
	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Get(url) //nolint:gosec -- URL statis dari pin versi repo
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unduh %s: status %s", url, resp.Status)
	}
	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return "", err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	// Nama entry binary mengikuti GOOS (rilis Windows memuat caddy.exe).
	binName := "caddy"
	if runtime.GOOS == "windows" {
		binName = "caddy.exe"
	}
	bin := filepath.Join(dir, binName)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return "", fmt.Errorf("arsip %s tidak memuat binary caddy", url)
		}
		if err != nil {
			return "", err
		}
		if filepath.Base(hdr.Name) != binName || hdr.Typeflag != tar.TypeReg {
			continue
		}
		f, err := os.OpenFile(bin, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o755)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(f, tr); err != nil { //nolint:gosec -- arsip resmi, decompression bomb tak relevan untuk e2e sekali-jalan
			f.Close()
			return "", err
		}
		if err := f.Close(); err != nil {
			return "", err
		}
		break
	}
	if err := proxy.VerifyPATH(bin); err != nil {
		return "", err
	}
	return bin, nil
}

func needCaddy(t *testing.T) string {
	t.Helper()
	if caddyBin == "" {
		t.Skip("caddy tidak tersedia (unduhan gagal / offline)")
	}
	return caddyBin
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// phpFixture menulis stub artisan PHP (proses php nyata, tanpa Laravel).
func phpFixture(t *testing.T, root string) {
	t.Helper()
	artisan := "#!/usr/bin/env php\n<?php\n$cmd = $argv[1] ?? '';\nfwrite(STDOUT, \"artisan {$cmd} ok\\n\");\nexit(0);\n"
	if err := os.WriteFile(filepath.Join(root, "artisan"), []byte(artisan), 0o755); err != nil {
		t.Fatal(err)
	}
}

// --- Siklus 1: doctor + setup development (step pipeline penuh, proses nyata) ---

func TestSetupDevelopmentFullPipeline(t *testing.T) {
	root := t.TempDir()
	phpFixture(t, root)
	files := map[string]string{
		".env.example":  "APP_NAME=\"E2E App\"\nAPP_ENV=local\nAPP_KEY=\nDB_CONNECTION=sqlite\nINITIAL_SUPERADMIN_PASSWORD=e2e-smoke-password-123\n",
		"composer.json": "{\n  \"name\": \"e2e/fixture\",\n  \"description\": \"fixture e2e\",\n  \"require\": {}\n}\n",
		"package.json":  "{\n  \"name\": \"e2e-smoke\",\n  \"private\": true,\n  \"scripts\": {\n    \"build\": \"node -e \\\"require('fs').mkdirSync('public/build',{recursive:true}); require('fs').writeFileSync('public/build/manifest.json','{}')\\\"\"\n  }\n}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var out bytes.Buffer
	err := setup.Run(setup.Options{
		Root:    root,
		Mode:    mode.ModeDevelopment,
		Yes:     true,
		In:      strings.NewReader(""),
		Out:     &out,
		ReadEnv: env.ReadFile,
		Runner:  executor.Runner{Root: root, Out: &out, ErrOut: &out},
	})
	if err != nil {
		t.Fatalf("setup development gagal: %v\noutput:\n%s", err, out.String())
	}

	// Bukti tiap tahap benar-benar dieksekusi.
	for _, marker := range []string{"doctor", "SETUP SELESAI"} {
		if !strings.Contains(out.String(), marker) {
			t.Fatalf("output setup tidak memuat %q:\n%s", marker, out.String())
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".env")); err != nil {
		t.Fatalf(".env tidak dibuat: %v", err)
	}
	values, err := env.ReadFile(filepath.Join(root, ".env")) //nolint:staticcheck -- akses internal package test diizinkan
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(values["APP_KEY"]) == "" {
		t.Fatal("APP_KEY tidak terisi oleh step app:key")
	}
	for _, proof := range []string{"vendor/autoload.php", "public/build/manifest.json"} {
		if _, err := os.Stat(filepath.Join(root, proof)); err != nil {
			t.Fatalf("bukti %s tidak ada: %v", proof, err)
		}
	}
}

// --- Siklus 2: serve (orchestrator) → HTTP → stop (context cancel) ---

func TestServeStopCycle(t *testing.T) {
	root := t.TempDir()
	docroot := filepath.Join(root, "public")
	if err := os.MkdirAll(docroot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docroot, "index.php"), []byte("<?php echo \"e2e-upstream\";"), 0o644); err != nil {
		t.Fatal(err)
	}
	port := freePort(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- appruntime.Run(ctx, root, []appruntime.Service{
			{Name: "php", Args: []string{"-S", net.JoinHostPort("127.0.0.1", fmt.Sprint(port)), "-t", docroot}},
		})
	}()

	if err := proxy.WaitReady("127.0.0.1", port, 20*time.Second); err != nil {
		cancel()
		t.Fatalf("upstream php -S tidak siap: %v", err)
	}
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port)) //nolint:gosec,noctx -- e2e lokal
	if err != nil {
		cancel()
		t.Fatalf("HTTP ke upstream gagal: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "e2e-upstream" {
		cancel()
		t.Fatalf("respons upstream tidak sesuai: status=%d body=%q", resp.StatusCode, body)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runtime.Run setelah cancel harus nil, dapat: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("teardown runtime tidak selesai dalam 30s")
	}
	// Stop terverifikasi: port tidak lagi menerima koneksi.
	if err := proxy.WaitReady("127.0.0.1", port, 500*time.Millisecond); err == nil {
		t.Fatal("port masih terbuka setelah stop; proses tidak mati")
	}
}

// --- Siklus 3: virtual domain — Caddy asli (HTTP + HTTPS internal CA) ---

func TestCaddyVirtualDomain(t *testing.T) {
	bin := needCaddy(t)
	root := t.TempDir()

	// Layout rilis yang di-resolve proxy.CaddyPath: tools/<brand>/bin/.
	binDir := filepath.Join(root, "tools", brand.Name, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(binDir, "caddy_"+runtime.GOOS+"_"+runtime.GOARCH)
	b, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, b, 0o755); err != nil {
		t.Fatal(err)
	}
	resolved, err := proxy.CaddyPath(root)
	if err != nil || resolved != staged {
		t.Fatalf("CaddyPath salah resolve: got %q err %v, want %q", resolved, err, staged)
	}

	// Upstream nyata di-supervisi processgroup (identitas tercatat).
	docroot := filepath.Join(root, "public")
	if err := os.MkdirAll(docroot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docroot, "index.php"), []byte("<?php echo \"e2e-smoke-caddy\";"), 0o644); err != nil {
		t.Fatal(err)
	}
	upPort := freePort(t)
	upstream, err := processgroup.StartCmd(exec.Command("php", "-S", net.JoinHostPort("127.0.0.1", fmt.Sprint(upPort)), "-t", docroot))
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Terminate(5*time.Second, 2*time.Second)
	if err := proxy.WaitReady("127.0.0.1", upPort, 20*time.Second); err != nil {
		t.Fatalf("upstream tidak siap: %v", err)
	}

	domain := "e2e-smoke.localhost"
	caddyPort := freePort(t)
	storage := filepath.Join(root, "caddy-storage")

	startCaddy := func(t *testing.T, config string) *processgroup.Supervised {
		t.Helper()
		if err := proxy.ValidateConfig(staged, config); err != nil {
			t.Fatalf("validasi config Caddy gagal: %v", err)
		}
		var logs bytes.Buffer
		cmd := exec.Command(staged, "run", "--config", config, "--adapter", "caddyfile")
		cmd.Stdout, cmd.Stderr = &logs, &logs
		sup, err := processgroup.StartCmd(cmd)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { sup.Terminate(5*time.Second, 2*time.Second) })
		t.Cleanup(func() {
			if t.Failed() {
				t.Logf("log Caddy:\n%s", logs.String())
			}
		})
		return sup
	}

	t.Run("http", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "Caddyfile")
		cfg := proxy.CaddyConfig(domain, fmt.Sprintf("127.0.0.1:%d", upPort), caddyPort, false, storage)
		if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
			t.Fatal(err)
		}
		startCaddy(t, cfgPath)
		if err := proxy.WaitReady("127.0.0.1", caddyPort, 20*time.Second); err != nil {
			t.Fatalf("Caddy tidak siap: %v", err)
		}
		req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/", caddyPort), nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = domain // ekuivalen resolves-via-hosts tanpa menyentuh /etc/hosts
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request virtual domain gagal: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || string(body) != "e2e-smoke-caddy" {
			t.Fatalf("virtual domain salah route: status=%d body=%q", resp.StatusCode, body)
		}
	})

	t.Run("https-internal-ca", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "Caddyfile")
		cfg := proxy.CaddyConfig(domain, fmt.Sprintf("127.0.0.1:%d", upPort), caddyPort, true, storage)
		if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
			t.Fatal(err)
		}
		startCaddy(t, cfgPath)
		// Trust CA sistem butuh sudo — di-skip; handshake diverifikasi
		// langsung terhadap listener (SNI = domain) sesuai WaitTLSReady.
		if err := proxy.WaitTLSReady(domain, caddyPort, 30*time.Second); err != nil {
			t.Fatalf("TLS listener tidak siap: %v", err)
		}
		client := &http.Client{Transport: &http.Transport{
			DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return tls.Dial(network, addr, &tls.Config{ServerName: domain, InsecureSkipVerify: true}) //nolint:gosec -- probe e2e lokal, trust store host sengaja tidak disentuh
			},
		}}
		req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("https://127.0.0.1:%d/", caddyPort), nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = domain
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("HTTPS virtual domain gagal: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || string(body) != "e2e-smoke-caddy" {
			t.Fatalf("HTTPS virtual domain salah route: status=%d body=%q", resp.StatusCode, body)
		}
	})

	t.Run("hosts-entry-refusal-tanpa-privilege", func(t *testing.T) {
		before, err := os.ReadFile(hostsFile())
		if err != nil {
			t.Skipf("baca hosts: %v", err)
		}
		// Opt-in: sudo yang masih terotorisasi (cache) bisa membuat EnsureHost
		// berhasil dan menulis /etc/hosts sungguhan. Default hanya path refusal.
		if exec.Command("sudo", "-n", "true").Run() == nil && os.Getenv("E2E_ALLOW_HOSTS") != "1" {
			t.Skip("sudo terotorisasi; set E2E_ALLOW_HOSTS=1 untuk siklus hosts privileged")
		}
		cleanup, err := hostname.EnsureHost(domain, true)
		if err != nil {
			// Path yang diharapkan di host non-root: preflight sudo menolak.
			t.Logf("EnsureHost ditolak (limitasi e2e tanpa sudo): %v", err)
			after, err2 := os.ReadFile(hostsFile())
			if err2 != nil {
				t.Fatal(err2)
			}
			if sha256.Sum256(before) != sha256.Sum256(after) {
				t.Fatal("/etc/hosts berubah walau EnsureHost gagal")
			}
			return
		}
		// Host ternyata privileged (mis. root): siklus penuh + cleanup.
		defer cleanup()
		t.Log("hosts entry berhasil (privileged); verifikasi cleanup")
		cleanup()
		after, err := os.ReadFile(hostsFile())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(after), domain) {
			t.Fatal("cleanup hosts tidak menghapus entri managed")
		}
	})
}

// hostsFile meniru lokasi hosts per-OS untuk verifikasi hash.
func hostsFile() string {
	if runtime.GOOS == "windows" {
		return `C:\Windows\System32\drivers\etc\hosts`
	}
	return "/etc/hosts"
}

// --- Siklus 4: state + registry stop (identity-checked) ---

func TestStateAndManagedStop(t *testing.T) {
	root := t.TempDir()
	statePath, procPath, err := state.Paths(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		os.Remove(statePath)
		os.Remove(procPath)
	})

	sup, err := processgroup.StartCmd(exec.Command("sleep", "30"))
	if err != nil {
		t.Fatal(err)
	}
	// Jaring pengaman: Terminate idempoten — cleanup normal lewat
	// StopManagedProcess di bawah; ini menutup jalur failure sebelum stop.
	t.Cleanup(func() { sup.Terminate(time.Second, time.Second) })
	id := sup.Identity()
	if id.PID == 0 || id.Command == "" {
		t.Fatalf("identitas proses tidak lengkap: %+v", id)
	}

	if err := state.SaveState(statePath, state.RuntimeState{ProjectRoot: root, Port: 8000, MainPID: id}); err != nil {
		t.Fatal(err)
	}
	st, exists, err := state.LoadState(statePath)
	if err != nil || !exists {
		t.Fatalf("LoadState: exists=%v err=%v", exists, err)
	}
	if st.MainPID.PID != id.PID || st.Port != 8000 {
		t.Fatalf("state round-trip salah: %+v", st)
	}
	// Registry proses di prosesPath adalah milik processgroup (schema
	// processRecord). Round-trip diagnostik SaveProcesses dipisah lalu file
	// dihapus agar dua schema tidak bertabrakan di satu path — konflik wiring
	// ini milik p2 cmd/serve (catatan: state.SaveProcesses dan registry
	// processgroup memakai path yang sama dari state.Paths).
	if err := state.SaveProcesses(procPath, []state.Process{id}); err != nil {
		t.Fatal(err)
	}
	procs, exists, err := state.LoadProcesses(procPath)
	if err != nil || !exists || len(procs) != 1 || procs[0].PID != id.PID {
		t.Fatalf("processes round-trip salah: exists=%v err=%v procs=%+v", exists, err, procs)
	}
	if err := os.Remove(procPath); err != nil {
		t.Fatal(err)
	}

	// Stop path CLI (identity-checked): PID tercatat → sinyal terkirim.
	unregister, err := processgroup.RegisterProcess(procPath, "sleep 30", id.PID)
	if err != nil {
		t.Fatalf("register process: %v", err)
	}
	if !processgroup.StopManagedProcess(procPath, id.PID) {
		unregister()
		t.Fatal("StopManagedProcess menolak proses yang tercatat dan hidup")
	}
	select {
	case <-sup.Reaped():
	case <-time.After(10 * time.Second):
		unregister()
		t.Fatal("proses tidak mati setelah StopManagedProcess")
	}
	unregister()

	// Setelah unregister, stop lagi harus menolak (registry kosong).
	if processgroup.StopManagedProcess(procPath, id.PID) {
		t.Fatal("StopManagedProcess berhasil tanpa record registry")
	}
}
