// Package setup menjalankan alur setup proyek Laravel sebagai step-pipeline
// (arc:avior-architecture): daftar Step terurut yang dikomposisi per mode —
// BUKAN dua blok if development/production seperti codebase pendahulu. Semua output
// lewat io.Writer yang di-inject (Options.Out); .env dibaca TEPAT SATU kali
// per run (single-pass) dan nilai aktual di-thread antar step lewat run.values.
package setup

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/maleolabs/avior/internal/brand"
	"github.com/maleolabs/avior/internal/detect"
	"github.com/maleolabs/avior/internal/env"
	"github.com/maleolabs/avior/internal/mode"
)

// Runner menjalankan subprocess argumen-list. Sengaja kecil agar sequencing
// setup bisa dites tanpa spawn proses sungguhan.
type Runner interface {
	Run(name string, args ...string) error
}

// EnvReader membaca file .env menjadi map KEY=VALUE. Default env.ReadFile;
// di-inject agar test bisa menghitung jumlah pembacaan (single-pass guarantee).
type EnvReader func(path string) (map[string]string, error)

// Options adalah seluruh input Run. Out dan In wajib diisi pemanggil (cmd/);
// ReadEnv dan Runner wajib diisi juga — setup tidak pernah membuka stdout atau
// spawn proses sendiri.
type Options struct {
	Root    string
	Mode    mode.Mode // peroleh dari mode.ParseMode, jangan validasi string sendiri
	Dry     bool
	Yes     bool
	In      io.Reader
	Out     io.Writer
	ReadEnv EnvReader
	Runner  Runner
}

// Step adalah satu tahap pipeline. Mutating=true berarti step mengubah
// filesystem/subprocess: dilewati saat dry-run (dry-run hanya mencetak rencana
// dan tetap menjalankan step read-only seperti validasi).
type Step struct {
	Name     string
	Desc     string
	Mutating bool
	Run      func(*run) error
}

// run adalah state satu eksekusi pipeline.
type run struct {
	o      Options
	values map[string]string // hasil single-pass env read; step yang menulis .env wajib sinkron di sini
}

// Pipeline mengembalikan komposisi step untuk mode. Dev dan prod berbagi
// basis step yang sama; perbedaan mode hanya penyisipan/urutan step —
// bukan duplikasi alur.
func Pipeline(m mode.Mode) []Step {
	steps := []Step{
		{Name: "doctor", Desc: "Pemeriksaan awal: prerequisite proyek dan tool", Run: (*run).doctor},
		{Name: "env", Desc: "Bootstrap .env dari .env.example + baca environment (single-pass)", Run: (*run).envBootstrap},
	}
	if m == mode.ModeProduction {
		steps = append(steps, Step{Name: "env:production-flags", Desc: "Set APP_ENV=production, APP_DEBUG=false", Mutating: true, Run: (*run).enforceProductionEnv})
	}
	steps = append(steps,
		Step{Name: "preflight", Desc: "Preflight file wajib mode terpilih", Run: (*run).preflight},
		Step{Name: "runtime-dirs", Desc: "Siapkan direktori runtime Laravel", Mutating: true, Run: (*run).ensureRuntimeDirs},
	)
	if m == mode.ModeDevelopment {
		steps = append(steps,
			Step{Name: "deps:php", Desc: "composer install --no-interaction", Mutating: true, Run: (*run).composerInstall},
			Step{Name: "deps:js", Desc: "npm install", Mutating: true, Run: (*run).npmInstall},
		)
	}
	steps = append(steps,
		Step{Name: "app:key", Desc: "Pastikan APP_KEY terisi", Mutating: true, Run: (*run).ensureAppKey},
		Step{Name: "db:config", Desc: "Lengkapi konfigurasi database bila perlu", Mutating: true, Run: (*run).ensureDatabaseConfig},
		Step{Name: "env:validate", Desc: "Validasi .env terhadap mode", Run: (*run).validateEnv},
		Step{Name: "superadmin:password", Desc: "Tentukan INITIAL_SUPERADMIN_PASSWORD", Mutating: true, Run: (*run).ensureBootstrapPassword},
	)
	if m == mode.ModeDevelopment {
		steps = append(steps,
			Step{Name: "assets:build", Desc: "npm run build", Mutating: true, Run: (*run).buildAssets},
			Step{Name: "db:migrate", Desc: "php artisan migrate --seed", Mutating: true, Run: (*run).migrateDev},
			Step{Name: "storage:link", Desc: "php artisan storage:link", Mutating: true, Run: (*run).storageLink},
			Step{Name: "cache:clear", Desc: "php artisan optimize:clear", Mutating: true, Run: (*run).cacheClear},
		)
	} else {
		steps = append(steps,
			Step{Name: "db:migrate", Desc: "php artisan migrate --force", Mutating: true, Run: (*run).migrateProd},
			Step{Name: "db:seed", Desc: "php artisan db:seed --force", Mutating: true, Run: (*run).seedProd},
			Step{Name: "storage:link", Desc: "php artisan storage:link", Mutating: true, Run: (*run).storageLink},
			Step{Name: "cache:optimize", Desc: "php artisan optimize", Mutating: true, Run: (*run).cacheOptimize},
		)
	}
	steps = append(steps, Step{Name: "done", Desc: "Ringkasan penyelesaian", Run: (*run).finish})
	return steps
}

// Run mengeksekusi pipeline setup. Setiap step dicetak (nama + deskripsi)
// ke Options.Out sehingga dry-run menampilkan seluruh pipeline, termasuk
// step validasi yang memang tetap dijalankan (read-only).
func Run(o Options) error {
	if o.Out == nil || o.In == nil || o.ReadEnv == nil || o.Runner == nil {
		return fmt.Errorf("setup: Options.Out/In/ReadEnv/Runner wajib diisi")
	}
	if o.Mode != mode.ModeDevelopment && o.Mode != mode.ModeProduction {
		return fmt.Errorf("mode harus development atau production")
	}
	// Bungkus In ke satu bufio.Reader di level run agar semua prompt berbagi
	// buffer — bufio per-prompt menyerap seluruh pipe dan membuang jawaban
	// prompt berikutnya.
	if _, ok := o.In.(*bufio.Reader); !ok {
		o.In = bufio.NewReader(o.In)
	}
	r := &run{o: o}
	steps := Pipeline(o.Mode)
	fmt.Fprintf(o.Out, "\n╭─ %s SETUP\n", strings.ToUpper(brand.Name))
	fmt.Fprintf(o.Out, "│  Mode: %s\n", o.Mode)
	fmt.Fprintln(o.Out, "╰─")
	for i, s := range steps {
		fmt.Fprintf(o.Out, "  [%d/%d] %-20s %s\n", i+1, len(steps), s.Name, s.Desc)
		if o.Dry && s.Mutating {
			fmt.Fprintln(o.Out, "        └─ [dry-run] dilewati")
			continue
		}
		if err := s.Run(r); err != nil {
			return err
		}
	}
	return nil
}

// runStep memakai RunLabel bila runner mendukungnya agar label progres tampil
// di output subprocess; fallback ke Run polos.
func runStep(r Runner, label, name string, args ...string) error {
	if labeled, ok := r.(interface {
		RunLabel(string, string, ...string) error
	}); ok {
		return labeled.RunLabel(label, name, args...)
	}
	return r.Run(name, args...)
}

// readLine membaca satu baris dari in. *bufio.Reader di-reuse agar input
// multi-prompt dari satu buffer tidak hilang.
func readLine(in io.Reader) (string, error) {
	if br, ok := in.(*bufio.Reader); ok {
		return br.ReadString('\n')
	}
	return bufio.NewReader(in).ReadString('\n')
}

func (r *run) confirm(prompt string) bool {
	if r.o.Yes {
		return true
	}
	fmt.Fprintf(r.o.Out, "\n  ? %s [y/N] ", prompt)
	s, _ := readLine(r.o.In)
	return strings.EqualFold(strings.TrimSpace(s), "y") || strings.EqualFold(strings.TrimSpace(s), "yes")
}

// readPassword membaca secret dari in. ponytail: input TIDAK disembunyikan
// (echo tetap tampil) karena menyembunyikan butuh golang.org/x/term —
// dependency baru dilarang item ini; tambahkan masking saat item dependency/
// executor executor-port mendarat.
func (r *run) readPassword() (string, error) {
	s, err := readLine(r.o.In)
	return strings.TrimSpace(s), err
}

func (r *run) doctor() error {
	bad := false
	envPath := filepath.Join(r.o.Root, ".env")
	_, envExists := os.Stat(envPath)
	for _, item := range detect.ProjectMode(r.o.Root, r.o.Mode) {
		mark := "✓"
		if !item.OK {
			mark = "!"
		}
		fmt.Fprintf(r.o.Out, "  %s %-24s %s\n", mark, item.Name, item.Detail)
		// .env.example opsional bila .env sudah ada (proyek legacy).
		if !item.OK && item.Name == ".env.example" {
			if envExists == nil {
				fmt.Fprintf(r.o.Out, "  - %-24s %s\n", item.Name, "opsional: .env sudah ada")
			}
			continue
		}
		if !item.OK {
			bad = true
		}
	}
	if bad {
		return fmt.Errorf("doctor gagal: prerequisite belum tersedia")
	}
	return nil
}

// envBootstrap membuat .env dari .env.example bila belum ada, lalu melakukan
// SATU-satunya pembacaan .env untuk seluruh run. Step yang mengubah .env
// selanjutnya wajib menyinkronkan run.values — bukan membaca ulang file.
func (r *run) envBootstrap() error {
	envPath := filepath.Join(r.o.Root, ".env")
	if _, err := os.Stat(envPath); os.IsNotExist(err) {
		if r.o.Dry {
			fmt.Fprintln(r.o.Out, "        └─ [dry-run] .env akan dibuat dari .env.example")
		} else {
			if !r.confirm("Buat .env dari .env.example?") {
				return fmt.Errorf("setup dibatalkan")
			}
			if err := regular(r.o.Root, ".env.example"); err != nil {
				return err
			}
			b, err := os.ReadFile(filepath.Join(r.o.Root, ".env.example"))
			if err != nil {
				return err
			}
			if err := os.WriteFile(envPath, b, 0600); err != nil {
				return fmt.Errorf("buat .env: %w", err)
			}
		}
	} else if err != nil {
		return fmt.Errorf("cek .env: %w", err)
	}
	if r.o.Dry && !envExistsNow(envPath) {
		// Dry-run tidak membuat .env; tidak ada yang bisa dibaca.
		return nil
	}
	values, err := r.o.ReadEnv(envPath)
	if err != nil {
		return fmt.Errorf("baca .env: %w", err)
	}
	r.values = values
	return nil
}

func envExistsNow(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// enforceProductionEnv membuat mode production otoritatif: arsip rilis umumnya
// diinisialisasi dari .env.example yang masih APP_ENV=local. Hanya dua flag
// mode-controlled yang diubah; secret dan koneksi tidak disentuh.
func (r *run) enforceProductionEnv() error {
	for _, kv := range [][2]string{{"APP_ENV", string(mode.ModeProduction)}, {"APP_DEBUG", "false"}} {
		if err := r.setEnv(kv[0], kv[1]); err != nil {
			return fmt.Errorf("set production environment: %w", err)
		}
	}
	return nil
}

// setEnv menulis satu key ke .env lewat env.Update (quoted/escape-safe untuk
// secret) dan menyinkronkan run.values tanpa membaca ulang file.
// ponytail: satu key per tulis; batch multi-key saat env.Update punya API batch.
func (r *run) setEnv(key, value string) error {
	path := filepath.Join(r.o.Root, ".env")
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	out, err := env.Update(f, key, value)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, out, 0600); err != nil {
		return err
	}
	r.values[key] = value
	return nil
}

func regular(root, name string) error {
	info, err := os.Stat(filepath.Join(root, name))
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s bukan regular file", name)
	}
	return nil
}

func (r *run) preflight() error {
	required := []string{"artisan", "composer.json"}
	if r.o.Mode != mode.ModeProduction {
		required = append(required, "package.json")
	}
	for _, name := range required {
		if err := regular(r.o.Root, name); err != nil {
			return err
		}
	}
	if r.o.Mode == mode.ModeProduction {
		for _, name := range []string{"vendor/autoload.php", "public/build/manifest.json"} {
			if err := regular(r.o.Root, name); err != nil {
				return fmt.Errorf("production preflight: %w", err)
			}
		}
	}
	return nil
}

// ensureRuntimeDirs membuat ulang direktori yang sering tidak ikut arsip
// produksi karena isinya dibuat saat runtime. Laravel butuh ini sebelum
// command artisan pertama (termasuk key:generate yang mem-boot aplikasi).
// MkdirAll aman dijalankan berulang dan mempertahankan isi yang ada.
func (r *run) ensureRuntimeDirs() error {
	for _, dir := range []string{
		"bootstrap/cache",
		"storage/app",
		"storage/framework/cache/data",
		"storage/framework/sessions",
		"storage/framework/views",
		"storage/logs",
	} {
		if err := os.MkdirAll(filepath.Join(r.o.Root, dir), 0755); err != nil {
			return fmt.Errorf("buat runtime directory %s: %w", dir, err)
		}
	}
	return nil
}

func (r *run) composerInstall() error {
	if err := runStep(r.o.Runner, "Installing PHP dependencies", "composer", "install", "--no-interaction"); err != nil {
		return fmt.Errorf("composer install: %w", err)
	}
	return nil
}

func (r *run) npmInstall() error {
	if err := runStep(r.o.Runner, "Installing frontend dependencies", "npm", "install"); err != nil {
		return fmt.Errorf("npm install: %w", err)
	}
	return nil
}

// ensureAppKey mengisi APP_KEY bila kosong dengan format identik fallback
// legacy (base64 32-byte, ditulis env.Update yang quote-safe).
// ponytail: deviasi dari codebase pendahulu — artisan key:generate tidak dipanggil
// karena menulis .env di belakang punggung sehingga butuh re-read (melanggar
// single-pass env read); nilai akhir di .env identik. Kembalikan jalur
// artisan bila semantics generation sisi Laravel dibutuhkan.
func (r *run) ensureAppKey() error {
	if strings.TrimSpace(r.values["APP_KEY"]) != "" {
		return nil
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return fmt.Errorf("buat application key: %w", err)
	}
	if err := r.setEnv("APP_KEY", "base64:"+base64.StdEncoding.EncodeToString(key)); err != nil {
		return fmt.Errorf("simpan application key: %w", err)
	}
	return nil
}

func (r *run) ensureDatabaseConfig() error {
	if r.values["DB_CONNECTION"] == "sqlite" {
		return nil
	}
	if r.o.Yes {
		return nil // env.Validate melempar error lengkap untuk field yang kurang.
	}
	fmt.Fprintln(r.o.Out, "  ╭─ Konfigurasi database")
	fmt.Fprintln(r.o.Out, "  │  Isi koneksi database. Password boleh dikosongkan dengan menekan Enter atau memasukkan -.")
	fmt.Fprintln(r.o.Out, "  │")
	for _, item := range []struct{ key, label string }{
		{"DB_HOST", "Database host"}, {"DB_DATABASE", "Database name"}, {"DB_USERNAME", "Database user"},
	} {
		if err := r.promptValue(item.key, item.label); err != nil {
			return err
		}
	}
	if err := r.promptSecret("DB_PASSWORD", "Database password"); err != nil {
		return err
	}
	fmt.Fprintln(r.o.Out, "  ╰─")
	return nil
}

func (r *run) promptValue(key, label string) error {
	current := r.values[key]
	if current != "" {
		fmt.Fprintf(r.o.Out, "  │  %-24s [%s]: ", label, current)
	} else {
		fmt.Fprintf(r.o.Out, "  │  %-24s: ", label)
	}
	s, err := readLine(r.o.In)
	if err != nil && len(strings.TrimSpace(s)) == 0 {
		return fmt.Errorf("baca %s: %w", key, err)
	}
	s = strings.TrimSpace(s)
	if s == "" {
		s = current
	}
	if s == "-" {
		s = ""
	}
	if s == "" {
		return fmt.Errorf("%s wajib diisi", key)
	}
	if err := r.setEnv(key, s); err != nil {
		return fmt.Errorf("simpan %s: %w", key, err)
	}
	return nil
}

func (r *run) promptSecret(key, label string) error {
	current := r.values[key]
	if current != "" {
		fmt.Fprintf(r.o.Out, "  │  %-24s %s: ", label, "[tersimpan, Enter = tetap]")
	} else {
		fmt.Fprintf(r.o.Out, "  │  %-24s %s: ", label, "[kosong diperbolehkan]")
	}
	s, err := r.readPassword()
	if err != nil && strings.TrimSpace(s) == "" {
		return fmt.Errorf("baca %s: %w", key, err)
	}
	s = strings.TrimSpace(s)
	if s == "" {
		s = current
	}
	if s == "-" {
		s = ""
	}
	if err := r.setEnv(key, s); err != nil {
		return fmt.Errorf("simpan %s: %w", key, err)
	}
	return nil
}

// validateEnv selalu tampil di pipeline. Saat dry-run dengan .env terbaca,
// validasi dijalankan sungguhan (read-only) dan hasilnya dicetak; tanpa .env
// (proyek fresh) cukup checklist statis karena tidak ada yang bisa divalidasi.
func (r *run) validateEnv() error {
	if r.o.Dry {
		if r.values == nil {
			fmt.Fprintln(r.o.Out, "        └─ validasi (checklist): APP_KEY, APP_ENV, DB_CONNECTION wajib; production: APP_ENV=production & APP_DEBUG=false; DB non-sqlite: DB_HOST, DB_DATABASE, DB_USERNAME")
			return nil
		}
		if err := env.Validate(r.values, r.o.Mode); err != nil {
			fmt.Fprintf(r.o.Out, "        └─ validasi: GAGAL — %v\n", err)
			return nil
		}
		fmt.Fprintln(r.o.Out, "        └─ validasi: OK")
		return nil
	}
	if err := env.Validate(r.values, r.o.Mode); err != nil {
		return fmt.Errorf("validasi .env: %w", err)
	}
	return nil
}

// ensureBootstrapPassword memastikan password superadmin awal tersedia.
// Non-interactive (yes) fail-closed: tanpa password di .env, setup berhenti —
// seeder production fail-closed juga bila password tidak ada.
func (r *run) ensureBootstrapPassword() error {
	if strings.TrimSpace(r.values["INITIAL_SUPERADMIN_PASSWORD"]) != "" {
		return nil
	}
	if r.o.Yes {
		return fmt.Errorf("INITIAL_SUPERADMIN_PASSWORD wajib diisi di .env untuk mode non-interactive (minimal 12 karakter)")
	}
	fmt.Fprintln(r.o.Out, "  ╭─ Akun Superadmin")
	fmt.Fprintln(r.o.Out, "  │  Tentukan password awal untuk login Superadmin. Minimal 12 karakter.")
	fmt.Fprintln(r.o.Out, "  │")
	for {
		fmt.Fprintf(r.o.Out, "  │  %-24s ", "Password awal login Superadmin:")
		password, err := r.readPassword()
		if err != nil {
			return fmt.Errorf("baca password bootstrap: %w", err)
		}
		fmt.Fprintf(r.o.Out, "  │  %-24s ", "Ulangi password Superadmin:")
		confirmation, confirmErr := r.readPassword()
		if confirmErr != nil {
			return fmt.Errorf("baca konfirmasi password bootstrap: %w", confirmErr)
		}

		var problem string
		switch {
		case !utf8.ValidString(password):
			problem = "Password Superadmin tidak valid: format password tidak didukung."
		case utf8.RuneCountInString(password) < 12:
			problem = "Password Superadmin tidak valid: minimal 12 karakter."
		case password != confirmation:
			problem = "Konfirmasi password Superadmin tidak cocok."
		}
		if problem != "" {
			fmt.Fprintf(r.o.Out, "  │  ⚠ %s\n", problem)
			continue
		}
		if err := r.setEnv("INITIAL_SUPERADMIN_PASSWORD", password); err != nil {
			return fmt.Errorf("simpan password bootstrap: %w", err)
		}
		fmt.Fprintln(r.o.Out, "  ╰─")
		return nil
	}
}

func (r *run) buildAssets() error {
	if err := runStep(r.o.Runner, "Building frontend assets", "npm", "run", "build"); err != nil {
		return fmt.Errorf("npm build: %w", err)
	}
	return nil
}

func (r *run) migrateDev() error {
	if err := runStep(r.o.Runner, "Running database migrations", "php", "artisan", "migrate", "--seed"); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// migrateProd: konfirmasi eksplisit sebelum migrasi production (fail-closed).
func (r *run) migrateProd() error {
	if !r.confirm("Jalankan migrasi production (--force)?") {
		return fmt.Errorf("migrasi dibatalkan")
	}
	if err := runStep(r.o.Runner, "Running production database migrations", "php", "artisan", "migrate", "--force"); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// seedProd: migrasi dan seeding production sengaja dipisah agar urutan
// terlihat dan bootstrap superadmin benar-benar dibuat setelah tabelnya ada.
func (r *run) seedProd() error {
	if err := runStep(r.o.Runner, "Seeding production database", "php", "artisan", "db:seed", "--force"); err != nil {
		return fmt.Errorf("db seed: %w", err)
	}
	return nil
}

func (r *run) storageLink() error {
	if err := runStep(r.o.Runner, "Linking storage", "php", "artisan", "storage:link"); err != nil {
		return fmt.Errorf("storage link: %w", err)
	}
	return nil
}

func (r *run) cacheClear() error {
	if err := runStep(r.o.Runner, "Clearing application cache", "php", "artisan", "optimize:clear"); err != nil {
		return fmt.Errorf("optimize clear: %w", err)
	}
	return nil
}

func (r *run) cacheOptimize() error {
	if err := runStep(r.o.Runner, "Optimizing application", "php", "artisan", "optimize"); err != nil {
		return fmt.Errorf("optimize: %w", err)
	}
	return nil
}

func (r *run) finish() error {
	if r.o.Dry {
		fmt.Fprintln(r.o.Out, "\n  Dry-run selesai: tidak ada perubahan yang ditulis.")
		return nil
	}
	fmt.Fprintln(r.o.Out)
	fmt.Fprintln(r.o.Out, "╔════════════════════════════════════════════════════════════╗")
	fmt.Fprintln(r.o.Out, "║                     SETUP SELESAI                          ║")
	fmt.Fprintln(r.o.Out, "╚════════════════════════════════════════════════════════════╝")
	fmt.Fprintf(r.o.Out, "✓ Setup %s berhasil diselesaikan.\n", r.o.Mode)
	fmt.Fprintln(r.o.Out, "Langkah berikutnya:")
	fmt.Fprintln(r.o.Out, "  • Jalankan aplikasi sesuai environment Anda.")
	fmt.Fprintln(r.o.Out, "  • Login menggunakan akun Superadmin yang baru dibuat.")
	return nil
}
