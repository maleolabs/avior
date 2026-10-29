package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/maleolabs/avior/internal/state"
	"github.com/maleolabs/avior/internal/ui"
)

// runCLI menjalankan satu invocation lewat Run (writer buffer) — padanan
// run() di main.go sebelum rewire cobra.
func runCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := Run(args, &out, &out)
	return out.String(), err
}

func runCLIStrip(t *testing.T, args ...string) (string, error) {
	t.Helper()
	out, err := runCLI(t, args...)
	return ui.Strip(out), err
}

// runCLIPipe menjalankan CLI dengan stdin pipe (bukan TTY) — untuk path
// fail-closed yang menuntut terminal interaktif.
func runCLIPipe(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := RunIn(args, bytes.NewReader(nil), &out, &out)
	return ui.Strip(out.String()), err
}

func TestDiagnosticJSONFailureAndSuccess(t *testing.T) {
	root := t.TempDir()
	out, err := runCLIStrip(t, "--root", root, "verify", "production", "--json")
	if err == nil {
		t.Fatal("verify accepted missing requirements")
	}
	var failed struct {
		OK bool `json:"ok"`
	}
	if json.Unmarshal([]byte(out), &failed) != nil || failed.OK {
		t.Fatalf("failure JSON = %q", out)
	}
	for _, name := range []string{"artisan", "composer.json", ".env.example", "vendor/autoload.php", "public/build/manifest.json"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(""), 0600); err != nil {
			t.Fatal(err)
		}
	}
	bin := t.TempDir()
	for name, script := range map[string]string{"composer": "#!/bin/sh\necho Composer 2\n", "php": "#!/bin/sh\necho PHP 8\n", "mysql": "#!/bin/sh\necho MySQL 8\n"} {
		path := filepath.Join(bin, name)
		if err := os.WriteFile(path, []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	out, err = runCLIStrip(t, "--root", root, "verify", "production", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var passed struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal([]byte(out), &passed); err != nil || !passed.OK {
		t.Fatalf("success JSON = %q, err=%v", out, err)
	}
}

func TestDiagnosticJSONFalseOnCLIErrors(t *testing.T) {
	for _, args := range [][]string{
		{"--root", t.TempDir(), "verify", "--json", "--json"},
		{"--root", t.TempDir(), "verify", "--bad"},
		{"--root", "/path/that/does/not/exist", "verify", "--json"},
	} {
		out, err := runCLI(t, args...)
		if err == nil {
			t.Errorf("Run(%v) succeeded", args)
			continue
		}
		if strings.Contains(out, `"ok"`) {
			t.Errorf("Run(%v) wrote JSON on argument/path failure: %q", args, out)
		}
	}
}

func TestMonitorAndProjectScopedCommands(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a, b := filepath.Join(t.TempDir(), "project-a"), filepath.Join(t.TempDir(), "project-b")
	if err := os.MkdirAll(a, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(b, 0700); err != nil {
		t.Fatal(err)
	}
	pa, _, err := state.Paths(a)
	if err != nil {
		t.Fatal(err)
	}
	pb, _, err := state.Paths(b)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := state.SaveState(pa, state.RuntimeState{ProjectRoot: a, MainPID: state.Process{PID: os.Getpid(), Command: os.Args[0], StartedAt: now}}); err != nil {
		t.Fatal(err)
	}
	if err := state.SaveState(pb, state.RuntimeState{ProjectRoot: b, Port: 8000, MainPID: state.Process{PID: os.Getpid(), Command: os.Args[0], StartedAt: now}}); err != nil {
		t.Fatal(err)
	}
	out, err := runCLIStrip(t, "monitor", a)
	if err != nil {
		t.Fatal(err)
	}
	assertMonitorRow(t, out, a, 0)
	out, err = runCLIStrip(t, "monitor", b)
	if err != nil {
		t.Fatal(err)
	}
	assertMonitorRow(t, out, b, 8000)
	// monitor tanpa argumen: semua runtime hidup.
	out, err = runCLIStrip(t, "monitor")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, a) || !strings.Contains(out, b) {
		t.Fatalf("monitor global tidak memuat kedua proyek: %q", out)
	}
	if _, err := runCLIStrip(t, "attach", a); err != nil {
		t.Fatal(err)
	}
	if _, err := runCLIStrip(t, "attach", filepath.Join(t.TempDir(), "unknown")); err == nil {
		t.Fatal("attach fell back to another project")
	}
	// stop tanpa registry child harus menolak dengan aman.
	if _, err := runCLIStrip(t, "stop", a); err == nil {
		t.Fatal("stop accepted runtime without registered managed children")
	}
	if _, exists, err := state.LoadState(pb); err != nil || !exists {
		t.Fatalf("project B state changed: exists=%v err=%v", exists, err)
	}
	if _, exists, err := state.LoadState(pa); err != nil || !exists {
		t.Fatalf("unsafe stop removed state A: exists=%v err=%v", exists, err)
	}
}

func TestDoctorVerifyCLI(t *testing.T) {
	root := t.TempDir()
	for _, command := range []string{"doctor", "verify"} {
		for _, args := range [][]string{{"--root", root, command}, {"--root", root, command, "production"}, {"--root", root, command, "--json"}, {"--root", root, command, "development"}} {
			out, err := runCLIStrip(t, args...)
			if err == nil {
				t.Errorf("%v unexpectedly passed missing prerequisites", args)
			}
			if args[len(args)-1] == "--json" {
				var report map[string]any
				if decodeErr := json.Unmarshal([]byte(out), &report); decodeErr != nil {
					t.Fatalf("%v output not JSON: %q (%v)", args, out, decodeErr)
				}
			}
		}
	}
	for _, args := range [][]string{
		{"doctor", "development", "production"}, {"verify", "development", "production"},
		{"doctor", "--json", "--json"}, {"verify", "--json", "--json"},
		{"doctor", "--bogus"}, {"verify", "--bogus"},
	} {
		if _, err := runCLI(t, append([]string{"--root", root}, args...)...); err == nil {
			t.Errorf("accepted invalid args %v", args)
		}
	}
	out, err := runCLIStrip(t, "--root", root, "verify", "--json")
	if err == nil {
		t.Fatal("verify accepted failed prerequisites")
	}
	var report map[string]any
	if decodeErr := json.Unmarshal([]byte(out), &report); decodeErr != nil {
		t.Fatalf("failure output not JSON only: %q (%v)", out, decodeErr)
	}
	if report["ok"] != false {
		t.Fatalf("failure report missing ok=false: %v", report)
	}
}

func TestRootPathValidation(t *testing.T) {
	if _, err := runCLI(t, "--root", "/definitely/not/here", "doctor"); err == nil {
		t.Fatal("root tidak dapat diakses harus ditolak")
	}
	if _, err := runCLI(t, "totally-unknown-command"); err == nil {
		t.Fatal("command asing harus ditolak")
	}
}

func assertMonitorRow(t *testing.T, output, root string, port int) {
	t.Helper()
	rows := strings.Split(strings.TrimSpace(output), "\n")
	if len(rows) != 1 {
		t.Fatalf("expected one row, got %q", output)
	}
	fields := strings.Split(rows[0], "\t")
	want := []string{root, strconv.Itoa(os.Getpid())}
	if port != 0 {
		want = append(want, strconv.Itoa(port))
	}
	if len(fields) != len(want) {
		t.Fatalf("unexpected monitor row: %q (want %d kolom)", output, len(want))
	}
	for i, w := range want {
		if fields[i] != w {
			t.Fatalf("unexpected monitor row: %q (kolom %d = %q, want %q)", output, i, fields[i], w)
		}
	}
}
