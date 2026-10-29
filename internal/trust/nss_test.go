package trust

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestBundledCertutilVerifiesManifest(t *testing.T) {
	dir := t.TempDir()
	nss := filepath.Join(dir, "nss")
	_ = os.Mkdir(nss, 0700)
	p := filepath.Join(nss, "certutil")
	data := []byte("helper")
	if err := os.WriteFile(p, data, 0700); err != nil {
		t.Fatal(err)
	}
	s := sha256.Sum256(data)
	m := nssManifest{Helpers: map[string]nssHelper{runtime.GOOS + "/" + runtime.GOARCH: {Path: "certutil", SHA256: hex.EncodeToString(s[:])}}}
	b, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(nss, "MANIFEST.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := bundledCertutil(filepath.Join(dir, "avior"))
	if err != nil || got != p {
		t.Fatalf("got %q, %v; want %q", got, err, p)
	}
	data[0]++
	if err := os.WriteFile(p, data, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := bundledCertutil(filepath.Join(dir, "avior")); err == nil {
		t.Fatal("expected checksum failure")
	}
}
