package proxy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// writeFakeCaddy menulis script shell yang meniru `caddy version` — tanpa
// network, tanpa binary asli — lalu mengalihkan brand.StateDir() (via HOME)
// ke dir test agar manifest uji tidak menyentuh home asli. Skip di Windows
// (shebang script tak dieksekusi OS); cek checksum tetap ter-cover via
// TestFileSHA256.
func writeFakeCaddy(t *testing.T, versionOutput string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake caddy berupa shell script; skip di Windows")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)        // Unix: os.UserHomeDir
	t.Setenv("USERPROFILE", home) // Windows: os.UserHomeDir
	path := filepath.Join(home, "caddy")
	script := "#!/bin/sh\necho '" + versionOutput + "'\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeManifest menulis manifest checksum ke ManifestPath() (dir-nya
// dibuat dulu). sha kosong = checksum dihitung dari isi binPath.
func writeManifest(t *testing.T, binPath, sha string) {
	t.Helper()
	if sha == "" {
		sum := sha256.Sum256(mustRead(t, binPath))
		sha = hex.EncodeToString(sum[:])
	}
	data, err := json.Marshal(Manifest{SHA256: sha, Version: CaddyVersion})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(ManifestPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ManifestPath(), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestVerifyManaged(t *testing.T) {
	tests := []struct {
		name          string
		versionOutput string
		manifestSHA   string // "" = checksum benar; lainnya = checksum salah
		writeManifest bool
		wantErr       bool
	}{
		{name: "ok — versi + checksum match", versionOutput: "v2.10.0 h1:test", writeManifest: true},
		{name: "ok — patch beda masih dalam pin major.minor", versionOutput: "v2.10.2 h1:x", writeManifest: true},
		{name: "refuse — versi tidak match pin", versionOutput: "v2.9.3 h1:x", writeManifest: true, wantErr: true},
		{name: "refuse — checksum tidak match manifest (H3)", versionOutput: "v2.10.0 h1:test", manifestSHA: "deadbeef", writeManifest: true, wantErr: true},
		{name: "refuse — manifest hilang", versionOutput: "v2.10.0 h1:test", writeManifest: false, wantErr: true},
		{name: "refuse — binary tidak bisa dieksekusi", versionOutput: "", writeManifest: true, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bin := writeFakeCaddy(t, tt.versionOutput)
			if tt.writeManifest {
				writeManifest(t, bin, tt.manifestSHA)
			}
			err := VerifyManaged(bin)
			if (err != nil) != tt.wantErr {
				t.Fatalf("VerifyManaged() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestVerifyManagedCorruptManifest(t *testing.T) {
	bin := writeFakeCaddy(t, "v2.10.0 h1:test")
	if err := os.MkdirAll(filepath.Dir(ManifestPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ManifestPath(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyManaged(bin); err == nil {
		t.Fatal("manifest rusak harus refuse, dapat nil")
	}
}

// VerifyPATH adalah fallback dev: versi harus match, tapi tanpa manifest
// tetap lolos — kontras dengan VerifyManaged.
func TestVerifyPATH(t *testing.T) {
	bin := writeFakeCaddy(t, "v2.10.0 h1:test")
	if err := VerifyPATH(bin); err != nil {
		t.Fatalf("VerifyPATH() error = %v, want nil", err)
	}
	if err := os.MkdirAll(filepath.Dir(ManifestPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ManifestPath(), []byte(`{"sha256":"deadbeef"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyManaged(bin); err == nil {
		t.Fatal("VerifyManaged harus gagal padahal VerifyPATH lolos — gate harus berbeda")
	}
}

func TestCheckVersionBinaryHilang(t *testing.T) {
	if err := checkVersion(filepath.Join(t.TempDir(), "tidak-ada")); err == nil {
		t.Fatal("binary hilang harus error")
	}
}

func TestVersionMatches(t *testing.T) {
	tests := []struct {
		out  string
		want bool
	}{
		{"v2.10.0 h1:abc\n", true},
		{"caddy v2.10.1\n", true},
		{"v2.9.3 h1:abc\n", false},
		{"v2.1.0 h1:abc\n", false}, // bukan prefix salah-salah (v2.1 != v2.10.)
		{"", false},
	}
	prefix := "v" + CaddyVersion[:strings.LastIndex(CaddyVersion, ".")+1]
	for _, tt := range tests {
		var got bool
		for _, field := range strings.Fields(tt.out) {
			if strings.HasPrefix(field, prefix) {
				got = true
			}
		}
		if got != tt.want {
			t.Errorf("versionMatches(%q) = %v, want %v", tt.out, got, tt.want)
		}
	}
}

func TestFileSHA256(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("avior"), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("avior"))
	want := hex.EncodeToString(sum[:])
	got, err := fileSHA256(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("fileSHA256 = %s, want %s", got, want)
	}
	if _, err := fileSHA256(filepath.Join(t.TempDir(), "tidak-ada")); err == nil {
		t.Fatal("file hilang harus error")
	}
}
