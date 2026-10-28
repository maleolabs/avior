// Package env adalah satu-satunya parser/penulis dotenv untuk seluruh tool
// (arc:avior-architecture). Semantik mengikuti vlucas/phpdotenv yang dipakai
// Laravel: nilai double-quote diproses escape-nya (\n, \r, \t, \\, \", \$)
// dan variabel $VAR diinterpolasi — karena itu penulis meng-escape backslash,
// kutip ganda, dollar, dan newline.
package env

import (
	"fmt"
	"io"
	"strings"
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
			return nil, fmt.Errorf("env: baris %d tidak valid (tanpa '='): %q", i+1, trimmed)
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
		// ponytail: komentar inline unquoted hanya dipotong pada " #" (spasi
		// sebelum #) — cukup untuk file .env Laravel; perilaku penuh phpdotenv
		// upgrade saat kebutuhan nyata muncul.
		if i := strings.Index(v, " #"); i >= 0 {
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
		default: // \\ \" \$ dan escape tak dikenal
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
	for _, r := range value {
		switch r {
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
			b.WriteRune(r)
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

// Update mengembalikan isi .env baru dengan key bernilai secret: nilai lama
// diganti, atau entri ditambahkan di akhir bila belum ada. Komentar dan baris
// kosong dipertahankan. Secret di-quote via Quote sehingga round-trip identik
// meski mengandung #, spasi, kutip, atau newline. src boleh nil untuk file baru.
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
	for i := range entries {
		if entries[i].Key == key {
			entries[i].Value = secret
			return Marshal(entries), nil
		}
	}
	entries = append(entries, Entry{Key: key, Value: secret})
	return Marshal(entries), nil
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
