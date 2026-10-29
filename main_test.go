package main

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
)

func TestDiagnosticJSONFailureAndSuccess(t *testing.T) {
	root := t.TempDir()
	var out bytes.Buffer
	err := run([]string{"verify", "production", "--json", root}, &out)
	if err == nil {
		t.Fatal("verify accepted missing requirements")
	}
	var failed struct {
		OK bool `json:"ok"`
	}
	if json.Unmarshal(out.Bytes(), &failed) != nil || failed.OK {
		t.Fatalf("failure JSON = %q", out.String())
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
	out.Reset()
	if err := run([]string{"verify", "production", "--json", root}, &out); err != nil {
		t.Fatal(err)
	}
	var passed struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(out.Bytes(), &passed); err != nil || !passed.OK {
		t.Fatalf("success JSON = %q, err=%v", out.String(), err)
	}
}

func TestDiagnosticJSONFalseOnCLIErrors(t *testing.T) {
	for _, args := range [][]string{{"verify", "--json", "--json"}, {"verify", "--json", "--bad"}, {"verify", "--json", "/path/that/does/not/exist"}} {
		var out bytes.Buffer
		if err := run(args, &out); err == nil {
			t.Errorf("run(%v) succeeded", args)
			continue
		}
		if out.Len() != 0 {
			t.Errorf("run(%v) wrote JSON on argument/path failure: %q", args, out.String())
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
	if err := state.SaveState(pa, state.RuntimeState{ProjectRoot: a, MainPID: state.Process{PID: os.Getpid(), Command: os.Args[0], StartedAt: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	if err := state.SaveState(pb, state.RuntimeState{ProjectRoot: b, MainPID: state.Process{PID: os.Getpid(), Command: os.Args[0], StartedAt: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run([]string{"monitor", a}, &out); err != nil {
		t.Fatal(err)
	}
	assertMonitorRow(t, out.String(), a)
	out.Reset()
	if err := run([]string{"monitor", b}, &out); err != nil {
		t.Fatal(err)
	}
	assertMonitorRow(t, out.String(), b)
	if err := run([]string{"attach", a}, &out); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"attach", filepath.Join(t.TempDir(), "unknown")}, &out); err == nil {
		t.Fatal("attach fell back to another project")
	}
	if err := run([]string{"stop", a}, &out); err == nil {
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
		for _, args := range [][]string{{command}, {command, "production"}, {command, "--json"}, {command, "development", root}} {
			var out bytes.Buffer
			err := run(args, &out)
			if err == nil {
				t.Errorf("%v unexpectedly passed missing prerequisites", args)
			}
			if args[len(args)-1] == "--json" {
				var report map[string]any
				if decodeErr := json.Unmarshal(out.Bytes(), &report); decodeErr != nil {
					t.Fatalf("%v output not JSON: %q (%v)", args, out.String(), decodeErr)
				}
			}
		}
	}
	for _, args := range [][]string{
		{"verify"}, {"verify", "production"}, {"verify", root}, {"verify", "production", root},
	} {
		var out bytes.Buffer
		if err := run(args, &out); err == nil {
			t.Errorf("%v unexpectedly passed missing prerequisites", args)
		}
	}
	for _, args := range [][]string{
		{"doctor", "development", "production"}, {"verify", "development", "production"},
		{"doctor", "--json", "--json"}, {"verify", "--json", "--json"},
		{"doctor", filepath.Join(root, "missing")}, {"verify", filepath.Join(root, "missing")},
		{"doctor", "--bogus"}, {"verify", "--bogus"},
	} {
		if err := run(args, &bytes.Buffer{}); err == nil {
			t.Errorf("accepted invalid args %v", args)
		}
	}
	var out bytes.Buffer
	err := run([]string{"verify", "--json", root}, &out)
	if err == nil {
		t.Fatal("verify accepted failed prerequisites")
	}
	var report map[string]any
	if decodeErr := json.Unmarshal(out.Bytes(), &report); decodeErr != nil {
		t.Fatalf("failure output not JSON only: %q (%v)", out.String(), decodeErr)
	}
	if report["ok"] != false {
		t.Fatalf("failure report missing ok=false: %v", report)
	}
}

func assertMonitorRow(t *testing.T, output, root string) {
	t.Helper()
	rows := strings.Split(strings.TrimSpace(output), "\n")
	if len(rows) != 1 {
		t.Fatalf("expected one row, got %q", output)
	}
	fields := strings.Split(rows[0], "\t")
	if len(fields) != 3 || fields[0] != root || fields[1] != strconv.Itoa(os.Getpid()) || fields[2] != "0" {
		t.Fatalf("unexpected monitor row: %q", output)
	}
}
