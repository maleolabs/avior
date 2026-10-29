package diagnostic

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/maleolabs/avior/internal/mode"
)

func TestCheckRequiredByModeAndPassMissing(t *testing.T) {
	root := t.TempDir()
	// Isolate command checks from tools installed on the host. On Windows,
	// exec.LookPath also searches the current directory when PATH is empty, so
	// use an empty directory as PATH and change into an unrelated temp directory.
	pathDir := t.TempDir()
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(workingDir); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
	pathValue := pathDir
	if os.PathListSeparator == ';' {
		pathValue = pathDir + string(os.PathListSeparator)
	}
	t.Setenv("PATH", pathValue)
	devRequired := []string{"artisan", "composer.json", ".env.example", "composer", "php", "mysql", "package.json", "node", "npm"}
	prodRequired := []string{"artisan", "composer.json", ".env.example", "composer", "php", "mysql", "vendor/autoload.php", "public/build/manifest.json"}
	for _, tc := range []struct {
		mode mode.Mode
		want []string
	}{{mode.ModeDevelopment, devRequired}, {mode.ModeProduction, prodRequired}} {
		got := Check(root, tc.mode)
		names := make([]string, 0, len(got.Checks))
		for _, check := range got.Checks {
			names = append(names, check.Name)
		}
		if !reflect.DeepEqual(names, tc.want) {
			t.Errorf("%s checks = %v, want %v", tc.mode, names, tc.want)
		}
		if got.OK || !reflect.DeepEqual(got.Missing, tc.want) {
			t.Errorf("empty project report = %#v", got)
		}
	}
	for _, name := range append(append([]string{}, devRequired[:3]...), prodRequired[6:]...) {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(""), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Tool availability varies by host; assert deterministic file checks directly.
	for _, tc := range []struct {
		m        mode.Mode
		required []string
	}{{mode.ModeDevelopment, devRequired[:3]}, {mode.ModeProduction, prodRequired[:3]}} {
		report := Check(root, tc.m)
		for _, check := range report.Checks[:3] {
			if !check.OK {
				t.Errorf("%s unexpectedly missing: %#v", check.Name, check)
			}
		}
	}
	var output bytes.Buffer
	if err := Write(&output, Check(root, mode.ModeProduction), true); err != nil {
		t.Fatal(err)
	}
	var decoded Report
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Mode != mode.ModeProduction || decoded.OK {
		t.Fatalf("decoded report = %#v", decoded)
	}
}
