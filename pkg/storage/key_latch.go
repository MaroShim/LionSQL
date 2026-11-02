package storage

import (
	"strings"
	"sync"
	"time"
)

const (
	// DefaultKeyLatchStripes is the number of striped mutexes for in-memory key latching.
	// 2048 stripes ensure sub-0.05% hash collision probability across concurrent transactions.
	DefaultKeyLatchStripes = 2048
)

// KeyLatchManager provides in-memory key-striped mutexes to serialize concurrent updates
// on hotspot rows, preventing OCC/MVCC write-write conflict abort storms (PERF-HOTSPOT-LATCH-1).
type KeyLatchManager struct {
	stripes [DefaultKeyLatchStripes]sync.Mutex
}

var globalKeyLatchManager = &KeyLatchManager{}

// GlobalKeyLatch returns the singleton KeyLatchManager.
func GlobalKeyLatch() *KeyLatchManager {
	return globalKeyLatchManager
}

// LockKey acquires the striped mutex for table + key, returning an idempotent unlock function.
// It uses LockKeyWithTimeout with a 25ms safety timeout to prevent permanent deadlocks,
// falling back to a blocking Lock() if needed for backwards compatibility.
func (m *KeyLatchManager) LockKey(tableName, key string) func() {
	if m == nil {
		return func() {}
	}
	if unlock, ok := m.LockKeyWithTimeout(tableName, key, 25*time.Millisecond); ok {
		return unlock
	}
	idx := hashKeyLatch(tableName, key)
	m.stripes[idx].Lock()
	var once sync.Once
	return func() {
		once.Do(func() {
			m.stripes[idx].Unlock()
		})
	}
}

// LockKeyWithTimeout attempts to acquire the striped mutex within timeout.
// If the timeout expires without acquiring the latch, it returns (nil, false) to prevent deadlocks.
func (m *KeyLatchManager) LockKeyWithTimeout(tableName, key string, timeout time.Duration) (func(), bool) {
	if m == nil {
		return func() {}, true
	}
	idx := hashKeyLatch(tableName, key)
	if m.stripes[idx].TryLock() {
		var once sync.Once
		return func() {
			once.Do(func() {
				m.stripes[idx].Unlock()
			})
		}, true
	}

	if timeout <= 0 {
		return nil, false
	}

	deadline := time.Now().Add(timeout)
	backoff := 50 * time.Microsecond
	for {
		time.Sleep(backoff)
		if m.stripes[idx].TryLock() {
			var once sync.Once
			return func() {
				once.Do(func() {
					m.stripes[idx].Unlock()
				})
			}, true
		}
		if time.Now().After(deadline) {
			return nil, false
		}
		if backoff < 500*time.Microsecond {
			backoff *= 2
		}
	}
}

func hashKeyLatch(table, key string) uint32 {
	table = strings.ToLower(strings.Trim(table, "[]\"`"))
	var h uint32 = 2166136261
	for i := 0; i < len(table); i++ {
		h ^= uint32(table[i])
		h *= 16777619
	}
	h ^= ':'
	h *= 16777619
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= 16777619
	}
	return h % DefaultKeyLatchStripes
}
