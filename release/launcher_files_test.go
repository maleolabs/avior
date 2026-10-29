// TestLauncherFiles mengunci kehadiran launcher 3 OS di template arsip
// (release/) — CI menyalinnya ke setiap arsip rilis (m3-install-channels).
// Jalur Windows tidak bisa dieksekusi di non-Windows; isi minimum dijaga
// di sini, smoke eksekusi nyata ada di launcher_test.go (setup.sh).
package release

import (
	"os"
	"strings"
	"testing"

	"github.com/maleolabs/avior/internal/brand"
)

func TestLauncherFiles(t *testing.T) {
	exe := brand.Name + ".exe"
	cases := map[string][]string{
		"setup.sh":  {"$dir/" + brand.Name},
		"setup.ps1": {exe},
		"setup.bat": {exe},
	}
	for name, wants := range cases {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Errorf("launcher %s hilang dari template arsip: %v", name, err)
			continue
		}
		for _, want := range wants {
			if !strings.Contains(string(data), want) {
				t.Errorf("launcher %s tidak memanggil %q:\n%s", name, want, data)
			}
		}
	}
}
