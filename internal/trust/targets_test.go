package trust

import (
	"os"
	"strings"
	"testing"

	"github.com/maleolabs/avior/internal/brand"
)

func TestChromiumBinaryPrefersPathAndFindsNativeBrave(t *testing.T) {
	path, ok := ChromiumBinary(func(name string) (string, error) {
		if name == "brave-browser" {
			return "/custom/brave", nil
		}
		return "", os.ErrNotExist
	}, func(string) error { return os.ErrNotExist })
	if !ok || path != "/custom/brave" {
		t.Fatalf("got %q, %v", path, ok)
	}
	path, ok = ChromiumBinary(func(string) (string, error) { return "", os.ErrNotExist }, func(path string) error {
		if path == "/opt/brave.com/brave/brave" {
			return nil
		}
		return os.ErrNotExist
	})
	if !ok || path != "/opt/brave.com/brave/brave" {
		t.Fatalf("native Brave = %q, %v", path, ok)
	}
}

func TestFirefoxProfilesParsesRelativeAndAbsoluteEntries(t *testing.T) {
	profiles := firefoxProfiles("[Profile0]\nPath=abc.default\nIsRelative=1\n[Profile1]\nPath=/tmp/firefox\nIsRelative=0\n[General]\nStartWithLastProfile=1\n", "/home/test/.mozilla/firefox")
	if len(profiles) != 2 || profiles[0].Path != "/home/test/.mozilla/firefox/abc.default" || profiles[1].Path != "/tmp/firefox" {
		t.Fatalf("profiles = %#v", profiles)
	}
}

func TestFirefoxCertutilArgsAreStableAndIdempotent(t *testing.T) {
	want := []string{"-A", "-n", brand.CANickname, "-t", "C,,", "-i", "/tmp/root.crt", "-d", "sql:/tmp/profile"}
	if got := firefoxCertutilArgs("/tmp/profile", "/tmp/root.crt"); strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
	if got := firefoxCertutilArgs("/tmp/profile", "/tmp/root.crt"); strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatal("repeated command construction must be identical")
	}
}

func TestChromiumNSSPathAndCertutilArgs(t *testing.T) {
	if got, want := chromiumNSSPath("/home/test"), "/home/test/.pki/nssdb"; got != want {
		t.Fatalf("NSS path = %q, want %q", got, want)
	}
	want := []string{"-A", "-n", brand.CANickname, "-t", "C,,", "-i", "/tmp/root.crt", "-d", "sql:/home/test/.pki/nssdb"}
	got := chromiumNSSCertutilArgs("/home/test/.pki/nssdb", "/tmp/root.crt")
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}
