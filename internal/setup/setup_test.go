package setup

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maleolabs/avior/internal/brand"
	"github.com/maleolabs/avior/internal/env"
	"github.com/maleolabs/avior/internal/mode"
)

// fakeRunner merekam command tanpa spawn proses.
type fakeRunner struct {
	calls [][2]interface{} // {name, args}
}

func (f *fakeRunner) Run(name string, args ...string) error {
	f.calls = append(f.calls, [2]interface{}{name, args})
	return nil
}

func (f *fakeRunner) commands() []string {
	out := make([]string, 0, len(f.calls))
	for _, c := range f.calls {
		out = append(out, c[0].(string)+" "+strings.Join(c[1].([]string), " "))
	}
	return out
}

// countingEnv membungkus EnvReader dan menghitung jumlah pembacaan.
type countingEnv struct {
	inner EnvReader
	count int
}

func (c *countingEnv) Read(path string) (map[string]string, error) {
	c.count++
	return c.inner(path)
}

// fakeTools membuat executable palsu (composer, php, mysql, node, npm) agar
// doctor tidak gagal di lingkungan tanpa tool terpasang.
func fakeTools(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"composer", "php", "mysql", "node", "npm"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\necho fake 1.0\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// fixture menyiapkan proyek Laravel minimal dengan .env yang sudah valid.
func fixture(t *testing.T, envContent string, extraFiles ...string) string {
	t.Helper()
	fakeTools(t)
	root := t.TempDir()
	for _, name := range []string{"artisan", "composer.json", "package.json", ".env.example"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range extraFiles {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if envContent != "" {
		if err := os.WriteFile(filepath.Join(root, ".env"), []byte(envContent), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func stepNames(steps []Step) []string {
	out := make([]string, len(steps))
	for i, s := range steps {
		out[i] = s.Name
	}
	return out
}

func TestPipelineComposition(t *testing.T) {
	dev := stepNames(Pipeline(mode.ModeDevelopment))
	prod := stepNames(Pipeline(mode.ModeProduction))

	wantDev := []string{
		"doctor", "env", "preflight", "runtime-dirs",
		"deps:php", "deps:js",
		"app:key", "db:config", "env:validate",
		"assets:build", "db:migrate", "storage:link", "cache:clear", "done",
	}
	wantProd := []string{
		"doctor", "env", "env:production-flags", "preflight", "runtime-dirs",
		"app:key", "db:config", "env:validate",
		"db:migrate", "db:seed", "storage:link", "cache:optimize", "done",
	}
	if strings.Join(dev, ",") != strings.Join(wantDev, ",") {
		t.Fatalf("pipeline dev = %v, want %v", dev, wantDev)
	}
	if strings.Join(prod, ",") != strings.Join(wantProd, ",") {
		t.Fatalf("pipeline prod = %v, want %v", prod, wantProd)
	}
	// dev dan prod harus komposisi dari basis step yang sama.
	for _, name := range []string{"doctor", "env", "app:key", "db:config", "env:validate"} {
		if !contains(dev, name) || !contains(prod, name) {
			t.Fatalf("step %s harus ada di kedua mode", name)
		}
	}
}

func TestManifestOptInSkipStepsAndCustomStep(t *testing.T) {
	root := fixture(t, devEnv)
	manifest := `{"setup":{"skip":["deps:js"],"steps":[{"name":"project:prepare","run":"php","args":["artisan","project:prepare"]}]}}`
	if err := os.WriteFile(filepath.Join(root, brand.ManifestFile), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	runner := &fakeRunner{}
	err := Run(Options{Root: root, Mode: mode.ModeDevelopment, Yes: true, In: strings.NewReader(""), Out: &out, ReadEnv: env.ReadFile, Runner: runner})
	if err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	got := strings.Join(runner.commands(), "|")
	if strings.Contains(got, "npm install") || !strings.Contains(got, "php artisan project:prepare") {
		t.Fatalf("commands: %s", got)
	}
}

func TestManifestRejectsUnknownFieldsAndKeys(t *testing.T) {
	for _, body := range []string{`{"bogus":true}`, `{"setup":{"skip":["artisan"]}}`, `{"setup":{"steps":[{"name":"unsafe","run":"/bin/sh"}]}}`} {
		root := fixture(t, devEnv)
		if err := os.WriteFile(filepath.Join(root, brand.ManifestFile), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readManifest(root); err == nil {
			t.Errorf("readManifest(%s) succeeded", body)
		}
	}
}

func TestManifestEnvRequired(t *testing.T) {
	root := fixture(t, devEnv)
	var out bytes.Buffer
	err := Run(Options{Root: root, Mode: mode.ModeDevelopment, Yes: true, In: strings.NewReader(""), Out: &out, ReadEnv: env.ReadFile, Runner: &fakeRunner{}, Manifest: Manifest{EnvRequired: []string{"APP_URL"}}})
	if err == nil || !strings.Contains(err.Error(), "APP_URL wajib diisi") {
		t.Fatalf("error: %v", err)
	}
}

func TestProductionBuildManifestIsOptionalByDefault(t *testing.T) {
	root := fixture(t, "APP_KEY=base64:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=\nAPP_ENV=production\nAPP_DEBUG=false\nDB_CONNECTION=sqlite\n", "vendor/autoload.php")
	if err := (&run{o: Options{Root: root, Mode: mode.ModeProduction}}).preflight(); err != nil {
		t.Fatalf("preflight without build manifest: %v", err)
	}
	if err := (&run{o: Options{Root: root, Mode: mode.ModeProduction, Manifest: Manifest{EnvRequired: []string{"public/build/manifest.json"}}}}).preflight(); err == nil {
		t.Fatal("expected declared build manifest requirement")
	}
}

func TestMergeManifestExplicitOverridesFile(t *testing.T) {
	merged := mergeManifest(Manifest{Setup: ManifestSetup{Skip: []string{"deps:js"}}}, Manifest{Setup: ManifestSetup{Skip: []string{"deps:php"}}})
	if len(merged.Setup.Skip) != 1 || merged.Setup.Skip[0] != "deps:php" {
		t.Fatalf("merged skip = %v", merged.Setup.Skip)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

const devEnv = "APP_KEY=base64:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=\n" +
	"APP_ENV=development\n" +
	"DB_CONNECTION=sqlite\n" +
	"INITIAL_SUPERADMIN_PASSWORD=supersecret-panjang-12\n"

func TestSingleEnvReadPerRun(t *testing.T) {
	root := fixture(t, devEnv)
	var out bytes.Buffer
	counter := &countingEnv{inner: env.ReadFile}
	runner := &fakeRunner{}
	err := Run(Options{
		Root: root, Mode: mode.ModeDevelopment, Yes: true,
		In: strings.NewReader(""), Out: &out,
		ReadEnv: counter.Read, Runner: runner,
	})
	if err != nil {
		t.Fatalf("run: %v\nout:\n%s", err, out.String())
	}
	if counter.count != 1 {
		t.Fatalf(".env dibaca %d kali, want 1 (single-pass)", counter.count)
	}
	// Step dev harus tereksekusi sebagai subprocess dalam urutan pipeline.
	got := strings.Join(runner.commands(), "|")
	want := strings.Join([]string{
		"composer install --no-interaction",
		"npm install",
		"npm run build",
		"php artisan migrate --seed",
		"php artisan storage:link",
		"php artisan optimize:clear",
	}, "|")
	if got != want {
		t.Fatalf("commands =\n%s\nwant\n%s", got, want)
	}
}

func TestSingleEnvReadPerRunProduction(t *testing.T) {
	root := fixture(t,
		"APP_KEY=base64:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=\n"+
			"APP_ENV=production\nAPP_DEBUG=false\nDB_CONNECTION=sqlite\n"+
			"INITIAL_SUPERADMIN_PASSWORD=supersecret-panjang-12\n",
		"vendor/autoload.php", "public/build/manifest.json")
	var out bytes.Buffer
	counter := &countingEnv{inner: env.ReadFile}
	runner := &fakeRunner{}
	err := Run(Options{
		Root: root, Mode: mode.ModeProduction, Yes: true,
		In: strings.NewReader(""), Out: &out,
		ReadEnv: counter.Read, Runner: runner,
	})
	if err != nil {
		t.Fatalf("run: %v\nout:\n%s", err, out.String())
	}
	if counter.count != 1 {
		t.Fatalf(".env dibaca %d kali, want 1 (single-pass)", counter.count)
	}
	got := strings.Join(runner.commands(), "|")
	want := strings.Join([]string{
		"php artisan migrate --force",
		"php artisan db:seed --force",
		"php artisan storage:link",
		"php artisan optimize",
	}, "|")
	if got != want {
		t.Fatalf("commands =\n%s\nwant\n%s", got, want)
	}
	// enforceProductionEnv tidak boleh mengubah secret/koneksi; flag mode harus benar.
	values, err := env.ReadFile(filepath.Join(root, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if values["APP_ENV"] != "production" || values["APP_DEBUG"] != "false" {
		t.Fatalf("production flags salah: %#v", values)
	}
}

func TestDryRunPrintsEntirePipelineIncludingValidation(t *testing.T) {
	root := fixture(t, devEnv)
	var out bytes.Buffer
	runner := &fakeRunner{}
	err := Run(Options{
		Root: root, Mode: mode.ModeDevelopment, Dry: true,
		In: strings.NewReader(""), Out: &out,
		ReadEnv: env.ReadFile, Runner: runner,
	})
	if err != nil {
		t.Fatalf("dry run: %v\nout:\n%s", err, out.String())
	}
	for _, s := range Pipeline(mode.ModeDevelopment) {
		if !strings.Contains(out.String(), s.Name) {
			t.Errorf("dry-run output tidak mencetak step %q\nout:\n%s", s.Name, out.String())
		}
	}
	if !strings.Contains(out.String(), "env:validate") {
		t.Error("dry-run harus mencetak step validasi")
	}
	if len(runner.calls) != 0 {
		t.Errorf("dry-run tidak boleh spawn subprocess: %v", runner.commands())
	}
	// Dry-run tidak boleh menulis: direktori runtime tidak dibuat.
	if _, err := os.Stat(filepath.Join(root, "bootstrap", "cache")); err == nil {
		t.Error("dry-run membuat bootstrap/cache — dry-run tidak boleh memutasi filesystem")
	}
}

func TestAppKeyGeneratedAndSynced(t *testing.T) {
	root := fixture(t, "APP_ENV=development\nDB_CONNECTION=sqlite\nINITIAL_SUPERADMIN_PASSWORD=supersecret-panjang-12\n")
	var out bytes.Buffer
	counter := &countingEnv{inner: env.ReadFile}
	runner := &fakeRunner{}
	err := Run(Options{
		Root: root, Mode: mode.ModeDevelopment, Yes: true,
		In: strings.NewReader(""), Out: &out,
		ReadEnv: counter.Read, Runner: runner,
	})
	if err != nil {
		t.Fatalf("run: %v\nout:\n%s", err, out.String())
	}
	// Kunci dibuat dan ditulis ke .env meski .env tidak dibaca ulang.
	values, err := env.ReadFile(filepath.Join(root, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(values["APP_KEY"], "base64:") {
		t.Fatalf("APP_KEY tidak terisi: %q", values["APP_KEY"])
	}
	// Validasi tetap lolos dengan nilai tersinkron — count tetap 1.
	if counter.count != 1 {
		t.Fatalf(".env dibaca %d kali, want 1", counter.count)
	}
}

func TestSetupAbortedWhenEnvDeclined(t *testing.T) {
	root := fixture(t, "")
	var out bytes.Buffer
	runner := &fakeRunner{}
	err := Run(Options{
		Root: root, Mode: mode.ModeDevelopment,
		In: strings.NewReader("n\n"), Out: &out,
		ReadEnv: env.ReadFile, Runner: runner,
	})
	if err == nil || err.Error() != "setup dibatalkan" {
		t.Fatalf("err = %v, want 'setup dibatalkan'", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, ".env")); statErr == nil {
		t.Error(".env tidak seharusnya dibuat saat konfirmasi ditolak")
	}
}

func TestGenericProjectDoesNotRequireBootstrapPassword(t *testing.T) {
	root := fixture(t, "APP_ENV=development\nDB_CONNECTION=sqlite\n")
	var out bytes.Buffer
	err := Run(Options{
		Root: root, Mode: mode.ModeDevelopment, Yes: true,
		In: strings.NewReader(""), Out: &out,
		ReadEnv: env.ReadFile, Runner: &fakeRunner{},
	})
	if err != nil {
		t.Fatalf("generic project setup: %v", err)
	}
}

// TestMultipleDbPromptsFromOneReader menjaga fix review MAJOR: empat prompt
// berurutan (DB host, name, user, password) dari SATU strings.Reader — semua
// jawaban harus terbaca. Tanpa fix (bufio per-prompt), prompt pertama
// menyerap seluruh pipe dan prompt kedua dapat EOF.
func TestMultipleDbPromptsFromOneReader(t *testing.T) {
	root := fixture(t,
		"APP_KEY=base64:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=\n"+
			"APP_ENV=development\nDB_CONNECTION=mysql\n"+
			"")
	var out bytes.Buffer
	runner := &fakeRunner{}
	err := Run(Options{
		Root: root, Mode: mode.ModeDevelopment,
		In:  strings.NewReader("127.0.0.1\nappdb\nappuser\nsecret\n"),
		Out: &out, ReadEnv: env.ReadFile, Runner: runner,
	})
	if err != nil {
		t.Fatalf("run: %v\nout:\n%s", err, out.String())
	}
	values, err := env.ReadFile(filepath.Join(root, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"DB_HOST": "127.0.0.1", "DB_DATABASE": "appdb", "DB_USERNAME": "appuser", "DB_PASSWORD": "secret",
	} {
		if values[key] != want {
			t.Errorf("%s = %q, want %q (prompt ke-2+ kehilangan input?)", key, values[key], want)
		}
	}
}

func TestRunRejectsInvalidMode(t *testing.T) {
	root := fixture(t, devEnv)
	var out bytes.Buffer
	err := Run(Options{
		Root: root, Mode: mode.Mode("staging"),
		In: strings.NewReader(""), Out: &out,
		ReadEnv: env.ReadFile, Runner: &fakeRunner{},
	})
	if err == nil || err.Error() != "mode harus development atau production" {
		t.Fatalf("err = %v, want guard mode eksplisit", err)
	}
	if len(out.String()) > 0 {
		t.Error("mode tidak valid tidak boleh menghasilkan output pipeline")
	}
}

func TestDryRunValidatesLoadedEnvForReal(t *testing.T) {
	// .env valid terbaca → dry-run menjalankan env.Validate sungguhan: OK.
	root := fixture(t, devEnv)
	var out bytes.Buffer
	err := Run(Options{
		Root: root, Mode: mode.ModeDevelopment, Dry: true,
		In: strings.NewReader(""), Out: &out,
		ReadEnv: env.ReadFile, Runner: &fakeRunner{},
	})
	if err != nil {
		t.Fatalf("dry run: %v\nout:\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "validasi: OK") {
		t.Errorf("dry-run dengan .env valid harus mencetak 'validasi: OK'\nout:\n%s", out.String())
	}

	// .env rusak → dry-run mencetak GAGAL (read-only, tidak abort pipeline).
	badRoot := fixture(t, "APP_ENV=development\n") // APP_KEY & DB_CONNECTION kosong
	out.Reset()
	err = Run(Options{
		Root: badRoot, Mode: mode.ModeDevelopment, Dry: true,
		In: strings.NewReader(""), Out: &out,
		ReadEnv: env.ReadFile, Runner: &fakeRunner{},
	})
	if err != nil {
		t.Fatalf("dry run (env rusak) tidak boleh gagal: %v\nout:\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "validasi: GAGAL") || !strings.Contains(out.String(), "APP_KEY wajib diisi") {
		t.Errorf("dry-run dengan .env rusak harus mencetak 'validasi: GAGAL — APP_KEY wajib diisi'\nout:\n%s", out.String())
	}
}
