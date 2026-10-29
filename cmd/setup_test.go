package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maleolabs/avior/internal/ui"
)

// setupFixture menyiapkan proyek Laravel minimal + tool palsu agar doctor
// step tidak gagal.
func setupFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"artisan", "composer.json", "package.json", ".env.example"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	bin := t.TempDir()
	for _, name := range []string{"composer", "php", "mysql", "node", "npm"} {
		p := filepath.Join(bin, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\necho fake 1.0\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return root
}

func TestSetupDryRunPrintsPipelineWithoutMutating(t *testing.T) {
	root := setupFixture(t)
	out, err := runCLIStrip(t, "--root", root, "setup", "development", "--dry-run", "--yes")
	if err != nil {
		t.Fatalf("setup dry-run: %v\n%s", err, out)
	}
	for _, step := range []string{"doctor", "env", "preflight", "runtime-dirs", "app:key", "db:config", "env:validate", "done"} {
		if !strings.Contains(out, step) {
			t.Errorf("dry-run tidak mencetak step %q\n%s", step, out)
		}
	}
	if !strings.Contains(out, "dry-run") {
		t.Errorf("dry-run output tidak menandai dry-run\n%s", out)
	}
	// Dry-run tidak boleh memutasi: .env tidak dibuat.
	if _, err := os.Stat(filepath.Join(root, ".env")); err == nil {
		t.Error("dry-run membuat .env")
	}
}

func TestSetupRejectsInvalidModeAndExtraArgs(t *testing.T) {
	root := setupFixture(t)
	for _, args := range [][]string{
		{"--root", root, "setup", "staging"},
		{"--root", root, "setup", "development", "production"},
	} {
		if _, err := runCLI(t, args...); err == nil {
			t.Errorf("accepted invalid args %v", args)
		}
	}
}

func TestSetupRequiresModeWithoutInteractiveTerminal(t *testing.T) {
	root := setupFixture(t)
	_, err := runCLIPipe(t, "--root", root, "setup", "--yes")
	if err == nil || !strings.Contains(err.Error(), "terminal interaktif") {
		t.Fatalf("setup tanpa mode di non-TTY harus ditolak fail-closed, dapat: %v", err)
	}
}

func TestSetupErrorPathRendersErrorCard(t *testing.T) {
	root := setupFixture(t)
	// Preflight production tanpa vendor → gagal dengan error card di stderr.
	out, err := runCLI(t, "--root", root, "setup", "production", "--yes")
	if err == nil {
		t.Fatal("setup production tanpa vendor harus gagal")
	}
	if !strings.Contains(ui.Strip(out), "Error") {
		t.Fatalf("kegagalan setup tidak me-render error card:\n%s", out)
	}
}
