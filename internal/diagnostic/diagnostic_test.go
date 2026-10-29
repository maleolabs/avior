package diagnostic

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/maleolabs/avior/internal/mode"
)

func TestCheckIsModeAwareAndJSONIsMachineReadable(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"artisan", "composer.json", ".env.example", "package.json", "vendor/autoload.php", "public/build/manifest.json"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(""), 0600); err != nil {
			t.Fatal(err)
		}
	}
	dev := Check(root, mode.ModeDevelopment)
	prod := Check(root, mode.ModeProduction)
	if len(prod.Checks) >= len(dev.Checks) {
		t.Fatalf("production checks %d, development %d", len(prod.Checks), len(dev.Checks))
	}
	var output bytes.Buffer
	if err := Write(&output, prod, true); err != nil {
		t.Fatal(err)
	}
	var decoded Report
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Mode != mode.ModeProduction || len(decoded.Checks) != len(prod.Checks) {
		t.Fatalf("decoded report = %#v", decoded)
	}
}
