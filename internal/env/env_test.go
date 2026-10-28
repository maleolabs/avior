package env

import (
	"bytes"
	"strings"
	"testing"
)

// roundTrip memvalidasi Quote -> Parse mengembalikan nilai identik (semantik
// H6: secret dengan karakter spesial tidak boleh rusak).
func roundTrip(t *testing.T, value string) {
	t.Helper()
	entries, err := Parse(strings.NewReader("KEY=" + Quote(value) + "\n"))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if got := entries[0].Value; got != value {
		t.Fatalf("round-trip: got %q, want %q", got, value)
	}
}

func TestQuoteRoundTripSpecialChars(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"hash dan spasi", "p#ass word"},
		{"kutip ganda", `he said "hi"`},
		{"kutip tunggal", "it's"},
		{"multiline", "line1\nline2\nline3"},
		{"backslash", `a\b\c`},
		{"dollar", "$HOME ${VAR}"},
		{"escape newline literal", `not\anewline`},
		{"byte non-UTF-8", "pw\xff\xfe secret"},
		{"tab dan CR", "a\tb\rc"},
		{"kosong", ""},
		{"plain", "simple-secret-123"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			roundTrip(t, tt.value)
		})
	}
}

func TestUpdateRoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		secret string
	}{
		{"hash dan spasi", "p#ass word"},
		{"kutip ganda", `he said "hi"`},
		{"kutip tunggal", "it's"},
		{"multiline", "line1\nline2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := Update(nil, "DB_PASSWORD", tt.secret)
			if err != nil {
				t.Fatalf("Update error: %v", err)
			}
			entries, err := Parse(bytes.NewReader(out))
			if err != nil {
				t.Fatalf("Parse error: %v", err)
			}
			var got string
			found := false
			for _, e := range entries {
				if e.Key == "DB_PASSWORD" {
					got, found = e.Value, true
				}
			}
			if !found {
				t.Fatalf("entri DB_PASSWORD tidak ditemukan: %+v", entries)
			}
			if got != tt.secret {
				t.Fatalf("got %q, want %q", got, tt.secret)
			}
		})
	}
}

func TestUpdateExistingFile(t *testing.T) {
	src := "# konfigurasi aplikasi\nAPP_NAME=avior\n\nDB_HOST=localhost\n"
	out, err := Update(strings.NewReader(src), "DB_PASSWORD", `p#ass "word"`)
	if err != nil {
		t.Fatalf("Update error: %v", err)
	}
	entries, err := Parse(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	got := map[string]string{}
	for _, e := range entries {
		if e.Key != "" {
			got[e.Key] = e.Value
		}
	}
	want := map[string]string{
		"APP_NAME":    "avior",
		"DB_HOST":     "localhost",
		"DB_PASSWORD": `p#ass "word"`,
	}
	for k, w := range want {
		if got[k] != w {
			t.Fatalf("key %s: got %q, want %q", k, got[k], w)
		}
	}
	// komentar dan baris kosong dipertahankan
	if !bytes.Contains(out, []byte("# konfigurasi aplikasi\n")) {
		t.Fatalf("komentar hilang:\n%s", out)
	}
}

func TestUpdateReplacesExistingKey(t *testing.T) {
	src := "DB_PASSWORD=lama\nDB_HOST=localhost\n"
	out, err := Update(strings.NewReader(src), "DB_PASSWORD", "baru #1")
	if err != nil {
		t.Fatalf("Update error: %v", err)
	}
	entries, _ := Parse(bytes.NewReader(out))
	var values []string
	for _, e := range entries {
		if e.Key == "DB_PASSWORD" {
			values = append(values, e.Value)
		}
	}
	if len(values) != 1 || values[0] != "baru #1" {
		t.Fatalf("values = %q, want satu entri 'baru #1'", values)
	}
}

func TestUpdateInvalidKey(t *testing.T) {
	if _, err := Update(nil, "9BAD KEY", "x"); err == nil {
		t.Fatal("want error untuk key tidak valid")
	}
}

func TestUpdateRemovesDuplicateKeys(t *testing.T) {
	src := "DB_PASSWORD=lama\nDB_HOST=localhost\nDB_PASSWORD=rahasia\n"
	out, err := Update(strings.NewReader(src), "DB_PASSWORD", "baru")
	if err != nil {
		t.Fatalf("Update error: %v", err)
	}
	if n := bytes.Count(out, []byte("DB_PASSWORD=")); n != 1 {
		t.Fatalf("DB_PASSWORD muncul %d kali, want 1:\n%s", n, out)
	}
	entries, _ := Parse(bytes.NewReader(out))
	var values []string
	for _, e := range entries {
		if e.Key == "DB_PASSWORD" {
			values = append(values, e.Value)
		}
	}
	if len(values) != 1 || values[0] != "baru" {
		t.Fatalf("values = %q, want satu entri 'baru'", values)
	}
}

func TestParseUnquotedHashIsComment(t *testing.T) {
	// Sesuai phpdotenv: # mana pun di nilai unquoted memulai komentar.
	entries, err := Parse(strings.NewReader("KEY=val#ue\n"))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if got := entries[0].Value; got != "val" {
		t.Fatalf("got %q, want %q", got, "val")
	}
}

func TestParseErrorTruncatesLineContent(t *testing.T) {
	secret := "SUPERSECRETVALUE1234567890"
	_, err := Parse(strings.NewReader("BROKEN_" + secret + "_NO_EQUALS\n"))
	if err == nil {
		t.Fatal("want error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error membocorkan isi baris: %v", err)
	}
}

func TestParseFormats(t *testing.T) {
	src := "" +
		"# komentar awal\n" +
		"\n" +
		"export EXPORTED=ya\n" +
		"PLAIN=nilai\n" +
		"SINGLE='it s'\n" +
		`DOUBLE="a\nb"` + "\n" +
		"INLINE=nilai # komentar\n" +
		"EMPTY=\n"
	entries, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	want := []Entry{
		{Key: "EXPORTED", Value: "ya"},
		{Key: "PLAIN", Value: "nilai"},
		{Key: "SINGLE", Value: "it s"},
		{Key: "DOUBLE", Value: "a\nb"},
		{Key: "INLINE", Value: "nilai"},
		{Key: "EMPTY", Value: ""},
	}
	var got []Entry
	for _, e := range entries {
		if e.Key != "" {
			got = append(got, e)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entri, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Key != w.Key || got[i].Value != w.Value {
			t.Fatalf("entri %d: got %+v, want %+v", i, got[i], w)
		}
	}
}

func TestParseInvalidLine(t *testing.T) {
	if _, err := Parse(strings.NewReader("BUKAN_ASSIGNMENT\n")); err == nil {
		t.Fatal("want error untuk baris tanpa '='")
	}
}

func TestValidateSecret(t *testing.T) {
	tests := []struct {
		name    string
		secret  string
		wantMin int // jumlah minimal alasan
	}{
		{"aman", "S3cretKunci!", 0},
		{"spasi", "pass word", 1},
		{"tab", "pass\tword", 1},
		{"newline", "pass\nword", 1},
		{"hash", "p#ass", 1},
		{"kutip ganda", `p"ass`, 1},
		{"kutip tunggal", "p'ass", 1},
		{"gabungan", `p#ass "wo'rd` + "\n", 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reasons := ValidateSecret(tt.secret)
			if len(reasons) < tt.wantMin {
				t.Fatalf("reasons = %q, want minimal %d", reasons, tt.wantMin)
			}
			for _, r := range reasons {
				if strings.TrimSpace(r) == "" {
					t.Fatalf("alasan kosong: %q", reasons)
				}
			}
		})
	}
}
