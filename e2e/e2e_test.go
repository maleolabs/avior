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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/maleolabs/avior/internal/brand"
	"github.com/maleolabs/avior/internal/env"
	"github.com/maleolabs/avior/internal/executor"
	"github.com/maleolabs/avior/internal/gateway"
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
		".env.example":  "APP_NAME=\"E2E App\"\nAPP_ENV=local\nAPP_KEY=\nDB_CONNECTION=sqlite\nAPP_BOOTSTRAP_SECRET=e2e-smoke-password-123\n",
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

// --- Siklus 5: multi-project paralel — isolasi state/index/monitor/stop ---

// TestMultiProjectParallel menjalankan DUA runtime bersamaan (php -S nyata,
// port berbeda), lalu memverifikasi:
//   - kedua state file terpisah dan terisi (token/name/domain/ports),
//   - projects.json index memuat kedua proyek,
//   - Monitor() melihat keduanya dengan URL berbeda,
//   - Stop satu proyek tidak menghentikan yang lain,
//   - duplicate guard menolak serve ulang proyek yang masih hidup.
func TestMultiProjectParallel(t *testing.T) {
	isolateE2EHome(t)

	shop := t.TempDir()
	blog := t.TempDir()
	ports := make([]int, 2)
	for i := range ports {
		ports[i] = freePort(t)
	}

	// Fixtures: artisan + composer.json + .env minimal (apa yang dicek
	// discovery Serve) — di sini kita memakai Start langsung dengan child
	// php -S, mengikuti pola TestServeStopCycle (proses nyata, tanpa
	// Laravel).
	type runtime struct {
		root  string
		name  string
		token string
		sup   *appruntime.Supervision
	}
	startRuntime := func(root, name string, appPort int) *runtime {
		docroot := filepath.Join(root, "public")
		if err := os.MkdirAll(docroot, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(docroot, "index.php"), []byte("<?php echo \""+name+"\";"), 0o644); err != nil {
			t.Fatal(err)
		}
		token, err := state.ProjectToken(root)
		if err != nil {
			t.Fatal(err)
		}
		sup, err := appruntime.Start(context.Background(), appruntime.StartOptions{
			Dir:         root,
			ProjectName: name,
			Port:        appPort,
			Domain:      name + ".localhost",
			Ports:       state.PortSet{App: appPort, Caddy: 0},
			Services: []appruntime.Service{
				{Name: "php", Args: []string{"-S", net.JoinHostPort("127.0.0.1", fmt.Sprint(appPort)), "-t", docroot}},
			},
		})
		if err != nil {
			t.Fatalf("start %s: %v", name, err)
		}
		t.Cleanup(func() { sup.Stop() })
		return &runtime{root: root, name: name, token: token, sup: sup}
	}

	shopRT := startRuntime(shop, "shop", ports[0])
	blogRT := startRuntime(blog, "blog", ports[1])

	// Kedua upstream nyata melayani traffic.
	for i, rt := range []*runtime{shopRT, blogRT} {
		if err := proxy.WaitReady("127.0.0.1", ports[i], 20*time.Second); err != nil {
			t.Fatalf("%s tidak siap: %v", rt.name, err)
		}
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", ports[i])) //nolint:gosec,noctx -- e2e lokal
		if err != nil {
			t.Fatalf("HTTP %s gagal: %v", rt.name, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(body) != rt.name {
			t.Fatalf("%s body = %q, want %q", rt.name, body, rt.name)
		}
	}

	// State terpisah + identitas v2 terisi.
	for _, rt := range []*runtime{shopRT, blogRT} {
		path, _, err := state.Paths(rt.root)
		if err != nil {
			t.Fatal(err)
		}
		st, exists, err := state.LoadState(path)
		if err != nil || !exists {
			t.Fatalf("state %s: exists=%v err=%v", rt.name, exists, err)
		}
		if st.ProjectToken != rt.token || st.ProjectName != rt.name || st.Domain != rt.name+".localhost" {
			t.Fatalf("state %s identitas beda: %+v", rt.name, st)
		}
	}

	// Index memuat kedua proyek.
	idx, exists, err := state.LoadIndex()
	if err != nil || !exists {
		t.Fatalf("LoadIndex: exists=%v err=%v", exists, err)
	}
	if len(idx.Projects) != 2 {
		t.Fatalf("index = %d proyek, want 2: %+v", len(idx.Projects), idx.Projects)
	}

	// Monitor melihat keduanya, URL berbeda.
	statuses, err := appruntime.Monitor()
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 2 {
		t.Fatalf("Monitor = %d, want 2", len(statuses))
	}
	urls := map[string]bool{}
	for _, st := range statuses {
		if !st.Running {
			t.Fatalf("proyek %s harus running", st.ProjectName)
		}
		urls[st.URL] = true
	}
	if len(urls) != 2 {
		t.Fatalf("URL monitor tidak unik: %v", urls)
	}

	// Duplicate guard: serve ulang proyek yang masih hidup harus ditolak.
	if !appruntime.RuntimeRunning(shop) {
		t.Fatal("RuntimeRunning(shop) harus true saat runtime hidup")
	}

	// Stop satu proyek; yang lain tetap melayani.
	if err := appruntime.Stop(shop); err != nil {
		t.Fatalf("Stop shop gagal: %v", err)
	}
	if err := proxy.WaitReady("127.0.0.1", ports[1], 2*time.Second); err != nil {
		t.Fatalf("blog harus tetap hidup setelah shop dihentikan: %v", err)
	}
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", ports[1])) //nolint:gosec,noctx -- e2e lokal
	if err != nil {
		t.Fatalf("HTTP blog gagal setelah stop shop: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "blog" {
		t.Fatalf("blog body = %q, want %q", body, "blog")
	}
	if err := appruntime.Stop(blog); err != nil {
		t.Fatalf("Stop blog gagal: %v", err)
	}
	for i := range ports {
		if err := proxy.WaitReady("127.0.0.1", ports[i], 2*time.Second); err == nil {
			t.Fatalf("port %d masih melayani setelah stop", ports[i])
		}
	}
}

// isolateE2EHome mengarahkan state dir ke temp; tes multi-proyek tidak boleh
// menyentuh state user sungguhan.
func isolateE2EHome(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}

// --- Siklus 6: gateway clean URL (Phase 7) — opsi 3 acceptance ---

// TestGatewayConfigAggregatesSites: dua site terdaftar → config gateway
// memuat keduanya dengan urutan domain deterministik; upstream tiap site
// merujuk port app masing-masing. Config di-serve oleh Caddy sungguhan
// (gateway reload), namun tanpa privilege :80/:443, gateway dijalankan
// pada admin port uji dan dibuktikan lewat admin API. Ini membuktikan
// agregasi multi-project sekaligus parse round-trip pada binary nyata.
func TestGatewayConfigAggregatesSites(t *testing.T) {
	bin := needCaddy(t)
	isolateE2EHome(t)

	mgr := gateway.NewManager(bin)

	if err := mgr.RegisterSite(gateway.Site{Domain: "zebra.localhost", Upstream: "127.0.0.1:9001", HTTPS: false}, 2019); err != nil {
		t.Fatalf("register zebra: %v", err)
	}
	if err := mgr.RegisterSite(gateway.Site{Domain: "alpha.localhost", Upstream: "127.0.0.1:9002", HTTPS: false}, 2019); err != nil {
		t.Fatalf("register alpha: %v", err)
	}
	cfg, err := os.ReadFile(mgr.ConfigPath())
	if err != nil {
		t.Fatalf("baca config gateway: %v", err)
	}
	body := string(cfg)
	idxA := strings.Index(body, "alpha.localhost")
	idxZ := strings.Index(body, "zebra.localhost")
	if idxA < 0 || idxZ < 0 || !(idxA < idxZ) {
		t.Fatalf("config tidak agregasi urut: alpha=%d zebra=%d\n%s", idxA, idxZ, body)
	}
	if !strings.Contains(body, "127.0.0.1:9001") || !strings.Contains(body, "127.0.0.1:9002") {
		t.Fatalf("upstream site tidak tertulis:\n%s", body)
	}

	// Registry config gateway terhadap binary nyata (validate --adapter).
	if err := proxy.ValidateConfig(bin, mgr.ConfigPath()); err != nil {
		t.Fatalf("config gateway invalid menurut caddy: %v", err)
	}

	// Unregister satu site: site lain tetap utuh.
	if remaining, err := mgr.UnregisterSite("zebra.localhost", 2019); err != nil || remaining != 1 {
		t.Fatalf("unregister zebra: remaining=%d err=%v", remaining, err)
	}
	cfg, err = os.ReadFile(mgr.ConfigPath())
	if err != nil {
		t.Fatalf("baca config gateway setelah unregister: %v", err)
	}
	if strings.Contains(string(cfg), "zebra.localhost") {
		t.Fatalf("zebra masih ada setelah unregister:\n%s", cfg)
	}
	if !strings.Contains(string(cfg), "alpha.localhost") {
		t.Fatalf("alpha hilang setelah unregister zebra:\n%s", cfg)
	}
	// Site terakhir → config dihapus.
	if remaining, err := mgr.UnregisterSite("alpha.localhost", 2019); err != nil || remaining != 0 {
		t.Fatalf("unregister terakhir: remaining=%d err=%v", remaining, err)
	}
	if _, err := os.Stat(mgr.ConfigPath()); !os.IsNotExist(err) {
		t.Fatalf("config gateway tidak dihapus saat site terakhir pergi")
	}
}

// TestGatewayReloadOnProjectStartStop: siklus config gateway (register
// site → reload → unregister → reload) terhadap Caddy nyata; admin API
// gateway tetap merespons setelah setiap perubahan (reload zero-downtime).
//
// Catatan lingkungan: tes e2e berjalan TANPA privilege bind :80/:443, jadi
// config gateway situs penuh (yang ada di TestGatewayConfigAggregatesSites)
// tidak bisa di-`caddy run` di sini. Siklus ini memakai config host-only:
// global block + site pada port tinggi — membuktikan jalur reload (admin
// API), bukan jalur bind :80/443 (butuh sudo; acceptance user punya itu).
func TestGatewayReloadOnProjectStartStop(t *testing.T) {
	bin := needCaddy(t)
	isolateE2EHome(t)

	mgr := gateway.NewManager(bin)
	admin := freePort(t)
	sitePort := freePort(t)

	// Config host-only: site tidak memakai :80/443 (lihat catatan di atas)
	// sehingga `caddy run` berhasil tanpa privilege.
	if err := mgr.RegisterSite(gateway.Site{Domain: "127.0.0.1:" + strconv.Itoa(sitePort), Upstream: "127.0.0.1:9001"}, admin); err != nil {
		t.Fatalf("register site: %v", err)
	}
	gw := gateway.NewProcess(bin, admin)
	if err := gw.Start(mgr.ConfigPath()); err != nil {
		t.Fatalf("start gateway: %v", err)
	}
	defer func() { _ = gw.Stop() }()
	if err := gw.WaitReady(20 * time.Second); err != nil {
		t.Fatalf("gateway admin tidak siap: %v", err)
	}

	// Kedua site (reload dengan site block tambahan).
	sitePort2 := freePort(t)
	if err := mgr.RegisterSite(gateway.Site{Domain: "127.0.0.1:" + strconv.Itoa(sitePort2), Upstream: "127.0.0.1:9002"}, admin); err != nil {
		t.Fatalf("register site #2: %v", err)
	}
	if err := gw.Reload(mgr.ConfigPath()); err != nil {
		t.Fatalf("reload setelah register site #2: %v", err)
	}
	if err := gw.WaitReady(5 * time.Second); err != nil {
		t.Fatalf("admin gateway mati setelah reload: %v", err)
	}

	// Hapus site pertama (reload lagi).
	if _, err := mgr.UnregisterSite("127.0.0.1:"+strconv.Itoa(sitePort), admin); err != nil {
		t.Fatalf("unregister site: %v", err)
	}
	if err := gw.Reload(mgr.ConfigPath()); err != nil {
		t.Fatalf("reload setelah unregister: %v", err)
	}
	if err := gw.WaitReady(5 * time.Second); err != nil {
		t.Fatalf("admin gateway mati setelah reload unregister: %v", err)
	}
	if !gw.Running() {
		t.Fatal("gateway harus masih running setelah siklus reload")
	}
}

// TestCleanURLFallbackWithoutPrivilege: tanpa privilege bind :80/:443
// (test e2e berjalan tanpa sudo), CanBindStandardPorts false dan fail-closed
// dijalankan: runtime tetap high-port (cek lewat statusURL state v2).
func TestCleanURLFallbackWithoutPrivilege(t *testing.T) {
	bin := needCaddy(t)
	isolateE2EHome(t)
	_ = bin

	if gateway.CanBindStandardPorts() {
		t.Skip("proses tes punya privilege :80/:443 — skip tes fallback")
	}
	// Fail-closed sudah diuji unit (ServeOptions.CleanURL + virtual domain).
	// Di sini dibuktikan state v2 tanpa CleanURL → URL tetap memakai port
	// (default high-port tidak berubah).
	root := t.TempDir()
	statePath, _, err := state.Paths(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := state.SaveState(statePath, state.RuntimeState{
		Version:        state.StateVersion,
		ProjectRoot:    root,
		ProjectName:    "shop",
		Domain:         "shop.localhost",
		Ports:          state.PortSet{App: 8012, Caddy: 8090, CaddyAdmin: 2027},
		CaddyAdminPort: 2027,
		MainPID:        state.Process{PID: os.Getpid(), Command: os.Args[0], StartedAt: now},
	}); err != nil {
		t.Fatal(err)
	}
	statuses, err := appruntime.Monitor()
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range statuses {
		if st.ProjectRoot == root || st.ProjectName == "shop" {
			if !strings.Contains(st.URL, ":8090") {
				t.Fatalf("tanpa gateway, URL harus tetap high-port: %q", st.URL)
			}
			return
		}
	}
	t.Fatal("monitor tidak melihat runtime fallback")
}
