package wiredtiger

import (
	"strings"
	"testing"
)

func TestWiredTiger_BuildWiredTigerOpenConfig(t *testing.T) {
	// 1. Verify default values
	defCfg := BuildWiredTigerOpenConfig("", 0)
	if !strings.Contains(defCfg, "cache_size=500M") {
		t.Fatalf("expected default cache_size=500M, got %q", defCfg)
	}
	if !strings.Contains(defCfg, "session_max=2000") {
		t.Fatalf("expected default session_max=2000, got %q", defCfg)
	}
	if !strings.Contains(defCfg, "log=(enabled=true)") {
		t.Fatalf("expected log=(enabled=true), got %q", defCfg)
	}
	if strings.Contains(defCfg, "transaction_sync") {
		t.Fatalf("CRITICAL INVARIANT VIOLATION: transaction_sync must not appear in %q", defCfg)
	}

	// 2. Custom parameters
	customCfg := BuildWiredTigerOpenConfig("2G", 5000)
	if !strings.Contains(customCfg, "cache_size=2G") {
		t.Fatalf("expected cache_size=2G, got %q", customCfg)
	}
	if !strings.Contains(customCfg, "session_max=5000") {
		t.Fatalf("expected session_max=5000, got %q", customCfg)
	}
	if !strings.Contains(customCfg, "log=(enabled=true)") {
		t.Fatalf("expected log=(enabled=true), got %q", customCfg)
	}
	if strings.Contains(customCfg, "transaction_sync") {
		t.Fatalf("CRITICAL INVARIANT VIOLATION: transaction_sync must not appear in %q", customCfg)
	}

	// 3. Panic invariant if transaction_sync is injected into cache size string
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected panic on transaction_sync injection, but did not panic")
		}
	}()
	_ = BuildWiredTigerOpenConfig("500M,transaction_sync=(enabled=true)", 1000)
}
