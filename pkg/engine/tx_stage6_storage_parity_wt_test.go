//go:build cgo && wiredtiger

package engine_test

import (
	"testing"

	"lionsql/pkg/storage/wiredtiger"
)

// TestTxRigor_P6_1_StorageBackendParity_WiredTiger verifies that WiredTiger MVCC exhibits
// 100% identical transaction behavior to In-Memory OCC across rollback, savepoint,
// non-blocking read, and write-write conflict scenarios.
func TestTxRigor_P6_1_StorageBackendParity_WiredTiger(t *testing.T) {
	dir := t.TempDir()
	eng := wiredtiger.New()
	if err := eng.Open(dir); err != nil {
		t.Fatalf("wiredtiger.Open failed: %v", err)
	}
	defer eng.Close()

	runTxParityTestSuite(t, eng)
}
