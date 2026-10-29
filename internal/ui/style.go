// Styling terminal untuk help template cobra dan kartu progress (item
// m2-cli-cobra-structure). Palette kecil: semua warna tetap terbaca di
// terminal gelap dan menjaga output CLI konsisten.
//
// ponytail: TTY detection membaca status os.Stdout (read-only) — pelanggaran
// ketat "leaf tidak menyentuh os.Stdout" diterima di sini karena template
// function cobra hanya menerima string; upgrade path: inject detector per
// command dan pra-render help saat cmd layer punya akses writer penuh.
package ui

import (
	"os"
	"strings"
)

// Color adalah escape sequence SGR.
type Color string

const (
	Primary Color = "\x1b[94m" // blue
	Success Color = "\x1b[92m"
	Danger  Color = "\x1b[91m"
	Warning Color = "\x1b[93m"
	Info    Color = "\x1b[96m"
	// SGR faint diabaikan sebagian terminal; abu-abu konsisten terlihat.
	Dim     Color = "\x1b[90m"
	Default Color = "\x1b[97m"
)

var enabledOverride *bool

// SetEnabled dipakai test untuk mematikan/menyalakan warna deterministik.
// nil mengembalikan deteksi default (NO_COLOR + TTY os.Stdout).
func SetEnabled(enabled *bool) { enabledOverride = enabled }

// Enabled mendeteksi dukungan warna: NO_COLOR menang, lalu cek TTY stdout.
func Enabled() bool {
	if enabledOverride != nil {
		return *enabledOverride
	}
	if _, noColor := os.LookupEnv("NO_COLOR"); noColor {
		return false
	}
	info, err := os.Stdout.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// Style membungkus text dengan warna bila warna diaktifkan.
func Style(color Color, text string) string {
	if !Enabled() {
		return text
	}
	return string(color) + text + "\x1b[0m"
}

// ClearLine menghapus sisa baris (untuk spinner/progress in-place).
func ClearLine() string {
	if !Enabled() {
		return ""
	}
	return "\r\x1b[2K"
}

func DefaultText(s string) string { return Style(Default, s) }
func DimText(s string) string     { return Style(Dim, s) }
func PrimaryText(s string) string { return Style(Primary, s) }
func SuccessText(s string) string { return Style(Success, s) }
func DangerText(s string) string  { return Style(Danger, s) }
func InfoText(s string) string    { return Style(Info, s) }
func WarningText(s string) string { return Style(Warning, s) }

// Strip menghapus semua escape sequence palette — memudahkan assert output
// pada test.
func Strip(s string) string {
	return strings.NewReplacer(
		"\x1b[94m", "", "\x1b[92m", "", "\x1b[91m", "", "\x1b[93m", "",
		"\x1b[96m", "", "\x1b[90m", "", "\x1b[97m", "", "\x1b[0m", "",
	).Replace(s)
}
