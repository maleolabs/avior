// Package env adalah satu-satunya parser/penulis dotenv untuk seluruh tool
// (arc:avior-architecture). Semantik mengikuti vlucas/phpdotenv yang dipakai
// Laravel: nilai double-quote diproses escape-nya (\n, \r, \t, \\, \", \$)
// dan variabel $VAR diinterpolasi — karena itu penulis meng-escape backslash,
// kutip ganda, dollar, dan newline.
package env

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/maleolabs/avior/internal/mode"
)

// Entry merepresentasikan satu baris .env. Key kosong berarti baris non-assignment
// (komentar atau baris kosong) yang dipertahankan apa adanya lewat Raw.
type Entry struct {
	Key   string
	Value string
	Raw   string // baris asli; hanya dipakai saat Key kosong
}

// Parse membaca .env dari r. Mendukung: komentar (#), baris kosong, prefix
// export opsional, nilai unquoted / single-quote / double-quote, dan komentar
// inline setelah nilai unquoted.
func Parse(r io.Reader) ([]Entry, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("env: baca input: %w", err)
	}
	var entries []Entry
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSuffix(line, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			entries = append(entries, Entry{Raw: line})
			continue
		}
		trimmed = strings.TrimPrefix(trimmed, "export ")
		key, value, ok := strings.Cut(trimmed, "=")
		if !ok {
			// ponytail: potong prefix agar isi baris (bisa secret) tidak ikut
			// ke pesan error/log.
			prefix := trimmed
			if len(prefix) > 16 {
				prefix = prefix[:16] + "…"
			}
			return nil, fmt.Errorf("env: baris %d tidak valid (tanpa '='): %q", i+1, prefix)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("env: baris %d tanpa nama variabel", i+1)
		}
		entries = append(entries, Entry{Key: key, Value: parseValue(strings.TrimSpace(value))})
	}
	return entries, nil
}

// parseValue mengekstrak nilai dari sisi kanan '='. Sesuai phpdotenv: kutip
// membungkus nilai (double-quote di-unescape, single-quote literal); nilai
// unquoted dipotong pada komentar inline " #".
func parseValue(v string) string {
	switch {
	case strings.HasPrefix(v, `"`):
		return unquoteDouble(v)
	case strings.HasPrefix(v, "'"):
		if end := strings.Index(v[1:], "'"); end >= 0 {
			return v[1 : 1+end]
		}
		return strings.TrimPrefix(v, "'")
	default:
		// Sesuai phpdotenv: # mana pun di nilai unquoted memulai komentar —
		// dipotong di # pertama agar nilai efektif sama dengan Laravel.
		if i := strings.IndexByte(v, '#'); i >= 0 {
			v = v[:i]
		}
		return strings.TrimSpace(v)
	}
}

// unquoteDouble mengambil isi dalam kutip ganda lalu memproses escape
// phpdotenv. Escape tak dikenal dibiarkan apa adanya.
func unquoteDouble(v string) string {
	body := strings.TrimPrefix(v, `"`)
	if end := scanQuotedEnd(body); end >= 0 {
		body = body[:end]
	} else {
		body = strings.TrimSuffix(body, `"`)
	}
	var b strings.Builder
	for i := 0; i < len(body); i++ {
		if body[i] != '\\' || i+1 >= len(body) {
			b.WriteByte(body[i])
			continue
		}
		i++
		switch body[i] {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case '\\', '"', '$': // escape yang dihasilkan Quote
			b.WriteByte(body[i])
		default: // escape tak dikenal: dipertahankan apa adanya
			b.WriteByte('\\')
			b.WriteByte(body[i])
		}
	}
	return b.String()
}

// scanQuotedEnd mencari posisi kutip penutup dalam body (setelah kutip pembuka),
// melewati escape \\\". Mengembalikan -1 bila kutip penutup tak ditemukan.
func scanQuotedEnd(body string) int {
	for i := 0; i < len(body); i++ {
		if body[i] == '\\' {
			i++
			continue
		}
		if body[i] == '"' {
			return i
		}
	}
	return -1
}

// Quote mengembalikan value sebagai literal .env double-quote yang aman:
// round-trip identik via Parse maupun phpdotenv. Backslash dan kutip di-escape,
// dollar di-escape karena phpdotenv menginterpolasi $VAR di dalam kutip ganda,
// newline ditulis sebagai escape agar file tetap satu baris per entri.
func Quote(value string) string {
	var b strings.Builder
	b.Grow(len(value) + 2)
	b.WriteByte('"')
	// Iterasi per byte (bukan per rune): secret bisa berisi byte non-UTF-8;
	// range string akan menggantinya U+FFFD dan merusak nilai.
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '$':
			b.WriteString(`\$`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteByte(value[i])
		}
	}
	b.WriteByte('"')
	return b.String()
}

// Marshal menyusun entries menjadi isi file .env. Baris assignment ditulis
// selalu double-quote agar aman untuk secret.
func Marshal(entries []Entry) []byte {
	var b strings.Builder
	for _, e := range entries {
		if e.Key == "" {
			b.WriteString(e.Raw)
		} else {
			b.WriteString(e.Key)
			b.WriteByte('=')
			b.WriteString(Quote(e.Value))
		}
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// Update mengembalikan isi .env baru dengan key bernilai secret: entri pertama
// diganti, duplikat berikutnya dihapus (dotenv last-win membuat nilai duplikat
// lama tetap efektif bila dibiarkan), atau entri ditambahkan di akhir bila
// belum ada. Komentar dan baris kosong dipertahankan. Secret di-quote via
// Quote sehingga round-trip identik meski mengandung #, spasi, kutip, atau
// newline. src boleh nil untuk file baru.
func Update(src io.Reader, key, secret string) ([]byte, error) {
	if err := ValidateKey(key); err != nil {
		return nil, err
	}
	var entries []Entry
	if src != nil {
		var err error
		if entries, err = Parse(src); err != nil {
			return nil, err
		}
	}
	out := entries[:0]
	found := false
	for _, e := range entries {
		if e.Key == key {
			if !found {
				e.Value = secret
				found = true
			} else {
				continue // duplikat: buang, agar last-win tidak memulihkan nilai lama
			}
		}
		out = append(out, e)
	}
	if !found {
		out = append(out, Entry{Key: key, Value: secret})
	}
	return Marshal(out), nil
}

// ReadFile membaca file .env pada path menjadi map KEY=VALUE. Semantik dotenv
// last-win: duplikat key, nilai terakhir yang efektif. Ini satu-satunya cara
// membaca .env dari luar package — lapis lain (setup, runtime, discovery)
// WAJIB lewat sini; parser Parse tidak boleh diduplikasi (arc:avior-architecture:
// tidak ada parser .env kedua, menghabiskan duplikasi 3x di codebase pendahulu:
// env.Read, runtime.dotenvValues, discovery.env).
func ReadFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	entries, err := Parse(f)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(entries))
	for _, e := range entries {
		if e.Key != "" {
			out[e.Key] = e.Value // last-win
		}
	}
	return out, nil
}

// ReadEffective mengembalikan nilai efektif key untuk project di root:
// environment proses menang atas .env (precedence runtime effectiveEnv dan
// discovery.env di codebase pendahulu — env eksplisit dari CLI tidak boleh ditimpa
// file), fallback nilai .env di root. File tak ada/tak terbaca berarti
// ("", false): lookup best-effort, sesuai perilaku ketiga call site legacy.
// ponytail: parse error ikut fail-closed ke tidak-ada; saat discovery
// di-port, naikkan jadi diagnostic agar .env rusak kelihatan.
func ReadEffective(root, key string) (string, bool) {
	if v, ok := os.LookupEnv(key); ok {
		return v, true
	}
	values, err := ReadFile(filepath.Join(root, ".env"))
	if err != nil {
		return "", false
	}
	v, ok := values[key]
	return v, ok
}

// ValidateKey memastikan nama variabel layak untuk .env (huruf/angka/underscore,
// tidak diawali angka) — cukup untuk konvensi Laravel.
func ValidateKey(key string) error {
	if key == "" {
		return fmt.Errorf("env: nama variabel kosong")
	}
	for i, r := range key {
		ok := r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') ||
			(i > 0 && r >= '0' && r <= '9')
		if !ok {
			return fmt.Errorf("env: nama variabel tidak valid: %q", key)
		}
	}
	return nil
}

// Validate memeriksa konfigurasi minimum yang dibutuhkan mode terpilih.
// Diport dari codebase pendahulu (setup-side env validation); mode memakai tipe
// mode.Mode tunggal, bukan string [arc:avior-architecture].
func Validate(values map[string]string, m mode.Mode) error {
	required := []string{"APP_KEY", "APP_ENV", "DB_CONNECTION"}
	for _, key := range required {
		if strings.TrimSpace(values[key]) == "" {
			return fmt.Errorf("%s wajib diisi", key)
		}
	}
	if m == mode.ModeProduction && values["APP_ENV"] != string(mode.ModeProduction) {
		return fmt.Errorf("APP_ENV harus production")
	}
	if m == mode.ModeProduction && strings.EqualFold(values["APP_DEBUG"], "true") {
		return fmt.Errorf("APP_DEBUG harus false di production")
	}
	if values["DB_CONNECTION"] != "sqlite" {
		for _, key := range []string{"DB_HOST", "DB_DATABASE", "DB_USERNAME"} {
			if strings.TrimSpace(values[key]) == "" {
				return fmt.Errorf("%s wajib diisi untuk database %s", key, values["DB_CONNECTION"])
			}
		}
	}
	return nil
}
