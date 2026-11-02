package storage

import (
	"strings"
	"sync"
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
func (m *KeyLatchManager) LockKey(tableName, key string) func() {
	if m == nil {
		return func() {}
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
