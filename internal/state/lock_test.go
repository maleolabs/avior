package state

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestLockSerializesConcurrent: dua proses/goroutine yang mengambil lock
// secara bersamaan akan diserialisasi (flock pada file yang sama).
func TestLockSerializesConcurrent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	order := []string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			unlock, err := LockTrust()
			if err != nil {
				t.Errorf("LockTrust error: %v", err)
				return
			}
			defer unlock()
			mu.Lock()
			order = append(order, fmt.Sprintf("in-%d", n))
			mu.Unlock()
			time.Sleep(20 * time.Millisecond)
			mu.Lock()
			order = append(order, fmt.Sprintf("out-%d", n))
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	// Setiap goroutine harus keluar sebelum yang lain masuk.
	for i := 0; i+3 < len(order); i += 2 {
		if !strings.HasPrefix(order[i+1], "out-") {
			t.Fatalf("lock tidak menserialisasi: %v", order)
		}
	}
}
