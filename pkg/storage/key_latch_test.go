package storage

import (
	"sync"
	"testing"
	"time"
)

func TestKeyLatchBasic(t *testing.T) {
	mgr := GlobalKeyLatch()
	unlock := mgr.LockKey("users", "101")
	unlock()
	// Idempotent unlock shouldn't panic
	unlock()
}

func TestKeyLatchMutualExclusion(t *testing.T) {
	mgr := GlobalKeyLatch()
	var (
		mu       sync.Mutex
		order    []int
		executed bool
	)

	unlock1 := mgr.LockKey("inventories", "42")

	chStarted := make(chan struct{})
	chDone := make(chan struct{})

	go func() {
		close(chStarted)
		unlock2 := mgr.LockKey("inventories", "42")
		defer unlock2()

		mu.Lock()
		order = append(order, 2)
		executed = true
		mu.Unlock()
		close(chDone)
	}()

	<-chStarted
	// Small deterministic wait to ensure goroutine is waiting on LockKey
	time.Sleep(20 * time.Millisecond)

	mu.Lock()
	order = append(order, 1)
	mu.Unlock()

	unlock1()

	<-chDone

	mu.Lock()
	defer mu.Unlock()
	if !executed {
		t.Fatalf("goroutine did not execute")
	}
	if len(order) != 2 || order[0] != 1 || order[1] != 2 {
		t.Fatalf("unexpected order: %v", order)
	}
}

func TestKeyLatchDifferentKeysParallel(t *testing.T) {
	mgr := GlobalKeyLatch()
	unlock1 := mgr.LockKey("inventories", "1")
	defer unlock1()

	chDone := make(chan struct{})
	go func() {
		// Key 2 should acquire latch independently without waiting for Key 1
		unlock2 := mgr.LockKey("inventories", "2")
		unlock2()
		close(chDone)
	}()

	select {
	case <-chDone:
		// success
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("different keys deadlocked or blocked each other")
	}
}
