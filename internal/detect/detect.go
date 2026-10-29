// Package detect memeriksa prerequisite proyek generik (file wajib, tool di
// PATH). Diport dari codebase pendahulu tanpa perubahan perilaku; mode memakai
// tipe mode.Mode tunggal [arc:avior-architecture].
package detect

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/maleolabs/avior/internal/mode"
)

// Result adalah hasil satu pemeriksaan prerequisite.
type Result struct {
	Name   string
	OK     bool
	Detail string
}

// Version menjalankan tool dengan argumen version dan mengambil baris versi
// pertama sebagai detail. Gagal lookpath/exec tetap mengembalikan Result
// dengan OK=false — doctor menampilkan semua baris, tidak berhenti di awal.
func Version(name string, args ...string) Result {
	p, err := exec.LookPath(name)
	if err != nil && runtime.GOOS == "windows" && filepath.Ext(name) == "" {
		for _, ext := range []string{".exe", ".com", ".cmd", ".bat"} {
			if candidate, lookupErr := exec.LookPath(name + ext); lookupErr == nil {
				p, err = candidate, nil
				break
			}
		}
	}
	if err != nil {
		return Result{name, false, "tidak ditemukan"}
	}
	command, commandArgs := p, args
	if runtime.GOOS == "windows" && (strings.EqualFold(filepath.Ext(p), ".cmd") || strings.EqualFold(filepath.Ext(p), ".bat")) {
		command, commandArgs = "cmd.exe", append([]string{"/d", "/s", "/c", `"` + p + `"`}, args...)
	}
	out, err := exec.Command(command, commandArgs...).CombinedOutput()
	detail := conciseDetail(string(out))
	if err != nil {
		return Result{name, false, detail}
	}
	return Result{name, true, detail}
}

// conciseDetail menjaga output doctor tetap ringkas walau tool menulis
// diagnostik verbose (mis. banner copyright PHP atau output extra mysql).
// Baris non-kosong pertama adalah baris versi yang tampil ke user; kegagalan
// command tetap menyertakan baris itu sebagai konteks.
func conciseDetail(output string) string {
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return strings.Join(strings.Fields(line), " ")
		}
	}
	return ""
}

// File memeriksa keberadaan file name di dalam root.
func File(root, name string) Result {
	_, err := os.Stat(filepath.Join(root, name))
	return Result{name, err == nil, filepath.Join(root, name)}
}

// ProjectMode melaporkan hanya requirement untuk lifecycle mode yang diminta.
func ProjectMode(root string, m mode.Mode) []Result {
	r := []Result{
		File(root, "artisan"), File(root, "composer.json"), File(root, ".env.example"),
		Version("composer", "--version"), Version("php", "-v"), Version("mysql", "--version"),
	}
	if m != mode.ModeProduction {
		r = append(r, File(root, "package.json"), Version("node", "--version"), Version("npm", "--version"))
	}
	if m == mode.ModeProduction {
		// .env di-bootstrap dari .env.example oleh setup saat absen, jadi
		// ketiadaannya tidak boleh membuat doctor production gagal di awal.
		r = append(r, File(root, "vendor/autoload.php"), File(root, "public/build/manifest.json"))
	}
	return r
}
