package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maleolabs/avior/internal/state"
)

func TestMonitorAndProjectScopedCommands(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a, b := filepath.Join(t.TempDir(), "project-a"), filepath.Join(t.TempDir(), "project-b")
	for _, root := range []string{a, b} {
		if err := os.MkdirAll(root, 0700); err != nil {
			t.Fatal(err)
		}
	}
	pa, _, err := state.Paths(a)
	if err != nil {
		t.Fatal(err)
	}
	pb, _, err := state.Paths(b)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ path, root string }{{pa, a}, {pb, b}} {
		if err := state.SaveState(item.path, state.RuntimeState{ProjectRoot: item.root, MainPID: state.Process{PID: os.Getpid(), Command: os.Args[0], StartedAt: time.Now()}}); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := run([]string{"monitor"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), a) || !strings.Contains(out.String(), b) {
		t.Fatalf("monitor omitted projects: %q", out.String())
	}
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
