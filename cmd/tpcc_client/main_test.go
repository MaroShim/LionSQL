package main

import (
	"sync"
	"testing"
	"time"
)

func TestCalcPercentile(t *testing.T) {
	durs := []time.Duration{
		1 * time.Millisecond,
		2 * time.Millisecond,
		3 * time.Millisecond,
		4 * time.Millisecond,
		5 * time.Millisecond,
	}

	p50 := calcPercentile(durs, 0.50)
	if p50 != 3*time.Millisecond {
		t.Errorf("expected p50 to be 3ms, got %v", p50)
	}

	p99 := calcPercentile(durs, 0.99)
	if p99 != 5*time.Millisecond {
		t.Errorf("expected p99 to be 5ms, got %v", p99)
	}

	// Empty slice test
	if pEmpty := calcPercentile(nil, 0.50); pEmpty != 0 {
		t.Errorf("expected empty percentile to be 0, got %v", pEmpty)
	}
}

func TestTxStatsConcurrency(t *testing.T) {
	var s txStats
	var wg sync.WaitGroup

	numGoroutines := 16
	recordsPerGoroutine := 500

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for j := 0; j < recordsPerGoroutine; j++ {
				ok := (j%10 != 0) // 90% success, 10% abort
				retries := 0
				if !ok {
					retries = 2
				}
				s.record(time.Duration(j)*time.Microsecond, ok, retries)
			}
		}(i)
	}

	wg.Wait()

	expectedTotal := int64(numGoroutines * recordsPerGoroutine)
	if total := s.count.Load(); total != expectedTotal {
		t.Errorf("expected count %d, got %d", expectedTotal, total)
	}

	expectedAborts := int64(numGoroutines * (recordsPerGoroutine / 10))
	if aborts := s.abort.Load(); aborts != expectedAborts {
		t.Errorf("expected aborts %d, got %d", expectedAborts, aborts)
	}

	expectedSuccess := expectedTotal - expectedAborts
	if succ := s.success.Load(); succ != expectedSuccess {
		t.Errorf("expected success %d, got %d", expectedSuccess, succ)
	}

	s.mu.Lock()
	latencyLen := len(s.latencies)
	s.mu.Unlock()

	if latencyLen != int(expectedTotal) {
		t.Errorf("expected %d latencies recorded, got %d", expectedTotal, latencyLen)
	}
}
