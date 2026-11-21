//go:build windows

package wiredtiger

import (
	"testing"
)

func TestEnsureWiredTigerDLL(t *testing.T) {
	err := EnsureWiredTigerDLL()
	if err != nil {
		t.Fatalf("EnsureWiredTigerDLL failed: %v", err)
	}
}
