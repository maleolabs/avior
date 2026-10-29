package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// archiveFixture menyiapkan proyek minimal + VERSION + checkout git (build
// number) agar archive dapat berjalan tanpa toolchain Laravel.
func archiveFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "VERSION"), []byte("1.2.3\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "composer.json"), []byte("{}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "app"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app", "User.php"), []byte("<?php\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Build number bersumber dari git (paritas pendahulu): dry-run tetap
	// membutuhkan commit aktif.
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "-A"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "init"},
	} {
		c := exec.Command("git", args...)
		c.Dir = root
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return root
}

func TestArchiveDryRunMainSource(t *testing.T) {
	root := archiveFixture(t)
	out, err := runCLIStrip(t, "--root", root, "archive", "main-source", "--dry-run")
	if err != nil {
		t.Fatalf("archive dry-run: %v\n%s", err, out)
	}
	for _, want := range []string{"Archive plan", "main-source", "dry-run", "Create distribution archives", ".tar.gz"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run output tidak memuat %q:\n%s", want, out)
		}
	}
	// Dry-run tidak boleh membuat dist/.
	if _, err := os.Stat(filepath.Join(root, "dist")); err == nil {
		t.Error("dry-run membuat direktori dist/")
	}
}

func TestArchiveAliasDevelopmentAndDist(t *testing.T) {
	root := archiveFixture(t)
	if _, err := runCLIStrip(t, "--root", root, "archive", "development", "--dry-run"); err != nil {
		t.Errorf("alias development ditolak: %v", err)
	}
	out, err := runCLIStrip(t, "--root", root, "dist", "main-source", "--dry-run")
	if err != nil {
		t.Errorf("alias dist ditolak: %v", err)
	}
	if !strings.Contains(out, "main-source") {
		t.Errorf("alias dist salah mode:\n%s", out)
	}
}

func TestArchiveRejectsInvalidMode(t *testing.T) {
	root := archiveFixture(t)
	for _, args := range [][]string{
		{"--root", root, "archive"},
		{"--root", root, "archive", "staging"},
		{"--root", root, "archive", "main-source", "production"},
	} {
		if _, err := runCLI(t, args...); err == nil {
			t.Errorf("accepted invalid args %v", args)
		}
	}
}

func TestArchiveMissingVersionFailsCleanly(t *testing.T) {
	root := t.TempDir()
	_, err := runCLIStrip(t, "--root", root, "archive", "main-source", "--dry-run")
	if err == nil {
		t.Fatal("archive tanpa VERSION harus gagal")
	}
	if !strings.Contains(err.Error(), "VERSION") {
		t.Errorf("error tidak menyebut VERSION: %v", err)
	}
}

func TestArchiveEmptyVersionFailsCleanly(t *testing.T) {
	root := archiveFixture(t)
	if err := os.WriteFile(filepath.Join(root, "VERSION"), []byte("  \n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := runCLIStrip(t, "--root", root, "archive", "main-source", "--dry-run")
	if err == nil || !strings.Contains(err.Error(), "VERSION kosong") {
		t.Fatalf("VERSION kosong harus ditolak, dapat: %v", err)
	}
}
