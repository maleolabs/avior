package executor

import (
	"bytes"
	"errors"
	"io"
	"runtime"
	"strings"
	"testing"

	"github.com/maleolabs/avior/internal/setup"
	"github.com/maleolabs/avior/internal/ui"
)

// Compile-time: executor.Runner memenuhi kontrak Runner pipeline setup.
var _ setup.Runner = Runner{}

// outRunner membuat Runner dengan buffer Out/ErrOut (tanpa stdout nyata).
func outRunner() (Runner, *bytes.Buffer, *bytes.Buffer) {
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	return Runner{Out: out, ErrOut: errOut}, out, errOut
}

func TestWindowsCommandResolution(t *testing.T) {
	if got, _ := windowsCommand("tool", nil); got != "tool" {
		t.Fatalf("non-Windows command = %q", got)
	}
	if got, _ := windowsCommand("tool.exe", nil); got != "tool.exe" {
		t.Fatalf("explicit executable = %q", got)
	}
}

func TestWindowsCommandWrapsCmdScripts(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows command line only")
	}
	got, args := windowsCommand(`C:\Program Files\Composer\composer.cmd`, []string{"install", "--no-interaction"})
	if got != "cmd.exe" || len(args) != 7 || args[0] != "/d" || args[3] != `"C:\Program Files\Composer\composer.cmd"` {
		t.Fatalf("command=(%q,%q)", got, args)
	}
}

func TestRunnerRunLabelSuccessAndDryRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("true bukan executable Windows; cakupan dijalankan di runner POSIX")
	}
	r, out, _ := outRunner()
	if err := r.RunLabel("Membaca konfigurasi", "true"); err != nil {
		t.Fatalf("RunLabel: %v", err)
	}
	if !strings.Contains(out.String(), "  ✓ Membaca konfigurasi\n") {
		t.Fatalf("output sukses salah:\n%s", out.String())
	}

	dry, out2, _ := outRunner()
	dry.DryRun = true
	if err := dry.RunLabel("Langkah", "true"); err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if !strings.Contains(out2.String(), "[dry-run]") {
		t.Fatalf("dry-run harus tertanda:\n%s", out2.String())
	}
}

// TestRunnerNoShellInterpolation: argumen dengan spasi/metacharacter tetap
// dilewatkan sebagai SATU argv — tidak pernah dieksekusi lewat shell.
// Diskriminator exit-code: "1; exit 9" lewat shell exit 9 (gagal), lewat
// argv-list printf exit 0 (sukses) — asersi err==nil membuktikan tanpa shell.
func TestRunnerNoShellInterpolation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("printf external bukan executable Windows; cakupan dijalankan di runner POSIX")
	}
	r, _, _ := outRunner()
	err := r.RunLabel("cek argv", "printf", "%s", "1; exit 9")
	if err != nil {
		t.Fatalf("argv-list harus sukses (shell akan exit 9): %v", err)
	}
}

// TestRunnerFailureSanitizesSecrets: command gagal yang mencetak secret ke
// stdout harus di-REDACTED baik di kartu error (ErrOut) maupun di error.
func TestRunnerFailureSanitizesSecrets(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh bukan executable Windows; cakupan dijalankan di runner POSIX")
	}
	r, out, errOut := outRunner()
	err := r.RunLabel("migrasi", "sh", "-c", "echo 'migrate --password=s3cret-xyz gagal' >&2; exit 3")
	if err == nil {
		t.Fatal("command gagal harus mengembalikan error")
	}
	for _, w := range []io.Writer{out, errOut} {
		if s := w.(*bytes.Buffer).String(); strings.Contains(s, "s3cret-xyz") {
			t.Fatalf("secret bocor ke output:\n%s", s)
		}
	}
	card := errOut.String()
	if !strings.Contains(card, "--password=[REDACTED]") {
		t.Fatalf("kartu error harus menampilkan argumen tersamarkan:\n%s", card)
	}
	if !strings.Contains(card, "╭─ Error") || !strings.Contains(err.Error(), "exit status 3") {
		t.Fatalf("penyebab (exit status) harus ada di error:\n%s", card)
	}
	// Error path satu pintu: ReportedError dari ui.
	var re ui.ReportedError
	if !errors.As(err, &re) {
		t.Fatalf("error runner harus ui.ReportedError, dapat %T", err)
	}
	if !strings.Contains(re.Error(), "exit status 3") {
		t.Fatal("Error() harus membawa penyebab runtime")
	}
	if strings.Contains(re.UserMessage(), "s3cret-xyz") || strings.Contains(re.UserMessage(), "exit status 3") {
		t.Fatal("UserMessage tidak boleh membawa secret atau detail runtime mentah")
	}
}

func TestSanitizeText(t *testing.T) {
	cases := [][2]string{
		{"--password=hunter2", "--password=[REDACTED]"},
		{"-token abc", "-token [REDACTED]"},
		{"--API_KEY=zzz", "--API_KEY=[REDACTED]"},
		{"--key=abc", "--key=[REDACTED]"},
		{"--api-key=abc", "--api-key=[REDACTED]"},
		{"--secret abc", "--secret [REDACTED]"},
		{"\x1b[31mmerah\x1b[0m", "merah"},
		{"--file=x.txt", "--file=x.txt"}, // non-secret utuh
	}
	for _, c := range cases {
		if got := sanitizeText(c[0]); got != c[1] {
			t.Errorf("sanitizeText(%q) = %q, want %q", c[0], got, c[1])
		}
	}
}

func TestTruncateOutputKeepsHeadAndTail(t *testing.T) {
	long := strings.Repeat("a", 3000) + strings.Repeat("b", 3000)
	got := truncateOutput(long, 4096)
	if len(got) > 4096 || !strings.Contains(got, "[output truncated]") || !strings.HasPrefix(got, "aaa") || !strings.HasSuffix(got, "bbb") {
		t.Fatalf("truncate harus menyimpan head+tail: len=%d", len(got))
	}
	if got := truncateOutput("--password=hunter2 sisa", 0); strings.Contains(got, "hunter2") || !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("truncate harus tetap menyamarkan: %q", got)
	}
}
