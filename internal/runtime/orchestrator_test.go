package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maleolabs/avior/internal/state"
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

func TestConcurrentProjectsCleanupIsIsolated(t *testing.T) {
	sh := lookSh(t)
	rootA, rootB := filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")
	if err := os.MkdirAll(rootA, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(rootB, 0o700); err != nil {
		t.Fatal(err)
	}
	ctxA, cancelA := context.WithCancel(context.Background())
	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelB()
	start := func(ctx context.Context, root string) <-chan error {
		done := make(chan error, 1)
		go func() { done <- Run(ctx, root, []Service{{Name: sh, Args: []string{"-c", "sleep 30"}}}) }()
		return done
	}
	doneA, doneB := start(ctxA, rootA), start(ctxB, rootB)
	_, registryA, err := state.Paths(rootA)
	if err != nil {
		t.Fatal(err)
	}
	stateB, registryB, err := state.Paths(rootB)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, errA := os.Stat(registryA); errA == nil {
			if _, errB := os.Stat(registryB); errB == nil {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(registryA); err != nil {
		t.Fatalf("project A registry not created: %v", err)
	}
	if _, err := os.Stat(registryB); err != nil {
		t.Fatalf("project B registry not created: %v", err)
	}
	cancelA()
	select {
	case err := <-doneA:
		if err != nil {
			t.Fatalf("A cancel: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("A did not stop")
	}
	if _, err := os.Stat(registryB); err != nil {
		t.Fatalf("cleanup A removed project B registry: %v", err)
	}
	select {
	case err := <-doneB:
		t.Fatalf("project B stopped with A: %v", err)
	default:
	}
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		if _, exists, err := state.LoadState(stateB); err == nil && exists {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, exists, err := state.LoadState(stateB); err != nil || !exists {
		t.Fatalf("project B runtime state lost: exists=%v err=%v", exists, err)
	}
	cancelB()
	select {
	case err := <-doneB:
		if err != nil {
			t.Fatalf("B cancel: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("B did not stop")
	}
}

func TestConcurrentSameProjectCleanupPreservesOtherRuntimeState(t *testing.T) {
	sh := lookSh(t)
	root := t.TempDir()
	statePath, _, err := state.Paths(root)
	if err != nil {
		t.Fatal(err)
	}
	start := func() (context.CancelFunc, <-chan error) {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- Run(ctx, root, []Service{{Name: sh, Args: []string{"-c", "sleep 30"}}}) }()
		return cancel, done
	}
	cancelA, doneA := start()
	cancelB, doneB := start()
	defer cancelB()
	deadline := time.Now().Add(5 * time.Second)
	var first state.RuntimeState
	for time.Now().Before(deadline) {
		first, _, err = state.LoadState(statePath)
		if err == nil && first.MainPID.PID != 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if first.MainPID.PID == 0 {
		t.Fatal("first runtime state not created")
	}
	var second state.RuntimeState
	for time.Now().Before(deadline) {
		second, _, err = state.LoadState(statePath)
		if err == nil && second.MainPID.PID != 0 && second.MainPID != first.MainPID {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if second.MainPID == first.MainPID {
		t.Fatal("second runtime did not replace state")
	}
	cancelA()
	select {
	case err := <-doneA:
		if err != nil {
			t.Fatalf("A cancel: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("A did not stop")
	}
	got, exists, err := state.LoadState(statePath)
	if err != nil || !exists || got.MainPID != second.MainPID {
		t.Fatalf("A cleanup removed/changed B state: state=%+v exists=%v err=%v", got, exists, err)
	}
	cancelB()
	select {
	case err := <-doneB:
		if err != nil {
			t.Fatalf("B cancel: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("B did not stop")
	}
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

func TestServiceLifecycleUpdates(t *testing.T) {
	sh := lookSh(t)
	var mu sync.Mutex
	var states []string
	sup, err := Start(context.Background(), StartOptions{Dir: t.TempDir(), Services: []Service{{Name: sh, Args: []string{"-c", "sleep 30"}}}, OnServiceState: func(_, status string) {
		mu.Lock()
		states = append(states, status)
		mu.Unlock()
	}})
	if err != nil {
		t.Fatal(err)
	}
	sup.Stop()
	_ = sup.Wait()
	mu.Lock()
	defer mu.Unlock()
	want := []string{"starting", "running", "exited"}
	if !reflect.DeepEqual(states, want) {
		t.Fatalf("states = %v, want %v", states, want)
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
