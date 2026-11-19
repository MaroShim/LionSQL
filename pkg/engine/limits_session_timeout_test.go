package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func TestEngine_IdleInTxTimeout_SessionIsolation(t *testing.T) {
	eng := memengine.New()
	if err := eng.Open(""); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()
	cat := catalog.New(eng)

	cfg := DefaultConfig()
	cfg.ClientSessionIdleTTL = 10 * time.Minute // global default is large (10m)
	cfg.MaxConnections = 250
	coord := NewCoordinator(cat, eng, cfg)
	defer coord.Close()

	// 1. Verify SHOW max_connections reflects dynamic configured value
	res, err := coord.ExecuteSQL("SHOW max_connections;")
	if err != nil || len(res.Rows) == 0 {
		t.Fatalf("SHOW max_connections failed: %v", err)
	}
	if got := res.Rows[0]["max_connections"]; got != "250" {
		t.Fatalf("expected max_connections = '250', got %v", got)
	}

	// 2. Set up session 1 with explicit low timeout (50ms)
	_, err = coord.ExecuteSessionSQL("sess_short", "SET idle_in_transaction_session_timeout = '50ms';")
	if err != nil {
		t.Fatalf("SET idle_in_tx on sess_short failed: %v", err)
	}
	s1 := coord.GetSession("sess_short")
	if s1 == nil {
		t.Fatalf("expected s1 non-nil")
	}

	// Verify SHOW idle_in_transaction_session_timeout on sess_short
	resS1, err := coord.ExecuteSessionSQL("sess_short", "SHOW idle_in_transaction_session_timeout;")
	if err != nil || len(resS1.Rows) == 0 {
		t.Fatalf("SHOW on sess_short failed: %v", err)
	}
	if got := resS1.Rows[0]["idle_in_transaction_session_timeout"]; got != "50ms" {
		t.Fatalf("expected sess_short timeout '50ms', got %v", got)
	}

	// 3. Set up session 2 without override (should use default 10min)
	resS2, err := coord.ExecuteSessionSQL("sess_default", "SHOW idle_in_transaction_session_timeout;")
	if err != nil || len(resS2.Rows) == 0 {
		t.Fatalf("SHOW on sess_default failed: %v", err)
	}
	s2 := coord.GetSession("sess_default")
	if s2 == nil {
		t.Fatalf("expected s2 non-nil")
	}
	if got := resS2.Rows[0]["idle_in_transaction_session_timeout"]; got != "10min" {
		t.Fatalf("expected sess_default timeout '10min', got %v", got)
	}

	// 4. Begin transaction on both sessions
	if err := coord.BeginSessionTx("sess_short", storage.TxOptions{}); err != nil {
		t.Fatalf("BeginSessionTx on sess_short failed: %v", err)
	}
	if err := coord.BeginSessionTx("sess_default", storage.TxOptions{}); err != nil {
		t.Fatalf("BeginSessionTx on sess_default failed: %v", err)
	}

	if s1.Tx() == nil {
		t.Fatalf("expected s1 tx active")
	}
	if s2.Tx() == nil {
		t.Fatalf("expected s2 tx active")
	}

	// Fast-forward session 1 lastUsedAt to simulate 60ms idle
	s1.Mu.Lock()
	s1.lastUsedAt = time.Now().Add(-60 * time.Millisecond)
	s1.Mu.Unlock()

	// Session 2 is also idle for 60ms, which is far below 10min
	s2.Mu.Lock()
	s2.lastUsedAt = time.Now().Add(-60 * time.Millisecond)
	s2.Mu.Unlock()

	// Trigger reaper with global default (10m)
	reaped := coord.ReapIdleClientSessions(coord.ClientSessionIdleTTL())
	if reaped != 1 {
		t.Fatalf("expected exactly 1 session reaped, got %d", reaped)
	}

	// Session 1 tx must be forcibly rolled back due to per-session 50ms timeout
	if s1.Tx() != nil {
		t.Fatalf("expected s1 tx to be rolled back after exceeding 50ms per-session timeout")
	}

	// Session 2 tx must STILL BE ACTIVE (10m default not reached)
	if s2.Tx() == nil {
		t.Fatalf("expected s2 tx to remain active as it did not exceed 10min default threshold")
	}

	// Clean up s2
	_ = coord.RollbackSessionTx("sess_default")
	_ = context.Background()
	_ = strings.Contains
}
