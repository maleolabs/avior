package runtime

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// lookSh mencari `sh` untuk test orchestrator proses nyata. Fail-closed di
// Windows: sinyal Unix tidak portabel (coverage menyusul dengan job object).
func lookSh(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("binary `sh` tidak ditemukan; test orchestrator butuh proses nyata")
	}
	return p
}

func TestPlanRespectsQueueFlagSemantics(t *testing.T) {
	if got := PlanServices(Options{Queue: nil}, Plan{Horizon: true}); len(got) != 2 {
		t.Fatalf("unset flag + evidence Horizon = %d service, want 2", len(got))
	}
	if got := PlanServices(Options{Queue: nil}, Plan{Horizon: false}); len(got) != 1 {
		t.Fatalf("unset flag tanpa evidence = %d service, want 1", len(got))
	}
	if got := PlanServices(QueueOff(Options{}), Plan{Horizon: true}); len(got) != 1 {
		t.Fatalf("--queue=false menang atas evidence = %d service, want 1 (H1)", len(got))
	}
	if got := PlanServices(QueueOn(Options{}), Plan{Horizon: false}); len(got) != 2 {
		t.Fatalf("--queue=true menang atas evidence = %d service, want 2", len(got))
	}
}

// Run: satu child exit instan → runtime berhenti dengan error yang menyebut
// child tersebut (semantik legacy "runtime berhenti").
func TestRunStopsWhenChildExits(t *testing.T) {
	sh := lookSh(t)
	err := Run(context.Background(), t.TempDir(), []Service{
		{Name: sh, Args: []string{"-c", "exit 7"}},
	})
	if err == nil {
		t.Fatal("child exit instan harus mengakhiri runtime dengan error")
	}
	if !strings.Contains(err.Error(), "berhenti") {
		t.Fatalf("error tidak menyebut runtime berhenti: %v", err)
	}
}

// Run: context dibatalkan → teardown graceful, return nil.
func TestRunStopsOnContextCancel(t *testing.T) {
	sh := lookSh(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, t.TempDir(), []Service{
			{Name: sh, Args: []string{"-c", "trap '' TERM; sleep 30"}},
		})
	}()
	time.Sleep(100 * time.Millisecond) // beri waktu child trap TERM
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("cancel context harus return nil, dapat %v", err)
		}
	case <-time.After(terminateGrace + terminateForce + 3*time.Second):
		t.Fatal("Run tidak selesai setelah context cancel")
	}
}

// REGRESI QA: child KEDUA exit instan sementara child PERTAMA masih tidur —
// Run harus tetap return (loop sekuensial atas Reaped() akan block selamanya
// di child[0]) dan men-teardown child[0].
func TestRunReturnsWhenLaterChildExitsFirst(t *testing.T) {
	sh := lookSh(t)
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), t.TempDir(), []Service{
			{Name: sh, Args: []string{"-c", "trap '' TERM; sleep 30"}}, // child 0 hidup
			{Name: sh, Args: []string{"-c", "exit 7"}},                 // child 1 exit instan
		})
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("child[1] exit harus mengakhiri runtime dengan error")
		}
		if !strings.Contains(err.Error(), "berhenti") {
			t.Fatalf("error tidak menyebut runtime berhenti: %v", err)
		}
	case <-time.After(terminateGrace + terminateForce + 3*time.Second):
		t.Fatal("Run block pada child[0].Reaped() padahal child[1] sudah exit — supervisi tidak multiplexed")
	}
}
