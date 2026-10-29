package env

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReadFileValues mengunci semantik baca file lewat satu parser untuk
// semua kasus acceptance criteria (sto:p1-single-env-parser): plain, kutip
// simetris, kutip asimetris, escape, trailing comment, nilai kosong.
func TestReadFileValues(t *testing.T) {
	src := "" +
		"PLAIN=nilai\n" +
		`SYM="dikutip"` + "\n" +
		`SYM1='dikutip1'` + "\n" +
		`ASYM_OPEN="tanpa-tutup` + "\n" + // kutip asimetris: pembuka tanpa penutup
		`ASYM1_OPEN='tanpa-tutup1` + "\n" +
		`ESCAPED="kutip \" dan backslash \\"` + "\n" +
		"COMMENT=val # komentar\n" +
		"COMMENT_HASH=val#hash\n" +
		"EMPTY=\n" +
		"EMPTY_QUOTED=\"\"\n" +
		"DUPE=salah\n" +
		"DUPE=benar\n"
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	want := map[string]string{
		"PLAIN":        "nilai",
		"SYM":          "dikutip",
		"SYM1":         "dikutip1",
		"ASYM_OPEN":    "tanpa-tutup",
		"ASYM1_OPEN":   "tanpa-tutup1",
		"ESCAPED":      `kutip " dan backslash \`,
		"COMMENT":      "val",
		"COMMENT_HASH": "val",
		"EMPTY":        "",
		"EMPTY_QUOTED": "",
		"DUPE":         "benar", // last-win
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("key %s: got %q, want %q", k, got[k], w)
		}
	}
	if len(got) != len(want) {
		t.Errorf("jumlah key: got %d, want %d: %v", len(got), len(want), got)
	}
}

func TestReadFileError(t *testing.T) {
	dir := t.TempDir()
	if _, err := ReadFile(filepath.Join(dir, "tidak-ada.env")); err == nil {
		t.Error("want error untuk file tidak ada")
	}
	broken := filepath.Join(dir, "broken.env")
	if err := os.WriteFile(broken, []byte("BUKAN_ASSIGNMENT\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(broken); err == nil {
		t.Error("want error untuk baris tidak valid")
	}
}

func TestReadEffective(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte(
		"DB_HOST=file-host\nDB_PASSWORD=file-secret\nAPP_ENV=local\n",
	), 0600); err != nil {
		t.Fatal(err)
	}

	// Fallback .env saat env proses tidak ada.
	if v, ok := ReadEffective(root, "DB_HOST"); !ok || v != "file-host" {
		t.Errorf("fallback .env: got (%q, %v), want (file-host, true)", v, ok)
	}

	// Environment proses menang (precedence legacy runtime/discovery).
	t.Setenv("DB_HOST", "env-host")
	t.Setenv("DB_PASSWORD", "") // env kosong tetap menang — LookupEnv ok
	if v, ok := ReadEffective(root, "DB_HOST"); !ok || v != "env-host" {
		t.Errorf("env menang: got (%q, %v), want (env-host, true)", v, ok)
	}
	if v, ok := ReadEffective(root, "DB_PASSWORD"); !ok || v != "" {
		t.Errorf("env kosong menang: got (%q, %v), want (empty, true)", v, ok)
	}

	// Key tidak ada di mana pun.
	if v, ok := ReadEffective(root, "TIDAK_ADA"); v != "" || ok {
		t.Errorf("key tidak ada: got (%q, %v), want (empty, false)", v, ok)
	}

	// Root tanpa .env — best-effort, bukan error.
	if v, ok := ReadEffective(t.TempDir(), "APP_ENV"); v != "" || ok {
		t.Errorf("tanpa .env: got (%q, %v), want (empty, false)", v, ok)
	}
}
