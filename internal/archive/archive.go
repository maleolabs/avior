// Package archive membuat distribusi (item m2-cmd-archive): manifest
// terkurasi, staging production murni (checkout tidak pernah ditulis),
// build CLI lintas-platform, dan arsip tar.gz/zip.
//
// Port dari codebase pendahulu dengan dua perbaikan sadar:
//   - manifest() murni: pendahulu meng-append path production ke variabel
//     global `roots` (mutasi lintas pemanggilan dalam satu proses). Di sini
//     roots menjadi parameter dan ekstensi production dihitung lokal.
//   - Langkah "Acquire Caddy/NSS" tidak di-port: tool ini tidak punya fetcher
//     resource di repo (proxy.FetchCaddy mengambil binary saat runtime).
//     ponytail: kembalikan langkah acquire saat checksum manifest resource
//     dibundel ke arsip release (container m3).
package archive

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/maleolabs/avior/internal/brand"
)

type Mode string

const (
	MainSource Mode = "main-source"
	Production Mode = "production"
	// Development alias kompatibilitas untuk main-source.
	Development Mode = "development"
)

type Options struct {
	Root, Output string
	Mode         Mode
	// BuildNumber untuk test deterministik; caller production harus membiarkan
	// kosong agar nilai selalu dari Git.
	BuildNumber string
	DryRun      bool
	Progress    func(ProgressEvent)
}

// ProgressEvent sengaja output-agnostic agar caller bisa merendernya sebagai
// spinner, baris log CI, atau event test tanpa membocorkan output child.
type ProgressEvent struct {
	Label  string
	Status string // started, completed, skipped, phase
}

func (o Options) progress(label, status string) {
	if o.Progress != nil {
		o.Progress(ProgressEvent{Label: label, Status: status})
	}
}

var roots = []string{"app", "bootstrap", "config", "database", "lang", "public", "resources", "routes", "scripts", "artisan", "composer.json", "composer.lock", "package.json", "package-lock.json", "vite.config.ts", ".env.example", "README.md", "VERSION", "server.sh", "server.ps1", "server.bat"}

var excluded = map[string]bool{".git": true, ".ai": true, "EKA": true, "vendor": true, "node_modules": true, "dist": true, "storage": true, "tests": true, ".github": true, ".agents": true}

// toolLayout mengembalikan path direktori tool di dalam checkout.
func toolLayout(root string) string { return filepath.Join(root, "tools", brand.Name) }

func Create(o Options) (outputs []string, err error) {
	if o.Mode == Development {
		o.Mode = MainSource
	}
	if o.Mode != MainSource && o.Mode != Production {
		return nil, fmt.Errorf("mode harus main-source atau production")
	}
	if o.Output == "" {
		o.Output = filepath.Join(o.Root, "dist")
	}
	if !o.DryRun {
		if err := os.MkdirAll(o.Output, 0755); err != nil {
			return nil, err
		}
	}
	version, err := releaseVersion(o.Root)
	if err != nil {
		return nil, err
	}
	buildNumber := o.BuildNumber
	if buildNumber == "" {
		buildNumber, err = releaseBuildNumber(o.Root)
		if err != nil {
			return nil, err
		}
	}
	name := brand.ArchivePrefix() + version + "-" + buildNumber
	archiveRoot := o.Root
	var cleanup func() = func() {}
	if o.Mode == Production && !o.DryRun {
		archiveRoot, cleanup, err = prepareProductionStaging(o.Root, o.Progress)
		if err != nil {
			return nil, err
		}
		defer cleanup()
	}
	paths, err := manifest(archiveRoot, o.Mode)
	if err != nil {
		return nil, err
	}
	outputs = []string{filepath.Join(o.Output, name+".tar.gz"), filepath.Join(o.Output, name+".zip")}
	completed := false
	defer func() {
		// Cleanup terikat transaksi, bukan nilai error saja — menutup juga
		// kegagalan yang kembali lewat nilai intermediate dan menjaga arsip
		// tar/zip parsial tetap keluar dari direktori distribusi.
		if !completed {
			for _, path := range outputs {
				_ = os.RemoveAll(path)
			}
		}
	}()
	if o.Mode == Production {
		o.progress("Build CLI binaries", "phase")
		binaryOutputs, err := buildBinaries(archiveRoot, o.DryRun, o.Progress)
		if err != nil {
			return nil, err
		}
		outputs = append(outputs, binaryOutputs...)
		paths = append(paths, binaryPaths()...)
	}
	o.progress("Create distribution archives", "phase")
	if o.DryRun {
		o.progress("Creating tar.gz archive", "skipped")
		o.progress("Creating zip archive", "skipped")
		return outputs, nil
	}
	o.progress("Creating tar.gz archive", "started")
	if e := writeTarGz(outputs[0], name, archiveRoot, paths); e != nil {
		_ = os.RemoveAll(outputs[0])
		return nil, e
	}
	o.progress("Creating tar.gz archive", "completed")
	o.progress("Creating zip archive", "started")
	if e := writeZip(outputs[1], name, archiveRoot, paths); e != nil {
		_ = os.RemoveAll(outputs[0])
		_ = os.RemoveAll(outputs[1])
		return nil, e
	}
	o.progress("Creating zip archive", "completed")
	completed = true
	return outputs, nil
}

// prepareProductionStaging sengaja tidak pernah menulis dependency atau hasil
// build ke checkout. Tree sementara dihapus caller di setiap exit path.
func prepareProductionStaging(root string, progress func(ProgressEvent)) (string, func(), error) {
	// Jangan default ke OS temp: di Linux umumnya tmpfs kecil, sedangkan
	// checkout Laravel plus vendor bisa melebihinya. Staging di samping
	// checkout agar memakai disk yang sama dengan source.
	stage, err := os.MkdirTemp(filepath.Dir(root), "."+brand.Name+"-production-")
	if err != nil {
		return "", func() {}, fmt.Errorf("membuat staging production: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(stage) }
	for _, p := range roots {
		if err := copyTree(filepath.Join(root, p), filepath.Join(stage, p)); err != nil && !os.IsNotExist(err) {
			cleanup()
			return "", func() {}, fmt.Errorf("menyalin %s ke staging: %w", p, err)
		}
	}
	// Modul CLI dibutuhkan untuk kompilasi binary release, tetapi bukan
	// bagian dari payload aplikasi production.
	if err := copyTree(toolLayout(root), toolLayout(stage)); err != nil && !os.IsNotExist(err) {
		cleanup()
		return "", func() {}, fmt.Errorf("menyalin CLI ke staging: %w", err)
	}
	// Hook post-autoload-dump Laravel di Composer mem-boot aplikasi. Tree
	// staging rilis yang bersih tidak berisi direktori runtime yang diabaikan,
	// jadi buat semua path writable yang mungkin disentuh Laravel sebelum
	// composer berjalan.
	if err := ensureRuntimeDirectories(stage); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("menyiapkan direktori runtime Laravel: %w", err)
	}
	// Artisan command yang dipanggil plugin Vite butuh file environment nyata.
	// Tree staging terkurasi hanya berisi .env.example, jadi buat counterpart
	// lokal non-secret sebelum hook bootstrap Composer dan build npm berjalan.
	// Jangan generate key di sini agar route generation independen dari
	// secret deployment.
	if err := copyTree(filepath.Join(stage, ".env.example"), filepath.Join(stage, ".env")); err != nil && !os.IsNotExist(err) {
		cleanup()
		return "", func() {}, fmt.Errorf("menyiapkan environment staging: %w", err)
	}
	if progress != nil {
		progress(ProgressEvent{Label: "Install production dependencies", Status: "started"})
	}
	if err := runIn(stage, "composer", "install", "--no-dev", "--no-interaction", "--prefer-dist", "--optimize-autoloader"); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("composer install production gagal: %w", err)
	}
	// Vite adalah build-time dependency. Menginstal dependency
	// production-only di sini membuat `npm run build` gagal karena paket
	// build tidak ada. Build dengan lockfile lengkap dulu; node_modules
	// hanya untuk staging dan dihapus segera setelah build.
	npmArgs := []string{"install"}
	if _, err := os.Stat(filepath.Join(stage, "package-lock.json")); err == nil {
		npmArgs = []string{"ci"}
	}
	if err := runIn(stage, "npm", npmArgs...); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("npm install build dependencies gagal: %w", err)
	}
	if err := runIn(stage, "npm", "run", "build"); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("npm run build gagal: %w", err)
	}
	// Jangan bawa dependency tree ke tree staging production. Selain menjaga
	// artifact tetap kecil, ini mencegah paket build-only disalahartikan
	// sebagai dependency runtime.
	if err := os.RemoveAll(filepath.Join(stage, "node_modules")); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("menghapus node_modules staging gagal: %w", err)
	}
	if progress != nil {
		progress(ProgressEvent{Label: "Install production dependencies", Status: "completed"})
	}
	return stage, cleanup, nil
}

func ensureRuntimeDirectories(root string) error {
	for _, path := range []string{
		filepath.Join(root, "bootstrap", "cache"),
		filepath.Join(root, "storage", "app"),
		filepath.Join(root, "storage", "framework", "cache", "data"),
		filepath.Join(root, "storage", "framework", "sessions"),
		filepath.Join(root, "storage", "framework", "views"),
		filepath.Join(root, "storage", "logs"),
	} {
		if err := os.MkdirAll(path, 0755); err != nil {
			return err
		}
	}
	return nil
}

func runIn(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...) //nolint:gosec -- name/args konstan internal, bukan input user
	cmd.Dir = dir
	// Composer (dan npm) menulis diagnostik yang berguna ke salah satu stream
	// tergantung versi/konfigurasi. Keduanya disimpan privat dan dipertahankan
	// untuk error yang dikembalikan ke caller.
	var output lockedBuffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(output.String())
		if len(detail) > 4096 {
			// Composer mencetak exception berguna setelah baris progres paket.
			// Pertahankan awal dan ekor agar penyebab sebenarnya tidak hilang.
			const marker = "\n… [diagnostic truncated] …\n"
			keep := (4096 - len(marker)) / 2
			detail = detail[:keep] + marker + detail[len(detail)-keep:]
		}
		if detail != "" {
			return fmt.Errorf("%w (command=%s, cwd=%s, env=%s): %s", err, commandLine(name, args...), dir, diagnosticEnv(), detail)
		}
		return fmt.Errorf("%w (command=%s, cwd=%s, env=%s)", err, commandLine(name, args...), dir, diagnosticEnv())
	}
	return nil
}

func commandLine(name string, args ...string) string {
	return strings.Join(append([]string{name}, args...), " ")
}

// diagnosticEnv menjaga konteks environment tetap berguna tanpa pernah
// meng-echo credential atau environment penuh ke error arsip.
func diagnosticEnv() string {
	values := make([]string, 0, 5)
	for _, key := range []string{"APP_ENV", "APP_KEY", "DB_CONNECTION", "SESSION_DRIVER", "NODE_ENV"} {
		value, ok := os.LookupEnv(key)
		if key == "APP_KEY" {
			if ok && value != "" {
				value = "<set>"
			} else {
				value = "<empty>"
			}
		}
		if !ok {
			value = "<unset>"
		}
		values = append(values, key+"="+value)
	}
	return strings.Join(values, ",")
}

// lockedBuffer: exec.Cmd bisa menyalin stdout dan stderr secara konkuren;
// bytes.Buffer tidak aman untuk itu, jadi capture diagnostik gabungan
// dilindungi mutex.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func copyTree(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
			return err
		}
		return filepath.Walk(src, func(path string, i os.FileInfo, e error) error {
			if e != nil {
				return e
			}
			rel, _ := filepath.Rel(src, path)
			if rel == "." {
				return nil
			}
			return copyTree(path, filepath.Join(dst, rel))
		})
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func releaseVersion(root string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, "VERSION"))
	if err != nil {
		return "", fmt.Errorf("membaca VERSION gagal: %w", err)
	}
	version := strings.TrimSpace(string(b))
	if version == "" {
		return "", fmt.Errorf("VERSION kosong: isi versi rilis di %s/VERSION", root)
	}
	return version, nil
}

func releaseBuildNumber(root string) (string, error) {
	cmd := exec.Command("git", "-C", root, "rev-parse", "--short", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("build number tidak tersedia: git commit tidak dapat dibaca di %s; jalankan dari checkout Git dengan commit aktif: %w", root, err)
	}
	sha := strings.TrimSpace(string(out))
	if sha == "" {
		return "", fmt.Errorf("build number tidak tersedia: git menghasilkan commit SHA kosong di %s", root)
	}
	return sha, nil
}

// manifest murni: tidak memutasi state global (perbaikan dari pendahulu,
// yang meng-append path production ke variabel global roots).
func manifest(root string, m Mode) ([]string, error) {
	if m == Development {
		m = MainSource
	}
	manifestRoots := roots
	if m == Production {
		manifestRoots = append(append([]string(nil), roots...),
			filepath.Join("tools", brand.Name, "bin"), filepath.Join("tools", brand.Name, "nss"))
	}
	var out []string
	for _, p := range manifestRoots {
		if m == Production && (p == "package.json" || p == "package-lock.json" || p == "scripts" || p == "vite.config.ts") {
			continue
		}
		full := filepath.Join(root, p)
		if _, err := os.Stat(full); err != nil {
			continue
		}
		err := filepath.Walk(full, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			if rel == "." {
				return nil
			}
			parts := strings.Split(filepath.ToSlash(rel), "/")
			if m == Production && strings.HasPrefix(filepath.ToSlash(rel), "public/build/") {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			// Main source menyimpan file source frontend. Production mengirim
			// aset terkompilasi, jadi tree source frontend diabaikan di sana.
			if (m == Production && (strings.HasPrefix(filepath.ToSlash(rel), "resources/js/") || strings.HasPrefix(filepath.ToSlash(rel), "resources/css/"))) || (m == MainSource && strings.HasPrefix(filepath.ToSlash(rel), "public/build/")) {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			for _, part := range parts {
				if excluded[part] {
					if info.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
			}
			if !info.IsDir() {
				out = append(out, rel)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	if m == MainSource {
		tool := toolLayout(root)
		if _, err := os.Stat(tool); err == nil {
			if err := filepath.Walk(tool, func(path string, info os.FileInfo, err error) error {
				if err == nil && !info.IsDir() {
					rel, _ := filepath.Rel(root, path)
					if !strings.HasSuffix(rel, ".test") {
						out = append(out, rel)
					}
				}
				return err
			}); err != nil {
				return nil, err
			}
		}
	} else {
		// Production mengirim dependency runtime dan CLI terkompilasi,
		// bukan source-nya.
		for _, p := range []string{"vendor", filepath.Join("public", "build")} {
			full := filepath.Join(root, p)
			if _, err := os.Stat(full); err != nil {
				continue
			}
			err := filepath.Walk(full, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				if !info.IsDir() {
					rel, e := filepath.Rel(root, path)
					if e != nil {
						return e
					}
					out = append(out, rel)
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
		}
	}
	seen := make(map[string]struct{}, len(out))
	unique := out[:0]
	for _, p := range out {
		p = filepath.Clean(p)
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		unique = append(unique, p)
	}
	sort.Strings(unique)
	return unique, nil
}

func binaryPaths() []string {
	paths := []string{}
	for _, target := range []struct{ os, arch, suffix string }{
		{"linux", "amd64", ""}, {"linux", "arm64", ""},
		{"darwin", "amd64", ""}, {"darwin", "arm64", ""},
		{"windows", "amd64", ".exe"}, {"windows", "arm64", ".exe"},
	} {
		paths = append(paths, filepath.Join("tools", brand.Name, "bin", brand.Name+"_"+target.os+"_"+target.arch+target.suffix))
	}
	return paths
}

func buildBinaries(root string, dry bool, progress func(ProgressEvent)) ([]string, error) {
	specs := []struct{ goos, arch, suffix string }{{"linux", "amd64", ""}, {"linux", "arm64", ""}, {"darwin", "amd64", ""}, {"darwin", "arm64", ""}, {"windows", "amd64", ".exe"}, {"windows", "arm64", ".exe"}}
	if dry {
		for _, s := range specs {
			if progress != nil {
				progress(ProgressEvent{Label: fmt.Sprintf("Building CLI binary · %s %s", s.goos, s.arch), Status: "skipped"})
			}
		}
		return []string{}, nil
	}
	bin := filepath.Join(root, "tools", brand.Name, "bin")
	module := filepath.Join(root, "tools", brand.Name)
	// Command umumnya dijalankan dari root aplikasi, tetapi developer juga
	// bisa menjalankan CLI checkout dari tools/<brand> sendiri.
	if _, err := os.Stat(filepath.Join(module, "go.mod")); err != nil {
		if _, moduleErr := os.Stat(filepath.Join(root, "go.mod")); moduleErr == nil {
			module = root
		} else {
			return nil, fmt.Errorf("CLI module tidak ditemukan di %s: %w", module, err)
		}
	}
	if err := os.MkdirAll(bin, 0755); err != nil {
		return nil, fmt.Errorf("membuat direktori binary %s: %w", bin, err)
	}
	for _, s := range specs {
		label := fmt.Sprintf("Building CLI binary · %s %s", s.goos, s.arch)
		if progress != nil {
			progress(ProgressEvent{Label: label, Status: "started"})
		}
		path := filepath.Join(bin, brand.Name+"_"+s.goos+"_"+s.arch+s.suffix)
		c := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-ldflags", "-s -w", "-o", path, ".") //nolint:gosec -- argumen konstan
		c.Dir = module
		c.Env = append(os.Environ(), "GOOS="+s.goos, "GOARCH="+s.arch, "CGO_ENABLED=0")
		var stderr bytes.Buffer
		c.Stderr = &stderr
		if err := c.Run(); err != nil {
			detail := strings.TrimSpace(stderr.String())
			if detail == "" {
				return nil, fmt.Errorf("build CLI %s/%s gagal: %w", s.goos, s.arch, err)
			}
			// Hanya expose diagnostik compiler, bukan environment penuh.
			if len(detail) > 8192 {
				detail = detail[:8192] + "…"
			}
			return nil, fmt.Errorf("build CLI %s/%s gagal: %s (%w)", s.goos, s.arch, detail, err)
		}
		if progress != nil {
			progress(ProgressEvent{Label: label, Status: "completed"})
		}
	}
	return binaryPaths(), nil
}

func writeTarGz(file, name, root string, paths []string) (err error) {
	// Arsip adalah output, bukan work file: jangan pernah meninggalkan arsip
	// terpotong saat walk manifest atau close writer gagal.
	defer func() {
		if err != nil {
			_ = os.Remove(file)
		}
	}()
	f, e := os.Create(file)
	if e != nil {
		return e
	}
	defer f.Close()
	gz, e := gzip.NewWriterLevel(f, gzip.BestCompression)
	if e != nil {
		return e
	}
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()
	for _, p := range paths {
		if e := addTar(tw, name, root, p); e != nil {
			return e
		}
	}
	return nil
}

func addTar(tw *tar.Writer, name, root, p string) error {
	f, e := os.Open(filepath.Join(root, p))
	if e != nil {
		return e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return e
	}
	h, e := tar.FileInfoHeader(st, "")
	if e != nil {
		return e
	}
	h.Name = filepath.ToSlash(filepath.Join(name, p))
	if e = tw.WriteHeader(h); e != nil {
		return e
	}
	_, e = io.Copy(tw, f)
	return e
}

func writeZip(file, name, root string, paths []string) (err error) {
	defer func() {
		if err != nil {
			_ = os.Remove(file)
		}
	}()
	zf, e := os.Create(file)
	if e != nil {
		return e
	}
	defer zf.Close()
	zw := zip.NewWriter(zf)
	defer zw.Close()
	for _, p := range paths {
		st, e := os.Stat(filepath.Join(root, p))
		if e != nil {
			return e
		}
		h, e := zip.FileInfoHeader(st)
		if e != nil {
			return e
		}
		h.Name = filepath.ToSlash(filepath.Join(name, p))
		h.SetModTime(time.Time{})
		w, e := zw.CreateHeader(h)
		if e != nil {
			return e
		}
		f, e := os.Open(filepath.Join(root, p))
		if e != nil {
			return e
		}
		_, e = io.Copy(w, f)
		f.Close()
		if e != nil {
			return e
		}
	}
	return nil
}
