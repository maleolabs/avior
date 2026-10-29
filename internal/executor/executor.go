// Package executor menjalankan subprocess argumen-list (arc:avior-architecture):
// TANPA shell, tanpa interpolasi, dengan sanitasi secret pada semua output yang
// tampil ke pengguna. Out dan ErrOut di-inject (sto:p1-writer-injection) —
// paket ini tidak pernah membuka os.Stdout/os.Stderr sendiri.
package executor

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/maleolabs/avior/internal/ui"
)

// Runner menjalankan subprocess untuk pipeline setup. Memenuhi setup.Runner.
type Runner struct {
	Root   string
	DryRun bool
	Out    io.Writer // progres step
	ErrOut io.Writer // kartu error
}

// Run menjalankan command; label default adalah command-nya sendiri
// (disanitasi sebelum tampil — argumen bisa mengandung secret).
func (r Runner) Run(name string, args ...string) error {
	return r.RunLabel(strings.Join(append([]string{name}, args...), " "), name, args...)
}

// RunLabel menjalankan command sambil menampilkan deskripsi aman untuk
// pengguna. Command sengaja tidak dicetak mentah: bisa berisi secret dan
// berisik bagi pengguna non-teknis.
//
// Kontrak render (sto:p1-writer-injection): pada path gagal RunLabel me-render
// ErrorCard sendiri ke Runner.ErrOut (inline) dan mengembalikan
// ui.ReportedError yang membawa pesan yang sama. Render top-level untuk error
// yang naik ke pengguna adalah milik cmd/ lewat ui.RenderError. Karena error
// ini membawa Render sendiri, dua jalur me-render akan menghasilkan kartu
// dobel — cmd/ wajib memilih: render inline saja (jangan RenderError ulang)
// ATAU hapus render inline di sini. Keputusan final saat cmd/ mendarat.
func (r Runner) RunLabel(label, name string, args ...string) error {
	label = sanitizeText(label)
	fmt.Fprintf(r.Out, "  ▸ %s\n", label)
	if r.DryRun {
		fmt.Fprintf(r.Out, "  ✓ %s [dry-run]\n", label)
		return nil
	}
	name, args = windowsCommand(name, args)
	c := exec.Command(name, args...) // argumen-list; sengaja TANPA shell
	c.Dir = r.Root
	// Tangkap kedua stream: output berguna bisa di stdout (bukan hanya stderr).
	var output bytes.Buffer
	c.Stdout, c.Stderr = &output, &output
	if err := c.Run(); err != nil {
		fmt.Fprintf(r.Out, "  ✗ %s\n", label)
		detail := truncateOutput(output.String(), 4096)
		message := "Langkah gagal: " + label
		message += fmt.Sprintf("\nRuntime: cwd=%s, executable=%s", r.Root, executablePath(name))
		if detail != "" {
			message += "\nDetail:\n" + detail
		}
		ui.ErrorCard(r.ErrOut, message)
		return ui.NewReportedError(fmt.Errorf("%w: %s", err, message), message)
	}
	fmt.Fprintf(r.Out, "  ✓ %s\n", label)
	return nil
}

func windowsCommand(name string, args []string) (string, []string) {
	if runtime.GOOS != "windows" {
		return name, args
	}
	if strings.EqualFold(filepath.Ext(name), ".exe") || strings.EqualFold(filepath.Ext(name), ".com") || strings.EqualFold(filepath.Ext(name), ".bat") || strings.EqualFold(filepath.Ext(name), ".cmd") {
		if runtime.GOOS == "windows" && (strings.EqualFold(filepath.Ext(name), ".bat") || strings.EqualFold(filepath.Ext(name), ".cmd")) {
			if !safeBatchPath(name) {
				return name, args
			}
			return "cmd.exe", append([]string{"/d", "/s", "/c", `"` + name + `"`}, args...)
		}
		return name, args
	}
	for _, ext := range []string{".exe", ".com", ".cmd", ".bat"} {
		candidate := name + ext
		if _, err := exec.LookPath(candidate); err == nil {
			if ext == ".cmd" || ext == ".bat" {
				if !safeBatchPath(candidate) {
					return candidate, args
				}
				return "cmd.exe", append([]string{"/d", "/s", "/c", `"` + candidate + `"`}, args...)
			}
			return candidate, args
		}
	}
	return name, args
}

func safeBatchPath(path string) bool {
	for _, r := range path {
		if strings.ContainsRune(`"&|<>()^%!`, r) {
			return false
		}
	}
	return path != ""
}

func executablePath(name string) string {
	resolved, _ := windowsCommand(name, nil)
	path, err := exec.LookPath(resolved)
	if err != nil {
		return fmt.Sprintf("%s (tidak ditemukan: %v)", name, err)
	}
	return path
}

var ansiSequence = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
var secretArgument = regexp.MustCompile(`(?i)(--?(?:password|passwd|token|secret|api[-_]?key|key)(?:[= ]+))[^ ]+`)

// sanitizeText menghapus control sequence terminal dan menyamarkan nilai
// argumen secret sebelum konteks subprocess ditampilkan ke pengguna.
func sanitizeText(value string) string {
	value = ansiSequence.ReplaceAllString(value, "")
	return secretArgument.ReplaceAllString(value, `${1}[REDACTED]`)
}

// truncateOutput menyimpan awal (konteks command) dan akhir (di mana composer
// melaporkan failure yang actionable) dalam batas limit.
func truncateOutput(value string, limit int) string {
	value = strings.TrimSpace(sanitizeText(value))
	if limit <= 0 || len(value) <= limit {
		return value
	}
	const marker = "\n… [output truncated] …\n"
	if limit <= len(marker) {
		return value[len(value)-limit:]
	}
	remaining := limit - len(marker)
	head := remaining / 2
	tail := remaining - head
	return value[:head] + marker + value[len(value)-tail:]
}
