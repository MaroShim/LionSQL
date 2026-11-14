package storage

import (
	"encoding/binary"
	"sync/atomic"
)

var globalDBTS atomic.Uint64

func init() {
	// Standard starting baseline for SQL Server rowversion/timestamp (0x00000000000007D0 = 2000)
	globalDBTS.Store(2000)
}

// GetCurrentDBTS returns the current database 8-byte rowversion/timestamp counter as a big-endian byte slice.
func GetCurrentDBTS() []byte {
	v := globalDBTS.Load()
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, v)
	return buf
}

// AdvanceDBTS monotonically increments and returns the new global rowversion/timestamp counter.
func AdvanceDBTS() uint64 {
	return globalDBTS.Add(1)
}

// NextRowVersion atomically increments the global rowversion counter and returns the new 8-byte big-endian binary.
func NextRowVersion() []byte {
	v := AdvanceDBTS()
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, v)
	return buf
}
